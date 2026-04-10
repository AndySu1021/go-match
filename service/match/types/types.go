package types

import "go-match/pkg/orderbook"

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
