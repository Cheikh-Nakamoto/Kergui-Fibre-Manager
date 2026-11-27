-- When the stale rules of each box were last purged (RFC-007 D6): the purge
-- runs once a day. Unix seconds.
CREATE TABLE IF NOT EXISTS rule_purges (
    router_id TEXT PRIMARY KEY,
    last_at   INTEGER NOT NULL DEFAULT 0
);
