package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// GetReceiptsHandler returns receipts with optional filters.
func (h *ToolHandlers) GetReceiptsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	filter := domain.ReceiptFilter{
		Status:      stringArg(req, "status"),
		Source:      stringArg(req, "source"),
		MatchMethod: stringArg(req, "match_method"),
		Search:      stringArg(req, "search"),
		DateFrom:    stringArg(req, "date_from"),
		DateTo:      stringArg(req, "date_to"),
		SortBy:      stringArg(req, "sort_by"),
		SortOrder:   stringArg(req, "sort_order"),
	}

	if v := stringArg(req, "amount_min"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			filter.AmountMin = &f
		}
	}
	if v := stringArg(req, "amount_max"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			filter.AmountMax = &f
		}
	}

	receipts, _, err := h.ReceiptSvc.ListWithMatches(ctx, h.UserID, filter)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get receipts: %v", err)), nil
	}

	data, _ := json.Marshal(receipts)
	return mcp.NewToolResultText(string(data)), nil
}

// IngestReceiptHandler creates an upload session and returns a URL for the
// user to upload a receipt image in their browser.
func (h *ToolHandlers) IngestReceiptHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	baseURL, err := h.WebServer.EnsureRunning()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to start local web server: %v", err)), nil
	}

	sessionID := uuid.New().String()
	h.WebServer.RegisterUploadSession(sessionID)

	uploadURL := fmt.Sprintf("%s/receipts/upload?session=%s", baseURL, sessionID)

	return mcp.NewToolResultText(fmt.Sprintf(
		`Tell the user: "I can't process image files directly through our connection, so I've created a secure upload link for you. Please open the link below in your browser, drop or select your receipt image, and I'll automatically process it once uploaded."

Upload URL: %s

After showing the URL to the user, immediately call wait_for_receipt_upload with session_id: %s`,
		uploadURL, sessionID,
	)), nil
}

// WaitForReceiptUploadHandler blocks until the user completes the receipt
// upload via the browser, then returns the OCR and matching results.
func (h *ToolHandlers) WaitForReceiptUploadHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sessionID := stringArg(req, "session_id")
	if sessionID == "" {
		return mcp.NewToolResultError("session_id is required"), nil
	}

	ch, ok := h.WebServer.GetUploadChannel(sessionID)
	if !ok {
		return mcp.NewToolResultError("unknown session — it may have already completed or expired"), nil
	}

	select {
	case result := <-ch:
		h.WebServer.CleanupUploadSession(sessionID)
		if result.Error != "" {
			return mcp.NewToolResultError(fmt.Sprintf("receipt scan failed: %s", result.Error)), nil
		}
		return mcp.NewToolResultText(string(result.Data)), nil
	case <-time.After(5 * time.Minute):
		h.WebServer.CleanupUploadSession(sessionID)
		return mcp.NewToolResultError("upload timed out — the user did not upload an image within 5 minutes"), nil
	case <-ctx.Done():
		h.WebServer.CleanupUploadSession(sessionID)
		return mcp.NewToolResultError("request cancelled"), nil
	}
}

// BatchReceiptIngestionHandler scans a directory for receipt images and
// processes each one through OCR and auto-matching.
func (h *ToolHandlers) BatchReceiptIngestionHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dirPath := stringArg(req, "directory_path")
	if dirPath == "" {
		return mcp.NewToolResultError("directory_path is required"), nil
	}

	// Sync transactions first to have latest data for matching.
	h.PlaidSvc.SyncTransactions(ctx, h.UserID, "")

	storageSvc := h.WebServer.StorageSvc()

	result, err := h.ReceiptSvc.BatchScan(ctx, h.UserID, dirPath, storageSvc)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("batch ingestion failed: %v", err)), nil
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

// MatchReceiptHandler manually matches a receipt to a transaction.
func (h *ToolHandlers) MatchReceiptHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	receiptID := stringArg(req, "receipt_id")
	transactionID := stringArg(req, "transaction_id")
	if receiptID == "" || transactionID == "" {
		return mcp.NewToolResultError("receipt_id and transaction_id are required"), nil
	}

	rID, err := uuid.Parse(receiptID)
	if err != nil {
		return mcp.NewToolResultError("invalid receipt_id format"), nil
	}

	if err := h.ReceiptSvc.Match(ctx, h.UserID, rID, transactionID, ""); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to match receipt: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Receipt %s matched to transaction %s", receiptID, transactionID)), nil
}

// UnmatchReceiptHandler removes an existing receipt-transaction match.
func (h *ToolHandlers) UnmatchReceiptHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	receiptID := stringArg(req, "receipt_id")
	if receiptID == "" {
		return mcp.NewToolResultError("receipt_id is required"), nil
	}

	rID, err := uuid.Parse(receiptID)
	if err != nil {
		return mcp.NewToolResultError("invalid receipt_id format"), nil
	}

	if err := h.ReceiptSvc.Unmatch(ctx, h.UserID, rID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to unmatch receipt: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Receipt %s unmatched from its transaction", receiptID)), nil
}

// ConfirmSuggestionHandler confirms an auto-suggested receipt-transaction match.
func (h *ToolHandlers) ConfirmSuggestionHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	receiptID := stringArg(req, "receipt_id")
	if receiptID == "" {
		return mcp.NewToolResultError("receipt_id is required"), nil
	}

	rID, err := uuid.Parse(receiptID)
	if err != nil {
		return mcp.NewToolResultError("invalid receipt_id format"), nil
	}

	if err := h.ReceiptSvc.ConfirmSuggestion(ctx, h.UserID, rID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to confirm suggestion: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Match suggestion confirmed for receipt %s", receiptID)), nil
}

// RejectSuggestionHandler rejects an auto-suggested receipt-transaction match.
func (h *ToolHandlers) RejectSuggestionHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	receiptID := stringArg(req, "receipt_id")
	if receiptID == "" {
		return mcp.NewToolResultError("receipt_id is required"), nil
	}

	rID, err := uuid.Parse(receiptID)
	if err != nil {
		return mcp.NewToolResultError("invalid receipt_id format"), nil
	}

	if err := h.ReceiptSvc.RejectSuggestion(ctx, h.UserID, rID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to reject suggestion: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Match suggestion rejected for receipt %s", receiptID)), nil
}
