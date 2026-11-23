-- The back-office's release actions (issue #71), kept in the admin audit
-- like the pass grant: who published, withdrew, rewrote the notes or moved
-- the minimum.
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'RELEASE_PUBLISHED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'RELEASE_WITHDRAWN';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'RELEASE_NOTES_EDITED';
ALTER TYPE audit_action ADD VALUE IF NOT EXISTS 'RELEASE_MINIMUM_SET';
