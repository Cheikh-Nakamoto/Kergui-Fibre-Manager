-- Payment requests (one NabooPay checkout each) and admin settings.

CREATE TABLE IF NOT EXISTS payments (
    id            TEXT PRIMARY KEY,
    router_id     TEXT NOT NULL,
    mac           TEXT NOT NULL,
    period        TEXT NOT NULL,
    bytes         INTEGER NOT NULL DEFAULT 0,
    amount_xof    INTEGER NOT NULL DEFAULT 0,
    customer_name TEXT NOT NULL DEFAULT '',
    phone         TEXT NOT NULL DEFAULT '',
    order_id      TEXT NOT NULL DEFAULT '',
    checkout_url  TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending',
    created_at    INTEGER NOT NULL DEFAULT 0,
    updated_at    INTEGER NOT NULL DEFAULT 0,
    paid_at       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS payments_by_mac ON payments (router_id, mac, period);
CREATE INDEX IF NOT EXISTS payments_by_status ON payments (status);
CREATE INDEX IF NOT EXISTS payments_by_order ON payments (order_id);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
