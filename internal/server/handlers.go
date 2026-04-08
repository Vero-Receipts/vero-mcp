package server

import (
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/Vero-Receipts/vero-mcp/internal/webserver"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	pkgservice "github.com/Vero-Receipts/vero-mcp/pkg/service"
)

// ToolHandlers holds all dependencies needed by MCP tool handlers.
type ToolHandlers struct {
	UserID         uuid.UUID
	PlaidSvc       *pkgservice.PlaidService
	ReceiptSvc     *pkgservice.ReceiptService
	NoteSvc        *pkgservice.NoteService
	LabelSvc       *pkgservice.LabelService
	ReceiptItemSvc *pkgservice.ReceiptItemService
	UserRepo       repository.UserRepository
	WebServer      *webserver.LocalWebServer
}

// stringArg extracts a string argument from an MCP tool call request.
func stringArg(req mcp.CallToolRequest, key string) string {
	args := req.GetArguments()
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
