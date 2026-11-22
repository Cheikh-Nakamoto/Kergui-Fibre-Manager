-- The owner-signed relay policy (RFC-006, #36): the auto-reject rule as the
-- owner set it on the phone, and the entitlement token that pays for it. One
-- per household, replaced as a whole; like the trust bundle, the version only
-- ever grows, so the hub cannot hand a relay an older one.
CREATE TABLE IF NOT EXISTS hub_relay_policies (
    tenant_id  UUID PRIMARY KEY,
    version    BIGINT      NOT NULL CHECK (version > 0),
    payload    BYTEA       NOT NULL,
    sig        BYTEA       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'RELAY_POLICY_UPDATED';
