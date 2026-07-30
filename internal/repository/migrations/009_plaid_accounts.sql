-- 009_plaid_accounts.sql – accounts (with card mask) per Plaid Item, from /accounts/get.
-- mask is the last 2-4 digits, used to match a user transaction to a Square payment.

CREATE TABLE IF NOT EXISTS plaid_accounts (
    id            TEXT PRIMARY KEY,
    account_id    TEXT UNIQUE NOT NULL,        -- Plaid account id; = transaction_cache.account_id
    item_id       TEXT NOT NULL,
    user_id       TEXT REFERENCES users(id),
    mask          TEXT,
    name          TEXT,
    official_name TEXT,
    subtype       TEXT,
    type          TEXT,
    created_at    TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at    TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_plaid_accounts_user_id ON plaid_accounts(user_id);
CREATE INDEX IF NOT EXISTS idx_plaid_accounts_mask ON plaid_accounts(mask);
