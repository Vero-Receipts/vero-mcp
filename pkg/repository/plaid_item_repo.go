package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type PlaidItemRepo struct {
	BaseRepo
}

func NewPlaidItemRepo(db *sql.DB, dialect Dialect) *PlaidItemRepo {
	return &PlaidItemRepo{NewBaseRepo(db, dialect)}
}

func (r *PlaidItemRepo) Create(ctx context.Context, item *domain.PlaidItem) error {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("plaid_items").
		Columns("id", "user_id", "item_id", "access_token", "sync_cursor").
		Values(item.ID.String(), item.UserID.String(), item.ItemID, item.AccessToken, item.SyncCursor).
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
		item.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		item.UpdatedAt = *updatedAt.Val
	}
	return nil
}

var plaidItemCols = []string{
	"id", "user_id", "item_id", "access_token", "sync_cursor", "created_at", "updated_at",
}

func (r *PlaidItemRepo) FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.PlaidItem, error) {
	rows, err := r.SQ.Select(plaidItemCols...).
		From("plaid_items").
		Where(sq.Eq{"user_id": userID.String()}).
		OrderBy("created_at").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.PlaidItem
	for rows.Next() {
		item, err := r.scanPlaidItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *PlaidItemRepo) FindByItemID(ctx context.Context, itemID string) (*domain.PlaidItem, error) {
	row := r.SQ.Select(plaidItemCols...).
		From("plaid_items").
		Where(sq.Eq{"item_id": itemID}).
		QueryRowContext(ctx)

	item, err := r.scanPlaidItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return item, nil
}

func (r *PlaidItemRepo) UpdateSyncCursor(ctx context.Context, id uuid.UUID, cursor string) error {
	res, err := r.SQ.Update("plaid_items").
		Set("sync_cursor", cursor).
		Set("updated_at", sq.Expr("CURRENT_TIMESTAMP")).
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

func (r *PlaidItemRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.SQ.Delete("plaid_items").
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

type rowScanner interface {
	Scan(dest ...any) error
}

func (r *PlaidItemRepo) scanPlaidItem(s rowScanner) (*domain.PlaidItem, error) {
	var item domain.PlaidItem
	var idStr, userIDStr string
	var syncCursor sql.NullString
	var createdAt, updatedAt ScannableTime

	err := s.Scan(&idStr, &userIDStr, &item.ItemID, &item.AccessToken,
		&syncCursor, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	item.ID = ScanUUID(idStr)
	item.UserID = ScanUUID(userIDStr)
	item.SyncCursor = syncCursor.String
	if createdAt.Val != nil {
		item.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		item.UpdatedAt = *updatedAt.Val
	}
	return &item, nil
}
