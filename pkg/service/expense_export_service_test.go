package service

import (
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// The rule that keeps a spreadsheet honest: an itemized purchase spans several
// rows, and its own amount belongs to exactly one of them. Repeating it would
// make SUM(Transaction Amount) count the purchase once per line item.
func TestExpenseRecordWritesPurchaseMoneyOnce(t *testing.T) {
	row := domain.ExpenseRow{
		TransactionID:   "txn_1",
		Date:            "2026-03-04",
		Merchant:        "Corner Store",
		Amount:          15,
		ReceiptTotal:    15,
		ReceiptSubtotal: 13.50,
		HasItem:         true,
		ItemDescription: "Coffee",
		Quantity:        1,
		UnitPrice:       5,
		LineTotal:       5,
	}

	amountAt := indexOf(t, "Transaction Amount")
	totalAt := indexOf(t, "Receipt Total")
	lineAt := indexOf(t, "Line Number")
	itemTotalAt := indexOf(t, "Line Total")

	first := expenseRecord(row, true, 1)
	if first[amountAt] != "15.00" {
		t.Errorf("first row amount = %q, want 15.00", first[amountAt])
	}
	if first[totalAt] != "15.00" {
		t.Errorf("first row receipt total = %q, want 15.00", first[totalAt])
	}

	row.ItemDescription = "Pastry"
	second := expenseRecord(row, false, 2)
	if second[amountAt] != "" {
		t.Errorf("second row amount = %q, want empty — the purchase is counted once", second[amountAt])
	}
	if second[totalAt] != "" {
		t.Errorf("second row receipt total = %q, want empty", second[totalAt])
	}
	// The line's own money stays on every row; it is what differs between them.
	if second[itemTotalAt] != "5.00" {
		t.Errorf("second row line total = %q, want 5.00", second[itemTotalAt])
	}
	if second[lineAt] != "2" {
		t.Errorf("second row line number = %q, want 2", second[lineAt])
	}
}

// A purchase nobody itemized still reports its own money, and leaves the line
// columns blank rather than inventing a line.
func TestExpenseRecordHandlesAnUnitemizedPurchase(t *testing.T) {
	record := expenseRecord(domain.ExpenseRow{
		TransactionID: "txn_2",
		Date:          "2026-03-05",
		Merchant:      "Taxi",
		Amount:        40,
		HasItem:       false,
	}, true, 0)

	if record[indexOf(t, "Transaction Amount")] != "40.00" {
		t.Error("an unitemized purchase should still report its amount")
	}
	for _, column := range []string{"Line Number", "Quantity", "Unit Price", "Line Total"} {
		if got := record[indexOf(t, column)]; got != "" {
			t.Errorf("%s = %q, want empty for an unitemized purchase", column, got)
		}
	}
	// A receipt that was never attached records no totals, and a blank says so
	// where 0.00 would claim the receipt said zero.
	if got := record[indexOf(t, "Receipt Total")]; got != "" {
		t.Errorf("Receipt Total = %q, want empty", got)
	}
}

func TestExpenseRecordMatchesTheHeader(t *testing.T) {
	record := expenseRecord(domain.ExpenseRow{TransactionID: "txn_3"}, true, 0)
	if len(record) != len(expenseColumns) {
		t.Errorf("row has %d fields, header has %d", len(record), len(expenseColumns))
	}
}

func TestMerchantLocationSkipsMissingParts(t *testing.T) {
	record := expenseRecord(domain.ExpenseRow{
		TransactionID: "txn_4",
		MerchantCity:  "Denver",
		MerchantState: "CO",
	}, true, 0)

	if got := record[indexOf(t, "Merchant Location")]; got != "Denver, CO" {
		t.Errorf("location = %q, want %q", got, "Denver, CO")
	}
}

func indexOf(t *testing.T, column string) int {
	t.Helper()
	for i, name := range expenseColumns {
		if name == column {
			return i
		}
	}
	t.Fatalf("no %q column", column)
	return -1
}
