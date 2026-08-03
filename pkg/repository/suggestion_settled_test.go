package repository

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// A receipt already settled against one transaction must never be offered as a
// suggestion for a different one.
//
// Four code paths create a settled match — auto, confirmed, manual and
// recurring — and only the first two clear the receipt's proposals. Rather
// than trust every present and future writer to remember, the read side
// enforces the invariant: a receipt with any settled match has no pending
// proposals, full stop.
func TestSuggestions_SettledReceiptIsNeverProposed(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	userRepo := NewUserRepo(db, DialectSQLite)
	receiptRepo := NewReceiptRepo(db, DialectSQLite)
	txRepo := NewTransactionCacheRepo(db, DialectSQLite)
	suggestions := NewReceiptMatchSuggestionRepo(db, DialectSQLite)
	matches := NewReceiptMatchRepo(db, DialectSQLite)

	user := &domain.User{Name: "Test"}
	if err := userRepo.Upsert(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	receipt := &domain.Receipt{
		UserID: user.ID, Source: "upload", Status: "unmatched",
		LineItems: json.RawMessage("[]"),
	}
	if err := receiptRepo.Create(ctx, receipt); err != nil {
		t.Fatalf("create receipt: %v", err)
	}

	if _, err := txRepo.UpsertBatch(ctx, user.ID, []domain.Transaction{
		{TransactionID: "txn_settled", AccountID: "acct_1", Amount: 10, Date: "2025-03-15", Name: "Settled", Category: json.RawMessage("[]")},
		{TransactionID: "txn_other", AccountID: "acct_1", Amount: 10, Date: "2025-03-15", Name: "Other", Category: json.RawMessage("[]")},
	}); err != nil {
		t.Fatalf("seed transactions: %v", err)
	}

	// The receipt is proposed for txn_other...
	if err := suggestions.ReplaceForReceipt(ctx, receipt.ID, []domain.ReceiptMatchSuggestion{{
		UserID: user.ID, ReceiptID: receipt.ID, TransactionID: "txn_other",
		CompositeScore: 0.72, Flag: domain.FlagMerchantMismatch, Rank: 1,
	}}); err != nil {
		t.Fatalf("seed suggestion: %v", err)
	}

	// ...and then settled against txn_settled by a path that does not clear
	// proposals — a manual link here; recurring propagation behaves the same.
	if err := matches.Create(ctx, &domain.ReceiptMatch{
		ReceiptID: receipt.ID, TransactionID: "txn_settled",
		ConfidenceScore: 1.0, MatchMethod: "manual",
	}); err != nil {
		t.Fatalf("settle receipt: %v", err)
	}

	forTxn, err := suggestions.FindByTransactionID(ctx, "txn_other")
	if err != nil {
		t.Fatalf("FindByTransactionID: %v", err)
	}
	if len(forTxn) != 0 {
		t.Errorf("txn_other offers a receipt that is already matched elsewhere: %+v", forTxn)
	}

	forReceipt, err := suggestions.FindByReceiptID(ctx, receipt.ID)
	if err != nil {
		t.Fatalf("FindByReceiptID: %v", err)
	}
	if len(forReceipt) != 0 {
		t.Errorf("a settled receipt still lists %d pending proposal(s)", len(forReceipt))
	}

	if _, err := suggestions.FindPair(ctx, receipt.ID, "txn_other"); err == nil {
		t.Error("FindPair resolved a proposal on a settled receipt; confirm/reject would act on it")
	}

	count, err := suggestions.CountPendingByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("CountPendingByUser: %v", err)
	}
	if count != 0 {
		t.Errorf("review queue = %d, want 0 — the receipt is settled", count)
	}
}
