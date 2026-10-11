-- "Pay later" on the portal: a device with a pending payment link sees the
-- payment page until it pays, or until it postpones (per link) for a while.
CREATE TABLE payment_snoozes (
    router_id  TEXT NOT NULL,
    payment_id TEXT NOT NULL,
    until      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (router_id, payment_id)
);
