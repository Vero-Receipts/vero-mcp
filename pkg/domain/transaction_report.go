package domain

import "github.com/google/uuid"

// Aggregated spending, computed in the database rather than by walking the
// transaction list in a browser.
//
// Every query behind these types applies the same two rules — a positive amount
// (Plaid's sign convention for an expense) and a resolved primary that is not
// money movement — so the figures reconcile with each other and with the
// transaction list's own total_spent.

// ReportFilter scopes a spending report to one user and a date window.
//
// To is optional and unset by default: a transaction dated in the future still
// counts toward the current window, which is what the clients have always done
// with pending authorizations.
type ReportFilter struct {
	UserID uuid.UUID
	From   string // YYYY-MM-DD, inclusive; "" for no lower bound
	To     string // YYYY-MM-DD, inclusive; "" for no upper bound
}

// SpendingTotals is the headline: what was spent in the window, over how many
// transactions, across how many distinct months.
//
// MonthsActive counts months that saw spending rather than months in the
// window, so an average built from it is not diluted by months the account did
// not exist.
type SpendingTotals struct {
	Spent            float64
	TransactionCount int
	MonthsActive     int
}

// CategoryAmount is one row of a grouping — by primary category, by detailed
// sub-category, or by merchant.
type CategoryAmount struct {
	// Key is the grouping value: a PFC primary, a PFC detailed value, or a
	// merchant's canonical name.
	Key string
	// Primary is the parent category. Empty for a primary-level grouping.
	Primary string
	Amount  float64
	Count   int
}

// MonthCategoryAmount is spending in one month within one primary category.
// The reporting service pivots these into per-month buckets; the query returns
// them flat because the set of categories is open-ended.
type MonthCategoryAmount struct {
	Month   string // YYYY-MM
	Primary string
	Amount  float64
}

// MerchantAmount is spending with one merchant. Transactions that never
// resolved to a merchant record are grouped under the name on the transaction
// itself rather than dropped, so the totals still add up.
type MerchantAmount struct {
	MerchantID *uuid.UUID
	Name       string
	LogoURL    string
	Amount     float64
	Count      int
}
