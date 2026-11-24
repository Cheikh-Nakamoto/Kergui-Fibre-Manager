DROP TABLE IF EXISTS hub_whatsapp_pace;
DROP TABLE IF EXISTS hub_whatsapp_queue;
DELETE FROM hub_messages WHERE audience = 'numbers';
ALTER TABLE hub_messages DROP CONSTRAINT IF EXISTS hub_messages_audience_check;
ALTER TABLE hub_messages ADD CONSTRAINT hub_messages_audience_check
    CHECK (audience IN ('all', 'tier', 'household'));
