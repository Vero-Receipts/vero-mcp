package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// seedTx inserts one transaction for the user with an optional merchant.
func seedTx(t *testing.T, ctx context.Context, repo *TransactionCacheRepo, userID uuid.UUID, txID string, merchantID *uuid.UUID, amount float64, date string) {
	t.Helper()
	tx := domain.Transaction{
		TransactionID: txID,
		AccountID:     "acc_1",
		Amount:        amount,
		Date:          date,
		Name:          "TXN",
		MerchantID:    merchantID,
		Category:      json.RawMessage(`["Food"]`),
	}
	if _, err := repo.UpsertBatch(ctx, userID, []domain.Transaction{tx}); err != nil {
		t.Fatalf("upsert tx %s: %v", txID, err)
	}
}

// TestReceiptMatchRepo_OneReceiptManyTransactions is the regression test for migration 008:
// after dropping receipt_matches.receipt_id UNIQUE, one receipt can be linked to several
// transactions (a real match plus carried-forward 'recurring' matches).
func TestReceiptMatchRepo_OneReceiptManyTransactions(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	matchRepo := NewReceiptMatchRepo(db, DialectSQLite)

	user := &domain.User{Name: "U"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("user: %v", err)
	}
	seedTx(t, ctx, txRepo, user.ID, "t1", nil, 11.99, "2026-04-24")
	seedTx(t, ctx, txRepo, user.ID, "t2", nil, 11.99, "2026-05-24")

	merchant := "SoundCloud"
	r := &domain.Receipt{UserID: user.ID, MerchantName: &merchant, Source: "email", Status: "matched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, r); err != nil {
		t.Fatalf("receipt: %v", err)
	}

	// Real match on t1.
	if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "t1", ConfidenceScore: 0.95, MatchMethod: "auto"}); err != nil {
		t.Fatalf("real match: %v", err)
	}
	// Derived match reusing the SAME receipt on t2 — this is what 008 must permit.
	if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "t2", ConfidenceScore: 1.0, MatchMethod: "recurring"}); err != nil {
		t.Fatalf("derived match (many->1 should be allowed after 008): %v", err)
	}

	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM receipt_matches WHERE receipt_id = ?", r.ID.String()).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 matches for one receipt, got %d", n)
	}
}

// TestFindRecurringCandidates covers the detection query end-to-end: merchant-name join,
// the matched flag, and the real-vs-derived source-receipt distinction.
func TestFindRecurringCandidates(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)
	merchantRepo := NewMerchantRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	matchRepo := NewReceiptMatchRepo(db, DialectSQLite)

	user := &domain.User{Name: "U"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("user: %v", err)
	}

	m, err := merchantRepo.Upsert(ctx, "SoundCloud", nil, nil, nil)
	if err != nil {
		t.Fatalf("merchant: %v", err)
	}

	seedTx(t, ctx, txRepo, user.ID, "t1", &m.ID, 11.99, "2026-04-24") // will get a real subscription receipt
	seedTx(t, ctx, txRepo, user.ID, "t2", &m.ID, 11.99, "2026-05-24") // bare
	// A merchant-less transaction must be excluded by the query.
	seedTx(t, ctx, txRepo, user.ID, "t3", nil, 5.00, "2026-05-01")

	sub := true
	r := &domain.Receipt{UserID: user.ID, MerchantName: strPtr("SoundCloud"), Source: "email", Status: "matched", LineItems: json.RawMessage("[]"), IsSubscription: &sub}
	if err := receiptRepo.Create(ctx, r); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "t1", ConfidenceScore: 0.95, MatchMethod: "auto"}); err != nil {
		t.Fatalf("match: %v", err)
	}

	cands, err := txRepo.FindRecurringCandidates(ctx, user.ID)
	if err != nil {
		t.Fatalf("FindRecurringCandidates: %v", err)
	}

	byID := map[string]domain.RecurringCandidate{}
	for _, c := range cands {
		byID[c.TransactionID] = c
	}

	if _, ok := byID["t3"]; ok {
		t.Errorf("merchant-less transaction t3 should be excluded")
	}
	if len(byID) != 2 {
		t.Fatalf("expected 2 candidates (t1,t2), got %d: %+v", len(byID), cands)
	}

	c1 := byID["t1"]
	if c1.MerchantName != "SoundCloud" {
		t.Errorf("t1 MerchantName = %q, want SoundCloud (canonical_name join)", c1.MerchantName)
	}
	if !c1.Matched {
		t.Errorf("t1 should be matched")
	}
	if c1.SourceReceipt == nil || *c1.SourceReceipt != r.ID {
		t.Errorf("t1 SourceReceipt = %v, want %v", c1.SourceReceipt, r.ID)
	}
	if c1.IsSubscription == nil || !*c1.IsSubscription {
		t.Errorf("t1 IsSubscription = %v, want true", c1.IsSubscription)
	}

	c2 := byID["t2"]
	if c2.MerchantName != "SoundCloud" {
		t.Errorf("t2 MerchantName = %q, want SoundCloud", c2.MerchantName)
	}
	if c2.Matched {
		t.Errorf("t2 should be unmatched (bare renewal)")
	}
	if c2.SourceReceipt != nil {
		t.Errorf("t2 SourceReceipt = %v, want nil", c2.SourceReceipt)
	}
}

// TestFindRecurringCandidates_DerivedMatchIsNotASource verifies a carried-forward
// ('recurring') match is reported as matched but never as a source receipt — otherwise a
// derived match could seed another derived match.
func TestFindRecurringCandidates_DerivedMatchIsNotASource(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)
	merchantRepo := NewMerchantRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	matchRepo := NewReceiptMatchRepo(db, DialectSQLite)

	user := &domain.User{Name: "U"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("user: %v", err)
	}
	m, err := merchantRepo.Upsert(ctx, "Netflix", nil, nil, nil)
	if err != nil {
		t.Fatalf("merchant: %v", err)
	}
	seedTx(t, ctx, txRepo, user.ID, "t1", &m.ID, 15.49, "2026-05-01")

	r := &domain.Receipt{UserID: user.ID, MerchantName: strPtr("Netflix"), Source: "email", Status: "matched", LineItems: json.RawMessage("[]")}
	if err := receiptRepo.Create(ctx, r); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	// Only a derived match exists for t1.
	if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "t1", ConfidenceScore: 1.0, MatchMethod: "recurring"}); err != nil {
		t.Fatalf("match: %v", err)
	}

	cands, err := txRepo.FindRecurringCandidates(ctx, user.ID)
	if err != nil {
		t.Fatalf("FindRecurringCandidates: %v", err)
	}
	if len(cands) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(cands))
	}
	c := cands[0]
	if !c.Matched {
		t.Errorf("t1 has a match, should report Matched=true")
	}
	if c.SourceReceipt != nil {
		t.Errorf("derived match must not be reported as a source receipt, got %v", c.SourceReceipt)
	}
}

// TestSetRecurring verifies the flag is persisted and surfaces on read.
func TestSetRecurring(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)

	user := &domain.User{Name: "U"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("user: %v", err)
	}
	seedTx(t, ctx, txRepo, user.ID, "t1", nil, 10.00, "2026-05-01")
	seedTx(t, ctx, txRepo, user.ID, "t2", nil, 10.00, "2026-05-02")

	if err := txRepo.SetRecurring(ctx, []string{"t1"}); err != nil {
		t.Fatalf("SetRecurring: %v", err)
	}
	// Empty slice is a no-op, not an error.
	if err := txRepo.SetRecurring(ctx, nil); err != nil {
		t.Fatalf("SetRecurring(nil): %v", err)
	}

	got1, err := txRepo.FindByTransactionID(ctx, "t1")
	if err != nil {
		t.Fatalf("find t1: %v", err)
	}
	if !got1.Recurring {
		t.Errorf("t1.Recurring = false, want true")
	}
	got2, err := txRepo.FindByTransactionID(ctx, "t2")
	if err != nil {
		t.Fatalf("find t2: %v", err)
	}
	if got2.Recurring {
		t.Errorf("t2.Recurring = true, want false (not set)")
	}
}

// TestAllUserIDsWithTransactions verifies distinct users are returned.
func TestAllUserIDsWithTransactions(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	userRepo := NewUserRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)

	u1 := &domain.User{Name: "U1"}
	u2 := &domain.User{Name: "U2"}
	if err := userRepo.Upsert(ctx, u1); err != nil {
		t.Fatalf("u1: %v", err)
	}
	if err := userRepo.Upsert(ctx, u2); err != nil {
		t.Fatalf("u2: %v", err)
	}
	seedTx(t, ctx, txRepo, u1.ID, "t1", nil, 1.00, "2026-05-01")
	seedTx(t, ctx, txRepo, u1.ID, "t2", nil, 2.00, "2026-05-02")
	seedTx(t, ctx, txRepo, u2.ID, "t3", nil, 3.00, "2026-05-03")

	ids, err := txRepo.AllUserIDsWithTransactions(ctx)
	if err != nil {
		t.Fatalf("AllUserIDsWithTransactions: %v", err)
	}
	set := map[uuid.UUID]bool{}
	for _, id := range ids {
		set[id] = true
	}
	if !set[u1.ID] || !set[u2.ID] {
		t.Errorf("expected both users, got %v", ids)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 distinct users, got %d", len(ids))
	}
}

func strPtr(s string) *string { return &s }
