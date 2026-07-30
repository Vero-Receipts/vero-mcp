package domain

import (
	"time"

	"github.com/google/uuid"
)

// Common Plaid account types. Only card-spendable accounts (credit, depository)
// can back a POS purchase, so matchers filter on these.
const (
	PlaidAccountTypeCredit     = "credit"
	PlaidAccountTypeDepository = "depository"
)

// PlaidAccount is a single account under a Plaid Item, persisted from
// /accounts/get. Mask is the card's last 2-4 digits — the join key to a Square
// payment's last4 for transaction matching.
type PlaidAccount struct {
	ID           uuid.UUID `json:"id"`
	AccountID    string    `json:"account_id"` // Plaid account id; matches transaction_cache.account_id
	ItemID       string    `json:"item_id"`
	UserID       uuid.UUID `json:"user_id"`
	Mask         string    `json:"mask"`
	Name         string    `json:"name"`
	OfficialName string    `json:"official_name,omitempty"`
	Subtype      string    `json:"subtype"`
	Type         string    `json:"type"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// IsCardSpendable reports whether this account type can back a card purchase
// (and is therefore a candidate for POS/Square matching).
func (a PlaidAccount) IsCardSpendable() bool {
	return a.Type == PlaidAccountTypeCredit || a.Type == PlaidAccountTypeDepository
}
