-- Issue #41: the phone number verified over WhatsApp (a licensing account)
-- is the hub identity. Each account maps to the kernel user created for it
-- and to the household (tenant) it owns. The kernel's own tables are left
-- as they are: this is the hub's link between the two worlds.

CREATE TABLE IF NOT EXISTS hub_identity_households (
    account_id UUID        PRIMARY KEY REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL UNIQUE REFERENCES users (id) ON DELETE CASCADE,
    tenant_id  UUID        NOT NULL UNIQUE REFERENCES tenants (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
