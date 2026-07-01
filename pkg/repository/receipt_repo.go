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
	"github.com/jackc/pgx/v5/pgconn"
)

// scanner is the interface shared by *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// ReceiptRepo implements ReceiptRepository for both SQLite and Postgres.
type ReceiptRepo struct {
	BaseRepo
}

func NewReceiptRepo(db *sql.DB, dialect Dialect) *ReceiptRepo {
	return &ReceiptRepo{BaseRepo: NewBaseRepo(db, dialect)}
}

// ---- column lists (avoid repetition) ----

var receiptCols = []string{
	"id", "user_id", "image_url", "image_path", "thumbnail_url",
	"merchant_name", "merchant_address", "merchant_city", "merchant_state", "total", "currency", "total_usd",
	"subtotal", "tax", "tip", "payment_method", "last_four_digits",
	"date", "transaction_time", "raw_text", "ocr_error", "line_items",
	"source", "status",
	"content_hash", "gmail_message_id", "order_id", "merchant_key", "duplicate_of",
	"created_at", "updated_at",
}

// isUniqueViolation reports whether err is a Postgres 23505 unique constraint
// error (as surfaced by pgx's stdlib driver via errors.As). SQLite does not
// reach this path under current schema — partial unique indexes are
// Postgres-only.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	return false
}

func prefixCols(prefix string, cols []string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = prefix + c
	}
	return out
}

var receiptMatchCols = []string{
	"rm.id", "rm.receipt_id", "rm.transaction_id", "rm.account_id",
	"rm.confidence_score", "rm.match_method", "rm.match_reason", "rm.matched_at",
}

// ---- CRUD methods ----

func (r *ReceiptRepo) Create(ctx context.Context, rcpt *domain.Receipt) error {
	if rcpt.ID == uuid.Nil {
		rcpt.ID = uuid.New()
	}

	lineItems := json.RawMessage("[]")
	if rcpt.LineItems != nil {
		lineItems = rcpt.LineItems
	}

	var dateStr *string
	if rcpt.Date != nil {
		s := rcpt.Date.Format(time.RFC3339)
		dateStr = &s
	}

	now := time.Now().UTC().Format(time.RFC3339)

	var duplicateOfStr *string
	if rcpt.DuplicateOf != nil {
		s := rcpt.DuplicateOf.String()
		duplicateOfStr = &s
	}

	_, err := r.SQ.Insert("receipts").
		Columns(receiptCols...).
		Values(
			rcpt.ID.String(), rcpt.UserID.String(), rcpt.ImageURL, rcpt.ImagePath,
			rcpt.ThumbnailURL, rcpt.MerchantName, rcpt.MerchantAddress,
			rcpt.MerchantCity, rcpt.MerchantState,
			rcpt.Total, rcpt.Currency, rcpt.TotalUSD,
			rcpt.Subtotal, rcpt.Tax, rcpt.Tip,
			rcpt.PaymentMethod, rcpt.LastFourDigits,
			dateStr, rcpt.TransactionTime,
			rcpt.RawText, rcpt.OCRError, string(lineItems),
			rcpt.Source, rcpt.Status,
			rcpt.ContentHash, rcpt.GmailMessageID, rcpt.OrderID, rcpt.MerchantKey, duplicateOfStr,
			now, now,
		).
		ExecContext(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: %s", domain.ErrUniqueViolation, err.Error())
		}
		return err
	}

	rcpt.CreatedAt, _ = time.Parse(time.RFC3339, now)
	rcpt.UpdatedAt = rcpt.CreatedAt
	return nil
}

func (r *ReceiptRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Receipt, error) {
	row := r.SQ.Select(receiptCols...).
		From("receipts").
		Where(sq.Eq{"id": id.String()}).
		QueryRowContext(ctx)

	rcpt, err := r.scanReceipt(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return rcpt, nil
}

func (r *ReceiptRepo) FindByIDWithMatch(ctx context.Context, id uuid.UUID) (*domain.ReceiptWithMatch, error) {
	cols := append(prefixCols("r.", receiptCols), receiptMatchCols...)

	row := r.SQ.Select(cols...).
		From("receipts r").
		LeftJoin("receipt_matches rm ON rm.receipt_id = r.id").
		Where(sq.Eq{"r.id": id.String()}).
		QueryRowContext(ctx)

	rwm, err := r.scanReceiptWithMatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return rwm, nil
}

func (r *ReceiptRepo) FindByUserID(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.Receipt, error) {
	qb := r.SQ.Select(receiptCols...).
		From("receipts").
		Where(sq.Eq{"user_id": userID.String()})

	if !filter.IncludeDuplicates {
		qb = qb.Where("duplicate_of IS NULL")
	}

	qb = applyReceiptFiltersSQ(qb, filter, "")
	qb = qb.OrderBy(receiptOrderBySQ(filter, ""))

	rows, err := qb.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var receipts []domain.Receipt
	for rows.Next() {
		rcpt, err := r.scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, *rcpt)
	}
	return receipts, rows.Err()
}

// FindByUserIDWithMatches returns a user's receipts with their match (if any),
// plus the total number of matching receipts (for pagination). receipt_matches.
// receipt_id is unique, so the join never multiplies rows and LIMIT/OFFSET can
// be applied directly when filter.Limit > 0.
func (r *ReceiptRepo) FindByUserIDWithMatches(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.ReceiptWithMatch, int, error) {
	// applyWhere applies the user scope + filters shared by the count and page
	// queries.
	applyWhere := func(qb sq.SelectBuilder) sq.SelectBuilder {
		qb = qb.Where(sq.Eq{"r.user_id": userID.String()})
		if !filter.IncludeDuplicates {
			qb = qb.Where("r.duplicate_of IS NULL")
		}
		qb = applyReceiptFiltersSQ(qb, filter, "r.")
		if filter.MatchMethod != "" {
			qb = qb.Where(sq.Eq{"rm.match_method": filter.MatchMethod})
		}
		return qb
	}

	paginate := filter.Limit > 0

	var total int
	if paginate {
		countQB := applyWhere(r.SQ.Select("COUNT(*)").
			From("receipts r").
			LeftJoin("receipt_matches rm ON rm.receipt_id = r.id"))
		if err := countQB.QueryRowContext(ctx).Scan(&total); err != nil {
			return nil, 0, err
		}
		if total == 0 {
			return []domain.ReceiptWithMatch{}, 0, nil
		}
	}

	cols := append(prefixCols("r.", receiptCols), receiptMatchCols...)
	qb := applyWhere(r.SQ.Select(cols...).
		From("receipts r").
		LeftJoin("receipt_matches rm ON rm.receipt_id = r.id")).
		OrderBy(receiptOrderBySQ(filter, "r."))

	if paginate {
		qb = qb.Limit(uint64(filter.Limit)).Offset(uint64(filter.Offset))
	}

	rows, err := qb.QueryContext(ctx)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var results []domain.ReceiptWithMatch
	for rows.Next() {
		rwm, err := r.scanReceiptWithMatch(rows)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, *rwm)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if !paginate {
		total = len(results)
	}
	return results, total, nil
}

func (r *ReceiptRepo) FindUnmatchedValid(ctx context.Context, userID uuid.UUID) ([]domain.Receipt, error) {
	rows, err := r.SQ.Select(receiptCols...).
		From("receipts").
		Where(sq.Eq{"user_id": userID.String()}).
		Where(sq.NotEq{"status": "error"}).
		Where("duplicate_of IS NULL").
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE receipt_id = receipts.id)").
		OrderBy("created_at DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var receipts []domain.Receipt
	for rows.Next() {
		rcpt, err := r.scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, *rcpt)
	}
	return receipts, rows.Err()
}

func (r *ReceiptRepo) Update(ctx context.Context, rcpt *domain.Receipt) error {
	now := time.Now().UTC().Format(time.RFC3339)

	lineItems := json.RawMessage("[]")
	if rcpt.LineItems != nil {
		lineItems = rcpt.LineItems
	}

	var dateStr *string
	if rcpt.Date != nil {
		s := rcpt.Date.Format(time.RFC3339)
		dateStr = &s
	}

	var duplicateOfStr *string
	if rcpt.DuplicateOf != nil {
		s := rcpt.DuplicateOf.String()
		duplicateOfStr = &s
	}

	res, err := r.SQ.Update("receipts").
		Set("image_url", rcpt.ImageURL).
		Set("image_path", rcpt.ImagePath).
		Set("thumbnail_url", rcpt.ThumbnailURL).
		Set("merchant_name", rcpt.MerchantName).
		Set("merchant_address", rcpt.MerchantAddress).
		Set("merchant_city", rcpt.MerchantCity).
		Set("merchant_state", rcpt.MerchantState).
		Set("total", rcpt.Total).
		Set("currency", rcpt.Currency).
		Set("total_usd", rcpt.TotalUSD).
		Set("subtotal", rcpt.Subtotal).
		Set("tax", rcpt.Tax).
		Set("tip", rcpt.Tip).
		Set("payment_method", rcpt.PaymentMethod).
		Set("last_four_digits", rcpt.LastFourDigits).
		Set("date", dateStr).
		Set("transaction_time", rcpt.TransactionTime).
		Set("raw_text", rcpt.RawText).
		Set("ocr_error", rcpt.OCRError).
		Set("line_items", string(lineItems)).
		Set("source", rcpt.Source).
		Set("status", rcpt.Status).
		Set("content_hash", rcpt.ContentHash).
		Set("gmail_message_id", rcpt.GmailMessageID).
		Set("order_id", rcpt.OrderID).
		Set("merchant_key", rcpt.MerchantKey).
		Set("duplicate_of", duplicateOfStr).
		Set("updated_at", now).
		Where(sq.Eq{"id": rcpt.ID.String()}).
		ExecContext(ctx)
	if err != nil {
		return err
	}

	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	rcpt.UpdatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

func (r *ReceiptRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	res, err := r.SQ.Update("receipts").
		Set("status", status).
		Set("updated_at", now).
		Where(sq.Eq{"id": id.String()}).
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

func (r *ReceiptRepo) UpdateThumbnailURL(ctx context.Context, id uuid.UUID, thumbnailURL string) error {
	_, err := r.SQ.Update("receipts").
		Set("thumbnail_url", thumbnailURL).
		Set("updated_at", time.Now().UTC().Format(time.RFC3339)).
		Where(sq.Eq{"id": id.String()}).
		ExecContext(ctx)
	return err
}

func (r *ReceiptRepo) FindWithoutThumbnails(ctx context.Context, offset, limit int) ([]domain.Receipt, error) {
	rows, err := r.SQ.Select(prefixCols("r.", receiptCols)...).
		From("receipts r").
		Where("r.thumbnail_url IS NULL").
		Where("NULLIF(TRIM(r.image_url), '') IS NOT NULL").
		OrderBy("r.created_at ASC").
		Limit(uint64(limit)).
		Offset(uint64(offset)).
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Receipt
	for rows.Next() {
		rcpt, err := r.scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *rcpt)
	}
	return out, rows.Err()
}

func (r *ReceiptRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.SQ.Delete("receipts").
		Where(sq.Eq{"id": id.String()}).
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

func (r *ReceiptRepo) CountUnmatched(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := r.SQ.Select("COUNT(*)").
		From("receipts").
		Where(sq.Eq{"user_id": userID.String()}).
		Where(sq.NotEq{"status": "error"}).
		Where("duplicate_of IS NULL").
		Where("NOT EXISTS (SELECT 1 FROM receipt_matches WHERE receipt_id = receipts.id)").
		QueryRowContext(ctx).
		Scan(&count)
	return count, err
}

// DedupKey carries the Tier-1 hard-match signals for a receipt. Any field that
// is non-nil contributes a branch to the FindHardDuplicate lookup. A hit on
// any one signal is sufficient to treat the incoming receipt as a duplicate.
type DedupKey struct {
	ContentHash    *string
	GmailMessageID *string
	OrderID        *string
}

// HasAnySignal reports whether the key carries at least one non-nil signal.
func (k DedupKey) HasAnySignal() bool {
	return k.ContentHash != nil || k.GmailMessageID != nil || k.OrderID != nil
}

// FindHardDuplicate returns the existing primary receipt that matches any
// Tier-1 signal in key (content_hash, gmail_message_id, or order_id) for the
// given user. Duplicate rows (duplicate_of IS NOT NULL) are excluded so we
// always return the primary. Returns ErrNotFound when no signal hits.
func (r *ReceiptRepo) FindHardDuplicate(ctx context.Context, userID uuid.UUID, key DedupKey) (*domain.Receipt, error) {
	if !key.HasAnySignal() {
		return nil, domain.ErrNotFound
	}

	or := sq.Or{}
	if key.ContentHash != nil {
		or = append(or, sq.Eq{"content_hash": *key.ContentHash})
	}
	if key.GmailMessageID != nil {
		or = append(or, sq.Eq{"gmail_message_id": *key.GmailMessageID})
	}
	if key.OrderID != nil {
		or = append(or, sq.Eq{"order_id": *key.OrderID})
	}

	row := r.SQ.Select(receiptCols...).
		From("receipts").
		Where(sq.Eq{"user_id": userID.String()}).
		Where("duplicate_of IS NULL").
		Where(or).
		OrderBy("created_at ASC").
		Limit(1).
		QueryRowContext(ctx)

	rcpt, err := r.scanReceipt(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return rcpt, nil
}

// FindSoftDuplicate returns the existing primary receipt that matches the
// Tier-2 soft-match key: same merchant_key, same total, and a date within the
// given day window. The caller is responsible for computing merchant_key
// (alias-aware normalization). On a tie the row with the "best" status
// (matched > suggested > unmatched) and earliest created_at wins.
func (r *ReceiptRepo) FindSoftDuplicate(ctx context.Context, userID uuid.UUID, merchantKey string, total float64, date time.Time, dateWindowDays int) (*domain.Receipt, error) {
	if merchantKey == "" {
		return nil, domain.ErrNotFound
	}
	if dateWindowDays < 0 {
		dateWindowDays = 0
	}

	// Half-open range [lo, hiExclusive) — lets SQLite's lexicographic TEXT
	// comparison treat an RFC3339 value like "2026-02-01T00:00:00Z" as
	// on-date, and lets Postgres's DATE comparison work the same way.
	lo := date.AddDate(0, 0, -dateWindowDays).Format("2006-01-02")
	hiExclusive := date.AddDate(0, 0, dateWindowDays+1).Format("2006-01-02")

	row := r.SQ.Select(receiptCols...).
		From("receipts").
		Where(sq.Eq{"user_id": userID.String()}).
		Where("duplicate_of IS NULL").
		Where(sq.Eq{"merchant_key": merchantKey}).
		Where(sq.Eq{"total": total}).
		Where(sq.GtOrEq{"date": lo}).
		Where(sq.Lt{"date": hiExclusive}).
		OrderBy("CASE status WHEN 'matched' THEN 0 WHEN 'suggested' THEN 1 WHEN 'unmatched' THEN 2 ELSE 3 END ASC", "created_at ASC").
		Limit(1).
		QueryRowContext(ctx)

	rcpt, err := r.scanReceipt(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return rcpt, nil
}

// --- scan helpers ---

func (r *ReceiptRepo) scanReceipt(s scanner) (*domain.Receipt, error) {
	var rcpt domain.Receipt
	var idStr, userIDStr string
	var createdAt, updatedAt ScannableTime
	var dateVal ScannableTime
	var lineItemsStr sql.NullString
	var duplicateOfStr sql.NullString

	err := s.Scan(
		&idStr, &userIDStr, &rcpt.ImageURL, &rcpt.ImagePath, &rcpt.ThumbnailURL,
		&rcpt.MerchantName, &rcpt.MerchantAddress, &rcpt.MerchantCity, &rcpt.MerchantState, &rcpt.Total, &rcpt.Currency, &rcpt.TotalUSD,
		&rcpt.Subtotal, &rcpt.Tax, &rcpt.Tip, &rcpt.PaymentMethod, &rcpt.LastFourDigits,
		&dateVal, &rcpt.TransactionTime, &rcpt.RawText, &rcpt.OCRError, &lineItemsStr,
		&rcpt.Source, &rcpt.Status,
		&rcpt.ContentHash, &rcpt.GmailMessageID, &rcpt.OrderID, &rcpt.MerchantKey, &duplicateOfStr,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	rcpt.ID = ScanUUID(idStr)
	rcpt.UserID = ScanUUID(userIDStr)
	rcpt.Date = dateVal.Val
	if createdAt.Val != nil {
		rcpt.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		rcpt.UpdatedAt = *updatedAt.Val
	}
	if lineItemsStr.Valid && lineItemsStr.String != "" {
		rcpt.LineItems = json.RawMessage(lineItemsStr.String)
	} else {
		rcpt.LineItems = json.RawMessage("[]")
	}
	if duplicateOfStr.Valid && duplicateOfStr.String != "" {
		id := ScanUUID(duplicateOfStr.String)
		rcpt.DuplicateOf = &id
	}

	return &rcpt, nil
}

func (r *ReceiptRepo) scanReceiptWithMatch(s scanner) (*domain.ReceiptWithMatch, error) {
	var rwm domain.ReceiptWithMatch
	var idStr, userIDStr string
	var createdAt, updatedAt ScannableTime
	var dateVal ScannableTime
	var lineItemsStr sql.NullString
	var duplicateOfStr sql.NullString

	// match fields — all nullable from LEFT JOIN
	var matchID, matchReceiptID, matchTxnID, matchAccountID sql.NullString
	var matchConfidence sql.NullFloat64
	var matchMethod, matchReason sql.NullString
	var matchedAt ScannableTime

	err := s.Scan(
		&idStr, &userIDStr, &rwm.ImageURL, &rwm.ImagePath, &rwm.ThumbnailURL,
		&rwm.MerchantName, &rwm.MerchantAddress, &rwm.MerchantCity, &rwm.MerchantState, &rwm.Total, &rwm.Currency, &rwm.TotalUSD,
		&rwm.Subtotal, &rwm.Tax, &rwm.Tip, &rwm.PaymentMethod, &rwm.LastFourDigits,
		&dateVal, &rwm.TransactionTime, &rwm.RawText, &rwm.OCRError, &lineItemsStr,
		&rwm.Source, &rwm.Status,
		&rwm.ContentHash, &rwm.GmailMessageID, &rwm.OrderID, &rwm.MerchantKey, &duplicateOfStr,
		&createdAt, &updatedAt,
		&matchID, &matchReceiptID, &matchTxnID, &matchAccountID,
		&matchConfidence, &matchMethod, &matchReason, &matchedAt,
	)
	if err != nil {
		return nil, err
	}

	rwm.ID = ScanUUID(idStr)
	rwm.UserID = ScanUUID(userIDStr)
	rwm.Date = dateVal.Val
	if createdAt.Val != nil {
		rwm.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		rwm.UpdatedAt = *updatedAt.Val
	}
	if lineItemsStr.Valid && lineItemsStr.String != "" {
		rwm.LineItems = json.RawMessage(lineItemsStr.String)
	} else {
		rwm.LineItems = json.RawMessage("[]")
	}
	if duplicateOfStr.Valid && duplicateOfStr.String != "" {
		id := ScanUUID(duplicateOfStr.String)
		rwm.DuplicateOf = &id
	}

	if matchID.Valid {
		rwm.Match = &domain.ReceiptMatch{
			ID:              ScanUUID(matchID.String),
			ReceiptID:       ScanUUID(matchReceiptID.String),
			TransactionID:   matchTxnID.String,
			AccountID:       matchAccountID.String,
			ConfidenceScore: matchConfidence.Float64,
			MatchMethod:     matchMethod.String,
			MatchReason:     matchReason.String,
		}
		if matchedAt.Val != nil {
			rwm.Match.MatchedAt = *matchedAt.Val
		}
		if matchTxnID.Valid {
			rwm.LinkedTransactionID = &matchTxnID.String
		}
	}

	return &rwm, nil
}

// --- filter / order helpers (squirrel) ---

func applyReceiptFiltersSQ(qb sq.SelectBuilder, f domain.ReceiptFilter, prefix string) sq.SelectBuilder {
	if f.Status != "" {
		qb = qb.Where(sq.Eq{prefix + "status": f.Status})
	}
	if f.Source != "" {
		qb = qb.Where(sq.Eq{prefix + "source": f.Source})
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		qb = qb.Where(sq.Or{
			sq.Expr("LOWER("+prefix+"merchant_name) LIKE LOWER(?)", like),
			sq.Expr("LOWER("+prefix+"raw_text) LIKE LOWER(?)", like),
		})
	}
	if f.DateFrom != "" {
		qb = qb.Where(sq.GtOrEq{prefix + "date": f.DateFrom})
	}
	if f.DateTo != "" {
		qb = qb.Where(sq.LtOrEq{prefix + "date": f.DateTo})
	}
	if f.AmountMin != nil {
		qb = qb.Where(sq.GtOrEq{prefix + "total": *f.AmountMin})
	}
	if f.AmountMax != nil {
		qb = qb.Where(sq.LtOrEq{prefix + "total": *f.AmountMax})
	}
	return qb
}

func receiptOrderBySQ(f domain.ReceiptFilter, prefix string) string {
	col := prefix + "created_at"
	dir := "DESC"

	switch strings.ToLower(f.SortBy) {
	case "date":
		col = prefix + "date"
	case "amount", "total":
		col = prefix + "total"
	case "merchant", "merchant_name":
		col = prefix + "merchant_name"
	}

	if strings.EqualFold(f.SortOrder, "asc") {
		dir = "ASC"
	}

	// The id column is a stable, unique tie-breaker so consecutive offset pages
	// never overlap or skip rows when the primary sort key has ties.
	return fmt.Sprintf("%s %s, %sid", col, dir, prefix)
}
