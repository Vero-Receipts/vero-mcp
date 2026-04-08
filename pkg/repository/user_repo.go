package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type UserRepo struct {
	BaseRepo
}

func NewUserRepo(db *sql.DB, dialect Dialect) *UserRepo {
	return &UserRepo{NewBaseRepo(db, dialect)}
}

func (r *UserRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	row := r.SQ.Select("id", "name", "is_bank_connected", "created_at", "updated_at").
		From("users").
		Where(sq.Eq{"id": id.String()}).
		QueryRowContext(ctx)

	var u domain.User
	var idStr string
	var bankConn ScannableBool
	var createdAt, updatedAt ScannableTime

	err := row.Scan(&idStr, &u.Name, &bankConn, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}

	u.ID = ScanUUID(idStr)
	u.IsBankConnected = bankConn.Val
	if createdAt.Val != nil {
		u.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		u.UpdatedAt = *updatedAt.Val
	}
	return &u, nil
}

func (r *UserRepo) Upsert(ctx context.Context, user *domain.User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("users").
		Columns("id", "name", "is_bank_connected").
		Values(user.ID.String(), user.Name, user.IsBankConnected).
		Suffix(`ON CONFLICT (id) DO UPDATE SET
		  name = COALESCE(NULLIF(EXCLUDED.name, ''), users.name),
		  is_bank_connected = EXCLUDED.is_bank_connected,
		  updated_at = CURRENT_TIMESTAMP
		RETURNING created_at, updated_at`).
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
		user.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		user.UpdatedAt = *updatedAt.Val
	}
	return nil
}

func (r *UserRepo) SetBankConnected(ctx context.Context, userID uuid.UUID, connected bool) error {
	res, err := r.SQ.Update("users").
		Set("is_bank_connected", connected).
		Set("updated_at", sq.Expr("CURRENT_TIMESTAMP")).
		Where(sq.Eq{"id": userID.String()}).
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
