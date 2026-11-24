-- Issue #87: the owner's phones. When the owner's phone sees itself on the
-- box, the app records the MAC it has there (Android's private MAC is stable
-- per Wi-Fi), so every phone of the household leaves it out of its Devices
-- tab. Each row names the owner key it was declared under: only the rows of
-- the key that signed the bundle in force are the owner's, so a recovery or
-- an ownership change (a new owner key) drops the old phone at once.
CREATE TABLE IF NOT EXISTS hub_inventory_owner_devices (
    tenant_id   UUID        NOT NULL,
    mac         TEXT        NOT NULL,
    owner_key   TEXT        NOT NULL,
    declared_by TEXT        NOT NULL DEFAULT '',
    declared_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, mac)
);
