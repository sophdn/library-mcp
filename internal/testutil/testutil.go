// Package testutil provides shared test helpers for the library MCP server.
package testutil

import (
	"path/filepath"
	"testing"

	"github.com/sophdn/library-mcp/internal/db"
)

// NewTestDB opens a temporary SQLite database with the library schema applied.
// The database is closed and its temp directory removed via t.Cleanup, so each
// test runs against a fresh, isolated store.
func NewTestDB(t *testing.T) *db.Pool {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library_test.db")
	pool, err := db.Open(path)
	if err != nil {
		t.Fatalf("testutil.NewTestDB: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}
