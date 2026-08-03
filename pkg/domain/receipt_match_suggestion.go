package domain

import (
	"time"

	"github.com/google/uuid"
)

// Match flags name the one dimension that kept a candidate out of the
// auto-match band, in the order they are reported when several apply.
const (
	FlagFXSuspected      = "fx_suspected"
	FlagNoAmount         = "no_amount"         // receipt total unreadable
	FlagNoDate           = "no_date"           // receipt date unreadable
	FlagNoMerchant       = "no_merchant"       // receipt merchant unreadable
	FlagMerchantMismatch = "merchant_mismatch" // merchant names disagree
	FlagAmountUpward     = "amount_upward"     // bank charged more than the receipt
	FlagAmountMismatch   = "amount_mismatch"
	FlagDateMismatch     = "date_mismatch"
	FlagClean            = "clean"
)

// ReceiptMatchSuggestion is a proposed receipt↔transaction link awaiting the
// user's decision. Unlike ReceiptMatch it is non-exclusive: a receipt may have
// several ranked suggestions and a transaction may appear in several, so a
// wrong guess never reserves a transaction away from the right receipt.
//
// The three per-dimension scores are nil when the receipt carried no value for
// that dimension — see CandidateScores for why that differs from a zero score.
type ReceiptMatchSuggestion struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	ReceiptID      uuid.UUID  `json:"receipt_id"`
	TransactionID  string     `json:"transaction_id"`
	AccountID      string     `json:"account_id,omitempty"`
	AmountScore    *float64   `json:"amount_score"`
	DateScore      *float64   `json:"date_score"`
	MerchantScore  *float64   `json:"merchant_score"`
	CompositeScore float64    `json:"composite_score"`
	AmountDiffPct  *float64   `json:"amount_diff_pct,omitempty"`
	DateDiffDays   *int       `json:"date_diff_days,omitempty"`
	MerchantMethod string     `json:"merchant_method,omitempty"`
	Flag           string     `json:"flag"`
	Reason         string     `json:"reason,omitempty"`
	Rank           int        `json:"rank"`
	LLMUsed        bool       `json:"llm_used"`
	CreatedAt      time.Time  `json:"created_at"`
	RejectedAt     *time.Time `json:"rejected_at,omitempty"`
}

// SuggestionWithTransaction is a suggestion hydrated with the transaction it
// points at, for clients that render the pair side by side.
type SuggestionWithTransaction struct {
	ReceiptMatchSuggestion
	Transaction *Transaction `json:"transaction,omitempty"`
}

// SuggestionWithReceipt is the mirror: a suggestion hydrated with the receipt,
// for the transaction-side surfaces.
type SuggestionWithReceipt struct {
	ReceiptMatchSuggestion
	Receipt *Receipt `json:"receipt,omitempty"`
}
