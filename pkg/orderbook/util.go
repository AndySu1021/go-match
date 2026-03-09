package orderbook

import (
	"go-match/proto/gen"
)

func convertPbPriceLevel(pbLevel *gen.PriceLevel) PriceLevel {
	level := PriceLevel{
		Price:    pbLevel.Price,
		Decimals: uint8(pbLevel.Decimals),
		Orders:   make([]LevelOrder, 0),
	}

	for i := 0; i < len(pbLevel.Orders); i++ {
		if !pbLevel.Orders[i].IsRemoved {
			level.Orders = append(level.Orders, LevelOrder{
				ID:        pbLevel.Orders[i].Id,
				Quantity:  pbLevel.Orders[i].Quantity,
				IsRemoved: pbLevel.Orders[i].IsRemoved,
			})
		}
	}

	return level
}
