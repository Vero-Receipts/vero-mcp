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

// stubTxCacheRepo satisfies TransactionCacheRepository with no-op methods. The
// three candidate-query fields let a test choose which recall path it is
// exercising: MatchReceipt picks one based on what the receipt carries.
type stubTxCacheRepo struct {
	tight    []domain.Transaction
	byDate   []domain.Transaction
	byAmount []domain.Transaction
}

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
func (s *stubTxCacheRepo) FindUnmatchedTight(_ context.Context, _, _ uuid.UUID, _ float64, _ string, _ bool) ([]domain.Transaction, error) {
	return s.tight, nil
}
func (s *stubTxCacheRepo) FindUnmatchedByDateOnly(_ context.Context, _, _ uuid.UUID, _ string) ([]domain.Transaction, error) {
	return s.byDate, nil
}
func (s *stubTxCacheRepo) FindUnmatchedByAmountOnly(_ context.Context, _, _ uuid.UUID, _ float64, _ time.Time, _ bool) ([]domain.Transaction, error) {
	return s.byAmount, nil
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
	return makeMatchingServiceWithTxRepo(t, ts, audit, alias, &stubTxCacheRepo{})
}

func makeMatchingServiceWithTxRepo(t *testing.T, ts *httptest.Server, audit *mockAuditRepo, alias *mockAliasRepo, txRepo *stubTxCacheRepo) *MatchingService {
	t.Helper()
	var openAISvc *OpenAIService
	if ts != nil {
		openAISvc = newTestOpenAIService("test-key", ts)
	} else {
		openAISvc = NewOpenAIService("test-key")
	}
	scoringSvc := NewScoringService(alias)
	return NewMatchingService(txRepo, alias, audit, scoringSvc, openAISvc)
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

// testScores builds a fully-populated candidate — all three dimensions present
// and readable. Tests covering an unreadable dimension clear the matching
// *Known flag afterwards.
func testScores(merchantScore, amountScore, dateScore float64, method string, amtDiffPct float64, dateDiffDays int) *domain.CandidateScores {
	cs := &domain.CandidateScores{
		TransactionID:  "tx-test",
		MerchantScore:  merchantScore,
		MerchantMethod: method,
		AmountScore:    amountScore,
		DateScore:      dateScore,
		AmountDiffPct:  amtDiffPct,
		DateDiffDays:   dateDiffDays,
		AmountKnown:    true,
		DateKnown:      true,
		MerchantKnown:  true,
	}
	cs.CompositeScore = cs.Composite()
	return cs
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
	// An LLM "different businesses" verdict settles the merchant dimension as
	// disagreeing — it does not kill the candidate. Amount and date both hold,
	// so this still reaches the user as a proposal to accept or dismiss.
	if result == nil {
		t.Fatal("expected a suggestion: merchant disagrees but amount and date are strong")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\"", result.MatchType)
	}
	if result.Flag != domain.FlagMerchantMismatch {
		t.Errorf("Flag = %q, want %q", result.Flag, domain.FlagMerchantMismatch)
	}
	if result.Confidence > 0.75 {
		t.Errorf("Confidence = %.4f, want <= 0.75 (capped for unconfirmed merchant)", result.Confidence)
	}
	if len(audit.entries) == 0 {
		t.Fatal("expected audit entry")
	}
	if audit.entries[0].Outcome != "suggested" {
		t.Errorf("audit Outcome = %q, want \"suggested\"", audit.entries[0].Outcome)
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
	// Merchant scoring too low to even be worth an LLM call is still only the
	// merchant dimension disagreeing. Amount and date carry it to a suggestion.
	if result == nil {
		t.Fatal("expected a suggestion: merchant is weak but amount and date are strong")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\"", result.MatchType)
	}
	if result.Flag != domain.FlagMerchantMismatch {
		t.Errorf("Flag = %q, want %q", result.Flag, domain.FlagMerchantMismatch)
	}
	if result.LLMUsed {
		t.Error("expected no LLM call for a merchant score below the disambiguation band")
	}
}

func TestEvaluateCandidate_MissingMerchantNames(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	// Receipt has no merchant name — there is nothing for the LLM to compare.
	total := 10.0
	d := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	receipt := &domain.Receipt{Total: &total, Date: &d}

	tx := testTransaction("tx-1", 10.00, "STARBUCKS")
	cs := testScores(0.45, 0.85, 1.0, "word_overlap_1", 0.0, 0)
	cs.TransactionID = "tx-1"
	cs.MerchantKnown = false

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a suggestion: merchant is unreadable but amount and date are strong")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\"", result.MatchType)
	}
	if result.Flag != domain.FlagNoMerchant {
		t.Errorf("Flag = %q, want %q", result.Flag, domain.FlagNoMerchant)
	}
	if result.LLMUsed {
		t.Error("expected no LLM call when the receipt carries no merchant name")
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
	// Merchant unconfirmed, amount only tolerable, date only tolerable — no two
	// dimensions agree strongly, so there is nothing worth asking the user about.
	if result != nil {
		t.Errorf("expected nil result (LLM error, weak signals), got %+v", result)
	}
	if len(audit.entries) == 0 {
		t.Fatal("expected audit entry for rejection")
	}
	if audit.entries[0].Outcome != "rejected" {
		t.Errorf("audit Outcome = %q, want \"rejected\"", audit.entries[0].Outcome)
	}
	if !strings.Contains(audit.entries[0].Reason, "too few agreeing dimensions") {
		t.Errorf("audit Reason = %q, want to contain \"too few agreeing dimensions\"", audit.entries[0].Reason)
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
	cs := testScores(0, 1.0, 1.0, "none", 0.0, 0)
	cs.TransactionID = "tx-1"
	cs.MerchantKnown = false

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
	if result.Flag != domain.FlagNoMerchant {
		t.Errorf("Flag = %q, want %q", result.Flag, domain.FlagNoMerchant)
	}
	if result.Confidence > 0.75 {
		t.Errorf("Confidence = %.4f, want <= 0.75 (capped for unconfirmed merchant)", result.Confidence)
	}
}

// ---------------------------------------------------------------------------
// The decide table: which single dimension may be absent or disagreeing.
//
// Merchant is forgiven in any state. Amount and date may be absent — an
// unreadable total or date is a fact about the receipt, not evidence against
// the candidate — but a known one that disagrees badly is a veto, because
// there is no reading under which a $40 charge settles a $100 receipt.
// ---------------------------------------------------------------------------

func TestEvaluateCandidate_NoAmount_MerchantAndDateStrong(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	// Total unreadable (faded thermal print), merchant and date crisp.
	d := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	m := "Starbucks"
	receipt := &domain.Receipt{Date: &d, MerchantName: &m}

	tx := testTransaction("tx-1", 10.00, "STARBUCKS #123")
	cs := testScores(0.90, 0, 1.0, "exact", 0, 0)
	cs.TransactionID = "tx-1"
	cs.AmountKnown = false
	cs.CompositeScore = cs.Composite()

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a suggestion: amount is unreadable but merchant and date are strong")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\" (an unreadable total must never auto-match)", result.MatchType)
	}
	if result.Flag != domain.FlagNoAmount {
		t.Errorf("Flag = %q, want %q", result.Flag, domain.FlagNoAmount)
	}
}

func TestEvaluateCandidate_NoDate_MerchantAndAmountStrong(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	total := 10.0
	m := "Starbucks"
	receipt := &domain.Receipt{Total: &total, MerchantName: &m}

	tx := testTransaction("tx-1", 10.00, "STARBUCKS #123")
	cs := testScores(0.90, 1.0, 0, "exact", 0.0, 0)
	cs.TransactionID = "tx-1"
	cs.DateKnown = false
	cs.CompositeScore = cs.Composite()

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a suggestion: date is unreadable but merchant and amount are strong")
	}
	if result.MatchType != "suggested" {
		t.Errorf("MatchType = %q, want \"suggested\" (an unreadable date must never auto-match)", result.MatchType)
	}
	if result.Flag != domain.FlagNoDate {
		t.Errorf("Flag = %q, want %q", result.Flag, domain.FlagNoDate)
	}
}

func TestEvaluateCandidate_Veto_AmountFarOff(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 8.20, "STARBUCKS #123")
	// Merchant exact and same day, but the charge is 18% off the receipt total.
	cs := testScores(0.90, 0.40, 1.0, "exact", 18.0, 0)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil: a known amount this far off is a veto, not a suggestion; got %+v", result)
	}
}

func TestEvaluateCandidate_Veto_DateFarOff(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	receipt := testReceipt("Starbucks", 10.00)
	tx := testTransaction("tx-1", 10.00, "STARBUCKS #123")
	// Merchant exact and amount exact, but the receipt is dated well outside
	// the window. scoreDate would have dropped this in the scorer; assert the
	// decision layer refuses it too, so neither path can leak it through.
	cs := testScores(0.90, 1.0, 0.20, "exact", 0.0, 9)
	cs.TransactionID = "tx-1"

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil: a known date this far off is a veto, not a suggestion; got %+v", result)
	}
}

func TestEvaluateCandidate_TwoDimensionsMissingOrWeak_Rejected(t *testing.T) {
	audit := &mockAuditRepo{}
	svc := makeMatchingService(t, nil, audit, &mockAliasRepo{})

	total := 10.0
	m := "Starbucks"
	receipt := &domain.Receipt{Total: &total, MerchantName: &m}

	tx := testTransaction("tx-1", 10.00, "SOME OTHER STORE")
	// No date, and the merchant does not resolve either — only one dimension
	// left standing, which is never enough.
	cs := testScores(0.20, 1.0, 0, "none", 0.0, 0)
	cs.TransactionID = "tx-1"
	cs.DateKnown = false
	cs.CompositeScore = cs.Composite()

	result, err := svc.evaluateCandidate(context.Background(), receipt, tx, cs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil when only one dimension agrees, got %+v", result)
	}
}

// ---------------------------------------------------------------------------
// MatchReceipt: the full pipeline — candidate recall, ranking, and how an
// auto-match relates to the suggestions around it.
// ---------------------------------------------------------------------------

func matchReceiptSvc(t *testing.T, txRepo *stubTxCacheRepo) *MatchingService {
	t.Helper()
	return makeMatchingServiceWithTxRepo(t, nil, &mockAuditRepo{}, &mockAliasRepo{}, txRepo)
}

func TestMatchReceipt_AutoMatchShortCircuits(t *testing.T) {
	txRepo := &stubTxCacheRepo{tight: []domain.Transaction{
		*testTransaction("tx-exact", 10.00, "STARBUCKS #123"),
		*testTransaction("tx-other", 10.20, "STARBUCKS #999"),
	}}
	svc := matchReceiptSvc(t, txRepo)

	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), testReceipt("Starbucks", 10.00))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome == nil || outcome.Auto == nil {
		t.Fatalf("expected an auto-match, got %+v", outcome)
	}
	if outcome.Auto.TransactionID != "tx-exact" {
		t.Errorf("Auto.TransactionID = %q, want \"tx-exact\"", outcome.Auto.TransactionID)
	}
	// A settled link makes alternatives noise, not choice.
	if len(outcome.Suggestions) != 0 {
		t.Errorf("expected no suggestions alongside an auto-match, got %d", len(outcome.Suggestions))
	}
}

func TestMatchReceipt_RanksMultipleSuggestions(t *testing.T) {
	// Three same-day, same-amount charges at merchants that do not resolve to
	// the receipt's. None can auto-match; all three are worth asking about.
	txRepo := &stubTxCacheRepo{tight: []domain.Transaction{
		*testTransaction("tx-a", 10.00, "UNRELATED ALPHA"),
		*testTransaction("tx-b", 10.00, "UNRELATED BETA"),
		*testTransaction("tx-c", 10.00, "UNRELATED GAMMA"),
	}}
	svc := matchReceiptSvc(t, txRepo)

	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), testReceipt("Starbucks", 10.00))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected suggestions, got nil outcome")
	}
	if outcome.Auto != nil {
		t.Errorf("expected no auto-match when merchant never resolves, got %+v", outcome.Auto)
	}
	if len(outcome.Suggestions) != 3 {
		t.Fatalf("len(Suggestions) = %d, want 3", len(outcome.Suggestions))
	}
	for i, s := range outcome.Suggestions {
		if s.MatchType != "suggested" {
			t.Errorf("Suggestions[%d].MatchType = %q, want \"suggested\"", i, s.MatchType)
		}
		if s.Flag != domain.FlagMerchantMismatch {
			t.Errorf("Suggestions[%d].Flag = %q, want %q", i, s.Flag, domain.FlagMerchantMismatch)
		}
		if i > 0 && outcome.Suggestions[i-1].Confidence < s.Confidence {
			t.Errorf("suggestions not ranked: [%d]=%.4f before [%d]=%.4f",
				i-1, outcome.Suggestions[i-1].Confidence, i, s.Confidence)
		}
	}
}

func TestMatchReceipt_CapsSuggestions(t *testing.T) {
	var candidates []domain.Transaction
	for i := 0; i < 6; i++ {
		candidates = append(candidates, *testTransaction(
			"tx-"+string(rune('a'+i)), 10.00, "UNRELATED STORE "+string(rune('A'+i))))
	}
	svc := matchReceiptSvc(t, &stubTxCacheRepo{tight: candidates})

	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), testReceipt("Starbucks", 10.00))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome == nil {
		t.Fatal("expected suggestions, got nil outcome")
	}
	if len(outcome.Suggestions) != maxSuggestions {
		t.Errorf("len(Suggestions) = %d, want %d", len(outcome.Suggestions), maxSuggestions)
	}
}

func TestMatchReceipt_NoAmount_UsesDateAnchoredRecall(t *testing.T) {
	txRepo := &stubTxCacheRepo{
		tight:  []domain.Transaction{*testTransaction("tx-wrong", 10.00, "STARBUCKS")},
		byDate: []domain.Transaction{*testTransaction("tx-right", 42.00, "STARBUCKS #123")},
	}
	svc := matchReceiptSvc(t, txRepo)

	d := time.Date(2025, 3, 15, 0, 0, 0, 0, time.UTC)
	m := "Starbucks"
	receipt := &domain.Receipt{Date: &d, MerchantName: &m}

	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), receipt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome == nil || len(outcome.Suggestions) == 0 {
		t.Fatalf("expected a suggestion from the date-anchored query, got %+v", outcome)
	}
	if outcome.Auto != nil {
		t.Error("a receipt with no readable total must never auto-match")
	}
	if got := outcome.Suggestions[0].TransactionID; got != "tx-right" {
		t.Errorf("TransactionID = %q, want \"tx-right\" (amount-anchored query must not be used)", got)
	}
	if outcome.Suggestions[0].Flag != domain.FlagNoAmount {
		t.Errorf("Flag = %q, want %q", outcome.Suggestions[0].Flag, domain.FlagNoAmount)
	}
}

func TestMatchReceipt_NoDate_UsesAmountAnchoredRecall(t *testing.T) {
	txRepo := &stubTxCacheRepo{
		tight:    []domain.Transaction{*testTransaction("tx-wrong", 10.00, "STARBUCKS")},
		byAmount: []domain.Transaction{*testTransaction("tx-right", 10.00, "STARBUCKS #123")},
	}
	svc := matchReceiptSvc(t, txRepo)

	total := 10.0
	m := "Starbucks"
	receipt := &domain.Receipt{Total: &total, MerchantName: &m, CreatedAt: time.Now()}

	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), receipt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome == nil || len(outcome.Suggestions) == 0 {
		t.Fatalf("expected a suggestion from the amount-anchored query, got %+v", outcome)
	}
	if outcome.Auto != nil {
		t.Error("a receipt with no readable date must never auto-match")
	}
	if got := outcome.Suggestions[0].TransactionID; got != "tx-right" {
		t.Errorf("TransactionID = %q, want \"tx-right\"", got)
	}
	if outcome.Suggestions[0].Flag != domain.FlagNoDate {
		t.Errorf("Flag = %q, want %q", outcome.Suggestions[0].Flag, domain.FlagNoDate)
	}
}

func TestMatchReceipt_NoAmountNoDate_SkipsEntirely(t *testing.T) {
	txRepo := &stubTxCacheRepo{
		tight:    []domain.Transaction{*testTransaction("tx-a", 10.00, "STARBUCKS")},
		byDate:   []domain.Transaction{*testTransaction("tx-b", 10.00, "STARBUCKS")},
		byAmount: []domain.Transaction{*testTransaction("tx-c", 10.00, "STARBUCKS")},
	}
	svc := matchReceiptSvc(t, txRepo)

	m := "Starbucks"
	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), &domain.Receipt{MerchantName: &m})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A merchant name alone anchors nothing — every charge at that merchant
	// would score identically.
	if outcome != nil {
		t.Errorf("expected nil outcome with neither amount nor date, got %+v", outcome)
	}
}

func TestMatchReceipt_AmountFarOff_YieldsNothing(t *testing.T) {
	// Merchant exact, same day, but the only candidate is 40% off. The scorer
	// vetoes it and the receipt is left alone rather than badly suggested.
	txRepo := &stubTxCacheRepo{tight: []domain.Transaction{
		*testTransaction("tx-far", 6.00, "STARBUCKS #123"),
	}}
	svc := matchReceiptSvc(t, txRepo)

	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), testReceipt("Starbucks", 10.00))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != nil {
		t.Errorf("expected nil outcome for a 40%% amount gap, got %+v", outcome)
	}
}

func TestMatchReceipt_DateFarOff_YieldsNothing(t *testing.T) {
	tx := testTransaction("tx-far", 10.00, "STARBUCKS #123")
	tx.Date = "2025-03-27" // 12 days from the receipt date
	svc := matchReceiptSvc(t, &stubTxCacheRepo{tight: []domain.Transaction{*tx}})

	outcome, err := svc.MatchReceipt(context.Background(), uuid.New(), testReceipt("Starbucks", 10.00))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != nil {
		t.Errorf("expected nil outcome for a 12-day date gap, got %+v", outcome)
	}
}
