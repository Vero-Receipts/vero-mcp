package server

import (
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// RegisterTools wires all MCP tools into the given server instance.
func RegisterTools(s *mcpserver.MCPServer, h *ToolHandlers) {
	s.AddTool(
		mcp.NewTool("connect_bank_account",
			mcp.WithDescription("Creates a Plaid Link session and returns a URL for the user to connect their bank account in a browser. After the user completes the flow, the account is automatically linked."),
		),
		h.ConnectBankAccountHandler,
	)

	s.AddTool(
		mcp.NewTool("get_accounts",
			mcp.WithDescription("Returns the user's connected bank accounts with balances and account details."),
		),
		h.GetAccountsHandler,
	)

	s.AddTool(
		mcp.NewTool("disconnect_bank_account",
			mcp.WithDescription("Disconnects a bank account. Note: this removes the entire institution connection, so all accounts under the same institution will be disconnected. Only call this once per institution."),
			mcp.WithString("account_id",
				mcp.Required(),
				mcp.Description("The account ID to disconnect. Use get_accounts to find it."),
			),
		),
		h.DisconnectAccountHandler,
	)

	s.AddTool(
		mcp.NewTool("get_user_context",
			mcp.WithDescription("Returns a consolidated snapshot of the user's identity and current integration state, including bank connection status and profile info. Use this to understand what the user has set up before suggesting actions."),
		),
		h.GetUserContextHandler,
	)

	s.AddTool(
		mcp.NewTool("get_transactions",
			mcp.WithDescription("Syncs latest transactions from Plaid and returns them. Supports filtering by search text, date range, amount range, category, matched/unmatched status, and pending status."),
			mcp.WithString("search",
				mcp.Description("Search transactions by name or merchant name"),
			),
			mcp.WithString("date_from",
				mcp.Description("Start date filter (YYYY-MM-DD)"),
			),
			mcp.WithString("date_to",
				mcp.Description("End date filter (YYYY-MM-DD)"),
			),
			mcp.WithString("amount_min",
				mcp.Description("Minimum transaction amount (absolute value)"),
			),
			mcp.WithString("amount_max",
				mcp.Description("Maximum transaction amount (absolute value)"),
			),
			mcp.WithString("category",
				mcp.Description("Filter by transaction category"),
				mcp.Enum(
					"BANK_FEES",
					"ENTERTAINMENT",
					"FOOD_AND_DRINK",
					"GENERAL_MERCHANDISE",
					"GENERAL_SERVICES",
					"GOVERNMENT_AND_NON_PROFIT",
					"HOME_IMPROVEMENT",
					"INCOME",
					"LOAN_DISBURSEMENTS",
					"LOAN_PAYMENTS",
					"MEDICAL",
					"OTHER",
					"PERSONAL_CARE",
					"RENT_AND_UTILITIES",
					"TRANSFER_IN",
					"TRANSFER_OUT",
					"TRANSPORTATION",
					"TRAVEL",
				),
			),
			mcp.WithString("matched",
				mcp.Description("Filter by receipt match status: 'matched', 'unmatched', or omit for all"),
				mcp.Enum("matched", "unmatched"),
			),
			mcp.WithString("pending",
				mcp.Description("Filter by pending status: 'true', 'false', or omit for all"),
				mcp.Enum("true", "false"),
			),
			mcp.WithString("sort_by",
				mcp.Description("Sort field: 'date', 'amount', 'merchant', or 'name' (default: 'date')"),
				mcp.Enum("date", "amount", "merchant", "name"),
			),
			mcp.WithString("sort_order",
				mcp.Description("Sort direction: 'asc' or 'desc' (default: 'desc')"),
				mcp.Enum("asc", "desc"),
			),
		),
		h.GetTransactionsHandler,
	)

	s.AddTool(
		mcp.NewTool("get_receipts",
			mcp.WithDescription("Returns the user's receipts with optional filters. Includes match status, OCR-extracted data, and linked transaction info."),
			mcp.WithString("status",
				mcp.Description("Filter by receipt status"),
				mcp.Enum("matched", "unmatched", "suggested"),
			),
			mcp.WithString("source",
				mcp.Description("Filter by receipt source"),
				mcp.Enum("upload"),
			),
			mcp.WithString("match_method",
				mcp.Description("Filter by how the receipt was matched"),
				mcp.Enum("auto", "manual", "suggested", "confirmed"),
			),
			mcp.WithString("search",
				mcp.Description("Search by merchant name or line items"),
			),
			mcp.WithString("date_from",
				mcp.Description("Start date filter (YYYY-MM-DD)"),
			),
			mcp.WithString("date_to",
				mcp.Description("End date filter (YYYY-MM-DD)"),
			),
			mcp.WithString("amount_min",
				mcp.Description("Minimum receipt amount"),
			),
			mcp.WithString("amount_max",
				mcp.Description("Maximum receipt amount"),
			),
			mcp.WithString("sort_by",
				mcp.Description("Sort field: 'date', 'amount', or 'merchant' (default: created_at)"),
				mcp.Enum("date", "amount", "merchant"),
			),
			mcp.WithString("sort_order",
				mcp.Description("Sort direction: 'asc' or 'desc' (default: 'desc')"),
				mcp.Enum("asc", "desc"),
			),
		),
		h.GetReceiptsHandler,
	)

	s.AddTool(
		mcp.NewTool("ingest_receipt_image",
			mcp.WithDescription("Creates a receipt upload session and returns a URL for the user to upload a receipt in their browser (image formats jpg, png, gif, webp, heic, or PDF). Before showing the URL, explain to the user that you cannot process files directly and they need to upload it through the provided link. After showing the URL, immediately call wait_for_receipt_upload with the returned session_id to get the OCR and matching results."),
		),
		h.IngestReceiptHandler,
	)

	s.AddTool(
		mcp.NewTool("wait_for_receipt_upload",
			mcp.WithDescription("Waits for a receipt image upload to complete. Call this after ingest_receipt_image once the user has been given the upload URL. Blocks until the user uploads the image or the session times out (5 minutes)."),
			mcp.WithString("session_id",
				mcp.Required(),
				mcp.Description("The session ID returned by ingest_receipt_image"),
			),
		),
		h.WaitForReceiptUploadHandler,
	)

	s.AddTool(
		mcp.NewTool("batch_receipt_ingestion",
			mcp.WithDescription("Scans a directory for receipt files (jpg, png, gif, webp, heic, pdf) and processes each one through OCR and auto-matching. Returns a summary of succeeded and failed receipts."),
			mcp.WithString("directory_path",
				mcp.Required(),
				mcp.Description("Absolute path to the directory containing receipt images to process"),
			),
		),
		h.BatchReceiptIngestionHandler,
	)

	s.AddTool(
		mcp.NewTool("match_receipt",
			mcp.WithDescription("Manually matches a receipt to a specific transaction. Use this when automatic matching didn't find the right transaction or when the user wants to link a specific receipt to a specific transaction."),
			mcp.WithString("receipt_id",
				mcp.Required(),
				mcp.Description("The UUID of the receipt to match"),
			),
			mcp.WithString("transaction_id",
				mcp.Required(),
				mcp.Description("The Plaid transaction ID to match the receipt to"),
			),
		),
		h.MatchReceiptHandler,
	)

	s.AddTool(
		mcp.NewTool("unmatch_receipt",
			mcp.WithDescription("Removes an existing association between a receipt and a transaction."),
			mcp.WithString("receipt_id",
				mcp.Required(),
				mcp.Description("The UUID of the receipt to unmatch"),
			),
		),
		h.UnmatchReceiptHandler,
	)

	s.AddTool(
		mcp.NewTool("confirm_receipt_suggestion",
			mcp.WithDescription("Confirms an auto-suggested receipt-to-transaction match. Use when the system suggested a match and the user agrees it's correct."),
			mcp.WithString("receipt_id",
				mcp.Required(),
				mcp.Description("The UUID of the receipt whose suggestion to confirm"),
			),
		),
		h.ConfirmSuggestionHandler,
	)

	s.AddTool(
		mcp.NewTool("reject_receipt_suggestion",
			mcp.WithDescription("Rejects an auto-suggested receipt-to-transaction match. Use when the system suggested a match but the user says it's wrong."),
			mcp.WithString("receipt_id",
				mcp.Required(),
				mcp.Description("The UUID of the receipt whose suggestion to reject"),
			),
		),
		h.RejectSuggestionHandler,
	)

	s.AddTool(
		mcp.NewTool("add_note",
			mcp.WithDescription("Adds a note to a transaction or receipt."),
			mcp.WithString("entity_type",
				mcp.Required(),
				mcp.Description("The type of entity to attach the note to"),
				mcp.Enum("transaction", "receipt"),
			),
			mcp.WithString("entity_id",
				mcp.Required(),
				mcp.Description("The ID of the entity (transaction ID or receipt UUID)"),
			),
			mcp.WithString("content",
				mcp.Required(),
				mcp.Description("The note content"),
			),
		),
		h.AddNoteHandler,
	)

	s.AddTool(
		mcp.NewTool("get_notes",
			mcp.WithDescription("Gets all notes for a transaction or receipt."),
			mcp.WithString("entity_type",
				mcp.Required(),
				mcp.Description("The type of entity"),
				mcp.Enum("transaction", "receipt"),
			),
			mcp.WithString("entity_id",
				mcp.Required(),
				mcp.Description("The ID of the entity (transaction ID or receipt UUID)"),
			),
		),
		h.GetNotesHandler,
	)

	s.AddTool(
		mcp.NewTool("delete_note",
			mcp.WithDescription("Deletes a note by its ID."),
			mcp.WithString("note_id",
				mcp.Required(),
				mcp.Description("The UUID of the note to delete"),
			),
		),
		h.DeleteNoteHandler,
	)

	s.AddTool(
		mcp.NewTool("list_labels",
			mcp.WithDescription("Lists all labels created by the user."),
		),
		h.ListLabelsHandler,
	)

	s.AddTool(
		mcp.NewTool("create_label",
			mcp.WithDescription("Creates a new label for organizing transactions and receipts."),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("The label name"),
			),
			mcp.WithString("color",
				mcp.Description("The label color as a hex code (e.g. '#FF5733'). Defaults to gray."),
			),
		),
		h.CreateLabelHandler,
	)

	s.AddTool(
		mcp.NewTool("add_label",
			mcp.WithDescription("Assigns a label to a transaction or receipt."),
			mcp.WithString("label_id",
				mcp.Required(),
				mcp.Description("The UUID of the label to assign"),
			),
			mcp.WithString("entity_type",
				mcp.Required(),
				mcp.Description("The type of entity"),
				mcp.Enum("transaction", "receipt"),
			),
			mcp.WithString("entity_id",
				mcp.Required(),
				mcp.Description("The ID of the entity (transaction ID or receipt UUID)"),
			),
		),
		h.AddLabelHandler,
	)

	s.AddTool(
		mcp.NewTool("remove_label",
			mcp.WithDescription("Removes a label from a transaction or receipt."),
			mcp.WithString("label_id",
				mcp.Required(),
				mcp.Description("The UUID of the label to remove"),
			),
			mcp.WithString("entity_type",
				mcp.Required(),
				mcp.Description("The type of entity"),
				mcp.Enum("transaction", "receipt"),
			),
			mcp.WithString("entity_id",
				mcp.Required(),
				mcp.Description("The ID of the entity (transaction ID or receipt UUID)"),
			),
		),
		h.RemoveLabelHandler,
	)

	s.AddTool(
		mcp.NewTool("get_receipt_items",
			mcp.WithDescription("Returns the line items for a specific receipt, including description, quantity, unit price, and total price for each item."),
			mcp.WithString("receipt_id",
				mcp.Required(),
				mcp.Description("The UUID of the receipt"),
			),
		),
		h.GetReceiptItemsHandler,
	)
}
