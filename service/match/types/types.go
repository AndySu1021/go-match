package types

import (
	"go-match/pkg/orderbook"
	"go-match/proto/gen"
	"time"
)

type AddOrderParams struct {
	ID       uint64
	Price    string
	Quantity string
	Side     orderbook.Side
	Type     OrderType
}

type UpdateOrderParams struct {
	ID       uint64
	Price    string
	Quantity string
}

type MatchInfoResp struct {
	Price         string    `json:"price"`
	Quantity      string    `json:"quantity"`
	BuyerOrderID  uint64    `json:"buyer_order_id"`
	SellerOrderID uint64    `json:"seller_order_id"`
	Timestamp     time.Time `json:"timestamp"`
}

func (resp MatchInfoResp) ToPB() *gen.MatchInfo {
	return &gen.MatchInfo{
		Price:         resp.Price,
		Quantity:      resp.Quantity,
		BuyerOrderId:  resp.BuyerOrderID,
		SellerOrderId: resp.SellerOrderID,
		Timestamp:     uint64(resp.Timestamp.UnixMilli()),
	}
}
