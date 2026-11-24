-- « Messages » by WhatsApp (issue #81, parent #67).
--
-- whatsmeow is not WhatsApp's official API, and bulk sending gets a number
-- banned: a back-office WhatsApp message goes to one household or to a
-- small hand-picked group of opted-in numbers, and the hub sends one every
-- 40 s (KERGUI_HUB_WHATSAPP_SEND_INTERVAL). The alerts keep their own path
-- and never wait in this queue.

-- A hand-picked group of numbers is its own audience.
ALTER TABLE hub_messages DROP CONSTRAINT IF EXISTS hub_messages_audience_check;
ALTER TABLE hub_messages ADD CONSTRAINT hub_messages_audience_check
    CHECK (audience IN ('all', 'tier', 'household', 'numbers'));

-- One row per number to send to. A row is claimed with FOR UPDATE SKIP
-- LOCKED and stays locked while it is sent, so two workers never send it
-- twice, and a hub that stops halfway leaves it 'queued' for the next one.
-- tenant_id and user_id are the opt-in the number came from.
CREATE TABLE IF NOT EXISTS hub_whatsapp_queue (
    id              BIGSERIAL   PRIMARY KEY,
    message_id      UUID        NOT NULL REFERENCES hub_messages (id) ON DELETE CASCADE,
    tenant_id       UUID        NOT NULL,
    user_id         UUID,
    phone           TEXT        NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'sent', 'failed')),
    attempts        INT         NOT NULL DEFAULT 0,
    next_at         TIMESTAMPTZ NOT NULL,
    last_error      TEXT        NOT NULL DEFAULT '',
    provider_msg_id TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS hub_whatsapp_queue_next_idx ON hub_whatsapp_queue (next_at, id) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS hub_whatsapp_queue_message_idx ON hub_whatsapp_queue (message_id);

-- The pace, shared by every replica: when the last message of the queue
-- went out. A worker sends only while it holds this row and only once the
-- interval has passed since then, so two replicas, or a restart, never
-- send two messages closer than the interval.
CREATE TABLE IF NOT EXISTS hub_whatsapp_pace (
    id           INT PRIMARY KEY CHECK (id = 1),
    last_sent_at TIMESTAMPTZ
);

INSERT INTO hub_whatsapp_pace (id, last_sent_at) VALUES (1, NULL) ON CONFLICT (id) DO NOTHING;
