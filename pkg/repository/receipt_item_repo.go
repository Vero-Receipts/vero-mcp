package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type ReceiptItemRepo struct {
	BaseRepo
}

func NewReceiptItemRepo(db *sql.DB, dialect Dialect) *ReceiptItemRepo {
	return &ReceiptItemRepo{NewBaseRepo(db, dialect)}
}

var receiptItemCols = []string{
	"id", "receipt_id", "user_id", "description", "quantity", "unit_price", "price", "sort_order", "created_at", "updated_at",
}

func (r *ReceiptItemRepo) FindByReceiptID(ctx context.Context, receiptID uuid.UUID) ([]domain.ReceiptItem, error) {
	rows, err := r.SQ.Select(receiptItemCols...).
		From("receipt_items").
		Where(sq.Eq{"receipt_id": receiptID.String()}).
		OrderBy("sort_order ASC", "created_at ASC").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []domain.ReceiptItem
	for rows.Next() {
		item, err := r.scanReceiptItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

func (r *ReceiptItemRepo) FindByID(ctx context.Context, id uuid.UUID) (*domain.ReceiptItem, error) {
	row := r.SQ.Select(receiptItemCols...).
		From("receipt_items").
		Where(sq.Eq{"id": id.String()}).
		QueryRowContext(ctx)

	item, err := r.scanReceiptItem(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return item, nil
}

func (r *ReceiptItemRepo) Create(ctx context.Context, item *domain.ReceiptItem) error {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}

	query, args, err := r.SQ.Insert("receipt_items").
		Columns("id", "receipt_id", "user_id", "description", "quantity", "unit_price", "price", "sort_order").
		Values(item.ID.String(), item.ReceiptID.String(), item.UserID.String(),
			item.Description, item.Quantity, item.UnitPrice, item.Price, item.SortOrder).
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

func (r *ReceiptItemRepo) Update(ctx context.Context, item *domain.ReceiptItem) error {
	res, err := r.SQ.Update("receipt_items").
		Set("description", item.Description).
		Set("quantity", item.Quantity).
		Set("unit_price", item.UnitPrice).
		Set("price", item.Price).
		Set("sort_order", item.SortOrder).
		Set("updated_at", sq.Expr("CURRENT_TIMESTAMP")).
		Where(sq.Eq{"id": item.ID.String()}).
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

func (r *ReceiptItemRepo) Delete(ctx context.Context, id uuid.UUID) error {
	res, err := r.SQ.Delete("receipt_items").
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

func (r *ReceiptItemRepo) DeleteByReceiptID(ctx context.Context, receiptID uuid.UUID) error {
	_, err := r.SQ.Delete("receipt_items").
		Where(sq.Eq{"receipt_id": receiptID.String()}).
		ExecContext(ctx)
	return err
}

func (r *ReceiptItemRepo) scanReceiptItem(s rowScanner) (*domain.ReceiptItem, error) {
	var item domain.ReceiptItem
	var idStr, receiptIDStr, userIDStr string
	var createdAt, updatedAt ScannableTime

	err := s.Scan(&idStr, &receiptIDStr, &userIDStr,
		&item.Description, &item.Quantity, &item.UnitPrice, &item.Price, &item.SortOrder,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	item.ID = ScanUUID(idStr)
	item.ReceiptID = ScanUUID(receiptIDStr)
	item.UserID = ScanUUID(userIDStr)
	if createdAt.Val != nil {
		item.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		item.UpdatedAt = *updatedAt.Val
	}
	return &item, nil
}
