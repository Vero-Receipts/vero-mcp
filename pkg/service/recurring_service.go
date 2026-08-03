package service

import (
	"context"
	"log/slog"
	"sort"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// Recurring-detection tuning knobs.
const (
	// recurringAmountTolerance is the ± band on amount within which two charges count as
	// "the same" recurring amount. Subscriptions are fixed-price, so keep it tight.
	recurringAmountTolerance = 0.02
	// recurringMinPatternCount is how many occurrences establish recurrence WITHOUT a
	// subscription receipt to confirm it. With an is_subscription source, 2 is enough.
	recurringMinPatternCount = 3
	// recurringDuplicateWindowDays collapses charges within this many days of the previous
	// occurrence into one — they're duplicates (the same real charge re-imported after an
	// account re-link, with a fresh transaction_id) or a same-period double charge, not a new
	// cycle. It's below the smallest cadence bucket (weekly), so no real cadence is affected.
	recurringDuplicateWindowDays = 5
)

// frequencyBucket is an allowed cadence expressed as an inclusive day range (nominal ±
// tolerance): weekly 7±2, biweekly 14±3, monthly 30±5, annual 365±15.
type frequencyBucket struct {
	name   string
	lo, hi int
}

var frequencyBuckets = []frequencyBucket{
	{"weekly", 5, 9},
	{"biweekly", 11, 17},
	{"monthly", 25, 35},
	{"annual", 350, 380},
}

func snapToBucket(gapDays int) (string, bool) {
	for _, b := range frequencyBuckets {
		if gapDays >= b.lo && gapDays <= b.hi {
			return b.name, true
		}
	}
	return "", false
}

// ItemizeTarget is a derived (carried-forward) match to create: link ReceiptID to
// TransactionID with match_method='recurring'.
type ItemizeTarget struct {
	TransactionID string
	ReceiptID     uuid.UUID
}

// SeriesReport describes one detected recurring series (a single merchant + amount cluster).
// Used both to apply changes and to report them (e.g. a one-time re-analysis over existing data).
type SeriesReport struct {
	MerchantID    uuid.UUID
	MerchantName  string
	Count         int
	Amount        float64
	Cadence       string     // frequency bucket name, or "irregular"
	SourceReceipt *uuid.UUID // earliest real (non-derived) receipt in the series, if any
	SourceIsSub   *bool      // that receipt's is_subscription (nil = not yet evaluated)
	Established   bool       // qualifies to be marked recurring now, with known data
	// NeedsOCR is true for a not-yet-established (2-occurrence) series whose source receipt's
	// subscription flag is unknown — OCR decides whether it establishes. A ≥3-occurrence series
	// is established by pattern alone and never needs OCR.
	NeedsOCR bool
	Flag     []string        // transaction ids to mark recurring (when Established)
	Itemize  []ItemizeTarget // derived matches to create (when Established and a source exists)
}

// AnalyzeRecurring groups a user's candidates into per-merchant, per-amount series and
// evaluates each. Pure — no I/O. Callers apply Flag/Itemize, and may inspect
// NeedsOCR/SourceReceipt to decide which source receipts to re-OCR.
func AnalyzeRecurring(candidates []domain.RecurringCandidate) []SeriesReport {
	byMerchant := map[uuid.UUID][]domain.RecurringCandidate{}
	for _, c := range candidates {
		byMerchant[c.MerchantID] = append(byMerchant[c.MerchantID], c)
	}

	var reports []SeriesReport
	for _, members := range byMerchant {
		for _, cluster := range clusterByAmount(members) {
			if r, ok := analyzeCluster(cluster); ok {
				reports = append(reports, r)
			}
		}
	}
	return reports
}

// analyzeCluster evaluates one same-merchant, same-amount cluster. It returns ok=false for
// clusters that are neither a recurring series nor a candidate awaiting OCR (i.e. not worth
// reporting or acting on).
func analyzeCluster(cluster []domain.RecurringCandidate) (SeriesReport, bool) {
	if len(cluster) < 2 {
		return SeriesReport{}, false
	}
	sort.Slice(cluster, func(i, j int) bool { return cluster[i].Date < cluster[j].Date })

	// Cadence and occurrence count are computed over DISTINCT occurrences so duplicate charges
	// (e.g. the same charge re-imported after an account re-link) don't inject ~0-day gaps that
	// break the cadence, or inflate the count.
	occurrences := distinctOccurrences(cluster)
	cadence, cadenceOK := cadenceOf(occurrences)
	if !cadenceOK {
		return SeriesReport{}, false
	}

	// Earliest real source receipt (cluster is date-ascending) and whether any real source
	// is flagged as a subscription.
	var source *uuid.UUID
	var srcIsSub *bool
	hasSubReceipt := false
	for _, m := range cluster {
		if m.SourceReceipt != nil {
			if source == nil {
				source = m.SourceReceipt
				srcIsSub = m.IsSubscription
			}
			if m.IsSubscription != nil && *m.IsSubscription {
				hasSubReceipt = true
			}
		}
	}

	established := hasSubReceipt || len(occurrences) >= recurringMinPatternCount
	// A ≥3-occurrence, same-amount, regular-cadence series is strong enough on its own — real
	// non-subscription spend (restaurants, rideshare, gas) doesn't produce the identical bill
	// repeatedly, so the amount band + cadence already filter it out. OCR is therefore only
	// needed to rescue a 2-occurrence series, where the pattern alone is too weak and a
	// subscription receipt is what establishes it.
	needsOCR := !established && source != nil && srcIsSub == nil
	if !established && !needsOCR {
		return SeriesReport{}, false
	}

	r := SeriesReport{
		MerchantID:    cluster[0].MerchantID,
		MerchantName:  cluster[0].MerchantName,
		Count:         len(cluster),
		Amount:        cluster[0].Amount,
		Cadence:       cadence,
		SourceReceipt: source,
		SourceIsSub:   srcIsSub,
		Established:   established,
		NeedsOCR:      needsOCR,
	}
	if established {
		for _, m := range cluster {
			if !m.Recurring {
				r.Flag = append(r.Flag, m.TransactionID)
			}
		}
		// Carry the series' source receipt forward to its bare charges. For an established
		// series (subscription receipt at ≥2, or ≥3 occurrences on a regular cadence) the
		// recurring charges are the same thing each period, so the earlier receipt's items apply.
		if source != nil {
			for _, m := range cluster {
				if !m.Matched {
					r.Itemize = append(r.Itemize, ItemizeTarget{TransactionID: m.TransactionID, ReceiptID: *source})
				}
			}
		}
	}
	return r, true
}

// evaluateSeries is a thin wrapper over analyzeCluster returning just the actionable plan
// for an established series (nil,nil otherwise). Retained for direct unit testing.
func evaluateSeries(cluster []domain.RecurringCandidate) ([]string, []ItemizeTarget) {
	r, ok := analyzeCluster(cluster)
	if !ok || !r.Established {
		return nil, nil
	}
	return r.Flag, r.Itemize
}

// distinctOccurrences returns the dates of a (date-ascending) cluster's distinct billing
// occurrences, collapsing any member within recurringDuplicateWindowDays of the previous kept
// occurrence — duplicates or same-period double charges. All members stay in the cluster for
// flagging/itemization; this only feeds the cadence and occurrence-count checks.
func distinctOccurrences(sorted []domain.RecurringCandidate) []string {
	var occ []string
	for _, m := range sorted {
		if len(occ) == 0 || dayGap(occ[len(occ)-1], m.Date) >= recurringDuplicateWindowDays {
			occ = append(occ, m.Date)
		}
	}
	return occ
}

// cadenceOf returns the (first) frequency bucket for date-ascending distinct occurrences, and
// false if there are fewer than two occurrences or any consecutive gap does not snap to a bucket.
func cadenceOf(occ []string) (string, bool) {
	if len(occ) < 2 {
		return "irregular", false
	}
	name := ""
	for i := 1; i < len(occ); i++ {
		b, ok := snapToBucket(dayGap(occ[i-1], occ[i]))
		if !ok {
			return "irregular", false
		}
		if name == "" {
			name = b
		}
	}
	if name == "" {
		return "irregular", false
	}
	return name, true
}

// PropagateRecurring detects recurring series among a user's transactions and (a) flags
// every established member as recurring (drives the badge) and (b) carries the series' source
// receipt forward to any bare members (itemization). Runs in the background after a sync;
// best-effort — errors are logged, not returned. Series awaiting OCR (NeedsOCR) are left for
// a dedicated re-OCR pass.
func (s *ReceiptService) PropagateRecurring(ctx context.Context, userID uuid.UUID) {
	candidates, err := s.txCacheRepo.FindRecurringCandidates(ctx, userID)
	if err != nil {
		slog.Error("recurring: find candidates", "error", err, "user_id", userID)
		return
	}
	if len(candidates) == 0 {
		return
	}

	var toFlag []string
	var toItemize []ItemizeTarget
	for _, r := range AnalyzeRecurring(candidates) {
		toFlag = append(toFlag, r.Flag...)
		toItemize = append(toItemize, r.Itemize...)
	}

	if len(toFlag) > 0 {
		if err := s.txCacheRepo.SetRecurring(ctx, toFlag); err != nil {
			slog.Error("recurring: set flag", "error", err, "user_id", userID)
		}
	}
	for _, t := range toItemize {
		m := &domain.ReceiptMatch{
			ReceiptID:       t.ReceiptID,
			TransactionID:   t.TransactionID,
			ConfidenceScore: 1.0,
			MatchMethod:     "recurring",
			MatchReason:     "carried forward from recurring source receipt",
		}
		if err := s.matchRepo.Create(ctx, m); err != nil {
			slog.Error("recurring: create derived match", "error", err, "transaction_id", t.TransactionID)
			continue
		}
		// The transaction now carries line items, so nothing should still be
		// proposing a receipt for it.
		if s.suggestionRepo != nil {
			if err := s.suggestionRepo.DeleteForTransaction(ctx, t.TransactionID); err != nil {
				slog.Error("recurring: clear transaction suggestions", "error", err, "transaction_id", t.TransactionID)
			}
		}
	}
	if len(toFlag) > 0 || len(toItemize) > 0 {
		slog.Info("recurring: propagated", "user_id", userID, "flagged", len(toFlag), "itemized", len(toItemize))
	}
}

// clusterByAmount partitions same-merchant members into groups whose amounts fall within the
// tolerance band, so a merchant's $10 subscription and its $50 one-off purchases are not
// treated as one series.
func clusterByAmount(members []domain.RecurringCandidate) [][]domain.RecurringCandidate {
	sorted := append([]domain.RecurringCandidate{}, members...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Amount < sorted[j].Amount })

	var clusters [][]domain.RecurringCandidate
	var cur []domain.RecurringCandidate
	var anchor float64
	for _, m := range sorted {
		if len(cur) == 0 {
			cur = []domain.RecurringCandidate{m}
			anchor = m.Amount
			continue
		}
		if withinBand(anchor, m.Amount) {
			cur = append(cur, m)
		} else {
			clusters = append(clusters, cur)
			cur = []domain.RecurringCandidate{m}
			anchor = m.Amount
		}
	}
	if len(cur) > 0 {
		clusters = append(clusters, cur)
	}
	return clusters
}

func withinBand(a, b float64) bool {
	if a == 0 {
		return b == 0
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff <= a*recurringAmountTolerance
}

func dayGap(a, b string) int {
	ta, _ := time.Parse("2006-01-02", a)
	tb, _ := time.Parse("2006-01-02", b)
	d := int(tb.Sub(ta).Hours() / 24)
	if d < 0 {
		d = -d
	}
	return d
}
