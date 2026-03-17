package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	_ "github.com/lib/pq"
)

// ============================================================
// CryptoIntelligence — Phase 6 REST API Server
// ============================================================

type RiskResponse struct {
	Address        string    `json:"address"`
	RiskScore      int       `json:"risk_score"`
	RiskLevel      string    `json:"risk_level"`
	RiskFactors    []string  `json:"risk_factors"`
	SanctionsHit   bool      `json:"sanctions_hit"`
	MixerHit       bool      `json:"mixer_hit"`
	Label          string    `json:"label"`
	Category       string    `json:"category"`
	Recommendation string    `json:"recommendation"`
	ComputedAt     time.Time `json:"computed_at"`
}

type AddressResponse struct {
	Address     string            `json:"address"`
	Risk        RiskResponse      `json:"risk"`
	Label       string            `json:"label"`
	Category    string            `json:"category"`
	SubCategory string            `json:"sub_category"`
	Confidence  int               `json:"confidence"`
	Behavior    map[string]interface{} `json:"behavior"`
	Cluster     *ClusterInfo      `json:"cluster,omitempty"`
}

type ClusterInfo struct {
	ClusterID string `json:"cluster_id"`
	Size      int    `json:"size"`
	Confidence int   `json:"confidence"`
}

type BatchRequest struct {
	Addresses []string `json:"addresses" binding:"required"`
}

type StatsResponse struct {
	TotalAddresses    int       `json:"total_addresses"`
	TotalLabelled     int       `json:"total_labelled"`
	CoveragePercent   float64   `json:"coverage_percent"`
	CriticalCount     int       `json:"critical_count"`
	HighCount         int       `json:"high_count"`
	MediumCount       int       `json:"medium_count"`
	TotalTransfers    int       `json:"total_transfers"`
	TotalBlocks       int       `json:"total_blocks"`
	TotalClusters     int       `json:"total_clusters"`
	LastUpdated       time.Time `json:"last_updated"`
}

var db *sql.DB
var jwtSecret = []byte("cryptointelligence-secret-2026")

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}

	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("DB connect error:", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	r := gin.Default()

	// CORS
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-API-Key"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// ── Public routes ─────────────────────────────────────────
	r.GET("/health", healthHandler)
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"name":    "CryptoIntelligence API",
			"version": "1.0.0",
			"status":  "running",
			"docs":    "/health",
		})
	})

	// ── Generate test token ───────────────────────────────────
	r.POST("/auth/token", generateTokenHandler)

	// ── Protected routes ──────────────────────────────────────
	v1 := r.Group("/v1")
	v1.Use(authMiddleware())
	{
		v1.GET("/risk/:address",    riskHandler)
		v1.GET("/address/:address", addressHandler)
		v1.POST("/screen",          screenHandler)
		v1.POST("/batch",           batchHandler)
		v1.GET("/cluster/:address", clusterHandler)
		v1.GET("/stats",            statsHandler)
	}

	port := os.Getenv("API_PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Println()
	fmt.Println("  CryptoIntelligence API Server")
	fmt.Println("  ================================")
	fmt.Printf("  Running on http://localhost:%s\n", port)
	fmt.Println("  Endpoints:")
	fmt.Println("    GET  /health")
	fmt.Println("    POST /auth/token")
	fmt.Println("    GET  /v1/risk/:address")
	fmt.Println("    GET  /v1/address/:address")
	fmt.Println("    POST /v1/screen")
	fmt.Println("    POST /v1/batch")
	fmt.Println("    GET  /v1/cluster/:address")
	fmt.Println("    GET  /v1/stats")
	fmt.Println()

	r.Run(":" + port)
}

// ── Auth Middleware ───────────────────────────────────────────
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		authHeader := c.GetHeader("Authorization")

		var tokenString string
		if apiKey != "" {
			tokenString = apiKey
		} else if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "API key required. Pass X-API-Key header or Bearer token.",
			})
			c.Abort()
			return
		}

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// ── Generate Token ────────────────────────────────────────────
func generateTokenHandler(c *gin.Context) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "api_user",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(365 * 24 * time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString(jwtSecret)
	c.JSON(200, gin.H{
		"token":   tokenString,
		"expires": "1 year",
		"usage":   "Add header: X-API-Key: " + tokenString,
	})
}

// ── Health Handler ────────────────────────────────────────────
func healthHandler(c *gin.Context) {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM risk_scores").Scan(&count)
	c.JSON(200, gin.H{
		"status":        "healthy",
		"scored_addresses": count,
		"timestamp":     time.Now(),
	})
}

// ── Risk Handler ──────────────────────────────────────────────
func riskHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))
	resp, err := getRiskScore(address)
	if err != nil {
		c.JSON(404, gin.H{"error": "Address not found", "address": address})
		return
	}
	c.JSON(200, resp)
}

// ── Screen Handler ────────────────────────────────────────────
func screenHandler(c *gin.Context) {
	var req struct {
		Address string `json:"address" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "address field required"})
		return
	}
	address := strings.ToLower(req.Address)
	resp, err := getRiskScore(address)
	if err != nil {
		c.JSON(200, gin.H{
			"address":        address,
			"risk_score":     0,
			"risk_level":     "UNKNOWN",
			"risk_factors":   []string{},
			"recommendation": "NOT_FOUND",
			"message":        "Address not in database",
		})
		return
	}
	c.JSON(200, resp)
}

// ── Address Handler ───────────────────────────────────────────
func addressHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	risk, _ := getRiskScore(address)

	var label, category, subCategory string
	var confidence int
	db.QueryRow(`
		SELECT COALESCE(label,'Unknown'), COALESCE(category,'unknown'),
			   COALESCE(sub_category,''), COALESCE(confidence,0)
		FROM address_labels WHERE address = $1 LIMIT 1`, address).
		Scan(&label, &category, &subCategory, &confidence)

	var totalTxs, uniqueTokens int
	var txPerBlock float64
	db.QueryRow(`
		SELECT COALESCE(total_transactions,0),
			   COALESCE(unique_tokens_used,0),
			   COALESCE(tx_per_block,0)
		FROM address_behaviors WHERE address = $1`, address).
		Scan(&totalTxs, &uniqueTokens, &txPerBlock)

	behavior := map[string]interface{}{
		"total_transactions": totalTxs,
		"unique_tokens_used": uniqueTokens,
		"tx_per_block":       txPerBlock,
	}

	var clusterID string
	var clusterSize, clusterConf int
	var clusterInfo *ClusterInfo
	err := db.QueryRow(`
		SELECT c.canonical_address, c.size, c.confidence
		FROM cluster_memberships cm
		JOIN clusters c ON cm.cluster_id = c.id
		WHERE cm.address = $1`, address).
		Scan(&clusterID, &clusterSize, &clusterConf)
	if err == nil {
		clusterInfo = &ClusterInfo{
			ClusterID:  clusterID,
			Size:       clusterSize,
			Confidence: clusterConf,
		}
	}

	c.JSON(200, AddressResponse{
		Address:     address,
		Risk:        risk,
		Label:       label,
		Category:    category,
		SubCategory: subCategory,
		Confidence:  confidence,
		Behavior:    behavior,
		Cluster:     clusterInfo,
	})
}

// ── Batch Handler ─────────────────────────────────────────────
func batchHandler(c *gin.Context) {
	var req BatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "addresses array required"})
		return
	}
	if len(req.Addresses) > 100 {
		c.JSON(400, gin.H{"error": "Maximum 100 addresses per batch"})
		return
	}

	results := make([]RiskResponse, 0, len(req.Addresses))
	for _, addr := range req.Addresses {
		risk, err := getRiskScore(strings.ToLower(addr))
		if err != nil {
			risk = RiskResponse{
				Address:        strings.ToLower(addr),
				RiskScore:      0,
				RiskLevel:      "UNKNOWN",
				RiskFactors:    []string{},
				Recommendation: "NOT_FOUND",
			}
		}
		results = append(results, risk)
	}

	critical := 0
	for _, r := range results {
		if r.RiskLevel == "CRITICAL" {
			critical++
		}
	}

	c.JSON(200, gin.H{
		"total":          len(results),
		"critical_count": critical,
		"results":        results,
	})
}

// ── Cluster Handler ───────────────────────────────────────────
func clusterHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	var clusterID string
	var size, confidence int
	err := db.QueryRow(`
		SELECT c.canonical_address, c.size, c.confidence
		FROM cluster_memberships cm
		JOIN clusters c ON cm.cluster_id = c.id
		WHERE cm.address = $1`, address).
		Scan(&clusterID, &size, &confidence)

	if err != nil {
		c.JSON(404, gin.H{"error": "Address not in any cluster"})
		return
	}

	rows, _ := db.Query(`
		SELECT cm.address, COALESCE(al.label,'Unknown'),
			   COALESCE(rs.risk_score,0), COALESCE(rs.risk_level,'MINIMAL')
		FROM cluster_memberships cm
		JOIN clusters c ON cm.cluster_id = c.id
		LEFT JOIN address_labels al ON cm.address = al.address
		LEFT JOIN risk_scores rs ON cm.address = rs.address
		WHERE c.canonical_address = $1
		ORDER BY rs.risk_score DESC NULLS LAST
		LIMIT 20`, clusterID)
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

	c.JSON(200, gin.H{
		"cluster_id": clusterID,
		"size":       size,
		"confidence": confidence,
		"members":    members,
	})
}

// ── Stats Handler ─────────────────────────────────────────────
func statsHandler(c *gin.Context) {
	var stats StatsResponse
	db.QueryRow("SELECT COUNT(*) FROM token_transfers").Scan(&stats.TotalTransfers)
	db.QueryRow("SELECT COUNT(*) FROM blocks").Scan(&stats.TotalBlocks)
	db.QueryRow("SELECT COUNT(*) FROM address_labels").Scan(&stats.TotalLabelled)
	db.QueryRow("SELECT COUNT(*) FROM clusters").Scan(&stats.TotalClusters)
	db.QueryRow("SELECT COUNT(DISTINCT from_address||to_address) FROM token_transfers").
		Scan(&stats.TotalAddresses)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='CRITICAL'").
		Scan(&stats.CriticalCount)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='HIGH'").
		Scan(&stats.HighCount)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='MEDIUM'").
		Scan(&stats.MediumCount)

	if stats.TotalAddresses > 0 {
		stats.CoveragePercent = float64(stats.TotalLabelled) / float64(stats.TotalAddresses) * 100
	}
	stats.LastUpdated = time.Now()

	c.JSON(200, stats)
}

// ── Helper: Get Risk Score ────────────────────────────────────
func getRiskScore(address string) (RiskResponse, error) {
	var resp RiskResponse
	var factors []string
	var computedAt time.Time

	row := db.QueryRow(`
		SELECT rs.address, rs.risk_score, rs.risk_level,
			   rs.sanctions_hit, rs.mixer_hit, rs.computed_at,
			   COALESCE(al.label, 'Unknown') as label,
			   COALESCE(al.category, 'unknown') as category
		FROM risk_scores rs
		LEFT JOIN address_labels al ON rs.address = al.address
		WHERE rs.address = $1
		LIMIT 1`, address)

	err := row.Scan(&resp.Address, &resp.RiskScore, &resp.RiskLevel,
		&resp.SanctionsHit, &resp.MixerHit, &computedAt,
		&resp.Label, &resp.Category)
	if err != nil {
		return resp, err
	}

	rows, _ := db.Query(`
		SELECT unnest(risk_factors) FROM risk_scores WHERE address = $1`, address)
	defer rows.Close()
	for rows.Next() {
		var f string
		rows.Scan(&f)
		factors = append(factors, f)
	}
	resp.RiskFactors = factors
	resp.ComputedAt = computedAt

	switch {
	case resp.RiskScore >= 80:
		resp.Recommendation = "BLOCK"
	case resp.RiskScore >= 60:
		resp.Recommendation = "REVIEW"
	case resp.RiskScore >= 30:
		resp.Recommendation = "MONITOR"
	default:
		resp.Recommendation = "ALLOW"
	}

	return resp, nil
}
