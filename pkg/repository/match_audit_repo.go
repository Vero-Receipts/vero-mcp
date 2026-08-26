package repository

import (
	"context"
	"database/sql"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type MatchAuditRepo struct {
	BaseRepo
}

func NewMatchAuditRepo(db *sql.DB, dialect Dialect) *MatchAuditRepo {
	return &MatchAuditRepo{NewBaseRepo(db, dialect)}
}

func (r *MatchAuditRepo) Create(ctx context.Context, entry *domain.MatchAuditEntry) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("match_audit_log").
		Columns("id", "receipt_id", "transaction_id", "amount_score", "date_score",
			"merchant_score", "composite_score", "llm_used", "llm_merchant_confirm",
			"llm_confidence", "outcome", "reason").
		Values(entry.ID.String(), entry.ReceiptID.String(), entry.TransactionID,
			entry.AmountScore, entry.DateScore, entry.MerchantScore, entry.CompositeScore,
			entry.LLMUsed, entry.LLMMerchantConfirm, entry.LLMConfidence, entry.Outcome, entry.Reason).
		Suffix("RETURNING created_at").
		ToSql()
	if err != nil {
		return err
	}

	row := r.DB.QueryRowContext(ctx, query, args...)

	var createdAt ScannableTime
	if err := row.Scan(&createdAt); err != nil {
		return err
	}
	if createdAt.Val != nil {
		entry.CreatedAt = *createdAt.Val
	}
	return nil
}
