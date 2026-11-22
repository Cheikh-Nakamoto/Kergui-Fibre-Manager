-- Issue #42: the owner invites a household member by phone number. The
-- kernel's invite_tokens are keyed on an e-mail and its member.invited
-- event sends one, which a phone user cannot receive; the hub keeps its own
-- invites, keyed on the E.164 number, and accepts them when that number
-- signs in (identity.SignIn). The membership itself is the kernel's.

CREATE TABLE IF NOT EXISTS hub_identity_invites (
    id            UUID        PRIMARY KEY,
    tenant_id     UUID        NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    phone         TEXT        NOT NULL,
    role          TEXT        NOT NULL CHECK (role IN ('ROLE_ADMIN', 'ROLE_VIEWER')),
    invited_by    UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL,
    accepted_at   TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ,
    membership_id UUID
);

-- One open invite per number and household: inviting again replaces it.
CREATE UNIQUE INDEX IF NOT EXISTS hub_identity_invites_open_idx
    ON hub_identity_invites (tenant_id, phone)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- The sign-in path: the open invites of a number.
CREATE INDEX IF NOT EXISTS hub_identity_invites_phone_idx
    ON hub_identity_invites (phone)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;
