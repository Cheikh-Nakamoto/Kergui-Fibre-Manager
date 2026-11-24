-- The admins' WhatsApp summary (issue #83): « 3 nouvelles plaintes (2
-- paiement, 1 box)… », at most once per interval. summarized_at marks a
-- complaint already counted in a summary; the summary claims the unmarked
-- ones in the same transaction that moves its own clock (the hub setting
-- complaint_summary), so a summary is never sent twice for one complaint,
-- even by two hubs at once. Complaints older than this migration count as
-- told: the first summary is not the whole history.
ALTER TABLE hub_complaints ADD COLUMN IF NOT EXISTS summarized_at TIMESTAMPTZ;
UPDATE hub_complaints SET summarized_at = created_at WHERE summarized_at IS NULL;

CREATE INDEX IF NOT EXISTS hub_complaints_unsummarized_idx ON hub_complaints (created_at) WHERE summarized_at IS NULL;
