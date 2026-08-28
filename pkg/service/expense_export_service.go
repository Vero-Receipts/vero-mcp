package service

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

// ExpenseExportService writes a user's expenses as CSV, one row per receipt
// line item.
//
// Streamed rather than assembled: a long history is written out as it is read,
// so neither this process nor the caller holds the whole file.
type ExpenseExportService struct {
	repo repository.ExpenseExportRepository
}

func NewExpenseExportService(repo repository.ExpenseExportRepository) *ExpenseExportService {
	return &ExpenseExportService{repo: repo}
}

// expenseColumns is the header, and the order every row follows.
//
// Only columns that carry data. The clients' own export had eight more that
// were always blank — item name and SKU, receipt discount, invoice and
// confirmation numbers, the institution — because nothing ever captured them.
// Emitting them implied Vero held information it did not.
var expenseColumns = []string{
	"Date",
	"Merchant",
	"Category",
	"Subcategory",
	"Status",
	"Recurring",
	"Transaction Amount",
	"Payment Channel",
	"Line Number",
	"Item Description",
	"Quantity",
	"Unit Price",
	"Line Total",
	"Receipt Subtotal",
	"Receipt Tax",
	"Receipt Tip",
	"Receipt Total",
	"Receipt Source",
	"Payment Method",
	"Merchant Location",
	"Purchase Time",
	"Order Number",
	"Receipt Match",
	"Transaction ID",
}

// WriteCSV streams the export for one user and window.
//
// Errors surfacing after the first row has been written cannot be turned into
// an HTTP status — the caller has already committed to a response — so the
// caller should treat a returned error as a truncated file and log it.
func (s *ExpenseExportService) WriteCSV(ctx context.Context, w io.Writer, filter domain.ReportFilter) error {
	cursor, err := s.repo.ExpenseRows(ctx, filter)
	if err != nil {
		return err
	}
	defer cursor.Close()

	out := csv.NewWriter(w)
	if err := out.Write(expenseColumns); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	// The transaction whose money columns were already written. Repeating them
	// on every item of one purchase would make a spreadsheet's sum count an
	// itemized purchase once per line.
	var written string
	// Line numbering restarts per purchase and counts the items actually
	// emitted, so it reads 1, 2, 3 whatever sort_order held.
	line := 0

	for cursor.Next() {
		row := cursor.Row()
		first := row.TransactionID != written
		if first {
			written = row.TransactionID
			line = 0
		}
		if row.HasItem {
			line++
		}

		if err := out.Write(expenseRecord(row, first, line)); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
		// Flush periodically so a slow reader sees progress and the buffer
		// stays small, rather than the whole file landing at the end.
		out.Flush()
		if err := out.Error(); err != nil {
			return fmt.Errorf("flush: %w", err)
		}
	}

	if err := cursor.Err(); err != nil {
		return err
	}

	out.Flush()
	return out.Error()
}

func expenseRecord(row domain.ExpenseRow, first bool, line int) []string {
	status := "Posted"
	if row.Pending {
		status = "Pending"
	}

	// Money that belongs to the purchase rather than to the line, written once.
	amount, subtotal, tax, tip, total := "", "", "", "", ""
	source, method, location, purchased, order, match := "", "", "", "", "", ""
	if first {
		amount = money(row.Amount)
		subtotal = moneyOrBlank(row.ReceiptSubtotal)
		tax = moneyOrBlank(row.ReceiptTax)
		tip = moneyOrBlank(row.ReceiptTip)
		total = moneyOrBlank(row.ReceiptTotal)
		source = row.ReceiptSource
		method = row.PaymentMethod
		location = joinNonEmpty(", ", row.MerchantAddress, row.MerchantCity, row.MerchantState)
		purchased = row.PurchaseTime
		order = row.OrderNumber
		match = row.MatchMethod
	}

	lineNumber, quantity, unitPrice, lineTotal := "", "", "", ""
	if row.HasItem {
		lineNumber = strconv.Itoa(line)
		quantity = trimFloat(row.Quantity)
		unitPrice = moneyOrBlank(row.UnitPrice)
		lineTotal = moneyOrBlank(row.LineTotal)
	}

	return []string{
		row.Date,
		row.Merchant,
		row.Category,
		row.Subcategory,
		status,
		boolText(row.Recurring),
		amount,
		row.PaymentChannel,
		lineNumber,
		row.ItemDescription,
		quantity,
		unitPrice,
		lineTotal,
		subtotal,
		tax,
		tip,
		total,
		source,
		method,
		location,
		purchased,
		order,
		match,
		row.TransactionID,
	}
}

func money(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

// moneyOrBlank leaves a zero empty. A receipt that recorded no tip and a
// receipt that was never itemized both hold zero, and a blank cell says "not
// recorded" where 0.00 would claim it was.
func moneyOrBlank(v float64) string {
	if v == 0 {
		return ""
	}
	return money(v)
}

// trimFloat drops a trailing ".000" so a quantity of one reads as 1.
func trimFloat(v float64) string {
	if v == 0 {
		return ""
	}
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return s
}

func boolText(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}

// ExpenseExportFilename names the download, in the same shape the clients used.
func ExpenseExportFilename(from, to string) string {
	if from == "" {
		from = "all"
	}
	if to == "" {
		to = "today"
	}
	return fmt.Sprintf("vero-expenses-%s-to-%s.csv", from, to)
}

// ExpenseExportFilter builds the window for an export.
func ExpenseExportFilter(userID uuid.UUID, from, to string) domain.ReportFilter {
	return domain.ReportFilter{UserID: userID, From: from, To: to}
}
