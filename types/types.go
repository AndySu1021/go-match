package types

import (
	"time"
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

type AddOrderParams struct {
	ID           uint64    `json:"id"`
	Price        uint64    `json:"price"`
	Quantity     uint64    `json:"quantity"`
	Decimals     uint8     `json:"decimals"`
	Side         OrderSide `json:"side"`
	IsProcessing bool      `json:"is_processing"`
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
