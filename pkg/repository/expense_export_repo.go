package repository

import (
	"context"
	"database/sql"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// ExpenseExportRepo reads a user's expenses at the grain a spreadsheet wants:
// one row per receipt line item, and one row for a purchase with no itemization.
//
// The clients used to assemble this themselves — fetch every transaction, then
// one or two more requests per transaction for its receipt and that receipt's
// items. A year of history was hundreds of requests for a single file. It is
// one query here, streamed, so nothing has to be held in memory at either end.
type ExpenseExportRepo struct {
	BaseRepo
	dialect Dialect
}

func NewExpenseExportRepo(db *sql.DB, dialect Dialect) *ExpenseExportRepo {
	return &ExpenseExportRepo{
		BaseRepo: NewBaseRepo(db, dialect),
		dialect:  dialect,
	}
}

// ExpenseRows streams the export.
//
// The caller must drain or close the returned rows. Errors that surface mid-read
// are reported by ExpenseRowCursor.Err after iteration stops.
//
// Note what is joined and what is not. `receipt_matches` holds settled links
// only and is unique per transaction, so joining it cannot multiply a row —
// unlike the suggestion table, which is deliberately left out: a guess nobody
// confirmed is not part of someone's expense record.
func (r *ExpenseExportRepo) ExpenseRows(ctx context.Context, f domain.ReportFilter) (*ExpenseRowCursor, error) {
	primary := (&TransactionReportRepo{dialect: r.dialect}).primaryExpr()

	qb := r.SQ.Select(
		"t.date",
		"COALESCE(NULLIF(m.canonical_name, ''), t.name)",
		primary,
		"COALESCE(t.pfc_detailed, '')",
		"t.pending",
		"t.recurring",
		"t.amount",
		"COALESCE(t.payment_channel, '')",
		"t.transaction_id",
		"COALESCE(rm.match_method, '')",
		"COALESCE(r.subtotal, 0)",
		"COALESCE(r.tax, 0)",
		"COALESCE(r.tip, 0)",
		"COALESCE(r.total, 0)",
		"COALESCE(r.source, '')",
		"COALESCE(r.payment_method, '')",
		"COALESCE(r.merchant_address, '')",
		"COALESCE(r.merchant_city, '')",
		"COALESCE(r.merchant_state, '')",
		"COALESCE(r.transaction_time, '')",
		"COALESCE(r.order_id, '')",
		"COALESCE(ri.sort_order, 0)",
		"COALESCE(ri.description, '')",
		"COALESCE(ri.quantity, 0)",
		"COALESCE(ri.unit_price, 0)",
		"COALESCE(ri.price, 0)",
		"CASE WHEN ri.id IS NULL THEN 0 ELSE 1 END",
	).
		From("transaction_cache t").
		LeftJoin("merchants m ON m.id = t.merchant_id").
		LeftJoin("receipt_matches rm ON rm.transaction_id = t.transaction_id").
		LeftJoin("receipts r ON r.id = rm.receipt_id").
		LeftJoin("receipt_items ri ON ri.receipt_id = r.id").
		Where(sq.Eq{"t.user_id": f.UserID.String()}).
		// Refunds are kept: a spreadsheet of what was spent that silently drops
		// the money that came back is a worse record than one that shows both.
		// Money movement is still excluded, as it is everywhere else.
		Where(fmt.Sprintf("(%s) NOT IN (%s)", primary, placeholderList(len(domain.ExcludedPrimaries))),
			toAnySlice(domain.ExcludedPrimaries)...)

	if f.From != "" {
		qb = qb.Where(sq.GtOrEq{"t.date": f.From})
	}
	if f.To != "" {
		qb = qb.Where(sq.LtOrEq{"t.date": f.To})
	}

	// Newest first, matching every list in the product; then by transaction so
	// an itemized purchase's rows stay together, then in the order the items
	// were read off the receipt.
	qb = qb.OrderBy("t.date DESC", "t.transaction_id", "ri.sort_order ASC")

	rows, err := qb.RunWith(r.DB).QueryContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("expense rows: %w", err)
	}
	return &ExpenseRowCursor{rows: rows}, nil
}

// ExpenseRowCursor walks the export one row at a time, so a long history is
// never assembled in memory.
type ExpenseRowCursor struct {
	rows *sql.Rows
	row  domain.ExpenseRow
	err  error
}

// Next advances to the next row, returning false at the end or on error.
func (c *ExpenseRowCursor) Next() bool {
	if c.err != nil || !c.rows.Next() {
		return false
	}

	var hasItem int
	if err := c.rows.Scan(
		&c.row.Date,
		&c.row.Merchant,
		&c.row.Category,
		&c.row.Subcategory,
		&c.row.Pending,
		&c.row.Recurring,
		&c.row.Amount,
		&c.row.PaymentChannel,
		&c.row.TransactionID,
		&c.row.MatchMethod,
		&c.row.ReceiptSubtotal,
		&c.row.ReceiptTax,
		&c.row.ReceiptTip,
		&c.row.ReceiptTotal,
		&c.row.ReceiptSource,
		&c.row.PaymentMethod,
		&c.row.MerchantAddress,
		&c.row.MerchantCity,
		&c.row.MerchantState,
		&c.row.PurchaseTime,
		&c.row.OrderNumber,
		&c.row.LineNumber,
		&c.row.ItemDescription,
		&c.row.Quantity,
		&c.row.UnitPrice,
		&c.row.LineTotal,
		&hasItem,
	); err != nil {
		c.err = fmt.Errorf("scan expense row: %w", err)
		return false
	}

	c.row.HasItem = hasItem == 1
	return true
}

// Row is the row Next just read.
func (c *ExpenseRowCursor) Row() domain.ExpenseRow { return c.row }

// Err reports why iteration stopped, if it was not the end of the results.
func (c *ExpenseRowCursor) Err() error {
	if c.err != nil {
		return c.err
	}
	return c.rows.Err()
}

// Close releases the underlying result set.
func (c *ExpenseRowCursor) Close() error { return c.rows.Close() }
