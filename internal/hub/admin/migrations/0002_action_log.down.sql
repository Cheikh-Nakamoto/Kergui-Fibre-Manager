-- Postgres cannot drop an enum value; the actions stay, unused.
DROP INDEX IF EXISTS idx_audit_logs_action_occurred;
