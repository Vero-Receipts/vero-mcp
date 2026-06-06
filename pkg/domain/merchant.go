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
	ID            uuid.UUID         `json:"id"`
	CanonicalName string            `json:"canonical_name"`
	NormalizedKey string            `json:"normalized_key"`
	PlaidEntityID *string           `json:"plaid_entity_id,omitempty"`
	Domain        *string           `json:"domain,omitempty"`
	LogoCDNURL    *string           `json:"logo_cdn_url,omitempty"`
	Location      *MerchantLocation `json:"location,omitempty"`
	CreatedAt     time.Time         `json:"created_at,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at,omitempty"`
}

// MerchantLocation mirrors Plaid's transaction `location` object. We persist
// the whole thing (as a JSONB column) so future needs — lat/lon, postal code,
// store number — are already in the DB, even though only City currently feeds
// logo disambiguation. All fields are optional; a merchant seen without
// location data has a nil Location.
type MerchantLocation struct {
	Address     *string  `json:"address,omitempty"`
	City        *string  `json:"city,omitempty"`
	Region      *string  `json:"region,omitempty"`
	PostalCode  *string  `json:"postal_code,omitempty"`
	Country     *string  `json:"country,omitempty"`
	Lat         *float64 `json:"lat,omitempty"`
	Lon         *float64 `json:"lon,omitempty"`
	StoreNumber *string  `json:"store_number,omitempty"`
}
