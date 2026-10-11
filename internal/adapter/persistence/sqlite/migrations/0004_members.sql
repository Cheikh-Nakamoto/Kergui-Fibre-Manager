-- Members (people who pay), their devices, multi-device payments (monthly
-- invoices) and the cuts applied for unpaid invoices.

CREATE TABLE members (
    id         TEXT PRIMARY KEY,
    router_id  TEXT NOT NULL,
    name       TEXT NOT NULL,
    phone      TEXT NOT NULL DEFAULT '',
    kind       TEXT NOT NULL DEFAULT 'proche',
    active     INTEGER NOT NULL DEFAULT 1,
    protected  INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE member_devices (
    router_id TEXT NOT NULL,
    mac       TEXT NOT NULL,
    member_id TEXT NOT NULL,
    PRIMARY KEY (router_id, mac)
);
CREATE INDEX member_devices_by_member ON member_devices (member_id);

-- A payment covers one or more devices; bytes per device live here.
CREATE TABLE payment_items (
    payment_id TEXT NOT NULL,
    mac        TEXT NOT NULL,
    bytes      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (payment_id, mac)
);
INSERT OR IGNORE INTO payment_items (payment_id, mac, bytes)
    SELECT id, mac, bytes FROM payments WHERE mac <> '';

ALTER TABLE payments ADD COLUMN member_id TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN kind TEXT NOT NULL DEFAULT 'test';
ALTER TABLE payments ADD COLUMN due_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX payments_by_member ON payments (member_id, period);

-- Devices whose internet kergui cut because of an unpaid invoice. Only these are
-- restored on payment: a manual cut by the admin is never undone automatically.
CREATE TABLE enforcement_cuts (
    router_id  TEXT NOT NULL,
    mac        TEXT NOT NULL,
    payment_id TEXT NOT NULL,
    cut_at     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, mac)
);
