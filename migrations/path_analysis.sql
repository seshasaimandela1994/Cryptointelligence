-- ============================================================
-- CryptoIntelligence — Path Analysis Engine
-- Traces money flow from high-risk wallets to exchanges
-- ============================================================

-- Materialized view for fast path lookups
CREATE TABLE IF NOT EXISTS money_paths (
    id              SERIAL PRIMARY KEY,
    origin_address  CHAR(42) NOT NULL,
    end_address     CHAR(42) NOT NULL,
    hops            INTEGER NOT NULL,
    path            TEXT[] NOT NULL,
    end_entity      VARCHAR(255),
    end_category    VARCHAR(50),
    risk_level      VARCHAR(20),
    computed_at     TIMESTAMP DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_paths_origin
    ON money_paths(origin_address);
CREATE INDEX IF NOT EXISTS idx_paths_end
    ON money_paths(end_address);
CREATE INDEX IF NOT EXISTS idx_paths_hops
    ON money_paths(hops);
CREATE INDEX IF NOT EXISTS idx_paths_risk
    ON money_paths(risk_level);
