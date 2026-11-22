-- The audit action this module adds (see hub_agents/0002 for why): the
-- owner changing a member's role (issue #42). The invite, the acceptance
-- and the removal reuse the kernel's MEMBER_INVITED / _ACCEPTED / _REVOKED.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'MEMBER_ROLE_CHANGED';
