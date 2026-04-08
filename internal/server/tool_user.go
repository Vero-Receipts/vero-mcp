package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// GetUserContextHandler returns a consolidated snapshot of the user's
// identity and integration state.
func (h *ToolHandlers) GetUserContextHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	user, err := h.UserRepo.FindByID(ctx, h.UserID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get user info: %v", err)), nil
	}

	accounts, _ := h.PlaidSvc.GetAccounts(ctx, h.UserID)

	result := map[string]interface{}{
		"user": map[string]interface{}{
			"id":                user.ID.String(),
			"name":              user.Name,
			"is_bank_connected": user.IsBankConnected,
		},
		"accounts_count": len(accounts),
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}
