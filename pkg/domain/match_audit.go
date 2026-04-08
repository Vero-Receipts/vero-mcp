package domain

import (
	"time"

	"github.com/google/uuid"
)

// MatchAuditEntry records a single matching decision for observability.
type MatchAuditEntry struct {
	ID                  uuid.UUID `json:"id"`
	ReceiptID           uuid.UUID `json:"receipt_id"`
	TransactionID       string    `json:"transaction_id"`
	AmountScore         float64   `json:"amount_score"`
	DateScore           float64   `json:"date_score"`
	MerchantScore       float64   `json:"merchant_score"`
	CompositeScore      float64   `json:"composite_score"`
	LLMUsed             bool      `json:"llm_used"`
	LLMMerchantConfirm  *bool     `json:"llm_merchant_confirm,omitempty"`
	Outcome             string    `json:"outcome"` // "auto", "suggested", "rejected"
	Reason              string    `json:"reason,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// CandidateScores holds the deterministic scores for a single candidate.
type CandidateScores struct {
	TransactionID  string
	AmountScore    float64
	DateScore      float64
	MerchantScore  float64
	CompositeScore float64
	AmountDiffPct  float64 // for logging
	DateDiffDays   int     // for logging
	MerchantMethod string  // "exact", "substring", "alias", "levenshtein", "word_overlap", "none"
}
