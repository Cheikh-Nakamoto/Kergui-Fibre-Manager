-- What a WhatsApp reply asked for, and what became of it.
--
-- The table does two jobs. It makes a reply idempotent: one row per alert
-- and device number, so a provider that delivers the same message twice
-- blocks once. And it is the hub's own rate limit for the narrow key — the
-- relay's counter lives in memory and starts again at every hand-over,
-- while a household's budget should not.
CREATE TABLE IF NOT EXISTS hub_reply_actions (
    id         UUID PRIMARY KEY,
    tenant_id  UUID        NOT NULL,
    alert_id   UUID        NOT NULL,
    item_no    INTEGER     NOT NULL,
    mac        TEXT        NOT NULL,
    user_id    UUID,
    command_id TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL,
    reason     TEXT        NOT NULL DEFAULT '',
    at         TIMESTAMPTZ NOT NULL,
    UNIQUE (alert_id, item_no)
);

-- The rate limit counts a household's recent rows.
CREATE INDEX IF NOT EXISTS hub_reply_actions_rate_idx ON hub_reply_actions (tenant_id, at DESC);

-- The audit action this module records (see hub_agents/0002 for why the
-- kernel's enum has to be extended).
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'WHATSAPP_BLOCK_REQUESTED';
