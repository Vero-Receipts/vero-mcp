package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Transaction is a transaction_cache row. MerchantID is the FK; Merchant is
// populated from the JOIN on read so callers don't need a second query. None
// of the merchant identity fields (name, logo, domain) are stored on the
// transaction itself — they belong to the merchant record.
type Transaction struct {
	ID             uuid.UUID       `json:"id,omitempty"`
	UserID         uuid.UUID       `json:"user_id,omitempty"`
	TransactionID  string          `json:"transaction_id"`
	AccountID      string          `json:"account_id"`
	Amount         float64         `json:"amount"`
	Date           string          `json:"date"`
	DateTime       *time.Time      `json:"datetime,omitempty"`
	Name           string          `json:"name"`
	MerchantID     *uuid.UUID      `json:"merchant_id,omitempty"`
	Merchant       *Merchant       `json:"merchant,omitempty"`
	Category       json.RawMessage `json:"category,omitempty"`
	PFCPrimary     *string         `json:"pfc_primary,omitempty"`
	PFCDetailed    *string         `json:"pfc_detailed,omitempty"`
	PaymentChannel *string         `json:"payment_channel,omitempty"`
	Pending              bool            `json:"pending"`
	SyncedAt             time.Time       `json:"synced_at,omitempty"`
	CorrectedPFCPrimary  *string         `json:"corrected_pfc_primary,omitempty"`
	CorrectedPFCDetailed *string         `json:"corrected_pfc_detailed,omitempty"`
	CategoryCorrectedAt  *time.Time      `json:"category_corrected_at,omitempty"`
}


// TransactionWithReceipt combines a cached transaction with its matched receipt (if any).
type TransactionWithReceipt struct {
	Transaction
	Receipt *AttachedReceipt
}

// AttachedReceipt is the receipt summary embedded inside a TransactionResponse.
type AttachedReceipt struct {
	ID              string          `json:"id"`
	ImageURL        string          `json:"imageUrl"`
	ThumbnailURL    *string         `json:"thumbnailUrl"`
	MerchantName    *string         `json:"merchantName,omitempty"`
	Total           *float64        `json:"total,omitempty"`
	Subtotal        *float64        `json:"subtotal,omitempty"`
	Tax             *float64        `json:"tax,omitempty"`
	Tip             *float64        `json:"tip,omitempty"`
	PaymentMethod   *string         `json:"paymentMethod,omitempty"`
	LastFourDigits  *string         `json:"lastFourDigits,omitempty"`
	Date            *time.Time      `json:"date,omitempty"`
	LineItems       json.RawMessage `json:"lineItems,omitempty"`
	MatchMethod     string          `json:"matchMethod"`
	ConfidenceScore float64         `json:"confidenceScore"`
}

// TransactionFilter holds optional API query parameters for filtering the transaction list.
type TransactionFilter struct {
	Search     string   // ILIKE on name / merchant_name
	DateFrom   string   // YYYY-MM-DD
	DateTo     string   // YYYY-MM-DD
	AmountMin  *float64 // minimum absolute amount
	AmountMax  *float64 // maximum absolute amount
	Category    string   // substring match inside the category JSON array
	PFCPrimary  string   // exact (case-insensitive) match on pfc_primary
	PFCDetailed string   // exact (case-insensitive) match on pfc_detailed
	Matched    string   // "matched", "unmatched", or "" for all
	Pending    string   // "true", "false", or "" for all
	SortBy     string   // "date", "amount", "merchant", "name" (default: "date")
	SortOrder  string   // "asc" or "desc" (default: "desc")
	Limit      int      // page size; when > 0 the query is paginated
	Offset     int      // page offset; ignored unless Limit > 0
}

// TransactionResponse is what the API returns to clients (camelCase).
type TransactionResponse struct {
	ID             string           `json:"id"`
	AccountID      string           `json:"accountId"`
	Amount         float64          `json:"amount"`
	Date           string           `json:"date"`
	DateTime       *time.Time       `json:"datetime,omitempty"`
	Name           string           `json:"name"`
	MerchantName   *string          `json:"merchantName"`
	Category       []string         `json:"category"`
	PFCPrimary     *string          `json:"pfcPrimary,omitempty"`
	PFCDetailed    *string          `json:"pfcDetailed,omitempty"`
	PaymentChannel *string          `json:"paymentChannel,omitempty"`
	Pending              bool             `json:"pending"`
	MerchantLogo         *string          `json:"merchantLogo"`
	CorrectedPFCPrimary  *string          `json:"correctedPfcPrimary,omitempty"`
	CorrectedPFCDetailed *string          `json:"correctedPfcDetailed,omitempty"`
	Receipt              *AttachedReceipt `json:"receipt,omitempty"`
}
