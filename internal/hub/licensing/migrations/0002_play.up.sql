-- RFC-006 Google Play Billing. A subscription now says how it is paid: a
-- prepaid pass (NabooPay, the trial) whose days the hub adds, or a Play
-- subscription whose expiry is Google's. The reminder sweep skips Play: it
-- renews by itself.
ALTER TABLE hub_licensing_subscriptions
    ADD COLUMN IF NOT EXISTS channel TEXT NOT NULL DEFAULT 'prepaid' CHECK (channel IN ('prepaid', 'play'));

-- A Play purchase token is bound once, to the account and box it pays for;
-- a transfer moves the box. id orders the bindings: the box follows the
-- token bound last (an upgrade's new token replaces the old one).
CREATE TABLE IF NOT EXISTS hub_licensing_play_purchases (
    id             BIGSERIAL PRIMARY KEY,
    purchase_token TEXT        NOT NULL UNIQUE,
    account_id     UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    box_id         TEXT        NOT NULL,
    product_id     TEXT        NOT NULL,
    bound_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS hub_licensing_play_purchases_box_idx ON hub_licensing_play_purchases (account_id, box_id, id DESC);
