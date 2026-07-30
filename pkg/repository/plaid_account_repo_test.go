package repository

import (
	"context"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

func TestPlaidAccountRepo_UpsertAndFind(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	userRepo := NewUserRepo(db, DialectSQLite)
	user := &domain.User{Name: "Cardholder"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	repo := NewPlaidAccountRepo(db, DialectSQLite)
	acct := &domain.PlaidAccount{
		AccountID: "acct_6222",
		ItemID:    "item_1",
		UserID:    user.ID,
		Mask:      "6222",
		Name:      "Robinhood Credit Card **6222",
		Subtype:   "credit card",
		Type:      "credit",
	}
	if err := repo.Upsert(ctx, acct); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	found, err := repo.FindByAccountID(ctx, "acct_6222")
	if err != nil {
		t.Fatalf("find by account id: %v", err)
	}
	if found.Mask != "6222" || found.Type != "credit" {
		t.Errorf("unexpected account: %+v", found)
	}
	if !found.IsCardSpendable() {
		t.Error("credit account should be card-spendable")
	}

	// Upsert again with a changed mask → update, not duplicate.
	acct.Mask = "9999"
	if err := repo.Upsert(ctx, acct); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	byUser, err := repo.FindByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find by user: %v", err)
	}
	if len(byUser) != 1 {
		t.Fatalf("expected 1 account after re-upsert, got %d", len(byUser))
	}
	if byUser[0].Mask != "9999" {
		t.Errorf("mask not updated: %q", byUser[0].Mask)
	}
}

func TestPlaidAccount_IsCardSpendable(t *testing.T) {
	cases := map[string]bool{
		"credit":     true,
		"depository": true,
		"investment": false,
		"brokerage":  false,
		"":           false,
	}
	for typ, want := range cases {
		if got := (domain.PlaidAccount{Type: typ}).IsCardSpendable(); got != want {
			t.Errorf("IsCardSpendable(%q) = %v, want %v", typ, got, want)
		}
	}
}
