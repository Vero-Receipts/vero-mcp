-- The effective transaction category lives in pfc_primary / pfc_detailed, which
-- is what clients render, filter and aggregate on. Plaid's own assignment moves
-- to plaid_pfc_primary / plaid_pfc_detailed, refreshed on every sync.
--
-- A row is corrected exactly when the two pairs differ, so Plaid's view is never
-- lost and a correction is always reversible. Corrections previously went to
-- corrected_pfc_*, which nothing read; those columns are retired rather than
-- migrated, since the categories they hold were largely not real Plaid values.

ALTER TABLE transaction_cache ADD COLUMN plaid_pfc_primary TEXT;
ALTER TABLE transaction_cache ADD COLUMN plaid_pfc_detailed TEXT;

UPDATE transaction_cache
   SET plaid_pfc_primary  = pfc_primary,
       plaid_pfc_detailed = pfc_detailed;

-- Stage 1 of the receipt cascade answers from this cache without consulting the
-- model, so pre-existing entries would keep being applied and the corrected call
-- would never run.
DELETE FROM category_corrections_cache;
