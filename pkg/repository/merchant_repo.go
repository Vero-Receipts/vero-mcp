package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type MerchantRepo struct {
	BaseRepo
}

func NewMerchantRepo(db *sql.DB, dialect Dialect) *MerchantRepo {
	return &MerchantRepo{BaseRepo: NewBaseRepo(db, dialect)}
}

// NormalizeMerchantKey returns the canonical cache key for a merchant name.
// Lowercases, trims, and collapses internal whitespace so "  Starbucks  " and
// "starbucks" collide into one merchant row.
func NormalizeMerchantKey(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

// Upsert resolves-or-creates a merchant. When plaidEntityID is present it's
// the authoritative key (Plaid's stable cross-transaction merchant id). When
// absent, we fall back to upsert-by-normalized-name.
//
// Existing domain and logo_cdn_url are preserved on conflict so that a raw
// Plaid sync never clobbers a resolved DO CDN URL. First-seen merchants get
// seeded with whatever logo URL Plaid provided as a stopgap.
//
// If two different entity_ids happen to share a normalized_name (e.g.
// regional franchises Plaid tags as distinct entities), the second one
// lands with a disambiguated key (`<name>__<entity_id>`) so both rows
// coexist.
func (r *MerchantRepo) Upsert(ctx context.Context, canonicalName string, websiteDomain, logoURL, plaidEntityID *string, location *domain.MerchantLocation) (*domain.Merchant, error) {
	key := NormalizeMerchantKey(canonicalName)
	if key == "" {
		return nil, errors.New("merchant upsert: empty canonical name")
	}

	// If Plaid provided an entity_id, first try attaching it to any existing
	// row that was seeded (by backfill or earlier sync) without one. This
	// merges the records instead of forking a new one when the entity_id
	// would collide with an existing normalized_key row.
	if plaidEntityID != nil && *plaidEntityID != "" {
		if _, err := r.SQ.Update("merchants").
			Set("plaid_entity_id", *plaidEntityID).
			Set("updated_at", sq.Expr("CURRENT_TIMESTAMP")).
			Where(sq.Eq{"normalized_key": key}).
			Where(sq.Eq{"plaid_entity_id": nil}).
			ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("attach plaid_entity_id: %w", err)
		}
	}

	m, err := r.doUpsert(ctx, canonicalName, key, websiteDomain, logoURL, plaidEntityID, location)
	if err == nil {
		return m, nil
	}

	// Retry with a disambiguated key if this looks like a normalized_key
	// collision AND we have an entity_id to uniquely tag the row.
	if plaidEntityID != nil && *plaidEntityID != "" && isNormalizedKeyCollision(err) {
		disambiguated := key + "__" + *plaidEntityID
		// Log so the collision shows up in monitoring — these are rare and
		// usually worth a human look.
		return r.doUpsert(ctx, canonicalName, disambiguated, websiteDomain, logoURL, plaidEntityID, location)
	}

	return nil, err
}

// marshalLocation serializes a merchant location to a JSON string for the
// jsonb `location` column. A nil location (or one that fails to marshal)
// becomes a SQL NULL.
func marshalLocation(location *domain.MerchantLocation) *string {
	if location == nil {
		return nil
	}
	b, err := json.Marshal(location)
	if err != nil {
		return nil
	}
	s := string(b)
	return &s
}

// doUpsert runs the actual INSERT ... ON CONFLICT with the given normalized_key.
func (r *MerchantRepo) doUpsert(ctx context.Context, canonicalName, key string, websiteDomain, logoURL, plaidEntityID *string, location *domain.MerchantLocation) (*domain.Merchant, error) {
	// Conflict target depends on whether we can key by entity_id.
	conflictTarget := "normalized_key"
	if plaidEntityID != nil && *plaidEntityID != "" {
		conflictTarget = "plaid_entity_id"
	}

	row := r.SQ.Insert("merchants").
		Columns("id", "canonical_name", "normalized_key", "plaid_entity_id", "domain", "logo_cdn_url", "location").
		Values(uuid.New().String(), canonicalName, key, plaidEntityID, websiteDomain, logoURL, marshalLocation(location)).
		Suffix(`ON CONFLICT (`+conflictTarget+`) DO UPDATE SET
			canonical_name = COALESCE(NULLIF(EXCLUDED.canonical_name, ''), merchants.canonical_name),
			domain         = COALESCE(merchants.domain, EXCLUDED.domain),
			logo_cdn_url   = COALESCE(merchants.logo_cdn_url, EXCLUDED.logo_cdn_url),
			location       = COALESCE(merchants.location, EXCLUDED.location),
			updated_at     = CURRENT_TIMESTAMP
		RETURNING id, canonical_name, normalized_key, plaid_entity_id, domain, logo_cdn_url, location, created_at, updated_at`).
		QueryRowContext(ctx)
	return scanMerchant(row)
}

// isNormalizedKeyCollision reports whether err is a unique-constraint violation
// on merchants.normalized_key. Covers both Postgres ("merchants_normalized_key_key")
// and SQLite ("UNIQUE constraint failed: merchants.normalized_key") error shapes.
func isNormalizedKeyCollision(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "normalized_key")
}

func (r *MerchantRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Merchant, error) {
	row := r.SQ.Select("id", "canonical_name", "normalized_key", "plaid_entity_id", "domain", "logo_cdn_url", "location", "created_at", "updated_at").
		From("merchants").
		Where(sq.Eq{"id": id.String()}).
		QueryRowContext(ctx)
	return scanMerchant(row)
}

func (r *MerchantRepo) FindByNormalizedKey(ctx context.Context, key string) (*domain.Merchant, error) {
	row := r.SQ.Select("id", "canonical_name", "normalized_key", "plaid_entity_id", "domain", "logo_cdn_url", "location", "created_at", "updated_at").
		From("merchants").
		Where(sq.Eq{"normalized_key": key}).
		QueryRowContext(ctx)
	return scanMerchant(row)
}

func (r *MerchantRepo) UpdateLogoURL(ctx context.Context, merchantID uuid.UUID, cdnURL string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.SQ.Update("merchants").
		Set("logo_cdn_url", cdnURL).
		Set("updated_at", now).
		Where(sq.Eq{"id": merchantID.String()}).
		ExecContext(ctx)
	return err
}

func (r *MerchantRepo) UpdateDomain(ctx context.Context, merchantID uuid.UUID, domain string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.SQ.Update("merchants").
		Set("domain", domain).
		Set("updated_at", now).
		Where(sq.Eq{"id": merchantID.String()}).
		ExecContext(ctx)
	return err
}

func scanMerchant(row scanner) (*domain.Merchant, error) {
	var m domain.Merchant
	var idStr string
	var locationJSON sql.NullString
	var createdAt, updatedAt ScannableTime
	err := row.Scan(&idStr, &m.CanonicalName, &m.NormalizedKey, &m.PlaidEntityID, &m.Domain, &m.LogoCDNURL, &locationJSON, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	m.ID = ScanUUID(idStr)
	if locationJSON.Valid && locationJSON.String != "" {
		var loc domain.MerchantLocation
		if err := json.Unmarshal([]byte(locationJSON.String), &loc); err == nil {
			m.Location = &loc
		}
	}
	if createdAt.Val != nil {
		m.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		m.UpdatedAt = *updatedAt.Val
	}
	return &m, nil
}
