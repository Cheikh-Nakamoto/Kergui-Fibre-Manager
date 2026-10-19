-- The kernel makes tenants.name unique platform-wide: a second household
-- called "Maison Diallo" would fail its signup with a 500. Households are
-- told apart by id and slug (still unique), never by display name.
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_name_key;
