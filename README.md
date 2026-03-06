# go-match

高性能純記憶體撮合引擎，支援限價單（Limit）、GTC、IOC、FOK 四種訂單類型，搭配快照機制實現災難復原。透過 NATS JetStream 接收訂單並發布成交結果，適用於現貨交易場景。

---

## 架構概覽

```
NATS JetStream (orders.*)
        │
        ▼
┌───────────────────┐
│   Match Engine    │  ← 純記憶體撮合，單一 goroutine 有序處理
│                   │
│  ┌─────────────┐  │
│  │  OrderBook  │  │  Asks / Bids (已排序 []PriceLevel)
│  │  OrderMap   │  │  swiss.Map[uint64, OrderLocation]
│  └─────────────┘  │
└────────┬──────────┘
         │
    ┌────┴────┐
    ▼         ▼
matched.*  canceled.*     ← NATS JetStream Publish
    
         │
         ▼
   Snapshot (.bin)        ← Protobuf 序列化，原子寫入
```

**訊息流：**
```
NATS(orders.*) → Match Engine → NATS(matched.*)
                             → NATS(canceled.*)
```

---

## 訂單類型

| 類型 | 說明 |
|------|------|
| `Limit` | 限價掛單，直接進入訂單簿，不觸發撮合 |
| `GTC` (Good Till Cancel) | 以限價撮合，剩餘量掛回訂單簿 |
| `IOC` (Immediate or Cancel) | 立即以市價撮合，未成交部分直接取消 |
| `FOK` (Fill or Kill) | 必須完全成交，否則整筆取消，不修改訂單簿 |

---

## 資料結構設計

### OrderBook

```
OrderBook
├── Asks []PriceLevel   // 賣單，依價格遞增排序
├── Bids []PriceLevel   // 買單，依價格遞減排序
└── OrderMap            // swiss.Map[orderID → OrderLocation]，O(1) 查找
```

### PriceLevel

每個價格層級維護一個有序訂單佇列，使用 **Soft Delete** 標記已取消訂單（`IsRemoved = true`），避免頻繁記憶體重分配。

```
PriceLevel
├── Price        uint64
├── Orders       []LevelOrder   // 時間優先佇列（FIFO）
└── RemovedCount int            // 已標記刪除數量
```

### 懶惰清理（Lazy Purge）

滿足以下任一條件時，觸發全域清理，重建訂單簿與 OrderMap：

- 累積取消訂單數 ≥ **1,000 筆**（`PurgeThresholdOrders`）
- 取消比例 ≥ **30%**（`PurgeThresholdRatio`）

此設計讓熱路徑（撮合 / 取消）無需移動陣列元素，批次清理攤銷成本。

---

## 撮合邏輯

撮合遵循**價格優先、時間優先（Price-Time Priority）**原則：

1. **價格優先**：Asks 依價格由低到高排序；Bids 依價格由高到低排序，優先與最優對手價撮合。
2. **時間優先**：同價位訂單以進場時間（FIFO）決定成交順序。

FOK 採兩階段處理：
- **Phase 1**：掃描訂單簿確認可用量是否足夠，純讀不修改。
- **Phase 2**：確認可撮合才執行實際成交，保證原子性。

---

## 災難復原

系統以快照（Snapshot）+ NATS JetStream Sequence 實現崩潰後零遺漏復原：

```
啟動流程：
1. 載入 snapshot.bin（Protobuf 格式）→ 重建記憶體訂單簿
2. 從 NATS KV（matcher.last_seq）讀取上次確認的 SeqID
3. 建立 Consumer，從 last_seq + 1 開始重播訊息
4. 重播期間不對外發布成交事件（避免重複推送）
```

快照採**原子寫入**（先寫 `.tmp`，再 `rename`），防止寫入中途崩潰損壞前一版檔案。

### 性能指標（實測）

| 項目 | 數值 |
|------|------|
| 1,000 萬筆訂單崩潰復原時間 | ~1 秒（±10%） |
| 1,000 萬筆活躍訂單記憶體佔用 | ~1.6 GB |
| 1,000 萬筆訂單快照所需時間 | 700–800 ms |
| 1,000 萬筆活躍訂單快照檔案大小 | ~170 MB |
| Producer → NATS 吞吐量 | ~10,000 TPS |

---

## 快速開始

### 環境需求

- Go 1.21+
- NATS Server（建議 3 節點叢集）

### NATS Stream 建立

```shell
# 訂單輸入 Stream
nats stream add SPOT_ORDERS \
    --subjects="orders.*" \
    --replicas=3 \
    --storage="file" \
    --retention="limits" \
    --discard="old" \
    --max-age="3d" \
    --max-bytes="3G" \
    -s nats://nats1:4222

# 成交結果 Stream
nats stream add MATCH_ORDERS \
    --subjects="matched.*" \
    --replicas=3 \
    --storage="file" \
    --retention="limits" \
    --discard="old" \
    --max-age="3d" \
    --max-bytes="3G" \
    -s nats://nats1:4222

# 取消通知 Stream
nats stream add CANCEL_ORDERS \
    --subjects="canceled.*" \
    --replicas=3 \
    -s nats://nats1:4222
```

### 發送測試訂單

```shell
# 掛限價買單
nats pub orders.BTC_USDT \
  '{"id":1,"price":50000,"quantity":10,"decimals":0,"action":1,"side":1,"type":1}' \
  -s nats://nats1:4222

# GTC 賣單（部分成交後剩餘掛回）
nats pub orders.BTC_USDT \
  '{"id":2,"price":48000,"quantity":25,"decimals":0,"action":1,"side":2,"type":4}' \
  -s nats://nats1:4222

# FOK 買單（量不足則整筆取消）
nats pub orders.BTC_USDT \
  '{"id":3,"price":50000,"quantity":15,"decimals":0,"action":1,"side":2,"type":2}' \
  -s nats://nats1:4222

# 取消訂單
nats pub orders.BTC_USDT \
  '{"id":1,"action":2}' \
  -s nats://nats1:4222
```

### 訂閱成交結果

```shell
nats sub matched.BTC_USDT -s nats://nats1:4222
```

---

## 效能測試

### 執行 Benchmark

```shell
go test -bench=. -benchmem -count=3 ./... > before.txt
```

### 效能比較（使用 benchstat）

```shell
go install golang.org/x/perf/cmd/benchstat@latest

# 修改程式碼前後各跑一次
go test -bench=. -benchmem -count=5 ./... > before.txt
# （修改後）
go test -bench=. -benchmem -count=5 ./... > after.txt

benchstat before.txt after.txt
```

> `delta` 為負數代表性能改善；`p < 0.05` 代表統計上顯著。

### Benchmark 項目

| Benchmark | 說明 |
|-----------|------|
| `BenchmarkAddOrderBuy/Sell` | 純掛單，無撮合 |
| `BenchmarkRemoveOrder` | 取消訂單（Soft Delete） |
| `BenchmarkMatchLimit` | 限價掛單（不觸發撮合） |
| `BenchmarkMatchGTC` | GTC 撮合（有深度訂單簿） |
| `BenchmarkMatchIOC` | IOC 撮合 |
| `BenchmarkMatchFOKSuccess/Fail` | FOK 成功 / 失敗路徑 |
| `BenchmarkMatchGTCByDepth` | 不同訂單簿深度（10 / 100 / 500 / 1000）對撮合的影響 |
| `BenchmarkShouldPurge` | 清理條件判斷 |
| `BenchmarkPurgeLevels` | 批次清理已刪除訂單 |
| `BenchmarkRebuildOrderMap` | 重建索引 |
| `BenchmarkConcurrentMatch` | 並行撮合壓力測試 |

### pprof 分析

```shell
# 擷取 Heap Profile
curl http://localhost:5000/debug/pprof/heap > ./runtime/heap.pprof

# 對比兩份 Heap（找記憶體洩漏）
go tool pprof -http=:8081 -base ./runtime/heap.pprof ./runtime/snap-heap.pprof

# CPU + Memory Profile
go test -bench=. -benchmem \
    -memprofile=mem.out \
    -cpuprofile=cpu.out ./...

go tool pprof -http=:8080 cpu.out
```

---

## 維運指令

```shell
# 清除 KV 狀態（重置 Sequence 追蹤）
nats kv purge SYSTEM_STATE matcher.last_seq -s nats://nats1:4222

# 查看 Stream 資訊
nats stream list -s nats://nats1:4222

# 清空訂單 Stream
nats stream purge SPOT_ORDERS -s nats://nats1:4222

# 刪除 Stream
nats stream rm SPOT_ORDERS -s nats://nats1:4222
nats stream rm MATCH_ORDERS -s nats://nats1:4222
```

---

## 設計取捨

| 決策                     | 原因                                        |
|------------------------|-------------------------------------------|
| 純記憶體訂單簿                | 避免 I/O 瓶頸，撮合延遲維持在微秒級                      |
| Soft Delete + 懶惰清理     | 熱路徑零記憶體重分配，批次攤銷清理成本                       |
| `swiss.Map` 取代標準 `map` | 更低的記憶體佔用與更優的快取局部性                         |
| Protobuf 快照            | 序列化速度快、檔案體積小，比 JSON 節省約 60% 空間            |
| `MaxAckPending = 1`    | 確保訂單有序處理，避免重複撮合；如需提升吞吐量可調高並同步延長 `AckWait` |
| 原子快照（tmp → rename）     | 防止快照寫入中途崩潰導致檔案損毀                          |
| 當前消費 SeqID 快照          | 減少撮合 match event 重送，最多 1 筆                |
