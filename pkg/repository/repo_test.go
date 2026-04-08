package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// UserRepo
// ---------------------------------------------------------------------------

func TestUserRepo_UpsertAndFindByID(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Alice"}
	if err := repo.Upsert(ctx, user); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if user.ID == uuid.Nil {
		t.Fatal("expected non-nil ID after upsert")
	}

	found, err := repo.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Name != "Alice" {
		t.Errorf("expected name Alice, got %s", found.Name)
	}
	if found.IsBankConnected {
		t.Error("expected IsBankConnected false")
	}
}

func TestUserRepo_UpsertUpdatesName(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Alice"}
	if err := repo.Upsert(ctx, user); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Update name via upsert
	user.Name = "Bob"
	if err := repo.Upsert(ctx, user); err != nil {
		t.Fatalf("upsert update: %v", err)
	}

	found, err := repo.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Name != "Bob" {
		t.Errorf("expected name Bob, got %s", found.Name)
	}
}

func TestUserRepo_FindByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepo(db, DialectSQLite)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUserRepo_SetBankConnected(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Alice"}
	if err := repo.Upsert(ctx, user); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := repo.SetBankConnected(ctx, user.ID, true); err != nil {
		t.Fatalf("set bank connected: %v", err)
	}

	found, err := repo.FindByID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !found.IsBankConnected {
		t.Error("expected IsBankConnected true")
	}
}

func TestUserRepo_SetBankConnected_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepo(db, DialectSQLite)
	ctx := context.Background()

	err := repo.SetBankConnected(ctx, uuid.New(), true)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// PlaidItemRepo
// ---------------------------------------------------------------------------

func TestPlaidItemRepo_CRUD(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewPlaidItemRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	item := &domain.PlaidItem{
		UserID:      user.ID,
		ItemID:      "item_abc123",
		AccessToken: "access-sandbox-token",
	}
	if err := repo.Create(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}
	if item.ID == uuid.Nil {
		t.Fatal("expected non-nil ID")
	}

	// FindByItemID
	found, err := repo.FindByItemID(ctx, "item_abc123")
	if err != nil {
		t.Fatalf("find by item_id: %v", err)
	}
	if found.AccessToken != "access-sandbox-token" {
		t.Errorf("expected access token, got %s", found.AccessToken)
	}

	// FindByUserID
	items, err := repo.FindByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find by user_id: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// UpdateSyncCursor
	if err := repo.UpdateSyncCursor(ctx, item.ID, "cursor-123"); err != nil {
		t.Fatalf("update sync cursor: %v", err)
	}
	found, _ = repo.FindByItemID(ctx, "item_abc123")
	if found.SyncCursor != "cursor-123" {
		t.Errorf("expected cursor-123, got %s", found.SyncCursor)
	}

	// Delete
	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.FindByItemID(ctx, "item_abc123")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestPlaidItemRepo_FindByItemID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewPlaidItemRepo(db, DialectSQLite)
	ctx := context.Background()

	_, err := repo.FindByItemID(ctx, "nonexistent")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ReceiptRepo
// ---------------------------------------------------------------------------

func TestReceiptRepo_CreateAndFindByID(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewReceiptRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchant := "Starbucks"
	total := 5.50
	date := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)

	receipt := &domain.Receipt{
		UserID:       user.ID,
		MerchantName: &merchant,
		Total:        &total,
		Date:         &date,
		Source:       "upload",
		Status:       "unmatched",
		LineItems:    json.RawMessage("[]"),
	}
	if err := repo.Create(ctx, receipt); err != nil {
		t.Fatalf("create: %v", err)
	}
	if receipt.ID == uuid.Nil {
		t.Fatal("expected non-nil ID")
	}

	found, err := repo.FindByID(ctx, receipt.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if *found.MerchantName != "Starbucks" {
		t.Errorf("expected Starbucks, got %s", *found.MerchantName)
	}
	if *found.Total != 5.50 {
		t.Errorf("expected total 5.50, got %f", *found.Total)
	}
	if found.Status != "unmatched" {
		t.Errorf("expected status unmatched, got %s", found.Status)
	}
}

func TestReceiptRepo_FindByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewReceiptRepo(db, DialectSQLite)
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReceiptRepo_FindByUserID(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewReceiptRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchant := "Shop A"
	total := 10.0
	for i := 0; i < 3; i++ {
		r := &domain.Receipt{
			UserID:       user.ID,
			MerchantName: &merchant,
			Total:        &total,
			Source:       "upload",
			Status:       "unmatched",
			LineItems:    json.RawMessage("[]"),
		}
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("create receipt %d: %v", i, err)
		}
	}

	receipts, err := repo.FindByUserID(ctx, user.ID, domain.ReceiptFilter{})
	if err != nil {
		t.Fatalf("find by user: %v", err)
	}
	if len(receipts) != 3 {
		t.Errorf("expected 3 receipts, got %d", len(receipts))
	}
}

func TestReceiptRepo_UpdateAndDelete(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewReceiptRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchant := "Before"
	receipt := &domain.Receipt{
		UserID:       user.ID,
		MerchantName: &merchant,
		Source:       "upload",
		Status:       "unmatched",
		LineItems:    json.RawMessage("[]"),
	}
	if err := repo.Create(ctx, receipt); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Update
	newMerchant := "After"
	receipt.MerchantName = &newMerchant
	if err := repo.Update(ctx, receipt); err != nil {
		t.Fatalf("update: %v", err)
	}
	found, _ := repo.FindByID(ctx, receipt.ID)
	if *found.MerchantName != "After" {
		t.Errorf("expected After, got %s", *found.MerchantName)
	}

	// UpdateStatus
	if err := repo.UpdateStatus(ctx, receipt.ID, "matched"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	found, _ = repo.FindByID(ctx, receipt.ID)
	if found.Status != "matched" {
		t.Errorf("expected matched, got %s", found.Status)
	}

	// Delete
	if err := repo.Delete(ctx, receipt.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := repo.FindByID(ctx, receipt.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestReceiptRepo_CountUnmatched(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchant := "Shop"
	total := 10.0
	for i := 0; i < 3; i++ {
		r := &domain.Receipt{
			UserID:       user.ID,
			MerchantName: &merchant,
			Total:        &total,
			Source:       "upload",
			Status:       "unmatched",
			LineItems:    json.RawMessage("[]"),
		}
		if err := receiptRepo.Create(ctx, r); err != nil {
			t.Fatalf("create receipt: %v", err)
		}
	}

	count, err := receiptRepo.CountUnmatched(ctx, user.ID)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 unmatched, got %d", count)
	}
}

func TestReceiptRepo_ExistsDuplicate(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchant := "Starbucks"
	total := 5.50
	date := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)

	r := &domain.Receipt{
		UserID:       user.ID,
		MerchantName: &merchant,
		Total:        &total,
		Date:         &date,
		Source:       "upload",
		Status:       "unmatched",
		LineItems:    json.RawMessage("[]"),
	}
	if err := receiptRepo.Create(ctx, r); err != nil {
		t.Fatalf("create: %v", err)
	}

	exists, err := receiptRepo.ExistsDuplicate(ctx, user.ID, "Starbucks", 5.50, date)
	if err != nil {
		t.Fatalf("exists duplicate: %v", err)
	}
	if !exists {
		t.Error("expected duplicate to exist")
	}

	exists, err = receiptRepo.ExistsDuplicate(ctx, user.ID, "Starbucks", 99.99, date)
	if err != nil {
		t.Fatalf("exists duplicate: %v", err)
	}
	if exists {
		t.Error("expected no duplicate for different total")
	}
}

func TestReceiptRepo_FindByIDWithMatch(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	matchRepo := NewReceiptMatchRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchant := "Shop"
	receipt := &domain.Receipt{
		UserID:       user.ID,
		MerchantName: &merchant,
		Source:       "upload",
		Status:       "matched",
		LineItems:    json.RawMessage("[]"),
	}
	if err := receiptRepo.Create(ctx, receipt); err != nil {
		t.Fatalf("create receipt: %v", err)
	}

	match := &domain.ReceiptMatch{
		ReceiptID:       receipt.ID,
		TransactionID:   "txn_123",
		ConfidenceScore: 0.95,
		MatchMethod:     "auto",
	}
	if err := matchRepo.Create(ctx, match); err != nil {
		t.Fatalf("create match: %v", err)
	}

	rwm, err := receiptRepo.FindByIDWithMatch(ctx, receipt.ID)
	if err != nil {
		t.Fatalf("find with match: %v", err)
	}
	if rwm.Match == nil {
		t.Fatal("expected match to be present")
	}
	if rwm.Match.TransactionID != "txn_123" {
		t.Errorf("expected txn_123, got %s", rwm.Match.TransactionID)
	}
	if rwm.Match.ConfidenceScore != 0.95 {
		t.Errorf("expected 0.95, got %f", rwm.Match.ConfidenceScore)
	}
}

func TestReceiptRepo_FindUnmatchedValid(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	matchRepo := NewReceiptMatchRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchant := "Shop"
	// Create an unmatched receipt
	r1 := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "unmatched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, r1); err != nil {
		t.Fatalf("create r1: %v", err)
	}
	// Create a matched receipt (with a match row)
	r2 := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "matched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, r2); err != nil {
		t.Fatalf("create r2: %v", err)
	}
	match := &domain.ReceiptMatch{ReceiptID: r2.ID, TransactionID: "txn_x", ConfidenceScore: 1.0, MatchMethod: "manual"}
	if err := matchRepo.Create(ctx, match); err != nil {
		t.Fatalf("create match: %v", err)
	}
	// Create an error receipt
	r3 := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "error", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, r3); err != nil {
		t.Fatalf("create r3: %v", err)
	}

	unmatched, err := receiptRepo.FindUnmatchedValid(ctx, user.ID)
	if err != nil {
		t.Fatalf("find unmatched: %v", err)
	}
	if len(unmatched) != 1 {
		t.Errorf("expected 1 unmatched valid, got %d", len(unmatched))
	}
	if len(unmatched) > 0 && unmatched[0].ID != r1.ID {
		t.Errorf("expected receipt %s, got %s", r1.ID, unmatched[0].ID)
	}
}

// ---------------------------------------------------------------------------
// ReceiptMatchRepo
// ---------------------------------------------------------------------------

func TestReceiptMatchRepo_CRUD(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	repo := NewReceiptMatchRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	merchant := "Shop"
	receipt := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "unmatched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, receipt); err != nil {
		t.Fatalf("create receipt: %v", err)
	}

	match := &domain.ReceiptMatch{
		ReceiptID:       receipt.ID,
		TransactionID:   "txn_100",
		AccountID:       "acc_1",
		ConfidenceScore: 0.90,
		MatchMethod:     "auto",
		MatchReason:     "amount + date",
	}
	if err := repo.Create(ctx, match); err != nil {
		t.Fatalf("create: %v", err)
	}

	// FindByReceiptID
	found, err := repo.FindByReceiptID(ctx, receipt.ID)
	if err != nil {
		t.Fatalf("find by receipt_id: %v", err)
	}
	if found.TransactionID != "txn_100" {
		t.Errorf("expected txn_100, got %s", found.TransactionID)
	}

	// FindByTransactionID
	found, err = repo.FindByTransactionID(ctx, "txn_100")
	if err != nil {
		t.Fatalf("find by transaction_id: %v", err)
	}
	if found.ReceiptID != receipt.ID {
		t.Errorf("expected receipt_id %s, got %s", receipt.ID, found.ReceiptID)
	}

	// UpdateMethod
	if err := repo.UpdateMethod(ctx, match.ID, "confirmed"); err != nil {
		t.Fatalf("update method: %v", err)
	}
	found, _ = repo.FindByReceiptID(ctx, receipt.ID)
	if found.MatchMethod != "confirmed" {
		t.Errorf("expected confirmed, got %s", found.MatchMethod)
	}

	// DeleteByReceiptID
	if err := repo.DeleteByReceiptID(ctx, receipt.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.FindByReceiptID(ctx, receipt.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// ReceiptItemRepo
// ---------------------------------------------------------------------------

func TestReceiptItemRepo_CRUD(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	repo := NewReceiptItemRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	merchant := "Shop"
	receipt := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "unmatched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, receipt); err != nil {
		t.Fatalf("create receipt: %v", err)
	}

	item := &domain.ReceiptItem{
		ReceiptID:   receipt.ID,
		UserID:      user.ID,
		Description: "Latte",
		Quantity:    1,
		UnitPrice:   5.50,
		Price:       5.50,
		SortOrder:   0,
	}
	if err := repo.Create(ctx, item); err != nil {
		t.Fatalf("create: %v", err)
	}
	if item.ID == uuid.Nil {
		t.Fatal("expected non-nil ID")
	}

	// FindByID
	found, err := repo.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if found.Description != "Latte" {
		t.Errorf("expected Latte, got %s", found.Description)
	}

	// FindByReceiptID
	items, err := repo.FindByReceiptID(ctx, receipt.ID)
	if err != nil {
		t.Fatalf("find by receipt_id: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// Update
	item.Description = "Mocha"
	item.Price = 6.00
	if err := repo.Update(ctx, item); err != nil {
		t.Fatalf("update: %v", err)
	}
	found, _ = repo.FindByID(ctx, item.ID)
	if found.Description != "Mocha" {
		t.Errorf("expected Mocha, got %s", found.Description)
	}

	// Delete
	if err := repo.Delete(ctx, item.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.FindByID(ctx, item.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestReceiptItemRepo_DeleteByReceiptID(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	repo := NewReceiptItemRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	merchant := "Shop"
	receipt := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "unmatched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, receipt); err != nil {
		t.Fatalf("create receipt: %v", err)
	}

	for i := 0; i < 3; i++ {
		item := &domain.ReceiptItem{ReceiptID: receipt.ID, UserID: user.ID, Description: "Item", Quantity: 1, UnitPrice: 1, Price: 1}
		if err := repo.Create(ctx, item); err != nil {
			t.Fatalf("create item: %v", err)
		}
	}

	if err := repo.DeleteByReceiptID(ctx, receipt.ID); err != nil {
		t.Fatalf("delete by receipt_id: %v", err)
	}
	items, _ := repo.FindByReceiptID(ctx, receipt.ID)
	if len(items) != 0 {
		t.Errorf("expected 0 items after delete, got %d", len(items))
	}
}

// ---------------------------------------------------------------------------
// TransactionCacheRepo
// ---------------------------------------------------------------------------

func TestTransactionCacheRepo_UpsertBatchAndFind(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchantName := "Coffee Shop"
	txns := []domain.Transaction{
		{TransactionID: "txn_1", AccountID: "acc_1", Amount: 5.50, Date: "2025-03-15", Name: "COFFEE SHOP", MerchantName: &merchantName, Category: json.RawMessage(`["Food"]`)},
		{TransactionID: "txn_2", AccountID: "acc_1", Amount: 12.00, Date: "2025-03-16", Name: "GROCERY STORE", Category: json.RawMessage(`["Groceries"]`)},
	}

	count, err := repo.UpsertBatch(ctx, user.ID, txns)
	if err != nil {
		t.Fatalf("upsert batch: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 upserted, got %d", count)
	}

	// FindByUserID
	found, err := repo.FindByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("find by user: %v", err)
	}
	if len(found) != 2 {
		t.Errorf("expected 2 transactions, got %d", len(found))
	}

	// FindByTransactionID
	single, err := repo.FindByTransactionID(ctx, "txn_1")
	if err != nil {
		t.Fatalf("find by txn_id: %v", err)
	}
	if single.Amount != 5.50 {
		t.Errorf("expected 5.50, got %f", single.Amount)
	}
}

func TestTransactionCacheRepo_UpsertBatchEmpty(t *testing.T) {
	db := setupTestDB(t)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	count, err := repo.UpsertBatch(ctx, uuid.New(), nil)
	if err != nil {
		t.Fatalf("upsert empty: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestTransactionCacheRepo_FindByTransactionID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	_, err := repo.FindByTransactionID(ctx, "nonexistent")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestTransactionCacheRepo_FindUnmatchedCandidates(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	txns := []domain.Transaction{
		{TransactionID: "txn_a", AccountID: "acc_1", Amount: 10.00, Date: "2025-03-15", Name: "SHOP A", Category: json.RawMessage("[]")},
		{TransactionID: "txn_b", AccountID: "acc_1", Amount: 100.00, Date: "2025-03-15", Name: "SHOP B", Category: json.RawMessage("[]")},
		{TransactionID: "txn_c", AccountID: "acc_1", Amount: 10.00, Date: "2025-06-01", Name: "SHOP C", Category: json.RawMessage("[]")},
	}
	if _, err := repo.UpsertBatch(ctx, user.ID, txns); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	candidates, err := repo.FindUnmatchedCandidates(ctx, user.ID, 10.00, "2025-03-15")
	if err != nil {
		t.Fatalf("find candidates: %v", err)
	}
	// txn_a matches (amount and date), txn_b too far on amount, txn_c too far on date
	if len(candidates) != 1 {
		t.Errorf("expected 1 candidate, got %d", len(candidates))
	}
}

func TestTransactionCacheRepo_RemoveBatch(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	txns := []domain.Transaction{
		{TransactionID: "txn_x", AccountID: "acc_1", Amount: 1, Date: "2025-01-01", Name: "X", Category: json.RawMessage("[]")},
		{TransactionID: "txn_y", AccountID: "acc_1", Amount: 2, Date: "2025-01-01", Name: "Y", Category: json.RawMessage("[]")},
	}
	if _, err := repo.UpsertBatch(ctx, user.ID, txns); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := repo.RemoveBatch(ctx, []string{"txn_x"}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err := repo.FindByTransactionID(ctx, "txn_x")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound for removed txn, got %v", err)
	}
	_, err = repo.FindByTransactionID(ctx, "txn_y")
	if err != nil {
		t.Errorf("txn_y should still exist: %v", err)
	}
}

func TestTransactionCacheRepo_UpdateCorrectedCategory(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	repo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	txns := []domain.Transaction{
		{TransactionID: "txn_cat", AccountID: "acc_1", Amount: 10, Date: "2025-01-01", Name: "Store", Category: json.RawMessage("[]")},
	}
	if _, err := repo.UpsertBatch(ctx, user.ID, txns); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if err := repo.UpdateCorrectedCategory(ctx, "txn_cat", "FOOD_AND_DRINK", "FOOD_AND_DRINK_RESTAURANT"); err != nil {
		t.Fatalf("update corrected category: %v", err)
	}

	found, _ := repo.FindByTransactionID(ctx, "txn_cat")
	if found.CorrectedPFCPrimary == nil || *found.CorrectedPFCPrimary != "FOOD_AND_DRINK" {
		t.Errorf("expected corrected primary FOOD_AND_DRINK, got %v", found.CorrectedPFCPrimary)
	}
}

// ---------------------------------------------------------------------------
// NoteRepo
// ---------------------------------------------------------------------------

func TestNoteRepo_CRUD(t *testing.T) {
	db := setupTestDB(t)
	repo := NewNoteRepo(db, DialectSQLite)
	ctx := context.Background()

	userID := uuid.New()
	note := &domain.Note{
		UserID:     userID,
		EntityType: "receipt",
		EntityID:   uuid.New().String(),
		Content:    "Important note",
	}
	if err := repo.Create(ctx, note); err != nil {
		t.Fatalf("create: %v", err)
	}

	// FindByID
	found, err := repo.FindByID(ctx, note.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Content != "Important note" {
		t.Errorf("expected 'Important note', got %q", found.Content)
	}

	// FindByEntity
	notes, err := repo.FindByEntity(ctx, userID, "receipt", note.EntityID)
	if err != nil {
		t.Fatalf("find by entity: %v", err)
	}
	if len(notes) != 1 {
		t.Errorf("expected 1 note, got %d", len(notes))
	}

	// Update
	note.Content = "Updated note"
	if err := repo.Update(ctx, note); err != nil {
		t.Fatalf("update: %v", err)
	}
	found, _ = repo.FindByID(ctx, note.ID)
	if found.Content != "Updated note" {
		t.Errorf("expected 'Updated note', got %q", found.Content)
	}

	// Delete
	if err := repo.Delete(ctx, note.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.FindByID(ctx, note.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// LabelRepo
// ---------------------------------------------------------------------------

func TestLabelRepo_CRUD(t *testing.T) {
	db := setupTestDB(t)
	repo := NewLabelRepo(db, DialectSQLite)
	ctx := context.Background()

	userID := uuid.New()
	label := &domain.Label{
		UserID: userID,
		Name:   "Groceries",
		Color:  "#00FF00",
	}
	if err := repo.Create(ctx, label); err != nil {
		t.Fatalf("create: %v", err)
	}

	// FindByID
	found, err := repo.FindByID(ctx, label.ID)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.Name != "Groceries" {
		t.Errorf("expected Groceries, got %s", found.Name)
	}
	if found.Color != "#00FF00" {
		t.Errorf("expected #00FF00, got %s", found.Color)
	}

	// FindByUserID
	labels, err := repo.FindByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("find by user: %v", err)
	}
	if len(labels) != 1 {
		t.Errorf("expected 1 label, got %d", len(labels))
	}

	// Update
	label.Name = "Food"
	label.Color = "#FF0000"
	if err := repo.Update(ctx, label); err != nil {
		t.Fatalf("update: %v", err)
	}
	found, _ = repo.FindByID(ctx, label.ID)
	if found.Name != "Food" {
		t.Errorf("expected Food, got %s", found.Name)
	}

	// Delete
	if err := repo.Delete(ctx, label.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err = repo.FindByID(ctx, label.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// LabelAssignmentRepo
// ---------------------------------------------------------------------------

func TestLabelAssignmentRepo_AssignAndFind(t *testing.T) {
	db := setupTestDB(t)
	labelRepo := NewLabelRepo(db, DialectSQLite)
	repo := NewLabelAssignmentRepo(db, DialectSQLite)
	ctx := context.Background()

	userID := uuid.New()
	label := &domain.Label{UserID: userID, Name: "Tag", Color: "#000"}
	if err := labelRepo.Create(ctx, label); err != nil {
		t.Fatalf("create label: %v", err)
	}

	entityID := uuid.New().String()
	assignment := &domain.LabelAssignment{
		LabelID:    label.ID,
		UserID:     userID,
		EntityType: "receipt",
		EntityID:   entityID,
	}
	if err := repo.Assign(ctx, assignment); err != nil {
		t.Fatalf("assign: %v", err)
	}

	// FindByEntity
	labels, err := repo.FindByEntity(ctx, userID, "receipt", entityID)
	if err != nil {
		t.Fatalf("find by entity: %v", err)
	}
	if len(labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(labels))
	}
	if labels[0].Name != "Tag" {
		t.Errorf("expected Tag, got %s", labels[0].Name)
	}

	// Duplicate assign returns conflict
	dup := &domain.LabelAssignment{
		LabelID:    label.ID,
		UserID:     userID,
		EntityType: "receipt",
		EntityID:   entityID,
	}
	err = repo.Assign(ctx, dup)
	if !errors.Is(err, domain.ErrConflict) {
		t.Errorf("expected ErrConflict on duplicate assign, got %v", err)
	}

	// Unassign
	if err := repo.Unassign(ctx, label.ID, "receipt", entityID); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	labels, _ = repo.FindByEntity(ctx, userID, "receipt", entityID)
	if len(labels) != 0 {
		t.Errorf("expected 0 labels after unassign, got %d", len(labels))
	}
}

func TestLabelAssignmentRepo_Unassign_NotFound(t *testing.T) {
	db := setupTestDB(t)
	repo := NewLabelAssignmentRepo(db, DialectSQLite)
	ctx := context.Background()

	err := repo.Unassign(ctx, uuid.New(), "receipt", "xyz")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MerchantAliasRepo
// ---------------------------------------------------------------------------

func TestMerchantAliasRepo_CreateAndFind(t *testing.T) {
	db := setupTestDB(t)
	repo := NewMerchantAliasRepo(db, DialectSQLite)
	ctx := context.Background()

	alias := &domain.MerchantAlias{
		Canonical: "Starbucks",
		Alias:     "SBUX",
		Source:    "manual",
	}
	if err := repo.Create(ctx, alias); err != nil {
		t.Fatalf("create: %v", err)
	}

	// FindCanonical
	canonical, err := repo.FindCanonical(ctx, "SBUX")
	if err != nil {
		t.Fatalf("find canonical: %v", err)
	}
	if canonical != "Starbucks" {
		t.Errorf("expected Starbucks, got %s", canonical)
	}

	// FindCanonical case-insensitive
	canonical, err = repo.FindCanonical(ctx, "sbux")
	if err != nil {
		t.Fatalf("find canonical lowercase: %v", err)
	}
	if canonical != "Starbucks" {
		t.Errorf("expected Starbucks, got %s", canonical)
	}

	// FindCanonical not found
	_, err = repo.FindCanonical(ctx, "UNKNOWN")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestMerchantAliasRepo_AreSameMerchant(t *testing.T) {
	db := setupTestDB(t)
	repo := NewMerchantAliasRepo(db, DialectSQLite)
	ctx := context.Background()

	// Same string
	same, err := repo.AreSameMerchant(ctx, "Starbucks", "starbucks")
	if err != nil {
		t.Fatalf("are same: %v", err)
	}
	if !same {
		t.Error("expected same for case-insensitive match")
	}

	// Create aliases pointing to same canonical
	repo.Create(ctx, &domain.MerchantAlias{Canonical: "Starbucks", Alias: "SBUX", Source: "manual"})
	repo.Create(ctx, &domain.MerchantAlias{Canonical: "Starbucks", Alias: "Starbucks Coffee", Source: "manual"})

	same, err = repo.AreSameMerchant(ctx, "SBUX", "Starbucks Coffee")
	if err != nil {
		t.Fatalf("are same: %v", err)
	}
	if !same {
		t.Error("expected same for aliases with same canonical")
	}

	// Different merchants
	same, _ = repo.AreSameMerchant(ctx, "SBUX", "Walmart")
	if same {
		t.Error("expected different merchants")
	}
}

func TestMerchantAliasRepo_CreateDuplicate(t *testing.T) {
	db := setupTestDB(t)
	repo := NewMerchantAliasRepo(db, DialectSQLite)
	ctx := context.Background()

	a1 := &domain.MerchantAlias{Canonical: "Starbucks", Alias: "SBUX", Source: "manual"}
	if err := repo.Create(ctx, a1); err != nil {
		t.Fatalf("create first: %v", err)
	}

	// Duplicate should succeed silently (ON CONFLICT DO NOTHING)
	a2 := &domain.MerchantAlias{Canonical: "Starbucks", Alias: "SBUX", Source: "llm"}
	if err := repo.Create(ctx, a2); err != nil {
		t.Fatalf("create duplicate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MatchAuditRepo
// ---------------------------------------------------------------------------

func TestMatchAuditRepo_Create(t *testing.T) {
	db := setupTestDB(t)
	repo := NewMatchAuditRepo(db, DialectSQLite)
	ctx := context.Background()

	entry := &domain.MatchAuditEntry{
		ReceiptID:      uuid.New(),
		TransactionID:  "txn_audit",
		AmountScore:    1.0,
		DateScore:      0.9,
		MerchantScore:  0.8,
		CompositeScore: 0.90,
		LLMUsed:        false,
		Outcome:        "auto",
		Reason:         "all scores high",
	}
	if err := repo.Create(ctx, entry); err != nil {
		t.Fatalf("create: %v", err)
	}
	if entry.ID == uuid.Nil {
		t.Error("expected non-nil ID")
	}
	if entry.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

// ---------------------------------------------------------------------------
// CategoryCorrectionCacheRepo
// ---------------------------------------------------------------------------

func TestCategoryCorrectionCacheRepo_CreateAndFind(t *testing.T) {
	db := setupTestDB(t)
	repo := NewCategoryCorrectionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	entry := &domain.CategoryCorrectionCache{
		MerchantCanonical:    "Starbucks",
		OriginalPFCPrimary:   "GENERAL_MERCHANDISE",
		OriginalPFCDetailed:  "GENERAL_MERCHANDISE_OTHER",
		CorrectedPFCPrimary:  "FOOD_AND_DRINK",
		CorrectedPFCDetailed: "FOOD_AND_DRINK_COFFEE",
		Source:               "llm",
	}
	if err := repo.Create(ctx, entry); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Find
	found, err := repo.FindByMerchantAndCategory(ctx, "starbucks", "GENERAL_MERCHANDISE_OTHER")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if found.CorrectedPFCPrimary != "FOOD_AND_DRINK" {
		t.Errorf("expected FOOD_AND_DRINK, got %s", found.CorrectedPFCPrimary)
	}

	// Not found
	_, err = repo.FindByMerchantAndCategory(ctx, "unknown", "UNKNOWN")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCategoryCorrectionCacheRepo_CreateDuplicate(t *testing.T) {
	db := setupTestDB(t)
	repo := NewCategoryCorrectionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	entry := &domain.CategoryCorrectionCache{
		MerchantCanonical:    "Shop",
		OriginalPFCPrimary:   "X",
		OriginalPFCDetailed:  "X_Y",
		CorrectedPFCPrimary:  "A",
		CorrectedPFCDetailed: "A_B",
		Source:               "llm",
	}
	if err := repo.Create(ctx, entry); err != nil {
		t.Fatalf("create first: %v", err)
	}

	// Duplicate should succeed silently
	dup := &domain.CategoryCorrectionCache{
		MerchantCanonical:    "Shop",
		OriginalPFCPrimary:   "X",
		OriginalPFCDetailed:  "X_Y",
		CorrectedPFCPrimary:  "C",
		CorrectedPFCDetailed: "C_D",
		Source:               "rule",
	}
	if err := repo.Create(ctx, dup); err != nil {
		t.Fatalf("create duplicate: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TransactionCacheRepo - additional query tests
// ---------------------------------------------------------------------------

func TestTransactionCacheRepo_FindAllUnmatched(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	matchRepo := NewReceiptMatchRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	txns := []domain.Transaction{
		{TransactionID: "txn_u1", AccountID: "acc_1", Amount: 10, Date: "2025-03-15", Name: "Unmatched", Category: json.RawMessage("[]")},
		{TransactionID: "txn_m1", AccountID: "acc_1", Amount: 20, Date: "2025-03-15", Name: "Matched", Category: json.RawMessage("[]")},
	}
	if _, err := txRepo.UpsertBatch(ctx, user.ID, txns); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Create a receipt and match for txn_m1
	merchant := "Shop"
	receipt := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "upload", Status: "matched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, receipt); err != nil {
		t.Fatalf("create receipt: %v", err)
	}
	match := &domain.ReceiptMatch{ReceiptID: receipt.ID, TransactionID: "txn_m1", ConfidenceScore: 1.0, MatchMethod: "manual"}
	if err := matchRepo.Create(ctx, match); err != nil {
		t.Fatalf("create match: %v", err)
	}

	unmatched, err := txRepo.FindAllUnmatched(ctx, user.ID)
	if err != nil {
		t.Fatalf("find all unmatched: %v", err)
	}
	if len(unmatched) != 1 {
		t.Errorf("expected 1 unmatched, got %d", len(unmatched))
	}
	if len(unmatched) > 0 && unmatched[0].TransactionID != "txn_u1" {
		t.Errorf("expected txn_u1, got %s", unmatched[0].TransactionID)
	}
}

func TestTransactionCacheRepo_SearchUnmatched(t *testing.T) {
	db := setupTestDB(t)
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)
	ctx := context.Background()

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	merchantName := "Starbucks"
	txns := []domain.Transaction{
		{TransactionID: "txn_s1", AccountID: "acc_1", Amount: 5, Date: "2025-03-15", Name: "STARBUCKS #123", MerchantName: &merchantName, Category: json.RawMessage("[]")},
		{TransactionID: "txn_s2", AccountID: "acc_1", Amount: 10, Date: "2025-03-15", Name: "WALMART", Category: json.RawMessage("[]")},
	}
	if _, err := txRepo.UpsertBatch(ctx, user.ID, txns); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	results, err := txRepo.SearchUnmatched(ctx, user.ID, "starbucks")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'starbucks', got %d", len(results))
	}
}

