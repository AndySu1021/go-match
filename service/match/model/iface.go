package model

import (
	"context"
	"go-match/service/match/types"
)

type IMatchService interface {
	AddOrder(ctx context.Context, params types.AddOrderParams) error
	CancelOrder(ctx context.Context, orderId uint64) error
	UpdateOrder(ctx context.Context, params types.UpdateOrderParams) error
	Snapshot(ctx context.Context)
	Restore(ctx context.Context) error
}
