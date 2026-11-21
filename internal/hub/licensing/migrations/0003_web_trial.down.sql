DROP INDEX IF EXISTS hub_licensing_trials_pending_idx;
ALTER TABLE hub_licensing_trials DROP CONSTRAINT IF EXISTS hub_licensing_trials_bound;
-- A trial never bound to a box cannot be kept in the old shape.
DELETE FROM hub_licensing_trials WHERE box_id IS NULL;
ALTER TABLE hub_licensing_trials ALTER COLUMN started_at SET NOT NULL;
ALTER TABLE hub_licensing_trials ALTER COLUMN box_id SET NOT NULL;
ALTER TABLE hub_licensing_trials DROP COLUMN IF EXISTS requested_at;
