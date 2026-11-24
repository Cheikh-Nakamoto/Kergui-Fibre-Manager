-- Complaints (issue #82, parent #68) go to the action log: an answer sent
-- from the back-office, a complaint closed, and the admins' WhatsApp
-- numbers changed.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'COMPLAINT_ANSWERED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'COMPLAINT_CLOSED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'COMPLAINT_ADMINS_SET';
