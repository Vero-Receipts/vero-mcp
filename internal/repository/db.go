package repository

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if err := runMigrations(db); err != nil {
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	slog.Info("database initialized", "path", dbPath)
	return db, nil
}

func runMigrations(db *sql.DB) error {
	if _, err := db.Exec("CREATE TABLE IF NOT EXISTS schema_version (version INTEGER PRIMARY KEY)"); err != nil {
		return fmt.Errorf("create schema_version table: %w", err)
	}

	var current int
	_ = db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current)

	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Files are named NNN_description.sql and embed.FS returns them sorted.
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Extract version number from filename prefix (e.g. "001" → 1).
		var version int
		if _, err := fmt.Sscanf(name, "%d_", &version); err != nil {
			slog.Warn("skipping non-migration file", "name", name)
			continue
		}

		if version <= current {
			continue
		}

		data, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			return fmt.Errorf("execute migration %s: %w", name, err)
		}
		if _, err := db.Exec("INSERT INTO schema_version (version) VALUES (?)", version); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		slog.Info("applied migration", "file", name)
	}

	return nil
}

// EnsureUserExists creates the default single user if it does not already exist.
func EnsureUserExists(db *sql.DB, id uuid.UUID) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.Exec(
		`INSERT OR IGNORE INTO users (id, name, is_bank_connected, created_at, updated_at)
		 VALUES (?, '', 0, ?, ?)`,
		id.String(), now, now,
	)
	return err
}
