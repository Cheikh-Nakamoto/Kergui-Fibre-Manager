DROP INDEX IF EXISTS hub_complaints_unsummarized_idx;
ALTER TABLE hub_complaints DROP COLUMN IF EXISTS summarized_at;
DELETE FROM hub_settings WHERE key = 'complaint_summary';
