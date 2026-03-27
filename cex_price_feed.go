package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

var tokenMap = map[string]string{
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": "weth",
	"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": "usd-coin",
	"0xdac17f958d2ee523a2206206994597c13d831ec7": "tether",
	"0x6b175474e89094c44da98b954eedeac495271d0f": "dai",
	"0x2260fac5e5542a773aa44fbcfedf7c193bc2c599": "wrapped-bitcoin",
	"0x514910771af9ca656af840dff83e8264ecf986ca": "chainlink",
	"0x1f9840a85d5af5bf1d1762f925bdaddc4201f984": "uniswap",
	"0x7fc66500c84a76ad7e9c93437bfc5ac33e2ddae9": "aave",
	"0xd533a949740bb3306d119cc777fa900ba034cd52": "curve-dao-token",
	"0x9f8f72aa9304c8b593d555f12ef6589cc3a579a2": "maker",
	"0x5a98fcbea516cf06857215779fd812ca3bef1b32": "lido-dao",
	"0xae78736cd615f374d3085123a210448e74fc6393": "rocket-pool-eth",
	"0xae7ab96520de3a18e5e111b5eaab095312d7fe84": "staked-ether",
	"0xba100000625a3754423978a60c9317c58a424e3d": "balancer",
	"0x4e3fbd56cd56c3e72c1403e103b45db9da5b9d2b": "convex-finance",
}

type CGPrice struct {
	USD float64 `json:"usd"`
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

	daemon := len(os.Args) > 1 && os.Args[1] == "--daemon"

	fmt.Println()
	fmt.Println("  CryptoIntelligence — CoinGecko Price Feed")
	fmt.Println("  ==========================================")
	fmt.Println()

	fetch(db)

	if daemon {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 5, 0, 0, time.UTC)
			fmt.Printf("  Next update: %s UTC\n\n", next.Format("2006-01-02 15:04"))
			time.Sleep(time.Until(next))
			fetch(db)
		}
	}
}

func fetch(db *sql.DB) {
	fmt.Printf("  [%s] Fetching from CoinGecko...\n", time.Now().Format("15:04:05"))
	start := time.Now()
	today := time.Now().UTC().Format("2006-01-02")

	// Build ID list
	idToContract := map[string]string{}
	ids := []string{"ethereum"}
	for contract, cgID := range tokenMap {
		ids = append(ids, cgID)
		idToContract[cgID] = contract
	}

	// Fetch in one batch
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd",
		strings.Join(ids, ","))

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil || resp.StatusCode != 200 {
		fmt.Printf("  ⚠ CoinGecko unavailable (%v), using fallback prices\n", err)
		insertFallback(db, today)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result map[string]CGPrice
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("  ⚠ parse error: %v, using fallback\n", err)
		insertFallback(db, today)
		return
	}

	inserted := 0

	// ETH
	if p, ok := result["ethereum"]; ok && p.USD > 0 {
		db.Exec(`
			INSERT INTO asset_price_history (asset_type, chain_id, symbol, price_date, price_usd, source_name)
			VALUES ('ETH', 1, 'ETH', $1, $2, 'coingecko')
			ON CONFLICT DO NOTHING`, today, p.USD)
		fmt.Printf("  ETH:  $%.2f\n", p.USD)
		inserted++
	}

	// Tokens
	for cgID, p := range result {
		if cgID == "ethereum" || p.USD <= 0 { continue }
		contract, ok := idToContract[cgID]
		if !ok { continue }

		var tcID int64
		err := db.QueryRow(`SELECT token_contract_id FROM ethereum_token_contracts WHERE contract_address = $1`, contract).Scan(&tcID)
		if err != nil { continue }

		var sym string
		db.QueryRow(`SELECT COALESCE(symbol,'?') FROM ethereum_token_contracts WHERE token_contract_id = $1`, tcID).Scan(&sym)

		db.Exec(`
			INSERT INTO asset_price_history (asset_type, chain_id, token_contract_id, symbol, price_date, price_usd, source_name)
			VALUES ('ERC20', 1, $1, $2, $3, $4, 'coingecko')
			ON CONFLICT DO NOTHING`, tcID, sym, today, p.USD)
		fmt.Printf("  %-8s $%.4f\n", sym, p.USD)
		inserted++
	}

	// Apply to transfers
	res, _ := db.Exec(`
		UPDATE ethereum_token_transfers ett
		SET usd_value_estimate = ett.normalized_amount * aph.price_usd
		FROM asset_price_history aph
		WHERE aph.token_contract_id = ett.token_contract_id
		  AND aph.price_date = $1
		  AND ett.usd_value_estimate IS NULL
		  AND ett.normalized_amount IS NOT NULL`, today)
	nTx, _ := res.RowsAffected()

	// Apply to sweep events
	db.Exec(`
		UPDATE deposit_sweep_events dse
		SET sweep_amount_usd = (
			SELECT SUM(ett.normalized_amount * aph.price_usd)
			FROM ethereum_token_transfers ett
			JOIN asset_price_history aph ON aph.token_contract_id = ett.token_contract_id
				AND aph.price_date = $1
			WHERE ett.from_wallet_id = dse.source_wallet_id
			  AND (dse.token_contract_id IS NULL OR ett.token_contract_id = dse.token_contract_id)
		)
		WHERE dse.sweep_amount_usd IS NULL`, today)

	fmt.Printf("\n  ✓ %d prices inserted, %d transfers valued\n", inserted, nTx)
	fmt.Printf("  Done in %s\n\n", time.Since(start).Round(time.Millisecond))
}

func insertFallback(db *sql.DB, today string) {
	prices := map[string]float64{
		"ETH":3500,"WETH":3500,"WBTC":88000,
		"USDC":1,"USDT":1,"DAI":1,"FRAX":1,"LUSD":1,"TUSD":1,"BUSD":1,
		"LINK":18,"UNI":8,"AAVE":180,"CRV":0.45,"MKR":1400,
		"SNX":2.5,"LDO":1.8,"BAL":2.8,"CVX":3.5,"ARB":0.75,"OP":1.2,
		"stETH":3500,"wstETH":4100,"rETH":3800,
	}
	db.Exec(`INSERT INTO asset_price_history (asset_type,chain_id,symbol,price_date,price_usd,source_name)
		VALUES ('ETH',1,'ETH',$1,$2,'fallback') ON CONFLICT DO NOTHING`, today, prices["ETH"])
	for sym, price := range prices {
		db.Exec(`
			INSERT INTO asset_price_history (asset_type,chain_id,token_contract_id,symbol,price_date,price_usd,source_name)
			SELECT 'ERC20',1,token_contract_id,$1,$2,$3,'fallback'
			FROM ethereum_token_contracts WHERE symbol=$1
			ON CONFLICT DO NOTHING`, sym, today, price)
	}
	res, _ := db.Exec(`
		UPDATE ethereum_token_transfers ett
		SET usd_value_estimate = ett.normalized_amount * aph.price_usd
		FROM asset_price_history aph
		WHERE aph.token_contract_id = ett.token_contract_id
		  AND aph.price_date = $1
		  AND ett.usd_value_estimate IS NULL
		  AND ett.normalized_amount IS NOT NULL`, today)
	n, _ := res.RowsAffected()
	fmt.Printf("  ✓ Fallback prices applied, %d transfers valued\n\n", n)
}
