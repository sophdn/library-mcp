package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDB(t *testing.T) *Pool {
	t.Helper()
	pool, err := Open(filepath.Join(t.TempDir(), "pool_test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// TestOpen_AppliesSchema proves Open runs the embedded migrations: the
// library_entries and library_entry_projects tables exist after opening a fresh
// database.
func TestOpen_AppliesSchema(t *testing.T) {
	pool := tempDB(t)
	for _, table := range []string{"library_entries", "library_entry_projects"} {
		var name string
		err := pool.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("table %s not created: %v", table, err)
		}
	}
}

// TestOpen_EnablesWAL checks the WAL pragma took effect.
func TestOpen_EnablesWAL(t *testing.T) {
	pool := tempDB(t)
	var mode string
	if err := pool.DB().QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

// TestOpen_Idempotent proves opening the same file twice keeps the schema
// intact and preserves rows: the migrations are safe to re-apply on every open.
func TestOpen_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	pool1, err := Open(path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := pool1.WithWrite(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO library_entries (dewey) VALUES ('500')`)
		return err
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := pool1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	pool2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = pool2.Close() })
	var count int
	if err := pool2.DB().QueryRow(`SELECT COUNT(*) FROM library_entries`).Scan(&count); err != nil {
		t.Fatalf("count after reopen: %v", err)
	}
	if count != 1 {
		t.Errorf("row count after reopen = %d, want 1", count)
	}
}

// TestOpen_Error checks Open surfaces an error when the file cannot be created:
// the db path sits under a regular file, so its parent is not a directory.
func TestOpen_Error(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := Open(filepath.Join(blocker, "nested.db")); err == nil {
		t.Fatal("expected an error opening under a file, got nil")
	}
}

// TestOpen_MigrationError drives the apply-schema failure path: the file
// already holds a library_entries table without the status column, so when Open
// re-applies the embedded schema the CREATE INDEX on status fails and Open
// reports it rather than returning a half-migrated pool.
func TestOpen_MigrationError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.db")
	raw, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	// A library_entries lacking the columns the schema indexes. CREATE TABLE
	// IF NOT EXISTS then skips it on migrate, so the index build hits the gap.
	if _, err := raw.Exec(`CREATE TABLE library_entries (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("seed malformed table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("raw close: %v", err)
	}

	pool, err := Open(path)
	if err == nil {
		_ = pool.Close()
		t.Fatal("expected an apply-schema error from a malformed pre-existing table, got nil")
	}
	if !strings.Contains(err.Error(), "apply schema") {
		t.Errorf("error should identify the schema step, got %v", err)
	}
}

// TestDSN covers both query-separator branches: a bare path gets `?`, a path
// that already carries a query gets `&`.
func TestDSN(t *testing.T) {
	bare := dsn("library.db")
	if !strings.Contains(bare, "library.db?_txlock=immediate") {
		t.Errorf("bare dsn = %q", bare)
	}
	withQuery := dsn("library.db?mode=ro")
	if !strings.Contains(withQuery, "mode=ro&_txlock=immediate") {
		t.Errorf("query dsn = %q", withQuery)
	}
}

// TestWithWrite_Commits proves a nil return from fn commits the transaction.
func TestWithWrite_Commits(t *testing.T) {
	pool := tempDB(t)
	if err := pool.WithWrite(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO library_entries (dewey) VALUES ('501')`)
		return err
	}); err != nil {
		t.Fatalf("WithWrite: %v", err)
	}
	var count int
	if err := pool.DB().QueryRow(`SELECT COUNT(*) FROM library_entries WHERE dewey='501'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("committed row count = %d, want 1", count)
	}
}

var errRollback = errors.New("intentional rollback")

// TestWithWrite_RollsBackOnError proves a non-nil return from fn rolls the
// transaction back — the row written before the error must not survive — and
// that WithWrite returns that error unchanged.
func TestWithWrite_RollsBackOnError(t *testing.T) {
	pool := tempDB(t)
	err := pool.WithWrite(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO library_entries (dewey) VALUES ('502')`); err != nil {
			return err
		}
		return errRollback
	})
	if !errors.Is(err, errRollback) {
		t.Fatalf("WithWrite error = %v, want errRollback", err)
	}
	var count int
	if err := pool.DB().QueryRow(`SELECT COUNT(*) FROM library_entries WHERE dewey='502'`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("rolled-back row count = %d, want 0", count)
	}
}

// TestWithWrite_BeginError proves WithWrite reports a begin failure rather than
// panicking: a closed pool cannot start a transaction.
func TestWithWrite_BeginError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.db")
	pool, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := pool.WithWrite(context.Background(), func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("expected a begin error on a closed pool, got nil")
	}
}

// TestWithWrite_CommitError drives the commit-failure branch: fn cancels the
// transaction's context after doing valid work, so database/sql tears the
// transaction down and the deferred Commit returns an error, which WithWrite
// wraps and returns.
func TestWithWrite_CommitError(t *testing.T) {
	pool := tempDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := pool.WithWrite(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO library_entries (dewey) VALUES ('600')`); err != nil {
			return err
		}
		cancel() // tear the tx down out from under the pending commit
		return nil
	})
	if err == nil {
		t.Fatal("expected a commit error after the context was cancelled, got nil")
	}
}

// TestClose proves Close returns nil on a healthy pool.
func TestClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "close.db")
	pool, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
