-- « Supprimer mon compte » in the app (#37): the customer's own deletion of
-- the account goes to the action log, so support sees why it is gone.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'ACCOUNT_DELETED';
