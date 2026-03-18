-- ============================================================
-- CryptoIntelligence — Entity Resolution & Identity Links
-- Maps wallets to real-world identities
-- ============================================================

CREATE TABLE IF NOT EXISTS identity_links (
    id           SERIAL PRIMARY KEY,
    address      CHAR(42) NOT NULL,
    identity     VARCHAR(255) NOT NULL,
    platform     VARCHAR(50) NOT NULL,
    confidence   INTEGER DEFAULT 70,
    source       VARCHAR(50) DEFAULT 'on-chain',
    verified     BOOLEAN DEFAULT FALSE,
    created_at   TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_identity_address ON identity_links(address);
CREATE INDEX IF NOT EXISTS idx_identity_platform ON identity_links(platform);

-- Known verified identities
INSERT INTO identity_links (address, identity, platform, confidence, source, verified) VALUES
('0xd8da6bf26964af9d7eed9e03e53415d37aa96045', 'vitalik.eth', 'ENS', 100, 'osint', TRUE),
('0xab5801a7d398351b8be11c439e05c5b3259aec9b', 'Vitalik Buterin 2', 'osint', 100, 'osint', TRUE),
('0xbe0eb53f46cd790cd13851d5eff43d12404d33e8', 'Binance Cold Wallet', 'exchange', 100, 'osint', TRUE),
('0xf977814e90da44bfa03b6295a0616a897441acec', 'Binance Hot Wallet', 'exchange', 100, 'osint', TRUE),
('0x503828976d22510aad0201ac7ec88293211d23da', 'Coinbase Hot Wallet 1', 'exchange', 100, 'osint', TRUE),
('0x7f367cc41522ce07553e823bf3be79a889debe1b', 'Lazarus Group (DPRK)', 'sanctions', 100, 'ofac', TRUE),
('0x910cbd523d972eb0a6f4cae4618ad62622b39dbf', 'Tornado Cash 100 ETH Pool', 'mixer', 100, 'ofac', TRUE)
ON CONFLICT DO NOTHING;

-- KYC-linked wallets (sent to exchange)
INSERT INTO identity_links (address, identity, platform, confidence, source, verified)
SELECT DISTINCT tt.from_address,
    'KYC-Linked via ' || el.label,
    'exchange_kyc', 75, 'exchange_flow', FALSE
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category = 'exchange'
AND tt.from_address != '0x0000000000000000000000000000000000000000'
AND NOT EXISTS (SELECT 1 FROM identity_links il WHERE il.address = tt.from_address)
ON CONFLICT DO NOTHING;

-- Exchange funded wallets
INSERT INTO identity_links (address, identity, platform, confidence, source, verified)
SELECT DISTINCT tt.to_address,
    'Funded by ' || el.label,
    'exchange_funded', 70, 'exchange_flow', FALSE
FROM token_transfers tt
JOIN entity_labels el ON tt.from_address = el.address
WHERE el.category = 'exchange'
AND tt.to_address != '0x0000000000000000000000000000000000000000'
AND NOT EXISTS (SELECT 1 FROM identity_links il WHERE il.address = tt.to_address)
ON CONFLICT DO NOTHING;

-- 2-hop KYC linking
INSERT INTO identity_links (address, identity, platform, confidence, source, verified)
SELECT DISTINCT tt1.from_address,
    '2-hop KYC Link via ' || el.label,
    'indirect_kyc', 55, 'hop_analysis', FALSE
FROM token_transfers tt1
JOIN token_transfers tt2 ON tt1.to_address = tt2.from_address
JOIN entity_labels el ON tt2.to_address = el.address
WHERE el.category = 'exchange'
AND tt1.from_address != '0x0000000000000000000000000000000000000000'
AND NOT EXISTS (SELECT 1 FROM identity_links il WHERE il.address = tt1.from_address)
ON CONFLICT DO NOTHING;
