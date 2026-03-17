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
// CryptoIntelligence — Detection Rules Engine
// Rule-based classification for all 24 categories
// ============================================================

type DetectionRule struct {
	Name        string
	Category    string
	SubCategory string
	Confidence  int
	Description string
	Query       string
}

var detectionRules = []DetectionRule{

	// ── EXCHANGE ECOSYSTEM ──────────────────────────────────────

	{
		Name:        "Exchange User - Bidirectional",
		Category:    "exchange_user",
		SubCategory: "exchange_user",
		Confidence:  90,
		Description: "Sends TO exchange AND receives FROM same exchange",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT s.from_address,
				'Exchange User (Bidirectional)' as label,
				'exchange_user' as category,
				'exchange_user' as sub_category,
				90 as confidence,
				'rule_engine' as source,
				'bidirectional_exchange_rule' as labelled_by
			FROM token_transfers s
			JOIN entity_labels el1 ON s.to_address = el1.address AND el1.category = 'exchange'
			WHERE s.from_address IN (
				SELECT r.to_address FROM token_transfers r
				JOIN entity_labels el2 ON r.from_address = el2.address AND el2.category = 'exchange'
			)
			AND s.from_address NOT IN (SELECT address FROM address_labels)
			AND s.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Exchange Deposit Address",
		Category:    "exchange_user",
		SubCategory: "exchange_deposit",
		Confidence:  85,
		Description: "Receives from 5+ wallets but only sends to exchange",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.to_address,
				'Exchange Deposit Address' as label,
				'exchange_user' as category,
				'exchange_deposit' as sub_category,
				85 as confidence,
				'rule_engine' as source,
				'deposit_address_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.from_address = el.address AND el.category = 'exchange'
			WHERE tt.to_address IN (
				SELECT to_address FROM token_transfers
				GROUP BY to_address
				HAVING COUNT(DISTINCT from_address) >= 5
			)
			AND tt.to_address NOT IN (SELECT address FROM address_labels)
			AND tt.to_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Exchange Withdrawal Recipient",
		Category:    "exchange_user",
		SubCategory: "exchange_withdrawal",
		Confidence:  80,
		Description: "Receives directly from known exchange hot wallet",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.to_address,
				'Exchange Withdrawal Address' as label,
				'exchange_user' as category,
				'exchange_withdrawal' as sub_category,
				80 as confidence,
				'rule_engine' as source,
				'withdrawal_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.from_address = el.address
			WHERE el.label LIKE '%Hot Wallet%'
			AND tt.to_address NOT IN (SELECT address FROM address_labels)
			AND tt.to_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	// ── MEV DETECTION ───────────────────────────────────────────

	{
		Name:        "MEV Bot - Multi TX Same Block",
		Category:    "bot",
		SubCategory: "mev_searcher",
		Confidence:  95,
		Description: "Executes 3+ transactions in same block repeatedly",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT from_address,
				'MEV Bot (Multi-TX)' as label,
				'bot' as category,
				'mev_searcher' as sub_category,
				95 as confidence,
				'rule_engine' as source,
				'mev_multitx_rule' as labelled_by
			FROM (
				SELECT from_address, block_number, COUNT(*) as txs_in_block
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address, block_number
				HAVING COUNT(*) >= 3
			) multi_block
			GROUP BY from_address
			HAVING COUNT(*) >= 3
			AND from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "MEV Builder - Extremely High Frequency",
		Category:    "bot",
		SubCategory: "mev_builder",
		Confidence:  92,
		Description: "5+ transactions per block across many blocks",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT from_address,
				'MEV Builder' as label,
				'bot' as category,
				'mev_builder' as sub_category,
				92 as confidence,
				'rule_engine' as source,
				'mev_builder_rule' as labelled_by
			FROM (
				SELECT from_address, block_number, COUNT(*) as txs
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address, block_number
				HAVING COUNT(*) >= 5
			) high_freq
			GROUP BY from_address
			HAVING COUNT(*) >= 5
			AND from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Arbitrage Bot - Multi Token Same Block",
		Category:    "bot",
		SubCategory: "arbitrage_bot",
		Confidence:  88,
		Description: "Trades 3+ different tokens in same block",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT from_address,
				'Arbitrage Bot' as label,
				'bot' as category,
				'arbitrage_bot' as sub_category,
				88 as confidence,
				'rule_engine' as source,
				'arbitrage_rule' as labelled_by
			FROM (
				SELECT from_address, block_number,
					COUNT(DISTINCT token_address) as tokens_in_block
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address, block_number
				HAVING COUNT(DISTINCT token_address) >= 3
			) arb_blocks
			GROUP BY from_address
			HAVING COUNT(*) >= 3
			AND from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	// ── DEFI DETECTION ──────────────────────────────────────────

	{
		Name:        "Flash Loan User - Mint and Burn Same Block",
		Category:    "defi_user",
		SubCategory: "flashloan_user",
		Confidence:  92,
		Description: "Receives tokens minted from zero address AND burns in same block",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT mint.to_address,
				'Flash Loan User' as label,
				'defi_user' as category,
				'flashloan_user' as sub_category,
				92 as confidence,
				'rule_engine' as source,
				'flashloan_rule' as labelled_by
			FROM token_transfers mint
			JOIN token_transfers burn
				ON mint.to_address = burn.from_address
				AND mint.block_number = burn.block_number
				AND mint.token_address = burn.token_address
			WHERE mint.from_address = '0x0000000000000000000000000000000000000000'
			AND burn.to_address = '0x0000000000000000000000000000000000000000'
			AND mint.to_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Liquidity Provider - AMM Interaction",
		Category:    "defi_user",
		SubCategory: "liquidity_provider",
		Confidence:  82,
		Description: "Interacts with Curve, Balancer, Uniswap V3 pool contracts",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'Liquidity Provider' as label,
				'defi_user' as category,
				'liquidity_provider' as sub_category,
				82 as confidence,
				'rule_engine' as source,
				'lp_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.label IN (
				'Curve 3Pool','Curve Tricrypto2','Balancer Vault',
				'Uniswap V3 Positions NFT','SushiSwap MasterChef',
				'Convex Finance','Convex Booster','Curve FRAXUSDC'
			)
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Yield Farmer - Lending Protocol",
		Category:    "defi_user",
		SubCategory: "yield_farmer",
		Confidence:  80,
		Description: "Deposits into Aave, Compound, or Yearn",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'Yield Farmer' as label,
				'defi_user' as category,
				'yield_farmer' as sub_category,
				80 as confidence,
				'rule_engine' as source,
				'yield_farmer_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.label IN (
				'Aave Pool V3','Aave Lending Pool V2',
				'Compound Comptroller','Yearn ETH Vault',
				'Convex Finance','Lido Staking Router'
			)
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "DEX Trader - Router Interaction",
		Category:    "defi_user",
		SubCategory: "dex_trader",
		Confidence:  78,
		Description: "Uses DEX router contracts for token swaps",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'DEX Trader' as label,
				'defi_user' as category,
				'dex_trader' as sub_category,
				78 as confidence,
				'rule_engine' as source,
				'dex_trader_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.label IN (
				'Uniswap V2 Router','Uniswap V3 Router',
				'Uniswap Universal Router','SushiSwap Router V1',
				'1inch Router V5','1inch Router V6',
				'CoW Protocol Settlement','ParaSwap V5',
				'Odos Router V2','0x Exchange Proxy',
				'KyberSwap Router','Balancer Vault'
			)
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	// ── FINANCIAL BEHAVIOR ──────────────────────────────────────

	{
		Name:        "Whale - Top 1000 by Volume",
		Category:    "trader",
		SubCategory: "whale",
		Confidence:  85,
		Description: "Top 1000 addresses by transaction count",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT from_address,
				'Whale' as label,
				'trader' as category,
				'whale' as sub_category,
				85 as confidence,
				'rule_engine' as source,
				'whale_rule' as labelled_by
			FROM (
				SELECT from_address, COUNT(*) as tx_count
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address
				ORDER BY tx_count DESC
				LIMIT 1000
			) top_wallets
			WHERE from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "High Frequency Trader - 100+ TXs",
		Category:    "trader",
		SubCategory: "high_frequency_trader",
		Confidence:  82,
		Description: "More than 100 token transfers",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT from_address,
				'High Frequency Trader' as label,
				'trader' as category,
				'high_frequency_trader' as sub_category,
				82 as confidence,
				'rule_engine' as source,
				'hft_rule' as labelled_by
			FROM (
				SELECT from_address, COUNT(*) as tx_count
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address
				HAVING COUNT(*) > 100
			) hft
			WHERE from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Retail Trader - 1 to 10 TXs",
		Category:    "retail_trader",
		SubCategory: "retail_trader",
		Confidence:  65,
		Description: "Low activity wallet, 1-10 transactions",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT from_address,
				'Retail Trader' as label,
				'retail_trader' as category,
				'retail_trader' as sub_category,
				65 as confidence,
				'rule_engine' as source,
				'retail_rule' as labelled_by
			FROM (
				SELECT from_address, COUNT(*) as tx_count
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address
				HAVING COUNT(*) BETWEEN 1 AND 10
			) retail
			WHERE from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "New Wallet - Recent First Activity",
		Category:    "retail_trader",
		SubCategory: "new_wallet",
		Confidence:  70,
		Description: "First activity in last 100 blocks, fewer than 5 transactions",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT from_address,
				'New Wallet' as label,
				'retail_trader' as category,
				'new_wallet' as sub_category,
				70 as confidence,
				'rule_engine' as source,
				'new_wallet_rule' as labelled_by
			FROM (
				SELECT from_address,
					MIN(block_number) as first_block,
					COUNT(*) as tx_count
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address
				HAVING MIN(block_number) > (SELECT MAX(number) - 100 FROM blocks)
				AND COUNT(*) <= 5
			) new_wallets
			WHERE from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Dormant Wallet - Early + Inactive",
		Category:    "retail_trader",
		SubCategory: "dormant_wallet",
		Confidence:  68,
		Description: "Active early, less than 3 transactions total",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT from_address,
				'Dormant Wallet' as label,
				'retail_trader' as category,
				'dormant_wallet' as sub_category,
				68 as confidence,
				'rule_engine' as source,
				'dormant_rule' as labelled_by
			FROM (
				SELECT from_address,
					MIN(block_number) as first_block,
					MAX(block_number) as last_block,
					COUNT(*) as tx_count
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address
				HAVING COUNT(*) <= 3
				AND MIN(block_number) < (SELECT MIN(number) + 500 FROM blocks)
				AND MAX(block_number) < (SELECT MAX(number) - 500 FROM blocks)
			) dormant
			WHERE from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	// ── RISK / COMPLIANCE ───────────────────────────────────────

	{
		Name:        "Mixer User - Tornado Cash Interaction",
		Category:    "high_risk",
		SubCategory: "mixer_user",
		Confidence:  97,
		Description: "Sent tokens to or received from any Tornado Cash contract",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'Tornado Cash User' as label,
				'high_risk' as category,
				'mixer_user' as sub_category,
				97 as confidence,
				'rule_engine' as source,
				'mixer_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'mixer'
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Sanctions Exposure - Direct Contact",
		Category:    "high_risk",
		SubCategory: "sanctions_exposure",
		Confidence:  99,
		Description: "Directly transacted with OFAC sanctioned address",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'OFAC Sanctions Exposure' as label,
				'high_risk' as category,
				'sanctions_exposure' as sub_category,
				99 as confidence,
				'rule_engine' as source,
				'sanctions_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'sanctions'
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Scam Contract - Receive Only No Send",
		Category:    "high_risk",
		SubCategory: "scam_contract",
		Confidence:  72,
		Description: "Receives from 50+ wallets but never sends tokens",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT to_address,
				'Suspicious Contract' as label,
				'high_risk' as category,
				'scam_contract' as sub_category,
				72 as confidence,
				'rule_engine' as source,
				'scam_rule' as labelled_by
			FROM (
				SELECT to_address, COUNT(DISTINCT from_address) as senders
				FROM token_transfers
				WHERE to_address != '0x0000000000000000000000000000000000000000'
				GROUP BY to_address
				HAVING COUNT(DISTINCT from_address) > 50
				AND to_address NOT IN (SELECT from_address FROM token_transfers)
				AND to_address NOT IN (SELECT address FROM entity_labels)
			) suspicious
			WHERE to_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Phishing Wallet - Mass Sender",
		Category:    "high_risk",
		SubCategory: "phishing_wallet",
		Confidence:  75,
		Description: "Sends to 100+ unique addresses with tiny values",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT from_address,
				'Potential Phishing Wallet' as label,
				'high_risk' as category,
				'phishing_wallet' as sub_category,
				75 as confidence,
				'rule_engine' as source,
				'phishing_rule' as labelled_by
			FROM (
				SELECT from_address,
					COUNT(DISTINCT to_address) as unique_receivers,
					AVG(CAST(value AS NUMERIC)) as avg_value
				FROM token_transfers
				WHERE from_address != '0x0000000000000000000000000000000000000000'
				GROUP BY from_address
				HAVING COUNT(DISTINCT to_address) > 100
				AND COUNT(DISTINCT to_address) > COUNT(*) * 0.8
				AND AVG(CAST(value AS NUMERIC)) < 1000000
			) phishing
			WHERE from_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	// ── ECOSYSTEM ───────────────────────────────────────────────

	{
		Name:        "Airdrop Farmer - Multiple Mints",
		Category:    "defi_user",
		SubCategory: "airdrop_farmer",
		Confidence:  72,
		Description: "Received tokens minted from zero address 5+ times",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT to_address,
				'Airdrop Farmer' as label,
				'defi_user' as category,
				'airdrop_farmer' as sub_category,
				72 as confidence,
				'rule_engine' as source,
				'airdrop_rule' as labelled_by
			FROM (
				SELECT to_address, COUNT(*) as mint_count
				FROM token_transfers
				WHERE from_address = '0x0000000000000000000000000000000000000000'
				GROUP BY to_address
				HAVING COUNT(*) > 5
			) farmers
			WHERE to_address NOT IN (SELECT address FROM address_labels)
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "NFT Trader - Marketplace Interaction",
		Category:    "nft_trader",
		SubCategory: "nft_trader",
		Confidence:  78,
		Description: "Interacts with OpenSea, Blur, LooksRare, X2Y2",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'NFT Trader' as label,
				'nft_trader' as category,
				'nft_trader' as sub_category,
				78 as confidence,
				'rule_engine' as source,
				'nft_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'nft'
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "DAO Member - Governance Token Holder",
		Category:    "defi_user",
		SubCategory: "dao_member",
		Confidence:  72,
		Description: "Holds or trades UNI, AAVE, CRV, COMP, MKR governance tokens",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'DAO Member' as label,
				'defi_user' as category,
				'dao_member' as sub_category,
				72 as confidence,
				'rule_engine' as source,
				'dao_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.token_address = el.address
			WHERE el.label IN (
				'Uniswap Token (UNI)','Aave Token (AAVE)',
				'Curve DAO Token (CRV)','Balancer Token (BAL)',
				'Compound Token (COMP)','MakerDAO MKR Token',
				'CoW Token (COW)','Convex Token (CVX)'
			)
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Staker - Liquid Staking Protocol",
		Category:    "defi_user",
		SubCategory: "staking_contract",
		Confidence:  80,
		Description: "Uses Lido, Rocket Pool, or ETH2 deposit contract",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'ETH Staker' as label,
				'defi_user' as category,
				'staking_contract' as sub_category,
				80 as confidence,
				'rule_engine' as source,
				'staking_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.label IN (
				'Lido stETH','Lido wstETH','Rocket Pool rETH',
				'ETH2 Deposit Contract','Ethena Staking Contract',
				'Staked ENA Token'
			)
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},

	{
		Name:        "Bridge User - Cross Chain Activity",
		Category:    "bridge_user",
		SubCategory: "bridge_user",
		Confidence:  78,
		Description: "Uses Arbitrum, Optimism, Polygon, Base, or Avalanche bridge",
		Query: `
			INSERT INTO address_labels (address, label, category, sub_category, confidence, source, labelled_by)
			SELECT DISTINCT tt.from_address,
				'Bridge User' as label,
				'bridge_user' as category,
				'bridge_user' as sub_category,
				78 as confidence,
				'rule_engine' as source,
				'bridge_rule' as labelled_by
			FROM token_transfers tt
			JOIN entity_labels el ON tt.to_address = el.address
			WHERE el.category = 'bridge'
			AND tt.from_address NOT IN (SELECT address FROM address_labels)
			AND tt.from_address != '0x0000000000000000000000000000000000000000'
			ON CONFLICT (address) DO NOTHING`,
	},
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
	fmt.Println("  CryptoIntelligence - Detection Rules Engine")
	fmt.Println("  =============================================")
	fmt.Printf("  Total rules: %d\n\n", len(detectionRules))

	totalLabelled := 0
	startTime := time.Now()

	for i, rule := range detectionRules {
		fmt.Printf("  [%2d/%d] %s\n", i+1, len(detectionRules), rule.Name)
		fmt.Printf("         Category: %-25s Confidence: %d%%\n", rule.SubCategory, rule.Confidence)
		fmt.Printf("         Rule: %s\n", rule.Description)

		result, err := db.Exec(rule.Query)
		if err != nil {
			fmt.Printf("         ERROR: %v\n\n", err)
			continue
		}

		rows, _ := result.RowsAffected()
		totalLabelled += int(rows)
		fmt.Printf("         ✓ %d addresses labelled\n\n", rows)
	}

	elapsed := time.Since(startTime)

	fmt.Println("  ══════════════════════════════════════════════")
	fmt.Printf("  Detection Rules Complete!\n")
	fmt.Printf("  Rules executed:    %d\n", len(detectionRules))
	fmt.Printf("  New labels added:  %d\n", totalLabelled)
	fmt.Printf("  Time taken:        %s\n", elapsed.Round(time.Millisecond))
	fmt.Println("  ══════════════════════════════════════════════")

	// Print final summary
	rows, err := db.Query(`
		SELECT
			COALESCE(sub_category, category) as cat,
			COUNT(*) as cnt
		FROM address_labels
		GROUP BY COALESCE(sub_category, category)
		ORDER BY cnt DESC
		LIMIT 15`)
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()

	fmt.Println()
	fmt.Println("  Final Category Breakdown:")
	fmt.Println("  ─────────────────────────────────────────────")
	for rows.Next() {
		var cat string
		var cnt int
		rows.Scan(&cat, &cnt)
		fmt.Printf("  %-30s %d\n", cat, cnt)
	}
	fmt.Println()
}
