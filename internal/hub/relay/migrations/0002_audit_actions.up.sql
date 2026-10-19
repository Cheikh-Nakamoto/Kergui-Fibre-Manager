-- The audit actions this module records (see hub_agents/0002 for why).
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'ROUTER_COMMAND_QUEUED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'ROUTER_COMMAND_DONE';
