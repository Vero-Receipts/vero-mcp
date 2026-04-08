package service

import (
	"encoding/json"
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
		{"/path/to/file.bmp", "image/jpeg"},  // unknown defaults to jpeg
		{"/path/to/file.txt", "image/jpeg"},   // unknown defaults to jpeg
		{"file.PNG", "image/jpeg"},            // case-sensitive, .PNG != .png
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
