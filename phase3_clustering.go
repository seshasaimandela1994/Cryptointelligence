package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

// ── Union-Find ────────────────────────────────────────────────
type UnionFind struct {
	parent map[string]string
	rank   map[string]int
	size   map[string]int
}

func NewUnionFind() *UnionFind {
	return &UnionFind{
		parent: make(map[string]string),
		rank:   make(map[string]int),
		size:   make(map[string]int),
	}
}

func (uf *UnionFind) Add(addr string) {
	if _, exists := uf.parent[addr]; !exists {
		uf.parent[addr] = addr
		uf.rank[addr]   = 0
		uf.size[addr]   = 1
	}
}

func (uf *UnionFind) Find(addr string) string {
	if _, exists := uf.parent[addr]; !exists {
		uf.Add(addr)
	}
	if uf.parent[addr] != addr {
		uf.parent[addr] = uf.Find(uf.parent[addr])
	}
	return uf.parent[addr]
}

func (uf *UnionFind) Union(a, b string) bool {
	rootA := uf.Find(a)
	rootB := uf.Find(b)
	if rootA == rootB {
		return false
	}
	if uf.rank[rootA] < uf.rank[rootB] {
		uf.parent[rootA] = rootB
		uf.size[rootB]  += uf.size[rootA]
	} else if uf.rank[rootA] > uf.rank[rootB] {
		uf.parent[rootB] = rootA
		uf.size[rootA]  += uf.size[rootB]
	} else {
		uf.parent[rootB] = rootA
		uf.size[rootA]  += uf.size[rootB]
		uf.rank[rootA]++
	}
	return true
}

func (uf *UnionFind) Clusters() map[string][]string {
	groups := make(map[string][]string)
	for addr := range uf.parent {
		root := uf.Find(addr)
		groups[root] = append(groups[root], addr)
	}
	result := make(map[string][]string)
	for root, members := range groups {
		if len(members) > 1 {
			result[root] = members
		}
	}
	return result
}

// ── Known exchanges to exclude ────────────────────────────────
var knownExchanges = map[string]bool{
	"0xbe0eb53f46cd790cd13851d5eff43d12404d33e8": true,
	"0x28c6c06298d514db089934071355e5743bf21d60": true,
	"0x21a31ee1afc51d94c2efccaa2092ad1028285549": true,
	"0x7a250d5630b4cf539739df2c5dacb4c659f2488d": true,
	"0xe592427a0aece92de3edee1f18e0157c05861564": true,
	"0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2": true,
	"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": true,
	"0xdac17f958d2ee523a2206206994597c13d831ec7": true,
}

func isExcluded(addr string) bool {
	return knownExchanges[addr]
}

func main() {
	_ = godotenv.Load(".env")
	dbURL := os.Getenv("DATABASE_URL")
	ctx   := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Printf("DB error: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	fmt.Println()
	fmt.Println("  CryptoIntelligence - Phase 3 Clustering Engine")
	fmt.Println("  ================================================")
	fmt.Println()

	uf := NewUnionFind()
	totalLinks  := 0
	totalMerges := 0

	// ── Load all addresses ────────────────────────────────────
	fmt.Print("  Loading addresses... ")
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT from_address FROM token_transfers
		UNION
		SELECT DISTINCT to_address FROM token_transfers`)
	if err != nil {
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
	addrCount := 0
	for rows.Next() {
		var addr string
		rows.Scan(&addr)
		uf.Add(addr)
		addrCount++
	}
	rows.Close()
	fmt.Printf("%d addresses loaded\n\n", addrCount)

	// ── Heuristic 1: Co-spend ─────────────────────────────────
	// Find senders who sent tokens to the same destination
	// in the same block — they are likely the same entity
	fmt.Println("  Running Heuristic 1: Co-spend...")
	fmt.Println("  Logic: if A and B both sent tokens to C in same block")
	fmt.Println("         → A and B are probably the same person")
	fmt.Println()

	rows, err = pool.Query(ctx, `
		SELECT t1.from_address, t2.from_address, t1.tx_hash
		FROM token_transfers t1
		JOIN token_transfers t2
			ON  t1.to_address    = t2.to_address
			AND t1.block_number  = t2.block_number
			AND t1.from_address  < t2.from_address
			AND t1.from_address != t2.from_address
		LIMIT 5000`)
	if err != nil {
		fmt.Printf("  Co-spend query error: %v\n", err)
	} else {
		links := 0
		merges := 0
		for rows.Next() {
			var addrA, addrB, txHash string
			rows.Scan(&addrA, &addrB, &txHash)
			if isExcluded(addrA) || isExcluded(addrB) {
				continue
			}
			links++
			if uf.Union(addrA, addrB) {
				merges++
			}
		}
		rows.Close()
		totalLinks  += links
		totalMerges += merges
		fmt.Printf("  ✓ Co-spend: %d links found, %d clusters merged\n\n", links, merges)
	}

	// ── Heuristic 2: Deposit Address ─────────────────────────
	// Find addresses that receive from many wallets
	// but only send to one destination
	fmt.Println("  Running Heuristic 2: Deposit Address...")
	fmt.Println("  Logic: if address A receives from many wallets")
	fmt.Println("         but only sends to address B")
	fmt.Println("         → A is a deposit address, link A to B")
	fmt.Println()

	rows, err = pool.Query(ctx, `
		SELECT
			sender.from_address AS deposit_addr,
			sender.to_address   AS hot_wallet,
			receiver.from_address AS user_wallet
		FROM (
			SELECT from_address, to_address, COUNT(*) as sends
			FROM token_transfers
			GROUP BY from_address, to_address
			HAVING COUNT(DISTINCT to_address) = 1
			   AND COUNT(*) >= 2
		) sender
		JOIN token_transfers receiver
			ON receiver.to_address = sender.from_address
		WHERE sender.from_address != receiver.from_address
		LIMIT 3000`)
	if err != nil {
		fmt.Printf("  Deposit address query error: %v\n", err)
	} else {
		links  := 0
		merges := 0
		for rows.Next() {
			var depositAddr, hotWallet, userWallet string
			rows.Scan(&depositAddr, &hotWallet, &userWallet)
			if isExcluded(depositAddr) || isExcluded(userWallet) {
				continue
			}
			links++
			if uf.Union(depositAddr, userWallet) {
				merges++
			}
		}
		rows.Close()
		totalLinks  += links
		totalMerges += merges
		fmt.Printf("  ✓ Deposit address: %d links found, %d clusters merged\n\n", links, merges)
	}

	// ── Heuristic 3: Gas Station ──────────────────────────────
	// Find addresses that funded multiple wallets
	// with small amounts before their first transaction
	fmt.Println("  Running Heuristic 3: Shared Funding...")
	fmt.Println("  Logic: if address A sent tokens to both B and C")
	fmt.Println("         → B and C might be the same entity")
	fmt.Println()

	rows, err = pool.Query(ctx, `
		SELECT t1.to_address, t2.to_address, t1.from_address
		FROM token_transfers t1
		JOIN token_transfers t2
			ON  t1.from_address = t2.from_address
			AND t1.to_address   < t2.to_address
			AND t1.to_address  != t2.to_address
			AND ABS(t1.block_number::bigint - t2.block_number::bigint) <= 5
		WHERE t1.from_address NOT IN (
			SELECT address FROM entity_labels WHERE category = 'exchange'
		)
		LIMIT 3000`)
	if err != nil {
		fmt.Printf("  Shared funding query error: %v\n", err)
	} else {
		links  := 0
		merges := 0
		for rows.Next() {
			var addrA, addrB, funder string
			rows.Scan(&addrA, &addrB, &funder)
			if isExcluded(addrA) || isExcluded(addrB) || isExcluded(funder) {
				continue
			}
			links++
			if uf.Union(addrA, addrB) {
				merges++
			}
		}
		rows.Close()
		totalLinks  += links
		totalMerges += merges
		fmt.Printf("  ✓ Shared funding: %d links found, %d clusters merged\n\n", links, merges)
	}

	// ── Get final clusters ────────────────────────────────────
	clusters := uf.Clusters()
	fmt.Printf("  ─────────────────────────────────────────\n")
	fmt.Printf("  Total links found:    %d\n", totalLinks)
	fmt.Printf("  Total merges:         %d\n", totalMerges)
	fmt.Printf("  Clusters found:       %d\n", len(clusters))
	fmt.Printf("  ─────────────────────────────────────────\n\n")

	// ── Write clusters to PostgreSQL ──────────────────────────
	fmt.Println("  Writing clusters to database...")
	written := 0
	for root, members := range clusters {
		confidence := 50
		if len(members) >= 10 { confidence = 90 }
		if len(members) >= 5  { confidence = 80 }
		if len(members) >= 3  { confidence = 70 }
		if len(members) == 2  { confidence = 60 }

		var clusterID string
		err := pool.QueryRow(ctx, `
			INSERT INTO clusters
			    (canonical_address, size, confidence, heuristics_used)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (canonical_address) DO UPDATE
			SET size=EXCLUDED.size, confidence=EXCLUDED.confidence,
			    updated_at=NOW()
			RETURNING id`,
			root, len(members), confidence,
			[]string{"co_spend", "deposit_address", "shared_funding"},
		).Scan(&clusterID)
		if err != nil {
			continue
		}

		for _, member := range members {
			_, _ = pool.Exec(ctx, `
				INSERT INTO cluster_memberships
				    (address, cluster_id, heuristic, confidence)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (address) DO NOTHING`,
				member, clusterID, "engine", confidence,
			)
		}
		written++

		if written <= 5 {
			fmt.Printf("  Cluster #%d: %d addresses (root: %s...%s)\n",
				written, len(members),
				root[:6], root[len(root)-4:])
		}
	}

	fmt.Printf("\n  ✓ Written %d clusters to database\n", written)

	// ── Log heuristic run ─────────────────────────────────────
	_, _ = pool.Exec(ctx, `
		INSERT INTO heuristic_runs
		    (heuristic, completed_at, links_found, clusters_merged, status)
		VALUES ($1, NOW(), $2, $3, $4)`,
		"full_engine", totalLinks, totalMerges, "completed",
	)

	fmt.Println()
	fmt.Println("  ═══════════════════════════════════════════")
	fmt.Println("  Phase 3 Complete!")
	fmt.Printf("  %d clusters written to PostgreSQL\n", written)
	fmt.Println("  ═══════════════════════════════════════════")
	fmt.Println()

	// ── Show top clusters ─────────────────────────────────────
	fmt.Println("  Top 10 largest clusters:")
	fmt.Println()
	rows2, _ := pool.Query(ctx, `
		SELECT canonical_address, size, confidence
		FROM clusters
		ORDER BY size DESC
		LIMIT 10`)
	i := 1
	for rows2.Next() {
		var addr string
		var size, conf int
		rows2.Scan(&addr, &size, &conf)
		fmt.Printf("  #%d  %s...%s  %d addresses  confidence:%d%%\n",
			i, addr[:6], addr[len(addr)-4:], size, conf)
		i++
	}
	rows2.Close()
	fmt.Println()

	elapsed := time.Since(time.Now().Add(-time.Second))
	fmt.Printf("  Total time: %s\n\n", elapsed)
}
