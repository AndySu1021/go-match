
CREATE TABLE IF NOT EXISTS quotation (
    timestamp TIMESTAMP,
    symbol SYMBOL CAPACITY 256 CACHE INDEX CAPACITY 512,
    price DOUBLE,
    quantity DOUBLE,
    match_id VARCHAR
) TIMESTAMP(timestamp)
PARTITION BY day
TTL 90 DAY
DEDUP UPSERT KEYS (timestamp, match_id);

CREATE MATERIALIZED VIEW quotation_1m
       WITH BASE quotation
       REFRESH IMMEDIATE
       AS
       SELECT
           timestamp,
           symbol,
           first(price) AS open,
           max(price) AS high,
           min(price) AS low,
           last(price) AS close,
           sum(quantity) AS volume,
           count() AS trade_count
       FROM quotation
       SAMPLE BY 1m
       ALIGN TO CALENDAR;

CREATE MATERIALIZED VIEW quotation_5m
       WITH BASE quotation_1m
       REFRESH IMMEDIATE
       AS
SELECT
    timestamp,
    symbol,
    first(open) AS open,
    max(high) AS high,
    min(low) AS low,
    last(close) AS close,
    sum(volume) AS volume,
    sum(trade_count) AS trade_count
FROM quotation_1m
    SAMPLE BY 5m
    ALIGN TO CALENDAR;

CREATE MATERIALIZED VIEW quotation_15m
       WITH BASE quotation_5m
       REFRESH IMMEDIATE
       AS
SELECT
    timestamp,
    symbol,
    first(open) AS open,
    max(high) AS high,
    min(low) AS low,
    last(close) AS close,
    sum(volume) AS volume,
    sum(trade_count) AS trade_count
FROM quotation_5m
    SAMPLE BY 15m
    ALIGN TO CALENDAR;

ALTER MATERIALIZED VIEW quotation_1m SET REFRESH LIMIT 7 DAY;
ALTER MATERIALIZED VIEW quotation_5m SET REFRESH LIMIT 7 DAY;
ALTER MATERIALIZED VIEW quotation_15m SET REFRESH LIMIT 7 DAY;