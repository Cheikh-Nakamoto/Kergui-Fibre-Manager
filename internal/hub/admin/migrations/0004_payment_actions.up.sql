-- The « Paiements » page's gesture (issue #79) goes to the action log: a
-- payment re-checked at NabooPay, with its optional reason in the metadata.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'PAYMENT_RECHECKED';
