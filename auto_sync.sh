#!/bin/bash
# Auto-sync: updates labels, risk scores, clusters every 10 minutes
cd ~/cryptointelligence/indexer
export $(cat .env | grep -v '^#' | xargs)

echo "[$(date)] Starting auto-sync..."

while true; do
    echo "[$(date)] Running detection rules..."
    go run detection_rules.go 2>/dev/null
    
    echo "[$(date)] Running behavior engine..."
    go run behavior_engine.go 2>/dev/null
    
    echo "[$(date)] Refreshing materialized views..."
    psql $DATABASE_URL << 'SQL'
    REFRESH MATERIALIZED VIEW CONCURRENTLY mv_wallet_role_candidates;
    REFRESH MATERIALIZED VIEW CONCURRENTLY mv_wallet_hot_candidates;
    REFRESH MATERIALIZED VIEW CONCURRENTLY mv_wallet_deposit_candidates;
    
    -- Update cex_stats_cache
    INSERT INTO cex_stats_cache (key, value) VALUES
      ('total_wallets',   (SELECT COUNT(*) FROM wallet_addresses WHERE chain_id=1)),
      ('scored_wallets',  (SELECT COUNT(*) FROM wallet_counterparty_stats_30d)),
      ('high_confidence', (SELECT COUNT(*) FROM mv_wallet_role_candidates WHERE best_candidate_score >= 0.90)),
      ('exchange_labels', (SELECT COUNT(*) FROM exchange_wallet_labels WHERE is_active=TRUE)),
      ('transfers',       (SELECT COUNT(*) FROM ethereum_token_transfers)),
      ('transfers_usd',   (SELECT COUNT(*) FROM ethereum_token_transfers WHERE usd_value_estimate IS NOT NULL))
    ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=NOW();
SQL
    
    # Clear Redis so dashboard picks up fresh stats
    redis-cli FLUSHDB > /dev/null
    
    echo "[$(date)] Sync complete. Sleeping 10 minutes..."
    sleep 600
done
