package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

func TestTransactionCacheRepo_FindByUserIDWithReceipts_Pagination(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Pager"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Five transactions on distinct, descending dates. Default sort is date DESC,
	// so the expected full order is txn_0 (newest) .. txn_4 (oldest).
	dates := []string{"2025-03-05", "2025-03-04", "2025-03-03", "2025-03-02", "2025-03-01"}
	var txns []domain.Transaction
	for i, d := range dates {
		txns = append(txns, domain.Transaction{
			TransactionID: "txn_" + string(rune('0'+i)),
			AccountID:     "acc_1",
			Amount:        float64(i + 1),
			Date:          d,
			Name:          "TXN",
			Category:      json.RawMessage(`["Food"]`),
		})
	}
	if _, err := repo.UpsertBatch(ctx, user.ID, txns); err != nil {
		t.Fatalf("upsert batch: %v", err)
	}

	// Page through with limit 2: offsets 0, 2, 4 should yield disjoint pages that
	// reconstruct the full date-DESC ordering with no overlap or gap.
	var walked []string
	for _, offset := range []int{0, 2, 4} {
		page, total, totalSpent, err := repo.FindByUserIDWithReceipts(ctx, user.ID, domain.TransactionFilter{Limit: 2, Offset: offset})
		if err != nil {
			t.Fatalf("page offset %d: %v", offset, err)
		}
		if total != 5 {
			t.Errorf("offset %d: expected total 5, got %d", offset, total)
		}
		// Amounts are 1..5, all positive → total spent is 15 regardless of page.
		if totalSpent != 15 {
			t.Errorf("offset %d: expected total spent 15, got %v", offset, totalSpent)
		}
		for _, p := range page {
			walked = append(walked, p.TransactionID)
		}
	}
	want := []string{"txn_0", "txn_1", "txn_2", "txn_3", "txn_4"}
	if len(walked) != len(want) {
		t.Fatalf("expected %d transactions across pages, got %d (%v)", len(want), len(walked), walked)
	}
	for i := range want {
		if walked[i] != want[i] {
			t.Errorf("page walk order mismatch at %d: got %s want %s (full: %v)", i, walked[i], want[i], walked)
		}
	}

	// Limit 0 (unpaginated) returns everything with total == len.
	all, total, totalSpent, err := repo.FindByUserIDWithReceipts(ctx, user.ID, domain.TransactionFilter{})
	if err != nil {
		t.Fatalf("unpaginated: %v", err)
	}
	if len(all) != 5 || total != 5 {
		t.Errorf("unpaginated: expected 5 rows/total 5, got %d rows total %d", len(all), total)
	}
	if totalSpent != 15 {
		t.Errorf("unpaginated: expected total spent 15, got %v", totalSpent)
	}

	// Offset past the end returns an empty page but the correct total.
	empty, total, _, err := repo.FindByUserIDWithReceipts(ctx, user.ID, domain.TransactionFilter{Limit: 2, Offset: 100})
	if err != nil {
		t.Fatalf("offset past end: %v", err)
	}
	if len(empty) != 0 || total != 5 {
		t.Errorf("offset past end: expected 0 rows/total 5, got %d rows total %d", len(empty), total)
	}
}

// A transaction with multiple matched receipts must count and paginate as a
// single transaction (transaction_id is not unique in receipt_matches, so the
// hydrate join multiplies rows; the count and page size must stay at the
// transaction grain).
func TestTransactionCacheRepo_FindByUserIDWithReceipts_MultiMatchDedup(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	matchRepo := NewReceiptMatchRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Multi"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := txRepo.UpsertBatch(ctx, user.ID, []domain.Transaction{
		{TransactionID: "txn_x", AccountID: "acc_1", Amount: 9.99, Date: "2025-03-10", Name: "TXN", Category: json.RawMessage(`["Food"]`)},
	}); err != nil {
		t.Fatalf("upsert tx: %v", err)
	}

	// Two distinct receipts both matched to the same transaction.
	merchant := "Shop"
	for i := 0; i < 2; i++ {
		r := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "matched", LineItems: json.RawMessage("[]")}
		if err := receiptRepo.Create(ctx, r); err != nil {
			t.Fatalf("create receipt %d: %v", i, err)
		}
		if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "txn_x", AccountID: "acc_1", ConfidenceScore: 0.9, MatchMethod: "auto"}); err != nil {
			t.Fatalf("create match %d: %v", i, err)
		}
	}

	page, total, _, err := txRepo.FindByUserIDWithReceipts(ctx, user.ID, domain.TransactionFilter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("paged: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total 1 distinct transaction, got %d", total)
	}
	if len(page) != 1 {
		t.Errorf("expected 1 transaction in page, got %d", len(page))
	}
}

func TestReceiptRepo_FindByUserIDWithMatches_Pagination(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewReceiptRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Pager"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Five receipts. Default sort (created_at DESC) plus the unique id
	// tie-breaker guarantees a stable, disjoint walk across pages.
	for i := 0; i < 5; i++ {
		merchant := "Shop"
		total := float64(i + 1)
		r := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Total: &total, Source: "upload", Status: "unmatched", LineItems: json.RawMessage("[]")}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("create receipt %d: %v", i, err)
		}
	}

	seen := map[string]bool{}
	collected := 0
	for _, offset := range []int{0, 2, 4} {
		f := domain.ReceiptFilter{Limit: 2, Offset: offset}
		page, total, err := repo.FindByUserIDWithMatches(ctx, user.ID, f)
		if err != nil {
			t.Fatalf("page offset %d: %v", offset, err)
		}
		if total != 5 {
			t.Errorf("offset %d: expected total 5, got %d", offset, total)
		}
		for _, p := range page {
			if seen[p.ID.String()] {
				t.Errorf("receipt %s appeared on more than one page", p.ID.String())
			}
			seen[p.ID.String()] = true
			collected++
		}
	}
	if collected != 5 {
		t.Errorf("expected 5 distinct receipts across pages, got %d", collected)
	}
}
