-- Renewal reminders at J-3 and J0, by WhatsApp and in the app (issue #99).
-- hub_licensing_reminders was one row per end date (the J-3 WhatsApp
-- message); it becomes one row per end date and step, with what each
-- channel did, for the back-office's « rappels envoyés » column:
--   step            j3 (three days before) or j0 (the day it ends);
--   whatsapp        sending, sent, failed or skipped; a failed message is
--                   tried again, whatsapp_tries times at most;
--   push            the app notification: sending, sent, failed, none (no
--                   phone of the household has the app), off (the hub has no
--                   push sender) or skipped;
--   push_phones / push_sent  how many phones were tried and reached;
--   note            why a reminder was skipped (a converted trial, a pass
--                   bought and waiting for its box).
-- The rows already there are the J-3 WhatsApp messages that went out.
ALTER TABLE hub_licensing_reminders
    ADD COLUMN IF NOT EXISTS step           TEXT NOT NULL DEFAULT 'j3' CHECK (step IN ('j3', 'j0')),
    ADD COLUMN IF NOT EXISTS whatsapp       TEXT NOT NULL DEFAULT 'sent'
        CHECK (whatsapp IN ('sending', 'sent', 'failed', 'skipped')),
    ADD COLUMN IF NOT EXISTS whatsapp_tries INT  NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS push           TEXT NOT NULL DEFAULT 'off'
        CHECK (push IN ('sending', 'sent', 'failed', 'none', 'off', 'skipped')),
    ADD COLUMN IF NOT EXISTS push_phones    INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS push_sent      INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS note           TEXT NOT NULL DEFAULT '';

ALTER TABLE hub_licensing_reminders DROP CONSTRAINT IF EXISTS hub_licensing_reminders_pkey;
ALTER TABLE hub_licensing_reminders ADD PRIMARY KEY (account_id, box_id, expires_at, step);

-- The log is now what says a reminder went out: the subscription's own
-- reminder_sent_for, which could hold one step only, goes. Every value it
-- held has its row in the log (the claim wrote both in one transaction).
ALTER TABLE hub_licensing_subscriptions DROP COLUMN IF EXISTS reminder_sent_for;
