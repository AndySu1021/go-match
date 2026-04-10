package handler

import (
	"context"
	"go-match/internal/convert"
	"go-match/pkg/orderbook"
	"go-match/proto/gen"
	"go-match/service/match/model"
	"go-match/service/match/types"
)

var _ gen.MatchServiceServer = (*MatchGrpcHandler)(nil)

type MatchGrpcHandler struct {
	svc model.IMatchService
}

func NewMatchGrpcHandler(svc model.IMatchService) *MatchGrpcHandler {
	return &MatchGrpcHandler{svc: svc}
}

func (h *MatchGrpcHandler) AddOrder(ctx context.Context, req *gen.AddOrderReq) (*gen.AddOrderResp, error) {
	if err := h.svc.AddOrder(ctx, types.AddOrderParams{
		ID:       req.Id,
		Price:    req.Price,
		Quantity: req.Quantity,
		Side:     orderbook.Side(req.Side),
		Type:     types.OrderType(req.Type),
	}); err != nil {
		return &gen.AddOrderResp{
			Meta: convert.CommonErrRespMeta(err),
		}, err
	}

	return &gen.AddOrderResp{
		Meta: convert.CommonRespMeta(),
	}, nil
}

func (h *MatchGrpcHandler) CancelOrder(ctx context.Context, req *gen.CancelOrderReq) (*gen.CancelOrderResp, error) {
	if err := h.svc.CancelOrder(ctx, req.Id); err != nil {
		return &gen.CancelOrderResp{
			Meta: convert.CommonErrRespMeta(err),
		}, err
	}

	return &gen.CancelOrderResp{
		Meta: convert.CommonRespMeta(),
	}, nil
}

func (h *MatchGrpcHandler) UpdateOrder(ctx context.Context, req *gen.UpdateOrderReq) (*gen.UpdateOrderResp, error) {
	if err := h.svc.UpdateOrder(ctx, types.UpdateOrderParams{
		ID:       req.Id,
		Price:    req.Price,
		Quantity: req.Quantity,
	}); err != nil {
		return &gen.UpdateOrderResp{
			Meta: convert.CommonErrRespMeta(err),
		}, err
	}

	return &gen.UpdateOrderResp{
		Meta: convert.CommonRespMeta(),
	}, nil
}
