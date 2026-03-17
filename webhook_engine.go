package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	_ "github.com/lib/pq"
)

// ============================================================
// CryptoIntelligence — Phase 9 Webhook Alert System
// Monitors for new CRITICAL/HIGH addresses and pushes alerts
// ============================================================

var db  *sql.DB
var rdb *redis.Client
var ctx = context.Background()
var jwtSecret = []byte("cryptointelligence-secret-2026")

type WebhookSubscription struct {
	ID        int       `json:"id"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	LastFired *time.Time `json:"last_fired,omitempty"`
	FireCount int       `json:"fire_count"`
	FailCount int       `json:"fail_count"`
}

type WebhookPayload struct {
	Event     string    `json:"event"`
	Address   string    `json:"address"`
	RiskScore int       `json:"risk_score"`
	RiskLevel string    `json:"risk_level"`
	RiskFactors []string `json:"risk_factors"`
	Label     string    `json:"label"`
	Category  string    `json:"category"`
	Action    string    `json:"action"`
	DetectedAt time.Time `json:"detected_at"`
	Platform  string    `json:"platform"`
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	redisURL := os.Getenv("REDIS_URL")
	if dbURL == "" { log.Fatal("DATABASE_URL not set") }
	if redisURL == "" { redisURL = "redis://localhost:6379" }

	var err error
	db, err = sql.Open("postgres", dbURL)
	if err != nil { log.Fatal("DB connect error:", err) }
	defer db.Close()
	db.SetMaxOpenConns(25)

	opt, _ := redis.ParseURL(redisURL)
	rdb = redis.NewClient(opt)
	_, err = rdb.Ping(ctx).Result()
	if err != nil { log.Fatal("Redis connect error:", err) }

	// Start webhook monitor in background
	go webhookMonitor()

	// API server for webhook management
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Authorization", "X-API-Key"},
		MaxAge:       12 * time.Hour,
	}))

	r.GET("/health", healthHandler)
	r.POST("/auth/token", generateTokenHandler)

	v1 := r.Group("/v1")
	v1.Use(authMiddleware())
	{
		// Webhook management
		v1.POST("/webhooks",           registerWebhookHandler)
		v1.GET("/webhooks",            listWebhooksHandler)
		v1.DELETE("/webhooks/:id",     deleteWebhookHandler)
		v1.GET("/webhooks/:id/logs",   webhookLogsHandler)
		v1.POST("/webhooks/test",      testWebhookHandler)

		// Risk API (passthrough)
		v1.GET("/risk/:address",       riskHandler)
		v1.GET("/stats",               statsHandler)
	}

	fmt.Println()
	fmt.Println("  CryptoIntelligence — Phase 9 Webhook Engine")
	fmt.Println("  =============================================")
	fmt.Println("  Webhook Monitor: RUNNING (checks every 60s)")
	fmt.Println("  Endpoints:")
	fmt.Println("    POST   /v1/webhooks          Register webhook URL")
	fmt.Println("    GET    /v1/webhooks          List all webhooks")
	fmt.Println("    DELETE /v1/webhooks/:id      Remove webhook")
	fmt.Println("    GET    /v1/webhooks/:id/logs View delivery logs")
	fmt.Println("    POST   /v1/webhooks/test     Send test alert")
	fmt.Println()

	r.Run(":8080")
}

// ── Webhook Monitor — runs every 60 seconds ───────────────────
func webhookMonitor() {
	fmt.Println("  ✓ Webhook monitor started")

	// Track last check time in Redis
	lastCheckKey := "webhook:last_check"
	
	for {
		time.Sleep(60 * time.Second)

		// Get last check timestamp
		lastCheck := time.Now().Add(-65 * time.Second)
		if cached, err := rdb.Get(ctx, lastCheckKey).Result(); err == nil {
			if t, err := time.Parse(time.RFC3339, cached); err == nil {
				lastCheck = t
			}
		}

		// Find new CRITICAL/HIGH addresses since last check
		rows, err := db.Query(`
			SELECT rs.address, rs.risk_score, rs.risk_level,
				rs.risk_factors, rs.computed_at,
				COALESCE(al.label, 'Unknown') as label,
				COALESCE(al.category, 'unknown') as category
			FROM risk_scores rs
			LEFT JOIN address_labels al ON rs.address = al.address
			WHERE rs.computed_at > $1
			AND rs.risk_level IN ('CRITICAL', 'HIGH')
			ORDER BY rs.risk_score DESC
			LIMIT 50`, lastCheck)
		if err != nil {
			fmt.Printf("  ⚠ Monitor query error: %v\n", err)
			continue
		}

		count := 0
		for rows.Next() {
			var addr, level, label, category string
			var score int
			var factors []string
			var computedAt time.Time

			rows.Scan(&addr, &score, &level, &factors, &computedAt, &label, &category)

			action := "REVIEW"
			if level == "CRITICAL" { action = "BLOCK" }

			payload := WebhookPayload{
				Event:      "NEW_" + level + "_ADDRESS",
				Address:    addr,
				RiskScore:  score,
				RiskLevel:  level,
				RiskFactors: factors,
				Label:      label,
				Category:   category,
				Action:     action,
				DetectedAt: computedAt,
				Platform:   "CryptoIntelligence v2.0",
			}

			fireWebhooks(payload, level)
			count++
		}
		rows.Close()

		if count > 0 {
			fmt.Printf("  ✓ Webhook monitor: fired %d alerts\n", count)
		}

		// Update last check time
		rdb.Set(ctx, lastCheckKey, time.Now().Format(time.RFC3339), 0)
	}
}

// ── Fire all active webhooks ──────────────────────────────────
func fireWebhooks(payload WebhookPayload, level string) {
	rows, err := db.Query(`
		SELECT id, url, secret FROM webhook_subscriptions
		WHERE active = TRUE
		AND $1 = ANY(events)`, level)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id int
		var url, secret string
		rows.Scan(&id, &url, &secret)
		go deliverWebhook(id, url, secret, payload)
	}
}

// ── Deliver single webhook ────────────────────────────────────
func deliverWebhook(webhookID int, url, secret string, payload WebhookPayload) {
	body, _ := json.Marshal(payload)

	// HMAC-SHA256 signature for verification
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CryptoIntelligence-Signature", signature)
	req.Header.Set("X-CryptoIntelligence-Event", payload.Event)
	req.Header.Set("X-CryptoIntelligence-Timestamp", time.Now().Format(time.RFC3339))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)

	success := false
	statusCode := 0
	if err == nil {
		statusCode = resp.StatusCode
		success = statusCode >= 200 && statusCode < 300
		resp.Body.Close()
	}

	// Log delivery
	payloadJSON, _ := json.Marshal(payload)
	db.Exec(`
		INSERT INTO webhook_logs
			(webhook_id, address, event_type, payload, status_code, success)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		webhookID, payload.Address, payload.Event,
		payloadJSON, statusCode, success)

	// Update subscription stats
	if success {
		db.Exec(`UPDATE webhook_subscriptions
			SET fire_count = fire_count + 1, last_fired = NOW()
			WHERE id = $1`, webhookID)
		fmt.Printf("  ✓ Webhook #%d fired: %s → %s\n", webhookID, payload.Address[:10], url)
	} else {
		db.Exec(`UPDATE webhook_subscriptions
			SET fail_count = fail_count + 1
			WHERE id = $1`, webhookID)
		fmt.Printf("  ⚠ Webhook #%d failed: %s (status: %d)\n", webhookID, url, statusCode)
	}
}

// ── Register Webhook Handler ──────────────────────────────────
func registerWebhookHandler(c *gin.Context) {
	var req struct {
		URL    string   `json:"url" binding:"required"`
		Events []string `json:"events"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "url field required"})
		return
	}
	if len(req.Events) == 0 {
		req.Events = []string{"CRITICAL", "HIGH"}
	}

	// Generate webhook secret
	secret := fmt.Sprintf("%x", sha256.Sum256([]byte(req.URL+time.Now().String())))[:32]

	var id int
	err := db.QueryRow(`
		INSERT INTO webhook_subscriptions (url, secret, events)
		VALUES ($1, $2, $3) RETURNING id`,
		req.URL, secret, req.Events).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": "Failed to register webhook"})
		return
	}

	c.JSON(201, gin.H{
		"id":      id,
		"url":     req.URL,
		"events":  req.Events,
		"secret":  secret,
		"message": "Webhook registered. Use secret to verify signatures.",
		"verify":  "Check X-CryptoIntelligence-Signature header in POST requests",
	})
}

// ── List Webhooks Handler ─────────────────────────────────────
func listWebhooksHandler(c *gin.Context) {
	rows, err := db.Query(`
		SELECT id, url, events, active, created_at,
			last_fired, fire_count, fail_count
		FROM webhook_subscriptions
		ORDER BY created_at DESC`)
	if err != nil {
		c.JSON(500, gin.H{"error": "DB error"})
		return
	}
	defer rows.Close()

	webhooks := []WebhookSubscription{}
	for rows.Next() {
		var w WebhookSubscription
		var events []byte
		rows.Scan(&w.ID, &w.URL, &events, &w.Active,
			&w.CreatedAt, &w.LastFired, &w.FireCount, &w.FailCount)
		json.Unmarshal(events, &w.Events)
		webhooks = append(webhooks, w)
	}
	c.JSON(200, gin.H{"webhooks": webhooks, "total": len(webhooks)})
}

// ── Delete Webhook Handler ────────────────────────────────────
func deleteWebhookHandler(c *gin.Context) {
	id := c.Param("id")
	db.Exec(`UPDATE webhook_subscriptions SET active = FALSE WHERE id = $1`, id)
	c.JSON(200, gin.H{"message": "Webhook deactivated", "id": id})
}

// ── Webhook Logs Handler ──────────────────────────────────────
func webhookLogsHandler(c *gin.Context) {
	id := c.Param("id")
	rows, err := db.Query(`
		SELECT address, event_type, status_code, success, fired_at
		FROM webhook_logs WHERE webhook_id = $1
		ORDER BY fired_at DESC LIMIT 50`, id)
	if err != nil {
		c.JSON(500, gin.H{"error": "DB error"})
		return
	}
	defer rows.Close()

	logs := []map[string]interface{}{}
	for rows.Next() {
		var addr, eventType string
		var statusCode int
		var success bool
		var firedAt time.Time
		rows.Scan(&addr, &eventType, &statusCode, &success, &firedAt)
		logs = append(logs, map[string]interface{}{
			"address":     addr,
			"event_type":  eventType,
			"status_code": statusCode,
			"success":     success,
			"fired_at":    firedAt,
		})
	}
	c.JSON(200, gin.H{"logs": logs, "total": len(logs)})
}

// ── Test Webhook Handler ──────────────────────────────────────
func testWebhookHandler(c *gin.Context) {
	var req struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "url required"})
		return
	}

	payload := WebhookPayload{
		Event:       "TEST_ALERT",
		Address:     "0xf55e5d50a4f6755fdca30a4ae1ac12f92d8e1907",
		RiskScore:   100,
		RiskLevel:   "CRITICAL",
		RiskFactors: []string{"MIXER_CONTACT", "BEHAVIORAL_HIGH_RISK"},
		Label:       "Tornado Cash Repeat User",
		Category:    "high_risk",
		Action:      "BLOCK",
		DetectedAt:  time.Now(),
		Platform:    "CryptoIntelligence v2.0 — TEST",
	}

	body, _ := json.Marshal(payload)
	req2, _ := http.NewRequest("POST", req.URL, bytes.NewBuffer(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-CryptoIntelligence-Event", "TEST_ALERT")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req2)
	if err != nil {
		c.JSON(200, gin.H{
			"success": false,
			"error":   err.Error(),
			"payload": payload,
		})
		return
	}
	defer resp.Body.Close()

	c.JSON(200, gin.H{
		"success":     resp.StatusCode >= 200 && resp.StatusCode < 300,
		"status_code": resp.StatusCode,
		"payload":     payload,
		"message":     "Test webhook fired",
	})
}

// ── Auth Middleware ───────────────────────────────────────────
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			auth := c.GetHeader("Authorization")
			if len(auth) > 7 { apiKey = auth[7:] }
		}
		if apiKey == "" {
			c.JSON(401, gin.H{"error": "API key required"})
			c.Abort()
			return
		}
		token, err := jwt.Parse(apiKey, func(t *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		})
		if err != nil || !token.Valid {
			c.JSON(401, gin.H{"error": "Invalid API key"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func generateTokenHandler(c *gin.Context) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "api_user",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(365 * 24 * time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString(jwtSecret)
	c.JSON(200, gin.H{"token": tokenString, "expires": "1 year"})
}

func healthHandler(c *gin.Context) {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM webhook_subscriptions WHERE active=TRUE").Scan(&count)
	c.JSON(200, gin.H{
		"status":              "healthy",
		"version":             "3.0.0",
		"webhook_monitor":     "running",
		"active_webhooks":     count,
		"monitor_interval":    "60s",
		"timestamp":           time.Now(),
	})
}

func riskHandler(c *gin.Context) {
	address := c.Param("address")
	var riskScore int
	var riskLevel, label string
	err := db.QueryRow(`
		SELECT rs.risk_score, rs.risk_level,
			COALESCE(al.label,'Unknown')
		FROM risk_scores rs
		LEFT JOIN address_labels al ON rs.address = al.address
		WHERE rs.address = $1`, address).
		Scan(&riskScore, &riskLevel, &label)
	if err != nil {
		c.JSON(404, gin.H{"error": "Not found"})
		return
	}
	action := "ALLOW"
	if riskScore >= 80 { action = "BLOCK" } else if riskScore >= 60 { action = "REVIEW" }
	c.JSON(200, gin.H{
		"address":        address,
		"risk_score":     riskScore,
		"risk_level":     riskLevel,
		"label":          label,
		"recommendation": action,
	})
}

func statsHandler(c *gin.Context) {
	var webhooks, fired int
	db.QueryRow("SELECT COUNT(*) FROM webhook_subscriptions WHERE active=TRUE").Scan(&webhooks)
	db.QueryRow("SELECT COALESCE(SUM(fire_count),0) FROM webhook_subscriptions").Scan(&fired)
	var critical, high int
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='CRITICAL'").Scan(&critical)
	db.QueryRow("SELECT COUNT(*) FROM risk_scores WHERE risk_level='HIGH'").Scan(&high)
	c.JSON(200, gin.H{
		"active_webhooks":   webhooks,
		"total_alerts_fired": fired,
		"critical_addresses": critical,
		"high_addresses":     high,
		"monitor_status":    "running",
	})
}
