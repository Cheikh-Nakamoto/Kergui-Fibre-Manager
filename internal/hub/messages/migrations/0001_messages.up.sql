-- « Messages » (issue #80, parent #67): what the Kergui team told the
-- households from the back-office, and to whom it went.
--
-- A message is written once and never edited: title, text, the audience it
-- was sent to (everyone, a level, or one household) and the channel. Push
-- is the only channel today; WhatsApp (issue #81) adds its own value and
-- reuses the deliveries. households and phones are the audience's size when
-- it was sent; the deliveries say what each phone answered.
CREATE TABLE IF NOT EXISTS hub_messages (
    id             UUID        PRIMARY KEY,
    title          TEXT        NOT NULL CHECK (char_length(title) BETWEEN 1 AND 80),
    body           TEXT        NOT NULL CHECK (char_length(body) BETWEEN 1 AND 500),
    channel        TEXT        NOT NULL CHECK (channel IN ('push', 'whatsapp')),
    audience       TEXT        NOT NULL CHECK (audience IN ('all', 'tier', 'household')),
    -- The level ('free', 'basic', 'max') or the household's id; '' for all.
    audience_value TEXT        NOT NULL DEFAULT '',
    tenant_id      UUID,
    author_id      UUID,
    households     INT         NOT NULL DEFAULT 0,
    phones         INT         NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS hub_messages_created_idx ON hub_messages (created_at DESC, id DESC);

-- One row per phone (push) or number (WhatsApp) a message was sent to.
-- device is the platform and the end of the token, enough to tell two
-- phones apart, never the token itself.
CREATE TABLE IF NOT EXISTS hub_message_deliveries (
    id         BIGSERIAL   PRIMARY KEY,
    message_id UUID        NOT NULL REFERENCES hub_messages (id) ON DELETE CASCADE,
    tenant_id  UUID        NOT NULL,
    user_id    UUID,
    channel    TEXT        NOT NULL,
    provider   TEXT        NOT NULL,
    device     TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL CHECK (status IN ('sent', 'failed')),
    error      TEXT        NOT NULL DEFAULT '',
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS hub_message_deliveries_message_idx ON hub_message_deliveries (message_id);
CREATE INDEX IF NOT EXISTS hub_message_deliveries_tenant_idx ON hub_message_deliveries (tenant_id, sent_at DESC);
