package service

import (
	"context"
	"fmt"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

// ReportService assembles a spending report: one response carrying everything
// the reports screens draw, in place of the paged transaction walk each of them
// used to run.
//
// It returns keys and numbers, never labels, colours or icons. Which shade a
// category is drawn in depends on its rank in a list the client is already
// holding, and an icon is a component that cannot cross a JSON boundary — so
// presentation stays where it is rendered.
type ReportService struct {
	repo repository.TransactionReportRepository
}

func NewReportService(repo repository.TransactionReportRepository) *ReportService {
	return &ReportService{repo: repo}
}

// Wire DTOs. snake_case throughout, matching the other summary endpoints.

type SpendingWindow struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type SpendingTotalsDTO struct {
	Spent            float64 `json:"spent"`
	TransactionCount int     `json:"transaction_count"`
	MonthsActive     int     `json:"months_active"`
}

type CategoryDTO struct {
	Key    string  `json:"key"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
	// Pct is the share of the grand total for a category, and the share of its
	// parent category for a sub-category — matching what the clients drew.
	Pct float64 `json:"pct"`
}

type SubcategoryDTO struct {
	Primary string  `json:"primary"`
	Key     string  `json:"key"`
	Amount  float64 `json:"amount"`
	Count   int     `json:"count"`
	Pct     float64 `json:"pct"`
}

type BucketDTO struct {
	// Bucket is the date the bucket starts on: YYYY-MM-DD for days and weeks,
	// YYYY-MM for months.
	Bucket string  `json:"bucket"`
	Total  float64 `json:"total"`
	// ByCategory holds the category breakdown behind Total, keyed by primary.
	ByCategory map[string]float64 `json:"by_category"`
}

type IncomeSourceDTO struct {
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
	Count  int     `json:"count"`
}

type MerchantDTO struct {
	MerchantID string  `json:"merchant_id,omitempty"`
	Name       string  `json:"name"`
	LogoURL    string  `json:"logo_url,omitempty"`
	Amount     float64 `json:"amount"`
	Count      int     `json:"count"`
}

type SpendingReport struct {
	Window SpendingWindow `json:"window"`
	// Granularity is the width of each ByBucket entry, echoed so a client never
	// has to infer it from the key format.
	Granularity   string            `json:"granularity"`
	Totals        SpendingTotalsDTO `json:"totals"`
	ByCategory    []CategoryDTO     `json:"by_category"`
	BySubcategory []SubcategoryDTO  `json:"by_subcategory"`
	ByBucket      []BucketDTO       `json:"by_bucket"`
	ByMerchant    []MerchantDTO     `json:"by_merchant"`
	// IncomeTotal and ByIncomeSource cover money arriving over the window —
	// what the money-flow chart draws opposite spending. Only the INCOME
	// category counts; a refund is a credit but not earnings.
	IncomeTotal    float64           `json:"income_total"`
	ByIncomeSource []IncomeSourceDTO `json:"by_income_source"`
}

// merchantLimit caps the merchant breakdown. The clients render a handful;
// returning every merchant an account has ever used would dwarf the rest of the
// response for no one's benefit.
const merchantLimit = 50

// incomeSourceLimit caps the payers listed. The money-flow chart draws a
// handful of inflows and folds the rest.
const incomeSourceLimit = 25

// SpendingReportParams is what a caller asks for. Months is a convenience:
// "the last N calendar months, including this one".
type SpendingReportParams struct {
	UserID uuid.UUID
	From   string
	To     string
	Months int
	// PFCPrimary narrows the whole report to one category, for a drill-down.
	PFCPrimary string
	// Granularity is the width of each over-time bucket; empty means months.
	Granularity string
}

// WindowFrom resolves the effective start of the window.
//
// An explicit From wins. Otherwise Months counts back over calendar months from
// the first of the current month, so "3 months" spans the whole of the current
// month and the two before it rather than the last 90 days.
func (p SpendingReportParams) WindowFrom(now time.Time) string {
	if p.From != "" {
		return p.From
	}
	if p.Months <= 0 {
		return ""
	}
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).
		AddDate(0, -(p.Months - 1), 0)
	return start.Format("2006-01-02")
}

// SpendingReport builds the whole report for one user and window.
func (s *ReportService) SpendingReport(ctx context.Context, params SpendingReportParams) (*SpendingReport, error) {
	filter := domain.ReportFilter{
		UserID:      params.UserID,
		From:        params.WindowFrom(time.Now().UTC()),
		To:          params.To,
		PFCPrimary:  params.PFCPrimary,
		Granularity: domain.ParseGranularity(params.Granularity),
	}

	totals, err := s.repo.SpendingTotals(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("spending totals: %w", err)
	}
	byPrimary, err := s.repo.SpendingByPrimary(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("spending by category: %w", err)
	}
	byDetailed, err := s.repo.SpendingByDetailed(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("spending by subcategory: %w", err)
	}
	byBucket, err := s.repo.SpendingByBucket(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("spending by bucket: %w", err)
	}
	byMerchant, err := s.repo.SpendingByMerchant(ctx, filter, merchantLimit)
	if err != nil {
		return nil, fmt.Errorf("spending by merchant: %w", err)
	}
	income, err := s.repo.IncomeBySource(ctx, filter, incomeSourceLimit)
	if err != nil {
		return nil, fmt.Errorf("income by source: %w", err)
	}

	report := &SpendingReport{
		Window: SpendingWindow{From: filter.From, To: filter.To},
		Totals: SpendingTotalsDTO{
			Spent:            totals.Spent,
			TransactionCount: totals.TransactionCount,
			MonthsActive:     totals.MonthsActive,
		},
		ByCategory:     categoryDTOs(byPrimary, totals.Spent),
		BySubcategory:  subcategoryDTOs(byDetailed, byPrimary),
		ByBucket:       bucketDTOs(byBucket),
		ByMerchant:     merchantDTOs(byMerchant),
		ByIncomeSource: incomeDTOs(income),
	}
	report.Granularity = string(filter.Granularity)
	for _, source := range income {
		report.IncomeTotal += source.Amount
	}
	return report, nil
}

func categoryDTOs(rows []domain.CategoryAmount, grandTotal float64) []CategoryDTO {
	out := make([]CategoryDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, CategoryDTO{
			Key:    row.Key,
			Amount: row.Amount,
			Count:  row.Count,
			Pct:    share(row.Amount, grandTotal),
		})
	}
	return out
}

// subcategoryDTOs takes each sub-category's share of its own parent, not of the
// grand total, so a category's sub-shares add to 100.
func subcategoryDTOs(rows []domain.CategoryAmount, primaries []domain.CategoryAmount) []SubcategoryDTO {
	parentTotals := make(map[string]float64, len(primaries))
	for _, primary := range primaries {
		parentTotals[primary.Key] = primary.Amount
	}

	out := make([]SubcategoryDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, SubcategoryDTO{
			Primary: row.Primary,
			Key:     row.Key,
			Amount:  row.Amount,
			Count:   row.Count,
			Pct:     share(row.Amount, parentTotals[row.Primary]),
		})
	}
	return out
}

// bucketDTOs pivots the flat (bucket, category) rows into one entry per bucket,
// preserving the oldest-first order the query returns.
func bucketDTOs(rows []domain.BucketCategoryAmount) []BucketDTO {
	out := make([]BucketDTO, 0)
	index := make(map[string]int)

	for _, row := range rows {
		position, seen := index[row.Bucket]
		if !seen {
			position = len(out)
			index[row.Bucket] = position
			out = append(out, BucketDTO{Bucket: row.Bucket, ByCategory: map[string]float64{}})
		}
		out[position].Total += row.Amount
		out[position].ByCategory[row.Primary] += row.Amount
	}
	return out
}

func incomeDTOs(rows []domain.IncomeSource) []IncomeSourceDTO {
	out := make([]IncomeSourceDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, IncomeSourceDTO{
			Name:   row.Name,
			Amount: row.Amount,
			Count:  row.Count,
		})
	}
	return out
}

func merchantDTOs(rows []domain.MerchantAmount) []MerchantDTO {
	out := make([]MerchantDTO, 0, len(rows))
	for _, row := range rows {
		dto := MerchantDTO{
			Name:    row.Name,
			LogoURL: row.LogoURL,
			Amount:  row.Amount,
			Count:   row.Count,
		}
		if row.MerchantID != nil {
			dto.MerchantID = row.MerchantID.String()
		}
		out = append(out, dto)
	}
	return out
}

// share is a percentage that stays 0 rather than NaN when there is nothing to
// divide by — an account with no spending in the window renders as empty, not
// as broken.
func share(amount, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return amount / total * 100
}
