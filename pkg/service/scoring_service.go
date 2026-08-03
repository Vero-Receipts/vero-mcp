package service

import (
	"context"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
)

// Thresholds above which a dimension counts as "strong" — i.e. it agrees well
// enough to be one of the two dimensions carrying a decision. Below these a
// known dimension is "weak": it disagrees, but within tolerance. A score of
// exactly 0 from scoreAmount/scoreDate means "contradicting" and never reaches
// the caller — ScoreCandidates drops those candidates outright.
const (
	StrongAmount   = 0.85 // ≤5% difference
	StrongDate     = 0.70 // ≤3 days
	StrongMerchant = 0.80 // deterministic exact/substring/compact/alias/core
)

// ScoringService computes deterministic match scores between receipts and
// transactions. The LLM is only consulted for merchant disambiguation when
// the deterministic merchant score is ambiguous.
type ScoringService struct {
	aliasRepo repository.MerchantAliasRepository
}

func NewScoringService(aliasRepo repository.MerchantAliasRepository) *ScoringService {
	return &ScoringService{aliasRepo: aliasRepo}
}

// ScoreCandidates scores every candidate against the receipt and returns
// results sorted by composite score descending.
//
// A dimension the receipt has no value for (unreadable total or date) is left
// unscored and marked unknown rather than guessed at — in particular a missing
// date is NOT stood in for by today, which would score proximity against a
// date the receipt never carried. A dimension that IS known but disagrees
// beyond its cliff (amount past 20%/40% FX, date past 5 days) still drops the
// candidate here: that is the veto, and it is deliberately unchanged.
//
// Merchant is never a drop — the caller tolerates a disagreeing merchant when
// amount and date both hold up.
func (s *ScoringService) ScoreCandidates(ctx context.Context, receipt *domain.Receipt, candidates []domain.Transaction) []domain.CandidateScores {
	receiptAmount, amountKnown := s.effectiveUSD(receipt)
	isNonUSD := s.isNonUSD(receipt)

	var receiptDate time.Time
	dateKnown := receipt.Date != nil
	if dateKnown {
		receiptDate = *receipt.Date
	}

	receiptMerchant := ""
	if receipt.MerchantName != nil {
		receiptMerchant = *receipt.MerchantName
	}
	merchantKnown := receiptMerchant != ""

	if !amountKnown && !dateKnown {
		// Nothing to anchor on: merchant alone can never carry a decision.
		slog.Debug("scorer: skipped — receipt has neither amount nor date", "receipt_id", receipt.ID)
		return nil
	}

	slog.Debug("scorer: starting", "receipt_amount", receiptAmount, "amount_known", amountKnown, "date_known", dateKnown, "is_non_usd", isNonUSD, "candidate_count", len(candidates))

	var scored []domain.CandidateScores
	for _, tx := range candidates {
		cs := domain.CandidateScores{
			TransactionID: tx.TransactionID,
			AmountKnown:   amountKnown,
			DateKnown:     dateKnown,
			MerchantKnown: merchantKnown,
		}

		// --- Amount Score ---
		txAbs := math.Abs(tx.Amount)
		if txAbs == 0 {
			slog.Debug("scorer: dropped — zero amount", "tx_id", tx.TransactionID, "tx_amount", tx.Amount)
			continue
		}
		if amountKnown {
			amountDiffPct := math.Abs(receiptAmount-txAbs) / math.Max(receiptAmount, txAbs) * 100
			// chargeExceeds: bank charged more than the receipt total. Common when
			// fees, surcharges, or other charges are added after the receipt is
			// printed (applies across many merchant types, not just restaurants).
			chargeExceeds := txAbs > receiptAmount
			cs.AmountDiffPct = amountDiffPct
			cs.AmountScore = scoreAmount(amountDiffPct, isNonUSD, chargeExceeds)
			if cs.AmountScore == 0 {
				slog.Debug("scorer: dropped — amount too far", "tx_id", tx.TransactionID, "tx_amount", tx.Amount, "receipt_amount", receiptAmount, "diff_pct", amountDiffPct)
				continue
			}
			if chargeExceeds && amountDiffPct > 5 && amountDiffPct <= 35 {
				cs.ChargeExceedsReceipt = true
			}
		}

		// --- Date Score ---
		if dateKnown {
			txDate, err := time.Parse("2006-01-02", tx.Date)
			if err != nil {
				slog.Debug("scorer: dropped — date parse failed", "tx_id", tx.TransactionID, "tx_date_raw", tx.Date, "error", err)
				continue
			}
			daysDiff := int(math.Abs(receiptDate.Sub(txDate).Hours() / 24))
			cs.DateDiffDays = daysDiff
			cs.DateScore = scoreDate(daysDiff)
			if cs.DateScore == 0 {
				slog.Debug("scorer: dropped — date too far", "tx_id", tx.TransactionID, "tx_date", tx.Date, "days_diff", daysDiff)
				continue
			}
		}

		// --- Merchant Score (deterministic) ---
		txMerchant := ""
		if tx.Merchant != nil {
			txMerchant = tx.Merchant.CanonicalName
		}
		cs.MerchantScore, cs.MerchantMethod = s.scoreMerchant(ctx, receiptMerchant, txMerchant, tx.Name)

		// --- Composite (over known dimensions only) ---
		cs.CompositeScore = cs.Composite()

		slog.Debug("scorer: candidate passed", "tx_id", tx.TransactionID, "amount_score", cs.AmountScore, "date_score", cs.DateScore, "merchant_score", cs.MerchantScore, "composite", cs.CompositeScore)
		scored = append(scored, cs)
	}

	// Sort by composite descending.
	sortCandidateScores(scored)
	return scored
}

// effectiveUSD returns the best USD amount for comparison, and whether the
// receipt carries a usable total at all.
func (s *ScoringService) effectiveUSD(receipt *domain.Receipt) (float64, bool) {
	if receipt.TotalUSD != nil && *receipt.TotalUSD > 0 {
		return *receipt.TotalUSD, true
	}
	if receipt.Total != nil && *receipt.Total > 0 {
		return *receipt.Total, true
	}
	return 0, false
}

func (s *ScoringService) isNonUSD(receipt *domain.Receipt) bool {
	if receipt.TotalUSD != nil && *receipt.TotalUSD > 0 {
		return false // we have USD equivalent
	}
	return receipt.Currency != nil && *receipt.Currency != "" && !strings.EqualFold(*receipt.Currency, "USD")
}

// scoreAmount returns 0-1 based on percentage difference.
// chargeExceeds indicates the transaction amount exceeds the receipt total —
// a common pattern when fees or other charges are applied after receipt
// printing. A gentler penalty is applied to keep these candidates visible.
func scoreAmount(pctDiff float64, isNonUSD bool, chargeExceeds bool) float64 {
	maxPct := 20.0
	if isNonUSD {
		maxPct = 40.0 // wider FX tolerance
	}

	// When the bank charged more than the receipt, apply a floor so the
	// candidate is not silently dropped before merchant/date scoring runs.
	if chargeExceeds && pctDiff > 5 {
		switch {
		case pctDiff <= 20:
			return 0.75
		case pctDiff <= 35:
			return 0.55
		default:
			return 0 // DROP — too large to be a realistic tip
		}
	}

	switch {
	case pctDiff <= 0.5:
		return 1.0
	case pctDiff <= 2:
		return 0.95
	case pctDiff <= 5:
		return 0.85
	case pctDiff <= 10:
		return 0.70
	case pctDiff <= 15:
		return 0.55
	case pctDiff <= maxPct:
		return 0.40
	default:
		return 0 // DROP
	}
}

// scoreDate returns 0-1 based on days difference.
func scoreDate(days int) float64 {
	switch {
	case days == 0:
		return 1.0
	case days == 1:
		return 0.95
	case days == 2:
		return 0.85
	case days == 3:
		return 0.70
	case days <= 5:
		return 0.50
	default:
		return 0 // DROP
	}
}

// scoreMerchant deterministically scores merchant similarity.
// Returns (score, method) where method describes how the match was made.
func (s *ScoringService) scoreMerchant(ctx context.Context, receiptMerchant, txMerchant, txName string) (float64, string) {
	if receiptMerchant == "" {
		return 0, "none"
	}

	normReceipt := NormalizeMerchant(receiptMerchant)
	normTxMerchant := NormalizeMerchant(txMerchant)
	normTxName := NormalizeMerchant(txName)

	// Try matching against both txMerchant and txName, take the best.
	score1, method1 := s.scoreMerchantPair(ctx, normReceipt, normTxMerchant)
	score2, method2 := s.scoreMerchantPair(ctx, normReceipt, normTxName)

	if score1 >= score2 {
		return score1, method1
	}
	return score2, method2
}

func (s *ScoringService) scoreMerchantPair(ctx context.Context, a, b string) (float64, string) {
	if a == "" || b == "" {
		return 0, "none"
	}

	// 1. Exact match after normalization.
	if a == b {
		return 1.0, "exact"
	}

	// 2. One contains the other.
	if strings.Contains(a, b) || strings.Contains(b, a) {
		return 0.90, "substring"
	}

	// 2b. Compact match: strip spaces and compare. Handles Plaid canonical names
	// stored without spaces (e.g. "Parlordoughnuts" vs "parlor doughnuts nashville sobro").
	compactA := strings.ReplaceAll(a, " ", "")
	compactB := strings.ReplaceAll(b, " ", "")
	if strings.Contains(compactA, compactB) || strings.Contains(compactB, compactA) {
		return 0.85, "compact"
	}

	// 3. Check alias cache.
	if s.aliasRepo != nil {
		same, err := s.aliasRepo.AreSameMerchant(ctx, a, b)
		if err == nil && same {
			return 0.92, "alias"
		}
	}

	// 4. Levenshtein distance on core words.
	coreA := extractCoreWord(a)
	coreB := extractCoreWord(b)
	if coreA != "" && coreB != "" {
		if coreA == coreB {
			return 0.88, "core_exact"
		}
		dist := levenshtein(coreA, coreB)
		maxLen := max(len(coreA), len(coreB))
		if maxLen > 0 && dist <= 2 && float64(dist)/float64(maxLen) < 0.3 {
			return 0.75, "levenshtein"
		}
	}

	// 5. Multi-word overlap (2+ words).
	overlapCount := meaningfulWordOverlap(a, b)
	if overlapCount >= 2 {
		return 0.70, "word_overlap_2"
	}
	if overlapCount == 1 {
		return 0.45, "word_overlap_1" // ambiguous — needs LLM
	}

	return 0, "none"
}

// NormalizeMerchant cleans a merchant string for comparison.
func NormalizeMerchant(s string) string {
	s = strings.ToLower(s)

	// Strip common POS prefixes.
	prefixes := []string{
		"sq *", "sq*", "tst*", "tst *", "aplpay ", "py *",
		"sp *", "sp*", "pp*", "pp *", "zettle_",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			s = s[len(p):]
			break
		}
	}

	// Remove non-alphanumeric except spaces.
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			return r
		}
		return ' '
	}, s)

	// Remove stop words.
	words := strings.Fields(s)
	var filtered []string
	for _, w := range words {
		if !MerchantStopWords[w] {
			filtered = append(filtered, w)
		}
	}
	return strings.Join(filtered, " ")
}

// extractCoreWord returns the longest meaningful word — the "brand" word.
func extractCoreWord(normalized string) string {
	words := strings.Fields(normalized)
	best := ""
	for _, w := range words {
		if len(w) > len(best) && !MerchantStopWords[w] {
			best = w
		}
	}
	return best
}

// meaningfulWordOverlap returns how many meaningful words are shared.
func meaningfulWordOverlap(a, b string) int {
	wordsA := make(map[string]bool)
	for _, w := range strings.Fields(a) {
		if len(w) >= 3 && !MerchantStopWords[w] {
			wordsA[w] = true
		}
	}
	count := 0
	for _, w := range strings.Fields(b) {
		if len(w) >= 3 && !MerchantStopWords[w] && wordsA[w] {
			count++
		}
	}
	return count
}

var MerchantStopWords = map[string]bool{
	"the": true, "inc": true, "llc": true, "corp": true, "ltd": true,
	"co": true, "company": true, "group": true, "intl": true, "international": true,
	"aplpay": true, "sq": true, "tst": true, "py": true, "pp": true,
	"a": true, "an": true, "of": true, "and": true, "or": true, "for": true,
	"in": true, "at": true, "to": true, "on": true, "by": true,
	"store": true, "shop": true, "market": true, "restaurant": true,
	"cafe": true, "bar": true, "grill": true,
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func sortCandidateScores(s []domain.CandidateScores) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].CompositeScore > s[j-1].CompositeScore; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
