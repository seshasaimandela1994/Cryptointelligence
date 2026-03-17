package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// ── Known addresses ───────────────────────────────────────────
var knownAddresses = map[string][2]string{
	"0xbe0eb53f46cd790cd13851d5eff43d12404d33e8": {"Binance Cold Wallet",    "exchange"},
	"0x28c6c06298d514db089934071355e5743bf21d60": {"Binance Hot Wallet",     "exchange"},
	"0x21a31ee1afc51d94c2efccaa2092ad1028285549": {"Binance Hot Wallet 2",   "exchange"},
	"0x56eddb7aa87536c09ccc2793473599fd21a8b17f": {"Binance Hot Wallet 3",   "exchange"},
	"0x4976a4a02f38326660d17bf34b431dc6e2eb2327": {"Kraken Hot Wallet",      "exchange"},
	"0xe853c56864a2ebe4576a807d26fdc4a0ada51919": {"Kraken Cold Wallet",     "exchange"},
	"0x6cc5f688a315f3dc28a7781717a9a798a59fda7b": {"OKX Hot Wallet",         "exchange"},
	"0x503828976d22510aad0201ac7ec88293211d23da": {"Coinbase Hot Wallet",    "exchange"},
	"0xddfabcdc4d8ffc6d5beaf154f18b778f892a0740": {"Coinbase Hot Wallet 2",  "exchange"},
	"0x3cd751e6b0078be393132286c442345e5dc49699": {"Coinbase Hot Wallet 3",  "exchange"},
	"0x7a250d5630b4cf539739df2c5dacb4c659f2488d": {"Uniswap V2 Router",      "defi"},
	"0xe592427a0aece92de3edee1f18e0157c05861564": {"Uniswap V3 Router",      "defi"},
	"0x68b3465833fb72a70ecdf485e0e4c7bd8665fc45": {"Uniswap V3 Router 2",    "defi"},
	"0x00000000219ab540356cbb839cbe05303d7705fa": {"ETH2 Deposit Contract",  "protocol"},
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": {"WETH Contract",          "token"},
	"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": {"USDC Contract",          "token"},
	"0xdac17f958d2ee523a2206206994597c13d831ec7": {"USDT Contract",          "token"},
	"0x2260fac5e5542a773aa44fbcfedf7c193bc2c599": {"WBTC Contract",          "token"},
	"0x6b175474e89094c44da98b954eedeac495271d0f": {"DAI Contract",           "token"},
	"0xd8da6bf26964af9d7eed9e03e53415d37aa96045": {"Vitalik Buterin",        "individual"},
	"0xde0b295669a9fd93d5f28d9ec85e40f4cb697bae": {"Ethereum Foundation",    "foundation"},
	"0xab5801a7d398351b8be11c439e05c5b3259aec9b": {"Vitalik Buterin 2",      "individual"},
}

// ── OFAC sanctions (sample — real list has 10,000+ entries) ──
var sanctionedAddresses = map[string]string{
	"0x7f367cc41522ce07553e823bf3be79a889debe1b": "OFAC SDN - Lazarus Group",
	"0xd882cfc20f52f2599d84b8e8d58c7fb62cfe344b": "OFAC SDN - Lazarus Group",
	"0x901bb9583b24d97e995513c6778dc6888ab6870e": "OFAC SDN - Lazarus Group",
	"0xa7e5d5a720f06526557c513402f2e6b5fa20b008": "OFAC SDN - Lazarus Group",
	"0x8576acc5c05d6ce88f4e49bf65bdf0c62f91353c": "OFAC SDN - Lazarus Group",
	"0x1da5821544e25c636c1417ba96ade4cf6d2f9b5a": "OFAC SDN - Lazarus Group",
	"0x7db418b5d567a4e0e8c59ad71be1fce48f3e6107": "OFAC SDN - Lazarus Group",
	"0x72a5843cc08275c8171e582972aa4fda8c397b2a": "OFAC SDN - Lazarus Group",
	"0x9f4cda013e354b8fc285bf4b9a60460cee7f7ea9": "OFAC SDN - Tornado Cash",
	"0x722122df12d4e14e13ac3b6895a86e84145b6967": "OFAC SDN - Tornado Cash",
	"0xdd4c48c0b24039969fc16d1cdf626eab821d3384": "OFAC SDN - Tornado Cash",
	"0xd90e2f925da726b50c4ed8d0fb90ad053324f31b": "OFAC SDN - Tornado Cash",
}

// ── Claude API call ───────────────────────────────────────────
type ClaudeRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Messages  []Message `json:"messages"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ClaudeResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

type LabelResult struct {
	Label      string `json:"label"`
	Category   string `json:"category"`
	Confidence int    `json:"confidence"`
	Reasoning  string `json:"reasoning"`
}

func callClaude(apiKey, prompt string) (*LabelResult, error) {
	reqBody := ClaudeRequest{
		Model:     "claude-haiku-4-5-20251001",
		MaxTokens: 200,
		Messages: []Message{
			{Role: "user", Content: prompt},
		},
	}

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST",
		"https://api.anthropic.com/v1/messages",
		bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "messages-2023-12-15")
	req.Header.Set("anthropic-beta", "messages-2023-12-15")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	var claudeResp ClaudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return nil, err
	}
	if len(claudeResp.Content) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	text := claudeResp.Content[0].Text
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimSuffix(text, "```")
	text = strings.TrimSpace(text)

	var result LabelResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("parse error: %v raw: %s", err, text)
	}
	return &result, nil
}

func main() {
	_ = godotenv.Load(".env")
	dbURL    := os.Getenv("DATABASE_URL")
	claudeKey := os.Getenv("CLAUDE_API_KEY")
	ctx      := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("DB error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	fmt.Println()
	fmt.Println("  CryptoIntelligence - Phase 4 Entity Labelling")
	fmt.Println("  ===============================================")
	fmt.Println()

	// Start labelling run
	var runID int64
	pool.QueryRow(ctx,
		`INSERT INTO labelling_runs (status) VALUES ('running') RETURNING id`,
	).Scan(&runID)

	totalLabelled  := 0
	sanctionsFound := 0
	claudeLabelled := 0

	// ── Step 1: Label known addresses ────────────────────────
	fmt.Println("  Step 1: Labelling known addresses...")
	for addr, info := range knownAddresses {
		_, err := pool.Exec(ctx, `
			INSERT INTO address_labels
			    (address, label, category, confidence, source, labelled_by)
			VALUES ($1, $2, $3, 99, 'known_db', 'pattern')
			ON CONFLICT (address) DO UPDATE
			SET label=EXCLUDED.label, category=EXCLUDED.category,
			    confidence=99, updated_at=NOW()`,
			addr, info[0], info[1],
		)
		if err == nil {
			totalLabelled++
		}
	}
	fmt.Printf("  ✓ %d known addresses labelled\n\n", len(knownAddresses))

	// ── Step 2: Sanctions check ───────────────────────────────
	fmt.Println("  Step 2: Checking sanctions list...")
	rows, _ := pool.Query(ctx, `
		SELECT DISTINCT from_address FROM token_transfers
		UNION
		SELECT DISTINCT to_address FROM token_transfers`)

	sanctionHits := []string{}
	for rows.Next() {
		var addr string
		rows.Scan(&addr)
		if name, ok := sanctionedAddresses[addr]; ok {
			pool.Exec(ctx, `
				INSERT INTO sanctions_list (address, name, program)
				VALUES ($1, $2, 'OFAC SDN')
				ON CONFLICT (address) DO NOTHING`,
				addr, name,
			)
			pool.Exec(ctx, `
				INSERT INTO address_labels
				    (address, label, category, confidence, source, labelled_by)
				VALUES ($1, $2, 'sanctions', 100, 'OFAC', 'sanctions_check')
				ON CONFLICT (address) DO UPDATE
				SET label=EXCLUDED.label, category='sanctions',
				    confidence=100, updated_at=NOW()`,
				addr, name,
			)
			sanctionHits = append(sanctionHits, addr)
			sanctionsFound++
		}
	}
	rows.Close()

	if sanctionsFound > 0 {
		fmt.Printf("  🚨 %d SANCTIONED addresses found!\n", sanctionsFound)
		for _, addr := range sanctionHits {
			fmt.Printf("     %s\n", addr)
		}
	} else {
		fmt.Println("  ✓ No sanctioned addresses found in your data")
	}
	fmt.Println()

	// ── Step 3: Pattern-based labelling ──────────────────────
	fmt.Println("  Step 3: Pattern-based labelling...")

	// MEV Bot detection: multiple txs in same block
	rows, _ = pool.Query(ctx, `
		SELECT from_address, COUNT(DISTINCT block_number) as blocks,
		       COUNT(*) as total_txs,
		       MAX(COUNT(*)) OVER (PARTITION BY from_address) as max_per_block
		FROM token_transfers
		GROUP BY from_address
		HAVING COUNT(*) > 10
		   AND COUNT(DISTINCT block_number) < COUNT(*) * 0.7
		`)

	mevCount := 0
	for rows.Next() {
		var addr string
		var blocks, totalTxs, maxPerBlock int
		rows.Scan(&addr, &blocks, &totalTxs, &maxPerBlock)
		confidence := 75
		if totalTxs > 50 { confidence = 85 }
		if totalTxs > 100 { confidence = 90 }
		pool.Exec(ctx, `
			INSERT INTO address_labels
			    (address, label, category, sub_category,
			     confidence, source, labelled_by)
			VALUES ($1, 'MEV Bot', 'bot', 'mev',
			        $2, 'pattern', 'mev_detection')
			ON CONFLICT (address) DO NOTHING`,
			addr, confidence,
		)
		mevCount++
		totalLabelled++
	}
	rows.Close()
	fmt.Printf("  ✓ %d MEV bots detected\n", mevCount)

	// High volume trader detection
	rows, _ = pool.Query(ctx, `
		SELECT from_address, COUNT(*) as tx_count
		FROM token_transfers
		GROUP BY from_address
		HAVING COUNT(*) > 50
		`)

	traderCount := 0
	for rows.Next() {
		var addr string
		var txCount int
		rows.Scan(&addr, &txCount)
		pool.Exec(ctx, `
			INSERT INTO address_labels
			    (address, label, category, confidence, source, labelled_by)
			VALUES ($1, 'High Volume Trader', 'trader', 75,
			        'pattern', 'volume_detection')
			ON CONFLICT (address) DO NOTHING`,
			addr,
		)
		traderCount++
		totalLabelled++
	}
	rows.Close()
	fmt.Printf("  ✓ %d high volume traders detected\n", traderCount)

	// Contract detection
	rows, _ = pool.Query(ctx, `
		SELECT address FROM contracts
		WHERE contract_type IS NULL
		`)

	contractCount := 0
	for rows.Next() {
		var addr string
		rows.Scan(&addr)
		pool.Exec(ctx, `
			INSERT INTO address_labels
			    (address, label, category, confidence, source, labelled_by)
			VALUES ($1, 'Smart Contract', 'contract', 80,
			        'on-chain', 'contract_detection')
			ON CONFLICT (address) DO NOTHING`,
			addr,
		)
		contractCount++
		totalLabelled++
	}
	rows.Close()
	fmt.Printf("  ✓ %d smart contracts labelled\n\n", contractCount)

	// ── Step 4: Claude AI labelling ───────────────────────────
	fmt.Println("  Step 4: Claude AI labelling...")

	if claudeKey == "" {
		fmt.Println("  ⚠  CLAUDE_API_KEY not set — skipping AI labelling")
		fmt.Println("     Add to .env: CLAUDE_API_KEY=your_key")
		fmt.Println("     Get key at: https://console.anthropic.com")
	} else {
		// Get unlabelled addresses with transaction history
		rows, _ = pool.Query(ctx, `
			SELECT
				tt.from_address,
				COUNT(*) as tx_count,
				COUNT(DISTINCT tt.token_address) as unique_tokens,
				COUNT(DISTINCT tt.to_address) as unique_destinations
			FROM token_transfers tt
			LEFT JOIN address_labels al ON tt.from_address = al.address
			WHERE al.address IS NULL
			GROUP BY tt.from_address
			ORDER BY tx_count DESC
			LIMIT 20`)

		aiCount := 0
		for rows.Next() {
			var addr string
			var txCount, uniqueTokens, uniqueDests int
			rows.Scan(&addr, &txCount, &uniqueTokens, &uniqueDests)

			prompt := fmt.Sprintf(`You are a blockchain analyst. Classify this Ethereum address based on its on-chain behaviour.

Address: %s
Transaction count: %d
Unique tokens traded: %d
Unique destination addresses: %d

Respond ONLY with a JSON object, no other text:
{"label": "short label", "category": "one of: exchange/defi/trader/bot/individual/contract/unknown", "confidence": 0-100, "reasoning": "one sentence"}`,
				addr, txCount, uniqueTokens, uniqueDests)

			result, err := callClaude(claudeKey, prompt)
			if err != nil {
				fmt.Printf("  ⚠  Claude error for %s: %v\n", addr[:10], err)
				continue
			}

			pool.Exec(ctx, `
				INSERT INTO address_labels
				    (address, label, category, confidence, source, labelled_by)
				VALUES ($1, $2, $3, $4, 'claude_ai', 'claude')
				ON CONFLICT (address) DO NOTHING`,
				addr, result.Label, result.Category, result.Confidence,
			)

			fmt.Printf("  🤖 %s...%s → %s (%s) confidence:%d%%\n",
				addr[:6], addr[len(addr)-4:],
				result.Label, result.Category, result.Confidence)

			claudeLabelled++
			totalLabelled++
			time.Sleep(500 * time.Millisecond)
		}
		rows.Close()
		fmt.Printf("\n  ✓ %d addresses labelled by Claude AI\n", aiCount)
	}

	// ── Final summary ─────────────────────────────────────────
	pool.Exec(ctx, `
		UPDATE labelling_runs
		SET completed_at=NOW(), status='completed',
		    addresses_checked=$1, labels_added=$2, sanctions_found=$3
		WHERE id=$4`,
		totalLabelled, totalLabelled, sanctionsFound, runID,
	)

	fmt.Println()
	fmt.Println("  ═══════════════════════════════════════════════")
	fmt.Println("  Phase 4 Complete!")
	fmt.Printf("  Known addresses labelled:  %d\n", len(knownAddresses))
	fmt.Printf("  Sanctions found:           %d\n", sanctionsFound)
	fmt.Printf("  MEV bots detected:         %d\n", mevCount)
	fmt.Printf("  High volume traders:       %d\n", traderCount)
	fmt.Printf("  Smart contracts:           %d\n", contractCount)
	fmt.Printf("  Claude AI labels:          %d\n", claudeLabelled)
	fmt.Printf("  Total labels written:      %d\n", totalLabelled)
	fmt.Println("  ═══════════════════════════════════════════════")
	fmt.Println()

	// Show results
	fmt.Println("  Labels by category:")
	rows2, _ := pool.Query(ctx, `
		SELECT category, COUNT(*) as count
		FROM address_labels
		GROUP BY category
		ORDER BY count DESC`)
	for rows2.Next() {
		var cat string
		var count int
		rows2.Scan(&cat, &count)
		fmt.Printf("    %-20s %d\n", cat, count)
	}
	rows2.Close()
	fmt.Println()
}
