package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type MerchantAliasRepo struct {
	BaseRepo
}

func NewMerchantAliasRepo(db *sql.DB, dialect Dialect) *MerchantAliasRepo {
	return &MerchantAliasRepo{NewBaseRepo(db, dialect)}
}

func (r *MerchantAliasRepo) FindCanonical(ctx context.Context, alias string) (string, error) {
	var canonical string
	err := r.SQ.Select("canonical").
		From("merchant_aliases").
		Where("LOWER(alias) = LOWER(?)", alias).
		Limit(1).
		QueryRowContext(ctx).
		Scan(&canonical)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		return "", err
	}
	return canonical, nil
}

func (r *MerchantAliasRepo) AreSameMerchant(ctx context.Context, name1, name2 string) (bool, error) {
	if strings.EqualFold(name1, name2) {
		return true, nil
	}

	var c1, c2 string

	err := r.SQ.Select("canonical").
		From("merchant_aliases").
		Where("LOWER(alias) = LOWER(?)", name1).
		Limit(1).
		QueryRowContext(ctx).
		Scan(&c1)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	err = r.SQ.Select("canonical").
		From("merchant_aliases").
		Where("LOWER(alias) = LOWER(?)", name2).
		Limit(1).
		QueryRowContext(ctx).
		Scan(&c2)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	if c1 != "" && c2 != "" && strings.EqualFold(c1, c2) {
		return true, nil
	}
	if c1 != "" && strings.EqualFold(c1, name2) {
		return true, nil
	}
	if c2 != "" && strings.EqualFold(c2, name1) {
		return true, nil
	}

	return false, nil
}

func (r *MerchantAliasRepo) Create(ctx context.Context, a *domain.MerchantAlias) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("merchant_aliases").
		Columns("id", "canonical", "alias", "source").
		Values(a.ID.String(), a.Canonical, a.Alias, a.Source).
		Suffix("ON CONFLICT (canonical, alias) DO NOTHING RETURNING created_at").
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
		a.CreatedAt = *createdAt.Val
	}
	return nil
}
