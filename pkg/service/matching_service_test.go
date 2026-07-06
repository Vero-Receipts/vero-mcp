package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Mock repository implementations
// ---------------------------------------------------------------------------

type mockAliasRepo struct {
	pairs map[string]bool // key: "a|b"
}

func (m *mockAliasRepo) FindCanonical(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockAliasRepo) AreSameMerchant(_ context.Context, a, b string) (bool, error) {
	if m.pairs == nil {
		return false, nil
	}
	return m.pairs[a+"|"+b], nil
}

func (m *mockAliasRepo) Create(_ context.Context, _ *domain.MerchantAlias) error {
	return nil
}

type mockAuditRepo struct {
	entries []domain.MatchAuditEntry
}

func (m *mockAuditRepo) Create(_ context.Context, e *domain.MatchAuditEntry) error {
	m.entries = append(m.entries, *e)
	return nil
}

// stubTxCacheRepo satisfies TransactionCacheRepository with no-op methods.
type stubTxCacheRepo struct{}

func (s *stubTxCacheRepo) UpsertBatch(_ context.Context, _ uuid.UUID, _ []domain.Transaction) (int, error) {
	return 0, nil
}
func (s *stubTxCacheRepo) FindByUserID(_ context.Context, _ uuid.UUID) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) FindByUserIDWithReceipts(_ context.Context, _ uuid.UUID, _ domain.TransactionFilter) ([]domain.TransactionWithReceipt, int, float64, error) {
	return nil, 0, 0, nil
}
func (s *stubTxCacheRepo) FindUnmatchedCandidates(_ context.Context, _ uuid.UUID, _ float64, _ string) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) FindAllUnmatched(_ context.Context, _ uuid.UUID) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) FindUnmatchedByDateRange(_ context.Context, _ uuid.UUID, _ string) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) FindUnmatchedTight(_ context.Context, _ uuid.UUID, _ float64, _ string, _ bool) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) RemoveBatch(_ context.Context, _ []string) error { return nil }
func (s *stubTxCacheRepo) SearchUnmatched(_ context.Context, _ uuid.UUID, _ string) ([]domain.Transaction, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) FindByTransactionID(_ context.Context, _ string) (*domain.Transaction, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) UpdateCorrectedCategory(_ context.Context, _ string, _, _ string) error {
	return nil
}
func (s *stubTxCacheRepo) FindRecurringCandidates(_ context.Context, _ uuid.UUID) ([]domain.RecurringCandidate, error) {
	return nil, nil
}
func (s *stubTxCacheRepo) SetRecurring(_ context.Context, _ []string) error { return nil }

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func makeMatchingService(t *testing.T, ts *httptest.Server, audit *mockAuditRepo, alias *mockAliasRepo) *MatchingService {
	t.Helper()
	var openAISvc *OpenAIService
	if ts != nil {
		openAISvc = newTestOpenAIService("test-key", ts)
	} else {
		openAISvc = NewOpenAIService("test-key")
	}
	scoringSvc := NewScoringService(alias)
	return NewMatchingService(&stubTxCacheRepo{}, alias, audit, scoringSvc, openAISvc)
}

func testReceipt(merchantName string, total float64) *domain.Receipt {
	d := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	m := merchantName
	return &domain.Receipt{
		Date:         &d,
		Total:        &total,
		MerchantName: &m,
	}
}

func testTransaction(txID string, amount float64, name string) *domain.Transaction {
	return &domain.Transaction{
		TransactionID: txID,
		AccountID:     "acct-123",
		Amount:        amount,
		Date:          "2025-03-15",
		Name:          name,
	}
}

func testScores(merchantScore, amountScore, dateScore float64, method string, amtDiffPct float64, dateDiffDays int) *domain.CandidateScores {
	return &domain.CandidateScores{
		TransactionID:  "tx-test",
		MerchantScore:  merchantScore,
		MerchantMethod: method,
		AmountScore:    amountScore,
		DateScore:      dateScore,
		CompositeScore: merchantScore*0.40 + amountScore*0.35 + dateScore*0.25,
		AmountDiffPct:  amtDiffPct,
		DateDiffDays:   dateDiffDays,
	}
}

func disambiguateServer(t *testing.T, result MerchantDisambiguationResult) *httptest.Server {
	t.Helper()
	contentJSON, _ := json.Marshal(result)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": string(contentJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func errorServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
	}))
}

// ---------------------------------------------------------------------------
// evaluateCandidate tests
// ---------------------------------------------------------------------------

func TestEvaluateCandidate_HighMerchantNoLLM(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "STARBUCKS #123")
	cs := testScores(0.90, 0.85, 1.0, "exact", 0.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MatchType != "matched" {
		t.Errorf("MatchType = %q, want \"matched\"", result.MatchType)
	}
	if result.LLMUsed {
		t.Error("expected LLMUsed=false for high merchant score")
	}
	if result.Confidence < 0.85 {
		t.Errorf("Confidence = %.4f, want >= 0.85", result.Confidence)
	}
}

func TestEvaluateCandidate_HighMerchantWeakAmount(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "Starbucks")
	cs := testScores(0.90, 0.70, 0.50, "exact", 0.0, 4)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\"", result.MatchType)
	}
	if result.Confidence < 0.70 {
		t.Errorf("Confidence = %.4f, want >= 0.70", result.Confidence)
	}
}

func TestEvaluateCandidate_LLMConfirms_Matched(t *testing.T) {
	ts := disambiguateServer(t, MerchantDisambiguationResult{
		SameBusiness: true,
		Confidence:   0.95,
		Reason:       "SBUX is a known abbreviation for Starbucks",
	})
	defer ts.Close()

	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, ts, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "SBUX")
	cs := testScores(0.45, 0.85, 1.0, "word_overlap_1", 0.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MatchType != "matched" {
		t.Errorf("MatchType = %q, want \"matched\"", result.MatchType)
	}
	if !result.LLMUsed {
		t.Error("expected LLMUsed=true")
	}
	if result.Scores.MerchantScore != 0.85 {
		t.Errorf("MerchantScore = %.2f, want 0.85 (boosted by LLM confirmation)", result.Scores.MerchantScore)
	}
}

func TestEvaluateCandidate_LLMRejects(t *testing.T) {
	ts := disambiguateServer(t, MerchantDisambiguationResult{
		SameBusiness: false,
		Confidence:   0.90,
		Reason:       "Apple Store and Applebee's are different companies",
	})
	defer ts.Close()

	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, ts, audit, &mockAliasRepo{})

	receipt := testReceipt("Apple Store", 10.00)
	tx := testTransaction("tx-1", 10.00, "APPLEBEES")
	cs := testScores(0.45, 0.85, 1.0, "word_overlap_1", 0.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when LLM rejects, got %+v", result)
	}
	if len(audit.entries) == 0 {
		t.Fatal("expected audit entry for rejection")
	}
	if audit.entries[0].Outcome != "rejected" {
		t.Errorf("audit Outcome = %q, want \"rejected\"", audit.entries[0].Outcome)
	}
	if !strings.Contains(audit.entries[0].Reason, "LLM rejected") {
		t.Errorf("audit Reason = %q, want to contain \"LLM rejected\"", audit.entries[0].Reason)
	}
}

func TestEvaluateCandidate_LowMerchantScore(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "Netflix")
	cs := testScores(0.20, 0.95, 1.0, "none", 0.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for low merchant score, got %+v", result)
	}
	if len(audit.entries) == 0 {
		t.Fatal("expected audit entry for rejection")
	}
	if !strings.Contains(audit.entries[0].Reason, "merchant score too low") {
		t.Errorf("audit Reason = %q, want to contain \"merchant score too low\"", audit.entries[0].Reason)
	}
}

func TestEvaluateCandidate_MissingMerchantNames(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	// Receipt has no merchant name — LLM can't disambiguate.
	total := 10.0
	d := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	receipt := &domain.Receipt{Total: &total, Date: &d}

	tx := testTransaction("tx-1", 10.00, "STARBUCKS")
	cs := testScores(0.45, 0.85, 1.0, "word_overlap_1", 0.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when merchant names missing, got %+v", result)
	}
	if len(audit.entries) == 0 {
		t.Fatal("expected audit entry")
	}
	if !strings.Contains(audit.entries[0].Reason, "missing merchant names") {
		t.Errorf("audit Reason = %q, want to contain \"missing merchant names\"", audit.entries[0].Reason)
	}
}

func TestEvaluateCandidate_LLMError_StrongFallback(t *testing.T) {
	ts := errorServer(t, http.StatusInternalServerError)
	defer ts.Close()

	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, ts, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "SBUX")
	// Meets fallback threshold: MerchantScore >= 0.45, AmountScore >= 0.85, DateScore >= 0.70
	cs := testScores(0.45, 0.90, 0.80, "word_overlap_1", 2.0, 2)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result (LLM fallback)")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\"", result.MatchType)
	}
	if result.LLMUsed {
		t.Error("expected LLMUsed=false (LLM failed)")
	}
	if !strings.Contains(result.Reason, "LLM unavailable") {
		t.Errorf("Reason = %q, want to contain \"LLM unavailable\"", result.Reason)
	}
	if result.Confidence > 0.75 {
		t.Errorf("Confidence = %.4f, want <= 0.75 (capped for unconfirmed merchant)", result.Confidence)
	}
}

func TestEvaluateCandidate_LLMError_WeakReject(t *testing.T) {
	ts := errorServer(t, http.StatusInternalServerError)
	defer ts.Close()

	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, ts, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "SBUX")
	// Does NOT meet fallback threshold: AmountScore < 0.85
	cs := testScores(0.45, 0.70, 0.50, "word_overlap_1", 8.0, 4)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result (LLM error, weak signals), got %+v", result)
	}
	if len(audit.entries) == 0 {
		t.Fatal("expected audit entry for rejection")
	}
	if !strings.Contains(audit.entries[0].Reason, "LLM error") {
		t.Errorf("audit Reason = %q, want to contain \"LLM error\"", audit.entries[0].Reason)
	}
}

func TestEvaluateCandidate_Flag_AmountMismatch(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "Starbucks")
	cs := testScores(0.90, 0.70, 1.0, "exact", 8.0, 0) // AmountDiffPct=8 > 5
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Flag != "amount_mismatch" {
		t.Errorf("Flag = %q, want \"amount_mismatch\"", result.Flag)
	}
}

func TestEvaluateCandidate_Flag_DateMismatch(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "Starbucks")
	cs := testScores(0.90, 0.85, 0.70, "exact", 1.0, 3) // DateDiffDays=3 > 2, AmountDiffPct=1 <= 5
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Flag != "date_mismatch" {
		t.Errorf("Flag = %q, want \"date_mismatch\"", result.Flag)
	}
}

func TestEvaluateCandidate_Flag_Clean(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "Starbucks")
	cs := testScores(0.90, 0.85, 1.0, "exact", 1.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Flag != "clean" {
		t.Errorf("Flag = %q, want \"clean\"", result.Flag)
	}
}

func testReceiptNoMerchant(total float64) *domain.Receipt {
	d := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	return &domain.Receipt{
		Date:  &d,
		Total: &total,
	}
}

func TestEvaluateCandidate_NoMerchant_StrictMatch(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceiptNoMerchant(10.00)
	tx := testTransaction("tx-1", 10.00, "SOME STORE")
	// AmountScore=1.0 (exact), DateScore=1.0 (same day) — passes the strict threshold.
	cs := testScores(0, 1.0, 1.0, "none", 0.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a result for exact amount+date with no merchant")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\"", result.MatchType)
	}
	if result.Flag != "no_merchant" {
		t.Errorf("Flag = %q, want \"no_merchant\"", result.Flag)
	}
	if result.Confidence > 0.75 {
		t.Errorf("Confidence = %.4f, want <= 0.75 (capped for unconfirmed merchant)", result.Confidence)
	}
}

func TestEvaluateCandidate_NoMerchant_AmountTooLoose(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceiptNoMerchant(10.00)
	tx := testTransaction("tx-1", 10.00, "SOME STORE")
	// AmountScore=0.85 (≤5% diff) — below the 0.95 threshold for no-merchant path.
	cs := testScores(0, 0.85, 1.0, "none", 3.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when amount score %.2f is below 0.95 with no merchant", cs.AmountScore)
	}
}

func TestEvaluateCandidate_NoMerchant_DateTooLoose(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceiptNoMerchant(10.00)
	tx := testTransaction("tx-1", 10.00, "SOME STORE")
	// DateScore=0.85 (2 days off) — below the 0.95 threshold for no-merchant path.
	cs := testScores(0, 1.0, 0.85, "none", 0.0, 2)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when date score %.2f is below 0.95 with no merchant", cs.DateScore)
	}
}
