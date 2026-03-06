package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-match/config"
	"go-match/internal"
	"go-match/pkg/orderbook"
	"log"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var (
	ob            *orderbook.OrderBook
	recoverySeqID uint64
)

func init() {
	start := time.Now()
	defer func() {
		slog.Info(fmt.Sprintf("init latency: %d ms", time.Since(start).Milliseconds()))
	}()

	time.Local = time.UTC

	cfg = &Config{
		App:  config.InitAppConfig(),
		Nats: config.InitNatsConfig(),
	}

	tmpOB, err := orderbook.NewOrderBook(cfg.App.Snapshot.Dir)
	if err != nil {
		panic(err)
	}

	ob = tmpOB
}

func main() {
	// HA 多節點連線，任一節點掛掉自動 failover
	nc, err := nats.Connect(
		strings.Join(cfg.Nats.Urls, ","),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1), // 無限重連
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			slog.Info("⚠️  Disconnected: ", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			slog.Info("✅ Reconnected to: ", nc.ConnectedUrl())
		}),
	)
	if err != nil {
		slog.Error("連線失敗: ", err)
		panic(err)
	}
	defer nc.Drain() // Drain 確保訊息處理完才關閉

	// 建立 JetStream context
	js, err := jetstream.New(nc, jetstream.WithPublishAsyncMaxPending(1024))
	if err != nil {
		log.Fatalf("JetStream 初始化失敗: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	name := fmt.Sprintf("consumer_%s", cfg.App.Instrument)

	// 清除舊有 Consumer 做災難復原
	_ = js.DeleteConsumer(ctx, "SPOT_ORDERS", name)

	cons, err := js.CreateOrUpdateConsumer(ctx, "SPOT_ORDERS", jetstream.ConsumerConfig{
		Name:          name,
		Durable:       name,
		FilterSubject: fmt.Sprintf("orders.%s", cfg.App.Instrument),
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:   ob.LastSeqID + 1,
		MaxAckPending: 1, // 可以將這個調高
		MaxDeliver:    3,
		AckWait:       1 * time.Second, // 同時也要調高這個值，否則有可能重複投遞導致一張單撮 2 次以上
	})
	if err != nil {
		log.Printf("建立 Pull Consumer 失敗: %v", err)
		return
	}

	kv, _ := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:  "SYSTEM_STATE",
		History: 5,              // 每個 Key 保留最近 5 次修改紀錄
		TTL:     24 * time.Hour, // 資料存活時間（選用）
	})

	// 檢查是否有數據需要回放
	entry, err := kv.Get(ctx, "matcher.last_seq")
	if err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		panic(err)
	}
	if entry != nil {
		tmpSeqId, _ := strconv.ParseUint(string(entry.Value()), 10, 64)
		recoverySeqID = tmpSeqId
	}

	go consume(ctx, cons, kv, js)
	go snapshot(ctx)
	//go readMemUsage(ctx)
	//go pprof()

	// 等待 Ctrl+C 或 SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	cancel()

	// graceful shutdown
	_, cancel = context.WithTimeout(context.Background(), 30*time.Second)

	if err = ob.Snapshot(cfg.App.Snapshot.Dir); err != nil {
		slog.Error("snapshot error: ", err)
		return
	}

	log.Println("🛑 Shutting down...")
}

func consume(ctx context.Context, consumer jetstream.Consumer, kv jetstream.KeyValue, js jetstream.JetStream) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msgs, err := consumer.Fetch(1000)
			if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
				log.Printf("Fetch 錯誤: %v", err)
				continue
			}

			if msgs.Error() != nil {
				slog.Error("fetch stream error: ", msgs.Error())
				return
			}

			for msg := range msgs.Messages() {
				meta, err := msg.Metadata()
				if err != nil {
					panic(err)
				}
				slog.Info(fmt.Sprintf("📦 [Pull] subject=%s data=%s seq_id=%d", msg.Subject(), string(msg.Data()), meta.Sequence.Stream))
				if err = processOrder(ctx, meta.Sequence.Stream, msg.Data(), js); err != nil {
					slog.Error("process order failed: ", err)
					if meta.NumDelivered >= 3 {
						// 方案 A: 讓 NATS 自動處理 (需配置下面第 3 點的 DLQ)
						// 這裡我們直接 Terminate，表示不重試了，NATS 會根據配置送 DLQ
						msg.Term()
						continue
					}
					break
				}
				_, _ = kv.Put(ctx, "matcher.last_seq", []byte(strconv.FormatUint(meta.Sequence.Stream, 10)))
				msg.Ack()
			}
		}
	}
}

func processOrder(ctx context.Context, seqId uint64, data []byte, js jetstream.JetStream) error {
	order := orderbook.Order{}
	if err := json.Unmarshal(data, &order); err != nil {
		return err
	}

	ob.LastSeqID = seqId

	switch order.Action {
	case orderbook.OrderActionNormal:
		res, infos, err := ob.Match(order)
		if err != nil {
			slog.Error(fmt.Sprintf("match error: %s", err))
			return err
		}

		if !res.Success {
			if errors.Is(res.Error, internal.ErrFOKInsufficientLiquidity) {
				if err = publish(js, "canceled.BTC_USDT", orderbook.CancelInfo{
					OrderID: order.ID,
					Memo:    internal.ErrFOKInsufficientLiquidity.Error(),
				}); err != nil {
					return err
				}
			}
			return nil
		}

		// 以防止崩潰復原後重複推送成交日誌 queue
		if infos != nil && ob.LastSeqID > recoverySeqID {
			if err = publish(js, "matched.BTC_USDT", infos); err != nil {
				return err
			}
		}
	case orderbook.OrderActionCancel:
		if err := ob.CancelOrder(order.ID); err != nil {
			slog.Error("cancel order error: ", err)
			return err
		}

		if ob.LastSeqID > recoverySeqID {
			if err := publish(js, "canceled.BTC_USDT", orderbook.CancelInfo{
				OrderID: order.ID,
				Memo:    "member manually cancelled",
			}); err != nil {
				return err
			}
		}
	}

	return nil
}

func snapshot(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(cfg.App.Snapshot.Period) * time.Second):
			slog.Info("start snapshot")
			if err := ob.Snapshot(cfg.App.Snapshot.Dir); err != nil {
				slog.Error("snapshot error: ", err)
				return
			}
			slog.Info("snapshot completed")
		}
	}
}

func publish(js jetstream.JetStream, subject string, payload interface{}) error {
	// TODO: need to optimized for retry mechanism
	tmp, _ := json.Marshal(payload)
	if _, err := js.PublishAsync(subject, tmp); err != nil {
		return err
	}
	return nil
}

func readMemUsage(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			fmt.Println("=============================================")
			fmt.Printf("Alloc = %v MiB\n", m.Alloc/1024/1024)
			fmt.Printf("TotalAlloc = %v MiB\n", m.TotalAlloc/1024/1024)
			fmt.Printf("Sys = %v MiB\n", m.Sys/1024/1024)
			fmt.Printf("NumGC = %v\n", m.NumGC)
			fmt.Println("=============================================")
		}
	}
}

func pprof() {
	if err := http.ListenAndServe("localhost:5000", nil); err != nil {
		panic(err)
	}
}
