-- RFC-006 web trial (decision of 2026-11-21). The 7-day Max trial is asked
-- for on the hub's web page with a phone number alone, before any box is
-- known: the row waits on the number's account with no box (pending) and is
-- bound to a box serial when the app first signs in on one. The 7 days start
-- at that binding, so started_at is set with box_id. UNIQUE (phone) still
-- gives one trial per number and UNIQUE (box_id), which ignores NULLs, one
-- per box.
ALTER TABLE hub_licensing_trials ADD COLUMN IF NOT EXISTS requested_at TIMESTAMPTZ;
UPDATE hub_licensing_trials SET requested_at = started_at WHERE requested_at IS NULL;
ALTER TABLE hub_licensing_trials ALTER COLUMN requested_at SET NOT NULL;
ALTER TABLE hub_licensing_trials ALTER COLUMN box_id DROP NOT NULL;
ALTER TABLE hub_licensing_trials ALTER COLUMN started_at DROP NOT NULL;
ALTER TABLE hub_licensing_trials DROP CONSTRAINT IF EXISTS hub_licensing_trials_bound;
ALTER TABLE hub_licensing_trials ADD CONSTRAINT hub_licensing_trials_bound
    CHECK ((box_id IS NULL) = (started_at IS NULL));

CREATE INDEX IF NOT EXISTS hub_licensing_trials_pending_idx
    ON hub_licensing_trials (account_id) WHERE box_id IS NULL;
