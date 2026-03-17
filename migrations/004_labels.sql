
CREATE TABLE IF NOT EXISTS address_labels (
    address         CHAR(42)      PRIMARY KEY,
    label           TEXT,
    category        TEXT,
    sub_category    TEXT,
    confidence      SMALLINT      NOT NULL DEFAULT 0,
    source          TEXT,
    labelled_by     TEXT,
    metadata        JSONB,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_address_labels_category   ON address_labels (category);
CREATE INDEX IF NOT EXISTS idx_address_labels_confidence ON address_labels (confidence DESC);

CREATE TABLE IF NOT EXISTS sanctions_list (
    address         CHAR(42)      PRIMARY KEY,
    name            TEXT          NOT NULL,
    program         TEXT,
    source          TEXT          NOT NULL DEFAULT 'OFAC',
    added_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS labelling_runs (
    id              BIGSERIAL     PRIMARY KEY,
    started_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    addresses_checked INTEGER     NOT NULL DEFAULT 0,
    labels_added    INTEGER       NOT NULL DEFAULT 0,
    sanctions_found INTEGER       NOT NULL DEFAULT 0,
    status          TEXT          NOT NULL DEFAULT 'running'
);
