package repository

import (
	"context"
	"database/sql"
	"errors"
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

// Upsert resolves-or-creates a merchant by canonical name. On conflict, the
// existing domain and logo_cdn_url are preserved — so a raw Plaid sync
// never clobbers a resolved DO CDN URL, while first-seen merchants do get
// seeded with whatever logo URL Plaid provided as a stopgap.
func (r *MerchantRepo) Upsert(ctx context.Context, canonicalName string, websiteDomain *string, logoURL *string) (*domain.Merchant, error) {
	key := NormalizeMerchantKey(canonicalName)
	if key == "" {
		return nil, errors.New("merchant upsert: empty canonical name")
	}

	row := r.SQ.Insert("merchants").
		Columns("id", "canonical_name", "normalized_key", "domain", "logo_cdn_url").
		Values(uuid.New().String(), canonicalName, key, websiteDomain, logoURL).
		Suffix(`ON CONFLICT (normalized_key) DO UPDATE SET
			canonical_name = COALESCE(NULLIF(EXCLUDED.canonical_name, ''), merchants.canonical_name),
			domain         = COALESCE(merchants.domain, EXCLUDED.domain),
			logo_cdn_url   = COALESCE(merchants.logo_cdn_url, EXCLUDED.logo_cdn_url),
			updated_at     = CURRENT_TIMESTAMP
		RETURNING id, canonical_name, normalized_key, domain, logo_cdn_url, created_at, updated_at`).
		QueryRowContext(ctx)
	return scanMerchant(row)
}

func (r *MerchantRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Merchant, error) {
	row := r.SQ.Select("id", "canonical_name", "normalized_key", "domain", "logo_cdn_url", "created_at", "updated_at").
		From("merchants").
		Where(sq.Eq{"id": id.String()}).
		QueryRowContext(ctx)
	return scanMerchant(row)
}

func (r *MerchantRepo) FindByNormalizedKey(ctx context.Context, key string) (*domain.Merchant, error) {
	row := r.SQ.Select("id", "canonical_name", "normalized_key", "domain", "logo_cdn_url", "created_at", "updated_at").
		From("merchants").
		Where(sq.Eq{"normalized_key": key}).
		QueryRowContext(ctx)
	return scanMerchant(row)
}

// FindLogoJobCandidates returns merchants that need a logo resolved. A row
// qualifies when its logo_cdn_url is null OR its merchant_logo_jobs status is
// not 'ready' / 'failed'. Ordered by (never-attempted first, then oldest).
// FindLogoJobCandidates returns merchants that still need resolver work. A row
// qualifies when it has no merchant_logo_jobs entry yet (first-time seeding),
// or its job isn't in a terminal state (ready / failed). Ordered so
// never-attempted rows go first.
func (r *MerchantRepo) FindLogoJobCandidates(ctx context.Context, limit int) ([]MerchantLogoCandidate, error) {
	rows, err := r.SQ.Select("m.id", "m.canonical_name", "m.domain", "m.logo_cdn_url").
		From("merchants m").
		LeftJoin("merchant_logo_jobs j ON j.merchant_id = m.id").
		Where(sq.Or{
			sq.Eq{"j.merchant_id": nil},
			sq.And{
				sq.NotEq{"j.status": "ready"},
				sq.NotEq{"j.status": "failed"},
			},
		}).
		OrderBy("j.attempted_at ASC NULLS FIRST", "m.created_at ASC").
		Limit(uint64(limit)).
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []MerchantLogoCandidate
	for rows.Next() {
		var c MerchantLogoCandidate
		var idStr string
		if err := rows.Scan(&idStr, &c.CanonicalName, &c.WebsiteDomain, &c.ExistingLogoURL); err != nil {
			return nil, err
		}
		c.MerchantID = ScanUUID(idStr)
		out = append(out, c)
	}
	return out, rows.Err()
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

func (r *MerchantRepo) UpsertLogoJob(ctx context.Context, merchantID uuid.UUID, status, source string, attempts int, lastError *string) error {
	_, err := r.SQ.Insert("merchant_logo_jobs").
		Columns("merchant_id", "status", "source", "attempts", "last_error", "attempted_at", "updated_at").
		Values(merchantID.String(), status, source, attempts, lastError, sq.Expr("CURRENT_TIMESTAMP"), sq.Expr("CURRENT_TIMESTAMP")).
		Suffix(`ON CONFLICT (merchant_id) DO UPDATE SET
			status       = EXCLUDED.status,
			source       = EXCLUDED.source,
			attempts     = EXCLUDED.attempts,
			last_error   = EXCLUDED.last_error,
			attempted_at = EXCLUDED.attempted_at,
			updated_at   = EXCLUDED.updated_at`).
		ExecContext(ctx)
	return err
}

func scanMerchant(row scanner) (*domain.Merchant, error) {
	var m domain.Merchant
	var idStr string
	var createdAt, updatedAt ScannableTime
	err := row.Scan(&idStr, &m.CanonicalName, &m.NormalizedKey, &m.Domain, &m.LogoCDNURL, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	m.ID = ScanUUID(idStr)
	if createdAt.Val != nil {
		m.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		m.UpdatedAt = *updatedAt.Val
	}
	return &m, nil
}
