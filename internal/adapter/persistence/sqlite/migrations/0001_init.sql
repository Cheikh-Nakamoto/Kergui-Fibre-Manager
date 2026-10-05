-- Initial schema. Device identity is (router_id, mac): the MAC is the identity,
-- the IP is a mutable attribute (brief §6, §8). Inventory (devices) and access
-- control (access_rules) are separate models (brief §18).

CREATE TABLE IF NOT EXISTS routers (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    base_url   TEXT NOT NULL DEFAULT '',
    adapter_id TEXT NOT NULL DEFAULT '',
    vendor     TEXT NOT NULL DEFAULT '',
    model      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS devices (
    id          TEXT PRIMARY KEY,
    router_id   TEXT NOT NULL,
    mac         TEXT NOT NULL,
    ip          TEXT NOT NULL DEFAULT '',
    hostname    TEXT NOT NULL DEFAULT '',
    custom_name TEXT NOT NULL DEFAULT '',
    vendor      TEXT NOT NULL DEFAULT '',
    ssid        TEXT NOT NULL DEFAULT '',
    first_seen  INTEGER NOT NULL DEFAULT 0,
    last_seen   INTEGER NOT NULL DEFAULT 0,
    connected   INTEGER NOT NULL DEFAULT 0,
    blocked     INTEGER NOT NULL DEFAULT 0,
    notes       TEXT NOT NULL DEFAULT '',
    UNIQUE (router_id, mac)
);

CREATE TABLE IF NOT EXISTS access_rules (
    id         TEXT PRIMARY KEY,
    router_id  TEXT NOT NULL,
    mac        TEXT NOT NULL,
    mode       TEXT NOT NULL DEFAULT '',
    ssid       TEXT NOT NULL DEFAULT '',
    source     TEXT NOT NULL DEFAULT '',
    raw_ref    TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT 0,
    UNIQUE (router_id, mac, mode)
);

-- Encrypted-at-rest router credentials (brief §12). No plaintext is ever stored.
CREATE TABLE IF NOT EXISTS credentials (
    router_id  TEXT PRIMARY KEY,
    salt       BLOB NOT NULL,
    nonce      BLOB NOT NULL,
    ciphertext BLOB NOT NULL,
    updated_at INTEGER NOT NULL DEFAULT 0
);
