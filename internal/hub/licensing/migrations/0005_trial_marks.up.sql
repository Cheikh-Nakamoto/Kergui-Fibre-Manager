-- A deleted account forgets its number and box, but not that they had the
-- 7-day trial (owner decision, 2026-11-24): one keyed digest (HMAC-SHA256
-- with a key derived from the hub's secret) per number and per box, and
-- nothing else, so a re-created account is refused a second trial.
CREATE TABLE IF NOT EXISTS hub_licensing_trial_marks (
    kind       TEXT        NOT NULL CHECK (kind IN ('phone', 'box')),
    digest     BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, digest)
);
