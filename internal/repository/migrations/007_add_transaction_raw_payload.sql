-- 007_add_transaction_raw_payload.sql
-- Complete raw Plaid transaction payload per row (audit + future field extraction).
-- Stored as TEXT (JSON), matching this DB's convention. Written on sync.
ALTER TABLE transaction_cache ADD COLUMN raw_payload TEXT;
