package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

const openAIURL = "https://api.openai.com/v1/chat/completions"

// OpenAIService parses receipts into structured data: GPT-5-Mini Vision for
// images, GPT-5-Nano for text bodies (email/PDF) and the merchant/category calls.
type OpenAIService struct {
	apiKey     string
	httpClient *http.Client
}

func NewOpenAIService(apiKey string) *OpenAIService {
	return &OpenAIService{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

const maxRetries = 3

// DoRequest executes a single OpenAI chat-completions POST and retries on 429
// (rate-limit) responses using the Retry-After header or exponential back-off.
// It returns the raw response body on success (HTTP 200) and an error otherwise.
// Exported as a generic transport so dependents can issue their own chat-completions
// requests (own prompt + json_schema) without duplicating the retry/auth plumbing.
func (s *OpenAIService) DoRequest(ctx context.Context, bodyBytes []byte) ([]byte, error) {
	var (
		resp      *http.Response
		respBytes []byte
	)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIURL, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err = s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("openai request: %w", err)
		}
		respBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("read response: %w", err)
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			break
		}
		if attempt == maxRetries {
			return nil, fmt.Errorf("OpenAI API returned %d: %s", resp.StatusCode, string(respBytes))
		}

		// Honour Retry-After if present, otherwise use exponential back-off.
		wait := time.Duration(1<<uint(attempt)) * time.Second
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, parseErr := strconv.ParseFloat(ra, 64); parseErr == nil {
				wait = time.Duration(secs*float64(time.Second)) + 100*time.Millisecond
			}
		}
		slog.Warn("OpenAI rate limited, retrying", "attempt", attempt+1, "wait", wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned %d: %s", resp.StatusCode, string(respBytes))
	}
	return respBytes, nil
}

// openAIReceiptData is the structured schema returned by GTP-5-Nano.
type openAIReceiptData struct {
	MerchantName    string           `json:"merchantName"`
	MerchantAddress string           `json:"merchantAddress"`
	TransactionDate string           `json:"transactionDate"` // YYYY-MM-DD
	TransactionTime string           `json:"transactionTime"` // HH:MM
	Subtotal        float64          `json:"subtotal"`
	Tax             float64          `json:"tax"`
	Tip             float64          `json:"tip"`
	Total           float64          `json:"total"`
	Currency        string           `json:"currency"` // ISO 4217 (e.g. "USD", "MXN")
	PaymentMethod   string           `json:"paymentMethod"`
	LastFourDigits  string           `json:"lastFourDigits"`
	IsSubscription  bool             `json:"isSubscription"`
	LineItems       []openAILineItem `json:"lineItems"`
}

type openAILineItem struct {
	Description string  `json:"description"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unitPrice"`
	TotalPrice  float64 `json:"totalPrice"`
}

// mimeTypeFromPath returns the MIME type based on file extension.
func mimeTypeFromPath(filePath string) string {
	mimeTypes := map[string]string{
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".png":  "image/png",
		".gif":  "image/gif",
		".webp": "image/webp",
		".heic": "image/heic",
		".pdf":  "application/pdf",
	}
	ext := filepath.Ext(filePath)
	if m, ok := mimeTypes[ext]; ok {
		return m
	}
	return "image/jpeg"
}

// ParseImage sends the receipt image to GPT-5-Mini Vision and returns a structured OCRResult.
func (s *OpenAIService) ParseImage(ctx context.Context, filePath string) *domain.OCRResult {
	if s.apiKey == "" {
		slog.Warn("[OpenAI] no OPENAI_API_KEY configured, skipping receipt parsing")
		return &domain.OCRResult{Error: "OpenAI API key not configured"}
	}

	slog.Info("[OpenAI] parsing receipt image")

	imageBytes, err := os.ReadFile(filePath)
	if err != nil {
		return &domain.OCRResult{Error: fmt.Sprintf("read image: %v", err)}
	}

	mimeType := mimeTypeFromPath(filePath)
	return s.ParseImageData(ctx, imageBytes, mimeType)
}

// ParseImageData sends the receipt image bytes to GPT-5-Mini Vision and returns a structured OCRResult.
func (s *OpenAIService) ParseImageData(ctx context.Context, imageBytes []byte, mimeType string) *domain.OCRResult {
	return s.ParseImageDataWithContext(ctx, imageBytes, mimeType, TextSection{})
}

// ParseImageDataWithContext sends the receipt image alongside a secondary text
// source describing the SAME transaction (e.g. the email body the image arrived
// in), so items present only in that text are not lost. A blank supplement
// makes this exactly ParseImageData.
func (s *OpenAIService) ParseImageDataWithContext(ctx context.Context, imageBytes []byte, mimeType string, supplement TextSection) *domain.OCRResult {
	if s.apiKey == "" {
		slog.Warn("[OpenAI] no OPENAI_API_KEY configured, skipping receipt parsing")
		return &domain.OCRResult{Error: "OpenAI API key not configured"}
	}

	bodyBytes, err := BuildImageReceiptRequestWithContext(imageBytes, mimeType, supplement)
	if err != nil {
		return &domain.OCRResult{Error: fmt.Sprintf("marshal request: %v", err)}
	}

	respBytes, err := s.DoRequest(ctx, bodyBytes)
	if err != nil {
		return &domain.OCRResult{Error: err.Error()}
	}

	result := ParseReceiptCompletion(respBytes)
	if result.Error == "" {
		slog.Info("[OpenAI] successfully parsed receipt",
			"items", len(result.LineItems),
		)
	}
	return result
}

// derefFloat is a small helper for log lines.
func derefFloat(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// BuildImageReceiptRequest constructs the chat-completions request body for
// vision-based receipt parsing. Exported so callers (e.g. the OpenAI Batch API
// path) can produce identical request payloads without sending them inline.
func BuildImageReceiptRequest(imageBytes []byte, mimeType string) ([]byte, error) {
	return BuildImageReceiptRequestWithContext(imageBytes, mimeType, TextSection{})
}

// BuildImageReceiptRequestWithContext builds the vision request with an optional
// secondary text source appended as an extra content block after the image —
// used when a receipt image arrives in an email whose body carries part of the
// itemization. The image remains the primary source. With a blank supplement
// the emitted request is byte-identical to BuildImageReceiptRequest, so
// captured image goldens remain valid.
func BuildImageReceiptRequestWithContext(imageBytes []byte, mimeType string, supplement TextSection) ([]byte, error) {
	base64Image := base64.StdEncoding.EncodeToString(imageBytes)

	schema := receiptJSONSchema()

	prompt := imageReceiptPrompt()
	supplementText := strings.TrimSpace(supplement.Text)
	if supplementText != "" {
		prompt += mergedSourcesImageInstructions
	}

	content := []map[string]interface{}{
		{
			"type": "text",
			"text": prompt,
		},
		{
			"type": "image_url",
			"image_url": map[string]string{
				"url": fmt.Sprintf("data:%s;base64,%s", mimeType, base64Image),
			},
		},
	}
	if supplementText != "" {
		label := supplement.Label
		if label == "" {
			label = "Additional text"
		}
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": fmt.Sprintf("--- %s ---\n%s", label, clampReceiptText(supplementText, receiptSecondaryTextBudget)),
		})
	}

	reqBody := map[string]interface{}{
		"model": "gpt-5-mini",
		// Minimal reasoning is more accurate here, not merely cheaper: with the
		// default (medium) effort the model consolidates line items it should not
		// and drifts on the item total, while minimal reproduces it exactly. It is
		// also ~4x faster, which keeps a long receipt clear of the client timeout.
		// gpt-5-nano under-extracts long receipts at every effort level.
		"reasoning_effort": "minimal",
		"messages": []map[string]interface{}{
			{
				"role":    "user",
				"content": content,
			},
		},
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "receipt_extraction",
				"strict": true,
				"schema": schema,
			},
		},
	}

	return json.Marshal(reqBody)
}

// imageReceiptPrompt returns the prompt used for vision-based receipt parsing.
func imageReceiptPrompt() string {
	return `Analyze this receipt image and extract all information in a structured format.

Instructions:
- Extract merchant name, address, date, and time
- List all purchased items with their quantities and prices, including add-ons and customizations that have their own price as separate line items; do not split a single named menu item into multiple line items
- If a line item starts with the count (e.g. "2 Can Modelo"), move that leading count to the quantity field and use only the item name as the description
- Do not include taxes, fees, or carrier-imposed charges as line items — those belong in the tax field
- Include subtotal, tax, tip or gratuity (map gratuity/service charge to the tip field), and total. A tip may be handwritten below the printed total; report it in the tip field. Report each amount as the receipt states it; do not compute or reconcile them against each other.
- Identify the currency and return as ISO 4217 code: $ = USD, € = EUR, £ = GBP, ¥ = JPY, ₹ = INR, MX$ = MXN. If the merchant address is in Mexico and the symbol is $, use MXN. If ambiguous, default to USD.
- Identify payment method and last 4 digits of card if visible
- Set isSubscription to true if the receipt indicates a recurring/subscription charge (mentions a subscription, auto-renewal, a billing cycle, "recurring", "renews on", or "billed monthly/yearly"); otherwise false
- If any field is not visible or unclear, use reasonable defaults (empty string for text, 0 for numbers)
- IMPORTANT: For the transaction date, carefully distinguish the DATE from the TIME. They are separate fields. The date is the calendar day (month/day/year) and the time is the clock reading (hours:minutes). Do NOT mix digits from the time into the date.
- The date on the receipt may appear in various formats (e.g. "MARCH 1, 2026", "03/01/26", "2026-03-01"). Parse it carefully and output in YYYY-MM-DD format.
- If the year is ambiguous or appears to be in the past (e.g. 2023 for a recent receipt), double-check the receipt for other year clues (e.g. return policy dates, copyright notices). When in doubt and the current year is 2026, prefer the current year.
- For times, use HH:MM format (24-hour)
- All monetary amounts should be numbers (not strings)
- Merchant name should be a brand name, that is expected to be shown on transactions. It should not contain branch specific information.`
}

// receiptJSONSchema returns the JSON schema used for structured receipt output.
func receiptJSONSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"merchantName":    map[string]string{"type": "string", "description": "Name of the merchant or store. Use the brand name, not the store or branch specific name"},
			"merchantAddress": map[string]string{"type": "string", "description": "Street address of the merchant — do not include the merchant or business name"},
			"transactionDate": map[string]string{"type": "string", "description": "Date of transaction in YYYY-MM-DD format"},
			"transactionTime": map[string]string{"type": "string", "description": "Time of transaction in HH:MM format"},
			"subtotal":        map[string]string{"type": "number", "description": "Subtotal amount before tax and tip"},
			"tax":             map[string]string{"type": "number", "description": "Tax amount"},
			"tip":             map[string]string{"type": "number", "description": "Tip amount if present, otherwise 0"},
			"total":           map[string]string{"type": "number", "description": "Total amount paid"},
			"currency":        map[string]string{"type": "string", "description": "ISO 4217 currency code (e.g. USD, EUR, MXN, GBP, JPY). Determine from currency symbols or text on the receipt."},
			"paymentMethod":   map[string]string{"type": "string", "description": "Card brand or payment type (e.g., Visa, Mastercard, Apple Pay, Credit Card, Cash, Debit) — use the card brand name only; do not include card number digits (those go in lastFourDigits)"},
			"lastFourDigits":  map[string]string{"type": "string", "description": "Last 4 digits of card if visible, otherwise empty string"},
			"isSubscription":  map[string]string{"type": "boolean", "description": "True if this receipt is for a recurring/subscription charge — e.g. it mentions a subscription, auto-renewal, a billing cycle, 'recurring', 'renews on', or 'billed monthly/yearly'. False for ordinary one-off purchases."},
			"lineItems": map[string]interface{}{
				"type":        "array",
				"description": "List of items purchased",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"description": map[string]string{"type": "string", "description": "Item name or description"},
						"quantity":    map[string]string{"type": "number", "description": "Quantity of the item"},
						"unitPrice":   map[string]string{"type": "number", "description": "Price per unit"},
						"totalPrice":  map[string]string{"type": "number", "description": "Total price for this line item (quantity * unitPrice)"},
					},
					"required":             []string{"description", "quantity", "unitPrice", "totalPrice"},
					"additionalProperties": false,
				},
			},
		},
		"required": []string{
			"merchantName", "merchantAddress", "transactionDate", "transactionTime",
			"subtotal", "tax", "tip", "total", "currency", "paymentMethod", "lastFourDigits", "isSubscription", "lineItems",
		},
		"additionalProperties": false,
	}
}

// ParseReceiptCompletion decodes a chat-completions response body produced by
// the receipt-extraction prompt (image or text) into a domain.OCRResult.
// Exported so the OpenAI Batch API path can ingest results using the same
// parsing logic as the inline path.
func ParseReceiptCompletion(respBytes []byte) *domain.OCRResult {
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &completion); err != nil {
		return &domain.OCRResult{Error: fmt.Sprintf("decode completion: %v", err)}
	}
	if completion.Error != nil {
		return &domain.OCRResult{Error: completion.Error.Message}
	}
	if len(completion.Choices) == 0 {
		return &domain.OCRResult{Error: "no choices in OpenAI response"}
	}

	var data openAIReceiptData
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &data); err != nil {
		return &domain.OCRResult{Error: fmt.Sprintf("decode receipt data: %v", err)}
	}

	lineItems := make([]domain.LineItem, 0, len(data.LineItems))
	for _, item := range data.LineItems {
		unitPrice := item.UnitPrice
		if unitPrice == 0 && item.Quantity != 0 {
			unitPrice = item.TotalPrice / item.Quantity
		}
		lineItems = append(lineItems, domain.LineItem{
			Description: item.Description,
			Quantity:    item.Quantity,
			UnitPrice:   unitPrice,
			Price:       item.TotalPrice,
		})
	}
	lineItems = consolidateLineItems(lineItems)

	cur := data.Currency
	if cur == "" {
		cur = "USD"
	}

	subtotal := data.Subtotal
	tax := data.Tax
	tip := data.Tip
	total := applyTipToTotal(data.Subtotal, data.Tax, data.Tip, data.Total)

	var rawBuf bytes.Buffer
	fmt.Fprintf(&rawBuf, "Merchant: %s\n", data.MerchantName)
	fmt.Fprintf(&rawBuf, "Date: %s\n", data.TransactionDate)
	fmt.Fprintf(&rawBuf, "Time: %s\n", data.TransactionTime)
	fmt.Fprintf(&rawBuf, "Subtotal: %.2f %s\n", subtotal, cur)
	fmt.Fprintf(&rawBuf, "Tax: %.2f %s\n", tax, cur)
	fmt.Fprintf(&rawBuf, "Tip: %.2f %s\n", tip, cur)
	fmt.Fprintf(&rawBuf, "Total: %.2f %s\n", total, cur)
	rawBuf.WriteString("Items:\n")
	for _, item := range lineItems {
		fmt.Fprintf(&rawBuf, "%.gx %s - %.2f %s\n", item.Quantity, item.Description, item.Price, cur)
	}

	city, state := ParseAddressCityState(data.MerchantAddress)
	isSubscription := data.IsSubscription

	return &domain.OCRResult{
		RawText:         rawBuf.String(),
		LineItems:       lineItems,
		MerchantName:    data.MerchantName,
		MerchantAddress: data.MerchantAddress,
		MerchantCity:    city,
		MerchantState:   state,
		TransactionDate: data.TransactionDate,
		TransactionTime: data.TransactionTime,
		Subtotal:        &subtotal,
		Tax:             &tax,
		Tip:             &tip,
		Total:           &total,
		Currency:        data.Currency,
		PaymentMethod:   data.PaymentMethod,
		LastFourDigits:  data.LastFourDigits,
		IsSubscription:  &isSubscription,
	}
}

// applyTipToTotal returns the amount actually charged to the card.
//
// Restaurant receipts routinely print a pre-tip total, with the tip and the
// final total added by hand underneath. Vision models read the printed total
// literally, so the tip goes missing from a figure that has to match a bank
// transaction. When the reported total accounts for everything except the tip,
// the tip was charged on top of it.
//
// This deliberately does nothing unless the arithmetic reconciles exactly: the
// schema has no field for fees or discounts (the prompt folds them into tax and
// tip), so a total that does not equal subtotal+tax has something in it we
// cannot model, and guessing there would do more harm than leaving it alone.
//
// Kept out of the prompt on purpose — asking the model to apply this rule makes
// it treat the fields as an equation to satisfy, and it starts inventing a
// subtotal and back-solving tax on receipts that print neither.
func applyTipToTotal(subtotal, tax, tip, total float64) float64 {
	if tip > 0 && math.Abs(total-(subtotal+tax)) < 0.01 {
		return subtotal + tax + tip
	}
	return total
}

// consolidateLineItems merges line items that share the same description and
// unit price into a single entry, summing their quantities and total prices.
// Receipts frequently list the same product on multiple lines (e.g. two
// identical juices rung up separately); collapsing them into one row with an
// updated quantity yields a cleaner item list. Items with a blank description
// are never merged, and original order is preserved by first appearance.
func consolidateLineItems(items []domain.LineItem) []domain.LineItem {
	if len(items) < 2 {
		return items
	}
	type key struct {
		desc  string
		cents int64
	}
	index := make(map[key]int, len(items))
	out := make([]domain.LineItem, 0, len(items))
	for _, item := range items {
		desc := strings.ToLower(strings.TrimSpace(item.Description))
		if desc != "" {
			k := key{desc: desc, cents: int64(math.Round(item.UnitPrice * 100))}
			if pos, ok := index[k]; ok {
				qty := item.Quantity
				if qty == 0 {
					qty = 1
				}
				out[pos].Quantity += qty
				out[pos].Price += item.Price
				continue
			}
			index[k] = len(out)
		}
		out = append(out, item)
	}
	return out
}

// ParseTextAsReceipt sends text (e.g. extracted from an email body or PDF)
// to GTP-5-Nano for structured receipt extraction. This is the same underlying
// call that host applications use for email-body receipt parsing.
// The method is exported so that wrapper applications can call it directly.
func (s *OpenAIService) ParseTextAsReceipt(ctx context.Context, bodyText, contextLabel string, receivedAt *time.Time) *domain.OCRResult {
	return s.ParseTextSectionsAsReceipt(ctx, []TextSection{{Text: bodyText}}, contextLabel, receivedAt)
}

// ParseTextSectionsAsReceipt sends one or more labeled text sources that all
// describe the SAME transaction (e.g. a PDF attachment and the email body it
// arrived in) for a single merged receipt extraction. With one section this is
// exactly ParseTextAsReceipt.
func (s *OpenAIService) ParseTextSectionsAsReceipt(ctx context.Context, sections []TextSection, contextLabel string, receivedAt *time.Time) *domain.OCRResult {
	if s.apiKey == "" {
		return &domain.OCRResult{Error: "OpenAI API key not configured"}
	}

	slog.Info("[OpenAI] parsing text for receipt data", "context", contextLabel, "sources", len(sections))

	for _, sec := range sections {
		if len(sec.Text) > 8000 && strings.Count(sec.Text[:8000], "{") > 20 && strings.Count(sec.Text[:8000], "}") > 20 {
			slog.Warn("[OpenAI] text appears to still contain CSS, truncating", "context", contextLabel, "source", sec.Label)
		}
	}

	bodyBytes, err := BuildTextReceiptRequestSections(sections, contextLabel, receivedAt)
	if err != nil {
		return &domain.OCRResult{Error: fmt.Sprintf("marshal request: %v", err)}
	}

	respBytes, err := s.DoRequest(ctx, bodyBytes)
	if err != nil {
		return &domain.OCRResult{Error: err.Error()}
	}

	result := ParseReceiptCompletion(respBytes)
	if result.Error == "" {
		slog.Info("[OpenAI] successfully parsed text receipt",
			"items", len(result.LineItems),
		)
	}
	return result
}

// TextSection is one labeled block of text describing a transaction. Several
// sections are merged into a single extraction request when more than one place
// in the same message describes the same transaction — most often a PDF
// attachment and the email body it arrived in, where the attachment carries the
// totals and the body carries the itemization.
type TextSection struct {
	Label string // e.g. "Attached document: invoice.pdf", "Email body"
	Text  string
}

const (
	// receiptTextTotalBudget is the combined character budget across all
	// sections — the same cap the single-text path has always applied.
	receiptTextTotalBudget = 8000
	// receiptSecondaryTextBudget caps each non-primary section so a long, noisy
	// email body cannot crowd out the authoritative attachment text.
	receiptSecondaryTextBudget = 2000
	// receiptPrimaryTextFloor is the minimum the primary section keeps even when
	// the secondary sections consume their full budget.
	receiptPrimaryTextFloor = 4000
)

// receiptTotalAnchors are keywords used to locate the "total" region of receipt
// text so truncation preserves the items section rather than front-truncating.
var receiptTotalAnchors = []string{"Total", "TOTAL", "Amount Due", "AMOUNT DUE", "Amount Paid", "AMOUNT PAID", "Subtotal", "SUBTOTAL"}

// anchoredTruncate returns up to maxLen characters centred around the last
// occurrence of any anchor word. Items appear before the total line, so
// keeping 3000 chars before and 1000 after the anchor retains the full item
// table. Falls back to front-truncation if no anchor is found.
func anchoredTruncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	anchorPos := -1
	for _, anchor := range receiptTotalAnchors {
		if pos := strings.LastIndex(text, anchor); pos > anchorPos {
			anchorPos = pos
		}
	}
	if anchorPos < 0 {
		return text[:maxLen]
	}
	start := anchorPos - 3000
	if start < 0 {
		start = 0
	}
	end := anchorPos + 1000
	if end > len(text) {
		end = len(text)
	}
	if end-start > maxLen {
		end = start + maxLen
	}
	return text[start:end]
}

// BuildTextReceiptRequest constructs the chat-completions request body for
// text-based receipt parsing (e.g. email body, extracted PDF text). Exported so
// callers (e.g. the OpenAI Batch API path) can produce identical request
// payloads without sending them inline.
//
// Applies the same body-length guards as ParseTextAsReceipt: 8000-char hard cap
// plus an aggressive 4000-char truncation if the text still contains CSS-like
// `{`/`}` clusters (a sign that HTML stripping missed style blocks).
func BuildTextReceiptRequest(bodyText, contextLabel string, receivedAt *time.Time) ([]byte, error) {
	return BuildTextReceiptRequestSections([]TextSection{{Text: bodyText}}, contextLabel, receivedAt)
}

// clampReceiptText applies the receipt-text length guards to a single section: a
// hard character budget, plus anchor-aware truncation to half the budget when
// the text still contains CSS-like `{`/`}` clusters (a sign HTML stripping
// missed style blocks). At budget=8000 this is exactly the guard
// BuildTextReceiptRequest has always applied.
func clampReceiptText(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	if strings.Count(text, "{") > 20 && strings.Count(text, "}") > 20 {
		return anchoredTruncate(text, budget/2)
	}
	return text[:budget]
}

// clampSections applies per-section budgets BEFORE concatenation, so a single
// combined cap can never truncate a later section away entirely. Secondary
// sections are clamped first and the primary section takes whatever is left of
// the total budget — so when the secondary text is short or absent, the primary
// keeps the full budget it would have had on its own.
func clampSections(sections []TextSection) []TextSection {
	out := make([]TextSection, len(sections))
	used := 0
	for i := len(sections) - 1; i >= 1; i-- {
		out[i] = TextSection{
			Label: sections[i].Label,
			Text:  clampReceiptText(sections[i].Text, receiptSecondaryTextBudget),
		}
		used += len(out[i].Text)
	}
	primaryBudget := receiptTextTotalBudget - used
	if primaryBudget < receiptPrimaryTextFloor {
		primaryBudget = receiptPrimaryTextFloor
	}
	out[0] = TextSection{
		Label: sections[0].Label,
		Text:  clampReceiptText(sections[0].Text, primaryBudget),
	}
	return out
}

// mergedSourcesInstructions are appended to the standard instruction list only
// when more than one source is present. They exist to counter three behaviours
// observed on multi-source input: the model dropping items that appear in only
// one source to keep the item total consistent with the stated subtotal, the
// model listing a shared item once per source (which consolidateLineItems then
// merges by SUMMING quantity and price, silently doubling it), and the
// secondary source overriding the attachment's merchant or totals.
const mergedSourcesInstructions = `
- This text contains more than one source (e.g. an attached document and the email body it arrived in) describing the SAME transaction. Extract ONE receipt covering all of them, taking the merchant, date, and amounts from the first source listed when the sources disagree.
- Include every purchased item from every source, even if that means the line items no longer add up to the subtotal or total. Do not change the subtotal, tax, tip, or total to make them reconcile, and do not drop an item to make them reconcile.
- An item restated in another source is still ONE line item — list it exactly once. This de-duplication applies only ACROSS sources: when a single source legitimately lists the same product on several lines, those are separate purchases, and the quantity and total price you report must cover all of them.`

// mergedSourcesImageInstructions is the vision-prompt counterpart of
// mergedSourcesInstructions, appended only when a supplementary text source
// accompanies the image.
const mergedSourcesImageInstructions = `
- A text source follows this image (the body of the email the image arrived in) describing the SAME transaction. Extract ONE receipt covering both, taking the merchant, date, and amounts from the image when the two disagree.
- Include every purchased item from both sources, even if that means the line items no longer add up to the subtotal or total. Do not change the subtotal, tax, tip, or total to make them reconcile, and do not drop an item to make them reconcile.
- An item restated in the text source is still ONE line item — list it exactly once. This de-duplication applies only ACROSS the two sources: when the receipt itself rings the same product up on several lines, those are separate purchases, and the quantity and total price you report must cover all of them.`

// renderTextSections joins sections for substitution into the prompt's `Text:`
// slot. A single section renders as bare text, exactly as it always has, so the
// one-source request stays byte-identical.
func renderTextSections(sections []TextSection) string {
	if len(sections) == 1 {
		return sections[0].Text
	}
	var b strings.Builder
	for i, sec := range sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		label := sec.Label
		if label == "" {
			label = fmt.Sprintf("Source %d", i+1)
		}
		fmt.Fprintf(&b, "--- %s ---\n%s", label, sec.Text)
	}
	return b.String()
}

// BuildTextReceiptRequestSections builds one chat-completions request from one
// or more labeled sources that all describe the SAME transaction. sections[0]
// is treated as authoritative for the totals and merchant identity; later
// sections may only contribute additional line items and fields the primary
// source omits.
//
// Sections whose text is blank are dropped. With a single section the emitted
// request is byte-identical to the long-standing single-text request, so
// captured goldens remain valid.
func BuildTextReceiptRequestSections(sections []TextSection, contextLabel string, receivedAt *time.Time) ([]byte, error) {
	kept := make([]TextSection, 0, len(sections))
	for _, sec := range sections {
		if strings.TrimSpace(sec.Text) != "" {
			kept = append(kept, sec)
		}
	}
	if len(kept) == 0 {
		// Preserve the historical behaviour for empty input: one empty section.
		kept = []TextSection{{}}
	}
	kept = clampSections(kept)
	bodyText := renderTextSections(kept)

	prompt := fmt.Sprintf(`Extract receipt/invoice data from this text.

%s

Text:
%s

Instructions:
- Extract the merchant name, address, transaction date, and time
- List all purchased items with quantities and prices, including add-ons, substitutions, and modifiers as separate line items; do not include the quantity number in the description field
- Do not include taxes, fees, or carrier-imposed charges as line items — those belong in the tax field
- Include subtotal, tax, tip or gratuity (map gratuity/service charge to the tip field), and total
- Identify the currency and return as ISO 4217 code: $ = USD, € = EUR, £ = GBP, ¥ = JPY, ₹ = INR, MX$ = MXN. If text says "USD" or similar, use that. If ambiguous, default to USD.
- Identify payment method and last 4 digits of card if mentioned
- Set isSubscription to true if the text indicates a recurring/subscription charge (mentions a subscription, auto-renewal, a billing cycle, "recurring", "renews on", or "billed monthly/yearly"); otherwise false
- If any field is not present, use reasonable defaults (empty string for text, 0 for numbers)
- For dates, use YYYY-MM-DD format
- For times, use HH:MM format (24-hour)
- All monetary amounts should be numbers (not strings)
- The merchant name should be the actual BUSINESS name, not the payment processor — if "Stripe", "Square", "PayPal", or similar appears as the sender, use the underlying merchant name instead`, contextLabel, bodyText)

	if len(kept) > 1 {
		prompt += mergedSourcesInstructions
	}

	if receivedAt != nil {
		prompt += fmt.Sprintf(`
- IMPORTANT: This document was received on %s. The receipt/transaction date MUST be on or before this date. Never produce a date in the future relative to the received date.`, receivedAt.Format("2006-01-02"))
	}

	reqBody := map[string]interface{}{
		"model": "gpt-5-nano",
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": prompt,
					},
				},
			},
		},
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "receipt_extraction",
				"strict": true,
				"schema": receiptJSONSchema(),
			},
		},
	}

	return json.Marshal(reqBody)
}

// ---------------------------------------------------------------------------
// AI-suggested matching (v2: deterministic-first, LLM only for merchant disambiguation)
// ---------------------------------------------------------------------------

// MatchSuggestion represents a single receipt-to-transaction match decision.
type MatchSuggestion struct {
	ReceiptID     string  `json:"receipt_id"`
	TransactionID string  `json:"transaction_id"`
	Confidence    float64 `json:"confidence"`
	MatchType     string  `json:"match_type"` // "matched" (>=0.85) or "suggested" (0.70-0.85)
	Reason        string  `json:"reason"`
	Flag          string  `json:"flag,omitempty"` // fx_suspected, amount_mismatch, date_mismatch, clean
}

// MerchantDisambiguationResult is the LLM response for merchant name comparison.
type MerchantDisambiguationResult struct {
	SameBusiness bool    `json:"same_business"`
	Confidence   float64 `json:"confidence"`
	Reason       string  `json:"reason"`
}

// DisambiguateMerchant asks the LLM a simple yes/no question: are these two
// merchant names the same business? This is a focused, cheap call that replaces
// the old full-matching EvaluateCandidates prompt.
func (s *OpenAIService) DisambiguateMerchant(ctx context.Context, receiptMerchant, txMerchant, txName string) (*MerchantDisambiguationResult, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key not configured")
	}

	// Build the transaction side — include both merchant and name if different.
	txSide := txMerchant
	if txName != "" && txName != txMerchant {
		txSide = fmt.Sprintf("%s (also shown as: %s)", txMerchant, txName)
	}
	if txMerchant == "" {
		txSide = txName
	}

	prompt := fmt.Sprintf(`You are a merchant name matching engine. Determine if these two names refer to the SAME business.

Receipt merchant: "%s"
Bank transaction merchant: "%s"

Rules:
- Ignore casing, punctuation, prefixes like "SQ *", "TST *", "AplPay", "OPENAI *CHATGPT SUBS".
- Common abbreviations are fine: "AMZN" = "Amazon", "SBUX" = "Starbucks", "UBER" = "Uber Technologies".
- HARD RULE: Different companies are NEVER the same, even if names share words.
  Examples of DIFFERENT businesses: "Apple" != "Applebee's", "Post Bar" != "US Post Office",
  "CVS" != "Target", "Netflix" != "Spotify", "DTE Energy" != "Spotify".
- When in doubt, say false. A wrong "true" is much worse than a wrong "false".

Respond with a JSON object.`, receiptMerchant, txSide)

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"same_business": map[string]string{"type": "boolean", "description": "true if the two names refer to the same business"},
			"confidence":    map[string]string{"type": "number", "description": "Confidence 0.0-1.0"},
			"reason":        map[string]string{"type": "string", "description": "Brief explanation"},
		},
		"required":             []string{"same_business", "confidence", "reason"},
		"additionalProperties": false,
	}

	reqBody := map[string]interface{}{
		"model": "gpt-5-nano",
		"messages": []map[string]interface{}{
			{"role": "user", "content": []map[string]interface{}{{"type": "text", "text": prompt}}},
		},
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "merchant_disambiguation",
				"strict": true,
				"schema": schema,
			},
		},
		"max_completion_tokens": 2048,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	respBytes, err := s.DoRequest(ctx, bodyBytes)
	if err != nil {
		return nil, err
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &completion); err != nil {
		return nil, fmt.Errorf("decode completion: %w", err)
	}
	if completion.Error != nil {
		return nil, fmt.Errorf("openai error: %s", completion.Error.Message)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI response")
	}

	var result MerchantDisambiguationResult
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("decode disambiguation: %w", err)
	}

	return &result, nil
}

// CorrectCategory asks the LLM whether receipt line items suggest a different
// Plaid PFC category than what was assigned. Uses GTP-5-Nano for cost efficiency.
func (s *OpenAIService) CorrectCategory(ctx context.Context, lineItems []domain.LineItem, currentPrimary, currentDetailed string) (*domain.CategoryCorrectionResult, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key not configured")
	}

	var descriptions []string
	for _, li := range lineItems {
		if li.Description != "" {
			desc := li.Description
			if li.Quantity > 1 {
				desc = fmt.Sprintf("%s x%.0f", li.Description, li.Quantity)
			}
			descriptions = append(descriptions, desc)
			if len(descriptions) >= 15 {
				break
			}
		}
	}

	prompt := fmt.Sprintf(`You are a transaction category classifier. A bank categorized this transaction as "%s" (detailed: "%s").

Here are the actual items purchased according to the receipt:
%s

Plaid PFC primary categories: FOOD_AND_DRINK, TRANSPORTATION, GENERAL_MERCHANDISE, ENTERTAINMENT, MEDICAL, PERSONAL_CARE, GENERAL_SERVICES, GOVERNMENT_AND_NON_PROFIT, HOME_IMPROVEMENT, RENT_AND_UTILITIES, TRAVEL, TRANSFER_IN, TRANSFER_OUT, LOAN_PAYMENTS, BANK_FEES, INCOME.

Based on what was ACTUALLY purchased:
1. Is the bank's category "%s" correct for these items?
2. If not, what should the correct primary and detailed categories be?

Rules:
- Only suggest a correction if the items clearly belong to a DIFFERENT category.
- If the bank category seems reasonable, say should_correct=false.
- Use the Plaid PFC taxonomy for category names.
- The detailed category format is PRIMARY_SUBCATEGORY (e.g., FOOD_AND_DRINK_GROCERIES, FOOD_AND_DRINK_RESTAURANT, TRANSPORTATION_GAS).

Respond with a JSON object.`, currentPrimary, currentDetailed, strings.Join(descriptions, "\n"), currentPrimary)

	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"should_correct":         map[string]interface{}{"type": "boolean", "description": "true if the bank category is wrong"},
			"corrected_pfc_primary":  map[string]interface{}{"type": "string", "description": "Correct primary category"},
			"corrected_pfc_detailed": map[string]interface{}{"type": "string", "description": "Correct detailed category"},
			"reason":                 map[string]interface{}{"type": "string", "description": "Brief explanation"},
		},
		"required":             []string{"should_correct", "corrected_pfc_primary", "corrected_pfc_detailed", "reason"},
		"additionalProperties": false,
	}

	reqBody := map[string]interface{}{
		"model": "gpt-5-nano",
		"messages": []map[string]interface{}{
			{"role": "user", "content": []map[string]interface{}{{"type": "text", "text": prompt}}},
		},
		"response_format": map[string]interface{}{
			"type": "json_schema",
			"json_schema": map[string]interface{}{
				"name":   "category_correction",
				"strict": true,
				"schema": schema,
			},
		},
		"max_completion_tokens": 2048,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	respBytes, err := s.DoRequest(ctx, bodyBytes)
	if err != nil {
		return nil, err
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &completion); err != nil {
		return nil, fmt.Errorf("decode completion: %w", err)
	}
	if completion.Error != nil {
		return nil, fmt.Errorf("openai error: %s", completion.Error.Message)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI response")
	}

	var result domain.CategoryCorrectionResult
	if err := json.Unmarshal([]byte(completion.Choices[0].Message.Content), &result); err != nil {
		return nil, fmt.Errorf("decode category correction: %w", err)
	}
	result.Source = "llm"
	return &result, nil
}

// ---------------------------------------------------------------------------
// Text extraction helpers (used as fallback when OpenAI returns raw text only)
// ---------------------------------------------------------------------------

var (
	totalPattern = regexp.MustCompile(`(?i)(?:grand\s+total|total\s+amount|total\s+due|amount\s+due|order\s+total|total)[^\d\n]*\$?([\d]+\.[\d]{2})`)
	datePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(\d{1,2}/\d{1,2}/\d{4})`),
		regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`),
		regexp.MustCompile(`(\d{1,2}-\d{1,2}-\d{4})`),
	}
)

// ExtractTotal extracts the total amount from raw receipt text.
func ExtractTotal(rawText string) *float64 {
	if rawText == "" {
		return nil
	}
	lines := strings.Split(rawText, "\n")
	var lastTotal *float64
	for _, line := range lines {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		if strings.Contains(upper, "SUBTOTAL") || strings.Contains(upper, "SUB TOTAL") {
			continue
		}
		if m := totalPattern.FindStringSubmatch(line); m != nil {
			v, err := strconv.ParseFloat(m[1], 64)
			if err == nil {
				lastTotal = &v
			}
		}
	}
	return lastTotal
}

// ExtractDate extracts a date from raw receipt text.
func ExtractDate(rawText string) *time.Time {
	if rawText == "" {
		return nil
	}
	for _, pat := range datePatterns {
		if m := pat.FindStringSubmatch(rawText); m != nil {
			dateStr := m[1]
			for _, layout := range []string{"01/02/2006", "2006-01-02", "01-02-2006"} {
				if t, err := time.Parse(layout, dateStr); err == nil {
					return &t
				}
			}
		}
	}
	return nil
}
