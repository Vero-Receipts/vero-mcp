package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type LabelRepo struct {
	BaseRepo
}

func NewLabelRepo(db *sql.DB, dialect Dialect) *LabelRepo {
	return &LabelRepo{NewBaseRepo(db, dialect)}
}

func (r *LabelRepo) Create(ctx context.Context, label *domain.Label) error {
	if label.ID == uuid.Nil {
		label.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("labels").
		Columns("id", "user_id", "name", "color").
		Values(label.ID.String(), label.UserID.String(), label.Name, label.Color).
		Suffix("RETURNING created_at, updated_at").
		ToSql()
	if err != nil {
		return err
	}

	row := r.DB.QueryRowContext(ctx, query, args...)

	var createdAt, updatedAt ScannableTime
	if err := row.Scan(&createdAt, &updatedAt); err != nil {
		return err
	}
	if createdAt.Val != nil {
		label.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		label.UpdatedAt = *updatedAt.Val
	}
	return nil
}

var labelCols = []string{
	"id", "user_id", "name", "color", "created_at", "updated_at",
}

func (r *LabelRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Label, error) {
	row := r.SQ.Select(labelCols...).
		From("labels").
		Where(sq.Eq{"id": id.String()}).
		QueryRowContext(ctx)

	label, err := r.scanLabel(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return label, nil
}

func (r *LabelRepo) FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Label, error) {
	rows, err := r.SQ.Select(labelCols...).
		From("labels").
		Where(sq.Eq{"user_id": userID.String()}).
		OrderBy("name ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labels []domain.Label
	for rows.Next() {
		label, err := r.scanLabel(rows)
		if err != nil {
			return nil, err
		}
		labels = append(labels, *label)
	}
	return labels, rows.Err()
}

func (r *LabelRepo) Update(ctx context.Context, label *domain.Label) error {
	res, err := r.SQ.Update("labels").
		Set("name", label.Name).
		Set("color", label.Color).
		Set("updated_at", sq.Expr("CURRENT_TIMESTAMP")).
		Where(sq.Eq{"id": label.ID.String()}).
		ExecContext(ctx)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *LabelRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.SQ.Delete("labels").
		Where(sq.Eq{"id": id.String()}).
		ExecContext(ctx)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *LabelRepo) scanLabel(s rowScanner) (*domain.Label, error) {
	var label domain.Label
	var idStr, userIDStr string
	var createdAt, updatedAt ScannableTime

	err := s.Scan(&idStr, &userIDStr, &label.Name, &label.Color, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	label.ID = ScanUUID(idStr)
	label.UserID = ScanUUID(userIDStr)
	if createdAt.Val != nil {
		label.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		label.UpdatedAt = *updatedAt.Val
	}
	return &label, nil
}
