package config

import (
	"os"
	"path/filepath"
)

type Config struct {
	PlaidClientID string
	PlaidSecret   string
	PlaidEnv      string
	OpenAIAPIKey  string
	EncryptionKey string // optional; empty = tokens stored in plain text
	DataDir       string
}

// Load reads configuration from environment variables.
// It does NOT fail on missing values — the caller is responsible for
// prompting the user interactively if required fields are empty.
func Load() (*Config, error) {
	dataDir := getEnv("VERO_DATA_DIR", "")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(home, ".vero")
	}

	return &Config{
		PlaidClientID: getEnv("PLAID_CLIENT_ID", ""),
		PlaidSecret:   getEnv("PLAID_SECRET", ""),
		PlaidEnv:      getEnv("PLAID_ENV", "sandbox"),
		OpenAIAPIKey:  getEnv("OPENAI_API_KEY", ""),
		EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
		DataDir:       dataDir,
	}, nil
}

// DBPath returns the path to the SQLite database file.
func (c *Config) DBPath() string {
	return filepath.Join(c.DataDir, "vero.db")
}

// ReceiptsDir returns the path to the receipts storage directory.
func (c *Config) ReceiptsDir() string {
	return filepath.Join(c.DataDir, "receipts")
}

// EnvFilePath returns the path to the .env file in the data directory.
func (c *Config) EnvFilePath() string {
	return filepath.Join(c.DataDir, ".env")
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
