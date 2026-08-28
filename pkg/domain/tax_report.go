package domain

import "strings"

// The transaction-derived lines of a tax estimate.
//
// Each one is a category, sometimes narrowed to the sub-categories whose name
// contains a particular word — "charitable donations" is the non-profit
// category restricted to donations, "cell phone" is utilities restricted to
// telephone and mobile. The definitions used to live in the client that drew
// the estimate; they live here now, so the totals and the transactions behind
// them are selected by the same rules.

// TaxLineDefinition selects the transactions behind one line of an estimate.
type TaxLineDefinition struct {
	// Key names the line to the client, which supplies its own label and
	// whatever tax treatment applies.
	Key string
	// Primary is the PFC category the line draws from.
	Primary string
	// DetailContains narrows to sub-categories whose detailed value contains
	// any of these, case-insensitively. Empty takes the whole category.
	DetailContains []string
}

// TaxLineDefinitions are the lines an estimate can derive from spending. The
// rest of an estimate comes from the user's tax profile and what they type in,
// neither of which is a transaction.
var TaxLineDefinitions = []TaxLineDefinition{
	{Key: "charitable_donations", Primary: "GOVERNMENT_AND_NON_PROFIT", DetailContains: []string{"DONATION", "CHARIT"}},
	{Key: "large_medical_expenses", Primary: "MEDICAL"},
	{Key: "client_meals", Primary: "FOOD_AND_DRINK"},
	{Key: "cell_phone", Primary: "RENT_AND_UTILITIES", DetailContains: []string{"TELEPHONE", "MOBILE"}},
	{Key: "home_internet", Primary: "RENT_AND_UTILITIES", DetailContains: []string{"INTERNET", "CABLE"}},
	{Key: "rental_repairs", Primary: "HOME_IMPROVEMENT"},
}

// TaxLineFor returns the definition for a line key.
func TaxLineFor(key string) (TaxLineDefinition, bool) {
	for _, line := range TaxLineDefinitions {
		if line.Key == key {
			return line, true
		}
	}
	return TaxLineDefinition{}, false
}

// LowerDetailContains is the needle set folded to lower case, for a
// case-insensitive comparison that behaves the same on both dialects.
func (d TaxLineDefinition) LowerDetailContains() []string {
	out := make([]string, 0, len(d.DetailContains))
	for _, needle := range d.DetailContains {
		out = append(out, strings.ToLower(needle))
	}
	return out
}

// TaxLineTotal is what one line of an estimate came to, and over how many
// transactions.
//
// The transactions themselves are not here. A year of restaurant meals is
// thousands of rows, and an estimate only draws the totals — a client that
// needs the rows, to itemize them into a file, asks the transaction list for
// them a page at a time.
type TaxLineTotal struct {
	Key    string
	Amount float64
	Count  int
}
