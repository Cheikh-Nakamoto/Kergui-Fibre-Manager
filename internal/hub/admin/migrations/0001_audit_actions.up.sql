-- The audit action the back-office adds (issue #62): a platform
-- administrator granting a pass by hand (support).
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PASS_GRANTED_BY_SUPPORT';
