package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/lib/pq"
)

// ============================================================
// CryptoIntelligence — Bridge Monitor & Webhook Alert Engine
// Real-time cross-chain movement detection
// ============================================================

type BridgeAlert struct {
	AlertType     string    `json:"alert_type"`
	AlertLevel    string    `json:"alert_level"`
	Address       string    `json:"address"`
	WalletLabel   string    `json:"wallet_label"`
	RiskScore     int       `json:"risk_score"`
	RiskLevel     string    `json:"risk_level"`
	RiskFactors   []string  `json:"risk_factors"`
	BridgeName    string    `json:"bridge_name"`
	BridgeType    string    `json:"bridge_type"`
	TargetChains  []string  `json:"target_chains"`
	IsHacked      bool      `json:"is_hacked"`
	TxCount       int       `json:"tx_count"`
	BlockNumber   int64     `json:"block_number"`
	DetectedAt    time.Time `json:"detected_at"`
	Recommendation string   `json:"recommendation"`
	Message       string    `json:"message"`
}

type WebhookSubscription struct {
	ID       int
	URL      string
	MinRisk  int
	AlertTypes []string
}

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
	fmt.Println("  CryptoIntelligence — Bridge Monitor")
	fmt.Println("  =====================================")
	fmt.Println("  Scanning for cross-chain risk patterns...")
	fmt.Println()

	// ── DETECTION 1: High risk wallets using bridges ──────
	fmt.Println("  [1] Scanning high-risk wallets on bridges...")
	rows, err := db.Query(`
		SELECT
			tt.from_address,
			COALESCE(al.label, 'Unknown') as wallet_label,
			rs.risk_score,
			rs.risk_level,
			COALESCE(array_to_string(rs.risk_factors, ','), '') as risk_factors,
			el.label as bridge_name,
			COALESCE(el.bridge_type, 'UNKNOWN') as bridge_type,
			COALESCE(array_to_string(el.target_chains, ','), '') as target_chains,
			COALESCE(el.is_hacked, FALSE) as is_hacked,
			COUNT(*) as tx_count,
			MAX(tt.block_number) as last_block
		FROM token_transfers tt
		JOIN entity_labels el ON tt.to_address = el.address
		JOIN risk_scores rs ON tt.from_address = rs.address
		LEFT JOIN address_labels al ON tt.from_address = al.address
		WHERE el.category = 'bridge'
		AND rs.risk_score >= 15
		GROUP BY tt.from_address, al.label, rs.risk_score,
			rs.risk_level, rs.risk_factors, el.label,
			el.bridge_type, el.target_chains, el.is_hacked
		ORDER BY rs.risk_score DESC`)
	if err != nil {
		log.Fatal("Query error:", err)
	}
	defer rows.Close()

	var alerts []BridgeAlert
	seen := make(map[string]bool)

	for rows.Next() {
		var addr, label, riskLevel, bridge, bridgeType, riskFactorsStr, targetChainsStr string
		var riskScore, txCount int
		var lastBlock int64
		var isHacked bool

		err := rows.Scan(&addr, &label, &riskScore, &riskLevel,
			&riskFactorsStr, &bridge, &bridgeType, &targetChainsStr,
			&isHacked, &txCount, &lastBlock)
		if err != nil {
			fmt.Printf("  SCAN ERROR: %v\n", err)
			continue
		}
		riskFactors := []string{riskFactorsStr}
		targetChains := []string{targetChainsStr}

		key := addr + bridge
		if seen[key] {
			continue
		}
		seen[key] = true

		// Determine alert level
		alertLevel := "MEDIUM"
		if riskScore >= 80 {
			alertLevel = "CRITICAL"
		} else if riskScore >= 60 {
			alertLevel = "HIGH"
		}

		// Build recommendation
		rec := "MONITOR"
		if riskScore >= 80 {
			rec = "BLOCK — Report to compliance team immediately"
		} else if riskScore >= 60 {
			rec = "REVIEW — Enhanced due diligence required"
		} else if isHacked {
			rec = "FLAG — Using previously hacked bridge"
		}

		// Build message
		msg := fmt.Sprintf(
			"Risk score %d wallet '%s' used %s (%s). "+
				"Funds may be moving to: %v",
			riskScore, label, bridge, bridgeType, targetChains)

		alert := BridgeAlert{
			AlertType:      "BRIDGE_RISK_DETECTED",
			AlertLevel:     alertLevel,
			Address:        addr,
			WalletLabel:    label,
			RiskScore:      riskScore,
			RiskLevel:      riskLevel,
			RiskFactors:    riskFactors,
			BridgeName:     bridge,
			BridgeType:     bridgeType,
			TargetChains:   targetChains,
			IsHacked:       isHacked,
			TxCount:        txCount,
			BlockNumber:    lastBlock,
			DetectedAt:     time.Now(),
			Recommendation: rec,
			Message:        msg,
		}
		alerts = append(alerts, alert)

		icon := "⚠"
		if alertLevel == "CRITICAL" { icon = "🔴" }
		if alertLevel == "HIGH"     { icon = "🟠" }

		fmt.Printf("  %s [%s] %s\n", icon, alertLevel, addr[:10]+"..."+addr[38:])
		fmt.Printf("     Label:  %s\n", label)
		fmt.Printf("     Risk:   %d/100 %s\n", riskScore, riskLevel)
		fmt.Printf("     Bridge: %s (%s)\n", bridge, bridgeType)
		fmt.Printf("     Chains: %v\n", targetChains)
		fmt.Printf("     Action: %s\n\n", rec)
	}

	fmt.Printf("  Total bridge alerts: %d\n\n", len(alerts))

	// ── DETECTION 2: Wallets using HACKED bridges ─────────
	fmt.Println("  [2] Scanning for hacked bridge usage...")
	hackedRows, err := db.Query(`
		SELECT DISTINCT
			tt.from_address,
			COALESCE(al.label, 'Unknown') as label,
			el.label as bridge_name,
			el.hack_amount_usd,
			COALESCE(rs.risk_score, 0) as risk_score
		FROM token_transfers tt
		JOIN entity_labels el ON tt.to_address = el.address
		LEFT JOIN risk_scores rs ON tt.from_address = rs.address
		LEFT JOIN address_labels al ON tt.from_address = al.address
		WHERE el.category = 'bridge'
		AND el.is_hacked = TRUE
		ORDER BY risk_score DESC`)
	if err != nil {
		log.Printf("Hacked bridge query error: %v", err)
	} else {
		hackedCount := 0
		for hackedRows.Next() {
			var addr, label, bridge string
			var hackAmount, riskScore int64
			hackedRows.Scan(&addr, &label, &bridge, &hackAmount, &riskScore)
			fmt.Printf("  ⚠ HACKED BRIDGE USER: %s\n", addr[:10]+"..."+addr[38:])
			fmt.Printf("    Label:  %s\n", label)
			fmt.Printf("    Bridge: %s (HACKED - $%dM)\n", bridge, hackAmount/1000000)
			fmt.Printf("    Risk:   %d/100\n\n", riskScore)
			hackedCount++
		}
		hackedRows.Close()
		if hackedCount == 0 {
			fmt.Println("  ✓ No hacked bridge interactions found")
		}
	}

	// ── DETECTION 3: Multi-bridge users ───────────────────
	fmt.Println("\n  [3] Top cross-chain operators...")
	multiRows, err := db.Query(`
		SELECT
			tt.from_address,
			COALESCE(al.label, 'Unknown') as label,
			COUNT(DISTINCT el.label) as bridges_used,
			COUNT(DISTINCT el.bridge_type) as bridge_types,
			COUNT(*) as total_txs,
			COALESCE(rs.risk_score, 0) as risk_score,
			COALESCE(rs.risk_level, 'UNKNOWN') as risk_level
		FROM token_transfers tt
		JOIN entity_labels el ON tt.to_address = el.address
		LEFT JOIN risk_scores rs ON tt.from_address = rs.address
		LEFT JOIN address_labels al ON tt.from_address = al.address
		WHERE el.category = 'bridge'
		GROUP BY tt.from_address, al.label, rs.risk_score, rs.risk_level
		HAVING COUNT(DISTINCT el.label) >= 3
		ORDER BY bridges_used DESC
		LIMIT 10`)
	if err != nil {
		log.Printf("Multi-bridge query error: %v", err)
	} else {
		fmt.Printf("  %-12s %-25s %s %s %s %s\n",
			"ADDRESS", "LABEL", "BRIDGES", "TYPES", "TXS", "RISK")
		fmt.Println("  " + "──────────────────────────────────────────────────────────────────────")
		for multiRows.Next() {
			var addr, label, riskLevel string
			var bridges, types, txs, risk int
			multiRows.Scan(&addr, &label, &bridges, &types, &txs, &risk, &riskLevel)
			fmt.Printf("  %s %-25s %d      %d     %d   %d %s\n",
				addr[:10]+"..."+addr[38:], label, bridges, types, txs, risk, riskLevel)
		}
		multiRows.Close()
	}

	// ── STORE ALERTS IN DB ────────────────────────────────
	fmt.Println("\n  [4] Storing alerts in database...")
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS bridge_alerts (
			id              SERIAL PRIMARY KEY,
			address         CHAR(42) NOT NULL,
			wallet_label    VARCHAR(255),
			risk_score      INTEGER,
			risk_level      VARCHAR(20),
			bridge_name     VARCHAR(255),
			bridge_type     VARCHAR(50),
			alert_level     VARCHAR(20),
			recommendation  TEXT,
			tx_count        INTEGER,
			detected_at     TIMESTAMP DEFAULT NOW()
		)`)
	if err != nil {
		log.Printf("Create table error: %v", err)
	}

	stored := 0
	for _, alert := range alerts {
		_, err = db.Exec(`
			INSERT INTO bridge_alerts
				(address, wallet_label, risk_score, risk_level,
				 bridge_name, bridge_type, alert_level, recommendation, tx_count)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
			ON CONFLICT DO NOTHING`,
			alert.Address, alert.WalletLabel, alert.RiskScore,
			alert.RiskLevel, alert.BridgeName, alert.BridgeType,
			alert.AlertLevel, alert.Recommendation, alert.TxCount)
		if err == nil {
			stored++
		}
	}
	fmt.Printf("  ✓ %d alerts stored\n", stored)

	// ── FIRE WEBHOOKS ─────────────────────────────────────
	fmt.Println("\n  [5] Firing webhooks...")
	webhookRows, err := db.Query(`
		SELECT id, url FROM webhook_subscriptions
		WHERE is_active = TRUE LIMIT 10`)
	if err != nil {
		fmt.Println("  No webhooks configured yet")
		fmt.Println("  Register one: POST /v1/webhooks")
	} else {
		webhookCount := 0
		for webhookRows.Next() {
			var id int
			var url string
			webhookRows.Scan(&id, &url)

			for _, alert := range alerts {
				if alert.RiskScore >= 60 {
					payload, _ := json.Marshal(alert)
					resp, err := http.Post(url,
						"application/json",
						bytes.NewBuffer(payload))
					if err == nil {
						resp.Body.Close()
						webhookCount++
						fmt.Printf("  ✓ Alert fired to %s\n", url)
					}
				}
			}
		}
		webhookRows.Close()
		if webhookCount == 0 {
			fmt.Println("  No active webhooks to fire")
		}
	}

	// ── FINAL SUMMARY ─────────────────────────────────────
	fmt.Println()
	fmt.Println("  ══════════════════════════════════════════════")
	fmt.Printf("  Bridge Monitor Complete!\n")
	fmt.Printf("  Total alerts:     %d\n", len(alerts))
	fmt.Printf("  Alerts stored:    %d\n", stored)
	fmt.Printf("  Scanned at:       %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println("  ══════════════════════════════════════════════")
	fmt.Println()
}
