package service

import "testing"

func TestParseAddressCityState(t *testing.T) {
	cases := []struct {
		addr, city, state string
	}{
		{"800 Woodward Ave, Detroit, MI 48226", "Detroit", "MI"},
		{"735 GRISWOLD ST, DETROIT, MI", "DETROIT", "MI"},
		{"500 Rep John Lewis Way S, Nashville, TN 37203", "Nashville", "TN"},
		{"123 Main Street, Seattle, WA 98101", "Seattle", "WA"},
		{"2460 MARKET ST", "", ""},                       // single segment → nothing
		{"", "", ""},                                     // empty
		{"Some Shop, Brooklyn, New York 11201", "Brooklyn", "NY"}, // full state name
		{"1 Plaza, Austin, TX 78701-1234", "Austin", "TX"},        // zip+4
		{"Street, City, ZZ 00000", "", ""},               // unknown state → reject
	}
	for _, c := range cases {
		city, state := ParseAddressCityState(c.addr)
		if city != c.city || state != c.state {
			t.Errorf("ParseAddressCityState(%q) = (%q,%q), want (%q,%q)", c.addr, city, state, c.city, c.state)
		}
	}
}

func TestNormalizeUSState(t *testing.T) {
	cases := map[string]string{
		"mi": "MI", "MI": "MI", "Michigan": "MI", "new york": "NY",
		"NEW YORK": "NY", "Tennessee": "TN", "": "", "ZZ": "", "Freedonia": "",
	}
	for in, want := range cases {
		if got := NormalizeUSState(in); got != want {
			t.Errorf("NormalizeUSState(%q) = %q, want %q", in, got, want)
		}
	}
}
