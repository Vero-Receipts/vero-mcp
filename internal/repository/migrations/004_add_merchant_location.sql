-- 004_add_merchant_location.sql
-- Store the full Plaid transaction `location` object on the merchant. We
-- persist it as a JSON string, so a TEXT column is the natural fit here (and
-- matches how `category` is already stored). The plaid-wrapper Postgres schema
-- uses JSONB for the same data; the repo code reads/writes it as a JSON string
-- in both, so the two schemas stay compatible.
ALTER TABLE merchants ADD COLUMN location TEXT;
