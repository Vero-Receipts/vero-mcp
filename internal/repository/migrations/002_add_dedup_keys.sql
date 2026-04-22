-- 002_add_dedup_keys.sql - Two-tier receipt deduplication

ALTER TABLE receipts ADD COLUMN content_hash     TEXT;
ALTER TABLE receipts ADD COLUMN gmail_message_id TEXT;
ALTER TABLE receipts ADD COLUMN order_id         TEXT;
ALTER TABLE receipts ADD COLUMN merchant_key     TEXT;
ALTER TABLE receipts ADD COLUMN duplicate_of     TEXT REFERENCES receipts(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_receipts_gmail_msg
    ON receipts (user_id, gmail_message_id)
 WHERE gmail_message_id IS NOT NULL
   AND duplicate_of IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_receipts_content_hash
    ON receipts (user_id, content_hash)
 WHERE content_hash IS NOT NULL
   AND duplicate_of IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_receipts_order_id
    ON receipts (user_id, order_id)
 WHERE order_id IS NOT NULL
   AND duplicate_of IS NULL;

CREATE INDEX IF NOT EXISTS idx_receipts_softdup
    ON receipts (user_id, merchant_key, total, date)
 WHERE merchant_key IS NOT NULL
   AND total IS NOT NULL
   AND date IS NOT NULL
   AND duplicate_of IS NULL;

CREATE INDEX IF NOT EXISTS idx_receipts_duplicate_of
    ON receipts (duplicate_of)
 WHERE duplicate_of IS NOT NULL;
