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

	t.Run("pattern of 3 with a source itemizes regardless of subscription flag", func(t *testing.T) {
		// A ≥3-occurrence, same-amount, regular-cadence series is established by pattern alone,
		// so its source receipt is carried forward even without a subscription flag. (Real
		// non-subscription spend doesn't repeat the identical bill, so the amount band filters it.)
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 12.00, "2026-03-10", false, true, &rid, &fal), // receipt, is_sub=false
			mkCand("t2", 12.00, "2026-04-10", false, false, nil, nil),
			mkCand("t3", 12.00, "2026-05-10", false, false, nil, nil),
		}
		flag, itemize := evaluateSeries(cluster)
		if len(flag) != 3 {
			t.Fatalf("flag = %v, want all 3 marked recurring", flag)
		}
		if len(itemize) != 2 {
			t.Fatalf("itemize = %v, want t2 and t3 carried from the source", itemize)
		}
	})

	t.Run("pattern of 3 with an unevaluated source itemizes without needing OCR", func(t *testing.T) {
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 12.00, "2026-03-10", false, true, &rid, nil), // receipt, is_sub unknown
			mkCand("t2", 12.00, "2026-04-10", false, false, nil, nil),
			mkCand("t3", 12.00, "2026-05-10", false, false, nil, nil),
		}
		r, ok := analyzeCluster(cluster)
		if !ok {
			t.Fatal("expected the series to be reported")
		}
		if !r.Established {
			t.Errorf("Established = false, want true (pattern >= 3)")
		}
		if r.NeedsOCR {
			t.Errorf("NeedsOCR = true, want false (>=3 is established by pattern, no OCR needed)")
		}
		if len(r.Itemize) != 2 {
			t.Errorf("Itemize = %v, want t2 and t3", r.Itemize)
		}
	})

	t.Run("duplicate charges (0-day gaps) don't break cadence and are all flagged/itemized", func(t *testing.T) {
		// Same real charge re-imported after a re-link: Apr and May each appear twice. The
		// duplicates collapse for cadence (→ clean monthly), and every member row is flagged
		// and the bare ones itemized.
		cluster := []domain.RecurringCandidate{
			mkCand("apr_a", 11.99, "2026-04-24", false, true, &rid, &tru), // source (real match)
			mkCand("apr_b", 11.99, "2026-04-24", false, false, nil, nil),  // duplicate of apr
			mkCand("may_a", 11.99, "2026-05-24", false, false, nil, nil),
			mkCand("may_b", 11.99, "2026-05-24", false, false, nil, nil), // duplicate of may
			mkCand("jun", 11.99, "2026-06-24", false, false, nil, nil),
		}
		flag, itemize := evaluateSeries(cluster)
		if len(flag) != 5 {
			t.Fatalf("flag = %v, want all 5 members marked recurring", flag)
		}
		if len(itemize) != 4 { // every member except the matched source
			t.Fatalf("itemize = %d targets, want 4 bare members carried from source", len(itemize))
		}
	})

	t.Run("irregular cadence is rejected", func(t *testing.T) {
		cluster := []domain.RecurringCandidate{
			mkCand("t1", 10.00, "2026-05-01", false, false, nil, nil),
			mkCand("t2", 10.00, "2026-05-21", false, false, nil, nil), // 20-day gap: no bucket
			mkCand("t3", 10.00, "2026-06-10", false, false, nil, nil),
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
