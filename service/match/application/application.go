package application

import (
	"context"
	"fmt"
	"go-match/pkg/orderbook"
	"go-match/service/match/model"
	"go-match/service/match/types"

	"github.com/shopspring/decimal"
)

var _ model.IMatchService = (*MatchService)(nil)

const DECIMALS = 8

type MatchService struct {
	obMap map[string]*orderbook.OrderBook // instrument -> obMap
}

func NewMatchService() *MatchService {
	return &MatchService{
		obMap: make(map[string]*orderbook.OrderBook),
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

	ob, err := svc.getOrderBook(params.Instrument)
	if err != nil {
		return nil, err
	}

	switch params.Type {
	case types.OrderTypeLimit:
		return nil, ob.AddOrder(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
			SeqID:        params.SeqID,
		})
	case types.OrderTypeFOK:
		infos, handleErr = ob.HandleFOK(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
			SeqID:        params.SeqID,
		})
	case types.OrderTypeIOC:
		infos, handleErr = ob.HandleIOC(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
			SeqID:        params.SeqID,
		})
	case types.OrderTypeGTC:
		infos, handleErr = ob.HandleGTC(orderbook.Order{
			ID:           params.ID,
			Price:        price,
			Quantity:     quantity,
			Side:         params.Side,
			IsProcessing: false,
			SeqID:        params.SeqID,
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

func (svc *MatchService) CancelOrder(ctx context.Context, params types.CancelOrderParams) error {
	ob, err := svc.getOrderBook(params.Instrument)
	if err != nil {
		return err
	}

	return ob.CancelOrder(orderbook.Order{
		ID:    params.ID,
		SeqID: params.SeqID,
	})
}

func (svc *MatchService) UpdateOrder(ctx context.Context, params types.UpdateOrderParams) error {
	price, quantity, err := getPriceAndQuantity(params.Price, params.Quantity)
	if err != nil {
		return err
	}

	ob, err := svc.getOrderBook(params.Instrument)
	if err != nil {
		return err
	}

	return ob.UpdateOrder(orderbook.Order{
		ID:       params.ID,
		Price:    price,
		Quantity: quantity,
		SeqID:    params.SeqID,
	})
}

func (svc *MatchService) RegisterOrderBook(instrument string, ob *orderbook.OrderBook) {
	svc.obMap[instrument] = ob
}

func (svc *MatchService) getOrderBook(instrument string) (*orderbook.OrderBook, error) {
	if _, ok := svc.obMap[instrument]; !ok {
		return nil, fmt.Errorf("instrument %s not exist", instrument)
	}

	return svc.obMap[instrument], nil
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
