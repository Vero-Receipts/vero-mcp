package server

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ConnectBankAccountHandler creates a Plaid Link token and returns a URL
// for the user to connect their bank account in a browser.
func (h *ToolHandlers) ConnectBankAccountHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	result, err := h.PlaidSvc.CreateLinkToken(ctx, h.UserID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create link token: %v", err)), nil
	}

	resultJSON, _ := json.Marshal(result)
	var tokenResp struct {
		LinkToken string `json:"link_token"`
	}
	json.Unmarshal(resultJSON, &tokenResp)

	baseURL, err := h.WebServer.EnsureRunning()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to start local web server: %v", err)), nil
	}

	// Register a pending channel to wait for the user to complete Plaid Link.
	ch := h.WebServer.RegisterLinkWait(tokenResp.LinkToken)

	linkURL := fmt.Sprintf("%s/plaid/link?token=%s", baseURL, tokenResp.LinkToken)

	// Wait for the user to complete the flow in background (with timeout).
	go func() {
		select {
		case publicToken := <-ch:
			h.WebServer.CleanupLinkWait(tokenResp.LinkToken)
			if _, exchangeErr := h.PlaidSvc.ExchangePublicToken(context.Background(), h.UserID, publicToken); exchangeErr != nil {
				fmt.Printf("exchange error: %v\n", exchangeErr)
			}
		case <-time.After(30 * time.Minute):
			h.WebServer.CleanupLinkWait(tokenResp.LinkToken)
		}
	}()

	return mcp.NewToolResultText(fmt.Sprintf(
		"Please open the following URL in your browser to connect your bank account:\n\n%s\n\nThe link expires in 30 minutes.",
		linkURL,
	)), nil
}

// GetAccountsHandler returns the user's connected bank accounts.
func (h *ToolHandlers) GetAccountsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	accounts, err := h.PlaidSvc.GetAccounts(ctx, h.UserID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get accounts: %v", err)), nil
	}

	data, _ := json.Marshal(map[string]interface{}{"accounts": accounts})
	return mcp.NewToolResultText(string(data)), nil
}

// DisconnectAccountHandler removes a bank connection by account ID.
func (h *ToolHandlers) DisconnectAccountHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	accountID := stringArg(req, "account_id")
	if accountID == "" {
		return mcp.NewToolResultError("account_id is required"), nil
	}

	if err := h.PlaidSvc.DeleteAccount(ctx, h.UserID, accountID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to disconnect account: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Account %s disconnected (all accounts under the same institution have been removed)", accountID)), nil
}
