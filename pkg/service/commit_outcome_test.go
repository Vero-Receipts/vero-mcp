package service

import (
	"context"
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

// stubMarkRepo implements only the one receipt-repo method these paths reach.
// The embedded interface is nil, so any other call panics rather than passing
// silently — reaching one would mean the test exercised a path it does not mean to.
type stubMarkRepo struct {
	repository.ReceiptRepository
}

func (s *stubMarkRepo) MarkMatchAttempted(context.Context, uuid.UUID, *time.Time) error {
	return nil
}

// recordingSuggestionRepo counts the writes commitOutcome makes. Every read
// method is unreachable on the paths under test and says so if it is reached.
type recordingSuggestionRepo struct {
	replaceCalls int
	lastRows     []domain.ReceiptMatchSuggestion
}

func (r *recordingSuggestionRepo) ReplaceForReceipt(_ context.Context, _ uuid.UUID, rows []domain.ReceiptMatchSuggestion) error {
	r.replaceCalls++
	r.lastRows = rows
	return nil
}
func (r *recordingSuggestionRepo) FindByReceiptID(context.Context, uuid.UUID) ([]domain.ReceiptMatchSuggestion, error) {
	return nil, nil
}
func (r *recordingSuggestionRepo) FindByTransactionID(context.Context, string) ([]domain.ReceiptMatchSuggestion, error) {
	return nil, nil
}
func (r *recordingSuggestionRepo) FindPair(context.Context, uuid.UUID, string) (*domain.ReceiptMatchSuggestion, error) {
	return nil, nil
}
func (r *recordingSuggestionRepo) MarkRejected(context.Context, uuid.UUID, string) error { return nil }
func (r *recordingSuggestionRepo) DeleteForReceipt(context.Context, uuid.UUID) error     { return nil }
func (r *recordingSuggestionRepo) DeleteForTransaction(context.Context, string) error    { return nil }
func (r *recordingSuggestionRepo) CountPendingByUser(context.Context, uuid.UUID) (int, error) {
	return 0, nil
}

// A skipped receipt and a receipt with nothing to suggest look identical from
// the outside — both carry no Auto and no Suggestions — but they mean opposite
// things. "Nothing supports these any more" clears the receipt's proposals;
// "I did not look" must leave them exactly where they are.
func TestCommitOutcome_SkipLeavesSuggestionsIntact(t *testing.T) {
	sugg := &recordingSuggestionRepo{}
	svc := &ReceiptService{suggestionRepo: sugg}

	receipt := &domain.Receipt{ID: uuid.New()}
	settled := svc.commitOutcome(context.Background(), uuid.New(), receipt, &MatchOutcome{Unchanged: true})

	if settled {
		t.Error("a skipped receipt settles nothing")
	}
	if sugg.replaceCalls != 0 {
		t.Errorf("ReplaceForReceipt called %d times, want 0 — the skip must not touch live proposals", sugg.replaceCalls)
	}
	if receipt.MatchAttemptedAt != nil {
		t.Error("a skipped receipt reached no verdict, so its stamp must not move")
	}
}

// The contrast case: the pipeline ran and found nothing, which is a real verdict
// and does clear whatever the last run had proposed.
func TestCommitOutcome_EmptyOutcomeClearsSuggestions(t *testing.T) {
	sugg := &recordingSuggestionRepo{}
	svc := &ReceiptService{suggestionRepo: sugg, receiptRepo: &stubMarkRepo{}}

	receipt := &domain.Receipt{ID: uuid.New()}
	svc.commitOutcome(context.Background(), uuid.New(), receipt, &MatchOutcome{})

	if sugg.replaceCalls != 1 {
		t.Errorf("ReplaceForReceipt called %d times, want 1", sugg.replaceCalls)
	}
	if len(sugg.lastRows) != 0 {
		t.Errorf("wrote %d rows, want 0", len(sugg.lastRows))
	}
	if receipt.MatchAttemptedAt == nil {
		t.Error("the pipeline reached a verdict, so the stamp must advance")
	}
}
