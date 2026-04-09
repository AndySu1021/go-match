package main

import (
	"fmt"
	"go-match/pkg/orderbook_v2"
	"go-match/types"
	"math/rand"
	_ "net/http/pprof"
	"time"
)

func main() {
	snapshot()
	restore()
}

func snapshot() {
	ob := orderbook_v2.NewOrderBook()

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	total := 10000000
	bidSize := 4615324

	for i := 0; i < bidSize; i++ {
		if err := ob.AddOrder(types.AddOrderParams{
			ID:       uint64(i + 1),
			Price:    uint64(r.Intn(50000-20000+1) + 20000),
			Quantity: uint64(r.Intn(100) + 1),
			Decimals: 0,
			Side:     types.OrderSideBuy,
		}); err != nil {
			panic(err)
		}

		if i%50000 == 0 {
			fmt.Printf("已投遞: %d 筆...\n", i)
		}

		if i%1000 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	for i := bidSize; i < total; i++ {
		if err := ob.AddOrder(types.AddOrderParams{
			ID:       uint64(i + 1),
			Price:    uint64(r.Intn(50000-20000+1) + 20000),
			Quantity: uint64(r.Intn(100) + 1),
			Decimals: 0,
			Side:     types.OrderSideSell,
		}); err != nil {
			panic(err)
		}

		if i%50000 == 0 {
			fmt.Printf("已投遞: %d 筆...\n", i)
		}

		if i%1000 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	count := 0
	it := ob.Asks.Iterator()
	for it.Next() {
		level := it.Value().(*orderbook_v2.PriceLevel)
		count += level.Orders.Len()
	}

	fmt.Printf("Init Ask Size: %d\n", count)

	// 從最大到最小（Bids 用這個）
	count = 0
	it = ob.Bids.Iterator()
	it.End() // 移到尾端
	for it.Prev() {
		level := it.Value().(*orderbook_v2.PriceLevel)
		count += level.Orders.Len()
	}

	fmt.Printf("Init Bid Size: %d\n", count)

	fmt.Printf("Init Total Size: %d\n", ob.OrderMap.Count())

	time.Sleep(3 * time.Second)

	t1 := time.Now()
	if err := ob.Snapshot("./snapshot.bin"); err != nil {
		panic(err)
	}
	fmt.Printf("snapshot latency %v\n", time.Since(t1))
}

func restore() {
	ob := orderbook_v2.NewOrderBook()

	t2 := time.Now()
	if err := ob.Restore("./snapshot.bin"); err != nil {
		panic(err)
	}
	fmt.Printf("restore latency %v\n", time.Since(t2))

	count := 0
	it := ob.Asks.Iterator()
	for it.Next() {
		level := it.Value().(*orderbook_v2.PriceLevel)
		count += level.Orders.Len()
	}

	fmt.Printf("Restore Ask Size: %d\n", count)

	// 從最大到最小（Bids 用這個）
	count = 0
	it = ob.Bids.Iterator()
	it.End() // 移到尾端
	for it.Prev() {
		level := it.Value().(*orderbook_v2.PriceLevel)
		count += level.Orders.Len()
	}

	fmt.Printf("Restore Bid Size: %d\n", count)
	fmt.Printf("Restore Total Size: %d\n", ob.OrderMap.Count())
}
