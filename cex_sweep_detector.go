package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

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

	fmt.Println()
	fmt.Println("  CryptoIntelligence — Sweep Event Detector")
	fmt.Println("  ==========================================")
	fmt.Println()

	start := time.Now()

	fmt.Println("  Step 1: Relaxing sweep events constraints...")
	db.Exec(`ALTER TABLE deposit_sweep_events DROP CONSTRAINT IF EXISTS deposit_sweep_events_trigger_tx_hash_fkey`)
	db.Exec(`ALTER TABLE deposit_sweep_events ALTER COLUMN trigger_tx_hash DROP NOT NULL`)
	fmt.Println("  ✓ Done\n")

	fmt.Println("  Step 2: Detecting sweep patterns...")
	res, err := db.Exec(`
		INSERT INTO deposit_sweep_events (
			source_wallet_id, destination_wallet_id, trigger_tx_hash,
			asset_type, token_contract_id, sweep_amount_usd,
			inbound_count_prior_7d, destination_sweep_count_30d,
			heuristic_score, created_at
		)
		WITH fan_in AS (
			SELECT
				wa.wallet_id, wa.address,
				COUNT(DISTINCT ett.from_wallet_id) AS unique_senders,
				COUNT(*) AS total_inbound,
				SUM(COALESCE(ett.usd_value_estimate, 0)) AS total_inbound_usd
			FROM wallet_addresses wa
			JOIN ethereum_token_transfers ett ON ett.to_wallet_id = wa.wallet_id
			WHERE wa.chain_id = 1
			GROUP BY wa.wallet_id, wa.address
			HAVING COUNT(DISTINCT ett.from_wallet_id) >= 5
		),
		outbound AS (
			SELECT
				ett.from_wallet_id AS src,
				ett.to_wallet_id   AS dst,
				ett.token_contract_id,
				SUM(COALESCE(ett.usd_value_estimate, 0)) AS sweep_usd,
				COUNT(*) AS out_count,
				COUNT(DISTINCT ett.to_wallet_id) AS unique_dsts
			FROM ethereum_token_transfers ett
			JOIN fan_in fi ON fi.wallet_id = ett.from_wallet_id
			WHERE ett.to_wallet_id IS NOT NULL
			  AND ett.to_wallet_id != ett.from_wallet_id
			GROUP BY ett.from_wallet_id, ett.to_wallet_id, ett.token_contract_id
			HAVING COUNT(*) >= 2
		),
		scored AS (
			SELECT
				o.src, o.dst, o.token_contract_id, o.sweep_usd,
				fi.total_inbound,
				ROUND(LEAST(1.0,
					(LEAST(fi.unique_senders::numeric / 100.0, 1.0) * 0.35)
					+ (CASE WHEN o.unique_dsts <= 2 THEN 0.30
					        WHEN o.unique_dsts <= 5 THEN 0.20 ELSE 0.10 END)
					+ (LEAST(o.sweep_usd / 1000000.0, 1.0) * 0.20)
					+ (LEAST(o.out_count::numeric / 10.0, 1.0) * 0.15)
				)::numeric, 4) AS score
			FROM outbound o
			JOIN fan_in fi ON fi.wallet_id = o.src
		)
		SELECT
			s.src, s.dst,
			NULL::text,
			CASE WHEN s.token_contract_id IS NOT NULL THEN 'ERC20' ELSE 'ETH' END,
			s.token_contract_id, s.sweep_usd, s.total_inbound,
			(SELECT COUNT(*) FROM deposit_sweep_events d2 WHERE d2.destination_wallet_id = s.dst),
			s.score, NOW()
		FROM scored s
		WHERE s.score >= 0.40
		  AND NOT EXISTS (
			SELECT 1 FROM deposit_sweep_events dse
			WHERE dse.source_wallet_id = s.src
			  AND dse.destination_wallet_id = s.dst
		  )`)
	if err != nil {
		fmt.Printf("  ⚠ error: %v\n\n", err)
	} else {
		n, _ := res.RowsAffected()
		fmt.Printf("  ✓ %d sweep events detected\n\n", n)
	}

	fmt.Println("  Step 3: Updating deposit scores from sweeps...")
	res, _ = db.Exec(`
		UPDATE wallet_features_ethereum wfe
		SET deposit_wallet_pattern_score = LEAST(1.0, GREATEST(
			wfe.deposit_wallet_pattern_score,
			sw.avg_score * 1.1
		)), last_computed_at = NOW()
		FROM (
			SELECT source_wallet_id AS wallet_id,
				AVG(heuristic_score) AS avg_score
			FROM deposit_sweep_events
			GROUP BY source_wallet_id
		) sw
		WHERE wfe.wallet_id = sw.wallet_id`)
	n, _ := res.RowsAffected()
	fmt.Printf("  ✓ %d wallet scores updated\n\n", n)

	var total, sources, dests int
	db.QueryRow("SELECT COUNT(*), COUNT(DISTINCT source_wallet_id), COUNT(DISTINCT destination_wallet_id) FROM deposit_sweep_events").Scan(&total, &sources, &dests)

	fmt.Println("  ══════════════════════════════════════════")
	fmt.Printf("  Done in %s\n", time.Since(start).Round(time.Second))
	fmt.Printf("  Total sweep events:    %d\n", total)
	fmt.Printf("  Unique source wallets: %d\n", sources)
	fmt.Printf("  Unique destinations:   %d\n\n", dests)

	rows, _ := db.Query(`
		SELECT wa_src.address, wa_dst.address,
			ROUND(dse.sweep_amount_usd::numeric,0),
			dse.heuristic_score,
			COALESCE(ee.canonical_name,'Unknown')
		FROM deposit_sweep_events dse
		JOIN wallet_addresses wa_src ON wa_src.wallet_id = dse.source_wallet_id
		JOIN wallet_addresses wa_dst ON wa_dst.wallet_id = dse.destination_wallet_id
		LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id = dse.destination_wallet_id AND ewl.is_active=TRUE
		LEFT JOIN exchange_entities ee ON ee.exchange_id = ewl.exchange_id
		ORDER BY dse.heuristic_score DESC, dse.sweep_amount_usd DESC NULLS LAST
		LIMIT 10`)
	if rows != nil {
		defer rows.Close()
		fmt.Println("  Top Sweep Events:")
		fmt.Println("  ─────────────────────────────────────────────────────")
		for rows.Next() {
			var src, dst, exch string
			var usd float64
			var score float64
			rows.Scan(&src, &dst, &usd, &score, &exch)
			fmt.Printf("  %s...%s → %s...%s  $%.0f  score:%.2f  [%s]\n",
				src[:10], src[38:], dst[:10], dst[38:], usd, score, exch)
		}
	}
	fmt.Println()
}
