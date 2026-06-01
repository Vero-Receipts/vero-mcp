package service

import (
	"context"
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

func TestNormalizeMerchant(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"STARBUCKS #1234", "starbucks 1234"},
		{"SQ *BLUE BOTTLE COFFEE", "blue bottle coffee"},
		{"sq*Blue Bottle", "blue bottle"},
		{"TST* Sweetgreen NYC", "sweetgreen nyc"},
		{"APLPAY WHOLE FOODS MARKET", "whole foods"},
		{"PY *Venmo Payment", "venmo payment"},
		{"SP *Shopify Store", "shopify"},
		{"PP*PayPal Purchase", "paypal purchase"},
		{"Zettle_merchant123", "merchant123"},
		// Normal names pass through with cleanup.
		{"Walmart Supercenter #5678", "walmart supercenter 5678"},
		// Non-alphanumeric stripped.
		{"Joe's Café & Bar", "joe s café"},
		// Empty input.
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeMerchant(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeMerchant(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "xyz", 3},
		{"kitten", "sitting", 3},
		{"same", "same", 0},
		{"abc", "abd", 1},
		{"flaw", "lawn", 2},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_"+tt.b, func(t *testing.T) {
			got := levenshtein(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestScoreAmount(t *testing.T) {
	tests := []struct {
		name          string
		pctDiff       float64
		isNonUSD      bool
		chargeExceeds bool
		want          float64
	}{
		// USD tiers (charge does not exceed receipt)
		{"exact_match", 0.3, false, false, 1.0},
		{"near_exact", 1.5, false, false, 0.95},
		{"close", 4.0, false, false, 0.85},
		{"moderate", 8.0, false, false, 0.70},
		{"far", 12.0, false, false, 0.55},
		{"edge_of_max", 20.0, false, false, 0.40},
		{"beyond_max", 21.0, false, false, 0.0},
		// Non-USD wider tolerance
		{"nonusd_within_wide", 35.0, true, false, 0.40},
		{"nonusd_beyond_wide", 41.0, true, false, 0.0},
		// Boundary values
		{"exactly_half_pct", 0.5, false, false, 1.0},
		{"just_above_half_pct", 0.51, false, false, 0.95},
		{"exactly_2pct", 2.0, false, false, 0.95},
		{"just_above_2pct", 2.01, false, false, 0.85},
		// Charge exceeds receipt (bank charged more than receipt — fees/surcharges applied after printing)
		{"charge_exceeds_small", 3.0, false, true, 0.85},   // ≤5%: falls through to normal tier
		{"charge_exceeds_moderate", 8.0, false, true, 0.75}, // 5–20%: gentle penalty
		{"charge_exceeds_large", 25.0, false, true, 0.55},   // 20–35%: heavier penalty, still visible
		{"charge_exceeds_dropped", 40.0, false, true, 0.0},  // >35%: too large, DROP
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scoreAmount(tt.pctDiff, tt.isNonUSD, tt.chargeExceeds)
			if got != tt.want {
				t.Errorf("scoreAmount(%.2f, %v, %v) = %.2f, want %.2f", tt.pctDiff, tt.isNonUSD, tt.chargeExceeds, got, tt.want)
			}
		})
	}
}

func TestScoreDate(t *testing.T) {
	tests := []struct {
		days int
		want float64
	}{
		{0, 1.0},
		{1, 0.95},
		{2, 0.85},
		{3, 0.70},
		{4, 0.50},
		{5, 0.50},
		{6, 0.0},
		{30, 0.0},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := scoreDate(tt.days)
			if got != tt.want {
				t.Errorf("scoreDate(%d) = %.2f, want %.2f", tt.days, got, tt.want)
			}
		})
	}
}

func TestScoreMerchantPair(t *testing.T) {
	ctx := context.Background()

	aliasRepo := &mockAliasRepo{
		pairs: map[string]bool{
			"sbux|starbucks": true,
		},
	}
	svc := NewScoringService(aliasRepo)

	tests := []struct {
		name       string
		a, b       string
		wantScore  float64
		wantMethod string
	}{
		{"exact", "starbucks", "starbucks", 1.0, "exact"},
		{"substring_b_in_a", "starbucks coffee", "starbucks", 0.90, "substring"},
		{"substring_a_in_b", "starbucks", "starbucks coffee", 0.90, "substring"},
		{"alias", "sbux", "starbucks", 0.92, "alias"},
		// core_exact: both share longest brand word "starbucks" but differ overall
		{"core_exact", "starbucks uptown", "starbucks downtown", 0.88, "core_exact"},
		// levenshtein: single-char edit, ratio < 0.3
		{"levenshtein", "dunkin", "dunkan", 0.75, "levenshtein"},
		// word_overlap_2: two shared meaningful words
		{"word_overlap_2", "blue bottle roasters", "blue bottle coffee", 0.70, "word_overlap_2"},
		// compact: Plaid canonical names stored without spaces match space-separated receipt names
		{"compact_plaid_canonical", "parlordoughnuts", "parlor doughnuts nashville sobro", 0.85, "compact"},
		{"compact_reverse", "parlor doughnuts nashville sobro", "parlordoughnuts", 0.85, "compact"},
		// word_overlap_1: one shared meaningful word
		{"word_overlap_1", "blue bottle", "blue mountain", 0.45, "word_overlap_1"},
		// none: no overlap at any level
		{"none", "amazon", "netflix", 0.0, "none"},
		// empty inputs
		{"empty_a", "", "starbucks", 0.0, "none"},
		{"empty_b", "starbucks", "", 0.0, "none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotScore, gotMethod := svc.scoreMerchantPair(ctx, tt.a, tt.b)
			if gotScore != tt.wantScore {
				t.Errorf("scoreMerchantPair(%q, %q) score = %.2f, want %.2f", tt.a, tt.b, gotScore, tt.wantScore)
			}
			if gotMethod != tt.wantMethod {
				t.Errorf("scoreMerchantPair(%q, %q) method = %q, want %q", tt.a, tt.b, gotMethod, tt.wantMethod)
			}
		})
	}
}

func TestScoreCandidates(t *testing.T) {
	ctx := context.Background()
	svc := NewScoringService(&mockAliasRepo{})

	receiptDate := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	total := 10.0
	merchant := "Starbucks"

	receipt := &domain.Receipt{
		Total:        &total,
		Date:         &receiptDate,
		MerchantName: &merchant,
	}

	t.Run("nil_total_returns_nil", func(t *testing.T) {
		r := &domain.Receipt{}
		got := svc.ScoreCandidates(ctx, r, []domain.Transaction{{TransactionID: "tx1", Amount: 10, Date: "2025-03-15"}})
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("zero_amount_tx_dropped", func(t *testing.T) {
		got := svc.ScoreCandidates(ctx, receipt, []domain.Transaction{
			{TransactionID: "tx1", Amount: 0, Date: "2025-03-15"},
		})
		if len(got) != 0 {
			t.Errorf("expected zero candidates, got %d", len(got))
		}
	})

	t.Run("date_too_far_dropped", func(t *testing.T) {
		got := svc.ScoreCandidates(ctx, receipt, []domain.Transaction{
			{TransactionID: "tx1", Amount: 10, Date: "2025-03-21"}, // 6 days away
		})
		if len(got) != 0 {
			t.Errorf("expected zero candidates (date > 5 days), got %d", len(got))
		}
	})

	t.Run("amount_too_far_dropped", func(t *testing.T) {
		got := svc.ScoreCandidates(ctx, receipt, []domain.Transaction{
			{TransactionID: "tx1", Amount: 7.0, Date: "2025-03-15"}, // 30% below receipt — downward diff > 20%, no charge-exceeds path
		})
		if len(got) != 0 {
			t.Errorf("expected zero candidates (amount > 20%%), got %d", len(got))
		}
	})

	t.Run("happy_path_scores_correctly", func(t *testing.T) {
		got := svc.ScoreCandidates(ctx, receipt, []domain.Transaction{
			{TransactionID: "tx1", Amount: 10.0, Date: "2025-03-15", Name: "STARBUCKS #123"},
		})
		if len(got) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(got))
		}
		cs := got[0]
		if cs.AmountScore != 1.0 {
			t.Errorf("AmountScore = %.2f, want 1.0", cs.AmountScore)
		}
		if cs.DateScore != 1.0 {
			t.Errorf("DateScore = %.2f, want 1.0", cs.DateScore)
		}
		// "starbucks" contained in "starbucks 123" → substring → 0.90
		if cs.MerchantScore != 0.90 {
			t.Errorf("MerchantScore = %.2f, want 0.90", cs.MerchantScore)
		}
		wantComposite := 1.0*0.35 + 1.0*0.25 + 0.90*0.40
		if cs.CompositeScore != wantComposite {
			t.Errorf("CompositeScore = %.4f, want %.4f", cs.CompositeScore, wantComposite)
		}
	})

	t.Run("sorted_descending_by_composite", func(t *testing.T) {
		// tx1: same day, exact merchant name → CompositeScore = 1.0
		// tx2: 1 day off, substring match → CompositeScore ≈ 0.9475
		got := svc.ScoreCandidates(ctx, receipt, []domain.Transaction{
			{TransactionID: "tx2", Amount: 10.0, Date: "2025-03-14", Name: "Starbucks Coffee"},
			{TransactionID: "tx1", Amount: 10.0, Date: "2025-03-15", Name: "Starbucks"},
		})
		if len(got) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(got))
		}
		if got[0].TransactionID != "tx1" {
			t.Errorf("expected tx1 first (higher composite), got %s first", got[0].TransactionID)
		}
		if got[0].CompositeScore <= got[1].CompositeScore {
			t.Errorf("results not sorted descending: %.4f <= %.4f", got[0].CompositeScore, got[1].CompositeScore)
		}
	})
}
