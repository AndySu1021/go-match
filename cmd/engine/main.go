package main

import (
	"context"
	"fmt"
	"go-match/config"
	"go-match/proto/gen"
	"go-match/service/match/application"
	"go-match/service/match/handler"
	"go-match/service/match/model"
	"log"
	"log/slog"
	"net"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	DefaultMsgSize = 50 * 1024 * 1024
)

var (
	svc model.IMatchService
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

	svc = application.NewMatchService(cfg.App)
	if err := svc.Restore(context.Background()); err != nil {
		panic(fmt.Sprintf("order book restore error: %s", err))
	}
}

func main() {
	// HA 多節點連線，任一節點掛掉自動 failover
	//nc, err := nats.Connect(
	//	strings.Join(cfg.Nats.Urls, ","),
	//	nats.RetryOnFailedConnect(true),
	//	nats.MaxReconnects(-1), // 無限重連
	//	nats.ReconnectWait(2*time.Second),
	//	nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
	//		slog.Info("⚠️  Disconnected: ", err)
	//	}),
	//	nats.ReconnectHandler(func(nc *nats.Conn) {
	//		slog.Info("✅ Reconnected to: ", nc.ConnectedUrl())
	//	}),
	//)
	//if err != nil {
	//	slog.Error("連線失敗: ", err)
	//	panic(err)
	//}
	//defer nc.Drain() // Drain 確保訊息處理完才關閉
	//
	//// 建立 JetStream context
	//js, err := jetstream.New(nc, jetstream.WithPublishAsyncMaxPending(1024))
	//if err != nil {
	//	log.Fatalf("JetStream 初始化失敗: %v", err)
	//}

	ctx, cancel := context.WithCancel(context.Background())

	//name := fmt.Sprintf("consumer:engine:%s", cfg.App.Instrument)

	// 清除舊有 Consumer 做災難復原
	//_ = js.DeleteConsumer(ctx, cfg.App.Consumer.Stream, name)
	//
	//cons, err := js.CreateOrUpdateConsumer(ctx, cfg.App.Consumer.Stream, jetstream.ConsumerConfig{
	//	Name:          name,
	//	Durable:       name,
	//	FilterSubject: fmt.Sprintf("orders.%s", cfg.App.Instrument),
	//	AckPolicy:     jetstream.AckExplicitPolicy,
	//	DeliverPolicy: jetstream.DeliverByStartSequencePolicy,
	//	OptStartSeq:   ob.LastSeqID + 1,
	//	MaxAckPending: 10000, // 可以將這個調高
	//	MaxDeliver:    3,
	//	AckWait:       30 * time.Second, // 同時也要調高這個值，否則有可能重複投遞導致一張單撮 2 次以上
	//})
	//if err != nil {
	//	log.Printf("建立 Pull Consumer 失敗: %v", err)
	//	return
	//}

	//go consume(ctx, cons, js)

	lis, err := net.Listen("tcp", cfg.App.Net.Addr)
	if err != nil {
		panic(fmt.Sprintf("failed to listed: %v", err))
	}

	srv := grpc.NewServer(
		grpc.MaxRecvMsgSize(DefaultMsgSize),
		grpc.MaxSendMsgSize(DefaultMsgSize),
		// KeepAlive 參數設置
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Minute, // 默認 2 小時
			Timeout: 20 * time.Second, // 如果 20 秒內沒有收到回應則斷開連接
			// 連接管理：防止連接永久存在
			MaxConnectionIdle:     30 * time.Minute, // 30 分鐘無活動則發送 GoAway
			MaxConnectionAge:      2 * time.Hour,    // 連接存活最長 2 小時後發送 GoAway
			MaxConnectionAgeGrace: 30 * time.Second, // GoAway 後給客戶端 30 秒完成請求再強制關閉
		}),
		// 設置客戶端的 keep-alive 行為
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             1 * time.Minute, // 要求客戶端每1分鐘內只能發送一次 ping
			PermitWithoutStream: true,            // 即使沒有活動的流，客戶端也能發送 keep-alive ping
		}),
		//grpc.ChainUnaryInterceptor(
		//	apmgrpc.NewUnaryServerInterceptor(apmgrpc.WithRecovery()),
		//	ApmMetaInterceptor(),
		//	PanicLoggingInterceptor(),
		//	LoggingInterceptor(),
		//),
		//grpc.ChainStreamInterceptor(
		//	apmgrpc.NewStreamServerInterceptor(),
		//	StreamLoggingInterceptor,
		//),
		//grpc.StatsHandler(&connStatsHandler{}), // 註冊 StatsHandler
	)

	// TODO: add health check
	gen.RegisterMatchServiceServer(srv, handler.NewMatchGrpcHandler(svc))

	// register monitor or background process
	go svc.Snapshot(ctx)

	go func() {
		slog.Info(fmt.Sprintf("start grpc server at %s", cfg.App.Net.Addr))
		if err = srv.Serve(lis); err != nil {
			panic(fmt.Sprintf("server error: %v", err))
		}
	}()

	// 等待 Ctrl+C 或 SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	slog.Info("🛑 Shutting down...")

	// 4. 設定一個超時保護 (Optional)
	// 如果有些請求卡住太久，我們不希望伺服器永遠關不掉
	shutdownFinished := make(chan struct{})
	go func() {
		srv.GracefulStop() // gRPC 提供的優雅關閉函式
		close(shutdownFinished)
	}()

	cancel()

	select {
	case <-shutdownFinished:
		log.Println("所有請求已處理完畢，伺服器成功關閉")
	case <-time.After(30 * time.Second): // 強制關閉超時時間
		log.Println("超時！部分請求可能未處理完成，強制關閉中...")
		srv.Stop() // force close
	}
}

//func consume(ctx context.Context, consumer jetstream.Consumer, js jetstream.JetStream) {
//	for {
//		select {
//		case <-ctx.Done():
//			return
//		default:
//			msgs, err := consumer.Fetch(10000)
//			if err != nil && !errors.Is(err, jetstream.ErrNoMessages) {
//				log.Printf("Fetch 錯誤: %v", err)
//				continue
//			}
//
//			if msgs.Error() != nil {
//				slog.Error("fetch stream error: ", msgs.Error())
//				return
//			}
//
//			for msg := range msgs.Messages() {
//				meta, err := msg.Metadata()
//				if err != nil {
//					panic(err)
//				}
//
//				slog.Info(fmt.Sprintf("📦 [Pull] subject=%s data=%s seq_id=%d", msg.Subject(), string(msg.Data()), meta.Sequence.Stream))
//
//				if err = processOrder(ctx, meta.Sequence.Stream, msg.Data(), js); err != nil {
//					slog.Error("process order failed: ", err)
//					if meta.NumDelivered >= 3 {
//						// TODO: consider deliver to DLQ
//						msg.Term()
//						continue
//					}
//					break
//				}
//
//				msg.Ack()
//			}
//		}
//	}
//}

//func processOrder(ctx context.Context, seqId uint64, data []byte, js jetstream.JetStream) error {
//	order := orderbook.Order{}
//	if err := json.Unmarshal(data, &order); err != nil {
//		return err
//	}
//
//	ob.LastSeqID = seqId
//
//	switch order.Action {
//	case orderbook.OrderActionNormal:
//		res, infos, err := ob.Match(order)
//		if err != nil {
//			slog.Error(fmt.Sprintf("match error: %s", err))
//			return err
//		}
//
//		if !res.Success {
//			if errors.Is(res.Error, internal.ErrFOKInsufficientLiquidity) {
//				if err = publish(js, fmt.Sprintf("canceled.%s", cfg.App.Instrument), orderbook.CancelInfo{
//					OrderID: order.ID,
//					Memo:    internal.ErrFOKInsufficientLiquidity.Error(),
//				}, seqId); err != nil {
//					return err
//				}
//			}
//			return nil
//		}
//
//		// 以防止崩潰復原後重複推送成交日誌 queue
//		if infos != nil && len(infos) > 0 && ob.LastSeqID > cp.LastSeq() {
//			if err = publish(js, fmt.Sprintf("matched.%s", cfg.App.Instrument), infos, seqId); err != nil {
//				return err
//			}
//			if err = cp.Save(seqId); err != nil {
//				return err
//			}
//		}
//	case orderbook.OrderActionCancel:
//		if err := ob.CancelOrder(order.ID); err != nil {
//			slog.Error("cancel order error: ", err)
//			return err
//		}
//
//		if ob.LastSeqID > cp.LastSeq() {
//			if err := publish(js, fmt.Sprintf("canceled.%s", cfg.App.Instrument), orderbook.CancelInfo{
//				OrderID: order.ID,
//				Memo:    "member manually cancelled",
//			}, seqId); err != nil {
//				return err
//			}
//			if err := cp.Save(seqId); err != nil {
//				return err
//			}
//		}
//	}
//
//	return nil
//}

//func publish(js jetstream.JetStream, subject string, payload interface{}, reqId uint64) error {
//	// TODO: need to optimized for retry mechanism
//	tmp, _ := json.Marshal(payload)
//	if _, err := js.PublishAsync(subject, tmp, jetstream.WithMsgID(strconv.FormatUint(reqId, 10))); err != nil {
//		return err
//	}
//	return nil
//}
