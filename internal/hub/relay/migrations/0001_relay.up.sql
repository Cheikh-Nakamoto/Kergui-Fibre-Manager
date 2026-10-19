-- The relay lease, the presence of each candidate relay, the command queue
-- and the last signed snapshot per household.

-- One row per household: the box allows one admin session, so the lease is
-- the household's, not the relay's.
CREATE TABLE IF NOT EXISTS hub_relay_leases (
    tenant_id  UUID PRIMARY KEY,
    agent_id   UUID,
    epoch      BIGINT      NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- kind and is_owner are copied in on each heartbeat rather than joined from
-- hub_agents and the kernel's memberships: the lease decision needs them,
-- and a module that reaches into another's tables is a module that breaks
-- when the other one migrates.
CREATE TABLE IF NOT EXISTS hub_relay_presence (
    agent_id   UUID PRIMARY KEY,
    tenant_id  UUID        NOT NULL,
    kind       TEXT        NOT NULL DEFAULT '',
    is_owner   BOOLEAN     NOT NULL DEFAULT false,
    eligible   BOOLEAN     NOT NULL,
    reason     TEXT        NOT NULL DEFAULT '',
    ssid       TEXT        NOT NULL DEFAULT '',
    bssid      TEXT        NOT NULL DEFAULT '',
    router_pin TEXT        NOT NULL DEFAULT '',
    lan_ip     TEXT        NOT NULL DEFAULT '',
    version    TEXT        NOT NULL DEFAULT '',
    seen_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS hub_relay_presence_tenant_idx ON hub_relay_presence (tenant_id, seen_at DESC);

-- envelope and body are the bytes the owner signed. The hub stores method
-- and path alongside only to answer "what was asked" without re-parsing;
-- what the relay enforces is the signed envelope.
-- id is the cmd_id the owner signed, not a row id of the hub's own: one
-- command has one identity from the app to the relay and back, and
-- re-submitting the same signed command is then naturally idempotent.
CREATE TABLE IF NOT EXISTS hub_relay_commands (
    id             TEXT PRIMARY KEY,
    tenant_id      UUID        NOT NULL,
    issuer_user_id UUID,
    envelope       JSONB       NOT NULL,
    body           BYTEA,
    method         TEXT        NOT NULL,
    path           TEXT        NOT NULL,
    status         TEXT        NOT NULL,
    epoch          BIGINT      NOT NULL DEFAULT 0,
    agent_id       UUID,
    result         JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at     TIMESTAMPTZ NOT NULL,
    delivered_at   TIMESTAMPTZ,
    done_at        TIMESTAMPTZ
);

-- The claim query orders queued commands by age within a household.
CREATE INDEX IF NOT EXISTS hub_relay_commands_queue_idx
    ON hub_relay_commands (tenant_id, status, created_at);

CREATE TABLE IF NOT EXISTS hub_relay_snapshots (
    tenant_id UUID        NOT NULL,
    kind      TEXT        NOT NULL,
    agent_id  UUID        NOT NULL,
    payload   JSONB       NOT NULL,
    as_of     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, kind)
);
