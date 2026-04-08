package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
)

// GetTransactionsHandler syncs transactions from Plaid and returns them
// with optional filters.
func (h *ToolHandlers) GetTransactionsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Sync first to ensure we have the latest transactions.
	if _, err := h.PlaidSvc.SyncTransactions(ctx, h.UserID, ""); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to sync transactions: %v", err)), nil
	}

	filter := domain.TransactionFilter{
		Search:      stringArg(req, "search"),
		DateFrom:    stringArg(req, "date_from"),
		DateTo:      stringArg(req, "date_to"),
		PFCPrimary:  stringArg(req, "category"),
		PFCDetailed: stringArg(req, "subcategory"),
		Matched:     stringArg(req, "matched"),
		Pending:     stringArg(req, "pending"),
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

	result, err := h.PlaidSvc.ListTransactions(ctx, h.UserID, filter)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get transactions: %v", err)), nil
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}
