package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
)

// categoryLLMMinConfidence is the floor below which a model-proposed correction
// is recorded but not applied, matching llmLinkThreshold in the catalog matcher.
const categoryLLMMinConfidence = 0.80

// plaidCategoryOf returns the category Plaid assigned. Rows written before
// plaid_pfc_* existed fall back to the effective columns, which for an
// uncorrected row hold Plaid's value anyway.
func plaidCategoryOf(tx *domain.Transaction) (primary, detailed string) {
	primary, detailed = ptrStr(tx.PlaidPFCPrimary), ptrStr(tx.PlaidPFCDetailed)
	if primary == "" || detailed == "" {
		return ptrStr(tx.PFCPrimary), ptrStr(tx.PFCDetailed)
	}
	return primary, detailed
}

// CategoryService handles receipt-based transaction category corrections.
type CategoryService struct {
	correctionCacheRepo repository.CategoryCorrectionCacheRepository
	txCacheRepo         repository.TransactionCacheRepository
	openAISvc           *OpenAIService
}

func NewCategoryService(
	correctionCacheRepo repository.CategoryCorrectionCacheRepository,
	txCacheRepo repository.TransactionCacheRepository,
	openAISvc *OpenAIService,
) *CategoryService {
	return &CategoryService{
		correctionCacheRepo: correctionCacheRepo,
		txCacheRepo:         txCacheRepo,
		openAISvc:           openAISvc,
	}
}

// CorrectCategoryAfterMatch checks if a matched receipt's line items suggest a
// different category than what Plaid assigned, and updates the transaction if so.
func (s *CategoryService) CorrectCategoryAfterMatch(ctx context.Context, receipt *domain.Receipt, tx *domain.Transaction) {
	// Judge Plaid's own category, not the effective one: pfc_* may already hold a
	// correction this service or the vetting pass made, and asking the model to
	// review our own answer — or keying the cache on it — would compound an error
	// rather than catch it.
	bankPrimary, bankDetailed := plaidCategoryOf(tx)
	if len(receipt.LineItems) == 0 || bankPrimary == "" || bankDetailed == "" {
		return
	}
	// No guard on an existing correction. A receipt sees the merchant AND the
	// basket, so it outranks the merchant-vetting pass and should overwrite it;
	// and a transaction has at most one matched receipt, so the only way to
	// arrive here twice is the match itself changing — in which case re-deciding
	// is the point. The vetting pass yields in the other direction by skipping
	// rows whose category_corrected_at is already set.

	var lineItems []domain.LineItem
	if err := json.Unmarshal(receipt.LineItems, &lineItems); err != nil || len(lineItems) == 0 {
		return
	}

	merchantCanonical := ""
	if receipt.MerchantName != nil {
		merchantCanonical = NormalizeMerchant(*receipt.MerchantName)
	} else if tx.Merchant != nil {
		merchantCanonical = NormalizeMerchant(tx.Merchant.CanonicalName)
	}
	if merchantCanonical == "" {
		merchantCanonical = NormalizeMerchant(tx.Name)
	}

	// Stage 1: Check cache.
	cached, err := s.correctionCacheRepo.FindByMerchantAndCategory(ctx, merchantCanonical, bankDetailed)
	if err == nil {
		if cached.CorrectedPFCDetailed != bankDetailed {
			s.applyCorrection(ctx, tx, cached.CorrectedPFCPrimary, cached.CorrectedPFCDetailed, "cache")
		}
		return
	}

	// Stage 2: Rule-based keyword detection.
	ruleResult := detectCategoryByRules(lineItems, bankPrimary)

	if ruleResult != nil && !ruleResult.ShouldCorrect {
		// Rules agree with Plaid — cache and skip.
		s.cacheCorrection(ctx, merchantCanonical, bankPrimary, bankDetailed,
			bankPrimary, bankDetailed, "rule", lineItems)
		return
	}

	if ruleResult != nil && ruleResult.ShouldCorrect {
		// Rules confidently disagree — apply without LLM.
		s.applyCorrection(ctx, tx, ruleResult.CorrectedPFCPrimary, ruleResult.CorrectedPFCDetailed, "rule")
		s.cacheCorrection(ctx, merchantCanonical, bankPrimary, bankDetailed,
			ruleResult.CorrectedPFCPrimary, ruleResult.CorrectedPFCDetailed, "rule", lineItems)
		return
	}

	// Stage 3: LLM fallback (rules inconclusive).
	llmResult, err := s.openAISvc.CorrectCategory(ctx, merchantCanonical, lineItems, bankPrimary, bankDetailed)
	if err != nil {
		slog.Error("category correction LLM failed", "error", err, "tx_id", tx.TransactionID)
		return
	}

	correctedPrimary := bankPrimary
	correctedDetailed := bankDetailed
	if llmResult.ShouldCorrect && llmResult.Confidence < categoryLLMMinConfidence {
		slog.Info("category correction below confidence floor, leaving Plaid's category",
			"tx_id", tx.TransactionID, "confidence", llmResult.Confidence,
			"would_have_been", llmResult.CorrectedPFCDetailed)
		llmResult.ShouldCorrect = false
	}
	if llmResult.ShouldCorrect {
		s.applyCorrection(ctx, tx, llmResult.CorrectedPFCPrimary, llmResult.CorrectedPFCDetailed, "llm")
		correctedPrimary = llmResult.CorrectedPFCPrimary
		correctedDetailed = llmResult.CorrectedPFCDetailed
	}
	s.cacheCorrection(ctx, merchantCanonical, bankPrimary, bankDetailed,
		correctedPrimary, correctedDetailed, "llm", lineItems)
}

// applyCorrection writes a correction only if it names a real Plaid category.
// The schema enum already constrains model output, but the rule stage builds
// categories with no model and no schema involved, so this is the only check
// standing between mapPrimaryToDetailed's synthesised names and stored data.
func (s *CategoryService) applyCorrection(ctx context.Context, tx *domain.Transaction, primary, detailed, source string) {
	if !domain.ValidPFC(primary, detailed) {
		slog.Warn("category correction rejected: not a Plaid category",
			"tx_id", tx.TransactionID, "primary", primary, "detailed", detailed, "source", source)
		return
	}
	// A correction names a primary; its detailed value is that primary's
	// catch-all. Writing it when the primary already agrees would trade the
	// bank's specific sub-type for a vaguer one and gain nothing.
	if bankPrimary, _ := plaidCategoryOf(tx); bankPrimary == primary {
		return
	}
	if err := s.txCacheRepo.ApplyCategoryCorrection(ctx, tx.TransactionID, primary, detailed); err != nil {
		slog.Error("failed to apply category correction", "tx_id", tx.TransactionID, "error", err)
		return
	}
	slog.Info("category corrected",
		"tx_id", tx.TransactionID,
		"from", fmt.Sprintf("%s/%s", ptrStr(tx.PFCPrimary), ptrStr(tx.PFCDetailed)),
		"to", fmt.Sprintf("%s/%s", primary, detailed),
		"source", source,
	)
}

func (s *CategoryService) cacheCorrection(ctx context.Context, merchant, origPrimary, origDetailed, corrPrimary, corrDetailed, source string, lineItems []domain.LineItem) {
	var descriptions []string
	for i, li := range lineItems {
		if i >= 5 {
			break
		}
		descriptions = append(descriptions, li.Description)
	}
	sample := strings.Join(descriptions, ", ")

	entry := &domain.CategoryCorrectionCache{
		MerchantCanonical:    merchant,
		OriginalPFCPrimary:   origPrimary,
		OriginalPFCDetailed:  origDetailed,
		CorrectedPFCPrimary:  corrPrimary,
		CorrectedPFCDetailed: corrDetailed,
		Source:               source,
		SampleLineItems:      &sample,
	}
	if err := s.correctionCacheRepo.Create(ctx, entry); err != nil {
		slog.Error("failed to cache category correction", "error", err)
	}
}

func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// categoryKeywords maps Plaid PFC primary categories to item description keywords.
var categoryKeywords = map[string][]string{
	"FOOD_AND_DRINK": {
		"sandwich", "burger", "pizza", "coffee", "latte", "espresso", "mocha",
		"tea", "water", "juice", "soda", "cola", "pepsi", "coke", "sprite",
		"milk", "bread", "cheese", "chicken", "beef", "pork", "fish", "salad",
		"soup", "rice", "pasta", "noodle", "taco", "burrito", "wrap",
		"fries", "chips", "cookie", "cake", "donut", "bagel", "muffin",
		"yogurt", "fruit", "apple", "banana", "orange", "grape",
		"beer", "wine", "cocktail", "whiskey", "vodka",
		"protein", "power", "energy drink", "gatorade", "coconut",
		"snack", "candy", "chocolate", "ice cream", "smoothie",
		"breakfast", "lunch", "dinner", "meal", "food",
		"cocnut", "cocnutwtr", "core power",
	},
	"GENERAL_MERCHANDISE": {
		"shirt", "pants", "shoes", "jacket", "dress", "hat", "socks",
		"toy", "game", "book", "magazine", "charger", "cable", "battery",
		"soap", "shampoo", "toothpaste", "deodorant", "lotion",
		"cleaning", "paper towel", "tissue", "trash bag",
	},
	"MEDICAL": {
		"prescription", "medicine", "aspirin", "ibuprofen", "bandage",
		"vitamin", "supplement", "pharmacy",
	},
}

// detectCategoryByRules checks if a majority of line items suggest a different
// category than what Plaid assigned. Returns nil if inconclusive.
func detectCategoryByRules(lineItems []domain.LineItem, pfcPrimary string) *domain.CategoryCorrectionResult {
	if len(lineItems) == 0 {
		return nil
	}

	categoryCounts := make(map[string]int)
	totalItems := 0

	for _, item := range lineItems {
		desc := strings.ToLower(item.Description)
		if desc == "" {
			continue
		}
		totalItems++
		for category, keywords := range categoryKeywords {
			for _, kw := range keywords {
				if strings.Contains(desc, kw) {
					categoryCounts[category]++
					break
				}
			}
		}
	}

	if totalItems == 0 {
		return nil
	}

	bestCategory := ""
	bestCount := 0
	for cat, count := range categoryCounts {
		if count > bestCount {
			bestCount = count
			bestCategory = cat
		}
	}

	// Need at least 60% of items to match a category.
	if bestCount == 0 || float64(bestCount)/float64(totalItems) < 0.6 {
		return nil // inconclusive — defer to LLM
	}

	if bestCategory == pfcPrimary {
		return &domain.CategoryCorrectionResult{
			ShouldCorrect: false,
			Source:        "rule",
			Reason:        fmt.Sprintf("rules agree: %d/%d items match %s", bestCount, totalItems, pfcPrimary),
		}
	}

	// The catch-all leaf, not a synthesised name: the keyword vote establishes a
	// primary and nothing more, so claiming a sub-type would be inventing detail.
	detailedCategory, ok := domain.OtherDetailedFor(bestCategory)
	if !ok {
		return nil
	}
	return &domain.CategoryCorrectionResult{
		ShouldCorrect:        true,
		CorrectedPFCPrimary:  bestCategory,
		CorrectedPFCDetailed: detailedCategory,
		Source:               "rule",
		Reason:               fmt.Sprintf("rules: %d/%d items suggest %s, Plaid says %s", bestCount, totalItems, bestCategory, pfcPrimary),
	}
}
