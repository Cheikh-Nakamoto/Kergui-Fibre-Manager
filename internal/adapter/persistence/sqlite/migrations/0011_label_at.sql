-- When the household last set a device's name or notes (#95), Unix
-- milliseconds on the clock of the phone or PC that made the edit; 0 when
-- never, or before this column existed. The owner's phone and the relay keep
-- their own copies of the labels: when they meet, the later edit wins.
ALTER TABLE devices ADD COLUMN label_at INTEGER NOT NULL DEFAULT 0;
