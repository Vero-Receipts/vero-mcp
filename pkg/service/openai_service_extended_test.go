package service

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"context"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// redirectTransport rewrites every outbound request URL to point at the test server.
type redirectTransport struct {
	target    string
	transport http.RoundTripper
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.target, "http://")
	if t.transport != nil {
		return t.transport.RoundTrip(req)
	}
	return http.DefaultTransport.RoundTrip(req)
}

// newTestOpenAIService creates an OpenAIService whose HTTP client is redirected to ts.
func newTestOpenAIService(apiKey string, ts *httptest.Server) *OpenAIService {
	svc := NewOpenAIService(apiKey)
	svc.httpClient = &http.Client{
		Timeout:   5 * time.Second,
		Transport: &redirectTransport{target: ts.URL},
	}
	return svc
}

// ---------------------------------------------------------------------------
// ParseImageData
// ---------------------------------------------------------------------------

func TestParseImageData_NoAPIKey(t *testing.T) {
	svc := NewOpenAIService("")
	result := svc.ParseImageData(context.Background(), []byte("fake-image"), "image/jpeg")
	if result.Error == "" {
		t.Fatal("expected error when API key is empty")
	}
	if !strings.Contains(result.Error, "not configured") {
		t.Errorf("expected 'not configured' in error, got %q", result.Error)
	}
}

func TestParseImageData_Success(t *testing.T) {
	receiptData := openAIReceiptData{
		MerchantName:    "Test Coffee",
		MerchantAddress: "123 Main St",
		TransactionDate: "2025-03-15",
		TransactionTime: "14:30",
		Subtotal:        4.50,
		Tax:             0.50,
		Tip:             1.00,
		Total:           6.00,
		Currency:        "USD",
		PaymentMethod:   "Credit Card",
		LastFourDigits:  "4242",
		LineItems: []openAILineItem{
			{Description: "Latte", Quantity: 1, UnitPrice: 4.50, TotalPrice: 4.50},
		},
	}
	contentJSON, _ := json.Marshal(receiptData)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": string(contentJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	result := svc.ParseImageData(context.Background(), []byte("fake-image"), "image/jpeg")

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.MerchantName != "Test Coffee" {
		t.Errorf("expected Test Coffee, got %s", result.MerchantName)
	}
	if result.Total == nil || *result.Total != 6.00 {
		t.Errorf("expected total 6.00, got %v", result.Total)
	}
	if result.Currency != "USD" {
		t.Errorf("expected USD, got %s", result.Currency)
	}
	if len(result.LineItems) != 1 {
		t.Errorf("expected 1 line item, got %d", len(result.LineItems))
	}
	if result.TransactionDate != "2025-03-15" {
		t.Errorf("expected date 2025-03-15, got %s", result.TransactionDate)
	}
}

func TestParseImageData_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "internal server error"}}`))
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	result := svc.ParseImageData(context.Background(), []byte("fake-image"), "image/jpeg")

	if result.Error == "" {
		t.Fatal("expected error from API failure")
	}
	if !strings.Contains(result.Error, "500") {
		t.Errorf("expected status code in error, got %q", result.Error)
	}
}

func TestParseImageData_OpenAIErrorField(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"error": map[string]string{"message": "rate limit exceeded"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	result := svc.ParseImageData(context.Background(), []byte("fake-image"), "image/jpeg")

	if result.Error == "" {
		t.Fatal("expected error from OpenAI error field")
	}
	if !strings.Contains(result.Error, "rate limit") {
		t.Errorf("expected 'rate limit' in error, got %q", result.Error)
	}
}

func TestParseImageData_EmptyChoices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	result := svc.ParseImageData(context.Background(), []byte("fake-image"), "image/jpeg")

	if result.Error == "" {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(result.Error, "no choices") {
		t.Errorf("expected 'no choices' in error, got %q", result.Error)
	}
}

// ---------------------------------------------------------------------------
// DisambiguateMerchant
// ---------------------------------------------------------------------------

func TestDisambiguateMerchant_NoAPIKey(t *testing.T) {
	svc := NewOpenAIService("")
	_, err := svc.DisambiguateMerchant(context.Background(), "Starbucks", "SBUX", "STARBUCKS #123")
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestDisambiguateMerchant_Success(t *testing.T) {
	disambResult := MerchantDisambiguationResult{
		SameBusiness: true,
		Confidence:   0.95,
		Reason:       "SBUX is a known abbreviation for Starbucks",
	}
	contentJSON, _ := json.Marshal(disambResult)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": string(contentJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	result, err := svc.DisambiguateMerchant(context.Background(), "Starbucks", "SBUX", "STARBUCKS #123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.SameBusiness {
		t.Error("expected SameBusiness=true")
	}
	if result.Confidence != 0.95 {
		t.Errorf("expected confidence 0.95, got %f", result.Confidence)
	}
}

func TestDisambiguateMerchant_Failure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`server error`))
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	_, err := svc.DisambiguateMerchant(context.Background(), "A", "B", "B")
	if err == nil {
		t.Fatal("expected error from API failure")
	}
}

func TestDisambiguateMerchant_NotSameBusiness(t *testing.T) {
	disambResult := MerchantDisambiguationResult{
		SameBusiness: false,
		Confidence:   0.90,
		Reason:       "Different companies",
	}
	contentJSON, _ := json.Marshal(disambResult)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": string(contentJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	result, err := svc.DisambiguateMerchant(context.Background(), "Apple", "Applebee's", "APPLEBEES")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SameBusiness {
		t.Error("expected SameBusiness=false")
	}
}

// ---------------------------------------------------------------------------
// CorrectCategory
// ---------------------------------------------------------------------------

func TestCorrectCategory_NoAPIKey(t *testing.T) {
	svc := NewOpenAIService("")
	_, err := svc.CorrectCategory(context.Background(), []domain.LineItem{{Description: "Latte"}}, "GENERAL_MERCHANDISE", "GENERAL_MERCHANDISE_OTHER")
	if err == nil {
		t.Fatal("expected error when API key is empty")
	}
}

func TestCorrectCategory_ShouldCorrect(t *testing.T) {
	corrResult := domain.CategoryCorrectionResult{
		ShouldCorrect:        true,
		CorrectedPFCPrimary:  "FOOD_AND_DRINK",
		CorrectedPFCDetailed: "FOOD_AND_DRINK_COFFEE",
		Reason:               "Items are coffee drinks",
	}
	contentJSON, _ := json.Marshal(corrResult)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": string(contentJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	lineItems := []domain.LineItem{
		{Description: "Iced Latte", Quantity: 1, Price: 5.50},
		{Description: "Scone", Quantity: 1, Price: 3.00},
	}

	result, err := svc.CorrectCategory(context.Background(), lineItems, "GENERAL_MERCHANDISE", "GENERAL_MERCHANDISE_OTHER")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.ShouldCorrect {
		t.Error("expected ShouldCorrect=true")
	}
	if result.CorrectedPFCPrimary != "FOOD_AND_DRINK" {
		t.Errorf("expected FOOD_AND_DRINK, got %s", result.CorrectedPFCPrimary)
	}
	if result.Source != "llm" {
		t.Errorf("expected source llm, got %s", result.Source)
	}
}

func TestCorrectCategory_NoCorrection(t *testing.T) {
	corrResult := domain.CategoryCorrectionResult{
		ShouldCorrect:        false,
		CorrectedPFCPrimary:  "FOOD_AND_DRINK",
		CorrectedPFCDetailed: "FOOD_AND_DRINK_RESTAURANT",
		Reason:               "Category is already correct",
	}
	contentJSON, _ := json.Marshal(corrResult)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{"message": map[string]string{"content": string(contentJSON)}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	result, err := svc.CorrectCategory(context.Background(), []domain.LineItem{{Description: "Burger"}}, "FOOD_AND_DRINK", "FOOD_AND_DRINK_RESTAURANT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ShouldCorrect {
		t.Error("expected ShouldCorrect=false")
	}
}

func TestCorrectCategory_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(`bad gateway`))
	}))
	defer ts.Close()

	svc := newTestOpenAIService("test-key", ts)
	_, err := svc.CorrectCategory(context.Background(), []domain.LineItem{{Description: "X"}}, "A", "B")
	if err == nil {
		t.Fatal("expected error from API failure")
	}
}

// ---------------------------------------------------------------------------
// mimeTypeFromPath
// ---------------------------------------------------------------------------

func TestMimeTypeFromPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/path/to/file.jpg", "image/jpeg"},
		{"/path/to/file.jpeg", "image/jpeg"},
		{"/path/to/file.png", "image/png"},
		{"/path/to/file.gif", "image/gif"},
		{"/path/to/file.webp", "image/webp"},
		{"/path/to/file.heic", "image/heic"},
		{"/path/to/file.bmp", "image/jpeg"}, // unknown defaults to jpeg
		{"/path/to/file.txt", "image/jpeg"}, // unknown defaults to jpeg
		{"file.PNG", "image/jpeg"},          // case-sensitive, .PNG != .png
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := mimeTypeFromPath(tt.path)
			if got != tt.want {
				t.Errorf("mimeTypeFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ParseReceiptCompletion
// ---------------------------------------------------------------------------

func makeCompletionResponse(t *testing.T, data openAIReceiptData) []byte {
	t.Helper()
	contentJSON, _ := json.Marshal(data)
	resp := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"content": string(contentJSON)}},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestParseReceiptCompletion_Success(t *testing.T) {
	total := 9.48
	subtotal := 8.70
	tax := 0.78
	data := openAIReceiptData{
		MerchantName:    "Starbucks",
		TransactionDate: "2025-03-15",
		TransactionTime: "09:32",
		Total:           total,
		Subtotal:        subtotal,
		Tax:             tax,
		Currency:        "USD",
		PaymentMethod:   "Visa",
		LastFourDigits:  "4242",
		LineItems: []openAILineItem{
			{Description: "Grande Latte", Quantity: 1, UnitPrice: 5.45, TotalPrice: 5.45},
		},
	}

	result := ParseReceiptCompletion(makeCompletionResponse(t, data))

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.MerchantName != "Starbucks" {
		t.Errorf("MerchantName = %q, want \"Starbucks\"", result.MerchantName)
	}
	if result.Total == nil || *result.Total != total {
		t.Errorf("Total = %v, want %v", result.Total, total)
	}
	if result.TransactionDate != "2025-03-15" {
		t.Errorf("TransactionDate = %q, want \"2025-03-15\"", result.TransactionDate)
	}
	if result.Currency != "USD" {
		t.Errorf("Currency = %q, want \"USD\"", result.Currency)
	}
	if len(result.LineItems) != 1 {
		t.Fatalf("LineItems count = %d, want 1", len(result.LineItems))
	}
	if result.LineItems[0].Description != "Grande Latte" {
		t.Errorf("LineItems[0].Description = %q", result.LineItems[0].Description)
	}
}

func TestParseReceiptCompletion_UnitPriceRecovered(t *testing.T) {
	data := openAIReceiptData{
		MerchantName: "Test",
		Total:        10.00,
		Currency:     "USD",
		LineItems: []openAILineItem{
			// UnitPrice is zero; should be recovered from TotalPrice / Quantity.
			{Description: "Latte", Quantity: 2, UnitPrice: 0, TotalPrice: 10.00},
		},
	}

	result := ParseReceiptCompletion(makeCompletionResponse(t, data))

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if len(result.LineItems) != 1 {
		t.Fatalf("LineItems count = %d, want 1", len(result.LineItems))
	}
	if result.LineItems[0].UnitPrice != 5.00 {
		t.Errorf("recovered UnitPrice = %.2f, want 5.00", result.LineItems[0].UnitPrice)
	}
}

func TestParseReceiptCompletion_ConsolidatesDuplicateItems(t *testing.T) {
	data := openAIReceiptData{
		MerchantName: "Whole Foods",
		Currency:     "USD",
		LineItems: []openAILineItem{
			{Description: "COLD PRESSED ORGANIC GRE", Quantity: 1, UnitPrice: 5.99, TotalPrice: 5.99},
			{Description: "SOUP CALABRIAN CHILI TOM", Quantity: 1, UnitPrice: 4.99, TotalPrice: 4.99},
			{Description: "COLD PRESSED ORGANIC GRE", Quantity: 1, UnitPrice: 5.99, TotalPrice: 5.99},
		},
	}

	result := ParseReceiptCompletion(makeCompletionResponse(t, data))

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if len(result.LineItems) != 2 {
		t.Fatalf("LineItems count = %d, want 2", len(result.LineItems))
	}
	// First occurrence keeps its position; the duplicate is folded in.
	got := result.LineItems[0]
	if got.Description != "COLD PRESSED ORGANIC GRE" {
		t.Errorf("LineItems[0].Description = %q", got.Description)
	}
	if got.Quantity != 2 {
		t.Errorf("LineItems[0].Quantity = %v, want 2", got.Quantity)
	}
	if got.Price != 11.98 {
		t.Errorf("LineItems[0].Price = %.2f, want 11.98", got.Price)
	}
	if got.UnitPrice != 5.99 {
		t.Errorf("LineItems[0].UnitPrice = %.2f, want 5.99", got.UnitPrice)
	}
}

func TestApplyTipToTotal(t *testing.T) {
	tests := []struct {
		name                      string
		subtotal, tax, tip, total float64
		want                      float64
	}{
		{
			// parc: printed total $104.94, handwritten "+ Tip: 20.00 = Total: 124.94".
			name:     "pre-tip printed total is corrected",
			subtotal: 99, tax: 5.94, tip: 20, total: 104.94, want: 124.94,
		},
		{
			// vivios: same shape, smaller numbers.
			name:     "pre-tip printed total, small receipt",
			subtotal: 26, tax: 1.14, tip: 5, total: 27.14, want: 32.14,
		},
		{
			name:     "total already includes the tip - left alone",
			subtotal: 99, tax: 5.94, tip: 20, total: 124.94, want: 124.94,
		},
		{
			name:     "no tip - left alone",
			subtotal: 8.78, tax: 0, tip: 0, total: 8.78, want: 8.78,
		},
		{
			// receipt6 prints no subtotal, so the arithmetic cannot reconcile and
			// the total must be trusted as read.
			name:     "no printed subtotal - left alone",
			subtotal: 0, tax: 0.75, tip: 0, total: 143.24, want: 143.24,
		},
		{
			// A discount or fee we cannot model means subtotal+tax != total, so we
			// have no basis to conclude the tip is missing.
			name:     "unexplained difference with a tip - left alone",
			subtotal: 100, tax: 5, tip: 10, total: 95, want: 95,
		},
		{
			// A half-cent gap between the printed total and subtotal+tax still
			// reconciles, so the tip is still applied.
			name:     "sub-cent rounding still counts as reconciled",
			subtotal: 10, tax: 0.995, tip: 2, total: 11, want: 12.995,
		},
		{
			// Two cents out is over the line: something unmodelled is in there.
			name:     "cents out does not reconcile",
			subtotal: 10, tax: 1, tip: 2, total: 11.02, want: 11.02,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyTipToTotal(tt.subtotal, tt.tax, tt.tip, tt.total)
			if math.Abs(got-tt.want) > 0.005 {
				t.Errorf("applyTipToTotal(%v, %v, %v, %v) = %v, want %v",
					tt.subtotal, tt.tax, tt.tip, tt.total, got, tt.want)
			}
		})
	}
}

func TestConsolidateLineItems(t *testing.T) {
	t.Run("merges same description and unit price", func(t *testing.T) {
		in := []domain.LineItem{
			{Description: "Juice", Quantity: 1, UnitPrice: 5.99, Price: 5.99},
			{Description: "Juice", Quantity: 1, UnitPrice: 5.99, Price: 5.99},
			{Description: "Juice", Quantity: 1, UnitPrice: 5.99, Price: 5.99},
		}
		out := consolidateLineItems(in)
		if len(out) != 1 {
			t.Fatalf("len = %d, want 1", len(out))
		}
		if out[0].Quantity != 3 || out[0].Price != 17.97 {
			t.Errorf("got qty=%v price=%.2f, want qty=3 price=17.97", out[0].Quantity, out[0].Price)
		}
	})

	t.Run("matches case-insensitively and ignores surrounding space", func(t *testing.T) {
		in := []domain.LineItem{
			{Description: "Bagel", Quantity: 1, UnitPrice: 2.50, Price: 2.50},
			{Description: " bagel ", Quantity: 1, UnitPrice: 2.50, Price: 2.50},
		}
		out := consolidateLineItems(in)
		if len(out) != 1 || out[0].Quantity != 2 {
			t.Fatalf("got %d items, qty %v; want 1 item qty 2", len(out), out[0].Quantity)
		}
	})

	t.Run("does not merge same description with different unit price", func(t *testing.T) {
		in := []domain.LineItem{
			{Description: "Coffee", Quantity: 1, UnitPrice: 3.00, Price: 3.00},
			{Description: "Coffee", Quantity: 1, UnitPrice: 4.00, Price: 4.00},
		}
		out := consolidateLineItems(in)
		if len(out) != 2 {
			t.Fatalf("len = %d, want 2", len(out))
		}
	})

	t.Run("never merges blank descriptions", func(t *testing.T) {
		in := []domain.LineItem{
			{Description: "", Quantity: 1, UnitPrice: 1.00, Price: 1.00},
			{Description: "", Quantity: 1, UnitPrice: 1.00, Price: 1.00},
		}
		out := consolidateLineItems(in)
		if len(out) != 2 {
			t.Fatalf("len = %d, want 2", len(out))
		}
	})

	t.Run("preserves first-appearance order", func(t *testing.T) {
		in := []domain.LineItem{
			{Description: "A", Quantity: 1, UnitPrice: 1.00, Price: 1.00},
			{Description: "B", Quantity: 1, UnitPrice: 2.00, Price: 2.00},
			{Description: "A", Quantity: 1, UnitPrice: 1.00, Price: 1.00},
		}
		out := consolidateLineItems(in)
		if len(out) != 2 || out[0].Description != "A" || out[1].Description != "B" {
			t.Fatalf("unexpected order: %+v", out)
		}
	})
}

func TestParseReceiptCompletion_EmptyCurrencyNotDefaulted(t *testing.T) {
	// The OCRResult.Currency field returns what the model returned (empty),
	// not the "USD" default used only for raw text formatting.
	data := openAIReceiptData{
		MerchantName: "Test",
		Total:        5.00,
		Currency:     "", // model returned no currency
	}

	result := ParseReceiptCompletion(makeCompletionResponse(t, data))

	if result.Error != "" {
		t.Fatalf("unexpected error: %s", result.Error)
	}
	if result.Currency != "" {
		t.Errorf("Currency = %q, want \"\" (OCRResult.Currency is not defaulted to USD)", result.Currency)
	}
}

func TestParseReceiptCompletion_APIErrorField(t *testing.T) {
	resp := map[string]interface{}{
		"error": map[string]string{"message": "invalid_api_key"},
	}
	b, _ := json.Marshal(resp)

	result := ParseReceiptCompletion(b)

	if result.Error == "" {
		t.Fatal("expected error for API error field")
	}
	if !strings.Contains(result.Error, "invalid_api_key") {
		t.Errorf("Error = %q, want to contain \"invalid_api_key\"", result.Error)
	}
}

func TestParseReceiptCompletion_NoChoices(t *testing.T) {
	resp := map[string]interface{}{
		"choices": []interface{}{},
	}
	b, _ := json.Marshal(resp)

	result := ParseReceiptCompletion(b)

	if result.Error == "" {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(result.Error, "no choices") {
		t.Errorf("Error = %q, want to contain \"no choices\"", result.Error)
	}
}

func TestParseReceiptCompletion_InvalidJSON(t *testing.T) {
	result := ParseReceiptCompletion([]byte("not json at all {{{"))

	if result.Error == "" {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseReceiptCompletion_InvalidContentJSON(t *testing.T) {
	resp := map[string]interface{}{
		"choices": []map[string]interface{}{
			{"message": map[string]string{"content": "not valid json"}},
		},
	}
	b, _ := json.Marshal(resp)

	result := ParseReceiptCompletion(b)

	if result.Error == "" {
		t.Fatal("expected error when content is not valid JSON")
	}
}

// ---------------------------------------------------------------------------
// BuildTextReceiptRequest
// ---------------------------------------------------------------------------

// extractPromptText unmarshals a BuildTextReceiptRequest JSON body and returns
// the prompt text from messages[0].content[0].text.
func extractPromptText(t *testing.T, reqBytes []byte) string {
	t.Helper()
	var body struct {
		Messages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reqBytes, &body); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if len(body.Messages) == 0 || len(body.Messages[0].Content) == 0 {
		t.Fatal("no message content found in request")
	}
	return body.Messages[0].Content[0].Text
}

func TestBuildTextReceiptRequest_Basic(t *testing.T) {
	reqBytes, err := BuildTextReceiptRequest("Receipt text here", "Email from Stripe", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(reqBytes, &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body.Model != "gpt-5-nano" {
		t.Errorf("model = %q, want \"gpt-5-nano\"", body.Model)
	}

	prompt := extractPromptText(t, reqBytes)
	for _, want := range []string{"merchant", "YYYY-MM-DD", "USD"} {
		if !strings.Contains(strings.ToLower(prompt), strings.ToLower(want)) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if !strings.Contains(prompt, "Email from Stripe") {
		t.Error("prompt missing context label")
	}
	if !strings.Contains(prompt, "Receipt text here") {
		t.Error("prompt missing input text")
	}
}

func TestBuildTextReceiptRequest_WithReceivedAt(t *testing.T) {
	receivedAt := time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC)
	reqBytes, err := BuildTextReceiptRequest("some text", "label", &receivedAt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := extractPromptText(t, reqBytes)
	if !strings.Contains(prompt, "on or before") {
		t.Error("expected received-date constraint in prompt")
	}
	if !strings.Contains(prompt, "2025-03-20") {
		t.Error("expected received date in prompt")
	}
}

func TestBuildTextReceiptRequest_LongTextTruncated(t *testing.T) {
	// 8000 x's followed by a sentinel that should be cut off.
	bodyText := strings.Repeat("x", 8000) + "SENTINEL_BEYOND_8000"
	reqBytes, err := BuildTextReceiptRequest(bodyText, "label", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := extractPromptText(t, reqBytes)
	if strings.Contains(prompt, "SENTINEL_BEYOND_8000") {
		t.Error("expected sentinel beyond 8000 chars to be truncated")
	}
}

func TestBuildTextReceiptRequest_CSSHeavyTextTruncated(t *testing.T) {
	// CSS truncation only fires when body > 8000 chars AND has >20 braces.
	// Use 8016 chars of CSS-like content followed by a sentinel that must be cut.
	cssBlock := strings.Repeat("{color:red;}", 668) // 668 * 12 = 8016 chars, 668 braces each
	sentinel := "SENTINEL_AFTER_8000"
	bodyText := cssBlock + sentinel

	reqBytes, err := BuildTextReceiptRequest(bodyText, "label", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prompt := extractPromptText(t, reqBytes)
	if strings.Contains(prompt, sentinel) {
		t.Error("expected sentinel after 8000 chars to be truncated for CSS-heavy input")
	}
}
