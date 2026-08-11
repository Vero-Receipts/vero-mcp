package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// TransactionID pins the query to one row regardless of where that row falls in
// the default (date DESC) ordering. This is what lets a detail view fetch a
// single transaction instead of paging the list until it appears.
func TestTransactionCacheRepo_FindByUserIDWithReceipts_TransactionIDFilter(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "ByID"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	other := &domain.User{Name: "Other"}
	if err := userRepo.Upsert(ctx, other); err != nil {
		t.Fatalf("create other user: %v", err)
	}

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

	// The oldest row — last in the default ordering, so it would only surface on
	// a later page of an unfiltered list.
	got, _, _, err := repo.FindByUserIDWithReceipts(ctx, user.ID, domain.TransactionFilter{
		TransactionID: "txn_4",
	})
	if err != nil {
		t.Fatalf("filter by id: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 row, got %d", len(got))
	}
	if got[0].TransactionID != "txn_4" {
		t.Errorf("expected txn_4, got %s", got[0].TransactionID)
	}
	if got[0].Date != "2025-03-01" || got[0].Amount != 5 {
		t.Errorf("wrong row hydrated: date=%s amount=%v", got[0].Date, got[0].Amount)
	}

	// An unknown id is an empty result, not an error.
	none, _, _, err := repo.FindByUserIDWithReceipts(ctx, user.ID, domain.TransactionFilter{
		TransactionID: "txn_nope",
	})
	if err != nil {
		t.Fatalf("unknown id: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("expected 0 rows for unknown id, got %d", len(none))
	}

	// The user scope still applies: another user's id must not leak through the
	// id filter.
	leaked, _, _, err := repo.FindByUserIDWithReceipts(ctx, other.ID, domain.TransactionFilter{
		TransactionID: "txn_4",
	})
	if err != nil {
		t.Fatalf("cross-user lookup: %v", err)
	}
	if len(leaked) != 0 {
		t.Errorf("expected 0 rows for another user's transaction, got %d", len(leaked))
	}

	// Combining the id filter with pagination still resolves the single row —
	// the count/page-id query and the hydrate query apply the same filter.
	paged, total, _, err := repo.FindByUserIDWithReceipts(ctx, user.ID, domain.TransactionFilter{
		TransactionID: "txn_2",
		Limit:         10,
	})
	if err != nil {
		t.Fatalf("paginated id lookup: %v", err)
	}
	if total != 1 {
		t.Errorf("expected total 1, got %d", total)
	}
	if len(paged) != 1 || paged[0].TransactionID != "txn_2" {
		t.Errorf("expected single txn_2 row, got %+v", paged)
	}
}
