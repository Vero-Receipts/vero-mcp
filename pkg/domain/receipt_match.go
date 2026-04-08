package domain

import (
	"time"

	"github.com/google/uuid"
)

type ReceiptMatch struct {
	ID              uuid.UUID `json:"id"`
	ReceiptID       uuid.UUID `json:"receipt_id"`
	TransactionID   string    `json:"transaction_id"`
	AccountID       string    `json:"account_id,omitempty"`
	ConfidenceScore float64   `json:"confidence_score"`
	MatchMethod     string    `json:"match_method"` // auto, manual, suggested
	MatchReason     string    `json:"match_reason,omitempty"`
	MatchedAt       time.Time `json:"matched_at"`
}
