package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type NoteRepo struct {
	BaseRepo
}

func NewNoteRepo(db *sql.DB, dialect Dialect) *NoteRepo {
	return &NoteRepo{NewBaseRepo(db, dialect)}
}

func (r *NoteRepo) Create(ctx context.Context, note *domain.Note) error {
	if note.ID == uuid.Nil {
		note.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("notes").
		Columns("id", "user_id", "entity_type", "entity_id", "content").
		Values(note.ID.String(), note.UserID.String(), note.EntityType, note.EntityID, note.Content).
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
		note.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		note.UpdatedAt = *updatedAt.Val
	}
	return nil
}

var noteCols = []string{
	"id", "user_id", "entity_type", "entity_id", "content", "created_at", "updated_at",
}

func (r *NoteRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.Note, error) {
	row := r.SQ.Select(noteCols...).
		From("notes").
		Where(sq.Eq{"id": id.String()}).
		QueryRowContext(ctx)

	note, err := r.scanNote(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return note, nil
}

func (r *NoteRepo) FindByEntity(ctx context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Note, error) {
	rows, err := r.SQ.Select(noteCols...).
		From("notes").
		Where(sq.Eq{
			"user_id":     userID.String(),
			"entity_type": entityType,
			"entity_id":   entityID,
		}).
		OrderBy("created_at DESC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []domain.Note
	for rows.Next() {
		note, err := r.scanNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, *note)
	}
	return notes, rows.Err()
}

func (r *NoteRepo) Update(ctx context.Context, note *domain.Note) error {
	res, err := r.SQ.Update("notes").
		Set("content", note.Content).
		Set("updated_at", sq.Expr("CURRENT_TIMESTAMP")).
		Where(sq.Eq{"id": note.ID.String()}).
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

func (r *NoteRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.SQ.Delete("notes").
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

func (r *NoteRepo) scanNote(s rowScanner) (*domain.Note, error) {
	var note domain.Note
	var idStr, userIDStr string
	var createdAt, updatedAt ScannableTime

	err := s.Scan(&idStr, &userIDStr, &note.EntityType, &note.EntityID, &note.Content,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	note.ID = ScanUUID(idStr)
	note.UserID = ScanUUID(userIDStr)
	if createdAt.Val != nil {
		note.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		note.UpdatedAt = *updatedAt.Val
	}
	return &note, nil
}
