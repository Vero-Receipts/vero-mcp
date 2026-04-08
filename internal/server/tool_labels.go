package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// ListLabelsHandler returns all labels for the user.
func (h *ToolHandlers) ListLabelsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	labels, err := h.LabelSvc.ListLabels(ctx, h.UserID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to list labels: %v", err)), nil
	}

	data, _ := json.Marshal(labels)
	return mcp.NewToolResultText(string(data)), nil
}

// CreateLabelHandler creates a new label.
func (h *ToolHandlers) CreateLabelHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name := stringArg(req, "name")
	color := stringArg(req, "color")

	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	label, err := h.LabelSvc.CreateLabel(ctx, h.UserID, name, color)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to create label: %v", err)), nil
	}

	data, _ := json.Marshal(label)
	return mcp.NewToolResultText(string(data)), nil
}

// AddLabelHandler assigns a label to an entity.
func (h *ToolHandlers) AddLabelHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	labelID := stringArg(req, "label_id")
	entityType := stringArg(req, "entity_type")
	entityID := stringArg(req, "entity_id")

	if labelID == "" || entityType == "" || entityID == "" {
		return mcp.NewToolResultError("label_id, entity_type, and entity_id are required"), nil
	}

	lID, err := uuid.Parse(labelID)
	if err != nil {
		return mcp.NewToolResultError("invalid label_id format"), nil
	}

	assignment, err := h.LabelSvc.AssignLabel(ctx, h.UserID, lID, entityType, entityID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to assign label: %v", err)), nil
	}

	data, _ := json.Marshal(assignment)
	return mcp.NewToolResultText(string(data)), nil
}

// RemoveLabelHandler removes a label from an entity.
func (h *ToolHandlers) RemoveLabelHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	labelID := stringArg(req, "label_id")
	entityType := stringArg(req, "entity_type")
	entityID := stringArg(req, "entity_id")

	if labelID == "" || entityType == "" || entityID == "" {
		return mcp.NewToolResultError("label_id, entity_type, and entity_id are required"), nil
	}

	lID, err := uuid.Parse(labelID)
	if err != nil {
		return mcp.NewToolResultError("invalid label_id format"), nil
	}

	if err := h.LabelSvc.UnassignLabel(ctx, h.UserID, lID, entityType, entityID); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to remove label: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Label %s removed from %s %s", labelID, entityType, entityID)), nil
}

// GetReceiptItemsHandler returns line items for a receipt.
func (h *ToolHandlers) GetReceiptItemsHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	receiptID := stringArg(req, "receipt_id")
	if receiptID == "" {
		return mcp.NewToolResultError("receipt_id is required"), nil
	}

	rID, err := uuid.Parse(receiptID)
	if err != nil {
		return mcp.NewToolResultError("invalid receipt_id format"), nil
	}

	items, err := h.ReceiptItemSvc.ListByReceipt(ctx, h.UserID, rID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get receipt items: %v", err)), nil
	}

	data, _ := json.Marshal(items)
	return mcp.NewToolResultText(string(data)), nil
}
