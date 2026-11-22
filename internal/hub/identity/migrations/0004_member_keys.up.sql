-- Issue #47: a member's phone publishes its own public keys (Ed25519 to
-- sign, X25519 beside it), created in its Go core, so the owner's app can
-- delegate it view or control in the trust bundle. The hub only carries
-- them: the delegation is what the owner signs, and the owner's screen and
-- the member's show the same six digits derived from both keys, so a hub
-- that swapped one is caught. One key per member and household; publishing
-- again replaces it.

CREATE TABLE IF NOT EXISTS hub_identity_member_keys (
    tenant_id  UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    ed25519    BYTEA       NOT NULL CHECK (length(ed25519) = 32),
    x25519     BYTEA       NOT NULL CHECK (length(x25519) = 32),
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, user_id)
);
