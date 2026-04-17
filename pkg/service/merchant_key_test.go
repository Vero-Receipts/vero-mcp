package service

import "testing"

func TestNormalizeMerchantSuffix(t *testing.T) {
	cases := map[string]string{
		"":                      "",
		"   ":                   "",
		"Cloudflare":            "cloudflare",
		"Cloudflare, Inc.":      "cloudflare,",
		"Cloudflare LLC":        "cloudflare",
		"DigitalOcean LLC":      "digitalocean",
		"DigitalOcean":          "digitalocean",
		"Acme Corp.":            "acme",
		"Acme CORP":             "acme",
		"My Business Co.":       "my business",
		"SomethingElse plc":     "somethingelse",
		"Apple Inc.":            "apple",
		"  Stripe  ":            "stripe",
		"Has trailing space  ":  "has trailing space",
	}
	for in, want := range cases {
		if got := normalizeMerchantSuffix(in); got != want {
			t.Errorf("normalizeMerchantSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}
