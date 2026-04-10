package model

import "time"

type TradingPair struct {
	BaseSymbol       string    `json:"base_symbol"`       // ex. BTC
	QuoteSymbol      string    `json:"quote_symbol"`      // ex. USDT
	PriceDecimals    string    `json:"price_decimals"`    // ex. 100000.12 -> 2
	QuantityDecimals string    `json:"quantity_decimals"` // ex. 0.001 -> 3
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}
