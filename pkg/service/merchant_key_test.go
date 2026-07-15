package service

import "testing"

func TestNormalizeMerchantSuffix(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"   ":        "",
		"Cloudflare": "cloudflare",
		// The legal form is normally comma-separated. Matching only whitespace
		// left the comma behind ("cloudflare,"), which never matched the bare
		// name and so silently broke dedup and transaction matching.
		"Cloudflare, Inc.":        "cloudflare",
		"American Airlines, Inc.": "american airlines",
		"Acme , LLC":              "acme",
		"Cloudflare LLC":          "cloudflare",
		"DigitalOcean LLC":        "digitalocean",
		"DigitalOcean":            "digitalocean",
		"Acme Corp.":              "acme",
		"Acme CORP":               "acme",
		"My Business Co.":         "my business",
		"SomethingElse plc":       "somethingelse",
		"Apple Inc.":              "apple",
		"  Stripe  ":              "stripe",
		"Has trailing space  ":    "has trailing space",
		// A comma that is not separating a suffix must survive.
		"Smith, Jones Plumbing": "smith, jones plumbing",
	}
	for in, want := range cases {
		if got := normalizeMerchantSuffix(in); got != want {
			t.Errorf("normalizeMerchantSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}
