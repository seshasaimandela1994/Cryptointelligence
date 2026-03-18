-- ============================================================
-- CryptoIntelligence — Unknown Wallet Radar
-- Monitors new addresses for suspicious behavior
-- ============================================================

CREATE TABLE IF NOT EXISTS wallet_radar (
    address         CHAR(42) PRIMARY KEY,
    first_seen      BIGINT,
    last_seen       BIGINT,
    tx_count        INTEGER DEFAULT 0,
    alert_type      VARCHAR(50),
    alert_reason    TEXT,
    alert_score     INTEGER DEFAULT 0,
    status          VARCHAR(20) DEFAULT 'watching',
    created_at      TIMESTAMP DEFAULT NOW(),
    updated_at      TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_radar_alert ON wallet_radar(alert_type);
CREATE INDEX IF NOT EXISTS idx_radar_score ON wallet_radar(alert_score DESC);

-- ALERT 1: New wallet high frequency (75% confidence)
INSERT INTO wallet_radar
    (address, first_seen, last_seen, tx_count,
     alert_type, alert_reason, alert_score)
SELECT from_address, MIN(block_number), MAX(block_number), COUNT(*),
    'HIGH_FREQUENCY_NEW',
    'New wallet with 10+ transactions in first 100 blocks',
    75
FROM token_transfers
WHERE from_address != '0x0000000000000000000000000000000000000000'
AND from_address NOT IN (
    SELECT address FROM address_labels WHERE category != 'retail_trader'
)
GROUP BY from_address
HAVING COUNT(*) >= 10
AND MAX(block_number) - MIN(block_number) <= 100
ON CONFLICT (address) DO NOTHING;

-- ALERT 2: Mixer interaction (95% confidence)
INSERT INTO wallet_radar
    (address, first_seen, last_seen, tx_count,
     alert_type, alert_reason, alert_score)
SELECT DISTINCT tt.from_address,
    MIN(tt.block_number), MAX(tt.block_number), COUNT(*),
    'MIXER_INTERACTION',
    'Wallet interacted with known mixer',
    95
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category = 'mixer'
AND tt.from_address != '0x0000000000000000000000000000000000000000'
AND tt.from_address NOT IN (SELECT address FROM wallet_radar)
GROUP BY tt.from_address
ON CONFLICT (address) DO NOTHING;

-- ALERT 3: Sanctions receipt (90% confidence)
INSERT INTO wallet_radar
    (address, first_seen, last_seen, tx_count,
     alert_type, alert_reason, alert_score)
SELECT DISTINCT tt.to_address,
    MIN(tt.block_number), MAX(tt.block_number), COUNT(*),
    'SANCTIONS_RECEIPT',
    'Received funds from sanctioned address',
    90
FROM token_transfers tt
JOIN entity_labels el ON tt.from_address = el.address
WHERE el.category = 'sanctions'
AND tt.to_address != '0x0000000000000000000000000000000000000000'
AND tt.to_address NOT IN (SELECT address FROM wallet_radar)
GROUP BY tt.to_address
ON CONFLICT (address) DO NOTHING;
