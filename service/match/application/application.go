package application

import (
	"context"
	"fmt"
	"go-match/config"
	"go-match/pkg/orderbook"
	"go-match/service/match/model"
	"go-match/service/match/types"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
)

var _ model.IMatchService = (*MatchService)(nil)

const DECIMALS = 8

type MatchService struct {
	ob  *orderbook.OrderBook
	cfg *config.AppConfig
}

func NewMatchService(cfg *config.AppConfig) *MatchService {
	return &MatchService{
		ob:  orderbook.NewOrderBook(),
		cfg: cfg,
	}
}

func (svc *MatchService) AddOrder(ctx context.Context, params types.AddOrderParams) ([]types.MatchInfoResp, error) {
	price, quantity, err := getPriceAndQuantity(params.Price, params.Quantity)
	if err != nil {
		return nil, err
	}

	var (
		infos     []orderbook.MatchInfo
		handleErr error
	)

	switch params.Type {
	case types.OrderTypeLimit:
		return nil, svc.ob.AddOrder(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	case types.OrderTypeFOK:
		infos, handleErr = svc.ob.HandleFOK(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	case types.OrderTypeIOC:
		infos, handleErr = svc.ob.HandleIOC(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	case types.OrderTypeGTC:
		infos, handleErr = svc.ob.HandleGTC(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	default:
		return nil, fmt.Errorf("invalid order type: %s", params.Type)
	}

	if handleErr != nil {
		return nil, handleErr
	}

	res := make([]types.MatchInfoResp, len(infos))
	for i := 0; i < len(infos); i++ {
		info := infos[i]
		res[i] = types.MatchInfoResp{
			Price:         decimal.NewFromUint64(info.MatchedPrice).Div(decimal.New(1, DECIMALS)).String(),
			Quantity:      decimal.NewFromUint64(info.MatchedQuantity).Div(decimal.New(1, DECIMALS)).String(),
			BuyerOrderID:  info.BuyerOrderID,
			SellerOrderID: info.SellerOrderID,
			Timestamp:     info.Timestamp,
		}
	}

	// TODO: send match info to nats

	return res, nil
}

func (svc *MatchService) CancelOrder(ctx context.Context, orderId uint64) error {
	return svc.ob.CancelOrder(orderId)
}

func (svc *MatchService) UpdateOrder(ctx context.Context, params types.UpdateOrderParams) error {
	price, quantity, err := getPriceAndQuantity(params.Price, params.Quantity)
	if err != nil {
		return err
	}

	return svc.ob.UpdateOrder(orderbook.Order{
		ID:       params.ID,
		Price:    price,
		Quantity: quantity,
	})
}

func (svc *MatchService) Snapshot(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(svc.cfg.Snapshot.Period) * time.Second):
			slog.Info("start snapshot...")
			if err := svc.ob.Snapshot(svc.cfg.Snapshot.Path); err != nil {
				slog.Error("snapshot error: ", err)
				return
			}
			slog.Info("snapshot completed")
		}
	}
}

func (svc *MatchService) Restore(ctx context.Context) error {
	return svc.ob.Restore(svc.cfg.Snapshot.Path)
}

func getPriceAndQuantity(price string, quantity string) (uint64, uint64, error) {
	tmpPrice, err := decimal.NewFromString(price)
	if err != nil {
		return 0, 0, err
	}

	tmpQuantity, err := decimal.NewFromString(quantity)
	if err != nil {
		return 0, 0, err
	}

	tmpPrice = tmpPrice.Mul(decimal.New(1, DECIMALS))
	tmpQuantity = tmpQuantity.Mul(decimal.New(1, DECIMALS))

	return uint64(tmpPrice.IntPart()), uint64(tmpQuantity.IntPart()), nil
}
