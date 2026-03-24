package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

type JarvisRequest struct {
	Query   string `json:"query"`
	Address string `json:"address,omitempty"`
}

type JarvisResponse struct {
	Query       string      `json:"query"`
	Answer      string      `json:"answer"`
	Evidence    interface{} `json:"evidence,omitempty"`
	Confidence  int         `json:"confidence"`
	Action      string      `json:"action"`
	ProcessedAt time.Time   `json:"processed_at"`
}

type AddressIntel struct {
	Address       string   `json:"address"`
	Label         string   `json:"label"`
	Category      string   `json:"category"`
	RiskScore     int      `json:"risk_score"`
	RiskLevel     string   `json:"risk_level"`
	BehaviorClass string   `json:"behavior_class"`
	TxCount       int      `json:"tx_count"`
	Identities    []string `json:"identities"`
	BridgesUsed   []string `json:"bridges_used"`
	MoneyPaths    []string `json:"money_paths"`
}

var jarvisDB *sql.DB

func getIntel(address string) AddressIntel {
	intel := AddressIntel{Address: address}
	address = strings.ToLower(strings.TrimSpace(address))

	jarvisDB.QueryRow(`
		SELECT COALESCE(al.label,'Unknown'),
			COALESCE(al.category,'unknown'),
			COALESCE(rs.risk_score,0),
			COALESCE(rs.risk_level,'UNKNOWN')
		FROM address_labels al
		LEFT JOIN risk_scores rs ON al.address = rs.address
		WHERE al.address = $1 AND al.is_primary = TRUE
		LIMIT 1`, address).Scan(
		&intel.Label, &intel.Category,
		&intel.RiskScore, &intel.RiskLevel)

	jarvisDB.QueryRow(`
		SELECT COALESCE(behavior_class,'unknown'),
			COALESCE(total_transactions,0)
		FROM address_behaviors WHERE address = $1`,
		address).Scan(&intel.BehaviorClass, &intel.TxCount)

	rows, _ := jarvisDB.Query(`
		SELECT identity || ' (' || platform || ')'
		FROM identity_links WHERE address = $1
		ORDER BY confidence DESC LIMIT 5`, address)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			rows.Scan(&id)
			intel.Identities = append(intel.Identities, id)
		}
	}

	brows, _ := jarvisDB.Query(`
		SELECT DISTINCT el.label
		FROM token_transfers tt
		JOIN entity_labels el ON tt.to_address = el.address
		WHERE el.category = 'bridge'
		AND tt.from_address = $1 LIMIT 5`, address)
	if brows != nil {
		defer brows.Close()
		for brows.Next() {
			var b string
			brows.Scan(&b)
			intel.BridgesUsed = append(intel.BridgesUsed, b)
		}
	}

	prows, _ := jarvisDB.Query(`
		SELECT end_entity || ' (' || hops::text || ' hops)'
		FROM money_paths WHERE origin_address = $1
		LIMIT 3`, address)
	if prows != nil {
		defer prows.Close()
		for prows.Next() {
			var p string
			prows.Scan(&p)
			intel.MoneyPaths = append(intel.MoneyPaths, p)
		}
	}

	return intel
}

func askClaude(prompt string) (string, error) {
	apiKey := os.Getenv("GROQ_API_KEY")
	payload := map[string]interface{}{
		"model":      "llama-3.1-8b-instant",
		"max_tokens": 1000,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST",
		"https://api.groq.com/openai/v1/chat/completions",
		bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer " + apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	json.Unmarshal(respBody, &result)
	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return "", fmt.Errorf("API error: %s", string(respBody))
	}
	msg := choices[0].(map[string]interface{})["message"].(map[string]interface{})
	return msg["content"].(string), nil
}

func jarvisHandler(c *gin.Context) {
	var req JarvisRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Query == "" {
		c.JSON(400, gin.H{"error": "query required"})
		return
	}

	var totalAddresses, criticalCount int
	jarvisDB.QueryRow("SELECT COUNT(*) FROM address_labels").Scan(&totalAddresses)
	jarvisDB.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='CRITICAL'").Scan(&criticalCount)

	prompt := fmt.Sprintf(`You are JARVIS, AI investigator for CryptoIntelligence.
Platform monitors $48B in Ethereum transactions.
Database: %d labelled addresses, %d CRITICAL threats, 54K KYC links, 62 bridges tracked.

`, totalAddresses, criticalCount)

	if req.Address != "" {
		intel := getIntel(req.Address)
		intelJSON, _ := json.MarshalIndent(intel, "", "  ")
		prompt += fmt.Sprintf("INTELLIGENCE FOR %s:\n%s\n\n", req.Address, string(intelJSON))
	}

	prompt += fmt.Sprintf(`QUERY: %s

Respond with:
1. Direct answer
2. Key findings  
3. Recommended action (BLOCK/REVIEW/MONITOR/SAFE)
4. Confidence level`, req.Query)

	answer, err := askClaude(prompt)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	action := "REVIEW"
	upper := strings.ToUpper(answer)
	if strings.Contains(upper, "BLOCK") { action = "BLOCK" }
	if strings.Contains(upper, "SAFE") || strings.Contains(upper, "MINIMAL") { action = "SAFE" }
	if strings.Contains(upper, "MONITOR") && action == "REVIEW" { action = "MONITOR" }

	resp := JarvisResponse{
		Query:       req.Query,
		Answer:      answer,
		Confidence:  90,
		Action:      action,
		ProcessedAt: time.Now(),
	}
	if req.Address != "" {
		resp.Evidence = getIntel(req.Address)
	}
	c.JSON(200, resp)
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" { log.Fatal("DATABASE_URL not set") }

	var err error
	jarvisDB, err = sql.Open("postgres", dbURL)
	if err != nil { log.Fatal(err) }
	defer jarvisDB.Close()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())

	r.POST("/v1/jarvis", jarvisHandler)
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "JARVIS ONLINE", "model": "claude-sonnet-4-20250514"})
	})

	fmt.Println("\n  JARVIS — AI Investigator")
	fmt.Println("  POST /v1/jarvis  →  port 8082\n")
	r.Run(":8082")
}
