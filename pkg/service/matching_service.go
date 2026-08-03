package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

type MatchingService struct {
	txCacheRepo repository.TransactionCacheRepository
	aliasRepo   repository.MerchantAliasRepository
	auditRepo   repository.MatchAuditRepository
	scoringSvc  *ScoringService
	openAISvc   *OpenAIService
}

func NewMatchingService(
	txCacheRepo repository.TransactionCacheRepository,
	aliasRepo repository.MerchantAliasRepository,
	auditRepo repository.MatchAuditRepository,
	scoringSvc *ScoringService,
	openAISvc *OpenAIService,
) *MatchingService {
	return &MatchingService{
		txCacheRepo: txCacheRepo,
		aliasRepo:   aliasRepo,
		auditRepo:   auditRepo,
		scoringSvc:  scoringSvc,
		openAISvc:   openAISvc,
	}
}

// MatchResult is the outcome of the deterministic-first matching pipeline.
type MatchResult struct {
	TransactionID string
	AccountID     string
	Confidence    float64
	MatchType     string // "matched" or "suggested"
	Reason        string
	Flag          string
	Scores        domain.CandidateScores
	LLMUsed       bool
	Transaction   *domain.Transaction // the matched transaction, for downstream use
}

// MatchOutcome is what a receipt resolves to. At most one of the two is
// populated: a candidate good enough to link automatically short-circuits the
// search, otherwise every candidate worth a human decision is returned ranked.
type MatchOutcome struct {
	Auto        *MatchResult
	Suggestions []MatchResult
}

// maxSuggestions caps how many proposals a single receipt may raise. Beyond a
// handful the review is worse than no suggestion at all.
const maxSuggestions = 3

// suggestionCandidateLimit is how many scored candidates are evaluated before
// giving up. Higher than the auto-match path needs because a receipt missing a
// dimension casts a wider net.
const suggestionCandidateLimit = 8

// FindAllUnmatchedTransactions returns all transactions not yet matched to a receipt.
func (s *MatchingService) FindAllUnmatchedTransactions(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error) {
	return s.txCacheRepo.FindAllUnmatched(ctx, userID)
}

// FindCandidatesForReceipt returns unmatched transactions whose amount is within
// ±10% of the receipt total and whose date is within ±5 days of the receipt date.
func (s *MatchingService) FindCandidatesForReceipt(ctx context.Context, userID uuid.UUID, total float64, dateStr string) ([]domain.Transaction, error) {
	if total <= 0 {
		return nil, nil
	}
	return s.txCacheRepo.FindUnmatchedCandidates(ctx, userID, total, dateStr)
}

// FindCandidatesByDateRange returns unmatched transactions within ±3 days.
func (s *MatchingService) FindCandidatesByDateRange(ctx context.Context, userID uuid.UUID, dateStr string) ([]domain.Transaction, error) {
	return s.txCacheRepo.FindUnmatchedByDateRange(ctx, userID, dateStr)
}

// MatchReceipt is the unified matching pipeline:
//  1. DB pre-filter, anchored on whichever dimensions the receipt actually has
//  2. Deterministic scoring (amount, date, merchant)
//  3. Merchant alias cache check
//  4. LLM merchant disambiguation (only if ambiguous)
//  5. Decision rule → auto-match, suggest, or skip
//  6. Cache new merchant alias if LLM confirmed
//  7. Audit log
//
// A receipt resolves to at most one auto-match, or to a ranked set of
// suggestions for the user to decide on.
func (s *MatchingService) MatchReceipt(ctx context.Context, userID uuid.UUID, receipt *domain.Receipt) (*MatchOutcome, error) {
	candidates, err := s.findCandidates(ctx, userID, receipt)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	slog.Info("match-pipeline: candidates found",
		"receipt_id", receipt.ID, "count", len(candidates))

	// --- Stage 2: Deterministic Scoring ---
	scored := s.scoringSvc.ScoreCandidates(ctx, receipt, candidates)
	if len(scored) == 0 {
		slog.Debug("match-pipeline: all candidates dropped by scorer",
			"receipt_id", receipt.ID, "candidate_count", len(candidates))
		return nil, nil
	}

	// --- Stage 3 & 4: Evaluate top candidates ---
	limit := suggestionCandidateLimit
	if len(scored) < limit {
		limit = len(scored)
	}

	outcome := &MatchOutcome{}
	for _, cs := range scored[:limit] {
		// Find the transaction object.
		var tx *domain.Transaction
		for i := range candidates {
			if candidates[i].TransactionID == cs.TransactionID {
				tx = &candidates[i]
				break
			}
		}
		if tx == nil {
			continue
		}

		result, err := s.evaluateCandidate(ctx, receipt, tx, &cs)
		if err != nil {
			slog.Error("match-pipeline: evaluate candidate", "error", err,
				"receipt_id", receipt.ID, "tx_id", cs.TransactionID)
			continue
		}
		if result == nil {
			continue
		}

		// A candidate good enough to link outright ends the search — it is the
		// highest-scoring one that qualified, and suggesting alternatives
		// alongside a settled match would be noise.
		if result.MatchType == "matched" {
			outcome.Auto = result
			return outcome, nil
		}

		outcome.Suggestions = append(outcome.Suggestions, *result)
		if len(outcome.Suggestions) >= maxSuggestions {
			break
		}
	}

	if len(outcome.Suggestions) == 0 {
		return nil, nil
	}
	return outcome, nil
}

// findCandidates picks the pre-filter that matches what the receipt actually
// carries. The tight amount+date window is the common path; a receipt missing
// one of those two anchors falls back to a query anchored on the other, and
// the surviving dimensions have to be strong for it to amount to anything.
func (s *MatchingService) findCandidates(ctx context.Context, userID uuid.UUID, receipt *domain.Receipt) ([]domain.Transaction, error) {
	compareAmount := 0.0
	amountKnown := false
	isFX := false
	if receipt.TotalUSD != nil && *receipt.TotalUSD > 0 {
		compareAmount, amountKnown = *receipt.TotalUSD, true
	} else if receipt.Total != nil && *receipt.Total > 0 {
		compareAmount, amountKnown = *receipt.Total, true
		if receipt.Currency != nil && *receipt.Currency != "" && !strings.EqualFold(*receipt.Currency, "USD") {
			isFX = true
		}
	}

	switch {
	case amountKnown && receipt.Date != nil:
		dateStr := receipt.Date.Format("2006-01-02")
		candidates, err := s.txCacheRepo.FindUnmatchedTight(ctx, userID, receipt.ID, compareAmount, dateStr, isFX)
		if err != nil {
			return nil, fmt.Errorf("find candidates: %w", err)
		}

		// If receipt date differs from today by >3 days, also search around today.
		now := time.Now()
		diff := now.Sub(*receipt.Date)
		if diff < 0 {
			diff = -diff
		}
		if diff > 3*24*time.Hour {
			todayStr := now.Format("2006-01-02")
			extra, err2 := s.txCacheRepo.FindUnmatchedTight(ctx, userID, receipt.ID, compareAmount, todayStr, isFX)
			if err2 == nil {
				candidates = mergeCandidates(candidates, extra)
			}
		}
		return candidates, nil

	case amountKnown:
		// No date on the receipt. Anchor tightly on amount and bound the search
		// by when the receipt was ingested — the only temporal signal left.
		candidates, err := s.txCacheRepo.FindUnmatchedByAmountOnly(ctx, userID, receipt.ID, compareAmount, receipt.CreatedAt, isFX)
		if err != nil {
			return nil, fmt.Errorf("find candidates by amount: %w", err)
		}
		return candidates, nil

	case receipt.Date != nil:
		// No readable total. Anchor on the date and let merchant scoring carry it.
		dateStr := receipt.Date.Format("2006-01-02")
		candidates, err := s.txCacheRepo.FindUnmatchedByDateOnly(ctx, userID, receipt.ID, dateStr)
		if err != nil {
			return nil, fmt.Errorf("find candidates by date: %w", err)
		}
		return candidates, nil
	}

	// Neither amount nor date: nothing to anchor a search on.
	return nil, nil
}

func mergeCandidates(base, extra []domain.Transaction) []domain.Transaction {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]bool, len(base))
	for _, c := range base {
		seen[c.TransactionID] = true
	}
	for _, c := range extra {
		if !seen[c.TransactionID] {
			base = append(base, c)
		}
	}
	return base
}

// evaluateCandidate processes a single candidate: checks merchant confidence,
// calls LLM if needed, and decides the outcome.
func (s *MatchingService) evaluateCandidate(ctx context.Context, receipt *domain.Receipt, tx *domain.Transaction, cs *domain.CandidateScores) (*MatchResult, error) {
	llmUsed := false
	var llmConfirm *bool
	merchantConfirmed := false
	reason := ""

	receiptMerchant := ""
	if receipt.MerchantName != nil {
		receiptMerchant = *receipt.MerchantName
	}
	txMerchant := ""
	if tx.Merchant != nil {
		txMerchant = tx.Merchant.CanonicalName
	}

	switch {
	case cs.MerchantScore >= StrongMerchant:
		// High deterministic confidence — merchant confirmed without LLM.
		merchantConfirmed = true
		reason = fmt.Sprintf("merchant %s (%.0f%% match), %s, %s",
			cs.MerchantMethod, cs.MerchantScore*100, describeAmount(cs), describeDate(cs))

	case cs.MerchantScore >= 0.40 && cs.AmountKnown && cs.AmountScore >= 0.70 &&
		receiptMerchant != "" && (txMerchant != "" || tx.Name != ""):
		// Ambiguous merchant but decent amount — ask the LLM. The trigger is
		// deliberately narrow: everything outside it resolves deterministically,
		// so widening the suggestion band does not widen LLM spend.
		llmResult, err := s.openAISvc.DisambiguateMerchant(ctx, receiptMerchant, txMerchant, tx.Name)
		if err != nil {
			slog.Warn("match-pipeline: LLM disambiguation failed", "error", err)
			reason = "LLM unavailable, merchant unconfirmed"
		} else {
			llmUsed = true
			confirm := llmResult.SameBusiness
			llmConfirm = &confirm

			if llmResult.SameBusiness && llmResult.Confidence >= 0.70 {
				merchantConfirmed = true
				// Boost merchant score based on LLM confirmation.
				cs.MerchantScore = 0.85
				cs.CompositeScore = cs.Composite()
				reason = fmt.Sprintf("LLM confirmed merchant (%.0f%%): %s", llmResult.Confidence*100, llmResult.Reason)

				// Cache the alias for future lookups.
				s.cacheAlias(ctx, receiptMerchant, txMerchant, tx.Name)
			} else {
				// The LLM says these are different businesses. That settles the
				// merchant dimension as disagreeing — not the candidate. Amount
				// and date can still carry it as far as a suggestion.
				reason = fmt.Sprintf("LLM rejected merchant: %s", llmResult.Reason)
			}
		}

	default:
		// Merchant is unreadable, or disagrees, or is too ambiguous to be worth
		// an LLM call. Not a rejection: merchant is the dimension we forgive.
		reason = fmt.Sprintf("merchant unconfirmed (%.2f %s), %s, %s",
			cs.MerchantScore, cs.MerchantMethod, describeAmount(cs), describeDate(cs))
	}

	// --- Stage 5: Decision ---
	matchType, ok := decideMatchType(cs, merchantConfirmed)
	if !ok {
		slog.Debug("match-pipeline: candidate rejected",
			"receipt_id", receipt.ID,
			"tx_id", cs.TransactionID,
			"merchant_score", cs.MerchantScore,
			"merchant_method", cs.MerchantMethod,
			"amount_score", cs.AmountScore,
			"amount_known", cs.AmountKnown,
			"date_score", cs.DateScore,
			"date_known", cs.DateKnown,
			"composite_score", cs.CompositeScore,
		)
		s.logAudit(ctx, receipt, cs, llmUsed, llmConfirm, "rejected",
			fmt.Sprintf("too few agreeing dimensions: %s", reason))
		return nil, nil
	}

	confidence := cs.CompositeScore
	switch {
	case matchType == "matched":
		if confidence < 0.85 {
			confidence = 0.85
		}
	case merchantConfirmed:
		if confidence < 0.70 {
			confidence = 0.70
		}
	default:
		confidence = math.Min(confidence, 0.75)
	}

	flag := matchFlag(receipt, cs, merchantConfirmed)

	result := &MatchResult{
		TransactionID: cs.TransactionID,
		AccountID:     tx.AccountID,
		Confidence:    confidence,
		MatchType:     matchType,
		Reason:        reason,
		Flag:          flag,
		Scores:        *cs,
		LLMUsed:       llmUsed,
		Transaction:   tx,
	}

	// --- Stage 7: Audit Log ---
	s.logAudit(ctx, receipt, cs, llmUsed, llmConfirm, matchType, reason)

	slog.Info("match-pipeline: result",
		"receipt_id", receipt.ID,
		"tx_id", cs.TransactionID,
		"match_type", matchType,
		"confidence", confidence,
		"amount_score", cs.AmountScore,
		"date_score", cs.DateScore,
		"merchant_score", cs.MerchantScore,
		"merchant_method", cs.MerchantMethod,
		"llm_used", llmUsed,
		"reason", reason,
	)

	return result, nil
}

// decideMatchType applies the agreement rule to a scored candidate.
//
// Amount and date are the load-bearing dimensions: the scorer has already
// dropped any candidate whose known amount or date actively contradicts the
// receipt, and a known-but-soft amount still has to stay inside the suggestion
// band here. Either may be absent — an unreadable total or date leaves the
// other two dimensions to carry the decision — but neither may be wrong.
//
// Merchant is the forgiving dimension: names arrive mangled by POS prefixes,
// DBA registrations and payment aggregators often enough that a disagreement
// is weak evidence. A merchant that is missing, ambiguous, or outright
// different still yields a suggestion when amount and date both hold.
//
// Auto-matching is untouched: it still requires all three to be strong.
func decideMatchType(cs *domain.CandidateScores, merchantConfirmed bool) (string, bool) {
	strongAmount := cs.AmountKnown && cs.AmountScore >= StrongAmount
	strongDate := cs.DateKnown && cs.DateScore >= StrongDate
	strongMerchant := merchantConfirmed

	if strongMerchant && strongAmount && strongDate {
		return "matched", true
	}

	// A known amount that is merely tolerable (>10% off, or an upward charge
	// past the tip-sized band) is too far to propose on the strength of the
	// other two dimensions.
	if cs.AmountKnown && cs.AmountScore < 0.70 {
		return "", false
	}
	if cs.DateKnown && cs.DateScore < 0.50 {
		return "", false
	}

	weak := 0
	for _, strong := range []bool{strongAmount, strongDate, strongMerchant} {
		if !strong {
			weak++
		}
	}

	switch {
	case weak == 1:
		// Exactly one dimension is absent or disagreeing; the other two agree.
		return "suggested", true
	case weak == 2 && strongMerchant && cs.AmountKnown && cs.DateKnown:
		// Merchant is confirmed and both other dimensions are present and
		// inside tolerance, just not crisp — the long-standing soft band.
		return "suggested", true
	}
	return "", false
}

// matchFlag names the single dimension a client should explain to the user,
// most decisive first.
func matchFlag(receipt *domain.Receipt, cs *domain.CandidateScores, merchantConfirmed bool) string {
	isFX := receipt.Currency != nil && *receipt.Currency != "" && !strings.EqualFold(*receipt.Currency, "USD")
	switch {
	case isFX && (receipt.TotalUSD == nil || *receipt.TotalUSD <= 0):
		return domain.FlagFXSuspected
	case !cs.AmountKnown:
		return domain.FlagNoAmount
	case !cs.DateKnown:
		return domain.FlagNoDate
	case !cs.MerchantKnown:
		return domain.FlagNoMerchant
	case !merchantConfirmed:
		return domain.FlagMerchantMismatch
	case cs.ChargeExceedsReceipt:
		return domain.FlagAmountUpward
	case cs.AmountDiffPct > 5:
		return domain.FlagAmountMismatch
	case cs.DateDiffDays > 2:
		return domain.FlagDateMismatch
	}
	return domain.FlagClean
}

func describeAmount(cs *domain.CandidateScores) string {
	if !cs.AmountKnown {
		return "no amount on receipt"
	}
	return fmt.Sprintf("amount diff %.1f%%", cs.AmountDiffPct)
}

func describeDate(cs *domain.CandidateScores) string {
	if !cs.DateKnown {
		return "no date on receipt"
	}
	return fmt.Sprintf("date diff %d days", cs.DateDiffDays)
}

func (s *MatchingService) cacheAlias(ctx context.Context, receiptMerchant, txMerchant, txName string) {
	if s.aliasRepo == nil {
		return
	}

	canonical := NormalizeMerchant(receiptMerchant)
	if canonical == "" {
		return
	}

	aliases := []string{}
	if txMerchant != "" {
		aliases = append(aliases, NormalizeMerchant(txMerchant))
	}
	if txName != "" {
		n := NormalizeMerchant(txName)
		if n != "" && n != NormalizeMerchant(txMerchant) {
			aliases = append(aliases, n)
		}
	}

	for _, alias := range aliases {
		if alias == "" || alias == canonical {
			continue
		}
		// Map the tx merchant name → receipt canonical so AreSameMerchant can
		// resolve either side to the same canonical value.
		_ = s.aliasRepo.Create(ctx, &domain.MerchantAlias{
			Canonical: canonical,
			Alias:     alias,
			Source:    "llm",
		})
		// Self-ref so FindCanonical(canonical) also returns canonical — needed
		// for ComputeMerchantKey to produce a consistent soft-dedup key when
		// the receipt merchant name is looked up directly.
		_ = s.aliasRepo.Create(ctx, &domain.MerchantAlias{
			Canonical: canonical,
			Alias:     canonical,
			Source:    "llm",
		})
	}
}

func (s *MatchingService) logAudit(ctx context.Context, receipt *domain.Receipt, cs *domain.CandidateScores, llmUsed bool, llmConfirm *bool, outcome, reason string) {
	if s.auditRepo == nil {
		return
	}
	entry := &domain.MatchAuditEntry{
		ReceiptID:          receipt.ID,
		TransactionID:      cs.TransactionID,
		AmountScore:        cs.AmountScore,
		DateScore:          cs.DateScore,
		MerchantScore:      cs.MerchantScore,
		CompositeScore:     cs.CompositeScore,
		LLMUsed:            llmUsed,
		LLMMerchantConfirm: llmConfirm,
		Outcome:            outcome,
		Reason:             reason,
	}
	if err := s.auditRepo.Create(ctx, entry); err != nil {
		slog.Error("match-audit: failed to log", "error", err,
			"receipt_id", receipt.ID, "tx_id", cs.TransactionID,
			"outcome", outcome, "reason", reason)
	}
}
