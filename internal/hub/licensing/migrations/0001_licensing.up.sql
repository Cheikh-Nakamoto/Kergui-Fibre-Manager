-- RFC-006 licensing: accounts (a verified phone number), the WhatsApp
-- sign-in codes, the sessions of the household's phones, one subscription
-- per account and box, the trial, box transfers, payments and reminders.
-- Secrets are stored only as SHA-256 (codes, session tokens).

CREATE TABLE IF NOT EXISTS hub_licensing_accounts (
    id         UUID PRIMARY KEY,
    phone      TEXT        NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hub_licensing_otp_challenges (
    id          UUID PRIMARY KEY,
    phone       TEXT        NOT NULL,
    code_hash   BYTEA       NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    attempts    INT         NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The rate limit and the verification both read a number's latest codes.
CREATE INDEX IF NOT EXISTS hub_licensing_otp_phone_idx ON hub_licensing_otp_challenges (phone, created_at DESC);

CREATE TABLE IF NOT EXISTS hub_licensing_sessions (
    token_hash   BYTEA PRIMARY KEY,
    account_id   UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    device       TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    revoked_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS hub_licensing_sessions_account_idx ON hub_licensing_sessions (account_id);

-- One subscription per account and box. reminder_sent_for is the expiry
-- the last reminder announced: crediting a pass moves expires_at, which is
-- what makes the next reminder due.
CREATE TABLE IF NOT EXISTS hub_licensing_subscriptions (
    account_id        UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    box_id            TEXT        NOT NULL,
    tier              TEXT        NOT NULL CHECK (tier IN ('free', 'basic', 'max')),
    expires_at        TIMESTAMPTZ NOT NULL,
    reminder_sent_for TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, box_id)
);

CREATE INDEX IF NOT EXISTS hub_licensing_subscriptions_expiry_idx ON hub_licensing_subscriptions (expires_at);

-- The 7-day trial: once per number and once per box. The two unique
-- constraints are the guard; the use case only checks first to say which.
CREATE TABLE IF NOT EXISTS hub_licensing_trials (
    phone      TEXT        NOT NULL UNIQUE,
    box_id     TEXT        NOT NULL UNIQUE,
    account_id UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS hub_licensing_transfers (
    id          BIGSERIAL PRIMARY KEY,
    account_id  UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    from_box_id TEXT        NOT NULL,
    to_box_id   TEXT        NOT NULL,
    at          TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS hub_licensing_transfers_account_idx ON hub_licensing_transfers (account_id, at);

-- Payments, idempotent by the provider's reference (NabooPay order id, Play
-- purchase token): a webhook replayed any number of times credits once.
CREATE TABLE IF NOT EXISTS hub_licensing_payments (
    id           UUID PRIMARY KEY,
    provider     TEXT        NOT NULL,
    provider_ref TEXT        NOT NULL,
    account_id   UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    box_id       TEXT        NOT NULL,
    pass_code    TEXT        NOT NULL,
    amount_xof   BIGINT      NOT NULL CHECK (amount_xof >= 0),
    status       TEXT        NOT NULL CHECK (status IN ('pending', 'credited', 'failed', 'cancelled')),
    checkout_url TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL,
    credited_at  TIMESTAMPTZ,
    UNIQUE (provider, provider_ref)
);

CREATE INDEX IF NOT EXISTS hub_licensing_payments_pending_idx ON hub_licensing_payments (provider, created_at)
    WHERE status = 'pending';

-- The reminders that went out, one per subscription expiry.
CREATE TABLE IF NOT EXISTS hub_licensing_reminders (
    account_id UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    box_id     TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    sent_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (account_id, box_id, expires_at)
);
