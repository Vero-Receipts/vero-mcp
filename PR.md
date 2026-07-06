# Recurring transaction detection & itemization carry-forward

## Summary

Detects when a user's transactions form a **recurring series** (a subscription or regular bill) and carries the itemization from the series' source receipt forward onto the later, bare charges — so a subscription that only ever emails one itemized receipt still shows line items on every renewal. Recurring transactions are flagged so clients can badge them, independently of whether a receipt is attached.

## What changed

### OCR: a subscription signal

`OCRResult` / `Receipt` gain `is_subscription`, extracted during receipt parsing. The structured-output schema and both the image and text prompts now ask the model whether the receipt describes a recurring charge (mentions a subscription, auto-renewal, a billing cycle, "recurring", "renews on", or "billed monthly/yearly"). It's the high-precision signal that separates a real subscription from an ordinary repeat purchase.

### Recurrence detection (`pkg/service/recurring_service.go`)

`AnalyzeRecurring` groups a user's transactions by merchant, then by amount (a tight ±2% band), and evaluates each cluster:

- **Cadence** — consecutive gaps must snap to a frequency bucket (weekly 7±2, biweekly 14±3, monthly 30±5, annual 365±15 days).
- **Establishment** — either a source receipt flagged `is_subscription` (≥2 occurrences), or ≥3 occurrences on a regular cadence (pattern-only, no receipt required).

`ReceiptService.PropagateRecurring` applies the result: it flags every member of an established series as recurring, and for bare members it creates a carried-forward match (`match_method = 'recurring'`) to the series' source receipt. It runs in the background after a sync lands new transactions, is idempotent, and is best-effort (errors logged, not returned).

**Display contract:** for a carried-forward match, amount and date come from the transaction; only line items and merchant come from the receipt — the two are never reconciled (the source receipt legitimately carries an earlier date/total).

### Data model — migration `008_recurring.sql`

- `receipts.is_subscription` — subscription flag from OCR (`NULL` = not yet evaluated).
- `transaction_cache.recurring` — marks a transaction as part of a recurring series; drives the client badge and is set independently of any receipt.
- Drops `receipt_matches.receipt_id` uniqueness so **one receipt can back many transactions** (a source receipt is carried forward to the series' later charges). SQLite cannot drop a column constraint in place, so `receipt_matches` is rebuilt **copying existing rows first** — no match data is lost when an already-populated database upgrades.

### Repository / API

- `TransactionCacheRepo`: `FindRecurringCandidates` (transaction ⋈ receipt_matches ⋈ receipts, with merchant name from `merchants.canonical_name`), `SetRecurring`, and `AllUserIDsWithTransactions`. Transaction reads/writes now include `recurring`.
- `receipts` reads/writes now include `is_subscription`.
- `TransactionResponse` gains `recurring`, and `AttachedReceipt.matchMethod` surfaces `"recurring"`, so clients can render a badge and a "carried forward from an earlier receipt" note with no other API change.

## Testing

- **Detection logic** (`recurring_service_test.go`): cadence bucketing, amount clustering, the 2-with-subscription vs. ≥3-pattern establishment rule, idempotent flagging.
- **Repository** (`recurring_repo_test.go`, real SQLite + embedded migrations): one receipt → many transactions (regression guard for the migration), `FindRecurringCandidates` field correctness incl. the merchant-name join and the real-vs-derived source distinction, `SetRecurring`, `AllUserIDsWithTransactions`.
- **End-to-end propagation** (`recurring_propagate_test.go`, real DB via `Open`): subscription source establishes and itemizes; pattern-of-3 flags without itemizing; two non-subscription charges do not establish; running twice creates no duplicate matches.
- **Subscription detection golden** (`TestGolden_IsSubscriptionDetection`): a real SoundCloud Go+ email is parsed and asserted as `is_subscription = true`, replayed deterministically.
- Existing receipt-parsing goldens were re-captured because the extraction prompt changed.

## Known limitations / follow-ups

- **Concurrent same-price series & re-linked accounts.** Detection groups at the merchant level and does not partition by bank account. Two concurrent same-price subscriptions to one merchant (or duplicate charges from a re-linked account) can interleave and distort cadence. A planned follow-up dedups candidates by `(date, amount)` before computing cadence — robust to both without depending on an account identifier (which is not stable across a re-link).
- **Missed cycles.** A skipped charge produces an out-of-bucket gap and can break a series; multi-interval gap tolerance is future work.
