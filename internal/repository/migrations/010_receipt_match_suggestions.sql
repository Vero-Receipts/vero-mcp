-- Receipt-match suggestions (SQLite mirror of plaid-wrapper's 000062).
--
-- Suggestions used to live in receipt_matches, which made them exclusive on both
-- sides and let a guess reserve a charge away from the right receipt. They are
-- proposals now: several per receipt, several per transaction, and a rejection is
-- remembered so the matcher never raises the same pair twice.

CREATE TABLE IF NOT EXISTS receipt_match_suggestions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    receipt_id      TEXT NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
    transaction_id  TEXT NOT NULL,
    account_id      TEXT,

    -- NULL means the receipt carried no value for that dimension, which is a
    -- different fact from a score of 0 (it disagreed). The decision rule turns
    -- on the difference.
    amount_score    REAL,
    date_score      REAL,
    merchant_score  REAL,
    composite_score REAL NOT NULL,

    amount_diff_pct REAL,
    date_diff_days  INTEGER,
    merchant_method TEXT,

    flag            TEXT NOT NULL DEFAULT 'clean',
    reason          TEXT,
    rank            INTEGER NOT NULL DEFAULT 1,
    llm_used        INTEGER NOT NULL DEFAULT 0,

    created_at      TEXT DEFAULT CURRENT_TIMESTAMP,
    rejected_at     TEXT,

    UNIQUE (receipt_id, transaction_id)
);

-- Deliberately no unique index on transaction_id — a proposal is not a reservation.
CREATE INDEX IF NOT EXISTS idx_rms_receipt  ON receipt_match_suggestions (receipt_id, rank);
CREATE INDEX IF NOT EXISTS idx_rms_txn      ON receipt_match_suggestions (transaction_id);
CREATE INDEX IF NOT EXISTS idx_rms_user     ON receipt_match_suggestions (user_id);
CREATE INDEX IF NOT EXISTS idx_rms_rejected ON receipt_match_suggestions (receipt_id, transaction_id, rejected_at);

INSERT OR IGNORE INTO receipt_match_suggestions
    (id, user_id, receipt_id, transaction_id, account_id, composite_score, flag, reason, rank)
SELECT lower(hex(randomblob(16))), r.user_id, rm.receipt_id, rm.transaction_id, rm.account_id,
       COALESCE(rm.confidence_score, 0.70), 'clean', rm.match_reason, 1
FROM receipt_matches rm
JOIN receipts r ON r.id = rm.receipt_id
WHERE rm.match_method = 'suggested';

DELETE FROM receipt_matches WHERE match_method = 'suggested';

-- 'suggested' is retired as a stored receipt status; carrying proposals is now a
-- separate axis, queried through receipt_match_suggestions.
UPDATE receipts SET status = 'unmatched' WHERE status = 'suggested';
