
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS blocks (
    number          BIGINT          PRIMARY KEY,
    hash            CHAR(66)        NOT NULL UNIQUE,
    parent_hash     CHAR(66)        NOT NULL,
    timestamp       TIMESTAMPTZ     NOT NULL,
    miner           CHAR(42)        NOT NULL,
    gas_used        BIGINT          NOT NULL,
    gas_limit       BIGINT          NOT NULL,
    base_fee_gwei   NUMERIC(20, 9),
    tx_count        INTEGER         NOT NULL DEFAULT 0,
    indexed_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS transactions (
    hash            CHAR(66)        PRIMARY KEY,
    block_number    BIGINT          NOT NULL,
    block_timestamp TIMESTAMPTZ     NOT NULL,
    tx_index        INTEGER         NOT NULL,
    from_address    CHAR(42)        NOT NULL,
    to_address      CHAR(42),
    value_wei       NUMERIC(38, 0)  NOT NULL DEFAULT 0,
    gas_price_gwei  NUMERIC(20, 9),
    gas_limit       BIGINT          NOT NULL,
    nonce           BIGINT          NOT NULL,
    input_data      TEXT,
    status          SMALLINT        NOT NULL DEFAULT 1,
    tx_type         SMALLINT        NOT NULL DEFAULT 0,
    receipt_status      SMALLINT,
    receipt_gas_used    BIGINT,
    contract_created    CHAR(42),
    indexed_at      TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tx_from  ON transactions (from_address, block_timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_tx_to    ON transactions (to_address, block_timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_tx_block ON transactions (block_number);

CREATE TABLE IF NOT EXISTS token_transfers (
    id              BIGSERIAL       PRIMARY KEY,
    tx_hash         CHAR(66)        NOT NULL,
    block_number    BIGINT          NOT NULL,
    block_timestamp TIMESTAMPTZ     NOT NULL,
    log_index       INTEGER         NOT NULL,
    token_address   CHAR(42)        NOT NULL,
    from_address    CHAR(42)        NOT NULL,
    to_address      CHAR(42)        NOT NULL,
    value           NUMERIC(78, 0)  NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS addresses (
    address          CHAR(42)       PRIMARY KEY,
    first_seen_block BIGINT,
    last_seen_block  BIGINT,
    tx_count         BIGINT         NOT NULL DEFAULT 0,
    is_contract      BOOLEAN        NOT NULL DEFAULT FALSE,
    contract_creator CHAR(42),
    updated_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS indexer_state (
    id          SERIAL      PRIMARY KEY,
    key         TEXT        NOT NULL UNIQUE,
    value       TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO indexer_state (key, value) VALUES
    ('last_indexed_block', '0'),
    ('indexer_status', 'stopped'),
    ('index_start_time', NOW()::TEXT)
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS entity_labels (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    address         CHAR(42)    NOT NULL,
    label           TEXT        NOT NULL,
    category        TEXT        NOT NULL,
    confidence_tier SMALLINT    NOT NULL DEFAULT 2,
    source          TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (address, label)
);

INSERT INTO entity_labels (address, label, category, confidence_tier, source) VALUES
    ('0xbe0eb53f46cd790cd13851d5eff43d12404d33e8', 'Binance Cold Wallet', 'exchange',   1, 'on-chain'),
    ('0x28c6c06298d514db089934071355e5743bf21d60', 'Binance Hot Wallet',  'exchange',   1, 'on-chain'),
    ('0xd8da6bf26964af9d7eed9e03e53415d37aa96045', 'Vitalik Buterin',     'individual', 1, 'osint'),
    ('0x7a250d5630b4cf539739df2c5dacb4c659f2488d', 'Uniswap V2 Router',  'defi',       1, 'on-chain'),
    ('0xe592427a0aece92de3edee1f18e0157c05861564', 'Uniswap V3 Router',  'defi',       1, 'on-chain'),
    ('0x00000000219ab540356cbb839cbe05303d7705fa', 'ETH 2.0 Deposit',    'protocol',   1, 'on-chain'),
    ('0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2', 'WETH Contract',      'token',      1, 'on-chain'),
    ('0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48', 'USDC Contract',      'token',      1, 'on-chain'),
    ('0xdac17f958d2ee523a2206206994597c13d831ec7', 'USDT Contract',      'token',      1, 'on-chain')
ON CONFLICT (address, label) DO NOTHING;

CREATE TABLE IF NOT EXISTS risk_scores (
    address     CHAR(42)    PRIMARY KEY,
    score       SMALLINT    NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
