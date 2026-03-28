package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	_ "github.com/lib/pq"
)

// ============================================================
// CryptoIntelligence — Phase 8 API Server with Redis Cache
// ============================================================

var db  *sql.DB
var rdb *redis.Client
var ctx = context.Background()
var jwtSecret = []byte("cryptointelligence-secret-2026")

// Cache TTLs
const (
	ttlRisk    = 1 * time.Hour
	ttlStats   = 5 * time.Minute
	ttlCluster = 30 * time.Minute
	ttlAddress = 1 * time.Hour
)

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
	CachedAt       string    `json:"cached_at,omitempty"`
	ComputedAt     time.Time `json:"computed_at"`
}

type AddressResponse struct {
	Address     string                 `json:"address"`
	Risk        RiskResponse           `json:"risk"`
	Label       string                 `json:"label"`
	Category    string                 `json:"category"`
	SubCategory string                 `json:"sub_category"`
	Confidence  int                    `json:"confidence"`
	Behavior    map[string]interface{} `json:"behavior"`
	Cluster     *ClusterInfo           `json:"cluster,omitempty"`
}

type ClusterInfo struct {
	ClusterID  string `json:"cluster_id"`
	Size       int    `json:"size"`
	Confidence int    `json:"confidence"`
}

type BatchRequest struct {
	Addresses []string `json:"addresses" binding:"required"`
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	// Connect PostgreSQL
	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal("DB connect error:", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	// Connect Redis
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatal("Redis URL parse error:", err)
	}
	rdb = redis.NewClient(opt)
	_, err = rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatal("Redis connect error:", err)
	}

	// Gin router
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-API-Key"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// Public routes
	r.GET("/health", healthHandler)
	r.Static("/static", ".")
	r.StaticFile("/dashboard", "./CryptoIntelligence_Live_Dashboard.html")
	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"name":    "CryptoIntelligence API",
			"version": "2.0.0",
			"status":  "running",
			"cache":   "Redis enabled",
		})
	})
	r.POST("/auth/token", generateTokenHandler)

	// Cache management
	r.DELETE("/v1/cache/:address", authMiddleware(), clearCacheHandler)
	r.GET("/v1/cache/stats", authMiddleware(), cacheStatsHandler)

	// Protected routes
	v1 := r.Group("/v1")
	v1.Use(authMiddleware())
	{
		v1.GET("/risk/:address",    riskHandler)
		v1.GET("/address/:address", addressHandler)
		v1.POST("/screen",          screenHandler)
		v1.POST("/batch",           batchHandler)
		v1.GET("/cluster/:address", clusterHandler)
		v1.GET("/stats",            statsHandler)
	v1.GET("/inflows",          inflowsHandler)
		}

	port := "8080"
	fmt.Println()
	fmt.Println("  CryptoIntelligence API v2 — Redis Cache Enabled")
	fmt.Println("  =================================================")
	fmt.Printf("  Running on http://localhost:%s\n", port)
	fmt.Println("  Cache TTLs:")
	fmt.Printf("    Risk scores:  %s\n", ttlRisk)
	fmt.Printf("    Stats:        %s\n", ttlStats)
	fmt.Printf("    Clusters:     %s\n", ttlCluster)
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
			c.JSON(http.StatusUnauthorized, gin.H{"error": "API key required"})
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
	db.QueryRow("SELECT COUNT(*) FROM addresses_master").Scan(&count)

	// Check Redis
	redisOK := true
	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		redisOK = false
	}

	// Cache hit rate
	hits, _ := rdb.Get(ctx, "metrics:cache_hits").Int64()
	misses, _ := rdb.Get(ctx, "metrics:cache_misses").Int64()
	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	c.JSON(200, gin.H{
		"status":           "healthy",
		"version":          "2.0.0",
		"scored_addresses": count,
		"redis":            redisOK,
		"cache_hits":       hits,
		"cache_misses":     misses,
		"cache_hit_rate":   fmt.Sprintf("%.1f%%", hitRate),
		"timestamp":        time.Now(),
	})
}

// ── Cache Stats Handler ───────────────────────────────────────
func cacheStatsHandler(c *gin.Context) {
	hits, _ := rdb.Get(ctx, "metrics:cache_hits").Int64()
	misses, _ := rdb.Get(ctx, "metrics:cache_misses").Int64()
	keys, _ := rdb.DBSize(ctx).Result()

	total := hits + misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	c.JSON(200, gin.H{
		"cache_hits":     hits,
		"cache_misses":   misses,
		"hit_rate":       fmt.Sprintf("%.1f%%", hitRate),
		"total_keys":     keys,
		"ttl_risk":       ttlRisk.String(),
		"ttl_stats":      ttlStats.String(),
		"ttl_cluster":    ttlCluster.String(),
	})
}

// ── Clear Cache Handler ───────────────────────────────────────
func clearCacheHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))
	rdb.Del(ctx, "risk:"+address, "address:"+address, "cluster:"+address)
	c.JSON(200, gin.H{"cleared": address})
}

// ── Risk Handler (with cache) ─────────────────────────────────
func riskHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))
	cacheKey := "risk:" + address

	// Check Redis cache first
	cached, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		// Cache HIT — return immediately
		rdb.Incr(ctx, "metrics:cache_hits")
		var resp RiskResponse
		json.Unmarshal([]byte(cached), &resp)
		resp.CachedAt = "redis"
		c.Header("X-Cache", "HIT")
		c.JSON(200, resp)
		return
	}

	// Cache MISS — query PostgreSQL
	rdb.Incr(ctx, "metrics:cache_misses")
	resp, err := getRiskScore(address)
	if err != nil {
		c.JSON(404, gin.H{"error": "Address not found", "address": address})
		return
	}

	// Store in Redis
	data, _ := json.Marshal(resp)
	rdb.Set(ctx, cacheKey, data, ttlRisk)

	c.Header("X-Cache", "MISS")
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
	cacheKey := "risk:" + address

	cached, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		rdb.Incr(ctx, "metrics:cache_hits")
		var resp RiskResponse
		json.Unmarshal([]byte(cached), &resp)
		resp.CachedAt = "redis"
		c.Header("X-Cache", "HIT")
		c.JSON(200, resp)
		return
	}

	rdb.Incr(ctx, "metrics:cache_misses")
	resp, err := getRiskScore(address)
	if err != nil {
		c.JSON(200, gin.H{
			"address":        address,
			"risk_score":     0,
			"risk_level":     "UNKNOWN",
			"risk_factors":   []string{},
			"recommendation": "NOT_FOUND",
		})
		return
	}

	data, _ := json.Marshal(resp)
	rdb.Set(ctx, cacheKey, data, ttlRisk)
	c.Header("X-Cache", "MISS")
	c.JSON(200, resp)
}

// ── Address Handler (with cache) ──────────────────────────────
func addressHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))
	cacheKey := "address:" + address

	cached, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		rdb.Incr(ctx, "metrics:cache_hits")
		var resp AddressResponse
		json.Unmarshal([]byte(cached), &resp)
		c.Header("X-Cache", "HIT")
		c.JSON(200, resp)
		return
	}

	rdb.Incr(ctx, "metrics:cache_misses")
	risk, _ := getRiskScore(address)

	var label, category, subCategory string
	var confidence int
	db.QueryRow(`SELECT COALESCE(label,'Unknown'), COALESCE(category,'unknown'),
		COALESCE(sub_category,''), COALESCE(confidence,0)
		FROM address_labels WHERE address = $1 LIMIT 1`, address).
		Scan(&label, &category, &subCategory, &confidence)

	var totalTxs, uniqueTokens int
	var txPerBlock float64
	db.QueryRow(`SELECT COALESCE(total_transactions,0),
		COALESCE(unique_tokens_used,0), COALESCE(tx_per_block,0)
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
	err = db.QueryRow(`SELECT c.canonical_address, c.size, c.confidence
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

	resp := AddressResponse{
		Address:     address,
		Risk:        risk,
		Label:       label,
		Category:    category,
		SubCategory: subCategory,
		Confidence:  confidence,
		Behavior:    behavior,
		Cluster:     clusterInfo,
	}

	data, _ := json.Marshal(resp)
	rdb.Set(ctx, cacheKey, data, ttlAddress)
	c.Header("X-Cache", "MISS")
	c.JSON(200, resp)
}

// ── Batch Handler (parallel Redis lookup) ─────────────────────
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
	cacheHits := 0

	for _, addr := range req.Addresses {
		address := strings.ToLower(addr)
		cacheKey := "risk:" + address

		// Try cache first
		cached, err := rdb.Get(ctx, cacheKey).Result()
		if err == nil {
			var resp RiskResponse
			json.Unmarshal([]byte(cached), &resp)
			resp.CachedAt = "redis"
			results = append(results, resp)
			cacheHits++
			rdb.Incr(ctx, "metrics:cache_hits")
			continue
		}

		// Cache miss — query DB
		rdb.Incr(ctx, "metrics:cache_misses")
		risk, err := getRiskScore(address)
		if err != nil {
			risk = RiskResponse{
				Address:        address,
				RiskScore:      0,
				RiskLevel:      "UNKNOWN",
				RiskFactors:    []string{},
				Recommendation: "NOT_FOUND",
			}
		} else {
			data, _ := json.Marshal(risk)
			rdb.Set(ctx, cacheKey, data, ttlRisk)
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
		"cache_hits":     cacheHits,
		"cache_misses":   len(req.Addresses) - cacheHits,
		"results":        results,
	})
}

// ── Cluster Handler (with cache) ──────────────────────────────
func clusterHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))
	cacheKey := "cluster:" + address

	cached, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		rdb.Incr(ctx, "metrics:cache_hits")
		c.Header("X-Cache", "HIT")
		c.Data(200, "application/json", []byte(cached))
		return
	}

	rdb.Incr(ctx, "metrics:cache_misses")

	var clusterID string
	var size, confidence int
	err = db.QueryRow(`SELECT c.canonical_address, c.size, c.confidence
		FROM cluster_memberships cm
		JOIN clusters c ON cm.cluster_id = c.id
		WHERE cm.address = $1`, address).
		Scan(&clusterID, &size, &confidence)
	if err != nil {
		c.JSON(404, gin.H{"error": "Address not in any cluster"})
		return
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

	result := gin.H{
		"cluster_id": clusterID,
		"size":       size,
		"confidence": confidence,
		"members":    members,
	}

	data, _ := json.Marshal(result)
	rdb.Set(ctx, cacheKey, data, ttlCluster)
	c.Header("X-Cache", "MISS")
	c.JSON(200, result)
}

// ── Stats Handler (with cache) ────────────────────────────────
func statsHandler(c *gin.Context) {
	cacheKey := "stats:global"

	cached, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		rdb.Incr(ctx, "metrics:cache_hits")
		c.Header("X-Cache", "HIT")
		c.Data(200, "application/json", []byte(cached))
		return
	}

	rdb.Incr(ctx, "metrics:cache_misses")

	stats := map[string]interface{}{}
	var totalTransfers, totalBlocks, totalLabelled, totalClusters int
	var totalAddresses, criticalCount, highCount, mediumCount int
	db.QueryRow("SELECT COUNT(*) FROM token_transfers").Scan(&totalTransfers)
	db.QueryRow("SELECT COUNT(DISTINCT block_number) FROM token_transfers").Scan(&totalBlocks)
	db.QueryRow("SELECT COUNT(*) FROM address_labels").Scan(&totalLabelled)
	db.QueryRow("SELECT COUNT(*) FROM clusters").Scan(&totalClusters)
	db.QueryRow("SELECT COUNT(*) FROM address_labels WHERE is_primary = TRUE").Scan(&totalAddresses)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='CRITICAL'").Scan(&criticalCount)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='HIGH'").Scan(&highCount)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='MEDIUM'").Scan(&mediumCount)

	coveragePct := 0.0
	if totalAddresses > 0 {
		coveragePct = float64(totalLabelled) / float64(totalAddresses) * 100
	}

	stats["total_addresses"]   = totalAddresses
	stats["total_labelled"]    = totalLabelled
	stats["coverage_percent"]  = coveragePct
	stats["critical_count"]    = criticalCount
	stats["high_count"]        = highCount
	stats["medium_count"]      = mediumCount
	stats["total_transfers"]   = totalTransfers
	stats["total_blocks"]      = totalBlocks
	stats["total_clusters"]    = totalClusters
	stats["last_updated"]      = time.Now()

	data, _ := json.Marshal(stats)
	rdb.Set(ctx, cacheKey, data, ttlStats)
	c.Header("X-Cache", "MISS")
	c.JSON(200, stats)
}

// ── Helper: Get Risk Score ────────────────────────────────────
func getRiskScore(address string) (RiskResponse, error) {
	var resp RiskResponse
	var computedAt time.Time

	row := db.QueryRow(`SELECT rs.address, rs.risk_score, rs.risk_level,
		rs.sanctions_hit, rs.mixer_hit, rs.computed_at,
		COALESCE(al.label, 'Unknown') as label,
		COALESCE(al.category, 'unknown') as category
		FROM risk_scores rs
		LEFT JOIN address_labels al ON rs.address = al.address
		WHERE rs.address = $1 LIMIT 1`, address)

	err := row.Scan(&resp.Address, &resp.RiskScore, &resp.RiskLevel,
		&resp.SanctionsHit, &resp.MixerHit, &computedAt,
		&resp.Label, &resp.Category)
	if err != nil {
		return resp, err
	}

	rows, _ := db.Query(`SELECT unnest(risk_factors) FROM risk_scores WHERE address = $1`, address)
	defer rows.Close()
	var factors []string
	for rows.Next() {
		var f string
		rows.Scan(&f)
		factors = append(factors, f)
	}
	resp.RiskFactors = factors
	resp.ComputedAt = computedAt

	switch {
	case resp.RiskScore >= 80: resp.Recommendation = "BLOCK"
	case resp.RiskScore >= 60: resp.Recommendation = "REVIEW"
	case resp.RiskScore >= 30: resp.Recommendation = "MONITOR"
	default:                   resp.Recommendation = "ALLOW"
	}

	return resp, nil
}

func inflowsHandler(c *gin.Context) {
	type TokenInflow struct {
		Symbol    string  `json:"symbol"`
		Transfers int64   `json:"transfers"`
		USDValue  float64 `json:"usd_value"`
		USDLabel  string  `json:"usd_label"`
	}
	type InflowResponse struct {
		TotalUSD   float64       `json:"total_usd"`
		TotalLabel string        `json:"total_label"`
		Tokens     []TokenInflow `json:"tokens"`
		UpdatedAt  string        `json:"updated_at"`
	}

	rows, err := db.Query(`SELECT symbol, transfers, usd_value FROM live_dollar_inflows`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var tokens []TokenInflow
	var totalUSD float64
	for rows.Next() {
		var t TokenInflow
		rows.Scan(&t.Symbol, &t.Transfers, &t.USDValue)
		if t.USDValue >= 1e9 {
			t.USDLabel = fmt.Sprintf("$%.2fB", t.USDValue/1e9)
		} else {
			t.USDLabel = fmt.Sprintf("$%.2fM", t.USDValue/1e6)
		}
		totalUSD += t.USDValue
		tokens = append(tokens, t)
	}

	totalLabel := fmt.Sprintf("$%.2fB", totalUSD/1e9)
	c.JSON(200, InflowResponse{
		TotalUSD:   totalUSD,
		TotalLabel: totalLabel,
		Tokens:     tokens,
		UpdatedAt:  time.Now().Format(time.RFC3339),
	})
}
