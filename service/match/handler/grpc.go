package handler

import (
	"context"
	"encoding/binary"
	"go-match/internal/convert"
	"go-match/pkg/orderbook"
	"go-match/proto/gen"
	"go-match/service/match/model"
	"go-match/service/match/types"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

var _ gen.MatchServiceServer = (*MatchGrpcHandler)(nil)

type MatchGrpcHandler struct {
	svc   model.IMatchService
	wal   types.WalObj
	seqId atomic.Uint64
}

func NewMatchGrpcHandler(svc model.IMatchService, wal types.WalObj) *MatchGrpcHandler {
	h := &MatchGrpcHandler{svc: svc, wal: wal}
	return h
}

func (h *MatchGrpcHandler) AddOrder(ctx context.Context, req *gen.AddOrderReq) (*gen.AddOrderResp, error) {
	h.writeWAL(types.OrderParams{
		Instrument: req.Instrument,
		ID:         req.Id,
		Price:      req.Price,
		Quantity:   req.Quantity,
		Side:       orderbook.Side(req.Side),
		Type:       types.OrderType(req.Type),
		Action:     types.ActionAdd,
	})

	infos, err := h.svc.AddOrder(ctx, types.AddOrderParams{
		Instrument: req.Instrument,
		ID:         req.Id,
		Price:      req.Price,
		Quantity:   req.Quantity,
		Side:       orderbook.Side(req.Side),
		Type:       types.OrderType(req.Type),
		SeqID:      h.seqId.Load(),
	})
	if err != nil {
		return &gen.AddOrderResp{
			Meta: convert.CommonErrRespMeta(err),
		}, err
	}

	res := make([]*gen.MatchInfo, len(infos))
	for i := 0; i < len(infos); i++ {
		res[i] = infos[i].ToPB()
	}

	return &gen.AddOrderResp{
		Meta:  convert.CommonRespMeta(),
		Infos: res,
	}, nil
}

func (h *MatchGrpcHandler) CancelOrder(ctx context.Context, req *gen.CancelOrderReq) (*gen.CancelOrderResp, error) {
	h.writeWAL(types.OrderParams{
		Instrument: req.Instrument,
		ID:         req.Id,
		Action:     types.ActionCancel,
	})

	if err := h.svc.CancelOrder(ctx, types.CancelOrderParams{
		Instrument: req.Instrument,
		ID:         req.Id,
		SeqID:      h.seqId.Load(),
	}); err != nil {
		return &gen.CancelOrderResp{
			Meta: convert.CommonErrRespMeta(err),
		}, err
	}

	return &gen.CancelOrderResp{
		Meta: convert.CommonRespMeta(),
	}, nil
}

func (h *MatchGrpcHandler) UpdateOrder(ctx context.Context, req *gen.UpdateOrderReq) (*gen.UpdateOrderResp, error) {
	h.writeWAL(types.OrderParams{
		Instrument: req.Instrument,
		ID:         req.Id,
		Price:      req.Price,
		Quantity:   req.Quantity,
		Action:     types.ActionUpdate,
	})

	if err := h.svc.UpdateOrder(ctx, types.UpdateOrderParams{
		Instrument: req.Instrument,
		ID:         req.Id,
		Price:      req.Price,
		Quantity:   req.Quantity,
		SeqID:      h.seqId.Load(),
	}); err != nil {
		return &gen.UpdateOrderResp{
			Meta: convert.CommonErrRespMeta(err),
		}, err
	}

	return &gen.UpdateOrderResp{
		Meta: convert.CommonRespMeta(),
	}, nil
}

func (h *MatchGrpcHandler) writeWAL(params types.OrderParams) {
	recordSize := binary.Size(types.WalRecord{})
	meta := *(*types.WalMetadata)(unsafe.Pointer(&h.wal.File[0]))

	// write record
	*(*types.WalRecord)(unsafe.Pointer(&h.wal.File[meta.WritePos])) = params.ToWalRecord()

	// update meta
	*(*types.WalMetadata)(unsafe.Pointer(&h.wal.File[0])) = types.WalMetadata{
		LastSeqID: h.seqId.Add(1),
		WritePos:  meta.WritePos + uint32(recordSize),
		Magic:     h.wal.Meta.Magic,
		Version:   h.wal.Meta.Version,
	}

	unix.Msync(h.wal.File, unix.MS_SYNC)
}
