package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

type ReceiptService struct {
	receiptRepo      repository.ReceiptRepository
	matchRepo        repository.ReceiptMatchRepository
	txCacheRepo      repository.TransactionCacheRepository
	matchingSvc      *MatchingService
	openAISvc        *OpenAIService
	currencySvc      *CurrencyService
	categorySvc      *CategoryService
	thumbnailSvc     ThumbnailGenerator
	storageSvc       FileStorage
	baseImageURL     string
	suggestLocks     sync.Map // map[uuid.UUID]*sync.Mutex
	recentlyAccessed sync.Map // map[uuid.UUID]time.Time - tracks receipts being actively linked by user
}

func NewReceiptService(
	receiptRepo repository.ReceiptRepository,
	matchRepo repository.ReceiptMatchRepository,
	txCacheRepo repository.TransactionCacheRepository,
	matchingSvc *MatchingService,
	openAISvc *OpenAIService,
	currencySvc *CurrencyService,
	categorySvc *CategoryService,
	thumbnailSvc ThumbnailGenerator,
	storageSvc FileStorage,
	baseImageURL string,
) *ReceiptService {
	return &ReceiptService{
		receiptRepo:   receiptRepo,
		matchRepo:     matchRepo,
		txCacheRepo:   txCacheRepo,
		matchingSvc:   matchingSvc,
		openAISvc:     openAISvc,
		currencySvc:   currencySvc,
		categorySvc:   categorySvc,
		thumbnailSvc: thumbnailSvc,
		storageSvc:   storageSvc,
		baseImageURL: baseImageURL,
	}
}

// ReceiptRepo returns the underlying receipt repository.
func (s *ReceiptService) ReceiptRepo() repository.ReceiptRepository { return s.receiptRepo }

// OpenAISvc returns the underlying OpenAI service.
func (s *ReceiptService) OpenAISvc() *OpenAIService { return s.openAISvc }

// ConvertCurrencyIfNeeded checks the receipt's currency; if it is non-USD and
// we have a total, it calls the CurrencyService to obtain a TotalUSD value.
// The receipt is mutated in-place.
func (s *ReceiptService) ConvertCurrencyIfNeeded(ctx context.Context, receipt *domain.Receipt) {
	if s.currencySvc == nil {
		return
	}
	if receipt.Currency == nil || *receipt.Currency == "" || strings.EqualFold(*receipt.Currency, "USD") {
		return
	}
	if receipt.Total == nil || *receipt.Total <= 0 {
		return
	}
	dateStr := time.Now().Format("2006-01-02")
	if receipt.Date != nil {
		dateStr = receipt.Date.Format("2006-01-02")
	}
	usd, err := s.currencySvc.ConvertToUSD(ctx, *receipt.Total, *receipt.Currency, dateStr)
	if err != nil {
		slog.Error("fx conversion failed", "currency", *receipt.Currency, "error", err)
		return
	}
	receipt.TotalUSD = &usd
	slog.Info("fx conversion applied",
		"receipt_id", receipt.ID,
		"from", *receipt.Currency,
		"original", *receipt.Total,
		"usd", usd,
	)
}

// DedupOutcome reports which branch of the dedup pipeline produced the
// returned receipt.
type DedupOutcome int

const (
	// OutcomeCreated means a brand-new primary row was written.
	OutcomeCreated DedupOutcome = iota
	// OutcomeHardDuplicate means a Tier-1 signal matched an existing primary
	// (content hash, gmail message id, or order id). No new row was written.
	OutcomeHardDuplicate
	// OutcomeSoftDuplicate means a Tier-2 signal matched (normalized merchant
	// + amount + date window). A new row was written linked via duplicate_of.
	OutcomeSoftDuplicate
)

// String returns a stable machine-readable label for the outcome, suitable
// for use as an HTTP response discriminator (e.g. "exact_match").
func (o DedupOutcome) String() string {
	switch o {
	case OutcomeHardDuplicate:
		return "exact_match"
	case OutcomeSoftDuplicate:
		return "likely_duplicate"
	default:
		return "created"
	}
}

type ScanResult struct {
	Receipt *domain.Receipt `json:"receipt"`
	Outcome DedupOutcome    `json:"-"`
	// Primary is populated only when Outcome is OutcomeSoftDuplicate. It
	// carries the existing primary receipt that the new row was linked
	// against, so UIs can show the user which receipt we think this is a
	// duplicate of ("$14.20 Cloudflare from Feb 1").
	Primary *domain.Receipt `json:"primary,omitempty"`
}

// BatchScanResult holds the results of scanning multiple receipt images from a directory.
type BatchScanResult struct {
	Succeeded []ScanResult `json:"succeeded"`
	Failed    []BatchScanFailure `json:"failed"`
	Total     int `json:"total"`
}

// BatchScanFailure records a single image that failed to process.
type BatchScanFailure struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// ReceiptFromOCRInput holds the parameters for building a receipt from OCR output.
type ReceiptFromOCRInput struct {
	UserID       uuid.UUID
	OCRResult    *domain.OCRResult
	ImageURL     string
	ImagePath    string
	MerchantName *string  // caller-supplied override
	Total        *float64 // caller-supplied override
	Source       string   // "upload" or "email"
}

// BuildReceiptFromOCR creates a Receipt by mapping OCR fields, extracting totals/dates
// from raw text as fallback, and clamping future dates. The returned receipt is NOT
// persisted — the caller is responsible for dedup checks, currency conversion, and saving.
func BuildReceiptFromOCR(in ReceiptFromOCRInput) *domain.Receipt {
	ocr := in.OCRResult
	lineItemsJSON, _ := json.Marshal(ocr.LineItems)

	source := in.Source
	if source == "" {
		source = "upload"
	}

	receipt := &domain.Receipt{
		UserID:       in.UserID,
		ImageURL:     in.ImageURL,
		ImagePath:    in.ImagePath,
		MerchantName: in.MerchantName,
		Total:        in.Total,
		LineItems:    lineItemsJSON,
		Source:       source,
		Status:       "unmatched",
	}

	if ocr.RawText != "" {
		receipt.RawText = &ocr.RawText
	}
	if ocr.Error != "" {
		receipt.OCRError = &ocr.Error
	}

	if receipt.MerchantName == nil && ocr.MerchantName != "" {
		receipt.MerchantName = &ocr.MerchantName
	}
	if ocr.MerchantAddress != "" {
		receipt.MerchantAddress = &ocr.MerchantAddress
	}
	if ocr.PaymentMethod != "" {
		receipt.PaymentMethod = &ocr.PaymentMethod
	}
	if ocr.LastFourDigits != "" {
		receipt.LastFourDigits = &ocr.LastFourDigits
	}
	if ocr.TransactionTime != "" {
		receipt.TransactionTime = &ocr.TransactionTime
	}
	receipt.Subtotal = ocr.Subtotal
	receipt.Tax = ocr.Tax
	receipt.Tip = ocr.Tip

	if receipt.Total == nil {
		if ocr.Total != nil {
			receipt.Total = ocr.Total
		} else if ocr.RawText != "" {
			receipt.Total = ExtractTotal(ocr.RawText)
		}
	}

	if ocr.TransactionDate != "" {
		if t, err := time.Parse("2006-01-02", ocr.TransactionDate); err == nil {
			receipt.Date = &t
		}
	}
	if receipt.Date == nil && ocr.RawText != "" {
		receipt.Date = ExtractDate(ocr.RawText)
	}

	if ocr.Currency != "" {
		receipt.Currency = &ocr.Currency
	}

	ClampFutureDate(receipt, in.UserID, nil)

	return receipt
}

// ClampFutureDate replaces a future receipt date with fallback (if non-nil) or today.
// Logs a warning when clamping occurs.
func ClampFutureDate(receipt *domain.Receipt, userID uuid.UUID, fallback *time.Time) {
	if receipt.Date == nil {
		return
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !receipt.Date.After(today) {
		return
	}
	slog.Warn("clamping future receipt date",
		"original_date", receipt.Date.Format("2006-01-02"),
		"user_id", userID,
	)
	if fallback != nil {
		receipt.Date = fallback
	} else {
		receipt.Date = &today
	}
}

func (s *ReceiptService) Scan(ctx context.Context, userID uuid.UUID, imageBytes []byte, mimeType, imageURL string, merchantName *string, total *float64) (*ScanResult, error) {
	ocrResult := s.openAISvc.ParseImageData(ctx, imageBytes, mimeType)

	receipt := BuildReceiptFromOCR(ReceiptFromOCRInput{
		UserID:       userID,
		OCRResult:    ocrResult,
		ImageURL:     imageURL,
		MerchantName: merchantName,
		Total:        total,
		Source:       "upload",
	})

	s.ConvertCurrencyIfNeeded(ctx, receipt)

	if !IsValidReceipt(receipt) {
		slog.Info("skipping empty receipt from scan", "user_id", userID)
		return nil, fmt.Errorf("receipt contains no valid data: %w", domain.ErrBadRequest)
	}

	if err := s.receiptRepo.Create(ctx, receipt); err != nil {
		return nil, fmt.Errorf("create receipt: %w", err)
	}

	go s.GenerateThumbnailAsync(context.Background(), receipt.ID, imageBytes, mimeType, imageURL, false)

	result := &ScanResult{Receipt: receipt}
	s.TryMatch(ctx, userID, receipt)
	return result, nil
}

// imageExtensions is the set of file extensions treated as receipt images.
var imageExtensions = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".heic": "image/heic",
}

// BatchScan reads all image files from dirPath, runs OCR + matching on each,
// and returns aggregated results.
func (s *ReceiptService) BatchScan(ctx context.Context, userID uuid.UUID, dirPath string, storageSvc FileStorage) (*BatchScanResult, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	result := &BatchScanResult{}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		ext := strings.ToLower(filepath.Ext(entry.Name()))
		mimeType, ok := imageExtensions[ext]
		if !ok {
			continue
		}

		result.Total++

		fullPath := filepath.Join(dirPath, entry.Name())
		imageBytes, err := os.ReadFile(fullPath)
		if err != nil {
			result.Failed = append(result.Failed, BatchScanFailure{
				Filename: entry.Name(),
				Error:    fmt.Sprintf("read file: %v", err),
			})
			continue
		}

		// Save image to storage (private; host resolves to presigned URL at read time).
		imageKey := uuid.New().String() + ext
		var imageURL string
		if storageSvc != nil {
			if err := storageSvc.UploadPrivate(ctx, imageKey, bytes.NewReader(imageBytes), mimeType); err == nil {
				imageURL = imageKey
			}
		}

		scanResult, err := s.Scan(ctx, userID, imageBytes, mimeType, imageURL, nil, nil)
		if err != nil {
			result.Failed = append(result.Failed, BatchScanFailure{
				Filename: entry.Name(),
				Error:    err.Error(),
			})
			continue
		}

		result.Succeeded = append(result.Succeeded, *scanResult)
	}

	return result, nil
}

// IsValidReceipt returns true if the receipt has at least minimally useful data:
// a non-zero total OR a non-empty merchant name.
func IsValidReceipt(receipt *domain.Receipt) bool {
	hasTotal := receipt.Total != nil && *receipt.Total > 0
	hasMerchant := receipt.MerchantName != nil && *receipt.MerchantName != ""
	return hasTotal || hasMerchant
}

// TryMatch uses the deterministic-first matching pipeline to find and
// commit the best match for a receipt:
//   - match_type "matched"   -> receipt_match created with method "auto",    status "matched"
//   - match_type "suggested" -> receipt_match created with method "suggested", status "suggested"
//   - no result              -> receipt stays "unmatched"
func (s *ReceiptService) TryMatch(ctx context.Context, userID uuid.UUID, receipt *domain.Receipt) {
	if receipt.Total == nil || *receipt.Total <= 0 {
		return
	}

	result, err := s.matchingSvc.MatchReceipt(ctx, userID, receipt)
	if err != nil {
		slog.Error("match-pipeline: error", "error", err, "receipt_id", receipt.ID)
		return
	}
	if result == nil {
		return
	}

	method := result.MatchType
	if method == "matched" {
		method = "auto"
	}

	match := &domain.ReceiptMatch{
		ReceiptID:       receipt.ID,
		TransactionID:   result.TransactionID,
		AccountID:       result.AccountID,
		ConfidenceScore: result.Confidence,
		MatchMethod:     method,
		MatchReason:     result.Reason,
	}

	if err := s.matchRepo.Create(ctx, match); err != nil {
		slog.Error("match-pipeline: create match", "error", err, "receipt_id", receipt.ID)
		return
	}
	newStatus := result.MatchType // "matched" or "suggested"
	if err := s.receiptRepo.UpdateStatus(ctx, receipt.ID, newStatus); err != nil {
		slog.Error("match-pipeline: update status", "error", err, "receipt_id", receipt.ID)
		return
	}
	receipt.Status = newStatus

	// Trigger category correction asynchronously after successful match.
	if s.categorySvc != nil && result.Transaction != nil {
		go s.categorySvc.CorrectCategoryAfterMatch(context.Background(), receipt, result.Transaction)
	}
}

func (s *ReceiptService) GetByID(ctx context.Context, userID, receiptID uuid.UUID) (*domain.Receipt, error) {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	if receipt.UserID != userID {
		return nil, domain.ErrForbidden
	}
	return receipt, nil
}

func (s *ReceiptService) List(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.Receipt, error) {
	return s.receiptRepo.FindByUserID(ctx, userID, filter)
}

func (s *ReceiptService) ListWithMatches(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.ReceiptWithMatch, error) {
	return s.receiptRepo.FindByUserIDWithMatches(ctx, userID, filter)
}

func (s *ReceiptService) CountUnmatched(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.receiptRepo.CountUnmatched(ctx, userID)
}

// LinkCandidatesResult holds the two sections for the manual linking UI.
type LinkCandidatesResult struct {
	AmountMatches []domain.Transaction `json:"amount_matches"`
	AllUnmatched  []domain.Transaction `json:"all_unmatched"`
}

// GetLinkCandidates returns transaction candidates for manually linking a receipt.
// It returns amount-matched transactions (tight filter) and all other unmatched transactions.
func (s *ReceiptService) GetLinkCandidates(ctx context.Context, userID, receiptID uuid.UUID, search string) (*LinkCandidatesResult, error) {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	if receipt.UserID != userID {
		return nil, domain.ErrForbidden
	}

	// Mark this receipt as recently accessed to prevent background matching race conditions.
	// The background matcher will skip this receipt for 30 seconds.
	s.recentlyAccessed.Store(receiptID, time.Now())

	result := &LinkCandidatesResult{}

	// If we have a search term, filter all unmatched by search and return
	if search != "" {
		filtered, err := s.txCacheRepo.SearchUnmatched(ctx, userID, search)
		if err != nil {
			return nil, err
		}
		// Split into amount matches and the rest
		if receipt.Total != nil && *receipt.Total > 0 {
			for _, tx := range filtered {
				pctDiff := 0.0
				if *receipt.Total != 0 {
					pctDiff = (tx.Amount - *receipt.Total) / *receipt.Total
					if pctDiff < 0 {
						pctDiff = -pctDiff
					}
				}
				if pctDiff <= 0.05 { // within 5%
					result.AmountMatches = append(result.AmountMatches, tx)
				} else {
					result.AllUnmatched = append(result.AllUnmatched, tx)
				}
			}
		} else {
			result.AllUnmatched = filtered
		}
		return result, nil
	}

	// No search: get amount matches from receipt data, then all unmatched
	if receipt.Total != nil && *receipt.Total > 0 && receipt.Date != nil {
		dateStr := receipt.Date.Format("2006-01-02")
		candidates, err := s.txCacheRepo.FindUnmatchedCandidates(ctx, userID, *receipt.Total, dateStr)
		if err != nil {
			slog.Warn("failed to find amount match candidates", "error", err)
		} else {
			result.AmountMatches = candidates
		}
	}

	allUnmatched, err := s.txCacheRepo.FindAllUnmatched(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Exclude amount matches from allUnmatched to avoid duplicates
	amountMatchIDs := make(map[string]bool, len(result.AmountMatches))
	for _, tx := range result.AmountMatches {
		amountMatchIDs[tx.TransactionID] = true
	}
	for _, tx := range allUnmatched {
		if !amountMatchIDs[tx.TransactionID] {
			result.AllUnmatched = append(result.AllUnmatched, tx)
		}
	}

	return result, nil
}

func (s *ReceiptService) GetByIDWithMatch(ctx context.Context, userID, receiptID uuid.UUID) (*domain.ReceiptWithMatch, error) {
	rwm, err := s.receiptRepo.FindByIDWithMatch(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	if rwm.UserID != userID {
		return nil, domain.ErrForbidden
	}
	return rwm, nil
}


func (s *ReceiptService) Update(ctx context.Context, userID, receiptID uuid.UUID, updates map[string]interface{}) (*domain.Receipt, error) {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	if receipt.UserID != userID {
		return nil, domain.ErrForbidden
	}

	if v, ok := updates["merchant_name"]; ok {
		if s, ok := v.(string); ok {
			receipt.MerchantName = &s
		}
	}
	if v, ok := updates["total"]; ok {
		if f, ok := v.(float64); ok {
			receipt.Total = &f
		}
	}
	if v, ok := updates["date"]; ok {
		if s, ok := v.(string); ok {
			if t, err := time.Parse("2006-01-02", s); err == nil {
				receipt.Date = &t
			}
		}
	}
	if v, ok := updates["subtotal"]; ok {
		if f, ok := v.(float64); ok {
			receipt.Subtotal = &f
		}
	}
	if v, ok := updates["tax"]; ok {
		if f, ok := v.(float64); ok {
			receipt.Tax = &f
		}
	}
	if v, ok := updates["tip"]; ok {
		if f, ok := v.(float64); ok {
			receipt.Tip = &f
		}
	}
	if v, ok := updates["payment_method"]; ok {
		if s, ok := v.(string); ok {
			receipt.PaymentMethod = &s
		}
	}
	if v, ok := updates["last_four_digits"]; ok {
		if s, ok := v.(string); ok {
			receipt.LastFourDigits = &s
		}
	}
	if v, ok := updates["currency"]; ok {
		if s, ok := v.(string); ok {
			receipt.Currency = &s
		}
	}
	if v, ok := updates["line_items"]; ok {
		if items, err := json.Marshal(v); err == nil {
			receipt.LineItems = items
		}
	}

	if err := s.receiptRepo.Update(ctx, receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (s *ReceiptService) Delete(ctx context.Context, userID, receiptID uuid.UUID) error {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return err
	}
	if receipt.UserID != userID {
		return domain.ErrForbidden
	}

	// Delete file from disk
	if receipt.ImagePath != "" {
		_ = os.Remove(receipt.ImagePath)
	}

	return s.receiptRepo.Delete(ctx, receiptID)
}

// PromoteDuplicate converts a soft-duplicate row into a standalone primary.
// Used when the user reviews a likely-duplicate receipt and confirms it is
// actually a distinct charge that should be kept. Clears the `duplicate_of`
// link, resets status to "unmatched", and runs the auto-match pipeline so
// the promoted receipt can find its own transaction.
//
// Returns ErrBadRequest if the receipt isn't currently a duplicate.
func (s *ReceiptService) PromoteDuplicate(ctx context.Context, userID, receiptID uuid.UUID) (*domain.Receipt, error) {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return nil, err
	}
	if receipt.UserID != userID {
		return nil, domain.ErrForbidden
	}
	if receipt.DuplicateOf == nil {
		return nil, fmt.Errorf("receipt is not a duplicate: %w", domain.ErrBadRequest)
	}

	receipt.DuplicateOf = nil
	receipt.Status = "unmatched"
	if err := s.receiptRepo.Update(ctx, receipt); err != nil {
		return nil, fmt.Errorf("clear duplicate_of: %w", err)
	}

	s.TryMatch(ctx, userID, receipt)
	return receipt, nil
}

func (s *ReceiptService) Match(ctx context.Context, userID, receiptID uuid.UUID, transactionID, accountID string) error {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return err
	}
	if receipt.UserID != userID {
		return domain.ErrForbidden
	}

	// Check if receipt already has a match
	existingMatch, err := s.matchRepo.FindByReceiptID(ctx, receiptID)
	if err == nil {
		// Receipt already has a match
		// Allow overriding auto-generated or suggested matches, but not manual or confirmed ones
		if existingMatch.MatchMethod == "manual" || existingMatch.MatchMethod == "confirmed" {
			return fmt.Errorf("receipt already manually matched: %w", domain.ErrConflict)
		}

		// Allow override if user is linking to the same transaction (treat as success)
		if existingMatch.TransactionID == transactionID {
			slog.Info("receipt already matched to same transaction, treating as success",
				"receipt_id", receiptID,
				"transaction_id", transactionID,
				"existing_method", existingMatch.MatchMethod,
			)
			return nil
		}

		// Delete the auto/suggested match so we can create the manual one
		slog.Info("overriding auto/suggested match with manual link",
			"receipt_id", receiptID,
			"old_transaction_id", existingMatch.TransactionID,
			"new_transaction_id", transactionID,
			"old_method", existingMatch.MatchMethod,
		)
		if err := s.matchRepo.DeleteByReceiptID(ctx, receiptID); err != nil {
			return err
		}
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	// Check if transaction already has a different receipt matched
	existingTxMatch, err := s.matchRepo.FindByTransactionID(ctx, transactionID)
	if err == nil {
		// Transaction already has a receipt - don't allow overriding this
		return fmt.Errorf("transaction already matched to receipt %s: %w", existingTxMatch.ReceiptID, domain.ErrConflict)
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	// Clear the recently accessed marker since we're completing the link
	s.recentlyAccessed.Delete(receiptID)

	match := &domain.ReceiptMatch{
		ReceiptID:       receiptID,
		TransactionID:   transactionID,
		AccountID:       accountID,
		ConfidenceScore: 1.0,
		MatchMethod:     "manual",
	}
	if err := s.matchRepo.Create(ctx, match); err != nil {
		return err
	}
	return s.receiptRepo.UpdateStatus(ctx, receiptID, "matched")
}

// SuggestMatches runs the deterministic-first matching pipeline for all
// unmatched receipts. Returns the number of matches created.
func (s *ReceiptService) SuggestMatches(ctx context.Context, userID uuid.UUID) (int, error) {
	release, ok := s.acquireSuggestLock(userID)
	if !ok {
		slog.Debug("suggest-match skipped: run already in progress", "user_id", userID)
		return 0, nil
	}
	defer release()

	receipts, err := s.receiptRepo.FindUnmatchedValid(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("find unmatched receipts: %w", err)
	}
	if len(receipts) == 0 {
		return 0, nil
	}

	slog.Info("running deterministic matching pipeline",
		"user_id", userID,
		"unmatched_receipts", len(receipts),
	)

	matched := 0
	for i := range receipts {
		receipt := &receipts[i]

		// Skip receipts the user is actively working with (recently accessed link candidates).
		// This prevents race conditions where the user is manually linking a receipt while
		// the background matcher tries to auto-match it.
		if lastAccess, ok := s.recentlyAccessed.Load(receipt.ID); ok {
			if time.Since(lastAccess.(time.Time)) < 30*time.Second {
				slog.Debug("skipping recently accessed receipt in background matcher",
					"receipt_id", receipt.ID,
					"seconds_since_access", time.Since(lastAccess.(time.Time)).Seconds(),
				)
				continue
			}
			// Clean up old entries
			s.recentlyAccessed.Delete(receipt.ID)
		}

		result, err := s.matchingSvc.MatchReceipt(ctx, userID, receipt)
		if err != nil {
			slog.Error("suggest-match: pipeline error", "error", err, "receipt_id", receipt.ID)
			continue
		}
		if result == nil {
			continue
		}

		method := result.MatchType
		if method == "matched" {
			method = "auto"
		}

		match := &domain.ReceiptMatch{
			ReceiptID:       receipt.ID,
			TransactionID:   result.TransactionID,
			AccountID:       result.AccountID,
			ConfidenceScore: result.Confidence,
			MatchMethod:     method,
			MatchReason:     result.Reason,
		}
		if err := s.matchRepo.Create(ctx, match); err != nil {
			slog.Error("suggest-match: create match", "error", err, "receipt_id", receipt.ID)
			continue
		}
		if err := s.receiptRepo.UpdateStatus(ctx, receipt.ID, result.MatchType); err != nil {
			slog.Error("suggest-match: update status", "error", err, "receipt_id", receipt.ID)
			continue
		}
		matched++

		// Trigger category correction asynchronously.
		if s.categorySvc != nil && result.Transaction != nil {
			go s.categorySvc.CorrectCategoryAfterMatch(context.Background(), receipt, result.Transaction)
		}
	}

	return matched, nil
}

func (s *ReceiptService) acquireSuggestLock(userID uuid.UUID) (func(), bool) {
	v, _ := s.suggestLocks.LoadOrStore(userID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	if !mu.TryLock() {
		return nil, false
	}
	return mu.Unlock, true
}

// ConfirmSuggestion confirms a suggested match, changing status from "suggested" to "matched".
func (s *ReceiptService) ConfirmSuggestion(ctx context.Context, userID, receiptID uuid.UUID) error {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return err
	}
	if receipt.UserID != userID {
		return domain.ErrForbidden
	}
	if receipt.Status != "suggested" {
		return fmt.Errorf("receipt is not in suggested state: %w", domain.ErrBadRequest)
	}

	match, err := s.matchRepo.FindByReceiptID(ctx, receiptID)
	if err != nil {
		return fmt.Errorf("no match found for suggested receipt: %w", err)
	}

	if err := s.matchRepo.UpdateMethod(ctx, match.ID, "confirmed"); err != nil {
		return fmt.Errorf("update match method: %w", err)
	}

	if err := s.receiptRepo.UpdateStatus(ctx, receiptID, "matched"); err != nil {
		return err
	}

	// Trigger category correction when user confirms a suggested match.
	if s.categorySvc != nil {
		go func() {
			tx, err := s.txCacheRepo.FindByTransactionID(context.Background(), match.TransactionID)
			if err != nil {
				return
			}
			s.categorySvc.CorrectCategoryAfterMatch(context.Background(), receipt, tx)
		}()
	}

	return nil
}

// RejectSuggestion rejects a suggested match, deleting the match and reverting to "unmatched".
func (s *ReceiptService) RejectSuggestion(ctx context.Context, userID, receiptID uuid.UUID) error {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return err
	}
	if receipt.UserID != userID {
		return domain.ErrForbidden
	}
	if receipt.Status != "suggested" {
		return fmt.Errorf("receipt is not in suggested state: %w", domain.ErrBadRequest)
	}

	if err := s.matchRepo.DeleteByReceiptID(ctx, receiptID); err != nil {
		return fmt.Errorf("delete suggested match: %w", err)
	}

	return s.receiptRepo.UpdateStatus(ctx, receiptID, "unmatched")
}

func (s *ReceiptService) Unmatch(ctx context.Context, userID, receiptID uuid.UUID) error {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return err
	}
	if receipt.UserID != userID {
		return domain.ErrForbidden
	}

	if err := s.matchRepo.DeleteByReceiptID(ctx, receiptID); err != nil {
		return err
	}
	return s.receiptRepo.UpdateStatus(ctx, receiptID, "unmatched")
}

// GenerateThumbnailAsync generates a thumbnail for a receipt in the background.
// This runs asynchronously to avoid blocking the upload/scan response.
func (s *ReceiptService) GenerateThumbnailAsync(ctx context.Context, receiptID uuid.UUID, imageBytes []byte, mimeType, imageURL string, isHTML bool) {
	if s.thumbnailSvc == nil {
		slog.Debug("thumbnail service not configured, skipping", "receipt_id", receiptID)
		return
	}

	slog.Info("generating thumbnail", "receipt_id", receiptID, "is_html", isHTML)

	in := ThumbnailInput{
		ImageBytes: imageBytes,
		MimeType:   mimeType,
		ImageURL:   imageURL,
	}
	if isHTML && imageURL != "" {
		in.EmailHTMLURL = imageURL
		in.ImageURL = ""
	}
	thumbnailBytes, err := s.thumbnailSvc.GenerateReceiptThumbnail(ctx, in)
	if err != nil {
		slog.Error("thumbnail generation failed", "error", err, "receipt_id", receiptID)
		return
	}

	// Generate thumbnail filename
	thumbFilename := fmt.Sprintf("thumb_%s.png", receiptID.String())
	var thumbnailURL string

	// Upload thumbnail to storage
	if s.storageSvc != nil {
		// Fetch the receipt to get user_id for the storage key.
		receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
		if err != nil {
			slog.Error("fetch receipt for thumbnail upload", "error", err, "receipt_id", receiptID)
			return
		}

		key := fmt.Sprintf("receipts/%s/%s", receipt.UserID.String(), thumbFilename)

		if err := s.storageSvc.UploadPrivate(ctx, key, bytes.NewReader(thumbnailBytes), "image/png"); err != nil {
			slog.Error("thumbnail upload failed", "error", err, "receipt_id", receiptID)
			return
		}
		// Persist the object key; host resolves to a short-lived presigned URL
		// at read time so the underlying object can remain private.
		thumbnailURL = key
	} else {
		// Local filesystem fallback
		receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
		if err != nil {
			slog.Error("fetch receipt for thumbnail save", "error", err, "receipt_id", receiptID)
			return
		}

		// Derive local path from baseImageURL (e.g., "./uploads")
		// This is a bit fragile, but matches existing pattern
		uploadDir := filepath.Join("./uploads", "receipts", receipt.UserID.String())
		if err := os.MkdirAll(uploadDir, 0755); err != nil {
			slog.Error("create thumbnail directory", "error", err, "receipt_id", receiptID)
			return
		}

		destPath := filepath.Join(uploadDir, thumbFilename)
		if err := os.WriteFile(destPath, thumbnailBytes, 0644); err != nil {
			slog.Error("save thumbnail locally", "error", err, "receipt_id", receiptID)
			return
		}

		thumbnailURL = fmt.Sprintf("%s/receipts/%s/%s", s.baseImageURL, receipt.UserID.String(), thumbFilename)
	}

	// Update receipt with thumbnail URL
	if err := s.receiptRepo.UpdateThumbnailURL(ctx, receiptID, thumbnailURL); err != nil {
		slog.Error("update thumbnail URL", "error", err, "receipt_id", receiptID)
		return
	}

	slog.Info("thumbnail generated successfully", "receipt_id", receiptID)
}
