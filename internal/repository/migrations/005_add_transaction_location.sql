-- 005_add_transaction_location.sql
-- Per-transaction location (the per-outlet discriminator). Stored as TEXT (JSON),
-- matching this DB's convention. The shared UpsertBatch now writes it; location was
-- removed from merchants (it is brand-grain and can't distinguish outlets). The old
-- merchants.location column (004) is left in place, now unused and harmless.
ALTER TABLE transaction_cache ADD COLUMN location TEXT;
