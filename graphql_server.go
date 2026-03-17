package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/graphql-go/graphql"
	"github.com/graphql-go/handler"
	_ "github.com/lib/pq"
)

// ============================================================
// CryptoIntelligence — Phase 10 GraphQL API
// Flexible queries for dashboards and developers
// ============================================================

var db *sql.DB

// ── GraphQL Types ─────────────────────────────────────────────
var riskType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Risk",
	Fields: graphql.Fields{
		"risk_score":     &graphql.Field{Type: graphql.Int},
		"risk_level":     &graphql.Field{Type: graphql.String},
		"risk_factors":   &graphql.Field{Type: graphql.NewList(graphql.String)},
		"sanctions_hit":  &graphql.Field{Type: graphql.Boolean},
		"mixer_hit":      &graphql.Field{Type: graphql.Boolean},
		"recommendation": &graphql.Field{Type: graphql.String},
		"computed_at":    &graphql.Field{Type: graphql.String},
	},
})

var behaviorType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Behavior",
	Fields: graphql.Fields{
		"total_transactions": &graphql.Field{Type: graphql.Int},
		"unique_tokens_used": &graphql.Field{Type: graphql.Int},
		"unique_destinations": &graphql.Field{Type: graphql.Int},
		"tx_per_block":       &graphql.Field{Type: graphql.Float},
		"total_volume":       &graphql.Field{Type: graphql.String},
		"first_seen_block":   &graphql.Field{Type: graphql.Int},
		"last_seen_block":    &graphql.Field{Type: graphql.Int},
		"block_span":         &graphql.Field{Type: graphql.Int},
		"behavior_class":     &graphql.Field{Type: graphql.String},
		"dex_interactions":   &graphql.Field{Type: graphql.Int},
		"exchange_interactions": &graphql.Field{Type: graphql.Int},
		"mixer_contacts":     &graphql.Field{Type: graphql.Int},
		"sanctions_contacts": &graphql.Field{Type: graphql.Int},
	},
})

var clusterMemberType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ClusterMember",
	Fields: graphql.Fields{
		"address":    &graphql.Field{Type: graphql.String},
		"label":      &graphql.Field{Type: graphql.String},
		"risk_score": &graphql.Field{Type: graphql.Int},
		"risk_level": &graphql.Field{Type: graphql.String},
	},
})

var clusterType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Cluster",
	Fields: graphql.Fields{
		"cluster_id": &graphql.Field{Type: graphql.String},
		"size":       &graphql.Field{Type: graphql.Int},
		"confidence": &graphql.Field{Type: graphql.Int},
		"members":    &graphql.Field{Type: graphql.NewList(clusterMemberType)},
	},
})

var addressType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Address",
	Fields: graphql.Fields{
		"address":      &graphql.Field{Type: graphql.String},
		"label":        &graphql.Field{Type: graphql.String},
		"category":     &graphql.Field{Type: graphql.String},
		"sub_category": &graphql.Field{Type: graphql.String},
		"confidence":   &graphql.Field{Type: graphql.Int},
		"source":       &graphql.Field{Type: graphql.String},
		"risk":         &graphql.Field{Type: riskType},
		"behavior":     &graphql.Field{Type: behaviorType},
		"cluster":      &graphql.Field{Type: clusterType},
	},
})

var statsType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Stats",
	Fields: graphql.Fields{
		"total_addresses":  &graphql.Field{Type: graphql.Int},
		"total_labelled":   &graphql.Field{Type: graphql.Int},
		"coverage_percent": &graphql.Field{Type: graphql.Float},
		"critical_count":   &graphql.Field{Type: graphql.Int},
		"high_count":       &graphql.Field{Type: graphql.Int},
		"medium_count":     &graphql.Field{Type: graphql.Int},
		"total_transfers":  &graphql.Field{Type: graphql.Int},
		"total_blocks":     &graphql.Field{Type: graphql.Int},
		"total_clusters":   &graphql.Field{Type: graphql.Int},
	},
})

var criticalAddressType = graphql.NewObject(graphql.ObjectConfig{
	Name: "CriticalAddress",
	Fields: graphql.Fields{
		"address":     &graphql.Field{Type: graphql.String},
		"risk_score":  &graphql.Field{Type: graphql.Int},
		"risk_level":  &graphql.Field{Type: graphql.String},
		"label":       &graphql.Field{Type: graphql.String},
		"category":    &graphql.Field{Type: graphql.String},
		"mixer_hit":   &graphql.Field{Type: graphql.Boolean},
		"sanctions_hit": &graphql.Field{Type: graphql.Boolean},
	},
})

// ── GraphQL Schema ────────────────────────────────────────────
var schema graphql.Schema

func buildSchema() {
	rootQuery := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{

			// ── address(hash: "0x...") ─────────────────────────
			"address": &graphql.Field{
				Type:        addressType,
				Description: "Get full intelligence profile for an address",
				Args: graphql.FieldConfigArgument{
					"hash": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					addr := strings.ToLower(p.Args["hash"].(string))
					return resolveAddress(addr)
				},
			},

			// ── risk(address: "0x...") ─────────────────────────
			"risk": &graphql.Field{
				Type:        riskType,
				Description: "Get risk score for an address",
				Args: graphql.FieldConfigArgument{
					"address": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					addr := strings.ToLower(p.Args["address"].(string))
					return resolveRisk(addr)
				},
			},

			// ── cluster(address: "0x...") ──────────────────────
			"cluster": &graphql.Field{
				Type:        clusterType,
				Description: "Get cluster intelligence for an address",
				Args: graphql.FieldConfigArgument{
					"address": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					addr := strings.ToLower(p.Args["address"].(string))
					return resolveCluster(addr)
				},
			},

			// ── stats ──────────────────────────────────────────
			"stats": &graphql.Field{
				Type:        statsType,
				Description: "Get platform statistics",
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					return resolveStats()
				},
			},

			// ── criticalAddresses(limit: 10) ───────────────────
			"criticalAddresses": &graphql.Field{
				Type:        graphql.NewList(criticalAddressType),
				Description: "Get top critical risk addresses",
				Args: graphql.FieldConfigArgument{
					"limit": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 10,
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					limit := p.Args["limit"].(int)
					return resolveCriticalAddresses(limit)
				},
			},

			// ── searchByLabel(label: "Tornado") ───────────────
			"searchByLabel": &graphql.Field{
				Type:        graphql.NewList(criticalAddressType),
				Description: "Search addresses by label keyword",
				Args: graphql.FieldConfigArgument{
					"label": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
					"limit": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 20,
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					label := p.Args["label"].(string)
					limit := p.Args["limit"].(int)
					return resolveSearchByLabel(label, limit)
				},
			},

			// ── addressesByCategory(category: "high_risk") ────
			"addressesByCategory": &graphql.Field{
				Type:        graphql.NewList(criticalAddressType),
				Description: "Get addresses by risk category",
				Args: graphql.FieldConfigArgument{
					"category": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
					"limit": &graphql.ArgumentConfig{
						Type:         graphql.Int,
						DefaultValue: 20,
					},
				},
				Resolve: func(p graphql.ResolveParams) (interface{}, error) {
					category := p.Args["category"].(string)
					limit := p.Args["limit"].(int)
					return resolveByCategory(category, limit)
				},
			},
		},
	})

	var err error
	schema, err = graphql.NewSchema(graphql.SchemaConfig{
		Query: rootQuery,
	})
	if err != nil {
		log.Fatal("GraphQL schema error:", err)
	}
}

// ── Resolvers ─────────────────────────────────────────────────
func resolveRisk(address string) (map[string]interface{}, error) {
	var score int
	var level string
	var sanctionsHit, mixerHit bool
	var computedAt time.Time
	var factors []string

	err := db.QueryRow(`
		SELECT risk_score, risk_level, sanctions_hit, mixer_hit, computed_at
		FROM risk_scores WHERE address = $1`, address).
		Scan(&score, &level, &sanctionsHit, &mixerHit, &computedAt)
	if err != nil {
		return nil, nil
	}

	rows, _ := db.Query(`SELECT unnest(risk_factors) FROM risk_scores WHERE address = $1`, address)
	defer rows.Close()
	for rows.Next() {
		var f string
		rows.Scan(&f)
		factors = append(factors, f)
	}

	rec := "ALLOW"
	if score >= 80 { rec = "BLOCK" } else if score >= 60 { rec = "REVIEW" } else if score >= 30 { rec = "MONITOR" }

	return map[string]interface{}{
		"risk_score":     score,
		"risk_level":     level,
		"risk_factors":   factors,
		"sanctions_hit":  sanctionsHit,
		"mixer_hit":      mixerHit,
		"recommendation": rec,
		"computed_at":    computedAt.Format(time.RFC3339),
	}, nil
}

func resolveAddress(address string) (map[string]interface{}, error) {
	var label, category, subCategory, source string
	var confidence int
	db.QueryRow(`SELECT COALESCE(label,'Unknown'), COALESCE(category,'unknown'),
		COALESCE(sub_category,''), COALESCE(confidence,0), COALESCE(source,'')
		FROM address_labels WHERE address = $1 LIMIT 1`, address).
		Scan(&label, &category, &subCategory, &confidence, &source)

	risk, _ := resolveRisk(address)
	behavior, _ := resolveBehavior(address)
	cluster, _ := resolveCluster(address)

	return map[string]interface{}{
		"address":      address,
		"label":        label,
		"category":     category,
		"sub_category": subCategory,
		"confidence":   confidence,
		"source":       source,
		"risk":         risk,
		"behavior":     behavior,
		"cluster":      cluster,
	}, nil
}

func resolveBehavior(address string) (map[string]interface{}, error) {
	var totalTx, uniqueTokens, uniqueDest, blockSpan int
	var firstBlock, lastBlock int
	var txPerBlock float64
	var totalVolume string
	var behaviorClass string
	var dexInt, exchInt, mixerCont, sanctCont int

	err := db.QueryRow(`
		SELECT COALESCE(total_transactions,0),
			COALESCE(unique_tokens_used,0),
			COALESCE(unique_destinations,0),
			COALESCE(tx_per_block,0),
			COALESCE(total_volume::text,'0'),
			COALESCE(first_seen_block,0),
			COALESCE(last_seen_block,0),
			COALESCE(block_span,0),
			COALESCE(behavior_class,'unknown'),
			COALESCE(dex_interactions,0),
			COALESCE(exchange_interactions,0),
			COALESCE(mixer_contacts,0),
			COALESCE(sanctions_contacts,0)
		FROM address_behaviors WHERE address = $1`, address).
		Scan(&totalTx, &uniqueTokens, &uniqueDest, &txPerBlock,
			&totalVolume, &firstBlock, &lastBlock, &blockSpan,
			&behaviorClass, &dexInt, &exchInt, &mixerCont, &sanctCont)
	if err != nil {
		return nil, nil
	}

	return map[string]interface{}{
		"total_transactions":    totalTx,
		"unique_tokens_used":    uniqueTokens,
		"unique_destinations":   uniqueDest,
		"tx_per_block":          txPerBlock,
		"total_volume":          totalVolume,
		"first_seen_block":      firstBlock,
		"last_seen_block":       lastBlock,
		"block_span":            blockSpan,
		"behavior_class":        behaviorClass,
		"dex_interactions":      dexInt,
		"exchange_interactions": exchInt,
		"mixer_contacts":        mixerCont,
		"sanctions_contacts":    sanctCont,
	}, nil
}

func resolveCluster(address string) (map[string]interface{}, error) {
	var clusterID string
	var size, confidence int
	err := db.QueryRow(`SELECT c.canonical_address, c.size, c.confidence
		FROM cluster_memberships cm
		JOIN clusters c ON cm.cluster_id = c.id
		WHERE cm.address = $1`, address).
		Scan(&clusterID, &size, &confidence)
	if err != nil {
		return nil, nil
	}

	rows, _ := db.Query(`SELECT cm.address,
		COALESCE(al.label,'Unknown'),
		COALESCE(rs.risk_score,0),
		COALESCE(rs.risk_level,'MINIMAL')
		FROM cluster_memberships cm
		JOIN clusters c ON cm.cluster_id = c.id
		LEFT JOIN address_labels al ON cm.address = al.address
		LEFT JOIN risk_scores rs ON cm.address = rs.address
		WHERE c.canonical_address = $1
		ORDER BY rs.risk_score DESC NULLS LAST
		LIMIT 10`, clusterID)
	defer rows.Close()

	members := []map[string]interface{}{}
	for rows.Next() {
		var addr, label, level string
		var score int
		rows.Scan(&addr, &label, &score, &level)
		members = append(members, map[string]interface{}{
			"address":    addr,
			"label":      label,
			"risk_score": score,
			"risk_level": level,
		})
	}

	return map[string]interface{}{
		"cluster_id": clusterID,
		"size":       size,
		"confidence": confidence,
		"members":    members,
	}, nil
}

func resolveStats() (map[string]interface{}, error) {
	var totalTransfers, totalBlocks, totalLabelled, totalClusters int
	var totalAddresses, criticalCount, highCount, mediumCount int
	db.QueryRow("SELECT COUNT(*) FROM token_transfers").Scan(&totalTransfers)
	db.QueryRow("SELECT COUNT(*) FROM blocks").Scan(&totalBlocks)
	db.QueryRow("SELECT COUNT(*) FROM address_labels").Scan(&totalLabelled)
	db.QueryRow("SELECT COUNT(*) FROM clusters").Scan(&totalClusters)
	db.QueryRow("SELECT COUNT(DISTINCT from_address||to_address) FROM token_transfers").Scan(&totalAddresses)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='CRITICAL'").Scan(&criticalCount)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='HIGH'").Scan(&highCount)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='MEDIUM'").Scan(&mediumCount)

	coveragePct := 0.0
	if totalAddresses > 0 {
		coveragePct = float64(totalLabelled) / float64(totalAddresses) * 100
	}

	return map[string]interface{}{
		"total_addresses":  totalAddresses,
		"total_labelled":   totalLabelled,
		"coverage_percent": coveragePct,
		"critical_count":   criticalCount,
		"high_count":       highCount,
		"medium_count":     mediumCount,
		"total_transfers":  totalTransfers,
		"total_blocks":     totalBlocks,
		"total_clusters":   totalClusters,
	}, nil
}

func resolveCriticalAddresses(limit int) ([]map[string]interface{}, error) {
	rows, err := db.Query(`
		SELECT rs.address, rs.risk_score, rs.risk_level,
			rs.mixer_hit, rs.sanctions_hit,
			COALESCE(al.label,'Unknown'),
			COALESCE(al.category,'unknown')
		FROM risk_scores rs
		LEFT JOIN address_labels al ON rs.address = al.address
		WHERE rs.risk_score >= 60
		ORDER BY rs.risk_score DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []map[string]interface{}{}
	for rows.Next() {
		var addr, level, label, category string
		var score int
		var mixerHit, sanctionsHit bool
		rows.Scan(&addr, &score, &level, &mixerHit, &sanctionsHit, &label, &category)
		result = append(result, map[string]interface{}{
			"address":       addr,
			"risk_score":    score,
			"risk_level":    level,
			"label":         label,
			"category":      category,
			"mixer_hit":     mixerHit,
			"sanctions_hit": sanctionsHit,
		})
	}
	return result, nil
}

func resolveSearchByLabel(label string, limit int) ([]map[string]interface{}, error) {
	rows, err := db.Query(`
		SELECT al.address, COALESCE(rs.risk_score,0),
			COALESCE(rs.risk_level,'MINIMAL'),
			al.label, al.category,
			COALESCE(rs.mixer_hit,false),
			COALESCE(rs.sanctions_hit,false)
		FROM address_labels al
		LEFT JOIN risk_scores rs ON al.address = rs.address
		WHERE al.label ILIKE $1
		ORDER BY rs.risk_score DESC NULLS LAST
		LIMIT $2`, "%"+label+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []map[string]interface{}{}
	for rows.Next() {
		var addr, level, lbl, category string
		var score int
		var mixerHit, sanctionsHit bool
		rows.Scan(&addr, &score, &level, &lbl, &category, &mixerHit, &sanctionsHit)
		result = append(result, map[string]interface{}{
			"address":       addr,
			"risk_score":    score,
			"risk_level":    level,
			"label":         lbl,
			"category":      category,
			"mixer_hit":     mixerHit,
			"sanctions_hit": sanctionsHit,
		})
	}
	return result, nil
}

func resolveByCategory(category string, limit int) ([]map[string]interface{}, error) {
	rows, err := db.Query(`
		SELECT al.address, COALESCE(rs.risk_score,0),
			COALESCE(rs.risk_level,'MINIMAL'),
			al.label, al.category,
			COALESCE(rs.mixer_hit,false),
			COALESCE(rs.sanctions_hit,false)
		FROM address_labels al
		LEFT JOIN risk_scores rs ON al.address = rs.address
		WHERE al.category = $1
		ORDER BY rs.risk_score DESC NULLS LAST
		LIMIT $2`, category, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := []map[string]interface{}{}
	for rows.Next() {
		var addr, level, label, cat string
		var score int
		var mixerHit, sanctionsHit bool
		rows.Scan(&addr, &score, &level, &label, &cat, &mixerHit, &sanctionsHit)
		result = append(result, map[string]interface{}{
			"address":       addr,
			"risk_score":    score,
			"risk_level":    level,
			"label":         label,
			"category":      cat,
			"mixer_hit":     mixerHit,
			"sanctions_hit": sanctionsHit,
		})
	}
	return result, nil
}

// ── Main ──────────────────────────────────────────────────────
func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" { log.Fatal("DATABASE_URL not set") }

	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil { log.Fatal("DB connect error:", err) }
	defer db.Close()
	db.SetMaxOpenConns(25)

	buildSchema()

	// GraphQL handler with playground
	h := handler.New(&handler.Config{
		Schema:     &schema,
		Pretty:     true,
		GraphiQL:   false,
		Playground: true,
	})

	// Auth middleware wrapper
	authHandler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Allow all requests in development mode
			next.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()

	// GraphQL endpoint
	mux.Handle("/graphql", authHandler(h))

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "healthy",
			"version": "4.0.0",
			"graphql": "/graphql",
			"playground": "Open /graphql in browser",
		})
	})

	// Example queries endpoint
	mux.HandleFunc("/examples", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		examples := map[string]string{
			"full_address": `{
  address(hash: "0xf55e5d50a4f6755fdca30a4ae1ac12f92d8e1907") {
    address label category
    risk { risk_score risk_level risk_factors recommendation }
    behavior { total_transactions tx_per_block behavior_class }
    cluster { size members { address risk_score } }
  }
}`,
			"quick_risk": `{
  risk(address: "0xf55e5d50a4f6755fdca30a4ae1ac12f92d8e1907") {
    risk_score risk_level recommendation sanctions_hit mixer_hit
  }
}`,
			"platform_stats": `{
  stats {
    total_addresses total_labelled coverage_percent
    critical_count high_count total_transfers
  }
}`,
			"top_critical": `{
  criticalAddresses(limit: 5) {
    address risk_score risk_level label mixer_hit sanctions_hit
  }
}`,
			"search_tornado": `{
  searchByLabel(label: "Tornado", limit: 10) {
    address risk_score risk_level label
  }
}`,
			"high_risk_category": `{
  addressesByCategory(category: "high_risk", limit: 10) {
    address risk_score label mixer_hit
  }
}`,
		}
		json.NewEncoder(w).Encode(examples)
	})

	port := "8081"
	fmt.Println()
	fmt.Println("  CryptoIntelligence — Phase 10 GraphQL API")
	fmt.Println("  ==========================================")
	fmt.Printf("  GraphQL Playground: http://localhost:%s/graphql\n", port)
	fmt.Printf("  Health:             http://localhost:%s/health\n", port)
	fmt.Printf("  Example queries:    http://localhost:%s/examples\n", port)
	fmt.Println()
	fmt.Println("  Available queries:")
	fmt.Println("    address(hash)              Full address profile")
	fmt.Println("    risk(address)              Quick risk score")
	fmt.Println("    cluster(address)           Cluster intelligence")
	fmt.Println("    stats                      Platform statistics")
	fmt.Println("    criticalAddresses(limit)   Top critical addresses")
	fmt.Println("    searchByLabel(label,limit) Search by label")
	fmt.Println("    addressesByCategory(cat)   Filter by category")
	fmt.Println()

	log.Fatal(http.ListenAndServe(":"+port, mux))
}
