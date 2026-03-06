package orderbook

import (
	"fmt"
	"testing"
)

// ─────────────────────────────────────────────
// 輔助函式
// ─────────────────────────────────────────────

// newEmptyOrderBook 建立一個乾淨的空訂單簿，不依賴檔案系統
func newEmptyOrderBook() *OrderBook {
	return &OrderBook{
		Asks:     make([]PriceLevel, 0),
		Bids:     make([]PriceLevel, 0),
		OrderMap: make(map[uint64]OrderLocation),
	}
}

// makeLimitOrder 快速建立一筆限價單
func makeLimitOrder(id uint64, side OrderSide, price, qty uint64) Order {
	return Order{
		ID:       id,
		Type:     OrderTypeLimit,
		Side:     side,
		Price:    price,
		Quantity: qty,
		Decimals: 2,
	}
}

// seedOrderBook 預填 n 筆買單與 n 筆賣單，形成有深度的訂單簿
// 買單：價格 base-1 ~ base-n（遞減）
// 賣單：價格 base+1 ~ base+n（遞增）
func seedOrderBook(ob *OrderBook, n int, base uint64) uint64 {
	var nextID uint64 = 1
	for i := 0; i < n; i++ {
		buyPrice := base - uint64(i+1)
		_ = ob.addOrder(makeLimitOrder(nextID, OrderSideBuy, buyPrice, 100))
		nextID++

		sellPrice := base + uint64(i+1)
		_ = ob.addOrder(makeLimitOrder(nextID, OrderSideSell, sellPrice, 100))
		nextID++
	}
	return nextID
}

// ─────────────────────────────────────────────
// BenchmarkAddOrder — 單純掛單（不撮合）
// ─────────────────────────────────────────────

func BenchmarkAddOrderBuy(b *testing.B) {
	ob := newEmptyOrderBook()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		order := makeLimitOrder(uint64(i+1), OrderSideBuy, 1000-uint64(i%500), 10)
		_ = ob.addOrder(order)
	}
}

func BenchmarkAddOrderSell(b *testing.B) {
	ob := newEmptyOrderBook()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		order := makeLimitOrder(uint64(i+1), OrderSideSell, 1001+uint64(i%500), 10)
		_ = ob.addOrder(order)
	}
}

// ─────────────────────────────────────────────
// BenchmarkRemoveOrder — 取消訂單
// ─────────────────────────────────────────────

func BenchmarkRemoveOrder(b *testing.B) {
	b.ReportAllocs()

	const count = 1000

	// 準備可用 ID 池與訂單簿，b.N 確定後只需一次 setup
	ob := newEmptyOrderBook()
	ids := make([]uint64, 0, count)

	refill := func() {
		ob = newEmptyOrderBook()
		ids = ids[:0]
		for j := 1; j <= count; j++ {
			id := uint64(j)
			_ = ob.addOrder(makeLimitOrder(id, OrderSideBuy, uint64(1000-j%200), 10))
			ids = append(ids, id)
		}
	}

	refill()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// 1000 筆用完才重建一次，StopTimer 呼叫從 b.N 次降到 b.N/1000 次
		if len(ids) == 0 {
			b.StopTimer()
			refill()
			b.StartTimer()
		}

		last := len(ids) - 1
		_ = ob.removeOrder(ids[last])
		ids = ids[:last]
	}
}

// ─────────────────────────────────────────────
// BenchmarkMatchLimit — 掛限價單（不成交）
// ─────────────────────────────────────────────

func BenchmarkMatchLimit(b *testing.B) {
	ob := newEmptyOrderBook()
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		order := Order{
			ID:       uint64(i + 1),
			Type:     OrderTypeLimit,
			Side:     OrderSideBuy,
			Price:    uint64(900 - i%100),
			Quantity: 10,
			Decimals: 2,
		}
		_, _, _ = ob.Match(order)
	}
}

// ─────────────────────────────────────────────
// BenchmarkMatchGTC — GTC 撮合（有深度的訂單簿）
// ─────────────────────────────────────────────

func BenchmarkMatchGTC(b *testing.B) {
	b.ReportAllocs()

	const depth = 200
	const base = uint64(1000)

	var ob *OrderBook
	var nextID uint64

	refill := func() {
		ob = newEmptyOrderBook()
		nextID = seedOrderBook(ob, depth, base)
	}

	refill()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Asks 耗盡時才重建，不是每輪都重建
		if len(ob.Asks) == 0 {
			b.StopTimer()
			refill()
			b.StartTimer()
		}

		order := Order{
			ID:       nextID,
			Type:     OrderTypeGTC,
			Side:     OrderSideBuy,
			Price:    1005,
			Quantity: 50,
			Decimals: 2,
		}
		nextID++
		_, _, _ = ob.Match(order)
	}
}

// ─────────────────────────────────────────────
// BenchmarkMatchIOC — IOC 撮合
// ─────────────────────────────────────────────

func BenchmarkMatchIOC(b *testing.B) {
	b.ReportAllocs()

	const depth = 200
	const base = uint64(1000)

	var ob *OrderBook
	var nextID uint64

	refill := func() {
		ob = newEmptyOrderBook()
		nextID = seedOrderBook(ob, depth, base)
	}

	refill()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if len(ob.Asks) == 0 {
			b.StopTimer()
			refill()
			b.StartTimer()
		}

		order := Order{
			ID:       nextID,
			Type:     OrderTypeIOC,
			Side:     OrderSideBuy,
			Price:    1010,
			Quantity: 200,
			Decimals: 2,
		}
		nextID++
		_, _, _ = ob.Match(order)
	}
}

// ─────────────────────────────────────────────
// BenchmarkMatchFOK — FOK 撮合（成功 & 失敗）
// ─────────────────────────────────────────────

func BenchmarkMatchFOKSuccess(b *testing.B) {
	b.ReportAllocs()

	const depth = 200
	const base = uint64(1000)

	var ob *OrderBook
	var nextID uint64

	refill := func() {
		ob = newEmptyOrderBook()
		nextID = seedOrderBook(ob, depth, base)
	}

	refill()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if len(ob.Asks) == 0 {
			b.StopTimer()
			refill()
			b.StartTimer()
		}

		order := Order{
			ID:       nextID,
			Type:     OrderTypeFOK,
			Side:     OrderSideBuy,
			Price:    1010,
			Quantity: 50,
			Decimals: 2,
		}
		nextID++
		_, _, _ = ob.Match(order)
	}
}

func BenchmarkMatchFOKFail(b *testing.B) {
	b.ReportAllocs()

	// FOK 失敗不消耗訂單簿，只需建一次
	ob := newEmptyOrderBook()
	nextID := seedOrderBook(ob, 5, 1000)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		order := Order{
			ID:       nextID,
			Type:     OrderTypeFOK,
			Side:     OrderSideBuy,
			Price:    1010,
			Quantity: 99999, // 遠超可用量，直接 return false，訂單簿不變
			Decimals: 2,
		}
		nextID++
		_, _, _ = ob.Match(order)
	}
}

// ─────────────────────────────────────────────
// BenchmarkCancelOrder — 透過公開介面取消訂單
// ─────────────────────────────────────────────

func BenchmarkCancelOrder(b *testing.B) {
	b.ReportAllocs()

	const count = 500

	ob := newEmptyOrderBook()
	ids := make([]uint64, 0, count)

	refill := func() {
		ob = newEmptyOrderBook()
		ids = ids[:0]
		for j := 1; j <= count; j++ {
			id := uint64(j)
			_ = ob.addOrder(makeLimitOrder(id, OrderSideBuy, uint64(1000-j%100), 10))
			ids = append(ids, id)
		}
	}

	refill()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if len(ids) == 0 {
			b.StopTimer()
			refill()
			b.StartTimer()
		}

		last := len(ids) - 1
		_ = ob.CancelOrder(ids[last])
		ids = ids[:last]
	}
}

// ─────────────────────────────────────────────
// BenchmarkShouldPurge — 判斷是否需要清理
// ─────────────────────────────────────────────

func BenchmarkShouldPurge(b *testing.B) {
	ob := newEmptyOrderBook()
	// 建立大量已標記刪除的訂單
	for j := 1; j <= 1000; j++ {
		_ = ob.addOrder(makeLimitOrder(uint64(j), OrderSideBuy, uint64(900+j%100), 10))
	}
	for j := 1; j <= 500; j++ {
		_ = ob.removeOrder(uint64(j))
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ob.shouldPurge()
	}
}

// ─────────────────────────────────────────────
// BenchmarkPurgeLevels — 清理已刪除的訂單
// ─────────────────────────────────────────────

func BenchmarkPurgeLevels(b *testing.B) {
	b.ReportAllocs()

	// purgeLevels 不修改原始訂單簿，可共用同一份資料
	ob := newEmptyOrderBook()
	for j := 1; j <= 1000; j++ {
		_ = ob.addOrder(makeLimitOrder(uint64(j), OrderSideBuy, uint64(900+j%100), 10))
	}
	for j := 1; j <= 800; j++ {
		_ = ob.removeOrder(uint64(j))
	}
	levels := ob.Bids

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = purgeLevels(levels)
	}
}

// ─────────────────────────────────────────────
// BenchmarkRebuildOrderMap — 重建 OrderMap 索引
// ─────────────────────────────────────────────

func BenchmarkRebuildOrderMap(b *testing.B) {
	ob := newEmptyOrderBook()
	seedOrderBook(ob, 500, 1000)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ob.rebuildOrderMap()
	}
}

// ─────────────────────────────────────────────
// BenchmarkOrderBookDepth — 不同深度的撮合效能比較
// ─────────────────────────────────────────────

func BenchmarkMatchGTCByDepth(b *testing.B) {
	depths := []int{10, 100, 500, 1000}

	for _, depth := range depths {
		depth := depth
		b.Run(fmt.Sprintf("depth-%d", depth), func(b *testing.B) {
			b.ReportAllocs()

			var ob *OrderBook
			var nextID uint64

			refill := func() {
				ob = newEmptyOrderBook()
				nextID = seedOrderBook(ob, depth, 1000)
			}

			refill()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if len(ob.Asks) == 0 {
					b.StopTimer()
					refill()
					b.StartTimer()
				}

				order := Order{
					ID:       nextID,
					Type:     OrderTypeGTC,
					Side:     OrderSideBuy,
					Price:    1000 + uint64(depth/2),
					Quantity: uint64(depth) * 50,
					Decimals: 2,
				}
				nextID++
				_, _, _ = ob.Match(order)
			}
		})
	}
}

// ─────────────────────────────────────────────
// BenchmarkConcurrentMatch — 並行撮合壓力測試
// ─────────────────────────────────────────────

func BenchmarkConcurrentMatch(b *testing.B) {
	ob := newEmptyOrderBook()
	seedOrderBook(ob, 200, 1000)

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		var id uint64 = 100000
		for pb.Next() {
			id++
			order := Order{
				ID:       id,
				Type:     OrderTypeGTC,
				Side:     OrderSideBuy,
				Price:    1003,
				Quantity: 5,
				Decimals: 2,
			}
			_, _, _ = ob.Match(order)
		}
	})
}
