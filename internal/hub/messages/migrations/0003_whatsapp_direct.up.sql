-- Direct WhatsApp texts in the back-office's queue (issue #82, parent #68).
--
-- An answer to a complaint, and later (issue #83) the admins' summary and
-- the replies to their commands, go to one number through the same queue
-- and the same 40 s pace (hub_whatsapp_pace) as the announcements, so the
-- Kergui number never sends faster than #81 decided. Such a row carries its
-- own text instead of an announcement: message_id stays null, and so may
-- tenant_id (an admin's number belongs to no household).
--
-- ref says what the text is about ("complaint-message:<id>"), so its
-- module can read back whether it went. optin_required is false only for a
-- number that wrote to Kergui first (a complaint that came in by WhatsApp,
-- an admin's command): everyone else must have opted in, as for an
-- announcement.
ALTER TABLE hub_whatsapp_queue ALTER COLUMN message_id DROP NOT NULL;
ALTER TABLE hub_whatsapp_queue ALTER COLUMN tenant_id DROP NOT NULL;
ALTER TABLE hub_whatsapp_queue ADD COLUMN IF NOT EXISTS text TEXT NOT NULL DEFAULT '';
ALTER TABLE hub_whatsapp_queue ADD COLUMN IF NOT EXISTS ref TEXT NOT NULL DEFAULT '';
ALTER TABLE hub_whatsapp_queue ADD COLUMN IF NOT EXISTS optin_required BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE hub_whatsapp_queue DROP CONSTRAINT IF EXISTS hub_whatsapp_queue_body_check;
ALTER TABLE hub_whatsapp_queue ADD CONSTRAINT hub_whatsapp_queue_body_check
    CHECK (message_id IS NOT NULL OR text <> '');

CREATE INDEX IF NOT EXISTS hub_whatsapp_queue_ref_idx ON hub_whatsapp_queue (ref) WHERE ref <> '';
