package model

import (
	"context"
	"go-match/pkg/orderbook"
	"go-match/service/match/types"
)

type IMatchService interface {
	AddOrder(ctx context.Context, params types.AddOrderParams) ([]types.MatchInfoResp, error)
	CancelOrder(ctx context.Context, params types.CancelOrderParams) error
	UpdateOrder(ctx context.Context, params types.UpdateOrderParams) error
	RegisterOrderBook(instrument string, ob *orderbook.OrderBook)
}
