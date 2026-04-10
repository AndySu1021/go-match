package types

type OrderType uint8

const (
	OrderTypeUnknown OrderType = iota
	OrderTypeLimit
	OrderTypeFOK
	OrderTypeIOC // 市價單，未完全成交剩下的取消
	OrderTypeGTC
)
