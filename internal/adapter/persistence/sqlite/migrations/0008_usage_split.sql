-- Consumption split by direction, from the device's point of view: sent is
-- its upload (the router's Rx), received its download (Tx). Months recorded
-- before this keep their total in bytes and zero here.
ALTER TABLE usage ADD COLUMN sent INTEGER NOT NULL DEFAULT 0;
ALTER TABLE usage ADD COLUMN received INTEGER NOT NULL DEFAULT 0;
