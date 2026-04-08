-- 001_initial.sql – Full schema for vero-mcp (SQLite / WAL mode)

CREATE TABLE IF NOT EXISTS users (
    id                TEXT PRIMARY KEY,
    name              TEXT,
    is_bank_connected INTEGER DEFAULT 0,
    created_at        TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at        TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS plaid_items (
    id           TEXT PRIMARY KEY,
    user_id      TEXT REFERENCES users(id),
    item_id      TEXT UNIQUE,
    access_token TEXT,
    sync_cursor  TEXT,
    created_at   TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at   TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_plaid_items_user_id ON plaid_items(user_id);

CREATE TABLE IF NOT EXISTS receipts (
    id               TEXT PRIMARY KEY,
    user_id          TEXT REFERENCES users(id),
    image_url        TEXT,
    image_path       TEXT,
    thumbnail_url    TEXT,
    merchant_name    TEXT,
    merchant_address TEXT,
    total            REAL,
    currency         TEXT,
    total_usd        REAL,
    subtotal         REAL,
    tax              REAL,
    tip              REAL,
    payment_method   TEXT,
    last_four_digits TEXT,
    date             TEXT,
    transaction_time TEXT,
    raw_text         TEXT,
    ocr_error        TEXT,
    line_items       TEXT DEFAULT '[]',
    source           TEXT,
    status           TEXT,
    created_at       TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at       TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_receipts_user_id ON receipts(user_id);
CREATE INDEX IF NOT EXISTS idx_receipts_status  ON receipts(status);
CREATE INDEX IF NOT EXISTS idx_receipts_date    ON receipts(date);

CREATE TABLE IF NOT EXISTS receipt_matches (
    id               TEXT PRIMARY KEY,
    receipt_id       TEXT UNIQUE REFERENCES receipts(id) ON DELETE CASCADE,
    transaction_id   TEXT,
    account_id       TEXT,
    confidence_score REAL,
    match_method     TEXT,
    match_reason     TEXT,
    matched_at       TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_receipt_matches_transaction_id ON receipt_matches(transaction_id);

CREATE TABLE IF NOT EXISTS transaction_cache (
    id                     TEXT PRIMARY KEY,
    user_id                TEXT REFERENCES users(id),
    transaction_id         TEXT UNIQUE,
    account_id             TEXT,
    amount                 REAL,
    date                   TEXT,
    datetime               TEXT,
    name                   TEXT,
    merchant_name          TEXT,
    category               TEXT DEFAULT '[]',
    pfc_primary            TEXT,
    pfc_detailed           TEXT,
    payment_channel        TEXT,
    pending                INTEGER DEFAULT 0,
    merchant_logo          TEXT,
    synced_at              TEXT,
    corrected_pfc_primary  TEXT,
    corrected_pfc_detailed TEXT,
    category_corrected_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_transaction_cache_user_id        ON transaction_cache(user_id);
CREATE INDEX IF NOT EXISTS idx_transaction_cache_transaction_id ON transaction_cache(transaction_id);
CREATE INDEX IF NOT EXISTS idx_transaction_cache_date           ON transaction_cache(date);
CREATE INDEX IF NOT EXISTS idx_transaction_cache_amount         ON transaction_cache(amount);

CREATE TABLE IF NOT EXISTS merchant_aliases (
    id         TEXT PRIMARY KEY,
    canonical  TEXT,
    alias      TEXT,
    source     TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(canonical, alias)
);
CREATE INDEX IF NOT EXISTS idx_merchant_aliases_alias     ON merchant_aliases(alias);
CREATE INDEX IF NOT EXISTS idx_merchant_aliases_canonical ON merchant_aliases(canonical);

CREATE TABLE IF NOT EXISTS match_audit_log (
    id                   TEXT PRIMARY KEY,
    receipt_id           TEXT,
    transaction_id       TEXT,
    amount_score         REAL,
    date_score           REAL,
    merchant_score       REAL,
    composite_score      REAL,
    llm_used             INTEGER DEFAULT 0,
    llm_merchant_confirm INTEGER,
    outcome              TEXT,
    reason               TEXT,
    created_at           TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_match_audit_log_receipt_id ON match_audit_log(receipt_id);

CREATE TABLE IF NOT EXISTS receipt_items (
    id          TEXT PRIMARY KEY,
    receipt_id  TEXT REFERENCES receipts(id) ON DELETE CASCADE,
    user_id     TEXT,
    description TEXT,
    quantity    REAL,
    unit_price  REAL,
    price       REAL,
    sort_order  INTEGER DEFAULT 0,
    created_at  TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_receipt_items_receipt_id ON receipt_items(receipt_id);

CREATE TABLE IF NOT EXISTS category_corrections_cache (
    id                     TEXT PRIMARY KEY,
    merchant_canonical     TEXT,
    original_pfc_primary   TEXT,
    original_pfc_detailed  TEXT,
    corrected_pfc_primary  TEXT,
    corrected_pfc_detailed TEXT,
    source                 TEXT,
    sample_line_items      TEXT,
    created_at             TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(merchant_canonical, original_pfc_detailed)
);
CREATE INDEX IF NOT EXISTS idx_category_corrections_cache_lookup
    ON category_corrections_cache(merchant_canonical, original_pfc_detailed);

CREATE TABLE IF NOT EXISTS notes (
    id          TEXT PRIMARY KEY,
    user_id     TEXT,
    entity_type TEXT,
    entity_id   TEXT,
    content     TEXT,
    created_at  TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_notes_entity ON notes(user_id, entity_type, entity_id);

CREATE TABLE IF NOT EXISTS labels (
    id         TEXT PRIMARY KEY,
    user_id    TEXT,
    name       TEXT,
    color      TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_labels_user_id ON labels(user_id);

CREATE TABLE IF NOT EXISTS label_assignments (
    id          TEXT PRIMARY KEY,
    label_id    TEXT REFERENCES labels(id) ON DELETE CASCADE,
    user_id     TEXT,
    entity_type TEXT,
    entity_id   TEXT,
    created_at  TEXT DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(label_id, entity_type, entity_id)
);
CREATE INDEX IF NOT EXISTS idx_label_assignments_entity ON label_assignments(user_id, entity_type, entity_id);
