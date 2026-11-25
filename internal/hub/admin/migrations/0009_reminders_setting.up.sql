-- The renewal reminders' switch (issue #99) goes to the action log.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'RENEWAL_REMINDERS_SET';
