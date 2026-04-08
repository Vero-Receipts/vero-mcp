package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// AddNoteHandler adds a note to a transaction or receipt.
func (h *ToolHandlers) AddNoteHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	entityType := stringArg(req, "entity_type")
	entityID := stringArg(req, "entity_id")
	content := stringArg(req, "content")

	if entityType == "" || entityID == "" || content == "" {
		return mcp.NewToolResultError("entity_type, entity_id, and content are required"), nil
	}

	note, err := h.NoteSvc.Create(ctx, h.UserID, entityType, entityID, content)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to add note: %v", err)), nil
	}

	data, _ := json.Marshal(note)
	return mcp.NewToolResultText(string(data)), nil
}

// GetNotesHandler returns notes for a transaction or receipt.
func (h *ToolHandlers) GetNotesHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	entityType := stringArg(req, "entity_type")
	entityID := stringArg(req, "entity_id")

	if entityType == "" || entityID == "" {
		return mcp.NewToolResultError("entity_type and entity_id are required"), nil
	}

	notes, err := h.NoteSvc.ListByEntity(ctx, h.UserID, entityType, entityID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get notes: %v", err)), nil
	}

	data, _ := json.Marshal(notes)
	return mcp.NewToolResultText(string(data)), nil
}

// DeleteNoteHandler deletes a note by ID.
func (h *ToolHandlers) DeleteNoteHandler(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	noteID := stringArg(req, "note_id")
	if noteID == "" {
		return mcp.NewToolResultError("note_id is required"), nil
	}

	id, err := uuid.Parse(noteID)
	if err != nil {
		return mcp.NewToolResultError("invalid note_id format"), nil
	}

	if err := h.NoteSvc.Delete(ctx, h.UserID, id); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to delete note: %v", err)), nil
	}

	return mcp.NewToolResultText(fmt.Sprintf("Note %s deleted", noteID)), nil
}
