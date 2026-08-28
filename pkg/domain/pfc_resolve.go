package domain

import (
	"encoding/json"
	"strings"
)

// Resolving a transaction's spending category, and deciding whether it counts
// as spending at all.
//
// Both are questions the clients used to answer for themselves, each with its
// own copy of the rules, which is how the same account came to show different
// totals on different screens. The definitions live here now, and the reporting
// queries and the clients read the same ones.

// LegacyToPFC maps a lowercased legacy Plaid category (the first element of the
// `category` array) onto a personal-finance-category primary.
//
// It is still load-bearing: pfc_primary was added nullable and never
// backfilled, so transactions synced before that column existed carry only the
// legacy array and resolve through this map.
var LegacyToPFC = map[string]string{
	"food and drink":                 "FOOD_AND_DRINK",
	"restaurants":                    "FOOD_AND_DRINK",
	"groceries":                      "FOOD_AND_DRINK",
	"coffee":                         "FOOD_AND_DRINK",
	"fast food":                      "FOOD_AND_DRINK",
	"shops":                          "GENERAL_MERCHANDISE",
	"shopping":                       "GENERAL_MERCHANDISE",
	"merchandise":                    "GENERAL_MERCHANDISE",
	"travel":                         "TRAVEL",
	"airlines and aviation services": "TRAVEL",
	"hotels":                         "TRAVEL",
	"transportation":                 "TRANSPORTATION",
	"taxi":                           "TRANSPORTATION",
	"ride share":                     "TRANSPORTATION",
	"gas":                            "TRANSPORTATION",
	"automotive":                     "TRANSPORTATION",
	"recreation":                     "ENTERTAINMENT",
	"entertainment":                  "ENTERTAINMENT",
	"arts and entertainment":         "ENTERTAINMENT",
	"gyms and fitness centers":       "PERSONAL_CARE",
	"personal care":                  "PERSONAL_CARE",
	"health and fitness":             "PERSONAL_CARE",
	"service":                        "GENERAL_SERVICES",
	"services":                       "GENERAL_SERVICES",
	"subscription":                   "GENERAL_SERVICES",
	"software":                       "GENERAL_SERVICES",
	"healthcare":                     "MEDICAL",
	"medical":                        "MEDICAL",
	"pharmacies":                     "MEDICAL",
	"home improvement":               "HOME_IMPROVEMENT",
	"home":                           "HOME_IMPROVEMENT",
	"utilities":                      "RENT_AND_UTILITIES",
	"rent":                           "RENT_AND_UTILITIES",
	"rent and utilities":             "RENT_AND_UTILITIES",
	"loan payments":                  "LOAN_PAYMENTS",
	"transfer":                       "TRANSFER_OUT",
	"transfer in":                    "TRANSFER_IN",
	"transfer out":                   "TRANSFER_OUT",
	"payroll":                        "INCOME",
	"income":                         "INCOME",
	"deposit":                        "INCOME",
	"community":                      "GOVERNMENT_AND_NON_PROFIT",
	"government and non-profit":      "GOVERNMENT_AND_NON_PROFIT",
	"bank fees":                      "BANK_FEES",
	"fees and charges":               "BANK_FEES",
	// Plaid files education under GENERAL_SERVICES (GENERAL_SERVICES_EDUCATION);
	// there is no EDUCATION primary in the taxonomy.
	"education": "GENERAL_SERVICES",
}

// UncategorizedPrimary is where a transaction lands when neither pfc_primary
// nor the legacy array identifies a category.
const UncategorizedPrimary = "OTHER"

// ExcludedPrimaries are the categories a spending report leaves out: money
// moving between a user's own accounts, the repayment of a balance, and money
// arriving. Counting any of them would double-count spending that is already
// represented by the purchases themselves.
//
// Deliberately NOT the same set as the category-vetting pipeline's money
// movement list, which also holds BANK_FEES and RENT_AND_UTILITIES. Those are
// genuine spending to someone reading a report, and dropping them here would
// change every total the clients have ever shown.
var ExcludedPrimaries = []string{
	"LOAN_PAYMENTS",
	"TRANSFER_IN",
	"TRANSFER_OUT",
	"INCOME",
}

// IsExcludedPrimary reports whether a resolved primary is money movement rather
// than spending.
func IsExcludedPrimary(primary string) bool {
	for _, excluded := range ExcludedPrimaries {
		if primary == excluded {
			return true
		}
	}
	return false
}

// ResolvePrimary derives a transaction's spending category from its
// personal-finance-category column, falling back to the legacy category array.
//
// An empty pfcPrimary is treated as absent rather than as a category named "",
// which is what the clients do and what the rows written before pfc_primary
// existed require.
func ResolvePrimary(pfcPrimary *string, category json.RawMessage) string {
	if pfcPrimary != nil && *pfcPrimary != "" {
		return *pfcPrimary
	}

	if legacy, ok := LegacyToPFC[firstLegacyCategory(category)]; ok {
		return legacy
	}
	return UncategorizedPrimary
}

// firstLegacyCategory reads the first element of the legacy category array,
// lowercased, or "" when the column is absent, malformed or empty. The column
// is JSONB on Postgres and TEXT holding the same JSON on SQLite, so both arrive
// here as bytes to unmarshal.
func firstLegacyCategory(category json.RawMessage) string {
	if len(category) == 0 {
		return ""
	}

	var values []string
	if err := json.Unmarshal(category, &values); err != nil || len(values) == 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(values[0]))
}
