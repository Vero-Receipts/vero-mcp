-- 004_add_merchant_location.sql
-- Store the full Plaid transaction `location` object on the merchant.
ALTER TABLE merchants ADD COLUMN location TEXT;
