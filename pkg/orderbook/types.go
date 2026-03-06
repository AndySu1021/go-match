package orderbook

import (
	"encoding/json"
	"go-match/proto/gen"
	"sync"
	"time"

	"github.com/dolthub/swiss"
)

type OrderType uint8

const (
	OrderTypeUnknown OrderType = iota
	OrderTypeLimit
	OrderTypeFOK
	OrderTypeIOC // 市價單，未完全成交剩下的取消
	OrderTypeGTC
)

type OrderSide uint8

const (
	OrderSideUnknown OrderSide = iota
	OrderSideBuy
	OrderSideSell
)

type OrderAction uint8

const (
	OrderActionUnknown OrderAction = iota
	OrderActionNormal
	OrderActionCancel
)

const (
	PurgeThresholdOrders = 1000 // 累積取消 1000 筆觸發
	PurgeThresholdRatio  = 0.3  // 取消比例超過 30% 觸發
)

type OrderBook struct {
	Lock      sync.RWMutex                      `json:"-"`
	Asks      []PriceLevel                      `json:"asks"`
	Bids      []PriceLevel                      `json:"bids"`
	OrderMap  *swiss.Map[uint64, OrderLocation] `json:"order_map"`
	LastSeqID uint64                            `json:"last_seq_id"`
}

func (ob OrderBook) MarshalJSON() ([]byte, error) {
	tmp := struct {
		Asks      []PriceLevel             `json:"asks"`
		Bids      []PriceLevel             `json:"bids"`
		OrderMap  map[uint64]OrderLocation `json:"order_map"`
		LastSeqID uint64                   `json:"last_seq_id"`
	}{
		Asks:      ob.Asks,
		Bids:      ob.Bids,
		OrderMap:  make(map[uint64]OrderLocation, ob.OrderMap.Count()),
		LastSeqID: ob.LastSeqID,
	}

	ob.OrderMap.Iter(func(k uint64, v OrderLocation) (stop bool) {
		tmp.OrderMap[k] = v
		return false
	})

	return json.Marshal(tmp)
}

func (ob *OrderBook) ToProto() *gen.OrderBook {
	res := &gen.OrderBook{
		Asks:      make([]*gen.PriceLevel, len(ob.Asks)),
		Bids:      make([]*gen.PriceLevel, len(ob.Bids)),
		LastSeqId: ob.LastSeqID,
		OrderSize: uint32(ob.OrderMap.Count()),
	}
	for i := range ob.Asks {
		res.Asks[i] = ob.Asks[i].ToPB()
	}
	for i := range ob.Bids {
		res.Bids[i] = ob.Bids[i].ToPB()
	}
	return res
}

type OrderLocation struct {
	Side     OrderSide `json:"side"`
	LevelPos int       `json:"level_pos"` // 在 Asks/Bids 中的 index
	Offset   int       `json:"offset"`    // 在 PriceLevel.Orders 中的 index
}

type PriceLevel struct {
	Price        uint64       `json:"price"`
	Decimals     uint8        `json:"decimals"`
	Orders       []LevelOrder `json:"orders"`
	RemovedCount int          `json:"removed_count"`
}

func (pl PriceLevel) IsEmpty() bool {
	return pl.RemovedCount == len(pl.Orders)
}

func (pl PriceLevel) ActiveOrders() int {
	return len(pl.Orders) - pl.RemovedCount
}

func (pl PriceLevel) ToPB() *gen.PriceLevel {
	level := &gen.PriceLevel{
		Price:    pl.Price,
		Decimals: uint32(pl.Decimals),
		Orders:   make([]*gen.Order, len(pl.Orders)),
	}

	for i := 0; i < len(pl.Orders); i++ {
		order := pl.Orders[i]
		level.Orders[i] = &gen.Order{
			Id:        order.ID,
			Quantity:  order.Quantity,
			IsRemoved: order.IsRemoved,
		}
	}

	return level
}

type Order struct {
	ID        uint64      `json:"id"`
	Price     uint64      `json:"price"`
	Quantity  uint64      `json:"quantity"`
	Decimals  uint8       `json:"decimals"`
	Action    OrderAction `json:"action"`
	Side      OrderSide   `json:"side"`
	Type      OrderType   `json:"type"`
	IsRemoved bool        `json:"is_removed"`
}

type LevelOrder struct {
	ID        uint64 `json:"id"`
	Quantity  uint64 `json:"quantity"`
	IsRemoved bool   `json:"is_removed"`
}

type MatchInfo struct {
	MatchedPrice    uint64    `json:"match_price"`
	MatchedQuantity uint64    `json:"match_quantity"`
	BuyerOrderID    uint64    `json:"buyer_order_id"`
	SellerOrderID   uint64    `json:"seller_order_id"`
	Decimals        uint8     `json:"decimals"`
	Timestamp       time.Time `json:"timestamp"`
}

type MatchResult struct {
	Success bool  `json:"success"`
	Error   error `json:"error"`
}

type CancelInfo struct {
	OrderID uint64 `json:"order_id"`
	Memo    string `json:"memo"`
}
