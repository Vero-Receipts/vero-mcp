package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	localrepo "github.com/Vero-Receipts/vero-mcp/internal/repository"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

func strPtr(s string) *string { return &s }

type propagateFixture struct {
	svc      *ReceiptService
	db       *sql.DB
	txRepo   *repository.TransactionCacheRepo
	merchant *repository.MerchantRepo
	receipts *repository.ReceiptRepo
	matches  *repository.ReceiptMatchRepo
	userID   uuid.UUID
}

// newPropagateFixture builds a ReceiptService backed by a real SQLite database (embedded
// migrations applied via repository.Open). Only the repos PropagateRecurring touches are
// wired; the OCR/currency/etc. services are nil because PropagateRecurring never calls them.
func newPropagateFixture(t *testing.T) propagateFixture {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := localrepo.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	userRepo := repository.NewUserRepo(db, repository.DialectSQLite)
	txRepo := repository.NewTransactionCacheRepo(db, repository.DialectSQLite)
	merchantRepo := repository.NewMerchantRepo(db, repository.DialectSQLite)
	receiptRepo := repository.NewReceiptRepo(db, repository.DialectSQLite)
	matchRepo := repository.NewReceiptMatchRepo(db, repository.DialectSQLite)

	user := &domain.User{Name: "U"}
	if err := userRepo.Upsert(context.Background(), user); err != nil {
		t.Fatalf("user: %v", err)
	}

	svc := NewReceiptService(receiptRepo, matchRepo, txRepo, nil, nil, nil, nil, nil, nil, "")
	return propagateFixture{svc, db, txRepo, merchantRepo, receiptRepo, matchRepo, user.ID}
}

func seedTxn(t *testing.T, ctx context.Context, repo *repository.TransactionCacheRepo, userID uuid.UUID, txID string, merchantID *uuid.UUID, amount float64, date string) {
	t.Helper()
	tx := domain.Transaction{
		TransactionID: txID, AccountID: "acc", Amount: amount, Date: date, Name: "TXN",
		MerchantID: merchantID, Category: json.RawMessage(`["Food"]`),
	}
	if _, err := repo.UpsertBatch(ctx, userID, []domain.Transaction{tx}); err != nil {
		t.Fatalf("seed %s: %v", txID, err)
	}
}

// TestPropagateRecurring_SubscriptionSource: a subscription receipt on the first charge
// establishes the series at 2 occurrences; every member is flagged recurring and the bare
// renewal inherits the receipt (a 'recurring' match).
func TestPropagateRecurring_SubscriptionSource(t *testing.T) {
	ctx := context.Background()
	f := newPropagateFixture(t)
	svc, txRepo, merchantRepo, receiptRepo, matchRepo, userID := f.svc, f.txRepo, f.merchant, f.receipts, f.matches, f.userID

	m, err := merchantRepo.Upsert(ctx, "SoundCloud", nil, nil, nil)
	if err != nil {
		t.Fatalf("merchant: %v", err)
	}
	seedTxn(t, ctx, txRepo, userID, "t1", &m.ID, 11.99, "2026-04-24")
	seedTxn(t, ctx, txRepo, userID, "t2", &m.ID, 11.99, "2026-05-24")

	sub := true
	r := &domain.Receipt{UserID: userID, MerchantName: strPtr("SoundCloud"), Source: "email", Status: "matched", LineItems: json.RawMessage("[]"), IsSubscription: &sub}
	if err := receiptRepo.Create(ctx, r); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "t1", ConfidenceScore: 0.95, MatchMethod: "auto"}); err != nil {
		t.Fatalf("match: %v", err)
	}

	svc.PropagateRecurring(ctx, userID)

	// Both transactions flagged recurring.
	for _, id := range []string{"t1", "t2"} {
		got, err := txRepo.FindByTransactionID(ctx, id)
		if err != nil {
			t.Fatalf("find %s: %v", id, err)
		}
		if !got.Recurring {
			t.Errorf("%s.Recurring = false, want true", id)
		}
	}

	// t2 now has a derived match to the same receipt.
	dm, err := matchRepo.FindByTransactionID(ctx, "t2")
	if err != nil {
		t.Fatalf("find t2 match: %v", err)
	}
	if dm.ReceiptID != r.ID {
		t.Errorf("t2 match receipt = %v, want %v", dm.ReceiptID, r.ID)
	}
	if dm.MatchMethod != "recurring" {
		t.Errorf("t2 match method = %q, want recurring", dm.MatchMethod)
	}
}

// TestPropagateRecurring_PatternNoReceipt: three same-amount monthly charges with no receipt
// anywhere establish the series (pattern >= 3) and are flagged recurring, but nothing is
// itemized (no source).
func TestPropagateRecurring_PatternNoReceipt(t *testing.T) {
	ctx := context.Background()
	f := newPropagateFixture(t)
	svc, txRepo, merchantRepo, matchRepo, userID := f.svc, f.txRepo, f.merchant, f.matches, f.userID

	m, err := merchantRepo.Upsert(ctx, "Planet Fitness", nil, nil, nil)
	if err != nil {
		t.Fatalf("merchant: %v", err)
	}
	seedTxn(t, ctx, txRepo, userID, "t1", &m.ID, 10.00, "2026-03-17")
	seedTxn(t, ctx, txRepo, userID, "t2", &m.ID, 10.00, "2026-04-17")
	seedTxn(t, ctx, txRepo, userID, "t3", &m.ID, 10.00, "2026-05-17")

	svc.PropagateRecurring(ctx, userID)

	for _, id := range []string{"t1", "t2", "t3"} {
		got, err := txRepo.FindByTransactionID(ctx, id)
		if err != nil {
			t.Fatalf("find %s: %v", id, err)
		}
		if !got.Recurring {
			t.Errorf("%s.Recurring = false, want true", id)
		}
		if _, err := matchRepo.FindByTransactionID(ctx, id); err != domain.ErrNotFound {
			t.Errorf("%s should have no match (no source receipt), got err=%v", id, err)
		}
	}
}

// TestPropagateRecurring_PatternItemizesFromSource verifies that a ≥3-occurrence series is
// established by pattern alone and carries its source receipt forward to the bare charges,
// regardless of the source receipt's subscription flag.
func TestPropagateRecurring_PatternItemizesFromSource(t *testing.T) {
	ctx := context.Background()
	f := newPropagateFixture(t)
	svc, txRepo, merchantRepo, receiptRepo, matchRepo, userID := f.svc, f.txRepo, f.merchant, f.receipts, f.matches, f.userID

	m, err := merchantRepo.Upsert(ctx, "SoundCloud", nil, nil, nil)
	if err != nil {
		t.Fatalf("merchant: %v", err)
	}
	seedTxn(t, ctx, txRepo, userID, "t1", &m.ID, 11.99, "2026-03-10")
	seedTxn(t, ctx, txRepo, userID, "t2", &m.ID, 11.99, "2026-04-10")
	seedTxn(t, ctx, txRepo, userID, "t3", &m.ID, 11.99, "2026-05-10")

	// Source receipt whose own wording does not self-declare a subscription (is_sub=false),
	// as with an invoice PDF — the ≥3 pattern should itemize from it anyway.
	notSub := false
	r := &domain.Receipt{UserID: userID, MerchantName: strPtr("SoundCloud"), Source: "email", Status: "matched", LineItems: json.RawMessage("[]"), IsSubscription: &notSub}
	if err := receiptRepo.Create(ctx, r); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "t1", ConfidenceScore: 0.95, MatchMethod: "auto"}); err != nil {
		t.Fatalf("match: %v", err)
	}

	svc.PropagateRecurring(ctx, userID)

	for _, id := range []string{"t1", "t2", "t3"} {
		got, err := txRepo.FindByTransactionID(ctx, id)
		if err != nil {
			t.Fatalf("find %s: %v", id, err)
		}
		if !got.Recurring {
			t.Errorf("%s.Recurring = false, want true", id)
		}
	}
	// The two bare charges inherit the source receipt as carried-forward matches.
	for _, id := range []string{"t2", "t3"} {
		dm, err := matchRepo.FindByTransactionID(ctx, id)
		if err != nil {
			t.Fatalf("%s should have a carried-forward match: %v", id, err)
		}
		if dm.ReceiptID != r.ID || dm.MatchMethod != "recurring" {
			t.Errorf("%s match = receipt %v method %q, want %v/recurring", id, dm.ReceiptID, dm.MatchMethod, r.ID)
		}
	}
}

// TestPropagateRecurring_NotRecurring: two same-amount charges with no subscription receipt
// do not establish (needs >= 3 without a subscription flag). Nothing is flagged or itemized.
func TestPropagateRecurring_NotRecurring(t *testing.T) {
	ctx := context.Background()
	f := newPropagateFixture(t)
	svc, txRepo, merchantRepo, userID := f.svc, f.txRepo, f.merchant, f.userID

	m, err := merchantRepo.Upsert(ctx, "Restaurant", nil, nil, nil)
	if err != nil {
		t.Fatalf("merchant: %v", err)
	}
	seedTxn(t, ctx, txRepo, userID, "t1", &m.ID, 42.00, "2026-04-01")
	seedTxn(t, ctx, txRepo, userID, "t2", &m.ID, 42.00, "2026-05-01")

	svc.PropagateRecurring(ctx, userID)

	for _, id := range []string{"t1", "t2"} {
		got, err := txRepo.FindByTransactionID(ctx, id)
		if err != nil {
			t.Fatalf("find %s: %v", id, err)
		}
		if got.Recurring {
			t.Errorf("%s.Recurring = true, want false (only 2 occurrences, no subscription)", id)
		}
	}
}

// TestPropagateRecurring_Idempotent: running twice does not create duplicate derived matches
// (transaction_id uniqueness + skip-already-matched).
func TestPropagateRecurring_Idempotent(t *testing.T) {
	ctx := context.Background()
	f := newPropagateFixture(t)
	svc, txRepo, merchantRepo, receiptRepo, matchRepo, userID := f.svc, f.txRepo, f.merchant, f.receipts, f.matches, f.userID

	m, err := merchantRepo.Upsert(ctx, "SoundCloud", nil, nil, nil)
	if err != nil {
		t.Fatalf("merchant: %v", err)
	}
	seedTxn(t, ctx, txRepo, userID, "t1", &m.ID, 11.99, "2026-04-24")
	seedTxn(t, ctx, txRepo, userID, "t2", &m.ID, 11.99, "2026-05-24")

	sub := true
	r := &domain.Receipt{UserID: userID, MerchantName: strPtr("SoundCloud"), Source: "email", Status: "matched", LineItems: json.RawMessage("[]"), IsSubscription: &sub}
	if err := receiptRepo.Create(ctx, r); err != nil {
		t.Fatalf("receipt: %v", err)
	}
	if err := matchRepo.Create(ctx, &domain.ReceiptMatch{ReceiptID: r.ID, TransactionID: "t1", ConfidenceScore: 0.95, MatchMethod: "auto"}); err != nil {
		t.Fatalf("match: %v", err)
	}

	svc.PropagateRecurring(ctx, userID)
	svc.PropagateRecurring(ctx, userID)

	var n int
	if err := f.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM receipt_matches WHERE transaction_id = ?", "t2").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("expected exactly 1 match for t2 after two runs, got %d", n)
	}
	_ = receiptRepo
	_ = matchRepo
}
