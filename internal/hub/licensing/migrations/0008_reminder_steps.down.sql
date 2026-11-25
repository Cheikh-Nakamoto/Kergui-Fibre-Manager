ALTER TABLE hub_licensing_subscriptions ADD COLUMN IF NOT EXISTS reminder_sent_for TIMESTAMPTZ;
UPDATE hub_licensing_subscriptions s SET reminder_sent_for = s.expires_at
WHERE EXISTS (SELECT 1 FROM hub_licensing_reminders r
              WHERE r.account_id = s.account_id AND r.box_id = s.box_id AND r.expires_at = s.expires_at AND r.step = 'j3');

DELETE FROM hub_licensing_reminders WHERE step <> 'j3';
ALTER TABLE hub_licensing_reminders DROP CONSTRAINT IF EXISTS hub_licensing_reminders_pkey;
ALTER TABLE hub_licensing_reminders ADD PRIMARY KEY (account_id, box_id, expires_at);
ALTER TABLE hub_licensing_reminders
    DROP COLUMN IF EXISTS note,
    DROP COLUMN IF EXISTS push_sent,
    DROP COLUMN IF EXISTS push_phones,
    DROP COLUMN IF EXISTS push,
    DROP COLUMN IF EXISTS whatsapp_tries,
    DROP COLUMN IF EXISTS whatsapp,
    DROP COLUMN IF EXISTS step;
