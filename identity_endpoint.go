package main

// Add this route to your api_server_v2.go v1 group:
// v1.GET("/identity/:address", identityHandler)

// identityHandler returns all identity links for an address
func identityHandler(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))
	cacheKey := "identity:" + address

	// Check cache
	cached, err := rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		rdb.Incr(ctx, "metrics:cache_hits")
		c.Header("X-Cache", "HIT")
		c.Data(200, "application/json", []byte(cached))
		return
	}

	rdb.Incr(ctx, "metrics:cache_misses")

	// Query identity links
	rows, err := db.Query(`
		SELECT identity, platform, confidence, verified, source, created_at
		FROM identity_links
		WHERE address = $1
		ORDER BY confidence DESC`, address)
	if err != nil {
		c.JSON(500, gin.H{"error": "DB error"})
		return
	}
	defer rows.Close()

	type IdentityLink struct {
		Identity   string    `json:"identity"`
		Platform   string    `json:"platform"`
		Confidence int       `json:"confidence"`
		Verified   bool      `json:"verified"`
		Source     string    `json:"source"`
		CreatedAt  time.Time `json:"created_at"`
	}

	links := []IdentityLink{}
	for rows.Next() {
		var il IdentityLink
		rows.Scan(&il.Identity, &il.Platform, &il.Confidence,
			&il.Verified, &il.Source, &il.CreatedAt)
		links = append(links, il)
	}

	// Get risk score too
	var riskScore int
	var riskLevel, label string
	db.QueryRow(`SELECT rs.risk_score, rs.risk_level,
		COALESCE(al.label,'Unknown')
		FROM risk_scores rs
		LEFT JOIN address_labels al ON rs.address = al.address
		WHERE rs.address = $1`, address).
		Scan(&riskScore, &riskLevel, &label)

	result := gin.H{
		"address":        address,
		"label":          label,
		"risk_score":     riskScore,
		"risk_level":     riskLevel,
		"identity_count": len(links),
		"identities":     links,
	}

	data, _ := json.Marshal(result)
	rdb.Set(ctx, cacheKey, data, 1*time.Hour)
	c.Header("X-Cache", "MISS")
	c.JSON(200, result)
}
