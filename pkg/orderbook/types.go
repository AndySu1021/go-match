package orderbook

import (
	"time"
)

type Side uint8

const (
	SideUnknown Side = iota
	SideBuy
	SideSell
)

type MatchInfo struct {
	MatchedPrice    uint64    `json:"match_price"`
	MatchedQuantity uint64    `json:"match_quantity"`
	BuyerOrderID    uint64    `json:"buyer_order_id"`
	SellerOrderID   uint64    `json:"seller_order_id"`
	Timestamp       time.Time `json:"timestamp"`
}

type CancelInfo struct {
	OrderID uint64 `json:"order_id"`
	Memo    string `json:"memo"`
}
