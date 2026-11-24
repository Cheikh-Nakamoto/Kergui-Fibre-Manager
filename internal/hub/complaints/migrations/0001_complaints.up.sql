-- Complaints (issue #82, parent #68): a household's problem, told from the
-- app, the customer area on kergui.click or (issue #83) WhatsApp, and the
-- thread of messages between the customer and the Kergui team.
--
-- number is what the customer and the admins say (« plainte n° 12 »): one
-- sequence for every source. account_id is the licensing account when the
-- customer signed in (app, web); phone is always known, the account's
-- number or the WhatsApp sender's. tenant_id is the household the account
-- owns, when it has one. The app's journal rides along as text, capped at
-- 64 KiB (the most recent end is kept).
CREATE TABLE IF NOT EXISTS hub_complaints (
    id         UUID        PRIMARY KEY,
    number     BIGINT      GENERATED ALWAYS AS IDENTITY UNIQUE,
    source     TEXT        NOT NULL CHECK (source IN ('app', 'web', 'whatsapp')),
    category   TEXT        NOT NULL CHECK (category IN ('paiement', 'box', 'application', 'autre')),
    account_id UUID        REFERENCES hub_licensing_accounts (id) ON DELETE SET NULL,
    phone      TEXT        NOT NULL,
    tenant_id  UUID,
    status     TEXT        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'answered', 'closed')),
    journal    TEXT        NOT NULL DEFAULT '' CHECK (octet_length(journal) <= 65536),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at  TIMESTAMPTZ,
    closed_by  UUID
);

CREATE INDEX IF NOT EXISTS hub_complaints_status_idx ON hub_complaints (status, created_at DESC);
CREATE INDEX IF NOT EXISTS hub_complaints_account_idx ON hub_complaints (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS hub_complaints_phone_idx ON hub_complaints (phone, created_at DESC);

-- The thread: what the customer wrote and what the team answered, in
-- order. channel is where the message was written (app, web, whatsapp, or
-- backoffice for an answer from the back-office). An admin's answer keeps
-- its author and what reached the customer: phones pushed, push failures,
-- and the WhatsApp copy ('' none tried, queued, not_opted_in, off); the
-- queue row (ref complaint-message:<id>) says whether it went.
CREATE TABLE IF NOT EXISTS hub_complaint_messages (
    id           BIGSERIAL   PRIMARY KEY,
    complaint_id UUID        NOT NULL REFERENCES hub_complaints (id) ON DELETE CASCADE,
    sender       TEXT        NOT NULL CHECK (sender IN ('customer', 'admin')),
    channel      TEXT        NOT NULL CHECK (channel IN ('app', 'web', 'whatsapp', 'backoffice')),
    body         TEXT        NOT NULL CHECK (char_length(body) BETWEEN 1 AND 2000),
    author_id    UUID,
    pushed       INT         NOT NULL DEFAULT 0,
    push_failed  INT         NOT NULL DEFAULT 0,
    whatsapp     TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS hub_complaint_messages_thread_idx ON hub_complaint_messages (complaint_id, id);

-- The hub's settings the back-office edits, one JSON value per key. The
-- first is complaint_admins: the admins' WhatsApp numbers (E.164), who get
-- the complaints' summary and may use the commands (issue #83).
CREATE TABLE IF NOT EXISTS hub_settings (
    key        TEXT        PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID
);
