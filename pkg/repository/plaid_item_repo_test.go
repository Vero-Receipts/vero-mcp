package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// seedPlaidItem stores one Item for a freshly created user and returns both.
func seedPlaidItem(t *testing.T, repo *PlaidItemRepo, userRepo *UserRepo, itemID string) (*domain.User, *domain.PlaidItem) {
	t.Helper()
	ctx := context.Background()

	user := &domain.User{Name: "Accountholder"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	item := &domain.PlaidItem{
		UserID:      user.ID,
		ItemID:      itemID,
		AccessToken: "access-sandbox-token",
		SyncCursor:  "cursor-1",
	}
	if err := repo.Create(ctx, item); err != nil {
		t.Fatalf("create item: %v", err)
	}
	return user, item
}

// Disconnecting keeps the row so the item_id stays resolvable for the
// transaction and account rows that outlive it, while every read behaves as
// though the row were gone.
func TestPlaidItemRepo_DeleteKeepsRow(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewPlaidItemRepo(db, DialectSQLite)
	user, item := seedPlaidItem(t, repo, NewUserRepo(db, DialectSQLite), "item_keep")

	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := repo.FindByItemID(ctx, "item_keep"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("FindByItemID after disconnect: got %v, want ErrNotFound", err)
	}
	items, err := repo.FindByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find by user: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("FindByUserID after disconnect returned %d items, want 0", len(items))
	}

	// The point of the whole exercise: the row, and its item_id, survive.
	var itemIDCol, accessToken string
	var deletedAt *string
	err = db.QueryRow(
		"SELECT item_id, access_token, deleted_at FROM plaid_items WHERE id = ?",
		item.ID.String(),
	).Scan(&itemIDCol, &accessToken, &deletedAt)
	if err != nil {
		t.Fatalf("read disconnected row: %v", err)
	}
	if itemIDCol != "item_keep" {
		t.Errorf("item_id = %q, want it preserved", itemIDCol)
	}
	if accessToken != "access-sandbox-token" {
		t.Errorf("access_token = %q, want it preserved as stored", accessToken)
	}
	if deletedAt == nil || *deletedAt == "" {
		t.Error("deleted_at not stamped")
	}
}

// There is no connected Item left to disconnect, so the second call reports the
// same ErrNotFound an unknown id would.
func TestPlaidItemRepo_DeleteTwice(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewPlaidItemRepo(db, DialectSQLite)
	_, item := seedPlaidItem(t, repo, NewUserRepo(db, DialectSQLite), "item_twice")

	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := repo.Delete(ctx, item.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("second delete: got %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, uuid.New()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown id: got %v, want ErrNotFound", err)
	}
}

// A disconnected Item has nothing left to sync, so a background sync must not
// move its cursor.
func TestPlaidItemRepo_UpdateSyncCursorAfterDelete(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewPlaidItemRepo(db, DialectSQLite)
	_, item := seedPlaidItem(t, repo, NewUserRepo(db, DialectSQLite), "item_cursor")

	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := repo.UpdateSyncCursor(ctx, item.ID, "cursor-2"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("UpdateSyncCursor after disconnect: got %v, want ErrNotFound", err)
	}

	var cursor string
	if err := db.QueryRow(
		"SELECT sync_cursor FROM plaid_items WHERE id = ?", item.ID.String(),
	).Scan(&cursor); err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor != "cursor-1" {
		t.Errorf("sync_cursor = %q, want it frozen at the disconnect", cursor)
	}
}

// Relinking a bank after disconnecting it is the ordinary path back, and the
// retained row must not stand in its way.
func TestPlaidItemRepo_RelinkAfterDelete(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	repo := NewPlaidItemRepo(db, DialectSQLite)
	user, item := seedPlaidItem(t, repo, NewUserRepo(db, DialectSQLite), "item_old")

	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Plaid issues a fresh item_id on relink, since /item/remove retires the old one.
	relinked := &domain.PlaidItem{
		UserID:      user.ID,
		ItemID:      "item_new",
		AccessToken: "access-sandbox-token-2",
	}
	if err := repo.Create(ctx, relinked); err != nil {
		t.Fatalf("relink: %v", err)
	}

	items, err := repo.FindByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find by user: %v", err)
	}
	if len(items) != 1 || items[0].ItemID != "item_new" {
		t.Errorf("after relink got %+v, want only item_new", items)
	}
}
