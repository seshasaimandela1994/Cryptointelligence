package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
	_ "github.com/lib/pq"
)

// ============================================================
// CryptoIntelligence — Neo4j Graph Classification Engine
// Builds relationship graph + classifies via graph traversal
// ============================================================

type Transfer struct {
	FromAddress  string
	ToAddress    string
	TokenAddress string
	Value        string
	BlockNumber  int64
	TxHash       string
}

type EntityLabel struct {
	Address  string
	Label    string
	Category string
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	neo4jURI := os.Getenv("NEO4J_URI")
	neo4jPass := os.Getenv("NEO4J_PASSWORD")

	if dbURL == "" || neo4jURI == "" || neo4jPass == "" {
		log.Fatal("Missing DATABASE_URL, NEO4J_URI, or NEO4J_PASSWORD")
	}

	// Connect PostgreSQL
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("PostgreSQL connect error:", err)
	}
	defer db.Close()

	// Connect Neo4j
	driver, err := neo4j.NewDriverWithContext(
		neo4jURI,
		neo4j.BasicAuth("neo4j", neo4jPass, ""),
	)
	if err != nil {
		log.Fatal("Neo4j connect error:", err)
	}
	defer driver.Close(context.Background())

	ctx := context.Background()

	fmt.Println()
	fmt.Println("  CryptoIntelligence - Neo4j Graph Engine")
	fmt.Println("  =========================================")
	fmt.Println()

	start := time.Now()

	// ── STEP 1: Setup Neo4j schema ────────────────────────────
	fmt.Println("  Step 1: Setting up Neo4j schema...")
	session := driver.NewSession(ctx, neo4j.SessionConfig{})
	defer session.Close(ctx)

	constraints := []string{
		"CREATE CONSTRAINT address_unique IF NOT EXISTS FOR (a:Address) REQUIRE a.address IS UNIQUE",
		"CREATE CONSTRAINT entity_unique IF NOT EXISTS FOR (e:Entity) REQUIRE e.address IS UNIQUE",
		"CREATE CONSTRAINT token_unique IF NOT EXISTS FOR (t:Token) REQUIRE t.address IS UNIQUE",
		"CREATE INDEX address_category IF NOT EXISTS FOR (a:Address) ON (a.category)",
		"CREATE INDEX address_risk IF NOT EXISTS FOR (a:Address) ON (a.risk_score)",
	}

	for _, constraint := range constraints {
		_, err := session.Run(ctx, constraint, nil)
		if err != nil {
			fmt.Printf("  ⚠ Schema warning: %v\n", err)
		}
	}
	fmt.Println("  ✓ Schema ready")
	fmt.Println()

	// ── STEP 2: Load known entities into Neo4j ────────────────
	fmt.Println("  Step 2: Loading known entities into graph...")

	rows, err := db.Query(`
		SELECT address, label, category
		FROM entity_labels
		ORDER BY category`)
	if err != nil {
		log.Fatal(err)
	}

	entityCount := 0
	batch := make([]map[string]interface{}, 0, 100)

	for rows.Next() {
		var addr, label, category string
		rows.Scan(&addr, &label, &category)
		batch = append(batch, map[string]interface{}{
			"address":  addr,
			"label":    label,
			"category": category,
		})

		if len(batch) >= 100 {
			_, err = session.Run(ctx, `
				UNWIND $batch AS row
				MERGE (e:Entity {address: row.address})
				SET e.label = row.label,
					e.category = row.category,
					e.is_known = true`,
				map[string]interface{}{"batch": batch})
			if err != nil {
				fmt.Printf("  ⚠ Entity batch error: %v\n", err)
			}
			entityCount += len(batch)
			batch = batch[:0]
		}
	}
	rows.Close()

	// Flush remaining
	if len(batch) > 0 {
		session.Run(ctx, `
			UNWIND $batch AS row
			MERGE (e:Entity {address: row.address})
			SET e.label = row.label,
				e.category = row.category,
				e.is_known = true`,
			map[string]interface{}{"batch": batch})
		entityCount += len(batch)
	}

	fmt.Printf("  ✓ %d entities loaded\n\n", entityCount)

	// ── STEP 3: Load address labels ───────────────────────────
	fmt.Println("  Step 3: Loading labelled addresses...")

	labelRows, err := db.Query(`
		SELECT address, label, category, COALESCE(sub_category,'') as sub_cat, confidence
		FROM address_labels
		LIMIT 50000`)
	if err != nil {
		log.Fatal(err)
	}

	addrCount := 0
	addrBatch := make([]map[string]interface{}, 0, 200)

	for labelRows.Next() {
		var addr, label, category, subCat string
		var confidence int
		labelRows.Scan(&addr, &label, &category, &subCat, &confidence)
		addrBatch = append(addrBatch, map[string]interface{}{
			"address":    addr,
			"label":      label,
			"category":   category,
			"sub_cat":    subCat,
			"confidence": confidence,
		})

		if len(addrBatch) >= 200 {
			_, err = session.Run(ctx, `
				UNWIND $batch AS row
				MERGE (a:Address {address: row.address})
				SET a.label = row.label,
					a.category = row.category,
					a.sub_category = row.sub_cat,
					a.confidence = row.confidence`,
				map[string]interface{}{"batch": addrBatch})
			if err != nil {
				fmt.Printf("  ⚠ Address batch error: %v\n", err)
			}
			addrCount += len(addrBatch)
			addrBatch = addrBatch[:0]
		}
	}
	labelRows.Close()

	if len(addrBatch) > 0 {
		session.Run(ctx, `
			UNWIND $batch AS row
			MERGE (a:Address {address: row.address})
			SET a.label = row.label,
				a.category = row.category,
				a.sub_category = row.sub_cat,
				a.confidence = row.confidence`,
			map[string]interface{}{"batch": addrBatch})
		addrCount += len(addrBatch)
	}

	fmt.Printf("  ✓ %d addresses loaded\n\n", addrCount)

	// ── STEP 4: Build transaction relationships ───────────────
	fmt.Println("  Step 4: Building transaction graph (relationships)...")
	fmt.Println("           Loading transfers between known entities...")

	// Only load transfers involving known entities for efficiency
	transferRows, err := db.Query(`
		SELECT DISTINCT
			tt.from_address,
			tt.to_address,
			tt.token_address,
			COUNT(*) as tx_count
		FROM token_transfers tt
		WHERE (
			tt.from_address IN (SELECT address FROM entity_labels)
			OR tt.to_address IN (SELECT address FROM entity_labels)
			OR tt.from_address IN (SELECT address FROM address_labels WHERE category IN ('high_risk','sanctions','exchange_user'))
			OR tt.to_address IN (SELECT address FROM address_labels WHERE category IN ('high_risk','sanctions','exchange_user'))
		)
		AND tt.from_address != '0x0000000000000000000000000000000000000000'
		AND tt.to_address != '0x0000000000000000000000000000000000000000'
		GROUP BY tt.from_address, tt.to_address, tt.token_address
		LIMIT 100000`,
	)
	if err != nil {
		log.Fatal(err)
	}

	relCount := 0
	relBatch := make([]map[string]interface{}, 0, 500)

	for transferRows.Next() {
		var from, to, token string
		var txCount int
		transferRows.Scan(&from, &to, &token, &txCount)
		relBatch = append(relBatch, map[string]interface{}{
			"from":     from,
			"to":       to,
			"token":    token,
			"tx_count": txCount,
		})

		if len(relBatch) >= 500 {
			_, err = session.Run(ctx, `
				UNWIND $batch AS row
				MERGE (a:Address {address: row.from})
				MERGE (b:Address {address: row.to})
				MERGE (a)-[r:SENT_TO {token: row.token}]->(b)
				SET r.tx_count = row.tx_count`,
				map[string]interface{}{"batch": relBatch})
			if err != nil {
				fmt.Printf("  ⚠ Relationship batch error: %v\n", err)
			}
			relCount += len(relBatch)
			relBatch = relBatch[:0]

			if relCount%10000 == 0 {
				fmt.Printf("  ... %d relationships loaded\n", relCount)
			}
		}
	}
	transferRows.Close()

	if len(relBatch) > 0 {
		session.Run(ctx, `
			UNWIND $batch AS row
			MERGE (a:Address {address: row.from})
			MERGE (b:Address {address: row.to})
			MERGE (a)-[r:SENT_TO {token: row.token}]->(b)
			SET r.tx_count = row.tx_count`,
			map[string]interface{}{"batch": relBatch})
		relCount += len(relBatch)
	}

	fmt.Printf("  ✓ %d relationships built\n\n", relCount)

	// ── STEP 5: Graph-based classifications ──────────────────
	fmt.Println("  Step 5: Running graph-based classifications...")
	fmt.Println()

	graphRules := []struct {
		Name        string
		Description string
		Query       string
	}{
		{
			Name:        "DEX Trader via Graph",
			Description: "Interacts with 2+ DEX protocols",
			Query: `
				MATCH (a:Address)-[:SENT_TO]->(e:Address)
				WHERE e.category = 'defi'
				WITH a, COUNT(DISTINCT e) as dex_count
				WHERE dex_count >= 2
				SET a.graph_class = 'dex_trader',
					a.graph_confidence = 85
				RETURN COUNT(a) as classified`,
		},
		{
			Name:        "Exchange User via Graph",
			Description: "Sends to AND receives from exchange",
			Query: `
				MATCH (a:Address)-[:SENT_TO]->(e:Address)
				WHERE e.category = 'exchange'
				MATCH (e2:Address)-[:SENT_TO]->(a)
				WHERE e2.category = 'exchange'
				SET a.graph_class = 'exchange_user',
					a.graph_confidence = 90
				RETURN COUNT(DISTINCT a) as classified`,
		},
		{
			Name:        "High Risk - Tornado Cash Contact",
			Description: "Directly sent to or received from Tornado Cash",
			Query: `
				MATCH (a:Address)-[:SENT_TO]->(tc:Address)
				WHERE tc.category = 'mixer' OR tc.label CONTAINS 'Tornado'
				SET a.graph_class = 'mixer_user',
					a.graph_confidence = 97,
					a.risk_flag = 'TORNADO_CASH_CONTACT'
				RETURN COUNT(a) as classified`,
		},
		{
			Name:        "High Risk - 2 Hops from Tornado Cash",
			Description: "Funded by address that used Tornado Cash",
			Query: `
				MATCH (tc:Address)<-[:SENT_TO]-(hop1:Address)<-[:SENT_TO]-(a:Address)
				WHERE (tc.category = 'mixer' OR tc.label CONTAINS 'Tornado')
				AND a.graph_class IS NULL
				SET a.graph_class = 'indirect_mixer_exposure',
					a.graph_confidence = 70,
					a.risk_flag = 'FUNDED_BY_MIXER_USER'
				RETURN COUNT(DISTINCT a) as classified`,
		},
		{
			Name:        "High Risk - OFAC Sanctions Contact",
			Description: "Directly transacted with sanctioned address",
			Query: `
				MATCH (a:Address)-[:SENT_TO]->(s:Address)
				WHERE s.category = 'sanctions'
				SET a.graph_class = 'sanctions_exposure',
					a.graph_confidence = 99,
					a.risk_flag = 'DIRECT_SANCTIONS_CONTACT'
				RETURN COUNT(a) as classified`,
		},
		{
			Name:        "Wash Trading Detection",
			Description: "Sends to address that sends back within same pattern",
			Query: `
				MATCH (a:Address)-[:SENT_TO]->(b:Address)-[:SENT_TO]->(a)
				WHERE a.address <> b.address
				AND a.graph_class IS NULL
				AND b.graph_class IS NULL
				SET a.graph_class = 'potential_wash_trader',
					a.graph_confidence = 65,
					a.risk_flag = 'CIRCULAR_TRADING'
				RETURN COUNT(DISTINCT a) as classified`,
		},
		{
			Name:        "Bridge Power User",
			Description: "Uses 2+ different bridge protocols",
			Query: `
				MATCH (a:Address)-[:SENT_TO]->(b:Address)
				WHERE b.category = 'bridge'
				WITH a, COUNT(DISTINCT b) as bridge_count
				WHERE bridge_count >= 2
				SET a.graph_class = 'bridge_power_user',
					a.graph_confidence = 82
				RETURN COUNT(a) as classified`,
		},
		{
			Name:        "DeFi Power User",
			Description: "Uses DEX + lending + bridge protocols",
			Query: `
				MATCH (a:Address)-[:SENT_TO]->(e:Address)
				WHERE e.category IN ['defi', 'bridge']
				WITH a, 
					SUM(CASE WHEN e.category = 'defi' THEN 1 ELSE 0 END) as defi_count,
					SUM(CASE WHEN e.category = 'bridge' THEN 1 ELSE 0 END) as bridge_count
				WHERE defi_count >= 3 AND bridge_count >= 1
				AND a.graph_class IS NULL
				SET a.graph_class = 'defi_power_user',
					a.graph_confidence = 80
				RETURN COUNT(a) as classified`,
		},
		{
			Name:        "Cluster - Funded by Same Source",
			Description: "Multiple addresses funded by same wallet",
			Query: `
				MATCH (source:Address)-[:SENT_TO]->(a:Address)
				WITH source, COLLECT(a) as funded, COUNT(a) as cnt
				WHERE cnt >= 5
				UNWIND funded as addr
				SET addr.cluster_source = source.address,
					addr.graph_class = COALESCE(addr.graph_class, 'cluster_member')
				RETURN COUNT(DISTINCT addr) as classified`,
		},
	}

	totalGraphLabelled := 0
	for _, rule := range graphRules {
		fmt.Printf("  ► %s\n", rule.Name)
		fmt.Printf("    %s\n", rule.Description)

		result, err := session.Run(ctx, rule.Query, nil)
		if err != nil {
			fmt.Printf("    ⚠ Error: %v\n\n", err)
			continue
		}

		if result.Next(ctx) {
			record := result.Record()
			classified, _ := record.Get("classified")
			count := 0
			if c, ok := classified.(int64); ok {
				count = int(c)
			}
			totalGraphLabelled += count
			fmt.Printf("    ✓ %d addresses classified\n\n", count)
		}
	}

	// ── STEP 6: Write graph classifications back to PostgreSQL ─
	fmt.Println("  Step 6: Writing graph classifications to PostgreSQL...")

	// Get graph-classified addresses from Neo4j
	graphResult, err := session.Run(ctx, `
		MATCH (a:Address)
		WHERE a.graph_class IS NOT NULL
		AND a.graph_class <> 'cluster_member'
		RETURN a.address as address,
			   a.graph_class as class,
			   a.graph_confidence as confidence,
			   COALESCE(a.risk_flag, '') as risk_flag
		LIMIT 10000`,
		nil)
	if err != nil {
		log.Fatal("Graph result error:", err)
	}

	pgWritten := 0
	for graphResult.Next(ctx) {
		record := graphResult.Record()
		addr, _ := record.Get("address")
		class, _ := record.Get("class")
		conf, _ := record.Get("confidence")
		flag, _ := record.Get("risk_flag")

		addrStr, _ := addr.(string)
		classStr, _ := class.(string)
		confInt, _ := conf.(int64)
		flagStr, _ := flag.(string)

		category := "defi_user"
		switch classStr {
		case "mixer_user", "sanctions_exposure", "indirect_mixer_exposure",
			"potential_wash_trader":
			category = "high_risk"
		case "exchange_user", "exchange_heavy_user":
			category = "exchange_user"
		case "bridge_power_user", "bridge_user":
			category = "bridge_user"
		case "dex_trader", "liquidity_provider", "defi_power_user":
			category = "defi_user"
		}

		label := "Graph: " + classStr
		if flagStr != "" {
			label = "Graph: " + classStr + " [" + flagStr + "]"
		}

		_, err = db.Exec(`
			INSERT INTO address_labels
				(address, label, category, sub_category, confidence, source, labelled_by)
			VALUES ($1, $2, $3, $4, $5, 'graph_engine', 'neo4j_classifier')
			ON CONFLICT (address) DO UPDATE
			SET label = EXCLUDED.label,
				category = EXCLUDED.category,
				sub_category = EXCLUDED.sub_category,
				confidence = EXCLUDED.confidence,
				source = EXCLUDED.source`,
			addrStr, label, category, classStr, int(confInt))
		if err == nil {
			pgWritten++
		}
	}

	fmt.Printf("  ✓ %d graph labels written to PostgreSQL\n\n", pgWritten)

	elapsed := time.Since(start)

	// ── STEP 7: Summary ───────────────────────────────────────
	fmt.Println("  ══════════════════════════════════════════════════════")
	fmt.Printf("  Graph Classification Complete!\n")
	fmt.Printf("  Entities in graph:        %d\n", entityCount)
	fmt.Printf("  Addresses in graph:       %d\n", addrCount)
	fmt.Printf("  Relationships built:      %d\n", relCount)
	fmt.Printf("  Graph rules applied:      %d\n", len(graphRules))
	fmt.Printf("  Graph labels classified:  %d\n", totalGraphLabelled)
	fmt.Printf("  Labels written to PG:     %d\n", pgWritten)
	fmt.Printf("  Time taken:               %s\n", elapsed.Round(time.Second))
	fmt.Println("  ══════════════════════════════════════════════════════")
	fmt.Println()

	// Query Neo4j for interesting patterns
	fmt.Println("  Interesting Graph Patterns Found:")
	fmt.Println("  ──────────────────────────────────────────────────────")

	patterns := []struct {
		Name  string
		Query string
	}{
		{
			"High Risk Addresses (direct sanctions/mixer contact)",
			`MATCH (a:Address) WHERE a.risk_flag IS NOT NULL
			 RETURN a.risk_flag as flag, COUNT(a) as count
			 ORDER BY count DESC`,
		},
		{
			"Most Connected Addresses (hub nodes)",
			`MATCH (a:Address)-[r:SENT_TO]->()
			 WITH a, COUNT(r) as out_degree
			 ORDER BY out_degree DESC LIMIT 5
			 RETURN a.address as address, out_degree,
					COALESCE(a.graph_class, a.category, 'unknown') as class`,
		},
		{
			"Exchange Ecosystem Size",
			`MATCH (a:Address)-[:SENT_TO]->(e:Address)
			 WHERE e.category = 'exchange'
			 RETURN e.label as exchange, COUNT(DISTINCT a) as users
			 ORDER BY users DESC LIMIT 5`,
		},
	}

	for _, p := range patterns {
		fmt.Printf("\n  %s:\n", p.Name)
		pResult, err := session.Run(ctx, p.Query, nil)
		if err != nil {
			fmt.Printf("  ⚠ Error: %v\n", err)
			continue
		}
		for pResult.Next(ctx) {
			record := pResult.Record()
			values := record.Values
			keys := record.Keys
			line := "  "
			for i, key := range keys {
				line += fmt.Sprintf("%s: %v  ", key, values[i])
			}
			fmt.Println(line)
		}
	}

	fmt.Println()
	fmt.Println("  Graph engine complete. Neo4j Aura contains your full")
	fmt.Println("  transaction graph — browse at console.neo4j.io")
	fmt.Println()
}
