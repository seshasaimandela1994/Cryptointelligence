
CREATE TABLE IF NOT EXISTS clusters (
    id                UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    canonical_address CHAR(42)    NOT NULL UNIQUE,
    size              INTEGER     NOT NULL DEFAULT 1,
    confidence        SMALLINT    NOT NULL DEFAULT 50,
    entity_label      TEXT,
    entity_category   TEXT,
    heuristics_used   TEXT[],
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cluster_memberships (
    address     CHAR(42)    NOT NULL,
    cluster_id  UUID        NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    heuristic   TEXT        NOT NULL,
    confidence  SMALLINT    NOT NULL DEFAULT 50,
    PRIMARY KEY (address)
);

CREATE INDEX IF NOT EXISTS idx_memberships_cluster   ON cluster_memberships (cluster_id);
CREATE INDEX IF NOT EXISTS idx_memberships_heuristic ON cluster_memberships (heuristic);

CREATE TABLE IF NOT EXISTS cluster_evidence (
    id             BIGSERIAL   PRIMARY KEY,
    cluster_id     UUID        NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    heuristic      TEXT        NOT NULL,
    address_a      CHAR(42)    NOT NULL,
    address_b      CHAR(42)    NOT NULL,
    evidence_tx    CHAR(66),
    confidence     SMALLINT    NOT NULL DEFAULT 50,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_evidence_cluster  ON cluster_evidence (cluster_id);
CREATE INDEX IF NOT EXISTS idx_evidence_addr_a   ON cluster_evidence (address_a);
CREATE INDEX IF NOT EXISTS idx_evidence_addr_b   ON cluster_evidence (address_b);

CREATE TABLE IF NOT EXISTS heuristic_runs (
    id            BIGSERIAL   PRIMARY KEY,
    heuristic     TEXT        NOT NULL,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at  TIMESTAMPTZ,
    links_found   INTEGER     NOT NULL DEFAULT 0,
    clusters_merged INTEGER   NOT NULL DEFAULT 0,
    status        TEXT        NOT NULL DEFAULT 'running'
);
