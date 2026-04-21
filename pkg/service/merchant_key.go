package service

import (
	"context"
	"regexp"
	"strings"
)

// merchantSuffixRe strips common business suffixes so "DigitalOcean LLC" and
// "DigitalOcean" produce the same dedup key. Mirrors the regex used in the
// cleanup migration 000025.
var merchantSuffixRe = regexp.MustCompile(`(?i)\s+(llc|inc\.?|corp\.?|ltd\.?|co\.?|llp|plc)$`)

// normalizeMerchantSuffix lower-cases, trims whitespace, and strips common
// business suffixes. It is the regex-only fallback used when no alias entry
// is available; the alias table should handle the harder cases.
func normalizeMerchantSuffix(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSpace(merchantSuffixRe.ReplaceAllString(lower, ""))
}

// ComputeMerchantKey returns a normalized merchant key suitable for Tier-2
// duplicate detection. It first consults merchant_aliases to resolve aliases
// to their canonical form (e.g. "Apple Services" -> "Apple", populated by
// the LLM during matching), then strips common business suffixes via regex.
// Returns an empty string for an empty or whitespace-only input.
func (s *ReceiptService) ComputeMerchantKey(ctx context.Context, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if s.matchingSvc != nil && s.matchingSvc.aliasRepo != nil {
		if canonical, err := s.matchingSvc.aliasRepo.FindCanonical(ctx, name); err == nil && canonical != "" {
			name = canonical
		}
	}
	return normalizeMerchantSuffix(name)
}
