package orderbook

type Side uint8

const (
	SideUnknown Side = iota
	SideBuy
	SideSell
)
