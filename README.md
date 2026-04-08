# Vero MCP

A local [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server for personal finance. Connect your bank accounts, scan receipts, auto-match them to transactions, and organize everything with notes and labels — all running locally on your machine.

Works with any MCP-compatible AI client.

## What it does

Vero MCP connects to [Plaid](https://plaid.com) to pull your bank transactions and exposes tools for:

- **View transactions** with filtering by date, amount, category, merchant
- **Scan receipts** via image upload with OCR (powered by OpenAI GPT-4o)
- **Auto-match receipts to transactions** using a deterministic scoring pipeline with optional LLM disambiguation
- **Manually match/unmatch** receipts and transactions
- **Add notes** to transactions and receipts
- **Organize with labels** — create labels and tag transactions or receipts
- **View receipt line items** extracted from OCR

All data is stored locally in a SQLite database at `~/.vero/vero.db`. No data leaves your machine except API calls to Plaid (bank data) and optionally OpenAI (receipt OCR).

## Prerequisites

- **Go 1.21+**
- A **Plaid account** with API credentials ([sign up](https://dashboard.plaid.com/signup))
- An **OpenAI API key** (optional — needed for receipt image scanning)

## Quick start

### 1. Install

```bash
git clone https://github.com/Vero-Receipts/vero-mcp.git
cd vero-mcp
go build -o vero-mcp ./cmd/vero-mcp
```

### 2. Setup

Run the interactive setup to configure your API credentials:

```bash
./vero-mcp --setup
```

This prompts for:
- **Plaid Client ID** — from your [Plaid dashboard](https://dashboard.plaid.com/developers/keys)
- **Plaid Secret** — from your Plaid dashboard (input is hidden)
- **Plaid Environment** — `sandbox` for testing with fake data, `production` for real bank accounts
- **OpenAI API Key** — optional, enables receipt image OCR (input is hidden)

Credentials are saved to `~/.vero/.env`.

### 3. Connect to your AI client

Vero MCP uses stdio transport. Add it to any MCP-compatible client by pointing it at the `vero-mcp` binary.

**Claude Code:**
```bash
claude mcp add --transport stdio vero-mcp -- vero-mcp
```

**Other clients:** configure a stdio MCP server with the command `vero-mcp`. Refer to your client's documentation for the exact setup.

## Available tools

| Tool | Description |
|------|-------------|
| `connect_bank_account` | Opens a browser link for Plaid bank connection |
| `get_accounts` | Lists connected bank accounts with balances |
| `disconnect_bank_account` | Removes a bank connection |
| `get_user_context` | Shows user profile and connection status |
| `get_transactions` | Syncs and returns transactions with filters |
| `get_receipts` | Lists receipts with filters |
| `ingest_receipt_image` | Creates an upload session for a receipt image |
| `wait_for_receipt_upload` | Waits for the image upload and returns OCR results |
| `match_receipt` | Manually links a receipt to a transaction |
| `unmatch_receipt` | Removes a receipt-transaction link |
| `confirm_receipt_suggestion` | Confirms an auto-suggested match |
| `reject_receipt_suggestion` | Rejects an auto-suggested match |
| `get_receipt_items` | Returns line items for a receipt |
| `add_note` | Adds a note to a transaction or receipt |
| `get_notes` | Lists notes for a transaction or receipt |
| `delete_note` | Deletes a note |
| `list_labels` | Lists all labels |
| `create_label` | Creates a new label |
| `add_label` | Tags a transaction or receipt with a label |
| `remove_label` | Removes a label from a transaction or receipt |

## How matching works

When a receipt is scanned, Vero runs a deterministic matching pipeline:

1. **Amount scoring** — compares receipt total vs transaction amount (within 20% tolerance, wider for foreign currency)
2. **Date scoring** — compares receipt date vs transaction date (within 5 days)
3. **Merchant scoring** — normalizes names, strips POS prefixes (SQ\*, TST\*, etc.), checks substrings, edit distance, word overlap
4. **LLM disambiguation** (optional) — if OpenAI is configured and the merchant match is ambiguous, asks GPT-4o-mini to confirm

High-confidence matches are auto-linked. Lower-confidence matches are suggested for user confirmation.

## Configuration

All configuration is via environment variables. The `--setup` command saves these to `~/.vero/.env`.

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `PLAID_CLIENT_ID` | Yes | — | Plaid API client ID |
| `PLAID_SECRET` | Yes | — | Plaid API secret |
| `PLAID_ENV` | No | `sandbox` | `sandbox` or `production` |
| `OPENAI_API_KEY` | No | — | Enables receipt OCR and LLM matching |
| `ENCRYPTION_KEY` | No | — | Hex-encoded AES key for encrypting stored Plaid tokens. If empty, tokens are stored in plain text. |
| `VERO_DATA_DIR` | No | `~/.vero` | Directory for database and receipt files |

## Data storage

Everything is stored locally:

```
~/.vero/
  vero.db        # SQLite database (transactions, receipts, matches, notes, labels)
  receipts/      # Uploaded receipt images
  .env           # API credentials
```

## Running tests

```bash
go test ./... -v -count=1 -timeout=180s
```

Repository tests use an in-memory SQLite database — no external dependencies needed.

## Architecture

```
cmd/vero-mcp/          # CLI entrypoint
internal/
  config/              # Environment-based configuration
  repository/          # SQLite database setup and migrations
  server/              # MCP tool handlers
  service/             # Local storage service
  webserver/           # Local web server for Plaid Link and receipt uploads
pkg/
  crypto/              # AES-GCM token encryption (optional)
  domain/              # Shared domain types
  repository/          # Database repositories (SQLite + Postgres via dialect)
  service/             # Business logic (Plaid, receipts, matching, OCR, etc.)
```

The `pkg/` layer is designed to be reusable. It supports both SQLite (for local use) and Postgres (for cloud deployments) via a dialect abstraction.

## License

MIT
