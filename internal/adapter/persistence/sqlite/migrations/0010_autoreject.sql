-- Auto-reject (RFC-006, Max): the setting of each box, and the devices the
-- rule tracks (when it first saw them, and whether they were accepted or
-- refused). Times are Unix seconds, 0 for none.
CREATE TABLE IF NOT EXISTS autoreject_settings (
    router_id TEXT PRIMARY KEY,
    enabled   INTEGER NOT NULL DEFAULT 0,
    since     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS autoreject_devices (
    router_id  TEXT NOT NULL,
    mac        TEXT NOT NULL,
    first_seen INTEGER NOT NULL,
    state      TEXT NOT NULL,
    decided_at INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, mac)
);
