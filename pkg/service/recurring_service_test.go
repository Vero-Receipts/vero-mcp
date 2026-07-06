package service

import (
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

func TestSnapToBucket(t *testing.T) {
	cases := []struct {
		gap  int
		want bool
	}{
		{7, true},   // weekly
		{9, true},   // weekly edge
		{3, false},  // too tight (same-week noise)
		{14, true},  // biweekly
		{20, false}, // between biweekly and monthly
		{30, true},  // monthly
		{35, true},  // monthly edge
		{60, false}, // skipped month — not a single bucket
		{365, true}, // annual
	}
	for _, c := range cases {
		if _, ok := snapToBucket(c.gap); ok != c.want {
			t.Errorf("snapToBucket(%d) = %v, want %v", c.gap, ok, c.want)
		}
	}
}

func mkCand(txID string, amount float64, date string, recurring, matched bool, source *uuid.UUID, isSub *bool) domain.RecurringCandidate {
	return domain.RecurringCandidate{
		TransactionID:  txID,
		MerchantID:     uuid.Nil,
		Date:           date,
		Amount:         amount,
		Recurring:      recurring,
		Matched:        matched,
		SourceReceipt:  source,
		IsSubscription: isSub,
	}
}

func TestEvaluateSeries(t *testing.T) {
	rid := uuid.New()
	tru := true
	fal := false

	t.Run("subscription receipt establishes at 2 occurrences and itemizes the bare one", func(t *testing.T) {
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 11.99, "2026-04-24", false, true, &rid, &tru), // source: real sub receipt
			mkCand("t2", 11.99, "2026-05-24", false, false, nil, nil),  // bare renewal
		}
		flag, itemize := evaluateSeries(cluster)
		if len(flag) != 2 {
			t.Fatalf("flag = %v, want both marked recurring", flag)
		}
		if len(itemize) != 1 || itemize[0].TransactionID != "t2" || itemize[0].ReceiptID != rid {
			t.Fatalf("itemize = %+v, want t2 <- source receipt", itemize)
		}
	})

	t.Run("no subscription receipt: 2 occurrences do NOT establish", func(t *testing.T) {
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 50.00, "2026-04-01", false, true, &rid, &fal), // real receipt, not a subscription
			mkCand("t2", 50.00, "2026-05-01", false, false, nil, nil),
		}
		flag, itemize := evaluateSeries(cluster)
		if flag != nil || itemize != nil {
			t.Fatalf("expected not established; got flag=%v itemize=%v", flag, itemize)
		}
	})

	t.Run("pattern of 3 establishes without a subscription receipt", func(t *testing.T) {
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 10.00, "2026-03-17", false, false, nil, nil),
			mkCand("t2", 10.00, "2026-04-17", false, false, nil, nil),
			mkCand("t3", 10.00, "2026-05-17", false, false, nil, nil),
		}
		flag, itemize := evaluateSeries(cluster)
		if len(flag) != 3 {
			t.Fatalf("flag = %v, want all 3 marked recurring", flag)
		}
		if len(itemize) != 0 {
			t.Fatalf("itemize = %v, want none (no source receipt)", itemize)
		}
	})

	t.Run("irregular cadence is rejected", func(t *testing.T) {
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 10.00, "2026-05-01", false, false, nil, nil),
			mkCand("t2", 10.00, "2026-05-03", false, false, nil, nil), // 2-day gap
			mkCand("t3", 10.00, "2026-06-02", false, false, nil, nil),
		}
		flag, _ := evaluateSeries(cluster)
		if flag != nil {
			t.Fatalf("expected rejected on cadence; got flag=%v", flag)
		}
	})

	t.Run("already-recurring member is not re-flagged", func(t *testing.T) {
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 11.99, "2026-04-24", true, true, &rid, &tru),
			mkCand("t2", 11.99, "2026-05-24", false, false, nil, nil),
		}
		flag, _ := evaluateSeries(cluster)
		if len(flag) != 1 || flag[0] != "t2" {
			t.Fatalf("flag = %v, want only t2", flag)
		}
	})
}

func TestClusterByAmount(t *testing.T) {
	members := []domain.RecurringCandidate{
		mkCand("a", 10.00, "2026-01-01", false, false, nil, nil),
		mkCand("b", 10.10, "2026-02-01", false, false, nil, nil), // within 2% of 10.00
		mkCand("c", 50.00, "2026-01-15", false, false, nil, nil), // separate cluster
	}
	clusters := clusterByAmount(members)
	if len(clusters) != 2 {
		t.Fatalf("got %d clusters, want 2", len(clusters))
	}
}
