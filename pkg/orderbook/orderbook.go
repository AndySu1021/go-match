package orderbook

import (
	"encoding/json"
	"errors"
	"fmt"
	"go-match/internal"
	"go-match/proto/gen"
	"log/slog"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"sort"
	"time"

	"github.com/dolthub/swiss"
	"google.golang.org/protobuf/proto"
)

func NewOrderBook(basePath string) (*OrderBook, error) {
	content, err := os.ReadFile(path.Join(basePath, "snapshot.bin"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error(fmt.Sprintf("read file error: %s", err))
		return nil, err
	}

	if errors.Is(err, os.ErrNotExist) {
		return &OrderBook{
			Asks:     make([]PriceLevel, 0),
			Bids:     make([]PriceLevel, 0),
			OrderMap: swiss.NewMap[uint64, OrderLocation](0),
		}, nil
	}

	protoOB := &gen.OrderBook{}
	if err = proto.Unmarshal(content, protoOB); err != nil {
		slog.Error(fmt.Sprintf("unmarshal protobuf error: %s", err))
		return nil, err
	}

	ob := &OrderBook{
		Asks:      make([]PriceLevel, len(protoOB.Asks)),
		Bids:      make([]PriceLevel, len(protoOB.Bids)),
		LastSeqID: protoOB.LastSeqId,
	}

	for i := 0; i < len(protoOB.Asks); i++ {
		ob.Asks[i] = convertPbPriceLevel(protoOB.Asks[i])
	}

	for i := 0; i < len(protoOB.Bids); i++ {
		ob.Bids[i] = convertPbPriceLevel(protoOB.Bids[i])
	}

	ob.buildOrderMap(protoOB.OrderSize)

	return ob, nil
}

func (ob *OrderBook) Match(order Order) (MatchResult, []MatchInfo, error) {
	ob.Lock.Lock()
	defer ob.Lock.Unlock()

	var (
		res   MatchResult
		infos []MatchInfo
		err   error
	)

	switch order.Type {
	case OrderTypeLimit:
		if err = ob.addOrder(order); err != nil {
			return MatchResult{}, nil, err
		}
		res = MatchResult{Success: true}
	case OrderTypeIOC:
		res, infos, err = ob.handleIOC(order)
		if err != nil {
			return MatchResult{}, nil, err
		}
	case OrderTypeFOK:
		res, infos, err = ob.handleFOK(order)
		if err != nil {
			return MatchResult{}, nil, err
		}
	case OrderTypeGTC:
		res, infos, err = ob.handleGTC(order)
		if err != nil {
			return MatchResult{}, nil, err
		}
	}

	if ob.shouldPurge() {
		total := uint32(0)

		// handle ask price
		count, levels := purgeLevels(ob.Asks)
		ob.Asks = levels
		total += count

		// handle bid price
		count, levels = purgeLevels(ob.Bids)
		ob.Bids = levels
		total += count

		ob.buildOrderMap(total)
	}

	return res, infos, nil
}

func (ob *OrderBook) buildOrderMap(size uint32) {
	ob.OrderMap = swiss.NewMap[uint64, OrderLocation](size)

	for i, pl := range ob.Asks {
		for j, o := range pl.Orders {
			ob.OrderMap.Put(o.ID, OrderLocation{
				Side:     OrderSideSell,
				LevelPos: i,
				Offset:   j,
			})
		}
	}

	for i, pl := range ob.Bids {
		for j, o := range pl.Orders {
			ob.OrderMap.Put(o.ID, OrderLocation{
				Side:     OrderSideBuy,
				LevelPos: i,
				Offset:   j,
			})
		}
	}
}

func (ob *OrderBook) CancelOrder(orderId uint64) error {
	ob.Lock.Lock()
	defer ob.Lock.Unlock()

	return ob.removeOrder(orderId)
}

func (ob *OrderBook) Snapshot(basePath string) error {
	go func() {
		runtime.GC()
		debug.FreeOSMemory()
	}()

	startTime := time.Now()

	var (
		content []byte
		err     error
	)

	ob.Lock.RLock()
	p := ob.ToProto()
	ob.Lock.RUnlock()

	content, err = proto.Marshal(p)
	if err != nil {
		slog.Error(fmt.Sprintf("marshal snapshot error: %s", err))
		return err
	}

	tmpPath := path.Join(basePath, "snapshot.bin.tmp")
	finalPath := path.Join(basePath, "snapshot.bin")

	if err = os.WriteFile(tmpPath, content, 0644); err != nil {
		slog.Error(fmt.Sprintf("write snapshot error: %s", err))
		return err
	}

	// atomic rename，避免寫到一半系統 crash 影響上一版的 snapshot 檔案
	if err = os.Rename(tmpPath, finalPath); err != nil {
		slog.Error(fmt.Sprintf("rename snapshot error: %s", err))
		return err
	}

	slog.Info(fmt.Sprintf("snapshot latency: %d ms", time.Since(startTime).Milliseconds()))

	return nil
}

func (ob *OrderBook) shouldPurge() bool {
	totalRemoved := 0
	totalOrders := 0
	for _, pl := range ob.Asks {
		totalRemoved += pl.RemovedCount
		totalOrders += len(pl.Orders)
	}
	for _, pl := range ob.Bids {
		totalRemoved += pl.RemovedCount
		totalOrders += len(pl.Orders)
	}

	if totalOrders == 0 {
		return false
	}

	return totalRemoved >= PurgeThresholdOrders ||
		float64(totalRemoved)/float64(totalOrders) >= PurgeThresholdRatio
}

func (ob *OrderBook) String() string {
	tmp, _ := json.Marshal(ob)
	return string(tmp)
}

func (ob *OrderBook) addOrder(order Order) error {
	if order.Quantity == 0 {
		slog.Error(fmt.Sprintf("order quantity is zero: %d", order.ID))
		return nil
	}

	if ob.OrderMap.Has(order.ID) {
		slog.Error(fmt.Sprintf("order exist: %d", order.ID))
		return nil
	}

	var levels []PriceLevel
	if order.Side == OrderSideBuy {
		levels = ob.Bids
	} else {
		levels = ob.Asks
	}

	pos := 0
	if order.Side == OrderSideBuy {
		pos = sort.Search(len(levels), func(i int) bool {
			return levels[i].Price <= order.Price
		})
	} else {
		pos = sort.Search(len(levels), func(i int) bool {
			return levels[i].Price >= order.Price
		})
	}

	var offset int

	levelOrder := LevelOrder{
		ID:        order.ID,
		Quantity:  order.Quantity,
		IsRemoved: false,
	}

	needRebuild := false
	if pos < len(levels) && levels[pos].Price == order.Price {
		offset = len(levels[pos].Orders)
		levels[pos].Orders = append(levels[pos].Orders, levelOrder)
	} else {
		levels = append(levels, PriceLevel{})
		copy(levels[pos+1:], levels[pos:])
		levels[pos] = PriceLevel{
			Price:    order.Price,
			Decimals: order.Decimals,
			Orders:   []LevelOrder{levelOrder},
		}
		offset = 0
		needRebuild = true
	}

	ob.OrderMap.Put(order.ID, OrderLocation{
		Side:     order.Side,
		LevelPos: pos,
		Offset:   offset,
	})

	if order.Side == OrderSideBuy {
		ob.Bids = levels
	} else {
		ob.Asks = levels
	}

	if needRebuild {
		ob.buildOrderMap(uint32(ob.OrderMap.Count()))
	}

	return nil
}

func (ob *OrderBook) removeOrder(orderId uint64) error {
	loc, ok := ob.OrderMap.Get(orderId)
	if !ok {
		slog.Error(fmt.Sprintf("order not found: %d", orderId))
		return nil
	}

	var levels []PriceLevel
	if loc.Side == OrderSideBuy {
		levels = ob.Bids
	} else {
		levels = ob.Asks
	}

	if loc.LevelPos >= len(levels) {
		return internal.ErrPriceLevelNotFound
	}

	// 只標記，不實際刪除
	levels[loc.LevelPos].Orders[loc.Offset].IsRemoved = true
	levels[loc.LevelPos].RemovedCount++

	if loc.Side == OrderSideBuy {
		ob.Bids = levels
	} else {
		ob.Asks = levels
	}

	ob.OrderMap.Delete(orderId)

	return nil
}

func (ob *OrderBook) handleIOC(order Order) (MatchResult, []MatchInfo, error) {
	return ob.handleMatch(order, false, false)
}

func (ob *OrderBook) handleFOK(order Order) (MatchResult, []MatchInfo, error) {
	// 第一階段：確認可用量是否足夠
	available := uint64(0)
	filled := false

	var levels []PriceLevel
	if order.Side == OrderSideBuy {
		levels = ob.Asks
	} else {
		levels = ob.Bids
	}

outer:
	for i := 0; i < len(levels); i++ {
		if levels[i].IsEmpty() {
			continue
		}

		if order.Side == OrderSideBuy {
			if levels[i].Price > order.Price {
				break
			}
		} else {
			if levels[i].Price < order.Price {
				break
			}
		}

		for j := 0; j < len(levels[i].Orders); j++ {
			o := levels[i].Orders[j]
			if o.IsRemoved {
				continue
			}
			available += o.Quantity
			if available >= order.Quantity {
				filled = true
				break outer
			}
		}
	}

	if !filled {
		return MatchResult{Success: false, Error: internal.ErrFOKInsufficientLiquidity}, nil, nil
	}

	return ob.handleMatch(order, true, false)
}

func (ob *OrderBook) handleGTC(order Order) (MatchResult, []MatchInfo, error) {
	return ob.handleMatch(order, true, true)
}

func (ob *OrderBook) handleMatch(order Order, isPriceRelated, needAddOrderBook bool) (MatchResult, []MatchInfo, error) {
	res := make([]MatchInfo, 0)
	toRemove := make([]uint64, 0)

	var levels []PriceLevel
	if order.Side == OrderSideBuy {
		levels = ob.Asks
	} else {
		levels = ob.Bids
	}

	for i := 0; i < len(levels) && order.Quantity > 0; i++ {
		if levels[i].IsEmpty() {
			continue
		}

		if isPriceRelated {
			if order.Side == OrderSideBuy {
				if levels[i].Price > order.Price {
					break
				}
			} else {
				if levels[i].Price < order.Price {
					break
				}
			}
		}

		j := nextActive(levels[i].Orders, 0)
		for order.Quantity > 0 && j != -1 {
			quantity := min(levels[i].Orders[j].Quantity, order.Quantity)
			levels[i].Orders[j].Quantity -= quantity
			order.Quantity -= quantity

			info := MatchInfo{
				MatchedPrice:    levels[i].Price,
				MatchedQuantity: quantity,
				Decimals:        levels[i].Decimals,
				Timestamp:       time.Now().UTC(),
			}

			if order.Side == OrderSideBuy {
				info.BuyerOrderID = order.ID
				info.SellerOrderID = levels[i].Orders[j].ID
			} else {
				info.BuyerOrderID = levels[i].Orders[j].ID
				info.SellerOrderID = order.ID
			}

			res = append(res, info)

			if levels[i].Orders[j].Quantity == 0 {
				toRemove = append(toRemove, levels[i].Orders[j].ID)
			}

			j = nextActive(levels[i].Orders, j+1)
		}
	}

	for i := 0; i < len(toRemove); i++ {
		_ = ob.removeOrder(toRemove[i])
	}

	if order.Side == OrderSideBuy {
		ob.Asks = levels
	} else {
		ob.Bids = levels
	}

	// 剩餘量掛回訂單簿
	if needAddOrderBook && order.Quantity > 0 {
		if err := ob.addOrder(order); err != nil {
			return MatchResult{Success: false, Error: err}, nil, err
		}
	}

	return MatchResult{Success: true}, res, nil
}

func nextActive(orders []LevelOrder, j int) int {
	for j < len(orders) {
		if !orders[j].IsRemoved {
			return j
		}
		j++
	}
	return -1
}

func purgeLevels(levels []PriceLevel) (uint32, []PriceLevel) {
	result := make([]PriceLevel, 0, len(levels))
	count := uint32(0)
	for _, pl := range levels {
		if pl.IsEmpty() {
			continue
		}
		active := make([]LevelOrder, 0, len(pl.Orders)-pl.RemovedCount)
		for _, o := range pl.Orders {
			if !o.IsRemoved {
				active = append(active, o)
				count++
			}
		}
		pl.Orders = active
		pl.RemovedCount = 0
		result = append(result, pl)
	}
	return count, result
}
