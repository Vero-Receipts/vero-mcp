package service

import (
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

func TestBuildReceiptFromOCR_BasicFields(t *testing.T) {
	userID := uuid.New()
	merchant := "Starbucks"
	total := 5.50
	ocr := &domain.OCRResult{
		MerchantName:    merchant,
		Total:           &total,
		TransactionDate: "2025-03-15",
		RawText:         "Starbucks #123\nTotal: $5.50",
		LineItems:       []domain.LineItem{{Description: "Latte", Price: 5.50}},
		Currency:        "USD",
		PaymentMethod:   "VISA",
		LastFourDigits:  "4242",
		TransactionTime: "14:30",
		MerchantAddress: "123 Main St",
	}

	receipt := BuildReceiptFromOCR(ReceiptFromOCRInput{
		UserID:    userID,
		OCRResult: ocr,
		ImageURL:  "https://example.com/img.png",
		Source:    "upload",
	})

	if receipt.UserID != userID {
		t.Errorf("expected user_id %v, got %v", userID, receipt.UserID)
	}
	if receipt.Source != "upload" {
		t.Errorf("expected source upload, got %s", receipt.Source)
	}
	if receipt.Status != "unmatched" {
		t.Errorf("expected status unmatched, got %s", receipt.Status)
	}
	if receipt.MerchantName == nil || *receipt.MerchantName != merchant {
		t.Errorf("expected merchant %s, got %v", merchant, receipt.MerchantName)
	}
	if receipt.Total == nil || *receipt.Total != total {
		t.Errorf("expected total %f, got %v", total, receipt.Total)
	}
	if receipt.Date == nil || receipt.Date.Format("2006-01-02") != "2025-03-15" {
		t.Errorf("expected date 2025-03-15, got %v", receipt.Date)
	}
	if receipt.Currency == nil || *receipt.Currency != "USD" {
		t.Errorf("expected currency USD, got %v", receipt.Currency)
	}
	if receipt.PaymentMethod == nil || *receipt.PaymentMethod != "VISA" {
		t.Error("expected payment method VISA")
	}
	if receipt.LastFourDigits == nil || *receipt.LastFourDigits != "4242" {
		t.Error("expected last four digits 4242")
	}
	if receipt.TransactionTime == nil || *receipt.TransactionTime != "14:30" {
		t.Error("expected transaction time 14:30")
	}
	if receipt.MerchantAddress == nil || *receipt.MerchantAddress != "123 Main St" {
		t.Error("expected merchant address")
	}
	if receipt.RawText == nil || *receipt.RawText != ocr.RawText {
		t.Error("expected raw text to be set")
	}
}

func TestBuildReceiptFromOCR_CallerOverrides(t *testing.T) {
	merchant := "Override Merchant"
	callerTotal := 99.99
	ocrTotal := 50.00
	ocr := &domain.OCRResult{
		MerchantName: "OCR Merchant",
		Total:        &ocrTotal,
	}

	receipt := BuildReceiptFromOCR(ReceiptFromOCRInput{
		UserID:       uuid.New(),
		OCRResult:    ocr,
		MerchantName: &merchant,
		Total:        &callerTotal,
		Source:       "email",
	})

	// Caller-supplied values should take precedence.
	if *receipt.MerchantName != merchant {
		t.Errorf("expected caller merchant %q, got %q", merchant, *receipt.MerchantName)
	}
	if *receipt.Total != callerTotal {
		t.Errorf("expected caller total %f, got %f", callerTotal, *receipt.Total)
	}
	if receipt.Source != "email" {
		t.Errorf("expected source email, got %s", receipt.Source)
	}
}

func TestBuildReceiptFromOCR_FallbackTotalFromRawText(t *testing.T) {
	ocr := &domain.OCRResult{
		RawText: "Items\nTotal: $42.50\nThank you",
	}
	receipt := BuildReceiptFromOCR(ReceiptFromOCRInput{
		UserID:    uuid.New(),
		OCRResult: ocr,
	})

	if receipt.Total == nil || *receipt.Total != 42.50 {
		t.Errorf("expected fallback total 42.50, got %v", receipt.Total)
	}
}

func TestBuildReceiptFromOCR_DefaultSource(t *testing.T) {
	ocr := &domain.OCRResult{}
	receipt := BuildReceiptFromOCR(ReceiptFromOCRInput{
		UserID:    uuid.New(),
		OCRResult: ocr,
		Source:    "", // empty
	})
	if receipt.Source != "upload" {
		t.Errorf("expected default source upload, got %s", receipt.Source)
	}
}

func TestClampFutureDate_NoDate(t *testing.T) {
	receipt := &domain.Receipt{}
	ClampFutureDate(receipt, uuid.New(), nil)
	if receipt.Date != nil {
		t.Error("expected date to remain nil")
	}
}

func TestClampFutureDate_PastDate(t *testing.T) {
	past := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	receipt := &domain.Receipt{Date: &past}
	ClampFutureDate(receipt, uuid.New(), nil)
	if !receipt.Date.Equal(past) {
		t.Error("expected past date to remain unchanged")
	}
}

func TestClampFutureDate_FutureDateClampsToToday(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0)
	receipt := &domain.Receipt{Date: &future}
	ClampFutureDate(receipt, uuid.New(), nil)

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if !receipt.Date.Equal(today) {
		t.Errorf("expected date clamped to today %v, got %v", today, receipt.Date)
	}
}

func TestClampFutureDate_FutureDateWithFallback(t *testing.T) {
	future := time.Now().AddDate(1, 0, 0)
	fallback := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	receipt := &domain.Receipt{Date: &future}
	ClampFutureDate(receipt, uuid.New(), &fallback)

	if !receipt.Date.Equal(fallback) {
		t.Errorf("expected date clamped to fallback %v, got %v", fallback, receipt.Date)
	}
}

func TestIsValidReceipt(t *testing.T) {
	total := 10.0
	zero := 0.0
	merchant := "Shop"
	empty := ""

	tests := []struct {
		name  string
		r     *domain.Receipt
		valid bool
	}{
		{"with total", &domain.Receipt{Total: &total}, true},
		{"with merchant", &domain.Receipt{MerchantName: &merchant}, true},
		{"with both", &domain.Receipt{Total: &total, MerchantName: &merchant}, true},
		{"zero total no merchant", &domain.Receipt{Total: &zero}, false},
		{"nil total nil merchant", &domain.Receipt{}, false},
		{"empty merchant", &domain.Receipt{MerchantName: &empty}, false},
		{"zero total empty merchant", &domain.Receipt{Total: &zero, MerchantName: &empty}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidReceipt(tt.r); got != tt.valid {
				t.Errorf("IsValidReceipt() = %v, want %v", got, tt.valid)
			}
		})
	}
}
