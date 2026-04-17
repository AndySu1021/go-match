package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"go-match/config"
	"go-match/pkg/orderbook"
	"go-match/proto/gen"
	"go-match/service/match/application"
	"go-match/service/match/handler"
	"go-match/service/match/model"
	"go-match/service/match/types"
	"log"
	"log/slog"
	"net"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"path"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

const (
	DefaultMsgSize = 50 * 1024 * 1024
)

var (
	svc     model.IMatchService
	obMap   map[string]*orderbook.OrderBook
	walPath string
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

	walPath = path.Join(cfg.App.WAL.Dir, "wal.bin")

	svc = application.NewMatchService()
	obMap = make(map[string]*orderbook.OrderBook)

	var wg sync.WaitGroup
	wg.Add(len(cfg.App.Instruments))

	for i := 0; i < len(cfg.App.Instruments); i++ {
		instrument := cfg.App.Instruments[i]
		ob := orderbook.NewOrderBook(instrument, cfg.App.Snapshot.Dir)
		obMap[instrument] = ob
		svc.RegisterOrderBook(instrument, ob)

		go func(instrument string, ob *orderbook.OrderBook) {
			defer wg.Done()

			// Restore
			if err := ob.Restore(); err != nil {
				panic(fmt.Sprintf("order book restore error: %s", err))
			}

			// Replay
			if err := replay(instrument, ob.GetLastSeqID()); err != nil {
				panic(fmt.Sprintf("replay error: %s", err))
			}
		}(instrument, ob)
	}

	wg.Wait()
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())

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

	f, err := os.Create(walPath)
	if err != nil {
		panic(err)
	}

	headerSize := binary.Size(types.WalMetadata{})
	recordSize := binary.Size(types.WalRecord{})
	totalSize := headerSize + recordSize*types.WalSize

	if err = f.Truncate(int64(totalSize)); err != nil {
		panic(err)
	}

	// 3. mmap 整個檔案用於寫入
	wal, err := unix.Mmap(
		int(f.Fd()), 0, totalSize,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_SHARED,
	)
	if err != nil {
		panic(err)
	}

	meta := types.WalMetadata{
		LastSeqID: 0,
		WritePos:  uint32(headerSize),
		Magic:     1,
		Version:   1,
	}

	*(*types.WalMetadata)(unsafe.Pointer(&wal[0])) = meta
	unix.Msync(wal, unix.MS_SYNC)

	// TODO: add health check
	gen.RegisterMatchServiceServer(srv, handler.NewMatchGrpcHandler(svc, types.WalObj{
		Meta: meta,
		File: wal,
	}))

	// register monitor or background process
	for i := 0; i < len(cfg.App.Instruments); i++ {
		go snapshot(ctx, cfg.App.Instruments[i])
	}

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

	shutdownFinished := make(chan struct{})
	go func() {
		srv.GracefulStop()
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

	unix.Msync(wal, unix.MS_SYNC)
	unix.Munmap(wal)

	for i := 0; i < len(cfg.App.Instruments); i++ {
		instrument := cfg.App.Instruments[i]
		if _, ok := obMap[instrument]; !ok {
			return
		}
		ob := obMap[instrument]
		if err = ob.Snapshot(cfg.App.Snapshot.Dir); err != nil {
			slog.Error("snapshot error: ", err)
			return
		}
	}
}

func snapshot(ctx context.Context, instrument string) {
	if _, ok := obMap[instrument]; !ok {
		return
	}

	ob := obMap[instrument]

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(cfg.App.Snapshot.Period) * time.Second):
			slog.Info("start snapshot...")
			if err := ob.Snapshot(cfg.App.Snapshot.Dir); err != nil {
				slog.Error("snapshot error: ", err)
				return
			}
			slog.Info("snapshot completed")
		}
	}
}

func replay(instrument string, lastSeqId uint64) error {
	f, err := os.Open(walPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open wal file error: %s", err)
	}
	defer f.Close()

	fi, _ := f.Stat()

	// mmap 整個檔案，避免大量 read syscall
	data, err := unix.Mmap(
		int(f.Fd()), 0, int(fi.Size()),
		unix.PROT_READ, unix.MAP_SHARED,
	)
	if err != nil {
		panic(fmt.Sprintf("mmap error: %s", err))
	}
	defer unix.Munmap(data)

	ctx := context.Background()

	header := *(*types.WalMetadata)(unsafe.Pointer(&data[0]))
	headerSize := binary.Size(types.WalMetadata{})
	recordSize := binary.Size(types.WalRecord{})
	offset := uint64(headerSize) + (lastSeqId * uint64(recordSize))
	total := (header.WritePos - uint32(offset)) / uint32(recordSize)
	for i := 0; i < int(total); i++ {
		record := *(*types.WalRecord)(unsafe.Pointer(&data[offset]))
		params := record.ToOrderParams()
		offset += uint64(recordSize)
		if params.Instrument != instrument {
			continue
		}
		switch params.Action {
		case types.ActionAdd:
			_, _ = svc.AddOrder(ctx, types.AddOrderParams{
				Instrument: params.Instrument,
				ID:         params.ID,
				Price:      params.Price,
				Quantity:   params.Quantity,
				Side:       params.Side,
				Type:       params.Type,
			})
		case types.ActionCancel:
			_ = svc.CancelOrder(ctx, types.CancelOrderParams{
				Instrument: params.Instrument,
				ID:         params.ID,
			})
		case types.ActionUpdate:
			_ = svc.UpdateOrder(ctx, types.UpdateOrderParams{
				Instrument: params.Instrument,
				ID:         params.ID,
				Price:      params.Price,
				Quantity:   params.Quantity,
			})
		default:
			return fmt.Errorf("unknown action: %d", params.Action)
		}
	}

	return nil
}
