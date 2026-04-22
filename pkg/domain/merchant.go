package domain

import (
	"time"

	"github.com/google/uuid"
)

// Merchant is the canonical record for a business that appears on transactions.
// Writing rules:
//   - plaid_service sync upserts canonical_name + domain only
//   - LogoService is the sole writer of logo_cdn_url (and merchant_logo_jobs)
type Merchant struct {
	ID             uuid.UUID `json:"id"`
	CanonicalName  string    `json:"canonical_name"`
	NormalizedKey  string    `json:"normalized_key"`
	Domain         *string   `json:"domain,omitempty"`
	LogoCDNURL     *string   `json:"logo_cdn_url,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}
