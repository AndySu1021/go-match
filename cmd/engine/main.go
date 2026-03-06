package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-match/config"
	"go-match/internal"
	"go-match/pkg/checkpoint"
	"go-match/pkg/orderbook"
	"log"
	"log/slog"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

var (
	ob *orderbook.OrderBook
	cp *checkpoint.Checkpoint
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

	tmpCp, err := checkpoint.NewCheckpoint(cfg.App.Snapshot.Dir)
	if err != nil {
		panic(err)
	}

	cp = tmpCp

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

	name := fmt.Sprintf("consumer:engine:%s", cfg.App.Instrument)

	// 清除舊有 Consumer 做災難復原
	_ = js.DeleteConsumer(ctx, cfg.App.Consumer.Stream, name)

	cons, err := js.CreateOrUpdateConsumer(ctx, cfg.App.Consumer.Stream, jetstream.ConsumerConfig{
		Name:          name,
		Durable:       name,
		FilterSubject: fmt.Sprintf("orders.%s", cfg.App.Instrument),
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:   ob.LastSeqID + 1,
		MaxAckPending: 10000, // 可以將這個調高
		MaxDeliver:    3,
		AckWait:       30 * time.Second, // 同時也要調高這個值，否則有可能重複投遞導致一張單撮 2 次以上
	})
	if err != nil {
		log.Printf("建立 Pull Consumer 失敗: %v", err)
		return
	}

	go consume(ctx, cons, js)
	go snapshot(ctx)

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

func consume(ctx context.Context, consumer jetstream.Consumer, js jetstream.JetStream) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msgs, err := consumer.Fetch(10000)
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
						// TODO: consider deliver to DLQ
						msg.Term()
						continue
					}
					break
				}

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
				if err = publish(js, fmt.Sprintf("canceled.%s", cfg.App.Instrument), orderbook.CancelInfo{
					OrderID: order.ID,
					Memo:    internal.ErrFOKInsufficientLiquidity.Error(),
				}, seqId); err != nil {
					return err
				}
			}
			return nil
		}

		// 以防止崩潰復原後重複推送成交日誌 queue
		if infos != nil && len(infos) > 0 && ob.LastSeqID > cp.LastSeq() {
			if err = publish(js, fmt.Sprintf("matched.%s", cfg.App.Instrument), infos, seqId); err != nil {
				return err
			}
			if err = cp.Save(seqId); err != nil {
				return err
			}
		}
	case orderbook.OrderActionCancel:
		if err := ob.CancelOrder(order.ID); err != nil {
			slog.Error("cancel order error: ", err)
			return err
		}

		if ob.LastSeqID > cp.LastSeq() {
			if err := publish(js, fmt.Sprintf("canceled.%s", cfg.App.Instrument), orderbook.CancelInfo{
				OrderID: order.ID,
				Memo:    "member manually cancelled",
			}, seqId); err != nil {
				return err
			}
			if err := cp.Save(seqId); err != nil {
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

func publish(js jetstream.JetStream, subject string, payload interface{}, reqId uint64) error {
	// TODO: need to optimized for retry mechanism
	tmp, _ := json.Marshal(payload)
	if _, err := js.PublishAsync(subject, tmp, jetstream.WithMsgID(strconv.FormatUint(reqId, 10))); err != nil {
		return err
	}
	return nil
}
