-- Disconnecting a bank account removes the Item at Plaid and marks this row
-- instead of deleting it, so the item_id stays resolvable: cached transactions
-- and plaid_accounts rows are keyed by it and outlive the disconnect, and Plaid
-- can still deliver webhooks for an Item it has removed.
--
-- Every read filters deleted_at IS NULL, so a marked row is invisible to the
-- application. The access token is kept as stored; /item/remove has already
-- invalidated it.

ALTER TABLE plaid_items ADD COLUMN deleted_at TEXT;
