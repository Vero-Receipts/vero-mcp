-- 006_add_receipt_city_state.sql
-- Receipt-derived per-outlet location: city/state parsed from the OCR'd merchant
-- address at ingest, so Stage 1/2 don't re-parse the free-text address.
ALTER TABLE receipts ADD COLUMN merchant_city TEXT;
ALTER TABLE receipts ADD COLUMN merchant_state TEXT;
