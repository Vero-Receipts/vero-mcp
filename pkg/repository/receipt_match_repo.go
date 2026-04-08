package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type ReceiptMatchRepo struct {
	BaseRepo
}

func NewReceiptMatchRepo(db *sql.DB, dialect Dialect) *ReceiptMatchRepo {
	return &ReceiptMatchRepo{NewBaseRepo(db, dialect)}
}

func (r *ReceiptMatchRepo) Create(ctx context.Context, m *domain.ReceiptMatch) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("receipt_matches").
		Columns("id", "receipt_id", "transaction_id", "account_id",
			"confidence_score", "match_method", "match_reason").
		Values(m.ID.String(), m.ReceiptID.String(), m.TransactionID, m.AccountID,
			m.ConfidenceScore, m.MatchMethod, m.MatchReason).
		Suffix("RETURNING matched_at").
		ToSql()
	if err != nil {
		return err
	}

	row := r.DB.QueryRowContext(ctx, query, args...)

	var matchedAt ScannableTime
	if err := row.Scan(&matchedAt); err != nil {
		return err
	}
	if matchedAt.Val != nil {
		m.MatchedAt = *matchedAt.Val
	}
	return nil
}

var rmCols = []string{
	"id", "receipt_id", "transaction_id", "account_id",
	"confidence_score", "match_method", "COALESCE(match_reason, '')", "matched_at",
}

func (r *ReceiptMatchRepo) FindByReceiptID(ctx context.Context, receiptID uuid.UUID) (*domain.ReceiptMatch, error) {
	row := r.SQ.Select(rmCols...).
		From("receipt_matches").
		Where(sq.Eq{"receipt_id": receiptID.String()}).
		QueryRowContext(ctx)

	m, err := r.scanReceiptMatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

func (r *ReceiptMatchRepo) FindByTransactionID(ctx context.Context, txID string) (*domain.ReceiptMatch, error) {
	row := r.SQ.Select(rmCols...).
		From("receipt_matches").
		Where(sq.Eq{"transaction_id": txID}).
		QueryRowContext(ctx)

	m, err := r.scanReceiptMatch(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

func (r *ReceiptMatchRepo) UpdateMethod(ctx context.Context, id uuid.UUID, method string) error {
	res, err := r.SQ.Update("receipt_matches").
		Set("match_method", method).
		Set("matched_at", sq.Expr("CURRENT_TIMESTAMP")).
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

func (r *ReceiptMatchRepo) DeleteByReceiptID(ctx context.Context, receiptID uuid.UUID) error {
	res, err := r.SQ.Delete("receipt_matches").
		Where(sq.Eq{"receipt_id": receiptID.String()}).
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

func (r *ReceiptMatchRepo) scanReceiptMatch(s rowScanner) (*domain.ReceiptMatch, error) {
	var m domain.ReceiptMatch
	var idStr, receiptIDStr string
	var accountID sql.NullString
	var matchedAt ScannableTime

	err := s.Scan(&idStr, &receiptIDStr, &m.TransactionID, &accountID,
		&m.ConfidenceScore, &m.MatchMethod, &m.MatchReason, &matchedAt)
	if err != nil {
		return nil, err
	}

	m.ID = ScanUUID(idStr)
	m.ReceiptID = ScanUUID(receiptIDStr)
	m.AccountID = accountID.String
	if matchedAt.Val != nil {
		m.MatchedAt = *matchedAt.Val
	}
	return &m, nil
}
