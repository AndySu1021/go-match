# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

go-match is a high-performance in-memory order matching engine for spot trading. It supports Limit, GTC (Good-Till-Cancel), IOC (Immediate-or-Cancel), and FOK (Fill-or-Kill) order types. Crash recovery is implemented via snapshots + WAL (Write-Ahead Log). NATS JetStream is used for order consumption and trade event distribution.

## Commands

```bash
# Build
go build -o build/engine ./cmd/engine
go build -o build/engine_grpc ./cmd/engine_grpc

# Run (active variant)
go run ./cmd/engine_grpc

# Tests
go test ./...

# Regenerate protobuf (requires buf)
buf generate proto/

# Start local dependencies (3-node NATS cluster + QuestDB)
docker-compose up
```

Configuration files are loaded from a `conf/` directory relative to the binary's working directory (`conf/app.yml`, `conf/nats.yml`).

## Architecture

### Active Entry Point

`cmd/engine_grpc` is the active variant. `cmd/engine` has commented-out NATS integration and a broken `svc.Snapshot()` call — treat it as legacy.

### Data Flow

```
NATS JetStream (orders.*)
        │
  gRPC Handler (service/match/handler/grpc.go)
        │  writes WAL before processing
        │
  MatchService (service/match/application/application.go)
        │
  OrderBook (pkg/orderbook/orderbook.go)
        │
NATS Publish (matched.* / canceled.*)
```

### OrderBook Core (`pkg/orderbook/orderbook.go`)

- Bids/Asks stored in Red-Black Trees sorted by price (bids descending, asks ascending)
- `swiss.Map` (Google's Swiss table implementation) for O(1) order ID lookup
- Soft-delete + lazy purge: orders are flagged deleted, purged when ≥1,000 deletions or ≥30% of a price level is deleted
- Prices and quantities use `Decimal8` fixed-point with 8 decimal places

### Order Type Matching Logic

| Type | Behavior |
|------|----------|
| Limit | Added directly to book, no matching |
| GTC | Match available liquidity, add remainder to book |
| IOC | Match available liquidity, cancel unfilled remainder |
| FOK | Two-phase: mock-match to verify full fill possible, then real match; cancel entirely if insufficient |

### Disaster Recovery

1. Load last snapshot (`snap_<instrument>.bin`) from configured snapshot dir
2. Read last confirmed `SeqID` from NATS KV store (`matcher.last_seq`)
3. Replay WAL records from `last_seq + 1` forward
4. Resume normal operation

### WAL (`service/match/types/types.go`)

- mmap'd file, pre-allocated for 100,000 records
- Header: `WalMetadata` (LastSeqID, WritePos, Magic, Version)
- Records: `WalRecord` (SeqID, ID, Instrument[32], Price[64], Quantity[64], Side, Type, Action)
- Atomic sync via `unix.Msync`

### Snapshots (`pkg/orderbook/orderbook.go`)

- Binary format with `SnapshotMetadata` header followed by fixed-size `OrderRecord` entries
- Bids and Asks written in parallel goroutines
- Atomic write: write to `.tmp` file → `Msync` → rename to final path

### gRPC Service (`proto/match.proto`)

Three RPCs: `AddOrder`, `CancelOrder`, `UpdateOrder`. Price and quantity are transmitted as strings to preserve arbitrary decimal precision.

## Key Package Roles

- `pkg/orderbook/` — core order book and matching logic (no external dependencies)
- `service/match/application/` — orchestrates business logic, converts proto ↔ internal types
- `service/match/handler/` — gRPC server, owns WAL writes
- `service/match/types/` — WAL types and DTOs
- `config/` — Viper-based YAML config loading with file watch support
- `pkg/checkpoint/` — tracks last processed NATS sequence ID
- `pkg/questdb/` — optional metrics sink (not wired into active engine)