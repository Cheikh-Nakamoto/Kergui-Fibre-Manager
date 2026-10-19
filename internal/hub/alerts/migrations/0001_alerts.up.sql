-- WhatsApp opt-ins, the alerts the hub sent, and what it did with them.
--
-- The hub stores a pairing code only as SHA-256, and a phone number only
-- because its owner wrote first: WhatsApp requires it, and so does the
-- design (RFC-005, opt-in).

-- One pending code per member. Asking again replaces it, so a code shown on
-- a screen that was closed cannot be redeemed later.
CREATE TABLE IF NOT EXISTS hub_whatsapp_pending (
    tenant_id  UUID        NOT NULL,
    user_id    UUID        NOT NULL,
    code_hash  BYTEA       NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX IF NOT EXISTS hub_whatsapp_pending_expires_idx ON hub_whatsapp_pending (expires_at);

-- A verified number, per household and member. The same number may serve
-- two households (an owner with two homes), which is why a reply is
-- resolved against the alert it answers and not against the number alone.
CREATE TABLE IF NOT EXISTS hub_whatsapp_optins (
    tenant_id  UUID        NOT NULL,
    user_id    UUID        NOT NULL,
    phone      TEXT        NOT NULL,
    locale     TEXT        NOT NULL DEFAULT 'fr',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    stopped_at TIMESTAMPTZ,
    PRIMARY KEY (tenant_id, user_id)
);

CREATE INDEX IF NOT EXISTS hub_whatsapp_optins_phone_idx ON hub_whatsapp_optins (phone) WHERE stopped_at IS NULL;

-- One alert per inventory window. The outbox delivers at least once, so
-- the uniqueness of window_id is what makes the household hear about it
-- exactly once.
CREATE TABLE IF NOT EXISTS hub_alerts (
    id          UUID PRIMARY KEY,
    tenant_id   UUID        NOT NULL,
    window_id   UUID        NOT NULL UNIQUE,
    devices     JSONB       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    reply_until TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS hub_alerts_tenant_idx ON hub_alerts (tenant_id, created_at DESC);

-- What was sent, to whom, over which channel — and why it was not, when a
-- provider refused: a household must be able to see that its alert never
-- went out.
CREATE TABLE IF NOT EXISTS hub_alert_deliveries (
    alert_id        UUID        NOT NULL REFERENCES hub_alerts (id) ON DELETE CASCADE,
    tenant_id       UUID        NOT NULL,
    user_id         UUID        NOT NULL,
    channel         TEXT        NOT NULL,
    provider        TEXT        NOT NULL,
    provider_msg_id TEXT        NOT NULL DEFAULT '',
    sent_at         TIMESTAMPTZ NOT NULL,
    error           TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (alert_id, user_id, channel)
);

-- A reply quotes the message it answers, so the provider's message id has
-- to resolve back to an alert.
CREATE INDEX IF NOT EXISTS hub_alert_deliveries_msg_idx
    ON hub_alert_deliveries (provider_msg_id) WHERE provider_msg_id <> '';
