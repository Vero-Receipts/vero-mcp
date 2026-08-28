package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/google/uuid"
)

func seedReceipt(t *testing.T, db *sql.DB, userID uuid.UUID, txnID string, total, subtotal, tax float64, items []string) uuid.UUID {
	t.Helper()

	receiptID := uuid.New()
	if _, err := db.Exec(
		`INSERT INTO receipts (id, user_id, merchant_name, total, subtotal, tax, date, source, status)
		 VALUES (?, ?, 'Test Merchant', ?, ?, ?, '2026-03-04', 'email', 'processed')`,
		receiptID.String(), userID.String(), total, subtotal, tax,
	); err != nil {
		t.Fatalf("seed receipt: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO receipt_matches (id, receipt_id, transaction_id, match_method)
		 VALUES (?, ?, ?, 'auto')`,
		uuid.New().String(), receiptID.String(), txnID,
	); err != nil {
		t.Fatalf("seed match: %v", err)
	}

	for i, description := range items {
		if _, err := db.Exec(
			`INSERT INTO receipt_items (id, receipt_id, user_id, description, quantity, unit_price, price, sort_order)
			 VALUES (?, ?, ?, ?, 1, 5.00, 5.00, ?)`,
			uuid.New().String(), receiptID.String(), userID.String(), description, i,
		); err != nil {
			t.Fatalf("seed item: %v", err)
		}
	}
	return receiptID
}

// seedTxnWithID is seedTxn with a caller-chosen transaction id, so a receipt
// can be matched to it.
func seedTxnWithID(t *testing.T, db *sql.DB, userID uuid.UUID, txnID string, s txnSeed) {
	t.Helper()

	var primary any
	if s.primary != "" {
		primary = s.primary
	}
	name := s.name
	if name == "" {
		name = "Test Transaction"
	}
	if _, err := db.Exec(
		`INSERT INTO transaction_cache
		   (id, user_id, transaction_id, account_id, amount, date, name, category, pfc_primary, pending)
		 VALUES (?, ?, ?, 'acc_1', ?, ?, ?, '[]', ?, 0)`,
		uuid.New().String(), userID.String(), txnID, s.amount, s.date, name, primary,
	); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
}

// The export is one row per line item, so an itemized purchase spans several
// rows. The money that belongs to the purchase must appear on exactly one of
// them, or summing the amount column counts that purchase once per item.
func TestExpenseRowsDoNotMultiplyAPurchase(t *testing.T) {
	db, _, filter := reportFixture(t)
	repo := NewExpenseExportRepo(db, DialectSQLite)

	seedTxnWithID(t, db, filter.UserID, "txn_itemized",
		txnSeed{amount: 15, date: "2026-03-04", primary: "FOOD_AND_DRINK", name: "Corner Store"})
	seedReceipt(t, db, filter.UserID, "txn_itemized", 15, 13.50, 1.50,
		[]string{"Coffee", "Pastry", "Juice"})

	// A purchase nobody itemized still has to appear, exactly once.
	seedTxnWithID(t, db, filter.UserID, "txn_bare",
		txnSeed{amount: 40, date: "2026-03-05", primary: "TRAVEL", name: "Taxi"})

	cursor, err := repo.ExpenseRows(context.Background(), filter)
	if err != nil {
		t.Fatalf("ExpenseRows: %v", err)
	}
	defer cursor.Close()

	rowsPerTxn := map[string]int{}
	itemsPerTxn := map[string]int{}
	for cursor.Next() {
		row := cursor.Row()
		rowsPerTxn[row.TransactionID]++
		if row.HasItem {
			itemsPerTxn[row.TransactionID]++
		}
		// The amount is carried on every row; the writer is what emits it once.
		// What must never happen is the join inventing rows.
		if row.TransactionID == "txn_itemized" && row.Amount != 15 {
			t.Errorf("itemized row amount = %v, want 15", row.Amount)
		}
	}
	if err := cursor.Err(); err != nil {
		t.Fatalf("cursor: %v", err)
	}

	if rowsPerTxn["txn_itemized"] != 3 {
		t.Errorf("itemized purchase produced %d rows, want 3 (one per item)", rowsPerTxn["txn_itemized"])
	}
	if rowsPerTxn["txn_bare"] != 1 {
		t.Errorf("unitemized purchase produced %d rows, want 1", rowsPerTxn["txn_bare"])
	}
	if itemsPerTxn["txn_bare"] != 0 {
		t.Errorf("unitemized purchase reported %d items, want 0", itemsPerTxn["txn_bare"])
	}
}

// Refunds stay in: a record of spending that silently drops the money that came
// back is a worse record than one showing both. Money movement still goes.
func TestExpenseRowsKeepRefundsAndDropTransfers(t *testing.T) {
	db, _, filter := reportFixture(t)
	repo := NewExpenseExportRepo(db, DialectSQLite)

	seedTxnWithID(t, db, filter.UserID, "txn_spend", txnSeed{amount: 50, date: "2026-03-04", primary: "GENERAL_MERCHANDISE"})
	seedTxnWithID(t, db, filter.UserID, "txn_refund", txnSeed{amount: -20, date: "2026-03-05", primary: "GENERAL_MERCHANDISE"})
	seedTxnWithID(t, db, filter.UserID, "txn_transfer", txnSeed{amount: 500, date: "2026-03-06", primary: "TRANSFER_OUT"})
	seedTxnWithID(t, db, filter.UserID, "txn_income", txnSeed{amount: -900, date: "2026-03-07", primary: "INCOME"})

	cursor, err := repo.ExpenseRows(context.Background(), filter)
	if err != nil {
		t.Fatalf("ExpenseRows: %v", err)
	}
	defer cursor.Close()

	seen := map[string]bool{}
	for cursor.Next() {
		seen[cursor.Row().TransactionID] = true
	}
	if err := cursor.Err(); err != nil {
		t.Fatalf("cursor: %v", err)
	}

	if !seen["txn_spend"] || !seen["txn_refund"] {
		t.Errorf("expected the purchase and its refund, got %v", seen)
	}
	if seen["txn_transfer"] || seen["txn_income"] {
		t.Errorf("money movement leaked into the export: %v", seen)
	}
}
