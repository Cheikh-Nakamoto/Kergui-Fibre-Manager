-- The action log (issue #77) records every admin gesture in audit_logs; the
-- hub's own WhatsApp session, linked and unlinked from the back-office, is
-- one of them. The reason and summary ride in the row's metadata.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'WHATSAPP_SESSION_LINK_REQUESTED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'WHATSAPP_SESSION_UNLINKED';

-- The log is read by kind and newest first.
CREATE INDEX IF NOT EXISTS idx_audit_logs_action_occurred ON audit_logs (action, occurred_at DESC, id DESC);
