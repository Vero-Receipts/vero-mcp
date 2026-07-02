package service

import (
	"regexp"
	"strings"
)

// Address → (city, state) parsing for receipt OCR. The OpenAI response already
// returns a free-text merchant address (e.g. "500 Rep John Lewis Way S, Nashville,
// TN 37203"); we derive city/state deterministically here so it is testable offline
// and does not change the OpenAI request (golden tests stay stable). See
// merchant-catalog-mapping-design.md §4.

var zipRe = regexp.MustCompile(`^\d{5}(-\d{4})?$`)

// ParseAddressCityState extracts the city and 2-letter (normalized) state from a
// US-style comma-separated address. Returns empty strings when it can't confidently
// find both (e.g. a single-segment street address).
func ParseAddressCityState(address string) (city, state string) {
	parts := strings.Split(strings.TrimSpace(address), ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	// Drop trailing empty segments.
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) < 2 {
		return "", ""
	}
	state = stateFromSegment(parts[len(parts)-1])
	if state == "" {
		return "", ""
	}
	city = parts[len(parts)-2]
	return city, state
}

// stateFromSegment pulls a normalized 2-letter state from a "<state> <zip>" tail
// segment (e.g. "TN 37203", "Tennessee", "New York 10001").
func stateFromSegment(seg string) string {
	tokens := strings.Fields(seg)
	for len(tokens) > 0 && zipRe.MatchString(tokens[len(tokens)-1]) {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 {
		return ""
	}
	return NormalizeUSState(strings.Join(tokens, " "))
}

// NormalizeUSState maps a US state full name or abbreviation (case-insensitive) to
// its 2-letter uppercase code, or "" if unrecognized. Shared by the receipt parser
// and Stage 1's state gate.
func NormalizeUSState(s string) string {
	key := strings.ToUpper(strings.TrimSpace(s))
	if key == "" {
		return ""
	}
	if _, ok := stateAbbrevs[key]; ok {
		return key
	}
	if abbr, ok := stateNames[key]; ok {
		return abbr
	}
	return ""
}

var stateAbbrevs = map[string]struct{}{
	"AL": {}, "AK": {}, "AZ": {}, "AR": {}, "CA": {}, "CO": {}, "CT": {}, "DE": {},
	"FL": {}, "GA": {}, "HI": {}, "ID": {}, "IL": {}, "IN": {}, "IA": {}, "KS": {},
	"KY": {}, "LA": {}, "ME": {}, "MD": {}, "MA": {}, "MI": {}, "MN": {}, "MS": {},
	"MO": {}, "MT": {}, "NE": {}, "NV": {}, "NH": {}, "NJ": {}, "NM": {}, "NY": {},
	"NC": {}, "ND": {}, "OH": {}, "OK": {}, "OR": {}, "PA": {}, "RI": {}, "SC": {},
	"SD": {}, "TN": {}, "TX": {}, "UT": {}, "VT": {}, "VA": {}, "WA": {}, "WV": {},
	"WI": {}, "WY": {}, "DC": {},
}

var stateNames = map[string]string{
	"ALABAMA": "AL", "ALASKA": "AK", "ARIZONA": "AZ", "ARKANSAS": "AR",
	"CALIFORNIA": "CA", "COLORADO": "CO", "CONNECTICUT": "CT", "DELAWARE": "DE",
	"FLORIDA": "FL", "GEORGIA": "GA", "HAWAII": "HI", "IDAHO": "ID",
	"ILLINOIS": "IL", "INDIANA": "IN", "IOWA": "IA", "KANSAS": "KS",
	"KENTUCKY": "KY", "LOUISIANA": "LA", "MAINE": "ME", "MARYLAND": "MD",
	"MASSACHUSETTS": "MA", "MICHIGAN": "MI", "MINNESOTA": "MN", "MISSISSIPPI": "MS",
	"MISSOURI": "MO", "MONTANA": "MT", "NEBRASKA": "NE", "NEVADA": "NV",
	"NEW HAMPSHIRE": "NH", "NEW JERSEY": "NJ", "NEW MEXICO": "NM", "NEW YORK": "NY",
	"NORTH CAROLINA": "NC", "NORTH DAKOTA": "ND", "OHIO": "OH", "OKLAHOMA": "OK",
	"OREGON": "OR", "PENNSYLVANIA": "PA", "RHODE ISLAND": "RI", "SOUTH CAROLINA": "SC",
	"SOUTH DAKOTA": "SD", "TENNESSEE": "TN", "TEXAS": "TX", "UTAH": "UT",
	"VERMONT": "VT", "VIRGINIA": "VA", "WASHINGTON": "WA", "WEST VIRGINIA": "WV",
	"WISCONSIN": "WI", "WYOMING": "WY", "DISTRICT OF COLUMBIA": "DC",
}
