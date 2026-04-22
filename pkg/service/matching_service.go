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

// MatchReceipt is the new unified matching pipeline:
//  1. DB pre-filter (tight amount + date)
//  2. Deterministic scoring (amount, date, merchant)
//  3. Merchant alias cache check
//  4. LLM merchant disambiguation (only if ambiguous)
//  5. Decision matrix → auto-match, suggest, or skip
//  6. Cache new merchant alias if LLM confirmed
//  7. Audit log
func (s *MatchingService) MatchReceipt(ctx context.Context, userID uuid.UUID, receipt *domain.Receipt) (*MatchResult, error) {
	if receipt.Total == nil || *receipt.Total <= 0 {
		return nil, nil
	}

	// Determine effective amount and date for DB query.
	compareAmount := *receipt.Total
	isFX := false
	if receipt.TotalUSD != nil && *receipt.TotalUSD > 0 {
		compareAmount = *receipt.TotalUSD
	} else if receipt.Currency != nil && *receipt.Currency != "" && !strings.EqualFold(*receipt.Currency, "USD") {
		isFX = true
	}

	// Build date string: try receipt date first, fallback to today.
	dateStr := time.Now().Format("2006-01-02")
	if receipt.Date != nil {
		dateStr = receipt.Date.Format("2006-01-02")
	}

	// --- Stage 1: DB Pre-Filter ---
	candidates, err := s.txCacheRepo.FindUnmatchedTight(ctx, userID, compareAmount, dateStr, isFX)
	if err != nil {
		return nil, fmt.Errorf("find candidates: %w", err)
	}

	// If receipt date differs from today by >3 days, also search around today.
	if receipt.Date != nil {
		now := time.Now()
		diff := now.Sub(*receipt.Date)
		if diff < 0 {
			diff = -diff
		}
		if diff > 3*24*time.Hour {
			todayStr := now.Format("2006-01-02")
			extra, err2 := s.txCacheRepo.FindUnmatchedTight(ctx, userID, compareAmount, todayStr, isFX)
			if err2 == nil && len(extra) > 0 {
				seen := make(map[string]bool, len(candidates))
				for _, c := range candidates {
					seen[c.TransactionID] = true
				}
				for _, c := range extra {
					if !seen[c.TransactionID] {
						candidates = append(candidates, c)
					}
				}
			}
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	slog.Info("match-pipeline: candidates found",
		"receipt_id", receipt.ID, "count", len(candidates))

	// --- Stage 2: Deterministic Scoring ---
	scored := s.scoringSvc.ScoreCandidates(ctx, receipt, candidates)
	if len(scored) == 0 {
		return nil, nil
	}

	// --- Stage 3 & 4: Evaluate top candidates ---
	// Look at top 3 scored candidates and try to resolve.
	limit := 3
	if len(scored) < limit {
		limit = len(scored)
	}

	var bestResult *MatchResult
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

		// Take the first valid result (highest composite score).
		bestResult = result
		break
	}

	return bestResult, nil
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
	case cs.MerchantScore >= 0.80:
		// High deterministic confidence — merchant confirmed without LLM.
		merchantConfirmed = true
		reason = fmt.Sprintf("merchant %s (%.0f%% match), amount diff %.1f%%, date diff %d days",
			cs.MerchantMethod, cs.MerchantScore*100, cs.AmountDiffPct, cs.DateDiffDays)

	case cs.MerchantScore >= 0.40 && cs.AmountScore >= 0.70:
		// Ambiguous merchant but decent amount — ask LLM.
		if receiptMerchant == "" || (txMerchant == "" && tx.Name == "") {
			// Can't disambiguate without names.
			s.logAudit(ctx, receipt, cs, false, nil, "rejected", "missing merchant names for LLM")
			return nil, nil
		}

		llmResult, err := s.openAISvc.DisambiguateMerchant(ctx, receiptMerchant, txMerchant, tx.Name)
		if err != nil {
			slog.Warn("match-pipeline: LLM disambiguation failed", "error", err)
			// Fallback: if single word overlap + great amount, suggest anyway.
			if cs.MerchantScore >= 0.45 && cs.AmountScore >= 0.85 && cs.DateScore >= 0.70 {
				merchantConfirmed = false
				reason = "LLM unavailable, weak merchant match but strong amount+date"
			} else {
				s.logAudit(ctx, receipt, cs, true, nil, "rejected", "LLM error: "+err.Error())
				return nil, nil
			}
		} else {
			llmUsed = true
			confirm := llmResult.SameBusiness
			llmConfirm = &confirm

			if llmResult.SameBusiness && llmResult.Confidence >= 0.70 {
				merchantConfirmed = true
				// Boost merchant score based on LLM confirmation.
				cs.MerchantScore = 0.85
				cs.CompositeScore = cs.AmountScore*0.35 + cs.DateScore*0.25 + cs.MerchantScore*0.40
				reason = fmt.Sprintf("LLM confirmed merchant (%.0f%%): %s", llmResult.Confidence*100, llmResult.Reason)

				// Cache the alias for future lookups.
				s.cacheAlias(ctx, receiptMerchant, txMerchant, tx.Name)
			} else {
				s.logAudit(ctx, receipt, cs, true, llmConfirm, "rejected",
					fmt.Sprintf("LLM rejected merchant: %s", llmResult.Reason))
				return nil, nil
			}
		}

	default:
		// Low merchant score — skip.
		s.logAudit(ctx, receipt, cs, false, nil, "rejected",
			fmt.Sprintf("merchant score too low: %.2f (%s)", cs.MerchantScore, cs.MerchantMethod))
		return nil, nil
	}

	// --- Stage 5: Decision Matrix ---
	confidence := cs.CompositeScore
	matchType := "suggested"

	if merchantConfirmed && cs.AmountScore >= 0.85 && cs.DateScore >= 0.70 {
		matchType = "matched"
		if confidence < 0.85 {
			confidence = 0.85
		}
	} else if merchantConfirmed && cs.AmountScore >= 0.70 && cs.DateScore >= 0.50 {
		matchType = "suggested"
		if confidence < 0.70 {
			confidence = 0.70
		}
	} else if !merchantConfirmed {
		matchType = "suggested"
		confidence = math.Min(confidence, 0.75)
	} else {
		// Merchant confirmed but amount or date weak.
		matchType = "suggested"
	}

	// Determine flag.
	flag := "clean"
	isFX := receipt.Currency != nil && *receipt.Currency != "" && !strings.EqualFold(*receipt.Currency, "USD")
	if isFX && (receipt.TotalUSD == nil || *receipt.TotalUSD <= 0) {
		flag = "fx_suspected"
	} else if cs.AmountDiffPct > 5 {
		flag = "amount_mismatch"
	} else if cs.DateDiffDays > 2 {
		flag = "date_mismatch"
	}

	if reason == "" {
		reason = fmt.Sprintf("amount %.1f%% diff, date %d days, merchant %s",
			cs.AmountDiffPct, cs.DateDiffDays, cs.MerchantMethod)
	}

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
		// Store both directions for AreSameMerchant to work.
		_ = s.aliasRepo.Create(ctx, &domain.MerchantAlias{
			Canonical: canonical,
			Alias:     alias,
			Source:    "llm",
		})
		_ = s.aliasRepo.Create(ctx, &domain.MerchantAlias{
			Canonical: canonical,
			Alias:     canonical,
			Source:    "llm",
		})
		_ = s.aliasRepo.Create(ctx, &domain.MerchantAlias{
			Canonical: canonical,
			Alias:     alias,
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
		slog.Error("match-audit: failed to log", "error", err)
	}
}
