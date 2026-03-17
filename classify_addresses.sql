-- ============================================================
-- EXCHANGE ECOSYSTEM
-- ============================================================

-- exchange_hot_wallet (already labelled as exchange)
UPDATE address_labels SET sub_category = 'exchange_hot_wallet'
WHERE category = 'exchange' AND label LIKE '%Hot%';

UPDATE address_labels SET sub_category = 'exchange_cold_wallet'
WHERE category = 'exchange' AND label LIKE '%Cold%';

-- exchange_deposit (receives from many, sends to exchange)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'Exchange Deposit Address' as label,
    'exchange_user' as category,
    'exchange_deposit' as sub_category,
    80 as confidence,
    'behavior_analysis' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category = 'exchange'
  AND tt.from_address NOT IN (SELECT address FROM address_labels)
  AND tt.from_address IN (
    SELECT to_address FROM token_transfers
    GROUP BY to_address
    HAVING COUNT(DISTINCT from_address) > 3
  )
ON CONFLICT (address) DO NOTHING;

-- ============================================================
-- DEFI ECOSYSTEM
-- ============================================================

-- liquidity_provider (interacts with AMM pools multiple times)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'Liquidity Provider' as label,
    'defi_user' as category,
    'liquidity_provider' as sub_category,
    80 as confidence,
    'behavior_analysis' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category = 'defi'
  AND el.label IN ('Curve 3Pool','Curve Tricrypto2','Balancer Vault',
                   'Uniswap V3 Positions NFT','SushiSwap MasterChef',
                   'Convex Finance','Convex Booster')
  AND tt.from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- yield_farmer (interacts with yield protocols)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'Yield Farmer' as label,
    'defi_user' as category,
    'yield_farmer' as sub_category,
    75 as confidence,
    'behavior_analysis' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category = 'defi'
  AND el.label IN ('Aave Pool V3','Aave Lending Pool V2',
                   'Compound Comptroller','Yearn ETH Vault',
                   'Convex Finance','Lido Staking Router')
  AND tt.from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- dex_trader (uses DEX routers)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'DEX Trader' as label,
    'defi_user' as category,
    'dex_trader' as sub_category,
    75 as confidence,
    'behavior_analysis' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category = 'defi'
  AND el.label IN ('Uniswap V2 Router','Uniswap V3 Router',
                   'Uniswap Universal Router','SushiSwap Router V1',
                   '1inch Router V5','1inch Router V6',
                   'CoW Protocol Settlement','ParaSwap V5',
                   'Odos Router V2','0x Exchange Proxy')
  AND tt.from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- flashloan_user (minted from zero address + burned in same block)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.to_address,
    'Flash Loan User' as label,
    'defi_user' as category,
    'flashloan_user' as sub_category,
    85 as confidence,
    'behavior_analysis' as source,
    'classifier' as labelled_by
FROM token_transfers tt
WHERE tt.from_address = '0x0000000000000000000000000000000000000000'
  AND tt.to_address IN (
    SELECT from_address FROM token_transfers
    WHERE to_address = '0x0000000000000000000000000000000000000000'
  )
  AND tt.to_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- ============================================================
-- FINANCIAL BEHAVIOR
-- ============================================================

-- whale (top 100 by transaction volume)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    from_address,
    'Whale' as label,
    'trader' as category,
    'whale' as sub_category,
    70 as confidence,
    'volume_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT from_address, COUNT(*) as tx_count
    FROM token_transfers
    WHERE from_address != '0x0000000000000000000000000000000000000000'
    GROUP BY from_address
    ORDER BY tx_count DESC
    LIMIT 100
) top_addresses
WHERE from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- new_wallet (only seen in last 100 blocks)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    from_address,
    'New Wallet' as label,
    'retail_trader' as category,
    'new_wallet' as sub_category,
    60 as confidence,
    'age_analysis' as source,
    'classifier' as labelled_by
FROM token_transfers
WHERE from_address NOT IN (SELECT address FROM address_labels)
  AND from_address != '0x0000000000000000000000000000000000000000'
  AND from_address IN (
    SELECT from_address FROM token_transfers
    GROUP BY from_address
    HAVING MIN(block_number) > (SELECT MAX(number) - 100 FROM blocks)
       AND COUNT(*) <= 3
  )
ON CONFLICT (address) DO NOTHING;

-- high_frequency_trader (100+ transactions)
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

-- ============================================================
-- RISK / COMPLIANCE
-- ============================================================

-- mixer_user (sent to any Tornado Cash contract)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'Mixer User' as label,
    'high_risk' as category,
    'mixer_user' as sub_category,
    95 as confidence,
    'ofac_screening' as source,
    'sanctions_check' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.category IN ('mixer','sanctions')
  AND tt.from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- sanctioned_entity (IS a sanctioned address)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    el.address,
    el.label as label,
    'sanctioned' as category,
    'sanctioned_entity' as sub_category,
    100 as confidence,
    'ofac' as source,
    'sanctions_check' as labelled_by
FROM entity_labels el
WHERE el.category = 'sanctions'
  AND el.address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- ============================================================
-- ECOSYSTEM
-- ============================================================

-- airdrop_farmer (receives from zero address many times)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT
    to_address,
    'Airdrop Farmer' as label,
    'defi_user' as category,
    'airdrop_farmer' as sub_category,
    70 as confidence,
    'behavior_analysis' as source,
    'classifier' as labelled_by
FROM (
    SELECT to_address, COUNT(*) as mint_count
    FROM token_transfers
    WHERE from_address = '0x0000000000000000000000000000000000000000'
    GROUP BY to_address
    HAVING COUNT(*) > 5
) airdrops
WHERE to_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

-- nft_trader (interacts with NFT marketplaces)
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

-- bridge_user (uses cross-chain bridges)
UPDATE address_labels SET sub_category = 'bridge_user'
WHERE category = 'bridge_user' AND sub_category IS NULL;

-- staking (interacts with staking contracts)
INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
SELECT DISTINCT
    tt.from_address,
    'Staker' as label,
    'defi_user' as category,
    'staking_contract' as sub_category,
    75 as confidence,
    'staking_interaction' as source,
    'classifier' as labelled_by
FROM token_transfers tt
JOIN entity_labels el ON tt.to_address = el.address
WHERE el.label IN ('Lido stETH','Lido wstETH','Rocket Pool rETH',
                   'ETH2 Deposit Contract','Ethena Staking Contract',
                   'Staked ENA Token')
  AND tt.from_address NOT IN (SELECT address FROM address_labels)
ON CONFLICT (address) DO NOTHING;

