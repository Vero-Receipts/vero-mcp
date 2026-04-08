package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"syscall"

	"golang.org/x/term"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/mark3labs/mcp-go/server"

	"github.com/Vero-Receipts/vero-mcp/internal/config"
	localrepo "github.com/Vero-Receipts/vero-mcp/internal/repository"
	mcpserver "github.com/Vero-Receipts/vero-mcp/internal/server"
	internalservice "github.com/Vero-Receipts/vero-mcp/internal/service"
	"github.com/Vero-Receipts/vero-mcp/internal/webserver"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	pkgservice "github.com/Vero-Receipts/vero-mcp/pkg/service"
)

// defaultUserID is the fixed user ID for the single-user local MCP server.
var defaultUserID = uuid.MustParse("00000000-0000-0000-0000-000000000001")

const logo = `
  ░██    ░██
  ░██    ░██
  ░██    ░██  ░███████  ░██░████  ░███████
  ░██    ░██ ░██    ░██ ░███     ░██    ░██
   ░██  ░██  ░█████████ ░██      ░██    ░██
    ░██░██   ░██        ░██      ░██    ░██
     ░███     ░███████  ░██       ░███████
`

func main() {
	setupOnly := len(os.Args) > 1 && (os.Args[1] == "--setup" || os.Args[1] == "setup")

	// Display logo on stderr (stdout is reserved for MCP stdio transport).
	fmt.Fprint(os.Stderr, logo)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  \033[1mVero MCP\033[0m \033[2mv0.1.0 — Local Receipt Intelligence\033[0m\n")
	fmt.Fprintf(os.Stderr, "  \033[2m─────────────────────────────────────────\033[0m\n\n")

	// Load .env from data dir first, then from current dir.
	godotenv.Load() // current dir .env
	cfg, err := config.Load()
	if err != nil {
		fatal("config error: %v", err)
	}

	// Also try loading .env from the data dir.
	godotenv.Load(cfg.EnvFilePath())
	// Re-load config to pick up values from data dir .env.
	cfg, _ = config.Load()

	// --setup: force interactive prompt even if values are already set.
	if setupOnly {
		forcePromptConfig(cfg)
		os.MkdirAll(cfg.DataDir, 0755)
		if err := saveEnvFile(cfg); err != nil {
			fatal("could not save .env: %v", err)
		}
		fmt.Fprintf(os.Stderr, "  \033[32m✓\033[0m Config saved to \033[1m%s\033[0m\n", cfg.EnvFilePath())
		return
	}

	// Prompt for any missing required values interactively.
	changed := promptMissingConfig(cfg)

	// If we collected new values, save them to the data dir .env.
	if changed {
		os.MkdirAll(cfg.DataDir, 0755)
		if err := saveEnvFile(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "  \033[33mwarning:\033[0m could not save .env: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "  \033[32m✓\033[0m Config saved to \033[1m%s\033[0m\n\n", cfg.EnvFilePath())
		}
	}

	// Final validation.
	if cfg.PlaidClientID == "" || cfg.PlaidSecret == "" {
		fatal("PLAID_CLIENT_ID and PLAID_SECRET are required")
	}

	// Configure logging to stderr.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Ensure data directories exist.
	os.MkdirAll(cfg.DataDir, 0755)
	os.MkdirAll(cfg.ReceiptsDir(), 0755)

	// Open SQLite database.
	db, err := localrepo.Open(cfg.DBPath())
	if err != nil {
		fatal("database error: %v", err)
	}
	defer db.Close()

	// Repositories.
	userRepo := repository.NewUserRepo(db, repository.DialectSQLite)
	plaidItemRepo := repository.NewPlaidItemRepo(db, repository.DialectSQLite)
	receiptRepo := repository.NewReceiptRepo(db, repository.DialectSQLite)
	receiptMatchRepo := repository.NewReceiptMatchRepo(db, repository.DialectSQLite)
	txCacheRepo := repository.NewTransactionCacheRepo(db, repository.DialectSQLite)
	merchantAliasRepo := repository.NewMerchantAliasRepo(db, repository.DialectSQLite)
	matchAuditRepo := repository.NewMatchAuditRepo(db, repository.DialectSQLite)
	receiptItemRepo := repository.NewReceiptItemRepo(db, repository.DialectSQLite)
	noteRepo := repository.NewNoteRepo(db, repository.DialectSQLite)
	labelRepo := repository.NewLabelRepo(db, repository.DialectSQLite)
	labelAssignRepo := repository.NewLabelAssignmentRepo(db, repository.DialectSQLite)
	catCacheRepo := repository.NewCategoryCorrectionCacheRepo(db, repository.DialectSQLite)

	// Ensure default user exists.
	if err := localrepo.EnsureUserExists(db, defaultUserID); err != nil {
		fatal("user init error: %v", err)
	}

	// Services.
	storageSvc := internalservice.NewStorageService(cfg.ReceiptsDir())
	openAISvc := pkgservice.NewOpenAIService(cfg.OpenAIAPIKey)
	currencySvc := pkgservice.NewCurrencyService()
	scoringSvc := pkgservice.NewScoringService(merchantAliasRepo)
	matchingSvc := pkgservice.NewMatchingService(txCacheRepo, merchantAliasRepo, matchAuditRepo, scoringSvc, openAISvc)
	categorySvc := pkgservice.NewCategoryService(catCacheRepo, txCacheRepo, openAISvc)

	receiptSvc := pkgservice.NewReceiptService(
		receiptRepo, receiptMatchRepo, txCacheRepo,
		matchingSvc, openAISvc, currencySvc, categorySvc,
		nil, // thumbnailSvc — not needed locally
		storageSvc,
		"", // baseImageURL
	)
	noteSvc := pkgservice.NewNoteService(noteRepo)
	labelSvc := pkgservice.NewLabelService(labelRepo, labelAssignRepo)
	receiptItemSvc := pkgservice.NewReceiptItemService(receiptItemRepo, receiptRepo)

	plaidSvc := pkgservice.NewPlaidService(
		cfg.PlaidClientID, cfg.PlaidSecret, cfg.PlaidEnv,
		"", // redirectURI not needed for local Plaid Link
		cfg.EncryptionKey,
		plaidItemRepo, userRepo, txCacheRepo, receiptSvc,
	)

	// Web server (started lazily on first use).
	ws := webserver.NewLocalWebServer(plaidSvc, receiptSvc, storageSvc, defaultUserID)

	// MCP tool handlers.
	handlers := &mcpserver.ToolHandlers{
		UserID:         defaultUserID,
		PlaidSvc:       plaidSvc,
		ReceiptSvc:     receiptSvc,
		NoteSvc:        noteSvc,
		LabelSvc:       labelSvc,
		ReceiptItemSvc: receiptItemSvc,
		UserRepo:       userRepo,
		WebServer:      ws,
	}

	// Print status.
	fmt.Fprintf(os.Stderr, "  \033[32m✓\033[0m Database    \033[2m%s\033[0m\n", cfg.DBPath())
	fmt.Fprintf(os.Stderr, "  \033[32m✓\033[0m Plaid       \033[2m%s\033[0m\n", cfg.PlaidEnv)
	if cfg.OpenAIAPIKey != "" {
		fmt.Fprintf(os.Stderr, "  \033[32m✓\033[0m OpenAI      \033[2mconfigured\033[0m\n")
	} else {
		fmt.Fprintf(os.Stderr, "  \033[33m-\033[0m OpenAI      \033[2mnot configured (receipt OCR disabled)\033[0m\n")
	}
	fmt.Fprintf(os.Stderr, "  \033[32m✓\033[0m Storage     \033[2m%s\033[0m\n", cfg.ReceiptsDir())
	fmt.Fprintf(os.Stderr, "\n  \033[2mReady — listening on stdio\033[0m\n\n")

	// Create and run the MCP server over stdio.
	s := mcpserver.NewMCPServer(handlers)
	stdio := server.NewStdioServer(s)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Fprintf(os.Stderr, "\n  \033[2mShutting down...\033[0m\n")
		ws.Stop()
		cancel()
	}()

	if err := stdio.Listen(ctx, os.Stdin, os.Stdout); err != nil {
		slog.Error("mcp server error", "error", err)
		os.Exit(1)
	}

	ws.Stop()
}

// isTerminal returns true if the given file is a terminal (not a pipe).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// forcePromptConfig prompts for all config values, showing current values as defaults.
// Used by --setup to let users reconfigure.
func forcePromptConfig(cfg *config.Config) {
	if !isTerminal(os.Stdin) {
		fatal("--setup requires an interactive terminal")
	}

	reader := bufio.NewReader(os.Stdin)

	type field struct {
		name   string
		envVar string
		value  *string
		secret bool
	}

	fields := []field{
		{"Plaid Client ID", "PLAID_CLIENT_ID", &cfg.PlaidClientID, false},
		{"Plaid Secret", "PLAID_SECRET", &cfg.PlaidSecret, true},
		{"Plaid Environment (sandbox or production)", "PLAID_ENV", &cfg.PlaidEnv, false},
		{"OpenAI API Key (optional, for receipt OCR)", "OPENAI_API_KEY", &cfg.OpenAIAPIKey, true},
	}

	fmt.Fprintf(os.Stderr, "  \033[1mSetup\033[0m — enter your API credentials (press Enter to keep current value):\n\n")

	for _, f := range fields {
		current := *f.value
		hint := ""
		if current != "" {
			if f.secret && len(current) > 4 {
				hint = fmt.Sprintf(" \033[2m[%s****]\033[0m", current[:4])
			} else if current != "" {
				hint = fmt.Sprintf(" \033[2m[%s]\033[0m", current)
			}
		}

		fmt.Fprintf(os.Stderr, "  %s%s: ", f.name, hint)
		var input string
		if f.secret {
			raw, _ := term.ReadPassword(int(syscall.Stdin))
			input = strings.TrimSpace(string(raw))
			fmt.Fprintln(os.Stderr)
		} else {
			input, _ = reader.ReadString('\n')
			input = strings.TrimSpace(input)
		}

		if input != "" {
			*f.value = input
			os.Setenv(f.envVar, input)
		}
	}

	fmt.Fprintln(os.Stderr)
}

// promptMissingConfig interactively asks for missing required config values.
// Returns true if any values were collected.
// Skips prompting if stdin is not a terminal (e.g., piped by an MCP client).
func promptMissingConfig(cfg *config.Config) bool {
	if !isTerminal(os.Stdin) {
		// Non-interactive — can't prompt. Check if required values are missing.
		if cfg.PlaidClientID == "" || cfg.PlaidSecret == "" {
			fatal("PLAID_CLIENT_ID and PLAID_SECRET are required. Set them in %s or as environment variables.", cfg.EnvFilePath())
		}
		return false
	}

	reader := bufio.NewReader(os.Stdin)
	changed := false

	type field struct {
		name     string
		envVar   string
		value    *string
		required bool
		secret   bool
	}

	fields := []field{
		{"Plaid Client ID", "PLAID_CLIENT_ID", &cfg.PlaidClientID, true, false},
		{"Plaid Secret", "PLAID_SECRET", &cfg.PlaidSecret, true, true},
		{"Plaid Environment", "PLAID_ENV", &cfg.PlaidEnv, false, false},
		{"OpenAI API Key", "OPENAI_API_KEY", &cfg.OpenAIAPIKey, false, true},
	}

	needsPrompt := false
	for _, f := range fields {
		if f.required && *f.value == "" {
			needsPrompt = true
			break
		}
	}

	if !needsPrompt {
		return false
	}

	fmt.Fprintf(os.Stderr, "  \033[1mFirst-time setup\033[0m — enter your API credentials:\n")
	fmt.Fprintf(os.Stderr, "  \033[2m(values will be saved to %s)\033[0m\n\n", cfg.EnvFilePath())

	for _, f := range fields {
		if *f.value != "" {
			continue
		}

		label := f.name
		if !f.required {
			label += " (optional, press Enter to skip)"
		}

		fmt.Fprintf(os.Stderr, "  %s: ", label)
		var input string
		if f.secret {
			raw, _ := term.ReadPassword(int(syscall.Stdin))
			input = strings.TrimSpace(string(raw))
			fmt.Fprintln(os.Stderr)
		} else {
			input, _ = reader.ReadString('\n')
			input = strings.TrimSpace(input)
		}

		if input != "" {
			*f.value = input
			os.Setenv(f.envVar, input)
			changed = true
		}
	}

	if changed {
		fmt.Fprintln(os.Stderr)
	}

	return changed
}

// saveEnvFile writes the current config to a .env file in the data directory.
func saveEnvFile(cfg *config.Config) error {
	var lines []string

	appendIfSet := func(key, value string) {
		if value != "" {
			lines = append(lines, fmt.Sprintf("%s=%s", key, value))
		}
	}

	appendIfSet("PLAID_CLIENT_ID", cfg.PlaidClientID)
	appendIfSet("PLAID_SECRET", cfg.PlaidSecret)
	appendIfSet("PLAID_ENV", cfg.PlaidEnv)
	appendIfSet("OPENAI_API_KEY", cfg.OpenAIAPIKey)
	appendIfSet("ENCRYPTION_KEY", cfg.EncryptionKey)

	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(cfg.EnvFilePath(), []byte(content), 0600)
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "  \033[31merror:\033[0m "+format+"\n", args...)
	os.Exit(1)
}
