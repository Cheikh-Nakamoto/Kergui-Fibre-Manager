-- Identity tracking across random MAC changes, blacklist, and the one-time
-- "use your fixed MAC" notice.

CREATE TABLE identities (
    router_id      TEXT NOT NULL,
    identity       TEXT NOT NULL,
    shared         INTEGER NOT NULL DEFAULT 0,
    blacklisted_at INTEGER NOT NULL DEFAULT 0,
    lifted_at      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, identity)
);

CREATE TABLE blacklist_cuts (
    router_id TEXT NOT NULL,
    mac       TEXT NOT NULL,
    identity  TEXT NOT NULL,
    cut_at    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, mac)
);

CREATE TABLE mac_notices (
    router_id TEXT NOT NULL,
    mac       TEXT NOT NULL,
    acked_at  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, mac)
);
