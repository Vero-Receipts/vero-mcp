package domain

import (
	"encoding/json"
	"testing"
)

func strptr(s string) *string { return &s }

func TestResolvePrimary(t *testing.T) {
	tests := []struct {
		name       string
		pfcPrimary *string
		category   json.RawMessage
		want       string
	}{
		{
			name:       "pfc_primary wins over the legacy array",
			pfcPrimary: strptr("FOOD_AND_DRINK"),
			category:   json.RawMessage(`["Travel"]`),
			want:       "FOOD_AND_DRINK",
		},
		{
			// The clients read this column with `||`, so "" falls through to
			// the legacy array rather than becoming a category of its own.
			name:       "empty pfc_primary falls through to the legacy array",
			pfcPrimary: strptr(""),
			category:   json.RawMessage(`["Travel"]`),
			want:       "TRAVEL",
		},
		{
			name:       "null pfc_primary falls through to the legacy array",
			pfcPrimary: nil,
			category:   json.RawMessage(`["Restaurants", "Coffee Shop"]`),
			want:       "FOOD_AND_DRINK",
		},
		{
			name:       "legacy lookup is case and whitespace insensitive",
			pfcPrimary: nil,
			category:   json.RawMessage(`["  Fast Food  "]`),
			want:       "FOOD_AND_DRINK",
		},
		{
			name:       "only the first legacy element is consulted",
			pfcPrimary: nil,
			category:   json.RawMessage(`["Travel", "Restaurants"]`),
			want:       "TRAVEL",
		},
		{
			name:       "an unmapped legacy value is uncategorized",
			pfcPrimary: nil,
			category:   json.RawMessage(`["Bespoke Taxidermy"]`),
			want:       UncategorizedPrimary,
		},
		{
			name:       "no category at all is uncategorized",
			pfcPrimary: nil,
			category:   nil,
			want:       UncategorizedPrimary,
		},
		{
			name:       "an empty array is uncategorized",
			pfcPrimary: nil,
			category:   json.RawMessage(`[]`),
			want:       UncategorizedPrimary,
		},
		{
			name:       "malformed category JSON is uncategorized, not an error",
			pfcPrimary: nil,
			category:   json.RawMessage(`{"not":"an array"}`),
			want:       UncategorizedPrimary,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvePrimary(tc.pfcPrimary, tc.category); got != tc.want {
				t.Errorf("ResolvePrimary() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsExcludedPrimary(t *testing.T) {
	for _, primary := range ExcludedPrimaries {
		if !IsExcludedPrimary(primary) {
			t.Errorf("IsExcludedPrimary(%q) = false, want true", primary)
		}
	}

	// BANK_FEES and RENT_AND_UTILITIES are money movement to the category
	// vetting pipeline but spending to a report, and the two must not converge.
	for _, primary := range []string{"FOOD_AND_DRINK", "BANK_FEES", "RENT_AND_UTILITIES", "OTHER"} {
		if IsExcludedPrimary(primary) {
			t.Errorf("IsExcludedPrimary(%q) = true, want false", primary)
		}
	}
}

// Every value the legacy map produces should be a primary the taxonomy knows,
// or the reports will group under a key nothing else in the system recognises.
func TestLegacyToPFCProducesKnownPrimaries(t *testing.T) {
	known := make(map[string]bool)
	for _, primary := range PrimaryEnum() {
		known[primary] = true
	}

	for legacy, primary := range LegacyToPFC {
		if !known[primary] {
			t.Errorf("LegacyToPFC[%q] = %q, which is not in the PFC taxonomy", legacy, primary)
		}
	}
}
