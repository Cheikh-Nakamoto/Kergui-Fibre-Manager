-- The closed Wi-Fi (RFC-007 D7): what the household asked of each box's
-- Wi-Fi, and the devices « déconnectés du Wi-Fi » (D5). The box is the truth
-- for display; this intent only finishes an interrupted close, ends an add
-- window and keeps devices from approving themselves while closed. Access
-- points are JSON arrays of the box's ids; times are Unix seconds, 0 for
-- none.
CREATE TABLE IF NOT EXISTS wifi_lock (
    router_id       TEXT PRIMARY KEY,
    closed          INTEGER NOT NULL DEFAULT 0,
    household_aps   TEXT    NOT NULL DEFAULT '[]',
    visitor_aps     TEXT    NOT NULL DEFAULT '[]',
    visitors_closed INTEGER NOT NULL DEFAULT 0,
    window_until    INTEGER NOT NULL DEFAULT 0,
    since           INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS wifi_cut_devices (
    router_id TEXT NOT NULL,
    mac       TEXT NOT NULL,
    PRIMARY KEY (router_id, mac)
);
