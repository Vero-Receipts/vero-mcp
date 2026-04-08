package service

import "testing"

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
