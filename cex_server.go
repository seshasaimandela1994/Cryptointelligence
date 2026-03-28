package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

var cexDB *sql.DB

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

type CEXCandidate struct {
	Address         string  `json:"address"`
	BestRole        string  `json:"best_candidate_role"`
	BestScore       float64 `json:"best_candidate_score"`
	DepositScore    float64 `json:"deposit_score"`
	HotScore        float64 `json:"hot_score"`
	ColdScore       float64 `json:"cold_score"`
	UniqueSenders   int64   `json:"unique_senders_30d"`
	UniqueReceivers int64   `json:"unique_receivers_30d"`
	OutboundUSD     float64 `json:"outbound_usd_30d"`
	InboundUSD      float64 `json:"inbound_usd_30d"`
	KnownExchange   string  `json:"known_exchange,omitempty"`
	KnownRole       string  `json:"known_role,omitempty"`
	Confidence      float64 `json:"known_confidence,omitempty"`
}

type ExchangeWallet struct {
	Address      string  `json:"address"`
	Role         string  `json:"wallet_role"`
	Subrole      string  `json:"wallet_subrole"`
	Method       string  `json:"attribution_method"`
	Confidence   float64 `json:"confidence_score"`
	Band         string  `json:"confidence_band"`
	Status       string  `json:"review_status"`
	Notes        string  `json:"analyst_notes,omitempty"`
	FirstBlock   int64   `json:"first_seen_block,omitempty"`
	LastBlock    int64   `json:"last_seen_block,omitempty"`
	DepositScore float64 `json:"deposit_score,omitempty"`
	HotScore     float64 `json:"hot_score,omitempty"`
	OutboundUSD  float64 `json:"outbound_usd_30d,omitempty"`
}

type ExchangeProfile struct {
	ExchangeID       int64            `json:"exchange_id"`
	CanonicalName    string           `json:"canonical_name"`
	LegalName        string           `json:"legal_name,omitempty"`
	TrustTier        string           `json:"trust_tier"`
	Status           string           `json:"status"`
	Category         string           `json:"exchange_category"`
	Jurisdiction     string           `json:"primary_jurisdiction"`
	Website          string           `json:"website_url,omitempty"`
	RegulatoryStatus string           `json:"regulatory_status"`
	AMLRisk          string           `json:"aml_risk_level"`
	Enforcement      bool             `json:"enforcement_history"`
	POR              bool             `json:"proof_of_reserves"`
	LabeledWallets   int              `json:"labeled_wallets"`
	AvgConfidence    float64          `json:"avg_confidence"`
	Wallets          []ExchangeWallet `json:"wallets"`
	Aliases          []string         `json:"aliases"`
}

type CEXScreenResult struct {
	Address          string        `json:"address"`
	IsExchangeWallet bool          `json:"is_exchange_wallet"`
	Exchange         string        `json:"exchange,omitempty"`
	Role             string        `json:"wallet_role,omitempty"`
	Confidence       float64       `json:"confidence,omitempty"`
	CandidateScores  *CEXCandidate `json:"candidate_scores,omitempty"`
	RiskSignals      []string      `json:"risk_signals"`
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)
	cexDB = db

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.Use(corsMiddleware())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "service": "cex-detection-api", "time": time.Now()})
	})

	v1 := r.Group("/v1/cex")
	v1.GET("/stats",           cexStatsHandler)
	v1.GET("/candidates",      cexCandidatesHandler)
	v1.GET("/exchange/:name",  cexExchangeHandler)
	v1.GET("/screen/:address", cexScreenHandler)
	v1.GET("/top/:role",       cexTopByRoleHandler)
	v1.GET("/exchanges/list",  cexExchangeListHandler)

	registerReviewRoutes(r)
	registerAlertRoutes(r)

	fmt.Println()
	fmt.Println("  CryptoIntelligence — CEX Detection API")
	fmt.Println("  ========================================")
	fmt.Println("  Running on http://localhost:8084 (CORS enabled)")
	fmt.Println()

	r.Run(":8084")
}

func cexStatsHandler(c *gin.Context) {
	stats := map[string]interface{}{}
	rows, err := cexDB.Query(`SELECT key, value FROM cex_stats_cache`)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()
	for rows.Next() {
		var k string; var v int64
		rows.Scan(&k, &v)
		stats[k] = v
	}
	var lastUpdated sql.NullTime
	cexDB.QueryRow(`SELECT MAX(last_computed_at) FROM wallet_features_ethereum`).Scan(&lastUpdated)
	last := ""
	if lastUpdated.Valid { last = lastUpdated.Time.Format(time.RFC3339) }
	c.JSON(200, gin.H{
		"total_wallets":              stats["total_wallets"],
		"scored_wallets":             stats["scored_wallets"],
		"high_confidence_candidates": stats["high_confidence"],
		"labeled_exchange_wallets":   stats["exchange_labels"],
		"total_exchanges":            stats["total_exchanges"],
		"top_candidate_score":        0.90,
		"transfers_indexed":          stats["transfers"],
		"transfers_with_usd":         stats["transfers_usd"],
		"last_updated":               last,
	})
}

func cexCandidatesHandler(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit > 200 { limit = 200 }
	minScore, _ := strconv.ParseFloat(c.DefaultQuery("min_score", "0.75"), 64)
	role := c.DefaultQuery("role", "")
	query := `SELECT vrc.address, vrc.best_candidate_role,
		ROUND(vrc.best_candidate_score::numeric,4),
		ROUND(COALESCE(vrc.deposit_score,0)::numeric,4),
		ROUND(COALESCE(vrc.hot_score,0)::numeric,4),
		ROUND(COALESCE(vrc.cold_score,0)::numeric,4),
		COALESCE(wcs.unique_token_senders_30d,0),
		COALESCE(wcs.unique_token_receivers_30d,0),
		COALESCE(wcs.total_outbound_usd_30d,0),
		COALESCE(wcs.total_inbound_usd_30d,0),
		COALESCE(ee.canonical_name,''),
		COALESCE(ewl.wallet_role::text,''),
		COALESCE(ewl.confidence_score,0)
		FROM mv_wallet_role_candidates vrc
		LEFT JOIN wallet_counterparty_stats_30d wcs ON wcs.wallet_id=vrc.wallet_id
		LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id=vrc.wallet_id AND ewl.is_active=TRUE
		LEFT JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
		WHERE vrc.best_candidate_score >= $1`
	if role != "" { query += fmt.Sprintf(" AND vrc.best_candidate_role='%s'", sanitize(role)) }
	query += " ORDER BY vrc.best_candidate_score DESC, wcs.total_outbound_usd_30d DESC NULLS LAST LIMIT $2"
	rows, err := cexDB.Query(query, minScore, limit)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()
	var candidates []CEXCandidate
	for rows.Next() {
		var cand CEXCandidate
		rows.Scan(&cand.Address, &cand.BestRole, &cand.BestScore,
			&cand.DepositScore, &cand.HotScore, &cand.ColdScore,
			&cand.UniqueSenders, &cand.UniqueReceivers,
			&cand.OutboundUSD, &cand.InboundUSD,
			&cand.KnownExchange, &cand.KnownRole, &cand.Confidence)
		candidates = append(candidates, cand)
	}
	c.JSON(200, gin.H{"total": len(candidates), "min_score": minScore, "candidates": candidates})
}

func cexExchangeHandler(c *gin.Context) {
	name := c.Param("name")
	var p ExchangeProfile
	var legal, website, juris, cat sql.NullString
	var avgConf sql.NullFloat64
	err := cexDB.QueryRow(`SELECT ee.exchange_id, ee.canonical_name, ee.legal_name,
		ee.trust_tier, ee.status, ee.exchange_category,
		ee.primary_jurisdiction, ee.website_url,
		rp.regulatory_status, rp.aml_risk_level,
		rp.enforcement_history_flag, rp.proof_of_reserves_flag,
		COUNT(DISTINCT ewl.wallet_id),
		ROUND(AVG(ewl.confidence_score)::numeric,4)
		FROM exchange_entities ee
		LEFT JOIN exchange_risk_profiles rp ON rp.exchange_id=ee.exchange_id
		LEFT JOIN exchange_wallet_labels ewl ON ewl.exchange_id=ee.exchange_id AND ewl.is_active=TRUE
		WHERE LOWER(ee.canonical_name)=LOWER($1)
		   OR EXISTS (SELECT 1 FROM exchange_aliases ea WHERE ea.exchange_id=ee.exchange_id AND LOWER(ea.alias_name)=LOWER($1))
		GROUP BY ee.exchange_id,ee.canonical_name,ee.legal_name,ee.trust_tier,ee.status,
			ee.exchange_category,ee.primary_jurisdiction,ee.website_url,
			rp.regulatory_status,rp.aml_risk_level,rp.enforcement_history_flag,rp.proof_of_reserves_flag
		LIMIT 1`, name).Scan(&p.ExchangeID, &p.CanonicalName, &legal,
		&p.TrustTier, &p.Status, &cat, &juris, &website,
		&p.RegulatoryStatus, &p.AMLRisk, &p.Enforcement, &p.POR,
		&p.LabeledWallets, &avgConf)
	if err == sql.ErrNoRows { c.JSON(404, gin.H{"error": "Exchange not found: "+name}); return }
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	if legal.Valid { p.LegalName = legal.String }
	if website.Valid { p.Website = website.String }
	if juris.Valid { p.Jurisdiction = juris.String }
	if cat.Valid { p.Category = cat.String }
	if avgConf.Valid { p.AvgConfidence = avgConf.Float64 }
	arows, _ := cexDB.Query(`SELECT alias_name FROM exchange_aliases WHERE exchange_id=$1 ORDER BY is_primary DESC`, p.ExchangeID)
	if arows != nil { defer arows.Close(); for arows.Next() { var a string; arows.Scan(&a); p.Aliases = append(p.Aliases, a) } }
	wrows, err := cexDB.Query(`SELECT wa.address, ewl.wallet_role::text, ewl.wallet_subrole::text,
		ewl.attribution_method::text, ewl.confidence_score, ewl.confidence_band::text,
		ewl.review_status::text, COALESCE(ewl.analyst_notes,''),
		COALESCE(wa.first_seen_block,0), COALESCE(wa.last_seen_block,0),
		COALESCE(vrc.deposit_score,0), COALESCE(vrc.hot_score,0),
		COALESCE(wcs.total_outbound_usd_30d,0)
		FROM exchange_wallet_labels ewl
		JOIN wallet_addresses wa ON wa.wallet_id=ewl.wallet_id
		LEFT JOIN v_wallet_role_candidates vrc ON vrc.wallet_id=ewl.wallet_id
		LEFT JOIN wallet_counterparty_stats_30d wcs ON wcs.wallet_id=ewl.wallet_id
		WHERE ewl.exchange_id=$1 AND ewl.is_active=TRUE
		ORDER BY ewl.confidence_score DESC`, p.ExchangeID)
	if err == nil && wrows != nil {
		defer wrows.Close()
		for wrows.Next() {
			var w ExchangeWallet
			wrows.Scan(&w.Address, &w.Role, &w.Subrole, &w.Method,
				&w.Confidence, &w.Band, &w.Status, &w.Notes,
				&w.FirstBlock, &w.LastBlock, &w.DepositScore, &w.HotScore, &w.OutboundUSD)
			p.Wallets = append(p.Wallets, w)
		}
	}
	c.JSON(200, p)
}

func cexScreenHandler(c *gin.Context) {
	addr := strings.ToLower(c.Param("address"))
	result := CEXScreenResult{Address: addr, RiskSignals: []string{}}
	var exchange, role string
	var confidence float64
	err := cexDB.QueryRow(`SELECT ee.canonical_name, ewl.wallet_role::text, ewl.confidence_score
		FROM exchange_wallet_labels ewl
		JOIN wallet_addresses wa ON wa.wallet_id=ewl.wallet_id
		JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
		WHERE wa.address=$1 AND ewl.is_active=TRUE
		ORDER BY ewl.confidence_score DESC LIMIT 1`, addr).Scan(&exchange, &role, &confidence)
	if err == nil {
		result.IsExchangeWallet = true
		result.Exchange = exchange
		result.Role = role
		result.Confidence = confidence
		result.RiskSignals = append(result.RiskSignals, "KNOWN_EXCHANGE_WALLET:"+exchange)
	}
	var walletID int64
	cexDB.QueryRow(`SELECT wallet_id FROM wallet_addresses WHERE address=$1`, addr).Scan(&walletID)
	if walletID > 0 {
		var cand CEXCandidate
		err = cexDB.QueryRow(`SELECT vrc.address, vrc.best_candidate_role,
			ROUND(vrc.best_candidate_score::numeric,4),
			ROUND(COALESCE(vrc.deposit_score,0)::numeric,4),
			ROUND(COALESCE(vrc.hot_score,0)::numeric,4),
			ROUND(COALESCE(vrc.cold_score,0)::numeric,4),
			COALESCE(wcs.unique_token_senders_30d,0),
			COALESCE(wcs.unique_token_receivers_30d,0),
			COALESCE(wcs.total_outbound_usd_30d,0),
			COALESCE(wcs.total_inbound_usd_30d,0)
			FROM mv_wallet_role_candidates vrc
			LEFT JOIN wallet_counterparty_stats_30d wcs ON wcs.wallet_id=vrc.wallet_id
			WHERE vrc.wallet_id=$1`, walletID).Scan(
			&cand.Address, &cand.BestRole, &cand.BestScore,
			&cand.DepositScore, &cand.HotScore, &cand.ColdScore,
			&cand.UniqueSenders, &cand.UniqueReceivers,
			&cand.OutboundUSD, &cand.InboundUSD)
		if err == nil {
			result.CandidateScores = &cand
			if cand.BestScore >= 0.90 { result.RiskSignals = append(result.RiskSignals, fmt.Sprintf("HIGH_CONFIDENCE:%.2f", cand.BestScore)) }
			if cand.OutboundUSD > 1_000_000 { result.RiskSignals = append(result.RiskSignals, fmt.Sprintf("HIGH_OUTBOUND_USD:$%.0f", cand.OutboundUSD)) }
			if cand.UniqueSenders > 100 { result.RiskSignals = append(result.RiskSignals, fmt.Sprintf("MANY_SENDERS:%d", cand.UniqueSenders)) }
		}
	}
	c.JSON(200, result)
}

func cexTopByRoleHandler(c *gin.Context) {
	role := c.Param("role")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit > 100 { limit = 100 }
	type topResult struct {
		Address   string  `json:"address"`
		Score     float64 `json:"score"`
		USD       float64 `json:"usd_value_30d"`
		Senders   int64   `json:"unique_senders_30d"`
		Receivers int64   `json:"unique_receivers_30d"`
		Exchange  string  `json:"known_exchange"`
		KnownRole string  `json:"known_role,omitempty"`
	}

	var queryStr string
	switch role {
	case "hot":
		queryStr = `SELECT t.address,
			ROUND(COALESCE(t.hot_candidate_score,0)::numeric,4),
			COALESCE(t.total_outbound_usd_30d,0), 0, COALESCE(t.total_unique_receivers_30d,0),
			COALESCE(ee.canonical_name,'NOT LABELED'), COALESCE(ewl.wallet_role::text,'')
			FROM mv_wallet_hot_candidates t
			LEFT JOIN wallet_addresses wa ON wa.address=t.address
			LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id=wa.wallet_id AND ewl.is_active=TRUE
			LEFT JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
			WHERE COALESCE(t.hot_candidate_score,0) > 0.30
			ORDER BY t.hot_candidate_score DESC, t.total_outbound_usd_30d DESC NULLS LAST
			LIMIT $1`
	case "deposit":
		queryStr = `SELECT t.address,
			ROUND(COALESCE(t.deposit_candidate_score,0)::numeric,4),
			COALESCE(t.total_inbound_usd_30d,0), COALESCE(t.total_unique_senders_30d,0), 0,
			COALESCE(ee.canonical_name,'NOT LABELED'), COALESCE(ewl.wallet_role::text,'')
			FROM mv_wallet_deposit_candidates t
			LEFT JOIN wallet_addresses wa ON wa.address=t.address
			LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id=wa.wallet_id AND ewl.is_active=TRUE
			LEFT JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
			WHERE COALESCE(t.deposit_candidate_score,0) > 0.30
			ORDER BY t.deposit_candidate_score DESC, t.total_inbound_usd_30d DESC NULLS LAST
			LIMIT $1`
	case "cold":
		queryStr = `SELECT t.address,
			ROUND(COALESCE(t.cold_score,0)::numeric,4),
			COALESCE(t.balance_usd,0), 0, 0,
			COALESCE(ee.canonical_name,'NOT LABELED'), COALESCE(ewl.wallet_role::text,'')
			FROM mv_wallet_cold_scores t
			LEFT JOIN wallet_addresses wa ON wa.address=t.address
			LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id=wa.wallet_id AND ewl.is_active=TRUE
			LEFT JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
			WHERE COALESCE(t.cold_score,0) > 0.30
			ORDER BY t.cold_score DESC, t.balance_usd DESC NULLS LAST
			LIMIT $1`
	default:
		c.JSON(400, gin.H{"error": "role must be deposit, hot, or cold"})
		return
	}
	rows, err := cexDB.Query(queryStr, limit)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()
	var results []topResult
	for rows.Next() {
		var r topResult
		rows.Scan(&r.Address, &r.Score, &r.USD, &r.Senders, &r.Receivers, &r.Exchange, &r.KnownRole)
		results = append(results, r)
	}
	c.JSON(200, gin.H{"role": role, "total": len(results), "results": results})
}

func sanitize(s string) string {
	s = strings.ReplaceAll(s, "'", "")
	s = strings.ReplaceAll(s, ";", "")
	return s
}

// ── ANALYST REVIEW WORKFLOW ───────────────────────────────

type ReviewAction struct {
	Action     string `json:"action"`      // approve, reject, dispute
	ExchangeID *int64 `json:"exchange_id"` // required for approve
	WalletRole string `json:"wallet_role"` // required for approve
	Notes      string `json:"notes"`
	Analyst    string `json:"analyst"`
}

func init() {
	// Register review routes — called from main via router
}

func registerReviewRoutes(r *gin.Engine) {
	review := r.Group("/v1/review")
	review.GET("/queue",              reviewQueueHandler)
	review.POST("/action/:wallet_id", reviewActionHandler)
	review.GET("/stats",              reviewStatsHandler)
}

func reviewQueueHandler(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit > 200 { limit = 200 }

	rows, err := cexDB.Query(`
		SELECT lrq.review_id, lrq.address,
			lrq.proposed_label_value,
			ROUND(lrq.proposed_confidence::numeric,4),
			COALESCE(lrq.reason,'detection_engine'),
			lrq.review_status::text,
			COALESCE(lrq.reviewer_name,''),
			lrq.created_at,
			COALESCE(vrc.best_candidate_score,0),
			COALESCE(wcs.total_outbound_usd_30d,0),
			COALESCE(wcs.unique_token_senders_30d,0)
		FROM label_review_queue lrq
		LEFT JOIN wallet_addresses wa ON wa.address=lrq.address
		LEFT JOIN mv_wallet_role_candidates vrc ON vrc.wallet_id=wa.wallet_id
		LEFT JOIN wallet_counterparty_stats_30d wcs ON wcs.wallet_id=wa.wallet_id
		WHERE lrq.review_status IN ('open','PENDING')
		ORDER BY lrq.created_at DESC LIMIT $1`, limit)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()

	type QueueItem struct {
		ReviewID    int64   `json:"review_id"`
		Address     string  `json:"address"`
		Role        string  `json:"proposed_label"`
		Confidence  float64 `json:"proposed_confidence"`
		Reason      string  `json:"reason"`
		Status      string  `json:"status"`
		Reviewer    string  `json:"reviewer,omitempty"`
		CreatedAt   string  `json:"created_at"`
		BestScore   float64 `json:"detection_score"`
		OutboundUSD float64 `json:"outbound_usd_30d"`
		Senders     int64   `json:"unique_senders_30d"`
	}

	var items []QueueItem
	for rows.Next() {
		var item QueueItem
		var createdAt time.Time
		rows.Scan(&item.ReviewID, &item.Address, &item.Role, &item.Confidence,
			&item.Reason, &item.Status, &item.Reviewer, &createdAt,
			&item.BestScore, &item.OutboundUSD, &item.Senders)
		item.CreatedAt = createdAt.Format(time.RFC3339)
		items = append(items, item)
	}
	c.JSON(200, gin.H{"total": len(items), "items": items})
}

func reviewActionHandler(c *gin.Context) {
	walletIDStr := c.Param("wallet_id")
	walletID, err := strconv.ParseInt(walletIDStr, 10, 64)
	if err != nil { c.JSON(400, gin.H{"error": "invalid wallet_id"}); return }

	var action ReviewAction
	if err := c.ShouldBindJSON(&action); err != nil {
		c.JSON(400, gin.H{"error": err.Error()}); return
	}

	analyst := action.Analyst
	if analyst == "" { analyst = "analyst" }

	switch action.Action {
	case "approve":
		if action.ExchangeID == nil || action.WalletRole == "" {
			c.JSON(400, gin.H{"error": "approve requires exchange_id and wallet_role"}); return
		}
		tx, _ := cexDB.Begin()

		// Add to exchange_wallet_labels
		_, err = tx.Exec(`
			INSERT INTO exchange_wallet_labels (
				wallet_id, exchange_id, wallet_role, wallet_subrole,
				attribution_method, confidence_score, confidence_band,
				review_status, is_primary_label, is_active,
				analyst_notes, created_by, approved_by
			) VALUES ($1,$2,$3::wallet_role_enum,'none'::wallet_subrole_enum,
				'model_predicted'::attribution_method_enum,
				0.85,'high'::confidence_band_enum,
				'approved'::review_status_enum,TRUE,TRUE,$4,$5,$5)
			ON CONFLICT DO NOTHING`,
			walletID, *action.ExchangeID, action.WalletRole,
			action.Notes, analyst)
		if err != nil { tx.Rollback(); c.JSON(500, gin.H{"error": err.Error()}); return }

		// Update wallet status
		tx.Exec(`UPDATE wallet_addresses SET current_label_status='labeled' WHERE wallet_id=$1`, walletID)

		// Close review queue item
		tx.Exec(`UPDATE label_review_queue SET review_status='approved', resolved_at=NOW(), assigned_to=$1 WHERE wallet_id=$2 AND review_status='open'`, analyst, walletID)

		// Update stats cache
		tx.Exec(`UPDATE cex_stats_cache SET value=value+1, updated_at=NOW() WHERE key='exchange_labels'`)

		tx.Commit()
		c.JSON(200, gin.H{"status": "approved", "wallet_id": walletID, "analyst": analyst})

	case "reject":
		_, err = cexDB.Exec(`UPDATE label_review_queue SET review_status='rejected', reviewed_at=NOW(), reviewer_name=$1 WHERE address=(SELECT address FROM wallet_addresses WHERE wallet_id=$2) AND review_status IN ('open','PENDING')`, analyst, walletID)
		if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
		c.JSON(200, gin.H{"status": "rejected", "wallet_id": walletID})

	case "dispute":
		_, err = cexDB.Exec(`UPDATE label_review_queue SET review_status='rejected', assigned_to=$1 WHERE address=(SELECT address FROM wallet_addresses WHERE wallet_id=$2) AND review_status='open'`, analyst, walletID)
		cexDB.Exec(`UPDATE exchange_wallet_labels SET review_status='disputed', analyst_notes=$1 WHERE wallet_id=$2 AND is_active=TRUE`, action.Notes, walletID)
		c.JSON(200, gin.H{"status": "disputed", "wallet_id": walletID})

	default:
		c.JSON(400, gin.H{"error": "action must be approve, reject, or dispute"})
	}
}

func reviewStatsHandler(c *gin.Context) {
	var open, approved, rejected, pending int64
	cexDB.QueryRow(`SELECT
		COUNT(*) FILTER (WHERE review_status='open'),
		COUNT(*) FILTER (WHERE review_status='approved'),
		COUNT(*) FILTER (WHERE review_status='rejected'),
		COUNT(*) FILTER (WHERE review_status='PENDING')
		FROM label_review_queue`).Scan(&open, &approved, &rejected, &pending)
	c.JSON(200, gin.H{
		"open": open + pending,
		"approved": approved,
		"rejected": rejected,
		"pending": pending,
		"total": open + pending + approved + rejected,
	})
}

// ── ALERT SYSTEM ─────────────────────────────────────────

func registerAlertRoutes(r *gin.Engine) {
	alerts := r.Group("/v1/alerts/cex")
	alerts.GET("/new",     newCandidateAlertsHandler)
	alerts.GET("/sweeps",  sweepAlertsHandler)
	alerts.GET("/summary", alertSummaryHandler)
}

func newCandidateAlertsHandler(c *gin.Context) {
	hours, _ := strconv.Atoi(c.DefaultQuery("hours", "24"))
	minScore, _ := strconv.ParseFloat(c.DefaultQuery("min_score", "0.85"), 64)

	rows, err := cexDB.Query(`
		SELECT wa.address, vrc.best_candidate_role,
			ROUND(vrc.best_candidate_score::numeric,4),
			COALESCE(wcs.total_outbound_usd_30d,0),
			COALESCE(wcs.unique_token_senders_30d,0),
			wfe.last_computed_at,
			COALESCE(ee.canonical_name,'NOT LABELED')
		FROM mv_wallet_role_candidates vrc
		JOIN wallet_addresses wa ON wa.wallet_id=vrc.wallet_id
		JOIN wallet_features_ethereum wfe ON wfe.wallet_id=vrc.wallet_id
		LEFT JOIN wallet_counterparty_stats_30d wcs ON wcs.wallet_id=vrc.wallet_id
		LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id=vrc.wallet_id AND ewl.is_active=TRUE
		LEFT JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
		WHERE vrc.best_candidate_score >= $1
		  AND wfe.last_computed_at >= NOW() - ($2 || ' hours')::interval
		  AND ewl.label_id IS NULL
		ORDER BY vrc.best_candidate_score DESC, wcs.total_outbound_usd_30d DESC NULLS LAST
		LIMIT 50`, minScore, hours)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()

	type Alert struct {
		Address     string  `json:"address"`
		Role        string  `json:"best_role"`
		Score       float64 `json:"score"`
		OutboundUSD float64 `json:"outbound_usd_30d"`
		Senders     int64   `json:"unique_senders_30d"`
		DetectedAt  string  `json:"detected_at"`
		Exchange    string  `json:"known_exchange"`
		Severity    string  `json:"severity"`
	}

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var detectedAt time.Time
		rows.Scan(&a.Address, &a.Role, &a.Score, &a.OutboundUSD,
			&a.Senders, &detectedAt, &a.Exchange)
		a.DetectedAt = detectedAt.Format(time.RFC3339)
		if a.Score >= 0.90 { a.Severity = "critical" } else if a.Score >= 0.85 { a.Severity = "high" } else { a.Severity = "medium" }
		alerts = append(alerts, a)
	}
	c.JSON(200, gin.H{"total": len(alerts), "hours": hours, "min_score": minScore, "alerts": alerts})
}

func sweepAlertsHandler(c *gin.Context) {
	rows, err := cexDB.Query(`
		SELECT wa_src.address, wa_dst.address,
			ROUND(dse.heuristic_score::numeric,4),
			COALESCE(dse.sweep_amount_usd,0),
			dse.inbound_count_prior_7d,
			dse.created_at,
			COALESCE(ee.canonical_name,'Unknown')
		FROM deposit_sweep_events dse
		JOIN wallet_addresses wa_src ON wa_src.wallet_id=dse.source_wallet_id
		JOIN wallet_addresses wa_dst ON wa_dst.wallet_id=dse.destination_wallet_id
		LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id=dse.destination_wallet_id AND ewl.is_active=TRUE
		LEFT JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
		WHERE dse.heuristic_score >= 0.90
		ORDER BY dse.heuristic_score DESC, dse.sweep_amount_usd DESC NULLS LAST
		LIMIT 20`)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()

	type SweepAlert struct {
		Source      string  `json:"source_address"`
		Destination string  `json:"destination_address"`
		Score       float64 `json:"heuristic_score"`
		SweepUSD    float64 `json:"sweep_amount_usd"`
		InboundCount int64  `json:"inbound_count"`
		DetectedAt  string  `json:"detected_at"`
		DestExchange string `json:"destination_exchange"`
	}

	var sweeps []SweepAlert
	for rows.Next() {
		var s SweepAlert
		var detectedAt time.Time
		rows.Scan(&s.Source, &s.Destination, &s.Score, &s.SweepUSD,
			&s.InboundCount, &detectedAt, &s.DestExchange)
		s.DetectedAt = detectedAt.Format(time.RFC3339)
		sweeps = append(sweeps, s)
	}
	c.JSON(200, gin.H{"total": len(sweeps), "sweeps": sweeps})
}

func alertSummaryHandler(c *gin.Context) {
	var newCandidates, highSweeps, totalSweeps int64
	cexDB.QueryRow(`SELECT COUNT(*) FROM mv_wallet_role_candidates WHERE best_candidate_score >= 0.90`).Scan(&newCandidates)
	cexDB.QueryRow(`SELECT COUNT(*) FROM deposit_sweep_events WHERE heuristic_score >= 0.90`).Scan(&highSweeps)
	cexDB.QueryRow(`SELECT COUNT(*) FROM deposit_sweep_events`).Scan(&totalSweeps)
	c.JSON(200, gin.H{
		"high_confidence_candidates": newCandidates,
		"high_score_sweeps":          highSweeps,
		"total_sweep_events":         totalSweeps,
		"platform_status":            "live",
	})
}

func cexExchangeListHandler(c *gin.Context) {
	rows, err := cexDB.Query(`
		SELECT ee.canonical_name, ee.trust_tier::text,
			ee.status::text, ee.exchange_category::text,
			COALESCE(ee.primary_jurisdiction,'Unknown'),
			COALESCE(ee.website_url,''),
			COUNT(ewl.label_id) as wallet_count,
			COALESCE(AVG(ewl.confidence_score),0) as avg_conf
		FROM exchange_entities ee
		LEFT JOIN exchange_wallet_labels ewl ON ewl.exchange_id=ee.exchange_id AND ewl.is_active=TRUE
		GROUP BY ee.exchange_id, ee.canonical_name, ee.trust_tier,
			ee.status, ee.exchange_category, ee.primary_jurisdiction, ee.website_url
		ORDER BY ee.trust_tier, ee.canonical_name`)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()

	type Exchange struct {
		N       int     `json:"n"`
		Name    string  `json:"name"`
		Tier    string  `json:"tier"`
		Status  string  `json:"status"`
		Cat     string  `json:"category"`
		Juris   string  `json:"juris"`
		Website string  `json:"website"`
		Wallets int     `json:"wallets"`
		Conf    float64 `json:"conf"`
		Reg     string  `json:"reg"`
		AML     string  `json:"aml"`
	}

	var exchanges []Exchange
	i := 1
	for rows.Next() {
		var ex Exchange
		rows.Scan(&ex.Name, &ex.Tier, &ex.Status, &ex.Cat,
			&ex.Juris, &ex.Website, &ex.Wallets, &ex.Conf)
		ex.N = i; i++
		if ex.Juris == "United States" || ex.Juris == "United Kingdom" ||
			ex.Juris == "Singapore" || ex.Juris == "Japan" || ex.Juris == "Australia" ||
			ex.Juris == "Netherlands" || ex.Juris == "Austria" || ex.Juris == "Estonia" {
			ex.Reg = "regulated"; ex.AML = "low"
		} else if ex.Juris == "Seychelles" || ex.Juris == "Cayman Islands" ||
			ex.Juris == "British Virgin Islands" || ex.Juris == "Malta" {
			ex.Reg = "offshore"; ex.AML = "medium"
		} else {
			ex.Reg = "mixed"; ex.AML = "medium"
		}
		ex.Conf = float64(int(ex.Conf*10000)) / 10000
		exchanges = append(exchanges, ex)
	}
	c.JSON(200, gin.H{"exchanges": exchanges, "total": len(exchanges)})
}
