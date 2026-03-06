package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-match/config"
	"go-match/pkg/orderbook"
	"go-match/pkg/questdb"
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
	"github.com/shopspring/decimal"
)

var qdb *questdb.Client

func init() {
	start := time.Now()
	defer func() {
		slog.Info(fmt.Sprintf("init latency: %d ms", time.Since(start).Milliseconds()))
	}()

	time.Local = time.UTC

	cfg = &Config{
		App:     config.InitAppConfig(),
		Nats:    config.InitNatsConfig(),
		QuestDB: config.InitQuestDBConfig(),
	}

	tmpQdb, err := questdb.NewClient(cfg.QuestDB)
	if err != nil {
		panic(err)
	}
	qdb = tmpQdb
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

	name := fmt.Sprintf("consumer:quotation:%s", cfg.App.Instrument)

	cons, err := js.CreateOrUpdateConsumer(ctx, cfg.App.Consumer.Stream, jetstream.ConsumerConfig{
		Name:          name,
		Durable:       name,
		FilterSubject: fmt.Sprintf("matched.%s", cfg.App.Instrument),
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
		OptStartSeq:   1,
		MaxAckPending: 10000, // 可以將這個調高
		MaxDeliver:    3,
		AckWait:       30 * time.Second, // 同時也要調高這個值，否則有可能重複投遞導致一張單撮 2 次以上
	})
	if err != nil {
		log.Printf("建立 Pull Consumer 失敗: %v", err)
		return
	}

	go consume(ctx, cons)

	// 等待 Ctrl+C 或 SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	cancel()

	// graceful shutdown
	_, cancel = context.WithTimeout(context.Background(), 30*time.Second)

	log.Println("🛑 Shutting down...")
}

func consume(ctx context.Context, consumer jetstream.Consumer) {
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

				var infos []orderbook.MatchInfo
				if err = json.Unmarshal(msg.Data(), &infos); err != nil {
					slog.Error(fmt.Sprintf("unmarshal error: %s", err))
					return
				}

				for i := 0; i < len(infos); i++ {
					info := infos[i]
					if err = qdb.Sender.Table("quotation").
						Symbol("symbol", cfg.App.Instrument).
						Float64Column("price", decimal.New(int64(info.MatchedPrice), -int32(info.Decimals)).InexactFloat64()).
						Float64Column("quantity", decimal.New(int64(info.MatchedQuantity), -int32(info.Decimals)).InexactFloat64()).
						StringColumn("match_id", strconv.FormatUint(meta.Sequence.Stream, 10)).
						At(ctx, info.Timestamp); err != nil {
						slog.Error(fmt.Sprintf("write questdb error: %s", err))
						if meta.NumDelivered >= 3 {
							// TODO: consider deliver to DLQ
							msg.Term()
							continue
						}
						break
					}
				}

				if err = qdb.Sender.Flush(ctx); err != nil {
					slog.Error(fmt.Sprintf("flush error: %s", err))
					return
				}

				msg.Ack()
			}
		}
	}
}
