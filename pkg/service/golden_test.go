package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/joho/godotenv"
)

// ---------------------------------------------------------------------------
// Golden file infrastructure
// ---------------------------------------------------------------------------

// loadDotEnv loads the project-root .env file once, silently ignoring errors
// (the file may not exist in CI where env vars are injected directly).
var loadDotEnvOnce sync.Once

func loadDotEnv() {
	loadDotEnvOnce.Do(func() {
		_, thisFile, _, _ := runtime.Caller(0)
		dir := filepath.Dir(thisFile)
		for range 5 {
			candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
				_ = godotenv.Load(candidate)
				return
			}
			dir = filepath.Dir(dir)
		}
	})
}

// goldenRecord is the on-disk format for a captured OpenAI round-trip.
// Only the hash and the response are stored — the request is not written
// because image requests embed multi-megabyte base64 data.
type goldenRecord struct {
	RequestSHA256 string          `json:"request_sha256"`
	Response      json.RawMessage `json:"response"`
}

// goldenTransport intercepts the HTTP call made by OpenAIService.doRequest.
// On UPDATE_GOLDEN=true (or when no golden file exists), it proxies the real
// request to OpenAI and saves the round-trip. Otherwise it compares the current
// request hash to the stored one and replays the stored response.
type goldenTransport struct {
	t    *testing.T
	name string
	real http.RoundTripper
}

func (g *goldenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	reqBytes, _ := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(reqBytes))

	hash := sha256Hex(reqBytes)
	gpath := goldenFilePath(g.name)
	update := os.Getenv("UPDATE_GOLDEN") == "true"

	// No golden file yet — must capture.
	if !goldenExists(gpath) {
		if os.Getenv("OPENAI_API_KEY") == "" {
			g.t.Fatalf(
				"no golden file at %s and OPENAI_API_KEY is not set\n"+
					"Set OPENAI_API_KEY (and optionally UPDATE_GOLDEN=true) to capture.",
				gpath,
			)
		}
		return g.captureAndSave(req, gpath, hash, reqBytes)
	}

	// Golden file exists — check if the prompt has changed.
	rec := readGolden(g.t, gpath)
	if rec.RequestSHA256 != hash {
		// Prompt changed. Recapture if UPDATE_GOLDEN=true, otherwise fail.
		if !update {
			g.t.Fatalf(
				"prompt changed since golden was captured (hash mismatch in %s)\n"+
					"Re-run with UPDATE_GOLDEN=true OPENAI_API_KEY=sk-... to recapture.",
				gpath,
			)
		}
		if os.Getenv("OPENAI_API_KEY") == "" {
			g.t.Skipf("UPDATE_GOLDEN=true but OPENAI_API_KEY not set; cannot recapture %s", gpath)
		}
		return g.captureAndSave(req, gpath, hash, reqBytes)
	}

	// Prompt unchanged — replay stored response regardless of UPDATE_GOLDEN.
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(rec.Response)),
		Header:     http.Header{"Content-Type": {"application/json"}},
	}, nil
}

func (g *goldenTransport) captureAndSave(req *http.Request, gpath, hash string, _ []byte) (*http.Response, error) {
	resp, err := g.real.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	respBytes, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(respBytes))
	writeGolden(g.t, gpath, hash, respBytes)
	return resp, nil
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func goldenFilePath(name string) string {
	return filepath.Join("testdata", "golden", name+".golden.json")
}

func goldenExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readGolden(t *testing.T, path string) goldenRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file %s: %v", path, err)
	}
	var rec goldenRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("parse golden file %s: %v", path, err)
	}
	return rec
}

func writeGolden(t *testing.T, path, hash string, respBytes []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("create golden dir: %v", err)
	}
	rec := goldenRecord{
		RequestSHA256: hash,
		Response:      json.RawMessage(respBytes),
	}
	data, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write golden file %s: %v", path, err)
	}
	t.Logf("golden file written: %s", path)
}

// newGoldenOpenAIService returns an OpenAIService whose HTTP transport is
// wired to the golden infrastructure for the named test case.
func newGoldenOpenAIService(t *testing.T, goldenName string) *OpenAIService {
	t.Helper()
	loadDotEnv()
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		// Non-empty so the API key guard in OpenAIService passes.
		// The transport decides what to do before any real network call is made.
		apiKey = "golden-replay"
	}
	svc := NewOpenAIService(apiKey)
	svc.httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &goldenTransport{
			t:    t,
			name: goldenName,
			real: http.DefaultTransport,
		},
	}
	return svc
}

// ---------------------------------------------------------------------------
// Shared assertion helpers
// ---------------------------------------------------------------------------

// assertOCRResult compares got to want using reflect.DeepEqual.
// RawText is cleared before comparison — it is a synthetic reconstruction
// built by ParseReceiptCompletion and is not part of the OpenAI response.
func assertOCRResult(t *testing.T, got *domain.OCRResult, want domain.OCRResult) {
	t.Helper()
	got.RawText = ""
	// IsSubscription is not asserted here: these goldens replay responses captured before the
	// field existed, so it always decodes to false. Its parsing is covered directly by
	// TestParseReceiptCompletion_IsSubscription.
	got.IsSubscription = nil
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("OCRResult mismatch:\n  got:  %+v\n  want: %+v", *got, want)
	}
}


// ---------------------------------------------------------------------------
// Golden test cases
// ---------------------------------------------------------------------------

// TestGolden_ParseImage covers ParseImageData with real receipt photos.
// To add a case: drop an image in testdata/images/, add a row to the table,
// then run with OPENAI_API_KEY set to capture the golden file.
func TestGolden_ParseImage(t *testing.T) {
	tests := []struct {
		name       string
		goldenName string
		imagePath  string
		mimeType   string
		want       domain.OCRResult // zero/nil fields are skipped; Total==nil means check > 0
	}{
		{
			name:       "parc_restaurant",
			goldenName: "parse_image_parc",
			imagePath:  "testdata/images/receipt1.jpeg",
			mimeType:   "image/jpeg",
			want: domain.OCRResult{
				MerchantName:    "Parc Detroit",
				MerchantAddress: "800 Woodward Ave, Detroit, MI 48226",
				MerchantCity:    "Detroit",
				MerchantState:   "MI",
				TransactionDate: "2026-05-28",
				TransactionTime: "13:20",
				Subtotal:        floatPtr(99.00),
				Tax:             floatPtr(5.94),
				Tip:             floatPtr(20.00),
				Total:           floatPtr(124.94),
				Currency:        "USD",
				PaymentMethod:   "Visa",
				LastFourDigits:  "2676",
				LineItems: []domain.LineItem{
					{Description: "KEY WEST SHRIMP WRAP", Quantity: 1, UnitPrice: 23, Price: 23},
					// The two identical PARC BURGER lines are consolidated into one
					// row with quantity 2 by consolidateLineItems.
					{Description: "PARC BURGER", Quantity: 2, UnitPrice: 28, Price: 56},
					{Description: "DIET COKE", Quantity: 1, UnitPrice: 5, Price: 5},
					{Description: "TRUFFLE FRIES", Quantity: 1, UnitPrice: 15, Price: 15},
				},
			},
		},
		{
			name:       "griswold_market",
			goldenName: "parse_image_griswold",
			imagePath:  "testdata/images/receipt2.jpeg",
			mimeType:   "image/jpeg",
			want: domain.OCRResult{
				MerchantName:    "GRISWOLD MARKET & MEATS",
				MerchantAddress: "735 GRISWOLD ST, DETROIT, MI",
				MerchantCity:    "DETROIT",
				MerchantState:   "MI",
				TransactionDate: "2026-05-27",
				TransactionTime: "20:28",
				Subtotal:        floatPtr(8.78),
				Tax:             floatPtr(0),
				Tip:             floatPtr(0),
				Total:           floatPtr(8.78),
				Currency:        "USD",
				PaymentMethod:   "Visa",
				LastFourDigits:  "2676",
				LineItems: []domain.LineItem{
					{Description: "GROCERY", Quantity: 1, UnitPrice: 5.99, Price: 5.99},
					{Description: "CHOBANI STRAWBERRY", Quantity: 1, UnitPrice: 2.79, Price: 2.79},
				},
			},
		},
		{
			name:       "vivios_restaurant",
			goldenName: "parse_image_vivios",
			imagePath:  "testdata/images/receipt3.jpeg",
			mimeType:   "image/jpeg",
			want: domain.OCRResult{
				MerchantName:    "Vivio's",
				MerchantAddress: "2460 MARKET ST",
				TransactionDate: "2026-05-17",
				TransactionTime: "13:53",
				Subtotal:        floatPtr(26.00),
				Tax:             floatPtr(1.14),
				Tip:             floatPtr(5.00),
				Total:           floatPtr(32.14),
				Currency:        "USD",
				PaymentMethod:   "Visa",
				LastFourDigits:  "9267",
				LineItems: []domain.LineItem{
					{Description: "Ground Round", Quantity: 1, UnitPrice: 18, Price: 18},
					{Description: "Soft Drinks", Quantity: 2, UnitPrice: 3.5, Price: 7},
					{Description: "Side Dressing Ranch", Quantity: 1, UnitPrice: 1, Price: 1},
				},
			},
		},
		{
			name:       "parlor_doughnuts",
			goldenName: "parse_image_parlor",
			imagePath:  "testdata/images/receipt4.jpg",
			mimeType:   "image/jpeg",
			want: domain.OCRResult{
				MerchantName:    "Parlor Doughnuts",
				MerchantAddress: "500 Rep John Lewis Way S, Nashville, TN 37203",
				MerchantCity:    "Nashville",
				MerchantState:   "TN",
				TransactionDate: "2026-05-30",
				TransactionTime: "09:55",
				Subtotal:        floatPtr(3.95),
				Tax:             floatPtr(0.37),
				Tip:             floatPtr(0),
				Total:           floatPtr(4.32),
				Currency:        "USD",
				PaymentMethod:   "Visa",
				LastFourDigits:  "2676",
				LineItems: []domain.LineItem{
					{Description: "Sandy Beach", Quantity: 1, UnitPrice: 3.95, Price: 3.95},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			imgBytes, err := os.ReadFile(tt.imagePath)
			if err != nil {
				t.Fatalf("read image: %v", err)
			}
			svc := newGoldenOpenAIService(t, tt.goldenName)
			r := svc.ParseImageData(context.Background(), imgBytes, tt.mimeType)
			if r.Error != "" {
				t.Fatalf("parse error: %s", r.Error)
			}
			assertOCRResult(t, r, tt.want)
		})
	}
}

// TestGolden_ParseTextAsReceipt covers plain-text and pre-stripped email receipts.
// Set exactly one of bodyText or bodyPath per case.
// To add a case: add a row and run with OPENAI_API_KEY set to capture the golden file.
func TestGolden_ParseTextAsReceipt(t *testing.T) {
	tests := []struct {
		name         string
		goldenName   string
		bodyText     string // inline plain text (mutually exclusive with bodyPath)
		bodyPath     string // path to pre-stripped text file (mutually exclusive with bodyText)
		contextLabel string
		want         domain.OCRResult
	}{
		{
			name:         "starbucks_plain_text",
			goldenName:   "parse_text_receipt",
			contextLabel: "Email from Starbucks",
			bodyText: `From: orders@starbucks.com
Subject: Your Starbucks receipt

Starbucks
123 Main Street, Seattle, WA 98101

Date: 2025-03-15
Time: 09:32

Items:
1x Grande Latte         $5.45
1x Blueberry Scone      $3.25

Subtotal:               $8.70
Tax:                    $0.78
Total:                  $9.48

Payment: Visa ending in 4242
Thank you for your purchase!`,
			want: domain.OCRResult{
				MerchantName:    "Starbucks",
				MerchantAddress: "123 Main Street, Seattle, WA 98101",
				MerchantCity:    "Seattle",
				MerchantState:   "WA",
				TransactionDate: "2025-03-15",
				TransactionTime: "09:32",
				Subtotal:        floatPtr(8.70),
				Tax:             floatPtr(0.78),
				Tip:             floatPtr(0),
				Total:           floatPtr(9.48),
				Currency:        "USD",
				PaymentMethod:   "Visa",
				LastFourDigits:  "4242",
				LineItems: []domain.LineItem{
					{Description: "Grande Latte", Quantity: 1, UnitPrice: 5.45, Price: 5.45},
					{Description: "Blueberry Scone", Quantity: 1, UnitPrice: 3.25, Price: 3.25},
				},
			},
		},
		{
			name:         "spkrbox_toast_html",
			goldenName:   "parse_email_spkrbox",
			bodyPath:     "testdata/emails/email1.txt",
			contextLabel: "Toast receipt from SPKRBOX",
			want: domain.OCRResult{
				MerchantName:    "SPKRBOX",
				MerchantAddress: "200 Grand River Avenue, Detroit, MI 48226",
				MerchantCity:    "Detroit",
				MerchantState:   "MI",
				TransactionDate: "2026-05-25",
				TransactionTime: "23:17",
				Subtotal:        floatPtr(12.00),
				Tax:             floatPtr(0),
				Tip:             floatPtr(2.26),
				Total:           floatPtr(14.26),
				Currency:        "USD",
				PaymentMethod:   "VISA CREDIT",
				LastFourDigits:  "2676",
				LineItems: []domain.LineItem{
					{Description: "Can Modelo", Quantity: 2, UnitPrice: 6, Price: 12},
				},
			},
		},
		{
			name:         "american_airlines_html",
			goldenName:   "parse_email_american_airlines",
			bodyPath:     "testdata/emails/email2.txt",
			contextLabel: "American Airlines receipt",
			want: domain.OCRResult{
				MerchantName:  "American Airlines",
				TransactionDate: "2026-05-13",
				Subtotal:      floatPtr(331.16),
				Tax:           floatPtr(40.24),
				Tip:           floatPtr(0),
				Total:         floatPtr(371.4),
				Currency:      "USD",
				PaymentMethod: "Apple Pay",
				LineItems: []domain.LineItem{
					{Description: "New ticket", Quantity: 1, UnitPrice: 331.16, Price: 331.16},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.bodyText
			if tt.bodyPath != "" {
				b, err := os.ReadFile(tt.bodyPath)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				body = string(b)
			}
			svc := newGoldenOpenAIService(t, tt.goldenName)
			r := svc.ParseTextAsReceipt(context.Background(), body, tt.contextLabel, nil)
			if r.Error != "" {
				t.Fatalf("parse error: %s", r.Error)
			}
			assertOCRResult(t, r, tt.want)
		})
	}
}

// TestGolden_DisambiguateMerchant covers the merchant name matching prompt.
// To add a case: add a row and run UPDATE_GOLDEN=true ... go test -run TestGolden_DisambiguateMerchant.
func TestGolden_DisambiguateMerchant(t *testing.T) {
	tests := []struct {
		name            string
		goldenName      string
		receiptMerchant string
		txMerchant      string
		txName          string
		wantSame        bool
		wantMinConf     float64 // 0 = skip confidence check
	}{
		{
			name:            "sbux_is_starbucks",
			goldenName:      "disambiguate_same",
			receiptMerchant: "Starbucks",
			txMerchant:      "SBUX",
			txName:          "STARBUCKS #123",
			wantSame:        true,
			wantMinConf:     0.70,
		},
		{
			name:            "apple_store_not_applebees",
			goldenName:      "disambiguate_different",
			receiptMerchant: "Apple Store",
			txMerchant:      "Applebee's",
			txName:          "APPLEBEES",
			wantSame:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newGoldenOpenAIService(t, tt.goldenName)
			result, err := svc.DisambiguateMerchant(context.Background(), tt.receiptMerchant, tt.txMerchant, tt.txName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.SameBusiness != tt.wantSame {
				t.Errorf("SameBusiness = %v, want %v (reason: %s)", result.SameBusiness, tt.wantSame, result.Reason)
			}
			if tt.wantMinConf > 0 && result.Confidence < tt.wantMinConf {
				t.Errorf("confidence = %.2f, want >= %.2f", result.Confidence, tt.wantMinConf)
			}
		})
	}
}

// TestGolden_CorrectCategory covers the category correction prompt.
// To add a case: add a row and run UPDATE_GOLDEN=true ... go test -run TestGolden_CorrectCategory.
func TestGolden_CorrectCategory(t *testing.T) {
	tests := []struct {
		name                string
		goldenName          string
		lineItems           []domain.LineItem
		currentPrimary      string
		currentDetailed     string
		wantShouldCorrect   bool
		wantPrimaryContains string // "" = skip
	}{
		{
			name:       "coffee_miscategorized_as_merchandise",
			goldenName: "correct_category",
			lineItems: []domain.LineItem{
				{Description: "Iced Latte", Quantity: 1, Price: 5.50},
				{Description: "Blueberry Scone", Quantity: 1, Price: 3.25},
			},
			currentPrimary:      "GENERAL_MERCHANDISE",
			currentDetailed:     "GENERAL_MERCHANDISE_OTHER",
			wantShouldCorrect:   true,
			wantPrimaryContains: "FOOD",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newGoldenOpenAIService(t, tt.goldenName)
			result, err := svc.CorrectCategory(context.Background(), tt.lineItems, tt.currentPrimary, tt.currentDetailed)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.ShouldCorrect != tt.wantShouldCorrect {
				t.Errorf("ShouldCorrect = %v, want %v (reason: %s)", result.ShouldCorrect, tt.wantShouldCorrect, result.Reason)
			}
			if tt.wantPrimaryContains != "" && !strings.Contains(result.CorrectedPFCPrimary, tt.wantPrimaryContains) {
				t.Errorf("primary = %q, want to contain %q (detailed: %s)", result.CorrectedPFCPrimary, tt.wantPrimaryContains, result.CorrectedPFCDetailed)
			}
		})
	}
}

// TestGolden_IsSubscriptionDetection verifies end-to-end that the OCR pipeline flags a real
// subscription receipt (the SoundCloud Go+ email, stripped to plain text) as a subscription.
// Uses the golden replay harness; until the golden is captured it skips, so the suite stays
// green.
//
// Capture once:
//
//	UPDATE_GOLDEN=true OPENAI_API_KEY=sk-... go test ./pkg/service -run TestGolden_IsSubscriptionDetection
func TestGolden_IsSubscriptionDetection(t *testing.T) {
	const goldenName = "parse_email_soundcloud"
	if !goldenExists(goldenFilePath(goldenName)) && os.Getenv("OPENAI_API_KEY") == "" {
		t.Skipf("golden %q not captured yet; run: UPDATE_GOLDEN=true OPENAI_API_KEY=sk-... go test ./pkg/service -run TestGolden_IsSubscriptionDetection", goldenName)
	}

	body, err := os.ReadFile("testdata/emails/soundcloud.txt")
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	svc := newGoldenOpenAIService(t, goldenName)
	r := svc.ParseTextAsReceipt(context.Background(), string(body), "SoundCloud subscription receipt", nil)
	if r.Error != "" {
		t.Fatalf("parse error: %s", r.Error)
	}

	// The assertion that matters: this receipt is detected as a subscription.
	if r.IsSubscription == nil || !*r.IsSubscription {
		t.Errorf("IsSubscription = %v, want true", r.IsSubscription)
	}
	// Sanity checks so the golden can't silently drift onto the wrong receipt.
	if !strings.Contains(strings.ToLower(r.MerchantName), "soundcloud") {
		t.Errorf("MerchantName = %q, want to contain SoundCloud", r.MerchantName)
	}
	if r.Total == nil || *r.Total != 11.99 {
		t.Errorf("Total = %v, want 11.99", r.Total)
	}
}
