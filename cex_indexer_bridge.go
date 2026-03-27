package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"
)

func execQ(db *sql.DB, label, query string, args ...interface{}) int64 {
	r, err := db.Exec(query, args...)
	if err != nil {
		fmt.Printf("  ⚠ %s: %v\n", label, err)
		return 0
	}
	n, _ := r.RowsAffected()
	return n
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
	if err := db.Ping(); err != nil {
		log.Fatal("ping:", err)
	}
	fmt.Println()
	fmt.Println("  CryptoIntelligence — CEX Detection Engine Bridge")
	fmt.Println("  ==================================================")
	fmt.Println()
	start := time.Now()

	fmt.Println("  Step 1: Syncing blocks...")
	n := execQ(db, "blocks", `
		INSERT INTO ethereum_blocks (block_number, block_hash, block_timestamp)
		SELECT DISTINCT tt.block_number,
			'0x' || lpad(tt.block_number::text, 64, '0'),
			NOW() - ((22000000 - tt.block_number) * interval '12 seconds')
		FROM token_transfers tt
		WHERE tt.block_number IS NOT NULL
		ON CONFLICT (block_number) DO NOTHING`)
	fmt.Printf("  ✓ %d blocks\n\n", n)

	fmt.Println("  Step 2: Syncing wallet addresses...")
	n = execQ(db, "senders", `
		INSERT INTO wallet_addresses (address, chain_id, address_type, is_contract, current_label_status)
		SELECT DISTINCT lower(from_address), 1, 'eoa'::address_type_enum, FALSE, 'unlabeled'::label_status_enum
		FROM token_transfers
		WHERE from_address != '0x0000000000000000000000000000000000000000'
		  AND from_address IS NOT NULL
		ON CONFLICT (address) DO NOTHING`)
	fmt.Printf("  ✓ %d sender wallets\n", n)

	n = execQ(db, "receivers", `
		INSERT INTO wallet_addresses (address, chain_id, address_type, is_contract, current_label_status)
		SELECT DISTINCT lower(to_address), 1, 'eoa'::address_type_enum, FALSE, 'unlabeled'::label_status_enum
		FROM token_transfers
		WHERE to_address != '0x0000000000000000000000000000000000000000'
		  AND to_address IS NOT NULL
		ON CONFLICT (address) DO NOTHING`)
	fmt.Printf("  ✓ %d receiver wallets\n\n", n)

	fmt.Println("  Step 3: Token contracts already synced, updating labels...")
	n = execQ(db, "contracts update", `
		UPDATE ethereum_token_contracts etc
		SET symbol = COALESCE(el.label, etc.symbol),
			name   = COALESCE(el.label, etc.name)
		FROM entity_labels el
		WHERE lower(el.address) = etc.contract_address
		  AND el.category = 'token'`)
	fmt.Printf("  ✓ %d token contracts updated\n\n", n)

	fmt.Println("  Step 4: Syncing token transfers (batched 50k)...")
	var already int
	db.QueryRow("SELECT COUNT(*) FROM ethereum_token_transfers").Scan(&already)
	fmt.Printf("  ℹ %d already synced\n", already)
	total := 0
	for offset := already; ; offset += 50000 {
		n = execQ(db, "transfers", `
			INSERT INTO ethereum_token_transfers (
				tx_hash, log_index, block_number, block_timestamp,
				token_contract_id, token_contract_address,
				from_wallet_id, to_wallet_id,
				from_address, to_address, raw_amount, normalized_amount
			)
			SELECT
				'0x' || lpad(tt.id::text, 64, '0'),
				0,
				tt.block_number,
				COALESCE(eb.block_timestamp,
					NOW() - ((22000000 - tt.block_number) * interval '12 seconds')),
				etc.token_contract_id,
				lower(tt.token_address),
				wa_from.wallet_id,
				wa_to.wallet_id,
				lower(tt.from_address),
				lower(tt.to_address),
				CAST(tt.value AS NUMERIC),
				CASE WHEN etc.decimals IS NOT NULL AND etc.decimals > 0
					THEN CAST(tt.value AS NUMERIC) / POWER(10::numeric, etc.decimals)
					ELSE CAST(tt.value AS NUMERIC) / 1e18 END
			FROM token_transfers tt
			LEFT JOIN ethereum_blocks eb ON eb.block_number = tt.block_number
			LEFT JOIN ethereum_token_contracts etc ON etc.contract_address = lower(tt.token_address)
			LEFT JOIN wallet_addresses wa_from ON wa_from.address = lower(tt.from_address)
			LEFT JOIN wallet_addresses wa_to   ON wa_to.address   = lower(tt.to_address)
			WHERE tt.from_address != '0x0000000000000000000000000000000000000000'
			ORDER BY tt.id
			LIMIT 50000 OFFSET $1
			ON CONFLICT (tx_hash, log_index) DO NOTHING`, offset)
		total += int(n)
		if n > 0 {
			fmt.Printf("  ... offset %d: +%d (total: %d)\n", offset, n, total+already)
		}
		if n < 50000 {
			break
		}
	}
	fmt.Printf("  ✓ %d new transfers\n\n", total)

	fmt.Println("  Step 5: Computing wallet features...")
	n = execQ(db, "features", `
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
			COALESCE(s.inbound,0),  COALESCE(s.outbound,0),
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
				MIN(block_number) AS first_block,
				MAX(block_number) AS last_block
			FROM token_transfers
			WHERE from_address!='0x0000000000000000000000000000000000000000'
			GROUP BY lower(from_address)
		) s ON s.addr = wa.address
		WHERE wa.chain_id = 1
		ON CONFLICT (wallet_id) DO UPDATE SET
			tx_count_total=EXCLUDED.tx_count_total,
			tx_count_30d=EXCLUDED.tx_count_30d,
			outbound_tx_count_30d=EXCLUDED.outbound_tx_count_30d,
			unique_inbound_counterparties_30d=EXCLUDED.unique_inbound_counterparties_30d,
			unique_outbound_counterparties_30d=EXCLUDED.unique_outbound_counterparties_30d,
			deposit_wallet_pattern_score=EXCLUDED.deposit_wallet_pattern_score,
			hot_wallet_pattern_score=EXCLUDED.hot_wallet_pattern_score,
			cold_wallet_pattern_score=EXCLUDED.cold_wallet_pattern_score,
			first_seen_block=EXCLUDED.first_seen_block,
			last_seen_block=EXCLUDED.last_seen_block,
			last_computed_at=NOW()`)
	fmt.Printf("  ✓ %d wallet features\n\n", n)

	fmt.Println("  Step 6: Syncing exchange labels...")
	n = execQ(db, "exchange labels", `
		INSERT INTO exchange_wallet_labels (
			wallet_id, exchange_id, wallet_role, wallet_subrole,
			attribution_method, confidence_score, confidence_band,
			review_status, is_primary_label, is_active, analyst_notes, created_by
		)
		SELECT wa.wallet_id, ee.exchange_id,
			CASE WHEN el.label ILIKE '%cold%' THEN 'exchange_cold_wallet'
			     ELSE 'exchange_hot_wallet' END::wallet_role_enum,
			CASE WHEN el.label ILIKE '%cold%' THEN 'eth_reserve'
			     ELSE 'omnibus_deposit' END::wallet_subrole_enum,
			'osint'::attribution_method_enum,
			0.95, 'high'::confidence_band_enum,
			'approved'::review_status_enum,
			TRUE, TRUE, el.label, 'bridge_sync'
		FROM entity_labels el
		JOIN wallet_addresses wa ON wa.address = lower(el.address)
		JOIN exchange_entities ee ON (
			el.label ILIKE '%' || ee.canonical_name || '%'
			OR (ee.canonical_name='HTX'  AND el.label ILIKE '%Huobi%')
			OR (ee.canonical_name='Gate' AND el.label ILIKE '%Gate.io%')
		)
		WHERE el.category = 'exchange'
		ON CONFLICT DO NOTHING`)
	fmt.Printf("  ✓ %d exchange labels\n\n", n)

	elapsed := time.Since(start)
	fmt.Println("  ══════════════════════════════════════════")
	fmt.Printf("  Done in %s\n\n", elapsed.Round(time.Second))
	var blocks, transfers, wallets, features, labels int
	db.QueryRow("SELECT COUNT(*) FROM ethereum_blocks").Scan(&blocks)
	db.QueryRow("SELECT COUNT(*) FROM ethereum_token_transfers").Scan(&transfers)
	db.QueryRow("SELECT COUNT(*) FROM wallet_addresses WHERE chain_id=1").Scan(&wallets)
	db.QueryRow("SELECT COUNT(*) FROM wallet_features_ethereum").Scan(&features)
	db.QueryRow("SELECT COUNT(*) FROM exchange_wallet_labels WHERE is_active=TRUE").Scan(&labels)
	fmt.Printf("  %-28s %d\n", "Blocks:", blocks)
	fmt.Printf("  %-28s %d\n", "Token transfers:", transfers)
	fmt.Printf("  %-28s %d\n", "Wallet addresses:", wallets)
	fmt.Printf("  %-28s %d\n", "Wallet features:", features)
	fmt.Printf("  %-28s %d\n", "Exchange labels:", labels)
	fmt.Println()
}
