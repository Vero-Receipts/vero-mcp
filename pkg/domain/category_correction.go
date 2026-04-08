package domain

import (
	"time"

	"github.com/google/uuid"
)

// CategoryCorrectionCache stores a confirmed category correction to avoid
// repeat LLM calls for the same merchant + original category combination.
type CategoryCorrectionCache struct {
	ID                   uuid.UUID `json:"id"`
	MerchantCanonical    string    `json:"merchant_canonical"`
	OriginalPFCPrimary   string    `json:"original_pfc_primary"`
	OriginalPFCDetailed  string    `json:"original_pfc_detailed"`
	CorrectedPFCPrimary  string    `json:"corrected_pfc_primary"`
	CorrectedPFCDetailed string    `json:"corrected_pfc_detailed"`
	Source               string    `json:"source"` // "rule", "llm"
	SampleLineItems      *string   `json:"sample_line_items,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

// CategoryCorrectionResult is the outcome of the category correction pipeline.
type CategoryCorrectionResult struct {
	ShouldCorrect        bool   `json:"should_correct"`
	CorrectedPFCPrimary  string `json:"corrected_pfc_primary"`
	CorrectedPFCDetailed string `json:"corrected_pfc_detailed"`
	Source               string `json:"source"` // "rule", "llm", "cache"
	Reason               string `json:"reason"`
}
