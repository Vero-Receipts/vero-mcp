-- 003_normalize_merchants.sql

CREATE TABLE IF NOT EXISTS merchants (
    id               TEXT PRIMARY KEY,
    canonical_name   TEXT NOT NULL,
    normalized_key   TEXT NOT NULL UNIQUE,
    plaid_entity_id  TEXT UNIQUE,
    domain           TEXT,
    logo_cdn_url     TEXT,
    created_at       TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);


-- Backfill merchants from existing transaction_cache rows.
-- SQLite lacks gen_random_uuid(); hex(randomblob(...)) yields a UUIDv4-shaped string.
INSERT INTO merchants (id, canonical_name, normalized_key, logo_cdn_url)
SELECT
    lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)),2) || '-a' || substr(hex(randomblob(2)),2) || '-' || hex(randomblob(6))),
    MAX(merchant_name) AS canonical_name,
    LOWER(TRIM(merchant_name)) AS normalized_key,
    MAX(merchant_logo) AS logo_cdn_url
FROM transaction_cache
WHERE merchant_name IS NOT NULL AND TRIM(merchant_name) <> ''
GROUP BY LOWER(TRIM(merchant_name))
ON CONFLICT (normalized_key) DO NOTHING;

-- SQLite can't add FK constraints via ALTER; logical FK only.
ALTER TABLE transaction_cache ADD COLUMN merchant_id TEXT;

UPDATE transaction_cache
SET merchant_id = (
    SELECT id FROM merchants
    WHERE normalized_key = LOWER(TRIM(transaction_cache.merchant_name))
)
WHERE merchant_id IS NULL
  AND merchant_name IS NOT NULL
  AND TRIM(merchant_name) <> '';

CREATE INDEX IF NOT EXISTS idx_transaction_cache_merchant_id ON transaction_cache(merchant_id);

ALTER TABLE transaction_cache DROP COLUMN merchant_name;
ALTER TABLE transaction_cache DROP COLUMN merchant_logo;
