package internal

import "errors"

var (
	ErrFOKInsufficientLiquidity = errors.New("FOK insufficient liquidity")
	ErrPriceLevelNotFound       = errors.New("price level not found")
)
