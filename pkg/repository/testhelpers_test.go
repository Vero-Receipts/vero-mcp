package repository

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	_ "modernc.org/sqlite"
)

// migrationsDir returns the absolute path to the SQLite migrations directory.
func migrationsDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "internal", "repository", "migrations")
}

// setupTestDB creates a fresh in-memory SQLite database and runs the migration.
// The returned *sql.DB is closed automatically when the test completes.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	entries, err := os.ReadDir(migrationsDir())
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sql" {
			continue
		}
		names = append(names, e.Name())
	}
	// Filenames are NNN_* so lexicographic order == migration order.
	sort.Strings(names)
	for _, name := range names {
		sqlBytes, err := os.ReadFile(filepath.Join(migrationsDir(), name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := db.Exec(string(sqlBytes)); err != nil {
			t.Fatalf("execute %s: %v", name, err)
		}
	}

	return db
}
