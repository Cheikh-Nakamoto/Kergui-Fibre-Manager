-- Lifetime passes (issue #98): a pass with no end, granted from the
-- back-office and revocable there. expires_at holds a far date (9999-12-31,
-- domain.LifetimeExpiresAt) so every « expires_at > now » reader keeps
-- seeing a running pass; lifetime says it has no end, for what must not
-- treat it as a dated pass (the expiry reminder, the expiring list, the
-- renewal cohorts). pre_lifetime_* is the pass that ran before the grant,
-- back when the lifetime pass is revoked.
ALTER TABLE hub_licensing_subscriptions
    ADD COLUMN IF NOT EXISTS lifetime                BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS pre_lifetime_tier       TEXT        CHECK (pre_lifetime_tier IN ('free', 'basic', 'max')),
    ADD COLUMN IF NOT EXISTS pre_lifetime_expires_at TIMESTAMPTZ;

-- A lifetime grant moves expires_at to 9999-12-31: that is no renewal of the
-- end date the pass had, and counting it would put a « renewed until 9999 »
-- in the month's cohort. The ledger skips it.
DROP TRIGGER IF EXISTS hub_licensing_subscriptions_renewal ON hub_licensing_subscriptions;
CREATE TRIGGER hub_licensing_subscriptions_renewal
    AFTER UPDATE OF expires_at ON hub_licensing_subscriptions
    FOR EACH ROW
    WHEN (OLD.tier <> 'free' AND NEW.tier <> 'free' AND NEW.expires_at > OLD.expires_at AND NOT NEW.lifetime)
    EXECUTE FUNCTION hub_licensing_record_renewal();
