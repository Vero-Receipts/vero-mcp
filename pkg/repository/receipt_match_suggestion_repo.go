package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type ReceiptMatchSuggestionRepo struct {
	BaseRepo
}

func NewReceiptMatchSuggestionRepo(db *sql.DB, dialect Dialect) *ReceiptMatchSuggestionRepo {
	return &ReceiptMatchSuggestionRepo{NewBaseRepo(db, dialect)}
}

var rmsCols = []string{
	"id", "user_id", "receipt_id", "transaction_id", "account_id",
	"amount_score", "date_score", "merchant_score", "composite_score",
	"amount_diff_pct", "date_diff_days", "COALESCE(merchant_method, '')",
	"flag", "COALESCE(reason, '')", "rank", "llm_used", "created_at", "rejected_at",
}

// pendingOnly narrows to proposals still awaiting a decision.
//
// Two things disqualify a row. The user having already dismissed it is the
// obvious one. The other is the receipt having been settled against some
// transaction in the meantime: a receipt that is spoken for must not be
// offered anywhere else, and enforcing that here rather than at each write
// keeps it true no matter which path did the settling — manual links and
// recurring propagation both create matches without touching this table.
func pendingOnly(qb sq.SelectBuilder) sq.SelectBuilder {
	return qb.
		Where("rejected_at IS NULL").
		Where(`NOT EXISTS (
			SELECT 1 FROM receipt_matches rm
			WHERE rm.receipt_id = receipt_match_suggestions.receipt_id)`)
}

// ReplaceForReceipt swaps in a fresh set of proposals for one receipt.
//
// Rejected pairs are left untouched: the user's "not a match" is a durable
// verdict, and re-proposing it on the next sync is exactly the churn this
// table exists to prevent. Callers are expected to have already filtered the
// rejected pairs out of their candidate set; the ON CONFLICT clause is the
// backstop that keeps a stale caller from resurrecting one.
func (r *ReceiptMatchSuggestionRepo) ReplaceForReceipt(ctx context.Context, receiptID uuid.UUID, suggestions []domain.ReceiptMatchSuggestion) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	del := r.SQ.Delete("receipt_match_suggestions").
		Where(sq.Eq{"receipt_id": receiptID.String()}).
		Where("rejected_at IS NULL")
	query, args, err := del.ToSql()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}

	for i := range suggestions {
		s := &suggestions[i]
		if s.ID == uuid.Nil {
			s.ID = uuid.New()
		}
		ins := r.SQ.Insert("receipt_match_suggestions").
			Columns("id", "user_id", "receipt_id", "transaction_id", "account_id",
				"amount_score", "date_score", "merchant_score", "composite_score",
				"amount_diff_pct", "date_diff_days", "merchant_method",
				"flag", "reason", "rank", "llm_used").
			Values(s.ID.String(), s.UserID.String(), s.ReceiptID.String(), s.TransactionID, s.AccountID,
				s.AmountScore, s.DateScore, s.MerchantScore, s.CompositeScore,
				s.AmountDiffPct, s.DateDiffDays, s.MerchantMethod,
				s.Flag, s.Reason, s.Rank, s.LLMUsed).
			Suffix("ON CONFLICT (receipt_id, transaction_id) DO NOTHING")
		query, args, err := ins.ToSql()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// FindByReceiptID returns a receipt's pending proposals, best first.
func (r *ReceiptMatchSuggestionRepo) FindByReceiptID(ctx context.Context, receiptID uuid.UUID) ([]domain.ReceiptMatchSuggestion, error) {
	rows, err := pendingOnly(r.SQ.Select(rmsCols...).
		From("receipt_match_suggestions").
		Where(sq.Eq{"receipt_id": receiptID.String()})).
		OrderBy("rank ASC", "composite_score DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanMany(rows)
}

// FindByTransactionID returns the pending proposals pointing at one
// transaction, best first.
func (r *ReceiptMatchSuggestionRepo) FindByTransactionID(ctx context.Context, txID string) ([]domain.ReceiptMatchSuggestion, error) {
	rows, err := pendingOnly(r.SQ.Select(rmsCols...).
		From("receipt_match_suggestions").
		Where(sq.Eq{"transaction_id": txID})).
		OrderBy("composite_score DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanMany(rows)
}

func (r *ReceiptMatchSuggestionRepo) FindPair(ctx context.Context, receiptID uuid.UUID, txID string) (*domain.ReceiptMatchSuggestion, error) {
	row := pendingOnly(r.SQ.Select(rmsCols...).
		From("receipt_match_suggestions").
		Where(sq.Eq{"receipt_id": receiptID.String(), "transaction_id": txID})).
		QueryRowContext(ctx)

	s, err := r.scanOne(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return s, nil
}

// MarkRejected records the user's verdict on one pair. The row is kept rather
// than deleted so the matcher can skip the pair forever after.
func (r *ReceiptMatchSuggestionRepo) MarkRejected(ctx context.Context, receiptID uuid.UUID, txID string) error {
	res, err := r.SQ.Update("receipt_match_suggestions").
		Set("rejected_at", sq.Expr("CURRENT_TIMESTAMP")).
		Where(sq.Eq{"receipt_id": receiptID.String(), "transaction_id": txID}).
		Where("rejected_at IS NULL").
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

// DeleteForReceipt clears a receipt's pending proposals without recording a
// verdict — used once the receipt is settled some other way.
func (r *ReceiptMatchSuggestionRepo) DeleteForReceipt(ctx context.Context, receiptID uuid.UUID) error {
	_, err := r.SQ.Delete("receipt_match_suggestions").
		Where(sq.Eq{"receipt_id": receiptID.String()}).
		Where("rejected_at IS NULL").
		ExecContext(ctx)
	return err
}

// DeleteForTransaction clears every pending proposal pointing at a transaction
// — used once that transaction is settled, so other receipts stop offering it.
func (r *ReceiptMatchSuggestionRepo) DeleteForTransaction(ctx context.Context, txID string) error {
	_, err := r.SQ.Delete("receipt_match_suggestions").
		Where(sq.Eq{"transaction_id": txID}).
		Where("rejected_at IS NULL").
		ExecContext(ctx)
	return err
}

// CountPendingByUser counts receipts with at least one pending proposal (not
// the proposals themselves — the user reviews receipts, not pairs).
func (r *ReceiptMatchSuggestionRepo) CountPendingByUser(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := pendingOnly(r.SQ.Select("COUNT(DISTINCT receipt_id)").
		From("receipt_match_suggestions").
		Where(sq.Eq{"user_id": userID.String()})).
		QueryRowContext(ctx).
		Scan(&count)
	return count, err
}

func (r *ReceiptMatchSuggestionRepo) scanMany(rows *sql.Rows) ([]domain.ReceiptMatchSuggestion, error) {
	var out []domain.ReceiptMatchSuggestion
	for rows.Next() {
		s, err := r.scanOne(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

func (r *ReceiptMatchSuggestionRepo) scanOne(s rowScanner) (*domain.ReceiptMatchSuggestion, error) {
	var out domain.ReceiptMatchSuggestion
	var idStr, userIDStr, receiptIDStr string
	var accountID sql.NullString
	var amountScore, dateScore, merchantScore, amountDiffPct sql.NullFloat64
	var dateDiffDays sql.NullInt64
	var createdAt, rejectedAt ScannableTime

	err := s.Scan(&idStr, &userIDStr, &receiptIDStr, &out.TransactionID, &accountID,
		&amountScore, &dateScore, &merchantScore, &out.CompositeScore,
		&amountDiffPct, &dateDiffDays, &out.MerchantMethod,
		&out.Flag, &out.Reason, &out.Rank, &out.LLMUsed, &createdAt, &rejectedAt)
	if err != nil {
		return nil, err
	}

	out.ID = ScanUUID(idStr)
	out.UserID = ScanUUID(userIDStr)
	out.ReceiptID = ScanUUID(receiptIDStr)
	out.AccountID = accountID.String
	if amountScore.Valid {
		out.AmountScore = &amountScore.Float64
	}
	if dateScore.Valid {
		out.DateScore = &dateScore.Float64
	}
	if merchantScore.Valid {
		out.MerchantScore = &merchantScore.Float64
	}
	if amountDiffPct.Valid {
		out.AmountDiffPct = &amountDiffPct.Float64
	}
	if dateDiffDays.Valid {
		d := int(dateDiffDays.Int64)
		out.DateDiffDays = &d
	}
	if createdAt.Val != nil {
		out.CreatedAt = *createdAt.Val
	}
	out.RejectedAt = rejectedAt.Val
	return &out, nil
}
