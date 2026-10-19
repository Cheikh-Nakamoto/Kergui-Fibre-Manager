-- Paired relays and the trust material a household's owner signs or seals.
-- The hub stores secrets only as SHA-256 (pairing codes, agent tokens) or as
-- ciphertext it cannot read (the sealed box password).

CREATE TABLE IF NOT EXISTS hub_pairing_codes (
    code_hash   BYTEA PRIMARY KEY,
    tenant_id   UUID        NOT NULL,
    created_by  UUID        NOT NULL,
    kind        TEXT        NOT NULL,
    for_user_id UUID,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Expired or spent codes are swept periodically.
CREATE INDEX IF NOT EXISTS hub_pairing_codes_expires_idx ON hub_pairing_codes (expires_at);

CREATE TABLE IF NOT EXISTS hub_agents (
    id           UUID PRIMARY KEY,
    tenant_id    UUID        NOT NULL,
    name         TEXT        NOT NULL,
    kind         TEXT        NOT NULL,
    user_id      UUID,
    token_hash   BYTEA       NOT NULL UNIQUE,
    ed25519      BYTEA       NOT NULL,
    x25519       BYTEA       NOT NULL,
    key_id       TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at   TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS hub_agents_tenant_idx ON hub_agents (tenant_id);

-- One bundle per household, replaced as a whole. Version only ever grows:
-- rolling it back would resurrect a revoked key.
CREATE TABLE IF NOT EXISTS hub_trust_bundles (
    tenant_id  UUID PRIMARY KEY,
    version    BIGINT      NOT NULL CHECK (version > 0),
    payload    BYTEA       NOT NULL,
    sig        BYTEA       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hub_sealed_credentials (
    agent_id   UUID PRIMARY KEY REFERENCES hub_agents (id) ON DELETE CASCADE,
    tenant_id  UUID        NOT NULL,
    blob       BYTEA       NOT NULL,
    version    BIGINT      NOT NULL CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hub_networks (
    tenant_id  UUID PRIMARY KEY,
    ssids      TEXT[]      NOT NULL DEFAULT '{}',
    bssids     TEXT[]      NOT NULL DEFAULT '{}',
    router_pin TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
