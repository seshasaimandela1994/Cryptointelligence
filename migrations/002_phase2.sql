
CREATE TABLE IF NOT EXISTS transaction_receipts (
    tx_hash             CHAR(66)      PRIMARY KEY,
    block_number        BIGINT        NOT NULL,
    block_timestamp     TIMESTAMPTZ   NOT NULL,
    status              SMALLINT      NOT NULL DEFAULT 1,
    gas_used            BIGINT        NOT NULL,
    cumulative_gas_used BIGINT        NOT NULL,
    effective_gas_price NUMERIC(20,9),
    contract_address    CHAR(42),
    log_count           INTEGER       NOT NULL DEFAULT 0,
    indexed_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS event_logs (
    id              BIGSERIAL     PRIMARY KEY,
    tx_hash         CHAR(66)      NOT NULL,
    block_number    BIGINT        NOT NULL,
    block_timestamp TIMESTAMPTZ   NOT NULL,
    log_index       INTEGER       NOT NULL,
    address         CHAR(42)      NOT NULL,
    topic0          CHAR(66),
    topic1          CHAR(66),
    topic2          CHAR(66),
    topic3          CHAR(66),
    data            TEXT
);
CREATE INDEX IF NOT EXISTS idx_logs_tx_hash ON event_logs (tx_hash);
CREATE INDEX IF NOT EXISTS idx_logs_address ON event_logs (address, block_timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_logs_topic0  ON event_logs (topic0, block_timestamp DESC);
CREATE TABLE IF NOT EXISTS contracts (
    address          CHAR(42)      PRIMARY KEY,
    deployer         CHAR(42),
    deploy_tx_hash   CHAR(66),
    deploy_block     BIGINT,
    deploy_timestamp TIMESTAMPTZ,
    contract_type    TEXT,
    name             TEXT,
    symbol           TEXT,
    decimals         SMALLINT,
    verified         BOOLEAN       NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS event_signatures (
    topic0      CHAR(66)  PRIMARY KEY,
    name        TEXT      NOT NULL,
    signature   TEXT      NOT NULL
);
INSERT INTO event_signatures (topic0, name, signature) VALUES
    ('0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef','Transfer','Transfer(address,address,uint256)'),
    ('0x8c5be1e5ebec7d5bd14f71427d1e84f3dd0314c0f7b2291e5b200ac8c7c3b925','Approval','Approval(address,address,uint256)'),
    ('0xd78ad95fa46c994b6551d0da85fc275fe613ce37657fb8d5e3d130840159d822','Swap','Swap(address,uint256,uint256,uint256,uint256,address)'),
    ('0xe1fffcc4923d04b559f4d29a8bfc6cda04eb5b0d3c460751c2402c5c5cc9109c','Deposit','Deposit(address,uint256)'),
    ('0x7fcf532c15f0a6db0bd6d0e038bea71d30d808c7d98cb3bf7268a95bf5081b65','Withdrawal','Withdrawal(address,uint256)')
ON CONFLICT (topic0) DO NOTHING;
CREATE TABLE IF NOT EXISTS indexer_metrics (
    id             BIGSERIAL   PRIMARY KEY,
    recorded_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    blocks_per_sec NUMERIC(10,2),
    txs_per_sec    NUMERIC(10,2),
    current_block  BIGINT,
    lag_blocks     BIGINT,
    error_count    INTEGER     NOT NULL DEFAULT 0
);
