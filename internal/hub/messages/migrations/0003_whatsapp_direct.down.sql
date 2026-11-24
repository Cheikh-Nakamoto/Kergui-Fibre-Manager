DROP INDEX IF EXISTS hub_whatsapp_queue_ref_idx;
DELETE FROM hub_whatsapp_queue WHERE message_id IS NULL OR tenant_id IS NULL;
ALTER TABLE hub_whatsapp_queue DROP CONSTRAINT IF EXISTS hub_whatsapp_queue_body_check;
ALTER TABLE hub_whatsapp_queue DROP COLUMN IF EXISTS optin_required;
ALTER TABLE hub_whatsapp_queue DROP COLUMN IF EXISTS ref;
ALTER TABLE hub_whatsapp_queue DROP COLUMN IF EXISTS text;
ALTER TABLE hub_whatsapp_queue ALTER COLUMN tenant_id SET NOT NULL;
ALTER TABLE hub_whatsapp_queue ALTER COLUMN message_id SET NOT NULL;
