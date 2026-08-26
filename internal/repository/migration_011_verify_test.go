package repository

import (
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

// The 011 migration is the only thing that puts these columns on an existing
// database, and nothing else in the suite runs the migration chain — so without
// this the SQL is never executed until a real deployment.
func TestMigration011AddsColumns(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open/migrate: %v", err)
	}
	defer db.Close()

	for _, tc := range []struct{ table, column string }{
		{"match_audit_log", "llm_confidence"},
		{"receipts", "match_attempted_at"},
	} {
		rows, err := db.Query("SELECT name FROM pragma_table_info(?)", tc.table)
		if err != nil {
			t.Fatalf("pragma %s: %v", tc.table, err)
		}
		found := false
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			if name == tc.column {
				found = true
			}
		}
		rows.Close()
		if !found {
			t.Errorf("%s.%s missing after migrations", tc.table, tc.column)
		}
	}

	// The stamp has to accept a real write and read back, not merely exist.
	id := uuid.NewString()
	if _, err := db.Exec("INSERT INTO receipts (id, match_attempted_at) VALUES (?, ?)", id, "2026-08-26T12:00:00Z"); err != nil {
		t.Fatalf("write stamp: %v", err)
	}
	var got *string
	if err := db.QueryRow("SELECT match_attempted_at FROM receipts WHERE id = ?", id).Scan(&got); err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	if got == nil || *got != "2026-08-26T12:00:00Z" {
		t.Errorf("match_attempted_at = %v, want the written value", got)
	}
}
