package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	ServerName    = "Vero MCP"
	ServerVersion = "0.1.0"

	serverInstructions = `You are connected to Vero, a local digital receipt platform that replaces cryptic merchant transaction codes with itemized, detailed receipts.

Core concepts:
- Vero connects to your bank accounts via Plaid to pull transactions.
- Transactions can be matched to detailed receipts (uploaded and scanned via OCR).
- Privacy-first: all data stays local on your machine.

When a user first connects, greet them, call get_user_context to understand their current setup, and let them know what you can help with based on the available tools.

If the user hasn't connected a bank account yet, suggest they do so first.

When presenting financial data, format amounts as currency and dates in a human-readable format.`
)

// NewMCPServer constructs and configures the MCP server instance.
func NewMCPServer(h *ToolHandlers) *server.MCPServer {
	s := server.NewMCPServer(
		ServerName,
		ServerVersion,
		server.WithToolCapabilities(false),
		server.WithToolHandlerMiddleware(toolLoggingMiddleware),
		server.WithRecovery(),
		server.WithInstructions(serverInstructions),
	)

	RegisterTools(s, h)

	return s
}

// toolLoggingMiddleware logs each tool invocation with duration and error info.
func toolLoggingMiddleware(next server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		toolName := req.Params.Name

		res, err := next(ctx, req)

		duration := time.Since(start)
		if err != nil {
			slog.Error("tool call failed", "tool", toolName, "duration_ms", duration.Milliseconds(), "error", err)
		} else {
			slog.Info("tool call completed", "tool", toolName, "duration_ms", duration.Milliseconds())
		}

		return res, err
	}
}
