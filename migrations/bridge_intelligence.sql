-- ============================================================
-- CryptoIntelligence — Complete Bridge Intelligence Database
-- 62 bridges across 6 types
-- ============================================================

-- Bridge types:
-- TRUSTLESS_WRAPPED  - Lock + mint (zkSync, Arbitrum, Wormhole)
-- TRUSTED_CUSTODIAL  - Central custody (Polygon, Avalanche, Ronin)
-- LIQUIDITY_NETWORK  - Pools on both sides (Hop, Across, Synapse)
-- MESSAGING_LAYER    - Data + messages (LayerZero, Axelar, Hyperlane)
-- BRIDGE_AGGREGATOR  - Best route router (LI.FI, Rango, Bungee)
-- NATIVE_ASSET       - Official bridges (Circle CCTP, Mayan)

-- Total hacked: $945M
--   Ronin Bridge:   $625M (Lazarus Group DPRK)
--   Wormhole Core:  $320M (Feb 2022)
--   Nomad Bridge:   $190M (Aug 2022)
--   Harmony Bridge: $100M (Jun 2022)

-- Key findings:
-- 18 bridges active in 5,014 blocks of data
-- 1 address (0x9f77...f54) uses ALL 4 bridge types
-- LI.FI most popular: 2,038 users, 6,992 txs
-- Wormhole ETH→SOL: 46 users bridging to Solana
