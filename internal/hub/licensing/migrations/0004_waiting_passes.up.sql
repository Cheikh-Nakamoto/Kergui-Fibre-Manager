-- RFC-006 passes bought on the web page (decision of 2026-11-21). The
-- purchase page asks for a phone number, a tier and a duration, not a box
-- serial: such a payment has no box (NULL) and, once credited, waits on the
-- number's account. The app's first box binding applies it (BindBox), and
-- its days start then. A Play purchase always names its box.
ALTER TABLE hub_licensing_payments ALTER COLUMN box_id DROP NOT NULL;

CREATE INDEX IF NOT EXISTS hub_licensing_payments_waiting_idx
    ON hub_licensing_payments (account_id) WHERE box_id IS NULL AND status = 'credited';
