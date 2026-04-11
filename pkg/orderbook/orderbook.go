package orderbook

import (
	"bytes"
	"container/list"
	"encoding/binary"
	"errors"
	"fmt"
	"go-match/internal"
	"os"
	"path"
	"sync"
	"time"
	"unsafe"

	"github.com/dolthub/swiss"
	rbt "github.com/emirpasic/gods/trees/redblacktree"
	"github.com/emirpasic/gods/utils"
	"golang.org/x/sys/unix"
)

type Order struct {
	ID           uint64 `json:"id"`
	Price        uint64 `json:"price"`
	Quantity     uint64 `json:"quantity"`
	Side         Side   `json:"side"`
	IsProcessing bool   `json:"isProcessing"`
	SeqID        uint64 `json:"seq_id"`
}

func (order *Order) ToRecord() OrderRecord {
	return OrderRecord{
		ID:           order.ID,
		Price:        order.Price,
		Quantity:     order.Quantity,
		Side:         uint8(order.Side),
		IsProcessing: order.IsProcessing,
	}
}

type OrderRecord struct {
	ID           uint64
	Price        uint64
	Quantity     uint64
	Side         uint8
	IsProcessing bool
	_            [5]byte // align to 32 bytes
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
	mu         sync.Mutex
	instrument string
	bids       *rbt.Tree
	asks       *rbt.Tree
	orderMap   *swiss.Map[uint64, *list.Element]
	snapPath   string
	lastSeqID  uint64
}

func NewOrderBook(instrument string, dir string) *OrderBook {
	ob := &OrderBook{
		instrument: instrument,
		bids:       rbt.NewWith(utils.UInt64Comparator),
		asks:       rbt.NewWith(asksComparator),
		orderMap:   swiss.NewMap[uint64, *list.Element](0),
		snapPath:   path.Join(dir, fmt.Sprintf("snap_%s.bin", instrument)),
		lastSeqID:  0,
	}

	return ob
}

func (ob *OrderBook) GetInstrument() string {
	return ob.instrument
}

func (ob *OrderBook) GetLastSeqID() uint64 {
	return ob.lastSeqID
}

func (ob *OrderBook) AddOrder(order Order) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	if ob.orderMap.Has(order.ID) {
		return fmt.Errorf("order exist: %d", order.ID)
	}

	tree := ob.getTree(order.Side)

	val, found := tree.Get(order.Price)
	if !found {
		val = &PriceLevel{Price: order.Price, Orders: list.New()}
		tree.Put(order.Price, val)
	}

	order.IsProcessing = false

	level := val.(*PriceLevel)
	el := level.Orders.PushBack(&order)

	ob.orderMap.Put(order.ID, el)
	ob.lastSeqID = order.SeqID

	return nil
}

func (ob *OrderBook) CancelOrder(params Order) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	tmp, ok := ob.orderMap.Get(params.ID)
	if !ok {
		return fmt.Errorf("order not found: %d", params.ID)
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
	ob.orderMap.Delete(order.ID)
	ob.lastSeqID = params.SeqID

	return nil
}

func (ob *OrderBook) UpdateOrder(params Order) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	tmp, ok := ob.orderMap.Get(params.ID)
	if !ok {
		return fmt.Errorf("order not found: %d", params.ID)
	}

	currOrder := tmp.Value.(*Order)

	if currOrder.IsProcessing {
		return fmt.Errorf("order is processing: %d", params.ID)
	}

	if currOrder.Price == params.Price && currOrder.Quantity == params.Quantity {
		return nil
	}

	if currOrder.Price == params.Price {
		currOrder.Quantity = params.Quantity
		return nil
	}

	if err := ob.CancelOrder(params); err != nil {
		return err
	}

	if err := ob.AddOrder(Order{
		ID:           currOrder.ID,
		Price:        params.Price,
		Quantity:     params.Quantity,
		Side:         currOrder.Side,
		IsProcessing: currOrder.IsProcessing,
	}); err != nil {
		return err
	}

	ob.lastSeqID = params.SeqID

	return nil
}

func (ob *OrderBook) HandleIOC(order Order) ([]MatchInfo, error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	res, err := ob.handleMatch(order, false, false)
	if err != nil {
		return nil, err
	}

	ob.lastSeqID = order.SeqID

	return res, nil
}

func (ob *OrderBook) HandleFOK(order Order) ([]MatchInfo, error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	filled := ob.handleMockMatch(order, true)
	if !filled {
		return nil, internal.ErrFOKInsufficientLiquidity
	}

	res, err := ob.handleMatch(order, true, false)
	if err != nil {
		return nil, err
	}

	ob.lastSeqID = order.SeqID

	return res, nil
}

func (ob *OrderBook) HandleGTC(order Order) ([]MatchInfo, error) {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	res, err := ob.handleMatch(order, true, true)
	if err != nil {
		return nil, err
	}

	ob.lastSeqID = order.SeqID

	return res, nil
}

func (ob *OrderBook) Snapshot(filePath string) error {
	ob.mu.Lock()
	defer ob.mu.Unlock()

	// 預先分配：1000萬 × 32 bytes = 320MB，在 5 秒內可寫完（NVMe ~3GB/s）
	totalOrders := ob.orderMap.Count()
	bidsCount := countOrders(ob.bids)
	asksCount := countOrders(ob.asks)

	// 使用 bufio.Writer 大幅降低 syscall 次數
	f, err := os.Create(ob.snapPath)
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
		LastSeqID:       ob.lastSeqID,
		TotalOrderCount: uint32(totalOrders),
	}

	*(*SnapshotMetadata)(unsafe.Pointer(&data[0])) = meta

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		offset := bidsOffset
		it := ob.bids.Iterator()
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
		it := ob.asks.Iterator()
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
	if err = unix.Msync(data, unix.MS_SYNC); err != nil {
		return err
	}

	return nil
}

func (ob *OrderBook) Restore() error {
	f, err := os.Open(ob.snapPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
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
	ob.lastSeqID = meta.LastSeqID

	// 預先分配 OrderMap 容量，避免 rehash
	ob.orderMap = swiss.NewMap[uint64, *list.Element](meta.TotalOrderCount)

	recSize := binary.Size(OrderRecord{})
	offset := metaSize

	var currentPrice uint64
	var currentLevel *PriceLevel
	var currentTree *rbt.Tree

	for i := 0; i < int(meta.TotalOrderCount); i++ {
		rec := *(*OrderRecord)(unsafe.Pointer(&data[offset]))
		offset += recSize

		tree := ob.getTree(Side(rec.Side))

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
			Side:     Side(rec.Side),
		}

		el := currentLevel.Orders.PushBack(order)
		ob.orderMap.Put(order.ID, el)
	}

	return nil
}

func (ob *OrderBook) getTree(side Side) *rbt.Tree {
	var tree *rbt.Tree

	if side == SideBuy {
		tree = ob.bids
	} else {
		tree = ob.asks
	}

	return tree
}

func (ob *OrderBook) handleMatch(order Order, isPriceRelated, needAddOrderBook bool) ([]MatchInfo, error) {
	res := make([]MatchInfo, 0, 8)
	now := time.Now().UTC()

	var tree *rbt.Tree
	if order.Side == SideBuy {
		tree = ob.asks
	} else {
		tree = ob.bids
	}

	for order.Quantity > 0 && !tree.Empty() {
		var node *rbt.Node
		node = tree.Left()
		if node == nil {
			break
		}

		bestPrice := node.Key.(uint64)
		if isPriceRelated {
			if order.Side == SideBuy && bestPrice > order.Price {
				break
			} else if order.Side == SideSell && bestPrice < order.Price {
				break
			}
		}

		orders := node.Value.(*PriceLevel).Orders
		el := orders.Front()
		for el != nil {
			tmpOrder := el.Value.(*Order)

			quantity := min(tmpOrder.Quantity, order.Quantity)
			tmpOrder.Quantity -= quantity
			order.Quantity -= quantity

			info := MatchInfo{
				MatchedPrice:    tmpOrder.Price,
				MatchedQuantity: quantity,
				Timestamp:       now,
			}

			if order.Side == SideBuy {
				info.BuyerOrderID = order.ID
				info.SellerOrderID = tmpOrder.ID
			} else {
				info.BuyerOrderID = tmpOrder.ID
				info.SellerOrderID = order.ID
			}

			res = append(res, info)

			if tmpOrder.Quantity == 0 {
				tmp := el
				el = el.Next()
				orders.Remove(tmp)
			}

			if order.Quantity == 0 {
				break
			}
		}

		if orders.Len() == 0 {
			tree.Remove(bestPrice)
		}
	}

	// 剩餘量掛回訂單簿
	if needAddOrderBook && order.Quantity > 0 {
		if err := ob.AddOrder(Order{
			ID:           order.ID,
			Price:        order.Price,
			Quantity:     order.Quantity,
			Side:         order.Side,
			IsProcessing: true,
		}); err != nil {
			return nil, err
		}
	}

	return res, nil
}

func (ob *OrderBook) handleMockMatch(order Order, isPriceRelated bool) bool {
	var tree *rbt.Tree
	if order.Side == SideBuy {
		tree = ob.asks
	} else {
		tree = ob.bids
	}

	for order.Quantity > 0 && !tree.Empty() {
		var node *rbt.Node
		node = tree.Left()
		if node == nil {
			break
		}

		bestPrice := node.Key.(uint64)
		if isPriceRelated {
			if order.Side == SideBuy && bestPrice > order.Price {
				break
			} else if order.Side == SideSell && bestPrice < order.Price {
				break
			}
		}

		orders := node.Value.(*PriceLevel).Orders
		for el := orders.Front(); el != nil; el = el.Next() {
			tmpOrder := el.Value.(*Order)
			tmpQuantity := tmpOrder.Quantity

			quantity := min(tmpQuantity, order.Quantity)
			tmpQuantity -= quantity
			order.Quantity -= quantity

			if order.Quantity == 0 {
				break
			}
		}
	}

	return order.Quantity == 0
}

func asksComparator(a, b interface{}) int {
	a1 := a.(uint64)
	b1 := b.(uint64)
	if a1 < b1 {
		return 1
	} else if a1 > b1 {
		return -1
	}
	return 0
}

func countOrders(tree *rbt.Tree) int {
	total := 0
	it := tree.Iterator()
	for it.Next() {
		total += it.Value().(*PriceLevel).Orders.Len()
	}
	return total
}
