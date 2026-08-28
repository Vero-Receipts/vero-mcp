package service

import (
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// A range button says "3 months" and means this month plus the two before it,
// whole — not the last 90 days.
func TestSpendingReportParamsWindowFrom(t *testing.T) {
	now := time.Date(2026, time.August, 28, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name   string
		params SpendingReportParams
		want   string
	}{
		{"an explicit from wins", SpendingReportParams{From: "2026-01-15", Months: 3}, "2026-01-15"},
		{"one month is the current month", SpendingReportParams{Months: 1}, "2026-08-01"},
		{"three months reaches back two", SpendingReportParams{Months: 3}, "2026-06-01"},
		{"twelve months crosses the year boundary", SpendingReportParams{Months: 12}, "2025-09-01"},
		{"no window at all is unbounded", SpendingReportParams{}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.params.WindowFrom(now); got != tc.want {
				t.Errorf("WindowFrom() = %q, want %q", got, tc.want)
			}
		})
	}
}

// A sub-category's percentage is its share of its own parent, so the parts of
// one category add to 100 — unlike a category's share, which is of the total.
func TestSubcategoryPercentagesAreRelativeToTheirParent(t *testing.T) {
	primaries := []domain.CategoryAmount{
		{Key: "FOOD_AND_DRINK", Amount: 200, Count: 4},
		{Key: "TRAVEL", Amount: 800, Count: 2},
	}
	detailed := []domain.CategoryAmount{
		{Primary: "FOOD_AND_DRINK", Key: "FOOD_AND_DRINK_RESTAURANT", Amount: 150, Count: 3},
		{Primary: "FOOD_AND_DRINK", Key: "FOOD_AND_DRINK_GROCERIES", Amount: 50, Count: 1},
		{Primary: "TRAVEL", Key: "TRAVEL_FLIGHTS", Amount: 800, Count: 2},
	}

	got := subcategoryDTOs(detailed, primaries)

	if got[0].Pct != 75 {
		t.Errorf("restaurant share = %v, want 75 (of its category, not the total)", got[0].Pct)
	}
	if got[1].Pct != 25 {
		t.Errorf("groceries share = %v, want 25", got[1].Pct)
	}
	if got[2].Pct != 100 {
		t.Errorf("flights share = %v, want 100", got[2].Pct)
	}

	// Categories, by contrast, are shares of the grand total.
	categories := categoryDTOs(primaries, 1000)
	if categories[0].Pct != 20 || categories[1].Pct != 80 {
		t.Errorf("category shares = %v/%v, want 20/80", categories[0].Pct, categories[1].Pct)
	}
}

// An account with nothing in the window renders as empty rather than dividing
// by zero and drawing NaN.
func TestPercentagesSurviveAnEmptyWindow(t *testing.T) {
	got := categoryDTOs([]domain.CategoryAmount{{Key: "FOOD_AND_DRINK", Amount: 0}}, 0)
	if got[0].Pct != 0 {
		t.Errorf("Pct = %v, want 0", got[0].Pct)
	}
}

func TestBucketPivotGroupsCategoriesUnderTheirBucket(t *testing.T) {
	rows := []domain.BucketCategoryAmount{
		{Bucket: "2026-06", Primary: "FOOD_AND_DRINK", Amount: 100},
		{Bucket: "2026-06", Primary: "TRAVEL", Amount: 50},
		{Bucket: "2026-07", Primary: "FOOD_AND_DRINK", Amount: 25},
	}

	got := bucketDTOs(rows)

	if len(got) != 2 {
		t.Fatalf("got %d months, want 2", len(got))
	}
	// Oldest first, as the query returns them — the charts draw left to right.
	if got[0].Bucket != "2026-06" || got[1].Bucket != "2026-07" {
		t.Errorf("months = %q, %q; want 2026-06, 2026-07", got[0].Bucket, got[1].Bucket)
	}
	if got[0].Total != 150 {
		t.Errorf("June total = %v, want 150", got[0].Total)
	}
	if got[0].ByCategory["FOOD_AND_DRINK"] != 100 || got[0].ByCategory["TRAVEL"] != 50 {
		t.Errorf("June breakdown = %v", got[0].ByCategory)
	}
	if got[1].Total != 25 {
		t.Errorf("July total = %v, want 25", got[1].Total)
	}
}

// The slices are built with make(..., 0) rather than left nil so an account
// with no spending serializes as [] and the clients can map over it.
func TestEmptyReportSlicesSerializeAsArrays(t *testing.T) {
	if categoryDTOs(nil, 0) == nil {
		t.Error("by_category is nil, want an empty slice")
	}
	if subcategoryDTOs(nil, nil) == nil {
		t.Error("by_subcategory is nil, want an empty slice")
	}
	if bucketDTOs(nil) == nil {
		t.Error("by_month is nil, want an empty slice")
	}
	if merchantDTOs(nil) == nil {
		t.Error("by_merchant is nil, want an empty slice")
	}
}
