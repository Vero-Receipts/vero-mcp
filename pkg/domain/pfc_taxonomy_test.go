package domain

import (
	"strings"
	"testing"
)

func TestPFCTaxonomy_DetailedBelongsToItsPrimary(t *testing.T) {
	for primary, detaileds := range pfcTaxonomy {
		for _, d := range detaileds {
			if !strings.HasPrefix(d, primary+"_") {
				t.Errorf("detailed %q is listed under primary %q but does not extend it", d, primary)
			}
		}
	}
}

func TestPFCTaxonomy_PrimaryForResolvesEveryDetailed(t *testing.T) {
	for _, detaileds := range pfcTaxonomy {
		for _, d := range detaileds {
			if _, ok := PrimaryFor(d); !ok {
				t.Errorf("PrimaryFor(%q) failed", d)
			}
		}
	}
}

// A correction names only a primary, so its detailed value comes from this map.
// A primary without a catch-all leaf would leave a correction with nothing to
// write, so the mapping has to be total over the enum the model picks from.
func TestPFCTaxonomy_EveryPrimaryHasACatchAllLeaf(t *testing.T) {
	for _, p := range PrimaryEnum() {
		d, ok := OtherDetailedFor(p)
		if !ok {
			t.Errorf("primary %q has no catch-all detailed category", p)
			continue
		}
		if !ValidPFC(p, d) {
			t.Errorf("catch-all %q is not a valid detailed category for %q", d, p)
		}
	}
}

func TestPFCTaxonomy_PrimaryEnumIsTheSixteenPlaidPrimaries(t *testing.T) {
	if got := len(PrimaryEnum()); got != 16 {
		t.Errorf("PrimaryEnum has %d entries, want Plaid's 16 primaries", got)
	}
	for _, p := range PrimaryEnum() {
		if p == uncategorizedPrimary {
			t.Error("OTHER must not be offered: correcting a real guess to uncategorized is worse than leaving it")
		}
	}
}

// The enum ships in every request body. Structured Outputs caps a single enum at
// 250 values, and beyond that caps their combined length at 7500 bytes; blowing
// either limit would fail every call at once, so a taxonomy refresh trips this
// test rather than production.
func TestPFCTaxonomy_EnumFitsStructuredOutputLimits(t *testing.T) {
	enum := PrimaryEnum()
	if len(enum) > 250 {
		total := 0
		for _, d := range enum {
			total += len(d)
		}
		if total > 7500 {
			t.Fatalf("enum has %d values totalling %d bytes, over the 7500-byte limit", len(enum), total)
		}
	}
}

func TestPFCTaxonomy_UncategorizedIsValidButNotOffered(t *testing.T) {
	if !ValidPFC("OTHER", uncategorized) {
		t.Error("OTHER_OTHER must validate: Plaid supplies it and stored rows carry it")
	}
	for _, p := range PrimaryEnum() {
		if p == uncategorizedPrimary {
			t.Fatal("OTHER must not be offered as a correction target")
		}
	}
}

// Plaid emits these but omits them from its published taxonomy CSV. They appear
// on real transactions, so the gate has to accept them.
func TestPFCTaxonomy_AcceptsCategoriesPlaidEmitsButDoesNotPublish(t *testing.T) {
	for _, tc := range []struct{ primary, detailed string }{
		{"LOAN_PAYMENTS", "LOAN_PAYMENTS_CASH_ADVANCES"},
		{"TRANSFER_OUT", "TRANSFER_OUT_TRANSFER_OUT_FROM_APPS"},
		{"OTHER", "OTHER_OTHER"},
	} {
		if !ValidPFC(tc.primary, tc.detailed) {
			t.Errorf("ValidPFC(%q, %q) = false, but Plaid emits it in production", tc.primary, tc.detailed)
		}
	}
}

func TestValidPFC_Accepts(t *testing.T) {
	for _, tc := range []struct{ primary, detailed string }{
		{"FOOD_AND_DRINK", "FOOD_AND_DRINK_COFFEE"},
		{"FOOD_AND_DRINK", "FOOD_AND_DRINK_GROCERIES"},
		{"TRANSPORTATION", "TRANSPORTATION_GAS"},
		{"GENERAL_MERCHANDISE", "GENERAL_MERCHANDISE_OFFICE_SUPPLIES"},
		{"MEDICAL", "MEDICAL_PHARMACIES_AND_SUPPLEMENTS"},
	} {
		if !ValidPFC(tc.primary, tc.detailed) {
			t.Errorf("ValidPFC(%q, %q) = false, want true", tc.primary, tc.detailed)
		}
	}
}

// Every string here was written to production by the old correction path. They
// are the reason this package exists.
func TestValidPFC_RejectsTheCategoriesProductionInvented(t *testing.T) {
	invented := []string{
		"GENERAL_SERVICES_FEE",
		"GENERAL_SERVICES_SUBSCRIPTION",
		"GENERAL_SERVICES_CLOUD_SERVICES",
		"GENERAL_SERVICES_MEMBERSHIP_FEES",
		"GENERAL_SERVICES_REGISTRATION_FEES",
		"GENERAL_MERCHANDISE_SOFTWARE",
		"GENERAL_MERCHANDISE_GIFT_CARD",
		"GENERAL_MERCHANDISE_APPAREL",
		"GENERAL_MERCHANDISE_SPORTS_EQUIPMENT",
		"ENTERTAINMENT_CLIMBING",
		"ENTERTAINMENT_TICKETS",
		"ENTERTAINMENT_GYMS_AND_FITNESS_CENTERS",
		"MEDICAL_SUPPLEMENTS",
		"PERSONAL_CARE_HAIR_CARE",
		"TRANSPORTATION_RIDESHARE",
		"TRANSPORTATION_RIDE_SHARING",
		"TRANSPORTATION_FLIGHTS",
		"RENT_AND_UTILITIES_BROADBAND",
		"FOOD_AND_DRINK_ALCOHOL",
		"FOOD_AND_DRINK_SNACKS",
		// The old prompt asked for PRIMARY_SUBCATEGORY strings and the rules
		// stage synthesised PRIMARY_OTHER; both shapes are near-misses for real
		// categories, which is exactly why they went unnoticed.
		"FOOD_AND_DRINK_OTHER",
		"GENERAL_MERCHANDISE_OTHER",
		"GENERAL_SERVICES_OTHER_SERVICES",
	}
	for _, d := range invented {
		primary, ok := PrimaryFor(d)
		if ok {
			t.Errorf("PrimaryFor(%q) = %q, but this category does not exist", d, primary)
		}
		parts := strings.SplitN(d, "_", 2)
		if ValidPFC(parts[0], d) {
			t.Errorf("ValidPFC accepted invented category %q", d)
		}
	}
}

// A primary used where a detailed belongs, and a detailed paired with the wrong
// primary. The schema enum makes the second unrepresentable for model output;
// this covers every other caller.
func TestValidPFC_RejectsMalformedPairs(t *testing.T) {
	if ValidPFC("TRANSFER_IN", "TRANSFER_IN") {
		t.Error("a bare primary must not validate as a detailed category")
	}
	if ValidPFC("FOOD_AND_DRINK", "TRANSPORTATION_GAS") {
		t.Error("a detailed category must not validate under the wrong primary")
	}
}

func TestTaxonomyPrompt_ListsEveryPrimaryAndDetailed(t *testing.T) {
	prompt := TaxonomyPrompt()
	for primary, detaileds := range pfcTaxonomy {
		if !strings.Contains(prompt, primary) {
			t.Errorf("TaxonomyPrompt omits primary %q", primary)
		}
		for _, d := range detaileds {
			if !strings.Contains(prompt, d) {
				t.Errorf("TaxonomyPrompt omits detailed %q", d)
			}
		}
	}
}
