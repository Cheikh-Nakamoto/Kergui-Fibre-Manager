-- The audit actions this module records.
--
-- audit_action is a Postgres enum owned by the kernel's audit module, and it
-- lists the kernel's own six actions. Every action a custom module records
-- has to be added to it, or the audit consumer fails to insert the row and
-- the outbox retries it ten times: H1's "who paired a relay" never reached
-- audit_logs, and the failures crowded the dispatcher that carries the
-- households' alerts.
--
-- Adding a value is additive and reversible only in principle: Postgres
-- cannot remove an enum value, which is why the down migration is empty.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'AGENT_PAIRED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'AGENT_REVOKED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'TRUST_BUNDLE_UPDATED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'CREDENTIALS_SEALED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'NETWORK_UPDATED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PAIRING_CODE_ISSUED';
