DROP INDEX IF EXISTS hub_licensing_payments_waiting_idx;
-- A payment never bound to a box cannot be kept in the old shape.
DELETE FROM hub_licensing_payments WHERE box_id IS NULL;
ALTER TABLE hub_licensing_payments ALTER COLUMN box_id SET NOT NULL;
