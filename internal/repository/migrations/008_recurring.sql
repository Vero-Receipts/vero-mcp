-- 008_recurring.sql
-- Recurring-transaction support:
--   * receipts.is_subscription    — subscription flag from OCR (NULL = not yet evaluated)
--   * transaction_cache.recurring  — marks a transaction as part of a recurring series
--     (drives the badge; set independently of whether a receipt is attached)
--   * drop receipt_matches.receipt_id UNIQUE so one receipt can back many transactions
--     (a recurring series' source receipt is carried forward to its later charges).
--
-- SQLite cannot drop a column-level constraint in place, so receipt_matches is rebuilt.
-- Existing rows are copied first, so no match data is lost when an already-populated
-- database upgrades. Only the OLD (now-copied-from) table is dropped.
ALTER TABLE receipts ADD COLUMN is_subscription INTEGER;
ALTER TABLE transaction_cache ADD COLUMN recurring INTEGER NOT NULL DEFAULT 0;

CREATE TABLE receipt_matches_new (
    id               TEXT PRIMARY KEY,
    receipt_id       TEXT REFERENCES receipts(id) ON DELETE CASCADE,
    transaction_id   TEXT,
    account_id       TEXT,
    confidence_score REAL,
    match_method     TEXT,
    match_reason     TEXT,
    matched_at       TEXT DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO receipt_matches_new (id, receipt_id, transaction_id, account_id, confidence_score, match_method, match_reason, matched_at)
    SELECT id, receipt_id, transaction_id, account_id, confidence_score, match_method, match_reason, matched_at
    FROM receipt_matches;
DROP TABLE receipt_matches;
ALTER TABLE receipt_matches_new RENAME TO receipt_matches;
CREATE INDEX IF NOT EXISTS idx_receipt_matches_transaction_id ON receipt_matches(transaction_id);
CREATE INDEX IF NOT EXISTS idx_receipt_matches_receipt_id ON receipt_matches(receipt_id);
