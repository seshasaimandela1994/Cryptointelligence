package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

const (
	pollInterval    = 12 * time.Second
	batchSize       = 100
	statsRefreshMin = 30
)

type LiveIndexer struct {
	db          *sql.DB
	lastBlock   int64
	processed   int64
	errors      int64
}

func execTx(tx *sql.Tx, label string, query string, args ...interface{}) error {
	_, err := tx.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	return nil
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		log.Fatal("DB ping:", err)
	}

	idx := &LiveIndexer{db: db}

	fmt.Println()
	fmt.Println("  CryptoIntelligence — CEX Live Indexer")
	fmt.Println("  ========================================")
	fmt.Println()

	var startBlock int64
	db.QueryRow(`SELECT COALESCE(MAX(block_number), 0) FROM ethereum_blocks`).Scan(&startBlock)
	if startBlock == 0 {
		db.QueryRow(`SELECT COALESCE(MAX(block_number), 21000000) FROM token_transfers`).Scan(&startBlock)
	}
	idx.lastBlock = startBlock
	fmt.Printf("  Starting from block: %d\n", startBlock)
	fmt.Printf("  Polling every 12s, stats refresh every %dm\n\n", statsRefreshMin)
	fmt.Println("  Press Ctrl+C to stop")
	fmt.Println()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(pollInterval)
	statsTicker := time.NewTicker(time.Duration(statsRefreshMin) * time.Minute)
	defer ticker.Stop()
	defer statsTicker.Stop()

	for {
		select {
		case <-ticker.C:
			idx.processNewBlocks()
		case <-statsTicker.C:
			idx.refreshCounterpartyStats()
		case <-quit:
			fmt.Println()
			fmt.Printf("  Stopped. Blocks processed: %d  Errors: %d\n", idx.processed, idx.errors)
			return
		}
	}
}

func (idx *LiveIndexer) processNewBlocks() {
	rows, err := idx.db.Query(`
		SELECT DISTINCT tt.block_number
		FROM token_transfers tt
		WHERE tt.block_number > $1
		  AND NOT EXISTS (
			SELECT 1 FROM ethereum_blocks eb WHERE eb.block_number = tt.block_number
		  )
		ORDER BY tt.block_number
		LIMIT $2`, idx.lastBlock, batchSize)
	if err != nil {
		fmt.Printf("  ⚠ [%s] query: %v\n", time.Now().Format("15:04:05"), err)
		idx.errors++
		return
	}
	defer rows.Close()

	var blocks []int64
	for rows.Next() {
		var bn int64
		rows.Scan(&bn)
		blocks = append(blocks, bn)
	}
	if len(blocks) == 0 {
		return
	}

	fmt.Printf("  [%s] %d new blocks (%d→%d)\n",
		time.Now().Format("15:04:05"), len(blocks), blocks[0], blocks[len(blocks)-1])

	ok := 0
	for _, bn := range blocks {
		if err := idx.processBlock(bn); err != nil {
			fmt.Printf("  ⚠ block %d: %v\n", bn, err)
			idx.errors++
			continue
		}
		idx.lastBlock = bn
		idx.processed++
		ok++
	}
	fmt.Printf("  ✓ %d/%d blocks synced (total: %d)\n", ok, len(blocks), idx.processed)
}

func (idx *LiveIndexer) processBlock(blockNum int64) error {
	tx, err := idx.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := execTx(tx, "block", `
		INSERT INTO ethereum_blocks (block_number, block_hash, block_timestamp, transaction_count)
		SELECT tt.block_number,
			'0x' || lpad(tt.block_number::text, 64, '0'),
			COALESCE(b.timestamp, NOW() - ((22000000 - tt.block_number) * interval '12 seconds')),
			COUNT(*)
		FROM token_transfers tt
		LEFT JOIN blocks b ON b.number = tt.block_number
		WHERE tt.block_number = $1
		GROUP BY tt.block_number, b.timestamp
		ON CONFLICT (block_number) DO NOTHING`, blockNum); err != nil {
		return err
	}

	if err := execTx(tx, "wallets_from", `
		INSERT INTO wallet_addresses (address, chain_id, address_type, is_contract, current_label_status)
		SELECT DISTINCT lower(from_address), 1, 'eoa'::address_type_enum, FALSE, 'unlabeled'::label_status_enum
		FROM token_transfers
		WHERE block_number = $1
		  AND from_address != '0x0000000000000000000000000000000000000000'
		ON CONFLICT (address) DO NOTHING`, blockNum); err != nil {
		return err
	}

	if err := execTx(tx, "wallets_to", `
		INSERT INTO wallet_addresses (address, chain_id, address_type, is_contract, current_label_status)
		SELECT DISTINCT lower(to_address), 1, 'eoa'::address_type_enum, FALSE, 'unlabeled'::label_status_enum
		FROM token_transfers
		WHERE block_number = $1
		  AND to_address != '0x0000000000000000000000000000000000000000'
		ON CONFLICT (address) DO NOTHING`, blockNum); err != nil {
		return err
	}

	if err := execTx(tx, "token_contracts", `
		INSERT INTO ethereum_token_contracts (contract_address, token_standard, symbol, name, decimals, is_stablecoin)
		SELECT DISTINCT lower(tt.token_address), 'ERC20',
			COALESCE(el.label, 'UNKNOWN'), COALESCE(el.label, 'Unknown Token'),
			CASE WHEN lower(tt.token_address) IN (
				'0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48',
				'0xdac17f958d2ee523a2206206994597c13d831ec7') THEN 6 ELSE 18 END,
			CASE WHEN lower(tt.token_address) IN (
				'0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48',
				'0xdac17f958d2ee523a2206206994597c13d831ec7',
				'0x6b175474e89094c44da98b954eedeac495271d0f') THEN TRUE ELSE FALSE END
		FROM token_transfers tt
		LEFT JOIN entity_labels el ON lower(el.address) = lower(tt.token_address)
		WHERE tt.block_number = $1 AND tt.token_address IS NOT NULL
		ON CONFLICT (contract_address) DO NOTHING`, blockNum); err != nil {
		return err
	}

	if err := execTx(tx, "transfers", `
		INSERT INTO ethereum_token_transfers (
			tx_hash, log_index, block_number, block_timestamp,
			token_contract_id, token_contract_address,
			from_wallet_id, to_wallet_id,
			from_address, to_address, raw_amount, normalized_amount, usd_value_estimate
		)
		SELECT
			'0x' || lpad(tt.id::text, 64, '0'), 0,
			tt.block_number,
			COALESCE(eb.block_timestamp, NOW() - ((22000000 - tt.block_number) * interval '12 seconds')),
			etc.token_contract_id, lower(tt.token_address),
			wa_from.wallet_id, wa_to.wallet_id,
			lower(tt.from_address), lower(tt.to_address),
			CASE WHEN length(tt.value::text) > 50 THEN NULL ELSE CAST(tt.value AS NUMERIC) END,
			CASE WHEN length(tt.value::text) > 50 THEN NULL
			     WHEN etc.decimals IS NOT NULL AND etc.decimals > 0
			         THEN CAST(tt.value AS NUMERIC) / POWER(10::numeric, etc.decimals)
			     ELSE CAST(tt.value AS NUMERIC) / 1e18 END,
			CASE WHEN length(tt.value::text) > 50 THEN NULL
			     WHEN etc.decimals IS NOT NULL AND etc.decimals > 0 AND aph.price_usd IS NOT NULL
			         THEN (CAST(tt.value AS NUMERIC) / POWER(10::numeric, etc.decimals)) * aph.price_usd
			     ELSE NULL END
		FROM token_transfers tt
		JOIN ethereum_blocks eb ON eb.block_number = tt.block_number
		LEFT JOIN ethereum_token_contracts etc ON etc.contract_address = lower(tt.token_address)
		LEFT JOIN wallet_addresses wa_from ON wa_from.address = lower(tt.from_address)
		LEFT JOIN wallet_addresses wa_to   ON wa_to.address   = lower(tt.to_address)
		LEFT JOIN asset_price_history aph
			ON aph.token_contract_id = etc.token_contract_id
			AND aph.price_date = CURRENT_DATE
		WHERE tt.block_number = $1
		  AND tt.from_address != '0x0000000000000000000000000000000000000000'
		ON CONFLICT (tx_hash, log_index) DO NOTHING`, blockNum); err != nil {
		return err
	}

	if err := execTx(tx, "wallet_features", `
		INSERT INTO wallet_features_ethereum (
			wallet_id, tx_count_total, tx_count_30d,
			inbound_tx_count_30d, outbound_tx_count_30d,
			unique_inbound_counterparties_30d, unique_outbound_counterparties_30d,
			deposit_wallet_pattern_score, hot_wallet_pattern_score,
			cold_wallet_pattern_score, sweep_pattern_score,
			first_seen_block, last_seen_block, last_computed_at
		)
		SELECT wa.wallet_id,
			COALESCE(s.total_tx,0), COALESCE(s.total_tx,0),
			COALESCE(s.inbound,0), COALESCE(s.outbound,0),
			COALESCE(s.unique_senders,0), COALESCE(s.unique_recv,0),
			CASE WHEN COALESCE(s.unique_senders,0)>=200 THEN 0.90
			     WHEN COALESCE(s.unique_senders,0)>=75  THEN 0.75
			     WHEN COALESCE(s.unique_senders,0)>=20  THEN 0.50 ELSE 0.20 END,
			CASE WHEN COALESCE(s.outbound,0)>=1000 THEN 0.90
			     WHEN COALESCE(s.outbound,0)>=100  THEN 0.75
			     WHEN COALESCE(s.outbound,0)>=20   THEN 0.50 ELSE 0.20 END,
			CASE WHEN COALESCE(s.total_tx,0)<=5  THEN 0.70
			     WHEN COALESCE(s.total_tx,0)<=20 THEN 0.40 ELSE 0.20 END,
			0.30, s.first_block, s.last_block, NOW()
		FROM wallet_addresses wa
		JOIN (
			SELECT lower(from_address) AS addr,
				COUNT(*) AS total_tx,
				COUNT(*) FILTER (WHERE to_address!='0x0000000000000000000000000000000000000000') AS outbound,
				COUNT(*) FILTER (WHERE from_address='0x0000000000000000000000000000000000000000') AS inbound,
				COUNT(DISTINCT to_address) AS unique_recv,
				COUNT(DISTINCT from_address) AS unique_senders,
				MIN(block_number) AS first_block, MAX(block_number) AS last_block
			FROM token_transfers
			WHERE from_address!='0x0000000000000000000000000000000000000000'
			GROUP BY lower(from_address)
		) s ON s.addr = wa.address
		WHERE wa.chain_id = 1
		  AND wa.address IN (
			SELECT DISTINCT lower(from_address) FROM token_transfers WHERE block_number = $1
			UNION
			SELECT DISTINCT lower(to_address)   FROM token_transfers WHERE block_number = $1
		  )
		ON CONFLICT (wallet_id) DO UPDATE SET
			tx_count_total=EXCLUDED.tx_count_total, tx_count_30d=EXCLUDED.tx_count_30d,
			outbound_tx_count_30d=EXCLUDED.outbound_tx_count_30d,
			unique_inbound_counterparties_30d=EXCLUDED.unique_inbound_counterparties_30d,
			unique_outbound_counterparties_30d=EXCLUDED.unique_outbound_counterparties_30d,
			deposit_wallet_pattern_score=EXCLUDED.deposit_wallet_pattern_score,
			hot_wallet_pattern_score=EXCLUDED.hot_wallet_pattern_score,
			cold_wallet_pattern_score=EXCLUDED.cold_wallet_pattern_score,
			last_seen_block=EXCLUDED.last_seen_block, last_computed_at=NOW()`, blockNum); err != nil {
		return err
	}

	return tx.Commit()
}

func (idx *LiveIndexer) refreshCounterpartyStats() {
	fmt.Printf("  [%s] Refreshing 30d counterparty stats...\n", time.Now().Format("15:04:05"))
	start := time.Now()
	_, err := idx.db.Exec(`
		INSERT INTO wallet_counterparty_stats_30d (
			wallet_id, as_of_date,
			eth_inbound_tx_count_30d, eth_outbound_tx_count_30d,
			token_inbound_tx_count_30d, token_outbound_tx_count_30d,
			unique_eth_senders_30d, unique_eth_receivers_30d,
			unique_token_senders_30d, unique_token_receivers_30d,
			total_inbound_usd_30d, total_outbound_usd_30d,
			stablecoin_inbound_usd_30d, stablecoin_outbound_usd_30d,
			updated_at
		)
		SELECT wa.wallet_id, CURRENT_DATE,
			0, 0,
			COUNT(*) FILTER (WHERE ett.to_wallet_id = wa.wallet_id),
			COUNT(*) FILTER (WHERE ett.from_wallet_id = wa.wallet_id),
			0, 0,
			COUNT(DISTINCT ett.from_wallet_id) FILTER (WHERE ett.to_wallet_id = wa.wallet_id),
			COUNT(DISTINCT ett.to_wallet_id)   FILTER (WHERE ett.from_wallet_id = wa.wallet_id),
			COALESCE(SUM(ett.usd_value_estimate) FILTER (WHERE ett.to_wallet_id   = wa.wallet_id), 0),
			COALESCE(SUM(ett.usd_value_estimate) FILTER (WHERE ett.from_wallet_id = wa.wallet_id), 0),
			0, 0, NOW()
		FROM wallet_addresses wa
		JOIN ethereum_token_transfers ett
			ON ett.from_wallet_id = wa.wallet_id OR ett.to_wallet_id = wa.wallet_id
		WHERE wa.chain_id = 1
		  AND ett.block_timestamp >= NOW() - INTERVAL '30 days'
		GROUP BY wa.wallet_id
		ON CONFLICT (wallet_id) DO UPDATE SET
			token_inbound_tx_count_30d  = EXCLUDED.token_inbound_tx_count_30d,
			token_outbound_tx_count_30d = EXCLUDED.token_outbound_tx_count_30d,
			unique_token_senders_30d    = EXCLUDED.unique_token_senders_30d,
			unique_token_receivers_30d  = EXCLUDED.unique_token_receivers_30d,
			total_inbound_usd_30d       = EXCLUDED.total_inbound_usd_30d,
			total_outbound_usd_30d      = EXCLUDED.total_outbound_usd_30d,
			as_of_date = CURRENT_DATE, updated_at = NOW()`)
	if err != nil {
		fmt.Printf("  ⚠ stats error: %v\n", err)
		return
	}
	fmt.Printf("  ✓ Stats refreshed in %s\n", time.Since(start).Round(time.Second))
}
