package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type CategoryCorrectionCacheRepo struct {
	BaseRepo
}

func NewCategoryCorrectionCacheRepo(db *sql.DB, dialect Dialect) *CategoryCorrectionCacheRepo {
	return &CategoryCorrectionCacheRepo{NewBaseRepo(db, dialect)}
}

func (r *CategoryCorrectionCacheRepo) FindByMerchantAndCategory(ctx context.Context, merchantCanonical, pfcDetailed string) (*domain.CategoryCorrectionCache, error) {
	row := r.SQ.Select("id", "merchant_canonical", "original_pfc_primary", "original_pfc_detailed",
		"corrected_pfc_primary", "corrected_pfc_detailed", "source", "sample_line_items", "created_at").
		From("category_corrections_cache").
		Where(sq.And{
			sq.Expr("LOWER(merchant_canonical) = LOWER(?)", merchantCanonical),
			sq.Eq{"original_pfc_detailed": pfcDetailed},
		}).
		Limit(1).
		QueryRowContext(ctx)

	var c domain.CategoryCorrectionCache
	var idStr string
	var sampleLineItems sql.NullString
	var createdAt ScannableTime

	err := row.Scan(&idStr, &c.MerchantCanonical, &c.OriginalPFCPrimary, &c.OriginalPFCDetailed,
		&c.CorrectedPFCPrimary, &c.CorrectedPFCDetailed, &c.Source, &sampleLineItems, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}

	c.ID = ScanUUID(idStr)
	c.SampleLineItems = NullString(sampleLineItems)
	if createdAt.Val != nil {
		c.CreatedAt = *createdAt.Val
	}
	return &c, nil
}

func (r *CategoryCorrectionCacheRepo) Create(ctx context.Context, c *domain.CategoryCorrectionCache) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("category_corrections_cache").
		Columns("id", "merchant_canonical", "original_pfc_primary", "original_pfc_detailed",
			"corrected_pfc_primary", "corrected_pfc_detailed", "source", "sample_line_items").
		Values(c.ID.String(), c.MerchantCanonical, c.OriginalPFCPrimary, c.OriginalPFCDetailed,
			c.CorrectedPFCPrimary, c.CorrectedPFCDetailed, c.Source, c.SampleLineItems).
		Suffix("ON CONFLICT (merchant_canonical, original_pfc_detailed) DO NOTHING RETURNING created_at").
		ToSql()
	if err != nil {
		return err
	}

	row := r.DB.QueryRowContext(ctx, query, args...)

	var createdAt ScannableTime
	err = row.Scan(&createdAt)
	if err != nil {
		// ON CONFLICT DO NOTHING returns no rows; treat as success
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if createdAt.Val != nil {
		c.CreatedAt = *createdAt.Val
	}
	return nil
}
