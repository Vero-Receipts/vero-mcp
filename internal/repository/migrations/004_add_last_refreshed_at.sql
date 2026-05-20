-- 004_add_last_refreshed_at.sql – Per-Item refresh tracking
--
-- Adds a column that records the last time we forwarded a
-- /transactions/refresh request to Plaid for a given Item. The column is
-- always present in the schema, but the application only reads/writes it
-- when a refresh cooldown is enabled on PlaidService (see
-- pkg/service/plaid_service.go SetRefreshCooldown). With the default zero
-- cooldown — the typical open-source setup — the column stays NULL and has
-- no effect on behaviour.

ALTER TABLE plaid_items ADD COLUMN last_refreshed_at TEXT;
