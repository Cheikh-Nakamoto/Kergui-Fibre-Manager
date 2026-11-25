-- Whether a relay yielded the box to the owner's own session at home (#94),
-- as its last heartbeat said. It keeps the lease and is heard; the app says
-- "paused", not "offline".
ALTER TABLE hub_relay_presence ADD COLUMN IF NOT EXISTS paused BOOLEAN NOT NULL DEFAULT false;
