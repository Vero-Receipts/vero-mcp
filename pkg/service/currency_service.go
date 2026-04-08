package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type CurrencyService struct {
	httpClient *http.Client
}

func NewCurrencyService() *CurrencyService {
	return &CurrencyService{
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

type frankfurterResponse struct {
	Amount float64            `json:"amount"`
	Base   string             `json:"base"`
	Date   string             `json:"date"`
	Rates  map[string]float64 `json:"rates"`
}

// ConvertToUSD converts the given amount in fromCurrency to USD using the
// Frankfurter API historical rate for dateStr (YYYY-MM-DD).
// Returns the amount unchanged when fromCurrency is "USD" or empty.
func (s *CurrencyService) ConvertToUSD(ctx context.Context, amount float64, fromCurrency, dateStr string) (float64, error) {
	if fromCurrency == "USD" || fromCurrency == "" {
		return amount, nil
	}

	url := fmt.Sprintf("https://api.frankfurter.dev/v1/%s?from=%s&to=USD&amount=%.2f",
		dateStr, fromCurrency, amount)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("frankfurter request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("frankfurter API returned %d: %s", resp.StatusCode, string(body))
	}

	var result frankfurterResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	usdAmount, ok := result.Rates["USD"]
	if !ok {
		return 0, fmt.Errorf("USD rate not found in response for %s", fromCurrency)
	}

	slog.Info("[CurrencyService] converted to USD",
		"from", fromCurrency, "original", amount,
		"usd", usdAmount, "date", dateStr,
	)
	return usdAmount, nil
}
