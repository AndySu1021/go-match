package application

import (
	"context"
	"encoding/json"
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

func (svc *MatchService) AddOrder(ctx context.Context, params types.AddOrderParams) error {
	price, quantity, err := getPriceAndQuantity(params.Price, params.Quantity)
	if err != nil {
		return err
	}

	var (
		res       []orderbook.MatchInfo
		handleErr error
	)

	switch params.Type {
	case types.OrderTypeLimit:
		return svc.ob.AddOrder(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	case types.OrderTypeFOK:
		res, handleErr = svc.ob.HandleFOK(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	case types.OrderTypeIOC:
		res, handleErr = svc.ob.HandleFOK(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	case types.OrderTypeGTC:
		res, handleErr = svc.ob.HandleFOK(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
		})
	}

	if handleErr != nil {
		return handleErr
	}

	// TODO: send match info to nats
	tmp, _ := json.Marshal(res)
	slog.Debug("match result: ", string(tmp))

	return fmt.Errorf("invalid order type: %s", params.Type)
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
