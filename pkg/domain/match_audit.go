package domain

import (
	"time"

	"github.com/google/uuid"
)

// MatchAuditEntry records a single matching decision for observability.
type MatchAuditEntry struct {
	ID                 uuid.UUID `json:"id"`
	ReceiptID          uuid.UUID `json:"receipt_id"`
	TransactionID      string    `json:"transaction_id"`
	AmountScore        float64   `json:"amount_score"`
	DateScore          float64   `json:"date_score"`
	MerchantScore      float64   `json:"merchant_score"`
	CompositeScore     float64   `json:"composite_score"`
	LLMUsed            bool      `json:"llm_used"`
	LLMMerchantConfirm *bool     `json:"llm_merchant_confirm,omitempty"`
	Outcome            string    `json:"outcome"` // "auto", "suggested", "rejected"
	Reason             string    `json:"reason,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// CandidateScores holds the deterministic scores for a single candidate.
//
// A dimension's score of 0 and a dimension the receipt simply has no value for
// are different facts, and the decision rule turns on the difference: a
// contradicting amount or date vetoes the candidate outright, whereas an
// unreadable one is merely unknown and leaves the other two dimensions to
// carry the decision. The *Known flags carry that distinction — when one is
// false its score is meaningless and must not be read.
type CandidateScores struct {
	TransactionID        string
	AmountScore          float64
	DateScore            float64
	MerchantScore        float64
	CompositeScore       float64
	AmountKnown          bool
	DateKnown            bool
	MerchantKnown        bool
	AmountDiffPct        float64 // for logging
	DateDiffDays         int     // for logging
	MerchantMethod       string  // "exact", "substring", "alias", "levenshtein", "word_overlap", "none"
	ChargeExceedsReceipt bool    // true when tx charged more than receipt by 5–35% (fees/charges not on receipt)
}

// Composite weights. A dimension the receipt has no value for is dropped from
// the sum and the remainder is renormalized, so scores stay comparable across
// candidates scored on different subsets of dimensions.
const (
	WeightAmount   = 0.35
	WeightDate     = 0.25
	WeightMerchant = 0.40
)

// Composite returns the weighted score over the known dimensions only.
func (cs *CandidateScores) Composite() float64 {
	var sum, weight float64
	if cs.AmountKnown {
		sum += cs.AmountScore * WeightAmount
		weight += WeightAmount
	}
	if cs.DateKnown {
		sum += cs.DateScore * WeightDate
		weight += WeightDate
	}
	if cs.MerchantKnown {
		sum += cs.MerchantScore * WeightMerchant
		weight += WeightMerchant
	}
	if weight == 0 {
		return 0
	}
	return sum / weight
}
