-- The add window's start (RFC-007 D4, G4): « Encore 5 min » never takes a
-- window past 15 minutes from when it opened. Unix seconds, 0 for none.
ALTER TABLE wifi_lock ADD COLUMN window_start INTEGER NOT NULL DEFAULT 0;
