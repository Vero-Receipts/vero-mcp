package domain

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Receipt struct {
	ID              uuid.UUID       `json:"id"`
	UserID          uuid.UUID       `json:"user_id"`
	ImageURL        string          `json:"image_url,omitempty"`
	ImagePath       string          `json:"image_path,omitempty"`
	ThumbnailURL    *string         `json:"thumbnail_url"`
	MerchantName    *string         `json:"merchant_name,omitempty"`
	MerchantAddress *string         `json:"merchant_address,omitempty"`
	MerchantCity    *string         `json:"merchant_city,omitempty"`  // parsed from MerchantAddress; drives receipt-based outlet resolution
	MerchantState   *string         `json:"merchant_state,omitempty"` // 2-letter, normalized
	Total           *float64        `json:"total,omitempty"`
	Currency        *string         `json:"currency,omitempty"`  // ISO 4217 (e.g. "USD", "MXN", "EUR")
	TotalUSD        *float64        `json:"total_usd,omitempty"` // FX-converted USD amount (nil if already USD)
	Subtotal        *float64        `json:"subtotal,omitempty"`
	Tax             *float64        `json:"tax,omitempty"`
	Tip             *float64        `json:"tip,omitempty"`
	PaymentMethod   *string         `json:"payment_method,omitempty"`
	LastFourDigits  *string         `json:"last_four_digits,omitempty"`
	Date            *time.Time      `json:"date,omitempty"`
	TransactionTime *string         `json:"transaction_time,omitempty"`
	RawText         *string         `json:"raw_text,omitempty"`
	OCRError        *string         `json:"ocr_error,omitempty"`
	LineItems       json.RawMessage `json:"line_items"`
	Source          string          `json:"source"`
	Status          string          `json:"status"`
	ContentHash     *string         `json:"content_hash,omitempty"`
	GmailMessageID  *string         `json:"gmail_message_id,omitempty"`
	OrderID         *string         `json:"order_id,omitempty"`
	MerchantKey     *string         `json:"merchant_key,omitempty"`
	DuplicateOf     *uuid.UUID      `json:"duplicate_of,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type LineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	Price       float64 `json:"price"` // total price for the line (quantity * unitPrice)
}

type OCRResult struct {
	RawText         string     `json:"raw_text"`
	LineItems       []LineItem `json:"line_items"`
	MerchantName    string     `json:"merchant_name,omitempty"`
	MerchantAddress string     `json:"merchant_address,omitempty"`
	MerchantCity    string     `json:"merchant_city,omitempty"`  // parsed from MerchantAddress
	MerchantState   string     `json:"merchant_state,omitempty"` // 2-letter, normalized
	TransactionDate string     `json:"transaction_date,omitempty"` // YYYY-MM-DD
	TransactionTime string     `json:"transaction_time,omitempty"` // HH:MM
	Subtotal        *float64   `json:"subtotal,omitempty"`
	Tax             *float64   `json:"tax,omitempty"`
	Tip             *float64   `json:"tip,omitempty"`
	Total           *float64   `json:"total,omitempty"`
	Currency        string     `json:"currency,omitempty"` // ISO 4217 (e.g. "USD", "MXN")
	PaymentMethod   string     `json:"payment_method,omitempty"`
	LastFourDigits  string     `json:"last_four_digits,omitempty"`
	Error           string     `json:"error,omitempty"`
}

// ReceiptWithMatch embeds a Receipt and optionally includes its match data.
// Used by the enhanced List/GetByID endpoints.
type ReceiptWithMatch struct {
	Receipt
	Match               *ReceiptMatch `json:"match,omitempty"`
	LinkedTransactionID *string       `json:"linked_transaction_id,omitempty"`
}

type ReceiptFilter struct {
	Status            string   // "matched", "unmatched", "suggested", or "" for all
	Source            string   // "upload", "email", or "" for all
	MatchMethod       string   // "auto", "manual", "suggested", "confirmed", or "" for all
	Search            string   // text search across merchant_name + line_items
	SortBy            string   // "date", "amount", "merchant", "created_at" (default)
	SortOrder         string   // "asc" or "desc" (default)
	DateFrom          string   // YYYY-MM-DD
	DateTo            string   // YYYY-MM-DD
	AmountMin         *float64 // minimum amount filter
	AmountMax         *float64 // maximum amount filter
	IncludeDuplicates bool     // when false (default), hides rows with duplicate_of set
	Limit             int      // page size; when > 0 the query is paginated
	Offset            int      // page offset; ignored unless Limit > 0
}
