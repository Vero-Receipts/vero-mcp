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
	ID            uuid.UUID  `json:"id,omitempty"`
	UserID        uuid.UUID  `json:"user_id,omitempty"`
	TransactionID string     `json:"transaction_id"`
	AccountID     string     `json:"account_id"`
	Amount        float64    `json:"amount"`
	Date          string     `json:"date"`
	DateTime      *time.Time `json:"datetime,omitempty"`
	Name          string     `json:"name"`
	MerchantID    *uuid.UUID `json:"merchant_id,omitempty"`
	Merchant      *Merchant  `json:"merchant,omitempty"`
	// Location is where THIS transaction happened (per-outlet), from Plaid's
	// transaction `location` object. Nil when Plaid gave none (e.g. online).
	Location *TransactionLocation `json:"location,omitempty"`
	// RawPayload is the complete Plaid transaction JSON, stored verbatim for audit
	// and future field extraction. Not read by any current query.
	RawPayload     json.RawMessage `json:"raw_payload,omitempty"`
	Category       json.RawMessage `json:"category,omitempty"`
	PFCPrimary     *string         `json:"pfc_primary,omitempty"`
	PFCDetailed    *string         `json:"pfc_detailed,omitempty"`
	PaymentChannel *string         `json:"payment_channel,omitempty"`
	Pending        bool            `json:"pending"`
	// Recurring marks a transaction as part of a recurring series (subscription/bill).
	// Set by the recurring-detection pass, not by Plaid. Drives the frontend badge and
	// renders independently of whether a receipt is attached.
	Recurring bool      `json:"recurring"`
	SyncedAt  time.Time `json:"synced_at,omitempty"`
	// PlaidPFC* are the category Plaid assigned, refreshed on every sync.
	// PFCPrimary/PFCDetailed above are the effective category a client renders,
	// which a correction may overwrite; the two differ exactly when this
	// transaction has been corrected.
	PlaidPFCPrimary     *string    `json:"plaid_pfc_primary,omitempty"`
	PlaidPFCDetailed    *string    `json:"plaid_pfc_detailed,omitempty"`
	CategoryCorrectedAt *time.Time `json:"category_corrected_at,omitempty"`
}

// TransactionLocation mirrors Plaid's transaction `location` object. Persisted as a
// JSONB column on transaction_cache. It is the per-outlet discriminator for
// user↔catalog matching (City drives outlet resolution; the rest is kept for future
// needs). All fields optional; a transaction with no Plaid location has a nil pointer.
type TransactionLocation struct {
	Address     *string  `json:"address,omitempty"`
	City        *string  `json:"city,omitempty"`
	Region      *string  `json:"region,omitempty"`
	PostalCode  *string  `json:"postal_code,omitempty"`
	Country     *string  `json:"country,omitempty"`
	Lat         *float64 `json:"lat,omitempty"`
	Lon         *float64 `json:"lon,omitempty"`
	StoreNumber *string  `json:"store_number,omitempty"`
}

// TransactionWithReceipt combines a cached transaction with its matched receipt (if any).
type TransactionWithReceipt struct {
	Transaction
	Receipt *AttachedReceipt
	// Suggested is the best pending proposal for this transaction, when it has
	// no settled receipt. Populated so a list can distinguish "we think this
	// receipt belongs here, confirm?" from a settled attachment without a
	// follow-up request per row.
	Suggested *SuggestedReceipt
}

// SuggestedReceipt is the summary of a proposed (not settled) receipt for a
// transaction. Deliberately separate from AttachedReceipt: conflating the two
// is what made a mere guess render as a confirmed match.
type SuggestedReceipt struct {
	ReceiptID      string   `json:"receiptId"`
	ImageURL       string   `json:"imageUrl,omitempty"`
	ThumbnailURL   *string  `json:"thumbnailUrl,omitempty"`
	MerchantName   *string  `json:"merchantName,omitempty"`
	Total          *float64 `json:"total,omitempty"`
	Confidence     float64  `json:"confidence"`
	Flag           string   `json:"flag"`
	Reason         string   `json:"reason,omitempty"`
	AlternateCount int      `json:"alternateCount"` // other pending proposals for this transaction
}

// RecurringCandidate is one transaction row consumed by recurring detection: the
// transaction's key facts plus, when it carries a REAL (non-derived) receipt match, that
// receipt's id and subscription flag. Built from transaction_cache ⋈ receipt_matches ⋈
// receipts — one row per transaction (a transaction has at most one match).
type RecurringCandidate struct {
	TransactionID  string
	MerchantID     uuid.UUID
	MerchantName   string // raw transaction merchant name, for display/diagnostics
	Date           string // YYYY-MM-DD
	Amount         float64
	Recurring      bool       // the transaction's current recurring flag
	Matched        bool       // has ANY receipt match (real or derived)
	SourceReceipt  *uuid.UUID // receipt id of its REAL match, if any (match_method <> 'recurring')
	IsSubscription *bool      // that source receipt's subscription flag
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
	// TransactionID pins the query to one Plaid transaction id. Distinct from
	// Search, which only matches name / merchant_name — this is how a detail
	// view fetches a single row without paging the whole list.
	TransactionID string
	Search        string   // ILIKE on name / merchant_name
	DateFrom      string   // YYYY-MM-DD
	DateTo        string   // YYYY-MM-DD
	AmountMin     *float64 // minimum absolute amount
	AmountMax     *float64 // maximum absolute amount
	Category      string   // substring match inside the category JSON array
	PFCPrimary    string   // exact match on pfc_primary
	PFCDetailed   string   // exact match on pfc_detailed
	// PFCPrimaryNotIn / PFCDetailedNotIn exclude categories rather than select
	// one, which is how the "Other" branch of a chart names its rows: everything
	// the chart did not draw a slice for. An uncategorized row counts as
	// excluded from nothing, so it survives both.
	PFCPrimaryNotIn  []string
	PFCDetailedNotIn []string
	// PFCDetailedContains matches sub-categories by substring, which is how a
	// tax line selects its rows — "donations" within the non-profit category.
	// Any one match is enough.
	PFCDetailedContains []string
	Matched             string // "matched", "unmatched", or "" for all
	Pending             string // "true", "false", or "" for all
	SortBy              string // "date", "amount", "merchant", "name" (default: "date")
	SortOrder           string // "asc" or "desc" (default: "desc")
	Limit               int    // page size; when > 0 the query is paginated
	Offset              int    // page offset; ignored unless Limit > 0
}

// TransactionResponse is what the API returns to clients (camelCase).
type TransactionResponse struct {
	ID               string           `json:"id"`
	AccountID        string           `json:"accountId"`
	Amount           float64          `json:"amount"`
	Date             string           `json:"date"`
	DateTime         *time.Time       `json:"datetime,omitempty"`
	Name             string           `json:"name"`
	MerchantName     *string          `json:"merchantName"`
	Category         []string         `json:"category"`
	PFCPrimary       *string          `json:"pfcPrimary,omitempty"`
	PFCDetailed      *string          `json:"pfcDetailed,omitempty"`
	PaymentChannel   *string          `json:"paymentChannel,omitempty"`
	Pending          bool             `json:"pending"`
	Recurring        bool             `json:"recurring"`
	MerchantLogo     *string          `json:"merchantLogo"`
	PlaidPFCPrimary  *string          `json:"plaidPfcPrimary,omitempty"`
	PlaidPFCDetailed *string          `json:"plaidPfcDetailed,omitempty"`
	Receipt          *AttachedReceipt `json:"receipt,omitempty"`
	// SuggestedReceipt is populated only when Receipt is nil: a proposal the
	// user has not acted on yet. Kept in its own field so a client can never
	// mistake a guess for a settled attachment.
	SuggestedReceipt *SuggestedReceipt `json:"suggestedReceipt,omitempty"`
}
