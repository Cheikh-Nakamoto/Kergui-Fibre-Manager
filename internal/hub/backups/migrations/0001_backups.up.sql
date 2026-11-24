-- Issue #88: a household's Kergui backup on the hub, per box. The archive is
-- the canonical one (appareils.csv and conso.csv, their columns only): the
-- hub rewrites what it receives, so a box password or a vault a sender put
-- in it never lands here. box_id is the core's id for the box (the router
-- URL as a slug).
CREATE TABLE IF NOT EXISTS hub_backups (
    tenant_id   UUID        NOT NULL,
    box_id      TEXT        NOT NULL,
    archive     BYTEA       NOT NULL,
    source_kind TEXT        NOT NULL CHECK (source_kind IN ('phone', 'relay')),
    source_id   TEXT        NOT NULL,
    devices     INT         NOT NULL DEFAULT 0,
    months      INT         NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, box_id)
);

-- The archives a merge replaced, kept a few days to undo a bad one. The
-- module prunes them on each save (five per box, seven days).
CREATE TABLE IF NOT EXISTS hub_backup_versions (
    id          BIGSERIAL   PRIMARY KEY,
    tenant_id   UUID        NOT NULL,
    box_id      TEXT        NOT NULL,
    archive     BYTEA       NOT NULL,
    source_kind TEXT        NOT NULL,
    source_id   TEXT        NOT NULL,
    devices     INT         NOT NULL DEFAULT 0,
    months      INT         NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL,
    replaced_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS hub_backup_versions_box_idx ON hub_backup_versions (tenant_id, box_id, replaced_at DESC);
