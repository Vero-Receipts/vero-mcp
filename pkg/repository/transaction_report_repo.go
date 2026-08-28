package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// TransactionReportRepo answers aggregate questions about a user's spending:
// how much, in which categories, in which months, with which merchants.
//
// These used to be answered in the browser, by downloading the transaction
// history a page at a time and summing it there. That capped the answers at
// whatever the walk had reached and cost a request per page; grouping in the
// database removes both problems.
//
// The SQL stays inside the subset both supported dialects share — no
// date_trunc, no FILTER, no json_agg — because the repository tests run against
// SQLite while production runs on Postgres. Where a difference is unavoidable
// it is confined to one function and branched on r.dialect.
type TransactionReportRepo struct {
	BaseRepo
	dialect Dialect
}

func NewTransactionReportRepo(db *sql.DB, dialect Dialect) *TransactionReportRepo {
	return &TransactionReportRepo{
		BaseRepo: NewBaseRepo(db, dialect),
		dialect:  dialect,
	}
}

// spentExpr sums expenses only. Identical to the expression behind the
// transaction list's total_spent, so a report and the list agree for the same
// filter — a property the integration tests assert directly.
const spentExpr = "COALESCE(SUM(CASE WHEN t.amount > 0 THEN t.amount ELSE 0 END), 0)"

// monthExpr renders a date as a YYYY-MM key.
//
// substr over the text form rather than date_trunc or strftime: the column is a
// DATE on Postgres and TEXT holding the same ISO string on SQLite, and casting
// to text yields YYYY-MM-DD in both. Note this buckets in UTC, unlike the
// clients' old local-time bucketing — a first-of-month transaction west of
// Greenwich used to land in the previous month.
const monthExpr = "substr(CAST(t.date AS TEXT), 1, 7)"

// primaryExpr resolves a transaction's spending category in SQL: the
// personal-finance-category column when it is set, otherwise the legacy
// category array, otherwise OTHER.
//
// The CASE arms are generated from domain.LegacyToPFC so the map stays the one
// place the mapping is written down; Go and SQL cannot drift apart.
func (r *TransactionReportRepo) primaryExpr() string {
	legacy := r.legacyCategoryExpr()

	// Deterministic order so the generated SQL is stable across processes,
	// which keeps query plans cacheable and test failures readable.
	keys := make([]string, 0, len(domain.LegacyToPFC))
	for legacyName := range domain.LegacyToPFC {
		keys = append(keys, legacyName)
	}
	sort.Strings(keys)

	var b strings.Builder
	b.WriteString("CASE WHEN COALESCE(t.pfc_primary, '') <> '' THEN t.pfc_primary ELSE (CASE ")
	for _, legacyName := range keys {
		// The values are internal constants, never user input; quoting them
		// inline keeps the expression usable in GROUP BY without repeating a
		// long placeholder list on every query.
		fmt.Fprintf(&b, "WHEN LOWER(TRIM(%s)) = '%s' THEN '%s' ", legacy, legacyName, domain.LegacyToPFC[legacyName])
	}
	fmt.Fprintf(&b, "ELSE '%s' END) END", domain.UncategorizedPrimary)
	return b.String()
}

// legacyCategoryExpr extracts the first element of the legacy category array.
//
// This is the one genuinely dialect-specific piece: Postgres stores the column
// as JSONB and indexes into it, while SQLite holds the same JSON as text and
// has no operator for it, so the value is picked out of the string.
func (r *TransactionReportRepo) legacyCategoryExpr() string {
	if r.dialect == DialectPostgres {
		return "(t.category->>0)"
	}
	// SQLite: strip the leading [" and take everything up to the closing quote.
	return `CASE WHEN t.category LIKE '["%' THEN substr(t.category, 3, instr(substr(t.category, 3), '"') - 1) ELSE '' END`
}

// applyScope adds the filters every reporting query shares: the owning user,
// the date window, and the definition of spending itself.
func (r *TransactionReportRepo) applyScope(qb sq.SelectBuilder, f domain.ReportFilter) sq.SelectBuilder {
	qb = qb.Where(sq.Eq{"t.user_id": f.UserID.String()}).
		Where(sq.Gt{"t.amount": 0}).
		Where(fmt.Sprintf("(%s) NOT IN (%s)", r.primaryExpr(), placeholderList(len(domain.ExcludedPrimaries))),
			toAnySlice(domain.ExcludedPrimaries)...)

	if f.From != "" {
		qb = qb.Where(sq.GtOrEq{"t.date": f.From})
	}
	if f.To != "" {
		qb = qb.Where(sq.LtOrEq{"t.date": f.To})
	}
	return qb
}

func placeholderList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func toAnySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// SpendingTotals returns the headline figures for the window.
func (r *TransactionReportRepo) SpendingTotals(ctx context.Context, f domain.ReportFilter) (domain.SpendingTotals, error) {
	qb := r.applyScope(
		r.SQ.Select(
			spentExpr,
			"COUNT(*)",
			fmt.Sprintf("COUNT(DISTINCT %s)", monthExpr),
		).From("transaction_cache t"),
		f,
	)

	var totals domain.SpendingTotals
	row := qb.RunWith(r.DB).QueryRowContext(ctx)
	if err := row.Scan(&totals.Spent, &totals.TransactionCount, &totals.MonthsActive); err != nil {
		return domain.SpendingTotals{}, fmt.Errorf("spending totals: %w", err)
	}
	return totals, nil
}

// SpendingByPrimary groups spending by resolved primary category, highest
// first.
func (r *TransactionReportRepo) SpendingByPrimary(ctx context.Context, f domain.ReportFilter) ([]domain.CategoryAmount, error) {
	primary := r.primaryExpr()
	qb := r.applyScope(
		r.SQ.Select(primary, spentExpr, "COUNT(*)").From("transaction_cache t"),
		f,
	).GroupBy(primary).OrderBy("2 DESC")

	rows, err := qb.RunWith(r.DB).QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("spending by primary: %w", err)
	}
	defer rows.Close()

	var out []domain.CategoryAmount
	for rows.Next() {
		var row domain.CategoryAmount
		if err := rows.Scan(&row.Key, &row.Amount, &row.Count); err != nil {
			return nil, fmt.Errorf("scan spending by primary: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SpendingByDetailed groups spending by sub-category within its parent.
//
// A transaction whose detailed value is missing is grouped under a synthetic
// "<PRIMARY>_OTHER" key, matching what the clients substituted, so a category's
// sub-totals still sum to the category.
func (r *TransactionReportRepo) SpendingByDetailed(ctx context.Context, f domain.ReportFilter) ([]domain.CategoryAmount, error) {
	primary := r.primaryExpr()
	detailed := fmt.Sprintf(
		"CASE WHEN COALESCE(t.pfc_detailed, '') <> '' THEN t.pfc_detailed ELSE ((%s) || '_OTHER') END",
		primary,
	)

	qb := r.applyScope(
		r.SQ.Select(primary, detailed, spentExpr, "COUNT(*)").From("transaction_cache t"),
		f,
	).GroupBy(primary, detailed).OrderBy("3 DESC")

	rows, err := qb.RunWith(r.DB).QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("spending by detailed: %w", err)
	}
	defer rows.Close()

	var out []domain.CategoryAmount
	for rows.Next() {
		var row domain.CategoryAmount
		if err := rows.Scan(&row.Primary, &row.Key, &row.Amount, &row.Count); err != nil {
			return nil, fmt.Errorf("scan spending by detailed: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SpendingByMonth returns spending per (month, primary), oldest month first.
// The caller pivots these into per-month buckets — the category set is
// open-ended, so pivoting in SQL would mean generating a column per category.
func (r *TransactionReportRepo) SpendingByMonth(ctx context.Context, f domain.ReportFilter) ([]domain.MonthCategoryAmount, error) {
	primary := r.primaryExpr()
	qb := r.applyScope(
		r.SQ.Select(monthExpr, primary, spentExpr).From("transaction_cache t"),
		f,
	).GroupBy(monthExpr, primary).OrderBy("1 ASC", "3 DESC")

	rows, err := qb.RunWith(r.DB).QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("spending by month: %w", err)
	}
	defer rows.Close()

	var out []domain.MonthCategoryAmount
	for rows.Next() {
		var row domain.MonthCategoryAmount
		if err := rows.Scan(&row.Month, &row.Primary, &row.Amount); err != nil {
			return nil, fmt.Errorf("scan spending by month: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// SpendingByMerchant groups spending by merchant, highest first.
//
// merchant_id is nullable — not every transaction resolves to a merchant record
// — so those rows group under the name on the transaction itself. Dropping them
// would make the merchant breakdown quietly fail to sum to the category totals.
//
// Note this joins merchants and nothing else. Joining receipt_matches here
// would multiply a transaction by its match count and inflate every sum.
func (r *TransactionReportRepo) SpendingByMerchant(ctx context.Context, f domain.ReportFilter, limit int) ([]domain.MerchantAmount, error) {
	name := "COALESCE(NULLIF(m.canonical_name, ''), t.name)"
	qb := r.applyScope(
		r.SQ.Select(
			"t.merchant_id",
			name,
			"COALESCE(MAX(m.logo_cdn_url), '')",
			spentExpr,
			"COUNT(*)",
		).
			From("transaction_cache t").
			LeftJoin("merchants m ON m.id = t.merchant_id"),
		f,
	).GroupBy("t.merchant_id", name).OrderBy("4 DESC")

	if limit > 0 {
		qb = qb.Limit(uint64(limit))
	}

	rows, err := qb.RunWith(r.DB).QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("spending by merchant: %w", err)
	}
	defer rows.Close()

	var out []domain.MerchantAmount
	for rows.Next() {
		var (
			row        domain.MerchantAmount
			merchantID sql.NullString
		)
		if err := rows.Scan(&merchantID, &row.Name, &row.LogoURL, &row.Amount, &row.Count); err != nil {
			return nil, fmt.Errorf("scan spending by merchant: %w", err)
		}
		if merchantID.Valid {
			if id, err := uuid.Parse(merchantID.String); err == nil {
				row.MerchantID = &id
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
