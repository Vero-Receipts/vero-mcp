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
type TransactionCacheRepo struct {
	BaseRepo
	dialect Dialect // retained for UpsertBatch prepared-statement placeholder format
}

func NewTransactionCacheRepo(db *sql.DB, dialect Dialect) *TransactionCacheRepo {
	return &TransactionCacheRepo{
		BaseRepo: NewBaseRepo(db, dialect),
		dialect:  dialect,
	}
}

// ---- column lists ----

var txnCols = []string{
	"id", "user_id", "transaction_id", "account_id", "amount", "date", "datetime", "name",
	"merchant_name", "category", "pfc_primary", "pfc_detailed", "payment_channel",
	"pending", "merchant_logo", "synced_at",
	"corrected_pfc_primary", "corrected_pfc_detailed", "category_corrected_at",
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

	// Build the upsert SQL once using raw SQL with dialect-appropriate placeholders.
	rawSQL := `INSERT INTO transaction_cache
		(id, user_id, transaction_id, account_id, amount, date, datetime, name,
		 merchant_name, category, pfc_primary, pfc_detailed, payment_channel,
		 pending, merchant_logo, synced_at)
	 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	 ON CONFLICT (transaction_id) DO UPDATE SET
	   account_id      = EXCLUDED.account_id,
	   amount          = EXCLUDED.amount,
	   date            = EXCLUDED.date,
	   datetime        = EXCLUDED.datetime,
	   name            = EXCLUDED.name,
	   merchant_name   = EXCLUDED.merchant_name,
	   category        = EXCLUDED.category,
	   pfc_primary     = EXCLUDED.pfc_primary,
	   pfc_detailed    = EXCLUDED.pfc_detailed,
	   payment_channel = EXCLUDED.payment_channel,
	   pending         = EXCLUDED.pending,
	   merchant_logo   = EXCLUDED.merchant_logo,
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

		var pending interface{}
		if r.dialect == DialectSQLite {
			if t.Pending {
				pending = 1
			} else {
				pending = 0
			}
		} else {
			pending = t.Pending
		}

		rowID := uuid.New().String()
		_, err := stmt.ExecContext(ctx,
			rowID, uid, t.TransactionID, t.AccountID, t.Amount, t.Date, dtStr, t.Name,
			t.MerchantName, category, t.PFCPrimary, t.PFCDetailed, t.PaymentChannel,
			pending, t.MerchantLogo, now,
		)
		if err != nil {
			return count, err
		}
		count++
	}

	return count, tx.Commit()
}

func (r *TransactionCacheRepo) FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnCols...).
		From("transaction_cache").
		Where(sq.Eq{"user_id": userID.String()}).
		OrderBy("date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindByUserIDWithReceipts(ctx context.Context, userID uuid.UUID, filter domain.TransactionFilter) ([]domain.TransactionWithReceipt, error) {
	txnColsPrefixed := prefixCols("t.", txnCols)
	receiptJoinCols := []string{
		"r.id", "r.image_url", "r.thumbnail_url", "r.merchant_name",
		"r.total", "r.subtotal", "r.tax", "r.tip",
		"r.payment_method", "r.last_four_digits", "r.date", "r.line_items",
		"rm.match_method", "rm.confidence_score",
	}
	cols := append(txnColsPrefixed, receiptJoinCols...)

	qb := r.SQ.Select(cols...).
		From("transaction_cache t").
		LeftJoin("receipt_matches rm ON rm.transaction_id = t.transaction_id").
		LeftJoin("receipts r ON r.id = rm.receipt_id").
		Where(sq.Eq{"t.user_id": userID.String()})

	qb = applyTransactionFiltersSQ(qb, filter, "t.")

	// Handle matched filter
	if strings.EqualFold(filter.Matched, "true") || strings.EqualFold(filter.Matched, "matched") {
		qb = qb.Where("rm.id IS NOT NULL")
	} else if strings.EqualFold(filter.Matched, "false") || strings.EqualFold(filter.Matched, "unmatched") {
		qb = qb.Where("rm.id IS NULL")
	}

	qb = qb.OrderBy(transactionOrderBySQ(filter, "t."))

	rows, err := qb.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []domain.TransactionWithReceipt
	seen := make(map[string]bool)
	for rows.Next() {
		var twr domain.TransactionWithReceipt
		var idStr, userIDStr string
		var dt, syncedAt, correctedAt ScannableTime
		var categoryStr string
		var pendingVal ScannableBool

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
			&twr.MerchantName, &categoryStr, &twr.PFCPrimary, &twr.PFCDetailed, &twr.PaymentChannel,
			&pendingVal, &twr.MerchantLogo, &syncedAt, &twr.CorrectedPFCPrimary, &twr.CorrectedPFCDetailed, &correctedAt,
			&rID, &rImageURL, &rThumbnailURL, &rMerchantName, &rTotal, &rSubtotal, &rTax, &rTip,
			&rPaymentMethod, &rLastFour, &rDate, &rLineItems,
			&rmMethod, &rmConfidence,
		)
		if err != nil {
			return nil, err
		}

		twr.ID = ScanUUID(idStr)
		twr.UserID = ScanUUID(userIDStr)
		twr.Pending = pendingVal.Val
		twr.Category = json.RawMessage(categoryStr)
		twr.DateTime = dt.Val
		if syncedAt.Val != nil {
			twr.SyncedAt = *syncedAt.Val
		}
		twr.CategoryCorrectedAt = correctedAt.Val

		// Deduplicate by transaction_id
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
	return results, rows.Err()
}

func (r *TransactionCacheRepo) FindUnmatchedCandidates(ctx context.Context, userID uuid.UUID, amount float64, dateStr string) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnCols...).
		From("transaction_cache").
		Where(sq.Eq{"user_id": userID.String()}).
		Where(sq.GtOrEq{"amount": amount * 0.9}).
		Where(sq.LtOrEq{"amount": amount * 1.1}).
		Where(sq.GtOrEq{"date": addDays(dateStr, -5)}).
		Where(sq.LtOrEq{"date": addDays(dateStr, 5)}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = transaction_cache.transaction_id)").
		OrderBy("date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindAllUnmatched(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnCols...).
		From("transaction_cache").
		Where(sq.Eq{"user_id": userID.String()}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = transaction_cache.transaction_id)").
		OrderBy("date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindUnmatchedByDateRange(ctx context.Context, userID uuid.UUID, dateStr string) ([]domain.Transaction, error) {
	rows, err := r.SQ.Select(txnCols...).
		From("transaction_cache").
		Where(sq.Eq{"user_id": userID.String()}).
		Where(sq.GtOrEq{"date": addDays(dateStr, -3)}).
		Where(sq.LtOrEq{"date": addDays(dateStr, 3)}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = transaction_cache.transaction_id)").
		OrderBy("date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindUnmatchedTight(ctx context.Context, userID uuid.UUID, amount float64, dateStr string, isFX bool) ([]domain.Transaction, error) {
	tolerance := 0.05
	if isFX {
		tolerance = 0.15
	}

	rows, err := r.SQ.Select(txnCols...).
		From("transaction_cache").
		Where(sq.Eq{"user_id": userID.String()}).
		Where(sq.GtOrEq{"amount": amount * (1 - tolerance)}).
		Where(sq.LtOrEq{"amount": amount * (1 + tolerance)}).
		Where(sq.GtOrEq{"date": addDays(dateStr, -2)}).
		Where(sq.LtOrEq{"date": addDays(dateStr, 2)}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = transaction_cache.transaction_id)").
		OrderBy("date DESC").
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

	rows, err := r.SQ.Select(txnCols...).
		From("transaction_cache").
		Where(sq.Eq{"user_id": userID.String()}).
		Where(sq.Or{
			sq.Expr("LOWER(name) LIKE LOWER(?)", like),
			sq.Expr("LOWER(merchant_name) LIKE LOWER(?)", like),
		}).
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE transaction_id = transaction_cache.transaction_id)").
		OrderBy("date DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return r.scanTransactions(rows)
}

func (r *TransactionCacheRepo) FindByTransactionID(ctx context.Context, transactionID string) (*domain.Transaction, error) {
	row := r.SQ.Select(txnCols...).
		From("transaction_cache").
		Where(sq.Eq{"transaction_id": transactionID}).
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

// --- scan helpers ---

func (r *TransactionCacheRepo) scanTransaction(s scanner) (*domain.Transaction, error) {
	var t domain.Transaction
	var idStr, userIDStr string
	var dt, syncedAt, correctedAt ScannableTime
	var categoryStr string
	var pendingVal ScannableBool

	err := s.Scan(
		&idStr, &userIDStr, &t.TransactionID, &t.AccountID,
		&t.Amount, &t.Date, &dt, &t.Name,
		&t.MerchantName, &categoryStr, &t.PFCPrimary, &t.PFCDetailed, &t.PaymentChannel,
		&pendingVal, &t.MerchantLogo, &syncedAt, &t.CorrectedPFCPrimary, &t.CorrectedPFCDetailed, &correctedAt,
	)
	if err != nil {
		return nil, err
	}

	t.ID = ScanUUID(idStr)
	t.UserID = ScanUUID(userIDStr)
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

func applyTransactionFiltersSQ(qb sq.SelectBuilder, f domain.TransactionFilter, prefix string) sq.SelectBuilder {
	if f.Search != "" {
		like := "%" + f.Search + "%"
		qb = qb.Where(sq.Or{
			sq.Expr("LOWER("+prefix+"name) LIKE LOWER(?)", like),
			sq.Expr("LOWER("+prefix+"merchant_name) LIKE LOWER(?)", like),
		})
	}
	if f.DateFrom != "" {
		qb = qb.Where(sq.GtOrEq{prefix + "date": f.DateFrom})
	}
	if f.DateTo != "" {
		qb = qb.Where(sq.LtOrEq{prefix + "date": f.DateTo})
	}
	if f.AmountMin != nil {
		qb = qb.Where(sq.GtOrEq{prefix + "amount": *f.AmountMin})
	}
	if f.AmountMax != nil {
		qb = qb.Where(sq.LtOrEq{prefix + "amount": *f.AmountMax})
	}
	if f.Category != "" {
		qb = qb.Where(sq.Expr("LOWER("+prefix+"category) LIKE LOWER(?)", "%" + f.Category + "%"))
	}
	if f.PFCPrimary != "" {
		qb = qb.Where(sq.Eq{prefix + "pfc_primary": f.PFCPrimary})
	}
	if f.PFCDetailed != "" {
		qb = qb.Where(sq.Eq{prefix + "pfc_detailed": f.PFCDetailed})
	}
	if strings.EqualFold(f.Pending, "true") {
		qb = qb.Where(sq.Eq{prefix + "pending": true})
	} else if strings.EqualFold(f.Pending, "false") {
		qb = qb.Where(sq.Eq{prefix + "pending": false})
	}
	return qb
}

func transactionOrderBySQ(f domain.TransactionFilter, prefix string) string {
	col := prefix + "date"
	dir := "DESC"

	switch strings.ToLower(f.SortBy) {
	case "amount":
		col = prefix + "amount"
	case "name":
		col = prefix + "name"
	case "merchant", "merchant_name":
		col = prefix + "merchant_name"
	}

	if strings.EqualFold(f.SortOrder, "asc") {
		dir = "ASC"
	}

	return fmt.Sprintf("%s %s", col, dir)
}
