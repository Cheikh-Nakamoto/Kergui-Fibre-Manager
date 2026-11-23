-- Issue #58: a device nobody approved alerts on every arrival, not only the
-- first time it is seen. "present" is whether the last snapshot showed the
-- address on the Wi-Fi; existing rows start present, so the upgrade itself
-- alerts nobody.
ALTER TABLE hub_inventory_macs ADD COLUMN IF NOT EXISTS present BOOLEAN NOT NULL DEFAULT TRUE;

-- The household's approved addresses: « Approuver » on an alert or a device
-- sheet, or a name given in the app. Kept apart from hub_inventory_macs so an
-- address can be approved before the hub has seen it (the phone syncs the
-- names it already had), and so a rewrite of the record never drops it.
CREATE TABLE IF NOT EXISTS hub_inventory_approvals (
    tenant_id   UUID        NOT NULL,
    mac         TEXT        NOT NULL,
    approved_by TEXT        NOT NULL DEFAULT '',
    approved_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, mac)
);
