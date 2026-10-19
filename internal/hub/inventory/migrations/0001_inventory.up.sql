-- The hub's reference inventory: the household's devices, the addresses bound
-- to each, and the windows that group new arrivals into one alert.

-- One row per household, set when its first snapshot has been absorbed.
-- Before that there is nothing to compare against, so nothing is new.
CREATE TABLE IF NOT EXISTS hub_inventory_baseline (
    tenant_id  UUID PRIMARY KEY,
    set_at     TIMESTAMPTZ NOT NULL,
    -- device_seq numbers the household's devices (DEVICE-000042). It lives
    -- here rather than being derived from a count, so a device the owner
    -- deletes never hands its number to another one.
    device_seq INTEGER     NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS hub_inventory_devices (
    tenant_id  UUID        NOT NULL,
    device_id  TEXT        NOT NULL,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, device_id)
);

-- last_obs is the observation exactly as the relay signed it. Recognition
-- re-reads it on the next poll, so a MAC that rotates is compared with what
-- the box actually said, not with a summary the hub made of it.
CREATE TABLE IF NOT EXISTS hub_inventory_macs (
    tenant_id  UUID        NOT NULL,
    mac        TEXT        NOT NULL,
    device_id  TEXT        NOT NULL,
    state      TEXT        NOT NULL,
    hostname   TEXT        NOT NULL DEFAULT '',
    ip         TEXT        NOT NULL DEFAULT '',
    ssid       TEXT        NOT NULL DEFAULT '',
    last_obs   JSONB,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, mac)
);

CREATE INDEX IF NOT EXISTS hub_inventory_macs_device_idx
    ON hub_inventory_macs (tenant_id, device_id);

-- A window collects the devices that arrive within two minutes of each other.
-- announced_at is set in the same transaction as the outbox event, so a
-- household is told once and only once.
CREATE TABLE IF NOT EXISTS hub_inventory_windows (
    id           UUID PRIMARY KEY,
    tenant_id    UUID        NOT NULL,
    opened_at    TIMESTAMPTZ NOT NULL,
    closes_at    TIMESTAMPTZ NOT NULL,
    items        JSONB       NOT NULL,
    announced_at TIMESTAMPTZ
);

-- One open window per household at a time: the diff extends it rather than
-- opening a second one, which is what makes the grouping work.
CREATE UNIQUE INDEX IF NOT EXISTS hub_inventory_windows_open_idx
    ON hub_inventory_windows (tenant_id) WHERE announced_at IS NULL;

-- The sweep looks for windows whose time is up and that nobody announced.
CREATE INDEX IF NOT EXISTS hub_inventory_windows_due_idx
    ON hub_inventory_windows (closes_at) WHERE announced_at IS NULL;
