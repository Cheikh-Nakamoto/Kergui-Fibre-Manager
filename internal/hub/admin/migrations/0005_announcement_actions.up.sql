-- « Messages » (issue #80) goes to the action log: an announcement sent from
-- the back-office, its audience and counts in the metadata.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'ANNOUNCEMENT_SENT';
