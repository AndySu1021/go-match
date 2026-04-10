package types

type OrderType uint8

const (
	OrderTypeUnknown OrderType = iota
	OrderTypeLimit
	OrderTypeFOK
	OrderTypeIOC // 市價單，未完全成交剩下的取消
	OrderTypeGTC
)

func (o OrderType) String() string {
	switch o {
	case OrderTypeUnknown:
		return "Unknown"
	case OrderTypeLimit:
		return "Limit"
	case OrderTypeFOK:
		return "FOK"
	case OrderTypeIOC:
		return "IOC"
	case OrderTypeGTC:
		return "GTC"
	}
	return ""
}
