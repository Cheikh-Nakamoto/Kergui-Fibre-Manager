-- Renewal tracking for the back-office (issue #97). A subscription keeps
-- only its current expiry, so once a pass is renewed nothing says it ever
-- had another end date, and « passes that came due this month and were
-- renewed » cannot be counted afterwards. This ledger keeps one row each
-- time a paid pass (Basic or Max, the trial included) gets a later expiry:
-- due_at is the end it had, renewed_at when it got more time. A renewal
-- before the end (early) and one after it (late) are both renewals of that
-- due date. The writers do not know about it: a trigger records every path
-- (a NabooPay credit, a Play renewal, a waiting pass bound, a support
-- extension, and whatever comes next) without the licensing code having to
-- remember. A transfer to another box is a new row on that box, not a
-- renewal; Play's end (an earlier expiry) is not one either.
CREATE TABLE IF NOT EXISTS hub_licensing_renewals (
    id             BIGSERIAL PRIMARY KEY,
    account_id     UUID        NOT NULL REFERENCES hub_licensing_accounts (id) ON DELETE CASCADE,
    box_id         TEXT        NOT NULL,
    tier           TEXT        NOT NULL,
    due_at         TIMESTAMPTZ NOT NULL,
    new_expires_at TIMESTAMPTZ NOT NULL,
    -- NULL for the rows backfilled below: renewed, but nobody wrote when.
    renewed_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS hub_licensing_renewals_due_idx ON hub_licensing_renewals (due_at);

CREATE OR REPLACE FUNCTION hub_licensing_record_renewal() RETURNS trigger AS $$
BEGIN
    INSERT INTO hub_licensing_renewals (account_id, box_id, tier, due_at, new_expires_at, renewed_at)
    VALUES (OLD.account_id, OLD.box_id, OLD.tier, OLD.expires_at, NEW.expires_at, now());
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS hub_licensing_subscriptions_renewal ON hub_licensing_subscriptions;
CREATE TRIGGER hub_licensing_subscriptions_renewal
    AFTER UPDATE OF expires_at ON hub_licensing_subscriptions
    FOR EACH ROW
    WHEN (OLD.tier <> 'free' AND NEW.tier <> 'free' AND NEW.expires_at > OLD.expires_at)
    EXECUTE FUNCTION hub_licensing_record_renewal();

-- When the ledger started: the back-office says the months before it are
-- incomplete. One row.
CREATE TABLE IF NOT EXISTS hub_licensing_renewals_since (
    one        BOOLEAN PRIMARY KEY DEFAULT true CHECK (one),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO hub_licensing_renewals_since DEFAULT VALUES ON CONFLICT DO NOTHING;

-- What the past can still tell: every expiry the WhatsApp reminder
-- announced (J-3) whose subscription now ends later was renewed. Passes
-- renewed before their reminder went out are not known.
INSERT INTO hub_licensing_renewals (account_id, box_id, tier, due_at, new_expires_at, renewed_at)
SELECT r.account_id, r.box_id, s.tier, r.expires_at, s.expires_at, NULL
FROM hub_licensing_reminders r
JOIN hub_licensing_subscriptions s ON s.account_id = r.account_id AND s.box_id = r.box_id
WHERE s.tier <> 'free' AND s.expires_at > r.expires_at
  AND NOT EXISTS (SELECT 1 FROM hub_licensing_renewals x
                  WHERE x.account_id = r.account_id AND x.box_id = r.box_id AND x.due_at = r.expires_at);
