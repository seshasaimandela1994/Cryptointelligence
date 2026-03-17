
-- ============================================================
-- CryptoIntelligence — Additional Categories V2
-- ============================================================

-- WHALE (top 500 by transaction count, not already labelled)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'Whale' as label,
    'trader' as category,
    'whale' as sub_category,
    80 as confidence,
    'volume_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT from_address, COUNT(*) as tx_count
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    ORDER BY tx_count DESC
    LIMIT 500
) whales
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- HIGH FREQUENCY TRADER (100+ transactions)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'High Frequency Trader' as label,
    'trader' as category,
    'high_frequency_trader' as sub_category,
    80 as confidence,
    'frequency_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT from_address, COUNT(*) as tx_count
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    HAVING COUNT(*) > 100
) hft
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- ARBITRAGE BOT (many txs, multiple tokens, same blocks repeatedly)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'Arbitrage Bot' as label,
    'bot' as category,
    'arbitrage_bot' as sub_category,
    85 as confidence,
    'pattern_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT
        from_address,
        COUNT(*) as tx_count,
        COUNT(DISTINCT token_address) as token_count,
        COUNT(DISTINCT block_number) as block_count
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    HAVING COUNT(*) > 20
       AND COUNT(DISTINCT token_address) > 3
       AND COUNT(*) > COUNT(DISTINCT block_number) * 2
) arb
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- RETAIL TRADER (1-10 transactions, not a bot)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'Retail Trader' as label,
    'retail_trader' as category,
    'retail_trader' as sub_category,
    65 as confidence,
    'volume_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT from_address, COUNT(*) as tx_count
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    HAVING COUNT(*) BETWEEN 1 AND 10
) retail
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- DORMANT WALLET (first seen in early blocks, very few txs)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'Dormant Wallet' as label,
    'retail_trader' as category,
    'dormant_wallet' as sub_category,
    65 as confidence,
    'age_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT
        from_address,
        MIN(block_number) as first_block,
        MAX(block_number) as last_block,
        COUNT(*) as tx_count
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    HAVING COUNT(*) <= 3
       AND MIN(block_number) < (SELECT MIN(number) + 500 FROM blocks)
       AND MAX(block_number) < (SELECT MAX(number) - 1000 FROM blocks)
) dormant
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- MULTISIG WALLET (Gnosis Safe interactions)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'Multisig Wallet' as label,
    'contract' as category,
    'multisig_wallet' as sub_category,
    85 as confidence,
    'contract_analysis' as source,
    'classifier' as labelled_by
FROM token_transfers tt
WHERE tt.from_address IN (
    SELECT DISTINCT from_address
    FROM token_transfers
    WHERE to_address IN (
        '0xa6b71e26c5e0845f74c812102ca7114b6a896ab2',
        '0x69f4d1788e39c87893c980c06edf4b7f686e2938',
        '0x76e2cfc1f5fa8f6a5b3fc4c8f4788d0657516f42'
    )
)
AND tt.from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- NFT TRADER (interacts with NFT marketplaces)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'NFT Trader' as label,
    'nft_trader' as category,
    'nft_trader' as sub_category,
    75 as confidence,
    'nft_interaction' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category = 'nft'
  AND tt.from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- DAO MEMBER (interacts with governance tokens)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'DAO Member' as label,
    'defi_user' as category,
    'dao_member' as sub_category,
    70 as confidence,
    'governance_interaction' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.token_address = el.address
WHERE el.label IN (
    'Uniswap Token (UNI)',
    'Aave Token (AAVE)',
    'Curve DAO Token (CRV)',
    'Balancer Token (BAL)',
    'Compound Token (COMP)',
    'MakerDAO MKR Token',
    'CoW Token (COW)',
    '0x Token (ZRX)',
    'Convex Token (CVX)'
)
AND tt.from_address NOT IN (SELECT address FROM address_labels)
AND tt.from_address != '0x0000000000000000000000000000000000000000'
ON CONFLICT (address) DO NOTHING;

-- MEV BUILDER (extremely high tx count per block)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'MEV Builder' as label,
    'bot' as category,
    'mev_builder' as sub_category,
    90 as confidence,
    'mev_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT
        from_address,
        COUNT(*) as total_txs,
        COUNT(DISTINCT block_number) as blocks,
        MAX(COUNT(*)) OVER (PARTITION BY from_address) as max_per_block
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    HAVING COUNT(*) > 50
       AND COUNT(*) > COUNT(DISTINCT block_number) * 4
) mev_builders
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- SCAM CONTRACT (receives from many but never sends)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    to_address,
    'Suspicious Contract' as label,
    'high_risk' as category,
    'scam_contract' as sub_category,
    60 as confidence,
    'pattern_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT
        to_address,
        COUNT(DISTINCT from_address) as senders
    FROM token_transfers
    WHERE to_address != '0x0000000000000000000000000000000000000000'
    GROUP BY to_address
    HAVING COUNT(DISTINCT from_address) > 50
       AND to_address NOT IN (
           SELECT from_address FROM token_transfers
       )
       AND to_address NOT IN (
           SELECT address FROM entity_labels
       )
) suspicious
WHERE to_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- PHISHING WALLET (sends to many unique addresses, low value)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'Potential Phishing' as label,
    'high_risk' as category,
    'phishing_wallet' as sub_category,
    65 as confidence,
    'pattern_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT
        from_address,
        COUNT(DISTINCT to_address) as unique_receivers,
        COUNT(*) as tx_count,
        AVG(CAST(value AS NUMERIC)) as avg_value
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    HAVING COUNT(DISTINCT to_address) > 100
       AND COUNT(DISTINCT to_address) > COUNT(*) * 0.8
       AND AVG(CAST(value AS NUMERIC)) < 1000000
) phishing
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- HACK ATTACKER (received large amounts then dispersed quickly)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'Hack Attacker' as label,
    'high_risk' as category,
    'hack_attacker' as sub_category,
    90 as confidence,
    'osint' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.from_address = el.address
WHERE el.category = 'hacker'
  AND tt.to_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

