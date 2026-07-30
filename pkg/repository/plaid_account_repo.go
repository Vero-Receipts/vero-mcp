package repository

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type PlaidAccountRepo struct {
	BaseRepo
	dialect Dialect
}

func NewPlaidAccountRepo(db *sql.DB, dialect Dialect) *PlaidAccountRepo {
	return &PlaidAccountRepo{
		BaseRepo: NewBaseRepo(db, dialect),
		dialect:  dialect,
	}
}

// Upsert inserts or updates an account keyed by account_id. Plaid account ids
// are stable per Item, so account_id is the natural conflict key.
func (r *PlaidAccountRepo) Upsert(ctx context.Context, a *domain.PlaidAccount) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}

	rawSQL := `INSERT INTO plaid_accounts
		(id, account_id, item_id, user_id, mask, name, official_name, subtype, type)
	 VALUES (?,?,?,?,?,?,?,?,?)
	 ON CONFLICT (account_id) DO UPDATE SET
	   item_id       = EXCLUDED.item_id,
	   user_id       = EXCLUDED.user_id,
	   mask          = EXCLUDED.mask,
	   name          = EXCLUDED.name,
	   official_name = EXCLUDED.official_name,
	   subtype       = EXCLUDED.subtype,
	   type          = EXCLUDED.type,
	   updated_at    = CURRENT_TIMESTAMP`

	if r.dialect == DialectPostgres {
		rawSQL, _ = sq.Dollar.ReplacePlaceholders(rawSQL)
	}

	_, err := r.DB.ExecContext(ctx, rawSQL,
		a.ID.String(), a.AccountID, a.ItemID, a.UserID.String(),
		a.Mask, a.Name, a.OfficialName, a.Subtype, a.Type)
	return err
}

var plaidAccountCols = []string{
	"id", "account_id", "item_id", "user_id", "mask", "name",
	"official_name", "subtype", "type", "created_at", "updated_at",
}

func (r *PlaidAccountRepo) FindByAccountID(ctx context.Context, accountID string) (*domain.PlaidAccount, error) {
	row := r.SQ.Select(plaidAccountCols...).
		From("plaid_accounts").
		Where(sq.Eq{"account_id": accountID}).
		QueryRowContext(ctx)

	a, err := r.scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return a, nil
}

func (r *PlaidAccountRepo) FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.PlaidAccount, error) {
	rows, err := r.SQ.Select(plaidAccountCols...).
		From("plaid_accounts").
		Where(sq.Eq{"user_id": userID.String()}).
		OrderBy("created_at").
		QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []domain.PlaidAccount
	for rows.Next() {
		a, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, *a)
	}
	return accounts, rows.Err()
}

func (r *PlaidAccountRepo) scan(s rowScanner) (*domain.PlaidAccount, error) {
	var a domain.PlaidAccount
	var idStr, userIDStr string
	var mask, name, officialName, subtype, accType sql.NullString
	var createdAt, updatedAt ScannableTime

	err := s.Scan(&idStr, &a.AccountID, &a.ItemID, &userIDStr, &mask, &name,
		&officialName, &subtype, &accType, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}

	a.ID = ScanUUID(idStr)
	a.UserID = ScanUUID(userIDStr)
	a.Mask = mask.String
	a.Name = name.String
	a.OfficialName = officialName.String
	a.Subtype = subtype.String
	a.Type = accType.String
	if createdAt.Val != nil {
		a.CreatedAt = *createdAt.Val
	}
	if updatedAt.Val != nil {
		a.UpdatedAt = *updatedAt.Val
	}
	return &a, nil
}
