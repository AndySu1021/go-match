package orderbook_v2

import (
	"bytes"
	"container/list"
	"encoding/binary"
	"fmt"
	"go-match/types"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/dolthub/swiss"
	rbt "github.com/emirpasic/gods/trees/redblacktree"
	"github.com/emirpasic/gods/utils"
	"golang.org/x/sys/unix"
)

type Order struct {
	ID           uint64          `json:"id"`
	Price        uint64          `json:"price"`
	Quantity     uint64          `json:"quantity"`
	Decimals     uint8           `json:"decimals"`
	Side         types.OrderSide `json:"side"`
	IsProcessing bool
}

func (order *Order) ToRecord() OrderRecord {
	return OrderRecord{
		ID:           order.ID,
		Price:        order.Price,
		Quantity:     order.Quantity,
		Decimals:     order.Decimals,
		Side:         uint8(order.Side),
		IsProcessing: order.IsProcessing,
	}
}

type OrderRecord struct {
	ID           uint64
	Price        uint64
	Quantity     uint64
	Decimals     uint8
	Side         uint8
	IsProcessing bool
	_            [5]byte // 補齊到 32 bytes
}

type PriceLevel struct {
	Price  uint64
	Orders *list.List
}

type SnapshotMetadata struct {
	LastSeqID       uint64
	TotalOrderCount uint32
}

type OrderBook struct {
	mu        sync.Mutex
	Bids      *rbt.Tree
	Asks      *rbt.Tree
	OrderMap  *swiss.Map[uint64, *list.Element]
	LastSeqID uint64
}

func NewOrderBook() *OrderBook {
	ob := &OrderBook{
		OrderMap: swiss.NewMap[uint64, *list.Element](0),
		Bids:     rbt.NewWith(utils.UInt64Comparator),
		Asks:     rbt.NewWith(utils.UInt64Comparator),
	}

	return ob
}

// AddOrder 新增訂單
func (ob *OrderBook) AddOrder(params types.AddOrderParams) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	if ob.OrderMap.Has(params.ID) {
		return fmt.Errorf("order exist: %d", params.ID)
	}

	tree := ob.getTree(params.Side)

	val, found := tree.Get(params.Price)
	if !found {
		val = &PriceLevel{Price: params.Price, Orders: list.New()}
		tree.Put(params.Price, val)
	}

	order := &Order{
		ID:           params.ID,
		Price:        params.Price,
		Quantity:     params.Quantity,
		Decimals:     params.Decimals,
		Side:         params.Side,
		IsProcessing: params.IsProcessing,
	}

	level := val.(*PriceLevel)
	el := level.Orders.PushBack(order)

	ob.OrderMap.Put(order.ID, el)

	return nil
}

// CancelOrder 取消訂單
func (ob *OrderBook) CancelOrder(orderId uint64) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	tmp, ok := ob.OrderMap.Get(orderId)
	if !ok {
		return fmt.Errorf("order not found: %d", orderId)
	}

	order := tmp.Value.(*Order)

	tree := ob.getTree(order.Side)
	level, found := tree.Get(order.Price)
	if !found {
		return fmt.Errorf("level not found: %d", order.Price)
	}

	level.(*PriceLevel).Orders.Remove(tmp)
	if level.(*PriceLevel).Orders.Len() == 0 {
		tree.Remove(order.Price)
	}
	ob.OrderMap.Delete(order.ID)

	return nil
}

// UpdateOrder 修改價格或數量 (僅限未撮合訂單)
func (ob *OrderBook) UpdateOrder(params types.AddOrderParams) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	tmp, ok := ob.OrderMap.Get(params.ID)
	if !ok {
		return fmt.Errorf("order not found: %d", params.ID)
	}

	order := tmp.Value.(*Order)

	if order.IsProcessing {
		return fmt.Errorf("order is processing: %d", params.ID)
	}

	if order.Side == params.Side {
		return fmt.Errorf("order side change is not allowed: %d", params.ID)
	}

	if order.Price == params.Price && order.Quantity == params.Quantity {
		return nil
	}

	// 只改數量
	if order.Price == params.Price {
		order.Quantity = params.Quantity
		return nil
	}

	// 價格改變：必須先 Cancel 再 Add，以確保 PriceLevel 排序與時間優先權 (FIFO)
	if err := ob.CancelOrder(params.ID); err != nil {
		return err
	}

	return ob.AddOrder(params)
}

func (ob *OrderBook) Snapshot(filePath string) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	// 預先分配：1000萬 × 32 bytes = 320MB，在 5 秒內可寫完（NVMe ~3GB/s）
	totalOrders := ob.OrderMap.Count()
	bidsCount := countOrders(ob.Bids)
	asksCount := countOrders(ob.Asks)

	// 使用 bufio.Writer 大幅降低 syscall 次數
	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	headerSize := binary.Size(SnapshotMetadata{})
	recordSize := binary.Size(OrderRecord{})
	bidsOffset := headerSize
	asksOffset := headerSize + bidsCount*recordSize
	totalSize := asksOffset + asksCount*recordSize

	if err = f.Truncate(int64(totalSize)); err != nil {
		return err // 失敗也沒關係，繼續
	}

	// 3. mmap 整個檔案用於寫入
	data, err := unix.Mmap(
		int(f.Fd()), 0, totalSize,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_SHARED,
	)
	if err != nil {
		return err
	}
	defer unix.Munmap(data)

	// 1. 寫 Header
	meta := SnapshotMetadata{
		LastSeqID:       ob.LastSeqID,
		TotalOrderCount: uint32(totalOrders),
	}

	*(*SnapshotMetadata)(unsafe.Pointer(&data[0])) = meta

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		offset := bidsOffset
		it := ob.Bids.Iterator()
		it.End()
		for it.Prev() {
			level := it.Value().(*PriceLevel)
			for el := level.Orders.Front(); el != nil; el = el.Next() {
				order := el.Value.(*Order)
				*(*OrderRecord)(unsafe.Pointer(&data[offset])) = order.ToRecord()
				offset += recordSize
			}
		}
	}()

	go func() {
		defer wg.Done()
		offset := asksOffset
		it := ob.Asks.Iterator()
		for it.Next() {
			level := it.Value().(*PriceLevel)
			for el := level.Orders.Front(); el != nil; el = el.Next() {
				order := el.Value.(*Order)
				*(*OrderRecord)(unsafe.Pointer(&data[offset])) = order.ToRecord()
				offset += recordSize
			}
		}
	}()

	wg.Wait()
	return unix.Msync(data, unix.MS_SYNC)
}

func (ob *OrderBook) Restore(filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	fi, _ := f.Stat()

	// mmap 整個檔案，避免大量 read syscall
	data, err := unix.Mmap(
		int(f.Fd()), 0, int(fi.Size()),
		unix.PROT_READ, unix.MAP_SHARED,
	)
	if err != nil {
		return err
	}
	defer unix.Munmap(data)

	// 讀 Header
	var meta SnapshotMetadata
	metaSize := binary.Size(meta)
	r := bytes.NewReader(data[:metaSize])
	binary.Read(r, binary.LittleEndian, &meta)
	ob.LastSeqID = meta.LastSeqID

	// 預先分配 OrderMap 容量，避免 rehash
	ob.OrderMap = swiss.NewMap[uint64, *list.Element](meta.TotalOrderCount)

	recSize := binary.Size(OrderRecord{})
	offset := metaSize

	var currentPrice uint64
	var currentLevel *PriceLevel
	var currentTree *rbt.Tree

	for i := 0; i < int(meta.TotalOrderCount); i++ {
		rec := *(*OrderRecord)(unsafe.Pointer(&data[offset]))
		offset += recSize

		tree := ob.getTree(types.OrderSide(rec.Side))

		// 同一個 price level 連續出現，不需要重複 tree.Get/Put
		if rec.Price != currentPrice || tree != currentTree {
			currentPrice = rec.Price
			currentTree = tree
			currentLevel = &PriceLevel{Price: rec.Price, Orders: list.New()}
			tree.Put(rec.Price, currentLevel)
		}

		order := &Order{
			ID:       rec.ID,
			Price:    rec.Price,
			Quantity: rec.Quantity,
			Decimals: rec.Decimals,
			Side:     types.OrderSide(rec.Side),
		}

		el := currentLevel.Orders.PushBack(order)
		ob.OrderMap.Put(order.ID, el)
	}

	return nil
}

func (ob *OrderBook) getTree(side types.OrderSide) *rbt.Tree {
	var tree *rbt.Tree

	if side == types.OrderSideBuy {
		tree = ob.Bids
	} else {
		tree = ob.Asks
	}

	return tree
}

func countOrders(tree *rbt.Tree) int {
	start := time.Now()
	total := 0
	it := tree.Iterator()
	for it.Next() {
		total += it.Value().(*PriceLevel).Orders.Len()
	}
	fmt.Printf("countOrders took %v\n", time.Since(start))
	return total
}
