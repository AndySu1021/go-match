package orderbook

import (
	"go-match/proto/gen"
)

func convertPbPriceLevel(pbLevel *gen.PriceLevel) PriceLevel {
	level := PriceLevel{
		Price:    pbLevel.Price,
		Decimals: uint8(pbLevel.Decimals),
		Orders:   make([]LevelOrder, len(pbLevel.Orders)),
	}

	for i := 0; i < len(level.Orders); i++ {
		level.Orders[i] = LevelOrder{
			ID:        pbLevel.Orders[i].Id,
			Quantity:  pbLevel.Orders[i].Quantity,
			IsRemoved: pbLevel.Orders[i].IsRemoved,
		}
	}

	return level
}
