-- Traffic metering. traffic_last keeps the previous counter sample per device so
-- deltas survive restarts; usage accumulates bytes per billing period (YYYY-MM).

CREATE TABLE IF NOT EXISTS traffic_last (
    router_id  TEXT NOT NULL,
    mac        TEXT NOT NULL,
    rx         INTEGER NOT NULL DEFAULT 0,
    tx         INTEGER NOT NULL DEFAULT 0,
    link_secs  INTEGER NOT NULL DEFAULT 0,
    sampled_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, mac)
);

CREATE TABLE IF NOT EXISTS usage (
    router_id TEXT NOT NULL,
    mac       TEXT NOT NULL,
    period    TEXT NOT NULL,
    bytes     INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, mac, period)
);
