package domain

import "github.com/google/uuid"

// Aggregated spending, computed in the database rather than by walking the
// transaction list in a browser.
//
// Every query behind these types applies the same two rules — a positive amount
// (Plaid's sign convention for an expense) and a resolved primary that is not
// money movement — so the figures reconcile with each other and with the
// transaction list's own total_spent.

// Granularity is the width of one bar of a chart.
type Granularity string

const (
	GranularityDay   Granularity = "day"
	GranularityWeek  Granularity = "week"
	GranularityMonth Granularity = "month"
)

// ParseGranularity reads a caller's granularity, defaulting to months. An
// unrecognised value is months rather than an error: a chart drawn at the wrong
// width is a better answer than no chart.
func ParseGranularity(raw string) Granularity {
	switch Granularity(raw) {
	case GranularityDay:
		return GranularityDay
	case GranularityWeek:
		return GranularityWeek
	default:
		return GranularityMonth
	}
}

// ReportFilter scopes a spending report to one user and a date window.
//
// To is optional and unset by default: a transaction dated in the future still
// counts toward the current window, which is what the clients have always done
// with pending authorizations.
type ReportFilter struct {
	UserID uuid.UUID
	From   string // YYYY-MM-DD, inclusive; "" for no lower bound
	To     string // YYYY-MM-DD, inclusive; "" for no upper bound

	// PFCPrimary narrows the whole report to one category, for a drill-down.
	// Empty covers every category.
	PFCPrimary string

	// Granularity is the width of a bucket in the over-time breakdown. It does
	// not affect any other figure.
	Granularity Granularity
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

// BucketCategoryAmount is spending in one time bucket within one primary
// category. The reporting service pivots these into per-bucket totals; the
// query returns them flat because the set of categories is open-ended.
type BucketCategoryAmount struct {
	// Bucket is the date the bucket starts on: YYYY-MM-DD for days and weeks,
	// YYYY-MM for months. Sorts lexicographically either way.
	Bucket  string
	Primary string
	Amount  float64
}

// IncomeSource is money arriving from one payer over the window.
//
// Only transactions whose resolved category is INCOME count. A refund is also a
// credit, but it is money coming back rather than coming in — counting it would
// draw a returned purchase as earnings from the shop that took it back.
type IncomeSource struct {
	Name   string
	Amount float64
	Count  int
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

// ExpenseRow is one line of an expense export: a receipt line item, or a whole
// purchase when nothing itemized it.
//
// The transaction-level money columns repeat across every item of one purchase,
// so a writer emits them on the first row only — otherwise summing the amount
// column in a spreadsheet counts an itemized purchase once per item.
type ExpenseRow struct {
	Date           string
	Merchant       string
	Category       string
	Subcategory    string
	Pending        bool
	Recurring      bool
	Amount         float64
	PaymentChannel string
	TransactionID  string
	// MatchMethod says how the receipt came to be attached — automatically, by
	// hand, or inherited from an earlier charge of the same subscription.
	MatchMethod string

	ReceiptSubtotal float64
	ReceiptTax      float64
	ReceiptTip      float64
	ReceiptTotal    float64
	ReceiptSource   string
	PaymentMethod   string
	MerchantAddress string
	MerchantCity    string
	MerchantState   string
	PurchaseTime    string
	OrderNumber     string

	// HasItem distinguishes a purchase with one line item from one with none.
	HasItem         bool
	LineNumber      int
	ItemDescription string
	Quantity        float64
	UnitPrice       float64
	LineTotal       float64
}
