package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// reportFixture seeds one user and returns a repo plus that user's filter.
func reportFixture(t *testing.T) (*sql.DB, *TransactionReportRepo, domain.ReportFilter) {
	t.Helper()

	db := setupTestDB(t)
	userID := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, name) VALUES (?, ?)`, userID.String(), "Report User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	return db, NewTransactionReportRepo(db, DialectSQLite), domain.ReportFilter{UserID: userID}
}

type txnSeed struct {
	amount   float64
	date     string
	name     string
	primary  string // pfc_primary; "" leaves it NULL
	detailed string
	category string // legacy JSON array; "" means "[]"
	merchant *uuid.UUID
}

func seedTxn(t *testing.T, db *sql.DB, userID uuid.UUID, s txnSeed) {
	t.Helper()

	var primary, detailed, merchant any
	if s.primary != "" {
		primary = s.primary
	}
	if s.detailed != "" {
		detailed = s.detailed
	}
	if s.merchant != nil {
		merchant = s.merchant.String()
	}
	category := s.category
	if category == "" {
		category = "[]"
	}
	name := s.name
	if name == "" {
		name = "Test Transaction"
	}

	if _, err := db.Exec(
		`INSERT INTO transaction_cache
		   (id, user_id, transaction_id, account_id, amount, date, name, category,
		    pfc_primary, pfc_detailed, merchant_id, pending)
		 VALUES (?, ?, ?, 'acc_1', ?, ?, ?, ?, ?, ?, ?, 0)`,
		uuid.New().String(), userID.String(), uuid.New().String(),
		s.amount, s.date, name, category, primary, detailed, merchant,
	); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
}

func seedMerchant(t *testing.T, db *sql.DB, name, logo string) uuid.UUID {
	t.Helper()

	id := uuid.New()
	if _, err := db.Exec(
		`INSERT INTO merchants (id, canonical_name, normalized_key, logo_cdn_url) VALUES (?, ?, ?, ?)`,
		id.String(), name, name, logo,
	); err != nil {
		t.Fatalf("seed merchant: %v", err)
	}
	return id
}

// The rule that decides what a spending report counts: expenses only, money
// movement left out.
func TestSpendingExcludesRefundsAndMoneyMovement(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 100, date: "2026-03-04", primary: "FOOD_AND_DRINK"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 50, date: "2026-03-05", primary: "TRAVEL"})
	// A refund: negative under Plaid's convention, and not spending.
	seedTxn(t, db, filter.UserID, txnSeed{amount: -30, date: "2026-03-06", primary: "FOOD_AND_DRINK"})
	// Money movement, every excluded flavour.
	seedTxn(t, db, filter.UserID, txnSeed{amount: 500, date: "2026-03-07", primary: "TRANSFER_OUT"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 400, date: "2026-03-08", primary: "TRANSFER_IN"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 300, date: "2026-03-09", primary: "LOAN_PAYMENTS"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 900, date: "2026-03-10", primary: "INCOME"})

	totals, err := repo.SpendingTotals(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != 150 {
		t.Errorf("Spent = %v, want 150 (only the two real expenses)", totals.Spent)
	}
	if totals.TransactionCount != 2 {
		t.Errorf("TransactionCount = %d, want 2", totals.TransactionCount)
	}
}

// BANK_FEES and RENT_AND_UTILITIES are money movement to the category vetting
// pipeline but spending to a report. Confusing the two sets would change every
// total the clients have shown.
func TestSpendingCountsBankFeesAndUtilities(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 35, date: "2026-03-04", primary: "BANK_FEES"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 120, date: "2026-03-05", primary: "RENT_AND_UTILITIES"})

	totals, err := repo.SpendingTotals(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != 155 {
		t.Errorf("Spent = %v, want 155", totals.Spent)
	}
}

// pfc_primary was added nullable and never backfilled, so older rows resolve
// through the legacy category array. If this regresses, those transactions
// silently become "OTHER".
func TestSpendingResolvesLegacyCategories(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 40, date: "2026-03-04", category: `["Restaurants","Coffee Shop"]`})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 60, date: "2026-03-05", category: `["Travel"]`})
	// Legacy money movement must be excluded exactly as the explicit column is.
	seedTxn(t, db, filter.UserID, txnSeed{amount: 500, date: "2026-03-06", category: `["Transfer"]`})
	// Unmapped, and rows with no category at all, land in OTHER.
	seedTxn(t, db, filter.UserID, txnSeed{amount: 25, date: "2026-03-07", category: `["Bespoke Taxidermy"]`})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 15, date: "2026-03-08"})

	byPrimary, err := repo.SpendingByPrimary(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingByPrimary: %v", err)
	}

	got := map[string]float64{}
	for _, row := range byPrimary {
		got[row.Key] = row.Amount
	}
	want := map[string]float64{"FOOD_AND_DRINK": 40, "TRAVEL": 60, "OTHER": 40}
	if len(got) != len(want) {
		t.Fatalf("categories = %v, want %v", got, want)
	}
	for key, amount := range want {
		if got[key] != amount {
			t.Errorf("category %s = %v, want %v", key, got[key], amount)
		}
	}
}

// An explicit pfc_primary wins, and an empty string is treated as absent —
// matching how the clients read the column.
func TestSpendingPrefersPFCPrimaryOverLegacy(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-03-04", primary: "MEDICAL", category: `["Travel"]`})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 20, date: "2026-03-05", primary: "", category: `["Travel"]`})

	byPrimary, err := repo.SpendingByPrimary(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingByPrimary: %v", err)
	}

	got := map[string]float64{}
	for _, row := range byPrimary {
		got[row.Key] = row.Amount
	}
	if got["MEDICAL"] != 10 {
		t.Errorf("MEDICAL = %v, want 10 (the explicit column wins)", got["MEDICAL"])
	}
	if got["TRAVEL"] != 20 {
		t.Errorf("TRAVEL = %v, want 20 (an empty column falls through to legacy)", got["TRAVEL"])
	}
}

func TestSpendingByPrimaryIsSortedAndCounted(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-03-04", primary: "TRAVEL"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 90, date: "2026-03-05", primary: "FOOD_AND_DRINK"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-03-06", primary: "FOOD_AND_DRINK"})

	byPrimary, err := repo.SpendingByPrimary(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingByPrimary: %v", err)
	}
	if len(byPrimary) != 2 {
		t.Fatalf("got %d categories, want 2", len(byPrimary))
	}
	if byPrimary[0].Key != "FOOD_AND_DRINK" || byPrimary[0].Amount != 100 || byPrimary[0].Count != 2 {
		t.Errorf("first row = %+v, want FOOD_AND_DRINK/100/2", byPrimary[0])
	}
	if byPrimary[1].Key != "TRAVEL" {
		t.Errorf("second row = %q, want TRAVEL", byPrimary[1].Key)
	}
}

// A missing detailed value still has to belong to its parent, or a category's
// sub-totals stop summing to the category.
func TestSpendingByDetailedBucketsMissingValues(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 60, date: "2026-03-04", primary: "FOOD_AND_DRINK", detailed: "FOOD_AND_DRINK_RESTAURANT"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 40, date: "2026-03-05", primary: "FOOD_AND_DRINK"})

	byDetailed, err := repo.SpendingByDetailed(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingByDetailed: %v", err)
	}

	var total float64
	got := map[string]float64{}
	for _, row := range byDetailed {
		if row.Primary != "FOOD_AND_DRINK" {
			t.Errorf("row %q has primary %q, want FOOD_AND_DRINK", row.Key, row.Primary)
		}
		got[row.Key] = row.Amount
		total += row.Amount
	}
	if got["FOOD_AND_DRINK_RESTAURANT"] != 60 {
		t.Errorf("restaurant = %v, want 60", got["FOOD_AND_DRINK_RESTAURANT"])
	}
	if got["FOOD_AND_DRINK_OTHER"] != 40 {
		t.Errorf("uncategorized detail = %v, want 40", got["FOOD_AND_DRINK_OTHER"])
	}
	if total != 100 {
		t.Errorf("sub-totals sum to %v, want 100 (the category total)", total)
	}
}

// Month keys are UTC calendar months. The clients used to bucket in local time,
// which pushed a first-of-month transaction into the previous month west of
// Greenwich; boundaries are where that shows up.
func TestSpendingByBucketBucketsOnCalendarBoundaries(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-01-31", primary: "TRAVEL"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 20, date: "2026-02-01", primary: "TRAVEL"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 5, date: "2026-02-28", primary: "FOOD_AND_DRINK"})

	byBucket, err := repo.SpendingByBucket(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingByBucket: %v", err)
	}
	if len(byBucket) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(byBucket), byBucket)
	}
	if byBucket[0].Bucket != "2026-01" || byBucket[0].Amount != 10 {
		t.Errorf("first row = %+v, want 2026-01/10", byBucket[0])
	}
	for _, row := range byBucket[1:] {
		if row.Bucket != "2026-02" {
			t.Errorf("row %+v should be in 2026-02", row)
		}
	}

	totals, err := repo.SpendingTotals(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.MonthsActive != 2 {
		t.Errorf("MonthsActive = %d, want 2", totals.MonthsActive)
	}
}

func TestSpendingRespectsDateWindow(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-01-15", primary: "TRAVEL"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 20, date: "2026-02-15", primary: "TRAVEL"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 40, date: "2026-03-15", primary: "TRAVEL"})

	windowed := filter
	windowed.From = "2026-02-01"

	totals, err := repo.SpendingTotals(context.Background(), windowed)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != 60 {
		t.Errorf("Spent from 2026-02-01 = %v, want 60", totals.Spent)
	}

	windowed.To = "2026-02-28"
	totals, err = repo.SpendingTotals(context.Background(), windowed)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != 20 {
		t.Errorf("Spent in February = %v, want 20", totals.Spent)
	}
}

// Another user's spending must never appear in this user's report.
func TestSpendingIsScopedToOneUser(t *testing.T) {
	db, repo, filter := reportFixture(t)

	otherID := uuid.New()
	if _, err := db.Exec(`INSERT INTO users (id, name) VALUES (?, ?)`, otherID.String(), "Other"); err != nil {
		t.Fatalf("seed other user: %v", err)
	}

	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-03-04", primary: "TRAVEL"})
	seedTxn(t, db, otherID, txnSeed{amount: 999, date: "2026-03-04", primary: "TRAVEL"})

	totals, err := repo.SpendingTotals(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != 10 {
		t.Errorf("Spent = %v, want 10 — another user's rows leaked in", totals.Spent)
	}
}

// merchant_id is nullable. Unresolved transactions group under their own name
// rather than vanishing, so the merchant breakdown still sums to the total.
func TestSpendingByMerchantKeepsUnresolvedTransactions(t *testing.T) {
	db, repo, filter := reportFixture(t)

	merchantID := seedMerchant(t, db, "Huckleberry Roasters", "https://cdn.example/huck.png")
	seedTxn(t, db, filter.UserID, txnSeed{amount: 12, date: "2026-03-04", primary: "FOOD_AND_DRINK", merchant: &merchantID})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 8, date: "2026-03-05", primary: "FOOD_AND_DRINK", merchant: &merchantID})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 30, date: "2026-03-06", primary: "TRAVEL", name: "UNRESOLVED VENDOR 123"})

	byMerchant, err := repo.SpendingByMerchant(context.Background(), filter, 0)
	if err != nil {
		t.Fatalf("SpendingByMerchant: %v", err)
	}
	if len(byMerchant) != 2 {
		t.Fatalf("got %d merchants, want 2: %+v", len(byMerchant), byMerchant)
	}

	if byMerchant[0].Name != "UNRESOLVED VENDOR 123" || byMerchant[0].Amount != 30 {
		t.Errorf("first row = %+v, want the unresolved vendor at 30", byMerchant[0])
	}
	if byMerchant[0].MerchantID != nil {
		t.Error("unresolved transaction should carry no merchant id")
	}

	named := byMerchant[1]
	if named.Name != "Huckleberry Roasters" || named.Amount != 20 || named.Count != 2 {
		t.Errorf("second row = %+v, want Huckleberry Roasters/20/2", named)
	}
	if named.LogoURL != "https://cdn.example/huck.png" {
		t.Errorf("logo = %q, want the merchant's cdn url", named.LogoURL)
	}
	if named.MerchantID == nil || *named.MerchantID != merchantID {
		t.Errorf("merchant id = %v, want %v", named.MerchantID, merchantID)
	}
}

// The groupings are different questions about one set of rows, so they must add
// up to the same number. This is the property that keeps a report internally
// consistent no matter which panel the user reads.
func TestGroupingsReconcileWithTotals(t *testing.T) {
	db, repo, filter := reportFixture(t)

	merchantID := seedMerchant(t, db, "Corner Store", "")
	seeds := []txnSeed{
		{amount: 42.50, date: "2026-01-15", primary: "FOOD_AND_DRINK", detailed: "FOOD_AND_DRINK_RESTAURANT", merchant: &merchantID},
		{amount: 17.25, date: "2026-02-03", primary: "FOOD_AND_DRINK"},
		{amount: 100.00, date: "2026-02-20", primary: "TRAVEL", detailed: "TRAVEL_FLIGHTS"},
		{amount: 8.75, date: "2026-03-01", category: `["Coffee"]`},
		{amount: 60.00, date: "2026-03-02", primary: "RENT_AND_UTILITIES"},
		// Present but excluded from every figure below.
		{amount: -20.00, date: "2026-03-03", primary: "FOOD_AND_DRINK"},
		{amount: 900.00, date: "2026-03-04", primary: "INCOME"},
	}
	for _, s := range seeds {
		seedTxn(t, db, filter.UserID, s)
	}

	const want = 42.50 + 17.25 + 100.00 + 8.75 + 60.00
	ctx := context.Background()

	totals, err := repo.SpendingTotals(ctx, filter)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != want {
		t.Errorf("totals.Spent = %v, want %v", totals.Spent, want)
	}

	sums := map[string]float64{}
	byPrimary, err := repo.SpendingByPrimary(ctx, filter)
	if err != nil {
		t.Fatalf("SpendingByPrimary: %v", err)
	}
	for _, row := range byPrimary {
		sums["primary"] += row.Amount
	}

	byDetailed, err := repo.SpendingByDetailed(ctx, filter)
	if err != nil {
		t.Fatalf("SpendingByDetailed: %v", err)
	}
	for _, row := range byDetailed {
		sums["detailed"] += row.Amount
	}

	byBucket, err := repo.SpendingByBucket(ctx, filter)
	if err != nil {
		t.Fatalf("SpendingByBucket: %v", err)
	}
	for _, row := range byBucket {
		sums["bucket"] += row.Amount
	}

	byMerchant, err := repo.SpendingByMerchant(ctx, filter, 0)
	if err != nil {
		t.Fatalf("SpendingByMerchant: %v", err)
	}
	for _, row := range byMerchant {
		sums["merchant"] += row.Amount
	}

	for grouping, sum := range sums {
		if sum != want {
			t.Errorf("%s grouping sums to %v, want %v", grouping, sum, want)
		}
	}
}

// Money arriving is the INCOME category and nothing else. A refund is also a
// credit, but drawing a returned purchase as earnings from the shop that took
// it back would be worse than leaving it out.
func TestIncomeCountsEarningsNotRefunds(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: -2500, date: "2026-03-01", name: "ACME PAYROLL", primary: "INCOME"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: -500, date: "2026-03-15", name: "ACME PAYROLL", primary: "INCOME"})
	// A returned television: a credit, in a spending category.
	seedTxn(t, db, filter.UserID, txnSeed{amount: -400, date: "2026-03-20", name: "BEST BUY", primary: "GENERAL_MERCHANDISE"})
	// Moving money between the user's own accounts is not earnings either.
	seedTxn(t, db, filter.UserID, txnSeed{amount: -1000, date: "2026-03-21", name: "TRANSFER", primary: "TRANSFER_IN"})
	// And an ordinary expense must not appear on the income side at all.
	seedTxn(t, db, filter.UserID, txnSeed{amount: 60, date: "2026-03-22", primary: "FOOD_AND_DRINK"})

	income, err := repo.IncomeBySource(context.Background(), filter, 0)
	if err != nil {
		t.Fatalf("IncomeBySource: %v", err)
	}
	if len(income) != 1 {
		t.Fatalf("got %d income sources, want 1: %+v", len(income), income)
	}
	if income[0].Name != "ACME PAYROLL" {
		t.Errorf("source = %q, want ACME PAYROLL", income[0].Name)
	}
	// Reported as a positive magnitude, not Plaid's negative.
	if income[0].Amount != 3000 {
		t.Errorf("amount = %v, want 3000", income[0].Amount)
	}
	if income[0].Count != 2 {
		t.Errorf("count = %d, want 2", income[0].Count)
	}
}

// Income and spending are asked about the same window, so a report can show
// both without one of them quietly covering a different period.
func TestIncomeRespectsTheWindow(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: -100, date: "2026-01-15", primary: "INCOME"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: -200, date: "2026-03-15", primary: "INCOME"})

	windowed := filter
	windowed.From = "2026-02-01"

	income, err := repo.IncomeBySource(context.Background(), windowed, 0)
	if err != nil {
		t.Fatalf("IncomeBySource: %v", err)
	}
	var total float64
	for _, source := range income {
		total += source.Amount
	}
	if total != 200 {
		t.Errorf("income from 2026-02-01 = %v, want 200", total)
	}
}

// Days and weeks are named by the date they start on, so the keys sort in
// order whatever the granularity and the client needs no calendar arithmetic.
func TestSpendingBucketsByGranularity(t *testing.T) {
	db, repo, filter := reportFixture(t)

	// 2026-08-24 is a Monday; 26th and 30th fall in the same week, 31st starts
	// the next one.
	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-08-26", primary: "TRAVEL"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 20, date: "2026-08-30", primary: "TRAVEL"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 40, date: "2026-08-31", primary: "TRAVEL"})

	byDay := filter
	byDay.Granularity = domain.GranularityDay
	days, err := repo.SpendingByBucket(context.Background(), byDay)
	if err != nil {
		t.Fatalf("SpendingByBucket(day): %v", err)
	}
	if len(days) != 3 {
		t.Fatalf("got %d day buckets, want 3: %+v", len(days), days)
	}
	if days[0].Bucket != "2026-08-26" {
		t.Errorf("first day bucket = %q, want 2026-08-26", days[0].Bucket)
	}

	byWeek := filter
	byWeek.Granularity = domain.GranularityWeek
	weeks, err := repo.SpendingByBucket(context.Background(), byWeek)
	if err != nil {
		t.Fatalf("SpendingByBucket(week): %v", err)
	}
	if len(weeks) != 2 {
		t.Fatalf("got %d week buckets, want 2: %+v", len(weeks), weeks)
	}
	if weeks[0].Bucket != "2026-08-24" || weeks[0].Amount != 30 {
		t.Errorf("first week = %+v, want 2026-08-24 at 30", weeks[0])
	}
	if weeks[1].Bucket != "2026-08-31" || weeks[1].Amount != 40 {
		t.Errorf("second week = %+v, want 2026-08-31 at 40", weeks[1])
	}

	// Months stay the default, and stay YYYY-MM.
	months, err := repo.SpendingByBucket(context.Background(), filter)
	if err != nil {
		t.Fatalf("SpendingByBucket(month): %v", err)
	}
	if len(months) != 1 || months[0].Bucket != "2026-08" {
		t.Errorf("month buckets = %+v, want one 2026-08", months)
	}
}

// Narrowing to a category narrows every figure, so a drill-down's percentages
// are shares of the category that was opened.
func TestReportNarrowsToOneCategory(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 60, date: "2026-03-04", primary: "FOOD_AND_DRINK"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 40, date: "2026-03-05", primary: "FOOD_AND_DRINK"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 500, date: "2026-03-06", primary: "TRAVEL"})

	narrowed := filter
	narrowed.PFCPrimary = "FOOD_AND_DRINK"

	totals, err := repo.SpendingTotals(context.Background(), narrowed)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != 100 {
		t.Errorf("Spent = %v, want 100", totals.Spent)
	}
}

// The category filter resolves through the legacy map, or a drill-down would
// drop exactly the older rows the category it was clicked from counted.
func TestCategoryFilterMatchesLegacyRows(t *testing.T) {
	db, repo, filter := reportFixture(t)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 60, date: "2026-03-04", primary: "FOOD_AND_DRINK"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 40, date: "2026-03-05", category: `["Restaurants"]`})

	narrowed := filter
	narrowed.PFCPrimary = "FOOD_AND_DRINK"

	totals, err := repo.SpendingTotals(context.Background(), narrowed)
	if err != nil {
		t.Fatalf("SpendingTotals: %v", err)
	}
	if totals.Spent != 100 {
		t.Errorf("Spent = %v, want 100 — a legacy-categorized row was dropped", totals.Spent)
	}
}

// The "Other" branch of a chart lists what the chart did not draw a slice for,
// and an uncategorized row is the likeliest thing to be in it. SQL's
// three-valued logic drops NULLs from a bare NOT IN, so this is the test that
// catches the filter silently hiding them.
func TestExcludeCategoriesKeepsUncategorizedRows(t *testing.T) {
	db, _, filter := reportFixture(t)
	listRepo := NewTransactionCacheRepo(db, DialectSQLite)

	seedTxn(t, db, filter.UserID, txnSeed{amount: 10, date: "2026-03-04", primary: "FOOD_AND_DRINK"})
	seedTxn(t, db, filter.UserID, txnSeed{amount: 20, date: "2026-03-05", primary: "TRAVEL"})
	// No category at all — this is the row the exclusion must not swallow.
	seedTxn(t, db, filter.UserID, txnSeed{amount: 30, date: "2026-03-06", name: "UNCATEGORIZED"})

	rows, _, _, err := listRepo.FindByUserIDWithReceipts(
		context.Background(),
		filter.UserID,
		domain.TransactionFilter{PFCPrimaryNotIn: []string{"FOOD_AND_DRINK", "TRAVEL"}},
	)
	if err != nil {
		t.Fatalf("FindByUserIDWithReceipts: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 — the uncategorized row was dropped: %+v", len(rows), rows)
	}
	if rows[0].Name != "UNCATEGORIZED" {
		t.Errorf("row = %q, want UNCATEGORIZED", rows[0].Name)
	}
}
