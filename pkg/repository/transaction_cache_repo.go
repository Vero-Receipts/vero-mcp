package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// TransactionCacheRepo implements TransactionCacheRepository for both SQLite and Postgres.
// Transactions reference merchants via merchant_id; read queries LEFT JOIN merchants
// to populate MerchantName / MerchantLogo on the domain model, so callers that
// need merchant display fields don't need a second query.
type TransactionCacheRepo struct {
	BaseRepo
	dialect Dialect
}

func NewTransactionCacheRepo(db *sql.DB, dialect Dialect) *TransactionCacheRepo {
	return &TransactionCacheRepo{
		BaseRepo: NewBaseRepo(db, dialect),
		dialect:  dialect,
	}
}

// ---- column selections ----
//
// SELECT lists. The joined "m.*" columns are appended after the transaction
// columns so scanners can scan them with a stable positional order.

var txnDBCols = []string{
	"t.id", "t.user_id", "t.transaction_id", "t.account_id", "t.amount", "t.date", "t.datetime", "t.name",
	"t.merchant_id", "t.category", "t.pfc_primary", "t.pfc_detailed", "t.payment_channel",
	"t.pending", "t.synced_at",
	"t.corrected_pfc_primary", "t.corrected_pfc_detailed", "t.category_corrected_at",
	"t.recurring",
}

var merchantJoinCols = []string{
	"m.canonical_name", "m.logo_cdn_url",
}

func txnSelectCols() []string {
	return append(append([]string{}, txnDBCols...), merchantJoinCols...)
}

// addDays parses a YYYY-MM-DD string and returns a new YYYY-MM-DD string offset by days.
func addDays(dateStr string, days int) string {
	t, _ := time.Parse("2006-01-02", dateStr)
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

// ---- methods ----

func (r *TransactionCacheRepo) UpsertBatch(ctx context.Context, userID uuid.UUID, txns []domain.Transaction) (int, error) {
	if len(txns) == 0 {
		return 0, nil
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rawSQL := `INSERT INTO transaction_cache
		(id, user_id, transaction_id, account_id, amount, date, datetime, name,
		 merchant_id, location, raw_payload, category, pfc_primary, pfc_detailed, payment_channel,
		 pending, synced_at)
	 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	 ON CONFLICT (transaction_id) DO UPDATE SET
	   account_id      = EXCLUDED.account_id,
	   amount          = EXCLUDED.amount,
	   date            = EXCLUDED.date,
	   datetime        = EXCLUDED.datetime,
	   name            = EXCLUDED.name,
	   merchant_id     = COALESCE(EXCLUDED.merchant_id, transaction_cache.merchant_id),
	   location        = COALESCE(EXCLUDED.location, transaction_cache.location),
	   raw_payload     = COALESCE(EXCLUDED.raw_payload, transaction_cache.raw_payload),
	   category        = EXCLUDED.category,
	   pfc_primary     = EXCLUDED.pfc_primary,
	   pfc_detailed    = EXCLUDED.pfc_detailed,
	   payment_channel = EXCLUDED.payment_channel,
	   pending         = EXCLUDED.pending,
	   synced_at       = EXCLUDED.synced_at`

	if r.dialect == DialectPostgres {
		rawSQL, _ = sq.Dollar.ReplacePlaceholders(rawSQL)
	}

	stmt, err := tx.PrepareContext(ctx, rawSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	uid := userID.String()
	count := 0

	for _, t := range txns {
		category := "[]"
		if t.Category != nil {
			category = string(t.Category)
		}

		var dtStr *string
		if t.DateTime != nil {
			s := t.DateTime.Format(time.RFC3339)
			dtStr = &s
		}

		var pending any
		if r.dialect == DialectSQLite {
			if t.Pending {
				pending = 1
			} else {
				pending = 0
			}
		} else {
			pending = t.Pending
		}

		var merchantIDStr *string
		if t.MerchantID != nil {
			s := t.MerchantID.String()
			merchantIDStr = &s
		}

		var locationStr *string
		if t.Location != nil {
			if b, err := json.Marshal(t.Location); err == nil {
				s := string(b)
				locationStr = &s
			}
		}

		var rawPayloadStr *string
		if len(t.RawPayload) > 0 {
			s := string(t.RawPayload)
			rawPayloadStr = &s
		}

		rowID := uuid.New().String()
		_, err := stmt.ExecContext(ctx,
			rowID, uid, t.TransactionID, t.AccountID, t.Amount, t.Date, dtStr, t.Name,
			merchantIDStr, locationStr, rawPayloadStr, category, t.PFCPrimary, t.PFCDetailed, t.PaymentChannel,
			pending, now,
		)
		if err != nil {
			return count, err
		}
		count++
	}

	return count, tx.Commit()
}

func (r *TransactionCacheRepo) FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnSelectCols()...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		Where(sq.Eq{"t.user_id": userID.String()}).
		OrderBy("t.date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

// FindByUserIDWithReceipts returns a user's transactions enriched with any
// matched receipt, plus the total number of matching transactions and the
// total expense amount (sum of positive amounts) across the whole filtered
// set — both pagination-independent. When filter.Limit > 0 the result is a
// single page: because a transaction can have multiple receipt_matches rows
// (transaction_id is not unique), LIMIT/OFFSET is applied to the distinct,
// ordered set of transaction IDs first, then the receipt details are hydrated
// for just that page.
func (r *TransactionCacheRepo) FindByUserIDWithReceipts(ctx context.Context, userID uuid.UUID, filter domain.TransactionFilter) ([]domain.TransactionWithReceipt, int, float64, error) {
	matched := strings.EqualFold(filter.Matched, "true") || strings.EqualFold(filter.Matched, "matched")
	unmatched := strings.EqualFold(filter.Matched, "false") || strings.EqualFold(filter.Matched, "unmatched")

	// applyBase applies the user scope + filters to a query over the
	// transaction grain (no receipt join), so counts and the page-id lookup
	// are not inflated by transactions with multiple matches. The matched /
	// unmatched filter is expressed via EXISTS for the same reason.
	applyBase := func(qb sq.SelectBuilder) sq.SelectBuilder {
		qb = qb.Where(sq.Eq{"t.user_id": userID.String()})
		qb = applyTransactionFiltersSQ(qb, filter)
		if matched {
			qb = qb.Where("EXISTS (SELECT 1 FROM receipt_matches rm WHERE rm.transaction_id = t.transaction_id)")
		} else if unmatched {
			qb = qb.Where("NOT EXISTS (SELECT 1 FROM receipt_matches rm WHERE rm.transaction_id = t.transaction_id)")
		}
		return qb
	}

	paginate := filter.Limit > 0

	// totalSpent sums only positive (expense) amounts, matching the client's
	// "Total spent" line, across every matching transaction (ignoring the page).
	const spentExpr = "COALESCE(SUM(CASE WHEN t.amount > 0 THEN t.amount ELSE 0 END), 0)"

	var total int
	var totalSpent float64
	if paginate {
		countQB := applyBase(r.SQ.Select("COUNT(*)", spentExpr).
			From("transaction_cache t").
			LeftJoin("merchants m ON m.id = t.merchant_id"))
		if err := countQB.QueryRowContext(ctx).Scan(&total, &totalSpent); err != nil {
			return nil, 0, 0, err
		}
		if total == 0 {
			return []domain.TransactionWithReceipt{}, 0, 0, nil
		}
	}

	receiptJoinCols := []string{
		"r.id", "r.image_url", "r.thumbnail_url", "r.merchant_name",
		"r.total", "r.subtotal", "r.tax", "r.tip",
		"r.payment_method", "r.last_four_digits", "r.date", "r.line_items",
		"rm.match_method", "rm.confidence_score",
	}
	cols := append(txnSelectCols(), receiptJoinCols...)

	qb := r.SQ.Select(cols...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		LeftJoin("receipt_matches rm ON rm.transaction_id = t.transaction_id").
		LeftJoin("receipts r ON r.id = rm.receipt_id").
		Where(sq.Eq{"t.user_id": userID.String()})

	qb = applyTransactionFiltersSQ(qb, filter)

	if matched {
		qb = qb.Where("rm.id IS NOT NULL")
	} else if unmatched {
		qb = qb.Where("rm.id IS NULL")
	}

	if paginate {
		// Resolve the ordered page of transaction IDs at the transaction
		// grain, then restrict the hydrate query to those IDs. Both queries
		// use the same ORDER BY (with its unique tie-breaker) so the page is
		// stable and the final ordering is preserved.
		idQB := applyBase(r.SQ.Select("t.transaction_id").
			From("transaction_cache t").
			LeftJoin("merchants m ON m.id = t.merchant_id")).
			OrderBy(transactionOrderBySQ(filter)).
			Limit(uint64(filter.Limit)).
			Offset(uint64(filter.Offset))

		idRows, err := idQB.QueryContext(ctx)
		if err != nil {
			return nil, 0, 0, err
		}
		var ids []string
		for idRows.Next() {
			var id string
			if err := idRows.Scan(&id); err != nil {
				idRows.Close()
				return nil, 0, 0, err
			}
			ids = append(ids, id)
		}
		idRows.Close()
		if err := idRows.Err(); err != nil {
			return nil, 0, 0, err
		}
		if len(ids) == 0 {
			return []domain.TransactionWithReceipt{}, total, totalSpent, nil
		}
		qb = qb.Where(sq.Eq{"t.transaction_id": ids})
	}

	qb = qb.OrderBy(transactionOrderBySQ(filter))

	rows, err := qb.QueryContext(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	var results []domain.TransactionWithReceipt
	seen := make(map[string]bool)
	for rows.Next() {
		var twr domain.TransactionWithReceipt
		var idStr, userIDStr string
		var merchantIDStr sql.NullString
		var mCanonical, mLogo sql.NullString
		var dt, syncedAt, correctedAt ScannableTime
		var categoryStr string
		var pendingVal, recurringVal ScannableBool

		// receipt match fields
		var rmMethod sql.NullString
		var rmConfidence sql.NullFloat64

		// receipt fields
		var rID, rImageURL, rThumbnailURL, rMerchantName sql.NullString
		var rTotal, rSubtotal, rTax, rTip sql.NullFloat64
		var rPaymentMethod, rLastFour sql.NullString
		var rDate ScannableTime
		var rLineItems sql.NullString

		err := rows.Scan(
			&idStr, &userIDStr, &twr.TransactionID, &twr.AccountID,
			&twr.Amount, &twr.Date, &dt, &twr.Name,
			&merchantIDStr, &categoryStr, &twr.PFCPrimary, &twr.PFCDetailed, &twr.PaymentChannel,
			&pendingVal, &syncedAt, &twr.CorrectedPFCPrimary, &twr.CorrectedPFCDetailed, &correctedAt,
			&recurringVal,
			&mCanonical, &mLogo,
			&rID, &rImageURL, &rThumbnailURL, &rMerchantName, &rTotal, &rSubtotal, &rTax, &rTip,
			&rPaymentMethod, &rLastFour, &rDate, &rLineItems,
			&rmMethod, &rmConfidence,
		)
		if err != nil {
			return nil, 0, 0, err
		}

		twr.ID = ScanUUID(idStr)
		twr.UserID = ScanUUID(userIDStr)
		if merchantIDStr.Valid {
			mid := ScanUUID(merchantIDStr.String)
			twr.MerchantID = &mid
			twr.Merchant = &domain.Merchant{ID: mid, CanonicalName: mCanonical.String}
			if mLogo.Valid {
				twr.Merchant.LogoCDNURL = &mLogo.String
			}
		}
		twr.Pending = pendingVal.Val
		twr.Recurring = recurringVal.Val
		twr.Category = json.RawMessage(categoryStr)
		twr.DateTime = dt.Val
		if syncedAt.Val != nil {
			twr.SyncedAt = *syncedAt.Val
		}
		twr.CategoryCorrectedAt = correctedAt.Val

		if seen[twr.TransactionID] {
			continue
		}
		seen[twr.TransactionID] = true

		if rID.Valid {
			ar := &domain.AttachedReceipt{
				ID:              rID.String,
				MatchMethod:     rmMethod.String,
				ConfidenceScore: rmConfidence.Float64,
			}
			if rImageURL.Valid {
				ar.ImageURL = rImageURL.String
			}
			if rThumbnailURL.Valid {
				ar.ThumbnailURL = &rThumbnailURL.String
			}
			if rMerchantName.Valid {
				ar.MerchantName = &rMerchantName.String
			}
			if rTotal.Valid {
				ar.Total = &rTotal.Float64
			}
			if rSubtotal.Valid {
				ar.Subtotal = &rSubtotal.Float64
			}
			if rTax.Valid {
				ar.Tax = &rTax.Float64
			}
			if rTip.Valid {
				ar.Tip = &rTip.Float64
			}
			if rPaymentMethod.Valid {
				ar.PaymentMethod = &rPaymentMethod.String
			}
			if rLastFour.Valid {
				ar.LastFourDigits = &rLastFour.String
			}
			ar.Date = rDate.Val
			if rLineItems.Valid {
				ar.LineItems = json.RawMessage(rLineItems.String)
			}
			twr.Receipt = ar
		}

		results = append(results, twr)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	if !paginate {
		// Unpaginated: results is the full matching set, so derive both
		// aggregates from it (no extra query needed).
		total = len(results)
		totalSpent = 0
		for i := range results {
			if results[i].Amount > 0 {
				totalSpent += results[i].Amount
			}
		}
	}
	return results, total, totalSpent, nil
}

func (r *TransactionCacheRepo) FindUnmatchedCandidates(ctx context.Context, userID uuid.UUID, amount float64, dateStr string) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnSelectCols()...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		Where(sq.Eq{"t.user_id": userID.String()}).
		Where(sq.GtOrEq{"t.amount": amount * 0.9}).
		Where(sq.LtOrEq{"t.amount": amount * 1.1}).
		Where(sq.GtOrEq{"t.date": addDays(dateStr, -5)}).
		Where(sq.LtOrEq{"t.date": addDays(dateStr, 5)}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = t.transaction_id)").
		OrderBy("t.date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindAllUnmatched(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnSelectCols()...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		Where(sq.Eq{"t.user_id": userID.String()}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = t.transaction_id)").
		OrderBy("t.date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindUnmatchedByDateRange(ctx context.Context, userID uuid.UUID, dateStr string) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnSelectCols()...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		Where(sq.Eq{"t.user_id": userID.String()}).
		Where(sq.GtOrEq{"t.date": addDays(dateStr, -3)}).
		Where(sq.LtOrEq{"t.date": addDays(dateStr, 3)}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = t.transaction_id)").
		OrderBy("t.date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindUnmatchedTight(ctx context.Context, userID uuid.UUID, amount float64, dateStr string, isFX bool) ([]domain.Transaction, error) {
	// Tolerances match the scorer's outer acceptance boundary so no valid
	// candidate is excluded before scoring gets a chance to evaluate it.
	tolerance := 0.20
	if isFX {
		tolerance = 0.40
	}

	rows, err := r.SQ.Select(txnSelectCols()...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		Where(sq.Eq{"t.user_id": userID.String()}).
		Where(sq.GtOrEq{"t.amount": amount * (1 - tolerance)}).
		Where(sq.LtOrEq{"t.amount": amount * (1 + tolerance)}).
		Where(sq.GtOrEq{"t.date": addDays(dateStr, -4)}).
		Where(sq.LtOrEq{"t.date": addDays(dateStr, 4)}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = t.transaction_id)").
		OrderBy("t.date DESC").
		Limit(20).
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) RemoveBatch(ctx context.Context, transactionIDs []string) error {
	if len(transactionIDs) == 0 {
		return nil
	}

	_, err := r.SQ.Delete("transaction_cache").
		Where(sq.Eq{"transaction_id": transactionIDs}).
		ExecContext(ctx)
	return err
}

func (r *TransactionCacheRepo) SearchUnmatched(ctx context.Context, userID uuid.UUID, search string) ([]domain.Transaction, error) {
	like := "%" + search + "%"

	rows, err := r.SQ.Select(txnSelectCols()...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		Where(sq.Eq{"t.user_id": userID.String()}).
		Where(sq.Or{
			sq.Expr("LOWER(t.name) LIKE LOWER(?)", like),
			sq.Expr("LOWER(m.canonical_name) LIKE LOWER(?)", like),
		}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = t.transaction_id)").
		OrderBy("t.date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindByTransactionID(ctx context.Context, transactionID string) (*domain.Transaction, error) {
	row := r.SQ.Select(txnSelectCols()...).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		Where(sq.Eq{"t.transaction_id": transactionID}).
		QueryRowContext(ctx)

	t, err := r.scanTransaction(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

func (r *TransactionCacheRepo) UpdateCorrectedCategory(ctx context.Context, transactionID string, primary, detailed string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := r.SQ.Update("transaction_cache").
		Set("corrected_pfc_primary", primary).
		Set("corrected_pfc_detailed", detailed).
		Set("category_corrected_at", now).
		Where(sq.Eq{"transaction_id": transactionID}).
		ExecContext(ctx)
	if err != nil {
		return err
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// FindRecurringCandidates returns every transaction for the user that has a merchant_id,
// joined with its (at most one) receipt match and — when that match is real (not a
// carried-forward 'recurring' link) — the source receipt id and its subscription flag.
// Ordered by (merchant, date) so the caller can walk each merchant's series in time order.
func (r *TransactionCacheRepo) FindRecurringCandidates(ctx context.Context, userID uuid.UUID) ([]domain.RecurringCandidate, error) {
	// Merchant display name comes from merchants.canonical_name (joined on merchant_id) — the
	// sync path writes merchant_id, not transaction_cache.merchant_name, so that column is
	// unreliable for synced rows.
	rows, err := r.SQ.Select(
		"tc.transaction_id", "tc.merchant_id", "m.canonical_name", "tc.date", "tc.amount", "tc.recurring",
		"CASE WHEN rm.id IS NOT NULL THEN 1 ELSE 0 END AS matched",
		"CASE WHEN rm.match_method <> 'recurring' THEN rm.receipt_id END AS source_receipt",
		"CASE WHEN rm.match_method <> 'recurring' THEN r.is_subscription END AS is_subscription",
	).
		From("transaction_cache tc").
		LeftJoin("merchants m ON m.id = tc.merchant_id").
		LeftJoin("receipt_matches rm ON rm.transaction_id = tc.transaction_id").
		LeftJoin("receipts r ON r.id = rm.receipt_id").
		Where(sq.Eq{"tc.user_id": userID.String()}).
		Where("tc.merchant_id IS NOT NULL").
		OrderBy("tc.merchant_id", "tc.date ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.RecurringCandidate
	for rows.Next() {
		var c domain.RecurringCandidate
		var merchantIDStr string
		var merchantName sql.NullString
		var dateVal ScannableTime
		var recurringVal, matchedVal ScannableBool
		var sourceReceipt sql.NullString
		var isSub sql.NullBool

		if err := rows.Scan(
			&c.TransactionID, &merchantIDStr, &merchantName, &dateVal, &c.Amount, &recurringVal,
			&matchedVal, &sourceReceipt, &isSub,
		); err != nil {
			return nil, err
		}
		c.MerchantID = ScanUUID(merchantIDStr)
		c.MerchantName = merchantName.String
		if dateVal.Val != nil {
			c.Date = dateVal.Val.Format("2006-01-02")
		}
		c.Recurring = recurringVal.Val
		c.Matched = matchedVal.Val
		if sourceReceipt.Valid && sourceReceipt.String != "" {
			id := ScanUUID(sourceReceipt.String)
			c.SourceReceipt = &id
		}
		if isSub.Valid {
			b := isSub.Bool
			c.IsSubscription = &b
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AllUserIDsWithTransactions returns every distinct user id that has at least one cached
// transaction. Useful for walking the whole user base when scanning for recurring series.
func (r *TransactionCacheRepo) AllUserIDsWithTransactions(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := r.SQ.Select("DISTINCT user_id").
		From("transaction_cache").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		ids = append(ids, ScanUUID(s))
	}
	return ids, rows.Err()
}

// SetRecurring marks the given transactions as part of a recurring series.
func (r *TransactionCacheRepo) SetRecurring(ctx context.Context, transactionIDs []string) error {
	if len(transactionIDs) == 0 {
		return nil
	}
	_, err := r.SQ.Update("transaction_cache").
		Set("recurring", true).
		Where(sq.Eq{"transaction_id": transactionIDs}).
		ExecContext(ctx)
	return err
}

// --- scan helpers ---

func (r *TransactionCacheRepo) scanTransaction(s scanner) (*domain.Transaction, error) {
	var t domain.Transaction
	var idStr, userIDStr string
	var merchantIDStr sql.NullString
	var mCanonical, mLogo sql.NullString
	var dateVal, dt, syncedAt, correctedAt ScannableTime
	var categoryStr string
	var pendingVal, recurringVal ScannableBool

	err := s.Scan(
		&idStr, &userIDStr, &t.TransactionID, &t.AccountID,
		&t.Amount, &dateVal, &dt, &t.Name,
		&merchantIDStr, &categoryStr, &t.PFCPrimary, &t.PFCDetailed, &t.PaymentChannel,
		&pendingVal, &syncedAt, &t.CorrectedPFCPrimary, &t.CorrectedPFCDetailed, &correctedAt,
		&recurringVal,
		&mCanonical, &mLogo,
	)
	if err != nil {
		return nil, err
	}
	t.Recurring = recurringVal.Val

	t.ID = ScanUUID(idStr)
	t.UserID = ScanUUID(userIDStr)
	if dateVal.Val != nil {
		t.Date = dateVal.Val.Format("2006-01-02")
	}
	if merchantIDStr.Valid {
		mid := ScanUUID(merchantIDStr.String)
		t.MerchantID = &mid
		if mCanonical.Valid {
			t.Merchant = &domain.Merchant{ID: mid, CanonicalName: mCanonical.String}
			if mLogo.Valid {
				t.Merchant.LogoCDNURL = &mLogo.String
			}
		}
	}
	t.Pending = pendingVal.Val
	t.Category = json.RawMessage(categoryStr)
	t.DateTime = dt.Val
	if syncedAt.Val != nil {
		t.SyncedAt = *syncedAt.Val
	}
	t.CategoryCorrectedAt = correctedAt.Val

	return &t, nil
}

func (r *TransactionCacheRepo) scanTransactions(rows *sql.Rows) ([]domain.Transaction, error) {
	var txns []domain.Transaction
	for rows.Next() {
		t, err := r.scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		txns = append(txns, *t)
	}
	return txns, rows.Err()
}

// --- filter / order helpers (squirrel) ---
//
// All filters reference the aliased tables: t. for transactions, m. for merchants.

func applyTransactionFiltersSQ(qb sq.SelectBuilder, f domain.TransactionFilter) sq.SelectBuilder {
	if f.Search != "" {
		like := "%" + f.Search + "%"
		qb = qb.Where(sq.Or{
			sq.Expr("LOWER(t.name) LIKE LOWER(?)", like),
			sq.Expr("LOWER(m.canonical_name) LIKE LOWER(?)", like),
		})
	}
	if f.DateFrom != "" {
		qb = qb.Where(sq.GtOrEq{"t.date": f.DateFrom})
	}
	if f.DateTo != "" {
		qb = qb.Where(sq.LtOrEq{"t.date": f.DateTo})
	}
	if f.AmountMin != nil {
		qb = qb.Where(sq.GtOrEq{"t.amount": *f.AmountMin})
	}
	if f.AmountMax != nil {
		qb = qb.Where(sq.LtOrEq{"t.amount": *f.AmountMax})
	}
	if f.Category != "" {
		qb = qb.Where(sq.Expr("LOWER(t.category) LIKE LOWER(?)", "%"+f.Category+"%"))
	}
	if f.PFCPrimary != "" {
		qb = qb.Where(sq.Eq{"t.pfc_primary": f.PFCPrimary})
	}
	if f.PFCDetailed != "" {
		qb = qb.Where(sq.Eq{"t.pfc_detailed": f.PFCDetailed})
	}
	if strings.EqualFold(f.Pending, "true") {
		qb = qb.Where(sq.Eq{"t.pending": true})
	} else if strings.EqualFold(f.Pending, "false") {
		qb = qb.Where(sq.Eq{"t.pending": false})
	}
	return qb
}

func transactionOrderBySQ(f domain.TransactionFilter) string {
	col := "t.date"
	dir := "DESC"

	switch strings.ToLower(f.SortBy) {
	case "amount":
		col = "t.amount"
	case "name":
		col = "t.name"
	case "merchant", "merchant_name":
		col = "m.canonical_name"
	}

	if strings.EqualFold(f.SortOrder, "asc") {
		dir = "ASC"
	}

	// t.transaction_id is a stable, unique tie-breaker so consecutive offset
	// pages never overlap or skip rows when the primary sort key has ties.
	return fmt.Sprintf("%s %s, t.transaction_id", col, dir)
}
