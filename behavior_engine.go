package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

// ============================================================
// CryptoIntelligence — Behavioral Classification Engine
// Computes 12 behavioral features per address
// Then classifies into 24 categories with confidence scores
// ============================================================

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("DB connect error:", err)
	}
	defer db.Close()

	fmt.Println()
	fmt.Println("  CryptoIntelligence - Behavioral Classification Engine")
	fmt.Println("  =======================================================")
	fmt.Println()

	start := time.Now()

	// ── STEP 1: Create behavioral features table ──────────────
	fmt.Println("  Step 1: Creating behavioral features table...")
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS address_behaviors (
			address              CHAR(42) PRIMARY KEY,
			total_transactions   INTEGER  DEFAULT 0,
			unique_tokens_used   INTEGER  DEFAULT 0,
			unique_destinations  INTEGER  DEFAULT 0,
			unique_senders       INTEGER  DEFAULT 0,
			dex_interactions     INTEGER  DEFAULT 0,
			exchange_interactions INTEGER DEFAULT 0,
			bridge_usage         INTEGER  DEFAULT 0,
			defi_interactions    INTEGER  DEFAULT 0,
			nft_interactions     INTEGER  DEFAULT 0,
			sanctions_contacts   INTEGER  DEFAULT 0,
			mixer_contacts       INTEGER  DEFAULT 0,
			contract_calls       INTEGER  DEFAULT 0,
			blocks_active        INTEGER  DEFAULT 0,
			tx_per_block         NUMERIC  DEFAULT 0,
			avg_tx_value         NUMERIC  DEFAULT 0,
			max_tx_value         NUMERIC  DEFAULT 0,
			total_volume         NUMERIC  DEFAULT 0,
			first_seen_block     BIGINT   DEFAULT 0,
			last_seen_block      BIGINT   DEFAULT 0,
			block_span           INTEGER  DEFAULT 0,
			mint_count           INTEGER  DEFAULT 0,
			burn_count           INTEGER  DEFAULT 0,
			same_block_txs       INTEGER  DEFAULT 0,
			behavior_class       VARCHAR(50),
			behavior_confidence  INTEGER  DEFAULT 0,
			computed_at          TIMESTAMP DEFAULT NOW()
		)`)
	if err != nil {
		log.Fatal("Create table error:", err)
	}
	fmt.Println("  ✓ Table ready")
	fmt.Println()

	// ── STEP 2: Compute behavioral features ──────────────────
	fmt.Println("  Step 2: Computing behavioral features for all addresses...")
	fmt.Println("           This analyses every transaction pattern...")
	fmt.Println()

	_, err = db.Exec(`
		INSERT INTO address_behaviors (
			address,
			total_transactions,
			unique_tokens_used,
			unique_destinations,
			unique_senders,
			blocks_active,
			tx_per_block,
			avg_tx_value,
			max_tx_value,
			total_volume,
			first_seen_block,
			last_seen_block,
			block_span,
			mint_count,
			burn_count,
			same_block_txs
		)
		SELECT
			from_address as address,
			COUNT(*) as total_transactions,
			COUNT(DISTINCT token_address) as unique_tokens_used,
			COUNT(DISTINCT to_address) as unique_destinations,
			0 as unique_senders,
			COUNT(DISTINCT block_number) as blocks_active,
			ROUND(COUNT(*)::numeric / NULLIF(COUNT(DISTINCT block_number), 0), 2) as tx_per_block,
			ROUND(AVG(CAST(value AS NUMERIC)), 0) as avg_tx_value,
			MAX(CAST(value AS NUMERIC)) as max_tx_value,
			SUM(CAST(value AS NUMERIC)) as total_volume,
			MIN(block_number) as first_seen_block,
			MAX(block_number) as last_seen_block,
			MAX(block_number) - MIN(block_number) as block_span,
			SUM(CASE WHEN from_address = '0x0000000000000000000000000000000000000000' THEN 1 ELSE 0 END) as mint_count,
			SUM(CASE WHEN to_address = '0x0000000000000000000000000000000000000000' THEN 1 ELSE 0 END) as burn_count,
			(SELECT COUNT(*) FROM (
				SELECT block_number, COUNT(*) as c
				FROM token_transfers t2
				WHERE t2.from_address = t1.from_address
				GROUP BY block_number
				HAVING COUNT(*) > 1
			) multi) as same_block_txs
		FROM token_transfers t1
		WHERE from_address != '0x0000000000000000000000000000000000000000'
		GROUP BY from_address
		ON CONFLICT (address) DO UPDATE SET
			total_transactions   = EXCLUDED.total_transactions,
			unique_tokens_used   = EXCLUDED.unique_tokens_used,
			unique_destinations  = EXCLUDED.unique_destinations,
			blocks_active        = EXCLUDED.blocks_active,
			tx_per_block         = EXCLUDED.tx_per_block,
			avg_tx_value         = EXCLUDED.avg_tx_value,
			max_tx_value         = EXCLUDED.max_tx_value,
			total_volume         = EXCLUDED.total_volume,
			first_seen_block     = EXCLUDED.first_seen_block,
			last_seen_block      = EXCLUDED.last_seen_block,
			block_span           = EXCLUDED.block_span,
			same_block_txs       = EXCLUDED.same_block_txs,
			computed_at          = NOW()`)
	if err != nil {
		log.Fatal("Feature computation error:", err)
	}

	var totalAddresses int
	db.QueryRow("SELECT COUNT(*) FROM address_behaviors").Scan(&totalAddresses)
	fmt.Printf("  ✓ Features computed for %d addresses\n\n", totalAddresses)

	// ── STEP 3: Compute interaction features ─────────────────
	fmt.Println("  Step 3: Computing interaction features...")

	// DEX interactions
	_, err = db.Exec(`
		UPDATE address_behaviors ab
		SET dex_interactions = (
			SELECT COUNT(*)
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'defi'
			AND tt.from_address = ab.address
		)`)
	if err != nil {
		fmt.Printf("  ⚠ DEX interactions error: %v\n", err)
	} else {
		fmt.Println("  ✓ DEX interactions computed")
	}

	// Exchange interactions
	_, err = db.Exec(`
		UPDATE address_behaviors ab
		SET exchange_interactions = (
			SELECT COUNT(*)
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'exchange'
			AND tt.from_address = ab.address
		)`)
	if err != nil {
		fmt.Printf("  ⚠ Exchange interactions error: %v\n", err)
	} else {
		fmt.Println("  ✓ Exchange interactions computed")
	}

	// Bridge usage
	_, err = db.Exec(`
		UPDATE address_behaviors ab
		SET bridge_usage = (
			SELECT COUNT(*)
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'bridge'
			AND tt.from_address = ab.address
		)`)
	if err != nil {
		fmt.Printf("  ⚠ Bridge usage error: %v\n", err)
	} else {
		fmt.Println("  ✓ Bridge usage computed")
	}

	// Sanctions contacts
	_, err = db.Exec(`
		UPDATE address_behaviors ab
		SET sanctions_contacts = (
			SELECT COUNT(*)
			FROM token_transfers tt
			JOIN entity_labels el
				ON tt.to_address = el.address OR tt.from_address = el.address
			WHERE el.category = 'sanctions'
			AND tt.from_address = ab.address
		)`)
	if err != nil {
		fmt.Printf("  ⚠ Sanctions contacts error: %v\n", err)
	} else {
		fmt.Println("  ✓ Sanctions contacts computed")
	}

	// Mixer contacts
	_, err = db.Exec(`
		UPDATE address_behaviors ab
		SET mixer_contacts = (
			SELECT COUNT(*)
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'mixer'
			AND tt.from_address = ab.address
		)`)
	if err != nil {
		fmt.Printf("  ⚠ Mixer contacts error: %v\n", err)
	} else {
		fmt.Println("  ✓ Mixer contacts computed")
	}

	// NFT interactions
	_, err = db.Exec(`
		UPDATE address_behaviors ab
		SET nft_interactions = (
			SELECT COUNT(*)
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'nft'
			AND tt.from_address = ab.address
		)`)
	if err != nil {
		fmt.Printf("  ⚠ NFT interactions error: %v\n", err)
	} else {
		fmt.Println("  ✓ NFT interactions computed")
	}

	fmt.Println()

	// ── STEP 4: Behavioral classification ────────────────────
	fmt.Println("  Step 4: Classifying addresses by behavior...")

	_, err = db.Exec(`
		UPDATE address_behaviors SET
		behavior_class = CASE

			-- HIGHEST PRIORITY: Risk / Compliance
			WHEN sanctions_contacts > 0
				THEN 'sanctions_exposure'

			WHEN mixer_contacts > 0
				THEN 'mixer_user'

			-- BOT DETECTION
			WHEN tx_per_block >= 5 AND same_block_txs >= 5
				THEN 'mev_builder'

			WHEN tx_per_block >= 3 AND same_block_txs >= 3
				AND unique_tokens_used >= 3
				THEN 'arbitrage_bot'

			WHEN tx_per_block >= 3 AND same_block_txs >= 3
				THEN 'mev_searcher'

			WHEN total_transactions > 50
				AND tx_per_block >= 2
				AND unique_destinations > total_transactions * 0.7
				THEN 'phishing_wallet'

			-- FLASH LOAN
			WHEN burn_count > 0 AND mint_count > 0
				AND tx_per_block > 2
				THEN 'flashloan_user'

			-- DEFI POWER USERS
			WHEN dex_interactions >= 20
				AND unique_tokens_used >= 5
				THEN 'dex_trader'

			WHEN dex_interactions >= 10
				AND exchange_interactions = 0
				THEN 'liquidity_provider'

			WHEN dex_interactions > 5
				AND bridge_usage > 2
				THEN 'defi_power_user'

			-- EXCHANGE USERS
			WHEN exchange_interactions >= 10
				AND dex_interactions = 0
				THEN 'exchange_heavy_user'

			WHEN exchange_interactions >= 3
				THEN 'exchange_user'

			-- BRIDGE USERS
			WHEN bridge_usage >= 3
				THEN 'bridge_user'

			-- NFT
			WHEN nft_interactions >= 5
				THEN 'nft_trader'

			-- FINANCIAL BEHAVIOR
			WHEN total_transactions > 500
				THEN 'whale'

			WHEN total_transactions > 100
				THEN 'high_frequency_trader'

			WHEN block_span = 0 AND total_transactions <= 3
				THEN 'new_wallet'

			WHEN block_span > 0
				AND last_seen_block < first_seen_block + 1000
				AND total_transactions <= 3
				THEN 'dormant_wallet'

			WHEN total_transactions BETWEEN 1 AND 10
				THEN 'retail_trader'

			WHEN total_transactions BETWEEN 11 AND 50
				THEN 'mid_frequency_trader'

			ELSE 'unclassified'
		END,

		behavior_confidence = CASE

			WHEN sanctions_contacts > 0     THEN 99
			WHEN mixer_contacts > 0         THEN 97
			WHEN tx_per_block >= 5
				AND same_block_txs >= 5      THEN 94
			WHEN tx_per_block >= 3
				AND unique_tokens_used >= 3  THEN 90
			WHEN tx_per_block >= 3          THEN 88
			WHEN burn_count > 0
				AND tx_per_block > 2         THEN 87
			WHEN dex_interactions >= 20     THEN 85
			WHEN exchange_interactions >= 10 THEN 85
			WHEN dex_interactions >= 10     THEN 82
			WHEN total_transactions > 500   THEN 80
			WHEN bridge_usage >= 3          THEN 78
			WHEN nft_interactions >= 5      THEN 76
			WHEN exchange_interactions >= 3 THEN 75
			WHEN total_transactions > 100   THEN 72
			WHEN total_transactions <= 3    THEN 65
			WHEN total_transactions <= 10   THEN 60
			ELSE 55
		END`)
	if err != nil {
		log.Fatal("Classification error:", err)
	}
	fmt.Println("  ✓ Behavioral classification complete")
	fmt.Println()

	// ── STEP 5: Write to address_labels ──────────────────────
	fmt.Println("  Step 5: Writing behavioral labels to address_labels...")

	result, err := db.Exec(`
		INSERT INTO address_labels
			(address, label, category, sub_category, confidence, source, labelled_by)
		SELECT
			ab.address,
			'Behavioral: ' || ab.behavior_class as label,
			CASE ab.behavior_class
				WHEN 'sanctions_exposure'  THEN 'high_risk'
				WHEN 'mixer_user'          THEN 'high_risk'
				WHEN 'mev_builder'         THEN 'bot'
				WHEN 'arbitrage_bot'       THEN 'bot'
				WHEN 'mev_searcher'        THEN 'bot'
				WHEN 'phishing_wallet'     THEN 'high_risk'
				WHEN 'flashloan_user'      THEN 'defi_user'
				WHEN 'dex_trader'          THEN 'defi_user'
				WHEN 'liquidity_provider'  THEN 'defi_user'
				WHEN 'defi_power_user'     THEN 'defi_user'
				WHEN 'exchange_heavy_user' THEN 'exchange_user'
				WHEN 'exchange_user'       THEN 'exchange_user'
				WHEN 'bridge_user'         THEN 'bridge_user'
				WHEN 'nft_trader'          THEN 'nft_trader'
				WHEN 'whale'               THEN 'trader'
				WHEN 'high_frequency_trader' THEN 'trader'
				WHEN 'new_wallet'          THEN 'retail_trader'
				WHEN 'dormant_wallet'      THEN 'retail_trader'
				WHEN 'retail_trader'       THEN 'retail_trader'
				WHEN 'mid_frequency_trader' THEN 'retail_trader'
				ELSE 'unknown'
			END as category,
			ab.behavior_class as sub_category,
			ab.behavior_confidence as confidence,
			'behavior_engine' as source,
			'behavioral_classifier' as labelled_by
		FROM address_behaviors ab
		WHERE ab.behavior_class != 'unclassified'
		AND ab.address NOT IN (SELECT address FROM address_labels)
		ON CONFLICT (address) DO NOTHING`)
	if err != nil {
		log.Fatal("Label write error:", err)
	}

	rows, _ := result.RowsAffected()
	fmt.Printf("  ✓ %d new behavioral labels written\n\n", rows)

	elapsed := time.Since(start)

	// ── STEP 6: Summary ───────────────────────────────────────
	fmt.Println("  ══════════════════════════════════════════════════")
	fmt.Printf("  Behavioral Classification Complete!\n")
	fmt.Printf("  Total addresses analysed: %d\n", totalAddresses)
	fmt.Printf("  New labels added:         %d\n", rows)
	fmt.Printf("  Time taken:               %s\n", elapsed.Round(time.Second))
	fmt.Println("  ══════════════════════════════════════════════════")
	fmt.Println()

	// Behavior class breakdown
	brows, err := db.Query(`
		SELECT behavior_class, COUNT(*) as cnt,
			ROUND(AVG(behavior_confidence), 1) as avg_conf
		FROM address_behaviors
		WHERE behavior_class IS NOT NULL
		GROUP BY behavior_class
		ORDER BY cnt DESC`)
	if err != nil {
		log.Fatal(err)
	}
	defer brows.Close()

	fmt.Println("  Behavioral Class Breakdown:")
	fmt.Println("  ──────────────────────────────────────────────────")
	fmt.Printf("  %-30s %8s  %s\n", "Class", "Count", "Avg Confidence")
	fmt.Println("  ──────────────────────────────────────────────────")
	for brows.Next() {
		var class string
		var cnt int
		var conf float64
		brows.Scan(&class, &cnt, &conf)
		fmt.Printf("  %-30s %8d  %.1f%%\n", class, cnt, conf)
	}
	fmt.Println()

	// Top 5 most interesting addresses
	fmt.Println("  Top 5 Most Active Addresses:")
	fmt.Println("  ──────────────────────────────────────────────────")
	trows, err := db.Query(`
		SELECT ab.address, ab.behavior_class, ab.total_transactions,
			ab.unique_tokens_used, ab.tx_per_block,
			COALESCE(al.label, 'Unknown') as known_label
		FROM address_behaviors ab
		LEFT JOIN entity_labels al ON ab.address = al.address
		ORDER BY ab.total_transactions DESC
		LIMIT 5`)
	if err != nil {
		log.Fatal(err)
	}
	defer trows.Close()

	for trows.Next() {
		var addr, class, label string
		var txCount, tokenCount int
		var txPerBlock float64
		trows.Scan(&addr, &class, &txCount, &tokenCount, &txPerBlock, &label)
		fmt.Printf("  %s...%s\n", addr[:10], addr[38:])
		fmt.Printf("    Class: %-25s TXs: %d  Tokens: %d  TX/Block: %.1f\n",
			class, txCount, tokenCount, txPerBlock)
		fmt.Printf("    Known: %s\n\n", label)
	}
}
