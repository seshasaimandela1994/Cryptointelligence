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
	scoreMap := map[string]string{"deposit": "vrc.deposit_score", "hot": "vrc.hot_score", "cold": "vrc.cold_score"}
	usdMap := map[string]string{"deposit": "wcs.total_inbound_usd_30d", "hot": "wcs.total_outbound_usd_30d", "cold": "wcs.total_inbound_usd_30d"}
	scoreCol, ok := scoreMap[role]
	if !ok { c.JSON(400, gin.H{"error": "role must be deposit, hot, or cold"}); return }
	usdCol := usdMap[role]
	rows, err := cexDB.Query(fmt.Sprintf(`SELECT vrc.address,
		ROUND(COALESCE(%s,0)::numeric,4), COALESCE(%s,0),
		COALESCE(wcs.unique_token_senders_30d,0),
		COALESCE(wcs.unique_token_receivers_30d,0),
		COALESCE(ee.canonical_name,'NOT LABELED'),
		COALESCE(ewl.wallet_role::text,'')
		FROM mv_wallet_role_candidates vrc
		LEFT JOIN wallet_counterparty_stats_30d wcs ON wcs.wallet_id=vrc.wallet_id
		LEFT JOIN exchange_wallet_labels ewl ON ewl.wallet_id=vrc.wallet_id AND ewl.is_active=TRUE
		LEFT JOIN exchange_entities ee ON ee.exchange_id=ewl.exchange_id
		WHERE COALESCE(%s,0) > 0.30
		ORDER BY COALESCE(%s,0) DESC, %s DESC NULLS LAST
		LIMIT $1`, scoreCol, usdCol, scoreCol, scoreCol, usdCol), limit)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	defer rows.Close()
	type Result struct {
		Address   string  `json:"address"`
		Score     float64 `json:"score"`
		USD       float64 `json:"usd_value_30d"`
		Senders   int64   `json:"unique_senders_30d"`
		Receivers int64   `json:"unique_receivers_30d"`
		Exchange  string  `json:"known_exchange"`
		KnownRole string  `json:"known_role,omitempty"`
	}
	var results []Result
	for rows.Next() {
		var r Result
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
