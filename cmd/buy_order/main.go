package main

import (
	"encoding/json"
	"fmt"
	"go-match/pkg/orderbook"
	"log"
	"math/rand"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func main() {
	nc, err := nats.Connect("nats://localhost:4222")
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		log.Fatalf("JetStream 初始化失敗: %v", err)
	}

	totalMsgs := 1000000
	idx := 0

	r := rand.New(rand.NewSource(time.Now().UnixNano()))

	start := time.Now()

	fmt.Printf("開始推送 %d 筆買單資料...\n", totalMsgs)

	for i := idx; i < idx+totalMsgs; i++ {
		payload := orderbook.Order{
			ID:       uint64(i + 1),
			Price:    uint64(r.Intn(50000-20000+1) + 20000),
			Quantity: uint64(r.Intn(100) + 1),
			Decimals: 0,
			Action:   orderbook.OrderActionNormal,
			Side:     orderbook.OrderSideBuy,
			Type:     orderbook.OrderTypeLimit,
		}
		tmp, _ := json.Marshal(payload)
		if _, err = js.PublishAsync("orders.BTC_USDT", tmp); err != nil {
			log.Printf("發布錯誤: %v", err)
		}

		if i%50000 == 0 {
			fmt.Printf("已投遞: %d 筆...\n", i)
		}

		if i%1000 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}

	select {
	case <-js.PublishAsyncComplete():
		fmt.Println("所有訊息已成功寫入 NATS！")
	case <-time.After(30 * time.Second):
		fmt.Println("等待逾時，可能部分訊息未送達。")
	}

	duration := time.Since(start)
	fmt.Printf("完成！總耗時: %v\n", duration)
	fmt.Printf("平均每秒處理: %.2f 筆\n", float64(totalMsgs)/duration.Seconds())
}
