package types

import (
	"bytes"
	"go-match/pkg/orderbook"
	"go-match/proto/gen"
	"time"
)

type AddOrderParams struct {
	Instrument string
	ID         uint64
	Price      string
	Quantity   string
	Side       orderbook.Side
	Type       OrderType
	SeqID      uint64
}

type CancelOrderParams struct {
	Instrument string
	ID         uint64
	SeqID      uint64
}

type UpdateOrderParams struct {
	Instrument string
	ID         uint64
	Price      string
	Quantity   string
	SeqID      uint64
}

type MatchInfoResp struct {
	Price         string    `json:"price"`
	Quantity      string    `json:"quantity"`
	BuyerOrderID  uint64    `json:"buyer_order_id"`
	SellerOrderID uint64    `json:"seller_order_id"`
	Timestamp     time.Time `json:"timestamp"`
}

func (resp MatchInfoResp) ToPB() *gen.MatchInfo {
	return &gen.MatchInfo{
		Price:         resp.Price,
		Quantity:      resp.Quantity,
		BuyerOrderId:  resp.BuyerOrderID,
		SellerOrderId: resp.SellerOrderID,
		Timestamp:     uint64(resp.Timestamp.UnixMilli()),
	}
}

type OrderParams struct {
	Instrument string
	ID         uint64
	Price      string
	Quantity   string
	Side       orderbook.Side
	Type       OrderType
	Action     Action
}

func (p OrderParams) ToWalRecord() WalRecord {
	var (
		instrument [32]byte
		price      [64]byte
		quantity   [64]byte
	)

	copy(instrument[:], p.Instrument)
	copy(price[:], p.Price)
	copy(quantity[:], p.Quantity)

	return WalRecord{
		Instrument: instrument,
		ID:         p.ID,
		Price:      price,
		Quantity:   quantity,
		Side:       uint8(p.Side),
		Type:       uint8(p.Type),
		Action:     uint8(p.Action),
	}
}

type WalRecord struct {
	SeqID      uint64
	ID         uint64
	Instrument [32]byte
	Price      [64]byte
	Quantity   [64]byte
	Side       uint8
	Type       uint8
	Action     uint8
	_          [5]byte // padding
}

func (r WalRecord) ToOrderParams() OrderParams {
	return OrderParams{
		Instrument: string(bytes.TrimRight(r.Instrument[:], "\x00")),
		ID:         r.ID,
		Price:      string(bytes.TrimRight(r.Price[:], "\x00")),
		Quantity:   string(bytes.TrimRight(r.Quantity[:], "\x00")),
		Side:       orderbook.Side(r.Side),
		Type:       OrderType(r.Type),
		Action:     Action(r.Action),
	}
}

type WalMetadata struct {
	LastSeqID uint64   // 最後一個 SeqID
	WritePos  uint32   // 目前寫到檔案的哪個 Offset
	Magic     uint16   // 識別碼
	Version   uint8    // 版本號
	_         [49]byte // padding
}

type WalObj struct {
	Meta WalMetadata
	File []byte
}
