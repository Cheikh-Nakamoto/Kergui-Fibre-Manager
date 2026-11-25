-- The household page's pass gestures (issue #98) go to the action log: a
-- pass renewed by hand, a lifetime pass granted, and one revoked.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PASS_RENEWED_BY_ADMIN';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PASS_LIFETIME_GRANTED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PASS_LIFETIME_REVOKED';
