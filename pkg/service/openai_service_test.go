package service

import (
	"strconv"
	"testing"
	"time"
)

func TestExtractTotal(t *testing.T) {
	tests := []struct {
		name    string
		rawText string
		want    *float64
	}{
		{
			name:    "simple total",
			rawText: "Items\nTotal: $42.50\nThank you",
			want:    floatPtr(42.50),
		},
		{
			name:    "skips subtotal",
			rawText: "Subtotal: $35.00\nTax: $5.00\nTotal: $40.00",
			want:    floatPtr(40.00),
		},
		{
			name:    "skips sub total with space",
			rawText: "Sub Total: $35.00\nTotal: $40.00",
			want:    floatPtr(40.00),
		},
		{
			name:    "last total wins",
			rawText: "Total: $10.00\nTotal: $20.00",
			want:    floatPtr(20.00),
		},
		{
			name:    "empty text",
			rawText: "",
			want:    nil,
		},
		{
			name:    "no total line",
			rawText: "Just some text\nNo amounts here",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTotal(tt.rawText)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %f", *got)
				}
				return
			}
			if got == nil {
				t.Errorf("expected %f, got nil", *tt.want)
				return
			}
			if *got != *tt.want {
				t.Errorf("expected %f, got %f", *tt.want, *got)
			}
		})
	}
}

func TestExtractDate(t *testing.T) {
	tests := []struct {
		name    string
		rawText string
		want    string // YYYY-MM-DD or empty
	}{
		{
			name:    "MM/DD/YYYY format",
			rawText: "Date: 03/15/2025\nItems...",
			want:    "2025-03-15",
		},
		{
			name:    "YYYY-MM-DD format",
			rawText: "Date: 2025-03-15\nItems...",
			want:    "2025-03-15",
		},
		{
			name:    "MM-DD-YYYY format",
			rawText: "Receipt from 03-15-2025",
			want:    "2025-03-15",
		},
		{
			name:    "empty text",
			rawText: "",
			want:    "",
		},
		{
			name:    "no date",
			rawText: "Just items and totals",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractDate(tt.rawText)
			if tt.want == "" {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Errorf("expected %s, got nil", tt.want)
				return
			}
			wantTime, _ := time.Parse("2006-01-02", tt.want)
			if !got.Equal(wantTime) {
				t.Errorf("expected %v, got %v", wantTime, *got)
			}
		})
	}
}

func floatPtr(f float64) *float64 { return &f }

// TestParseReceiptCompletion_IsSubscription verifies the OCR parser lifts the
// isSubscription flag out of the model's structured output.
func TestParseReceiptCompletion_IsSubscription(t *testing.T) {
	mk := func(content string) []byte {
		return []byte(`{"choices":[{"message":{"content":` + strconv.Quote(content) + `}}]}`)
	}
	cases := []struct {
		name    string
		content string
		want    *bool
	}{
		{"subscription true", `{"merchantName":"SoundCloud","total":11.99,"lineItems":[],"isSubscription":true}`, boolPtr(true)},
		{"subscription false", `{"merchantName":"Blue Bottle","total":6.50,"lineItems":[],"isSubscription":false}`, boolPtr(false)},
		{"absent defaults false", `{"merchantName":"Blue Bottle","total":6.50,"lineItems":[]}`, boolPtr(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := ParseReceiptCompletion(mk(tc.content))
			if r.Error != "" {
				t.Fatalf("parse error: %s", r.Error)
			}
			if r.IsSubscription == nil || tc.want == nil {
				if r.IsSubscription != tc.want {
					t.Fatalf("IsSubscription = %v, want %v", r.IsSubscription, tc.want)
				}
				return
			}
			if *r.IsSubscription != *tc.want {
				t.Fatalf("IsSubscription = %v, want %v", *r.IsSubscription, *tc.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
