-- Whether a relay enforces the household's auto-reject rule (#36), as its
-- last heartbeat said. The phones stand down while the lease holder does.
ALTER TABLE hub_relay_presence ADD COLUMN IF NOT EXISTS auto_reject BOOLEAN NOT NULL DEFAULT false;
