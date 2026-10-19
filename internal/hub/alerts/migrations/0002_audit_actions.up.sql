-- The audit actions this module records (see hub_agents/0002 for why).
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'WHATSAPP_INVITED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'WHATSAPP_LINKED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'WHATSAPP_STOPPED';
