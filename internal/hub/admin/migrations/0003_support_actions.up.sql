-- The « Support » page's gestures (issue #78) go to the action log: a pass
-- extended, a pass moved by an admin past the customer's limit, and an
-- account's sessions revoked. Each carries its reason in the metadata.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PASS_EXTENDED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PASS_TRANSFERRED_BY_ADMIN';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'SESSIONS_REVOKED';
