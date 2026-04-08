package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type LabelAssignmentRepo struct {
	BaseRepo
}

func NewLabelAssignmentRepo(db *sql.DB, dialect Dialect) *LabelAssignmentRepo {
	return &LabelAssignmentRepo{NewBaseRepo(db, dialect)}
}

func (r *LabelAssignmentRepo) Assign(ctx context.Context, assignment *domain.LabelAssignment) error {
	if assignment.ID == uuid.Nil {
		assignment.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("label_assignments").
		Columns("id", "label_id", "user_id", "entity_type", "entity_id").
		Values(assignment.ID.String(), assignment.LabelID.String(), assignment.UserID.String(),
			assignment.EntityType, assignment.EntityID).
		Suffix("ON CONFLICT (label_id, entity_type, entity_id) DO NOTHING RETURNING created_at").
		ToSql()
	if err != nil {
		return err
	}

	row := r.DB.QueryRowContext(ctx, query, args...)

	var createdAt ScannableTime
	err = row.Scan(&createdAt)
	if err != nil {
		// ON CONFLICT DO NOTHING returns no rows when the row already exists
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrConflict
		}
		return err
	}
	if createdAt.Val != nil {
		assignment.CreatedAt = *createdAt.Val
	}
	return nil
}

func (r *LabelAssignmentRepo) Unassign(ctx context.Context, labelID uuid.UUID, entityType, entityID string) error {
	res, err := r.SQ.Delete("label_assignments").
		Where(sq.Eq{
			"label_id":    labelID.String(),
			"entity_type": entityType,
			"entity_id":   entityID,
		}).
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

func (r *LabelAssignmentRepo) FindByEntity(ctx context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Label, error) {
	query, args, err := r.SQ.Select("l.id", "l.user_id", "l.name", "l.color", "l.created_at", "l.updated_at").
		From("labels l").
		Join("label_assignments la ON l.id = la.label_id").
		Where(sq.Eq{
			"la.user_id":     userID.String(),
			"la.entity_type": entityType,
			"la.entity_id":   entityID,
		}).
		OrderBy("l.name ASC").
		ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labels []domain.Label
	for rows.Next() {
		var label domain.Label
		var idStr, userIDStr string
		var createdAt, updatedAt ScannableTime

		if err := rows.Scan(&idStr, &userIDStr, &label.Name, &label.Color, &createdAt, &updatedAt); err != nil {
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
		labels = append(labels, label)
	}
	return labels, rows.Err()
}
