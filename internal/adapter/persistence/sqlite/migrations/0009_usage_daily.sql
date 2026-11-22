-- Consumption per device and per day (YYYY-MM-DD), kept 13 months: the Max
-- history and monthly filter (RFC-006). The month table (usage) stays the
-- source of the current month and of billing; this one adds the days up.
CREATE TABLE IF NOT EXISTS usage_daily (
    router_id TEXT NOT NULL,
    mac       TEXT NOT NULL,
    day       TEXT NOT NULL,
    bytes     INTEGER NOT NULL DEFAULT 0,
    sent      INTEGER NOT NULL DEFAULT 0,
    received  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, mac, day)
);

-- The months measured before the days were kept count on their first day.
INSERT OR IGNORE INTO usage_daily (router_id, mac, day, bytes, sent, received)
SELECT router_id, mac, period || '-01', bytes, sent, received FROM usage;
