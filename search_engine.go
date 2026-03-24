package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

type SearchResult struct {
	Address   string `json:"address"`
	Label     string `json:"label"`
	Category  string `json:"category"`
	RiskScore int    `json:"risk_score"`
	RiskLevel string `json:"risk_level"`
	TxCount   int    `json:"tx_count"`
	Relevance int    `json:"relevance"`
}

type SearchResponse struct {
	Query       string         `json:"query"`
	Total       int            `json:"total"`
	Results     []SearchResult `json:"results"`
	SearchedAt  time.Time      `json:"searched_at"`
}

var searchDB *sql.DB

func search(query string, limit int) []SearchResult {
	results := []SearchResult{}
	q := strings.ToLower(strings.TrimSpace(query))

	// Search entity_labels (known entities — highest quality)
	rows, _ := searchDB.Query(`
		SELECT el.address,
			el.label,
			el.category,
			COALESCE(rs.risk_score, 0),
			COALESCE(rs.risk_level, 'UNKNOWN'),
			COALESCE(ab.total_transactions, 0),
			100 as relevance
		FROM entity_labels el
		LEFT JOIN risk_scores rs ON el.address = rs.address
		LEFT JOIN address_behaviors ab ON el.address = ab.address
		WHERE LOWER(el.label) LIKE $1
		OR LOWER(el.category) LIKE $1
		OR LOWER(COALESCE(el.bridge_type,'')) LIKE $1
		ORDER BY rs.risk_score DESC NULLS LAST
		LIMIT $2`, "%"+q+"%", limit)

	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var r SearchResult
			rows.Scan(&r.Address, &r.Label, &r.Category,
				&r.RiskScore, &r.RiskLevel, &r.TxCount, &r.Relevance)
			results = append(results, r)
		}
	}

	// Search address_labels (all labelled addresses)
	if len(results) < limit {
		rows2, _ := searchDB.Query(`
			SELECT al.address,
				al.label,
				al.category,
				COALESCE(rs.risk_score, 0),
				COALESCE(rs.risk_level, 'UNKNOWN'),
				COALESCE(ab.total_transactions, 0),
				80 as relevance
			FROM address_labels al
			LEFT JOIN risk_scores rs ON al.address = rs.address
			LEFT JOIN address_behaviors ab ON al.address = ab.address
			WHERE (LOWER(al.label) LIKE $1
			OR LOWER(al.category) LIKE $1
			OR LOWER(COALESCE(al.sub_category,'')) LIKE $1)
			AND al.is_primary = TRUE
			ORDER BY rs.risk_score DESC NULLS LAST
			LIMIT $2`, "%"+q+"%", limit-len(results))

		if rows2 != nil {
			defer rows2.Close()
			for rows2.Next() {
				var r SearchResult
				rows2.Scan(&r.Address, &r.Label, &r.Category,
					&r.RiskScore, &r.RiskLevel, &r.TxCount, &r.Relevance)
				results = append(results, r)
			}
		}
	}

	return results
}

func searchHandler(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(400, gin.H{"error": "q parameter required. Example: /search?q=tornado"})
		return
	}

	// Direct address lookup
	if strings.HasPrefix(strings.ToLower(query), "0x") && len(query) == 42 {
		addr := strings.ToLower(query)
		var r SearchResult
		r.Address = addr
		searchDB.QueryRow(`
			SELECT COALESCE(al.label,'Unknown'),
				COALESCE(al.category,'unknown'),
				COALESCE(rs.risk_score,0),
				COALESCE(rs.risk_level,'UNKNOWN'),
				COALESCE(ab.total_transactions,0)
			FROM address_labels al
			LEFT JOIN risk_scores rs ON al.address = rs.address
			LEFT JOIN address_behaviors ab ON al.address = ab.address
			WHERE al.address = $1 AND al.is_primary = TRUE
			LIMIT 1`, addr).Scan(
			&r.Label, &r.Category, &r.RiskScore, &r.RiskLevel, &r.TxCount)
		r.Relevance = 100
		c.JSON(200, SearchResponse{
			Query: query, Total: 1,
			Results: []SearchResult{r}, SearchedAt: time.Now(),
		})
		return
	}

	results := search(query, 20)
	c.JSON(200, SearchResponse{
		Query:      query,
		Total:      len(results),
		Results:    results,
		SearchedAt: time.Now(),
	})
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" { log.Fatal("DATABASE_URL not set") }

	var err error
	searchDB, err = sql.Open("postgres", dbURL)
	if err != nil { log.Fatal(err) }
	defer searchDB.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.GET("/search", searchHandler)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "Search Engine ONLINE"})
	})

	fmt.Println("\n  Blockchain Search Engine")
	fmt.Println("  ─────────────────────────────────")
	fmt.Println("  GET /search?q=tornado")
	fmt.Println("  GET /search?q=binance")
	fmt.Println("  GET /search?q=mev+bot")
	fmt.Println("  GET /search?q=mixer")
	fmt.Println("  GET /search?q=0x48c0...  (direct address)")
	fmt.Println("  Running on :8083\n")
	r.Run(":8083")
}
