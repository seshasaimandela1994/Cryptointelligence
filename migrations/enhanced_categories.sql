
-- ============================================================
-- CryptoIntelligence — Enhanced Category Schema
-- 50+ categories matching enterprise intelligence standards
-- ============================================================

-- First add new columns to address_labels if not exist
ALTER TABLE address_labels ADD COLUMN IF NOT EXISTS sub_category VARCHAR(50);
ALTER TABLE address_labels ADD COLUMN IF NOT EXISTS behavior_flags TEXT[];
ALTER TABLE address_labels ADD COLUMN IF NOT EXISTS first_seen_block BIGINT;
ALTER TABLE address_labels ADD COLUMN IF NOT EXISTS last_seen_block BIGINT;
ALTER TABLE address_labels ADD COLUMN IF NOT EXISTS tx_count INTEGER DEFAULT 0;
ALTER TABLE address_labels ADD COLUMN IF NOT EXISTS volume_usd NUMERIC DEFAULT 0;
