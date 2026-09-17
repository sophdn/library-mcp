// Package db provides a small SQLite pool with serialized write access for the
// library MCP server. It uses the pure-Go modernc.org/sqlite driver, so the
// server builds and ships with CGO disabled.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// busyTimeoutMS is how long a connection waits for a competing writer to
// release the write lock before giving up with SQLITE_BUSY.
const busyTimeoutMS = 5000

// Scanner is the minimum interface satisfied by both *sql.Row and *sql.Rows.
// It lets a scan helper work over single-row and multi-row queries alike.
type Scanner interface {
	Scan(dest ...any) error
}

// Pool wraps a *sql.DB with serialized write access. Reads go through DB() in
// autocommit mode; writes go through WithWrite, which a mutex serializes.
type Pool struct {
	db *sql.DB
	mu sync.Mutex
}

// Open opens, or creates, the SQLite database at path, enables WAL mode,
// applies the embedded schema, and returns a Pool. Applying the schema on
// every open keeps a fresh database and an existing one on the same shape;
// the schema statements are idempotent.
func Open(path string) (*Pool, error) {
	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	pool := &Pool{db: sqlDB}
	if err := pool.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return pool, nil
}

// dsn appends the connection parameters that keep writes safe under
// contention: busy_timeout makes a blocked writer wait rather than fail, and
// txlock=immediate takes the write lock at BEGIN so a read-then-write step
// cannot race another writer.
func dsn(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%s_txlock=immediate&_pragma=busy_timeout(%d)", path, sep, busyTimeoutMS)
}

// migrate applies every embedded .sql file in name order.
func (p *Pool) migrate() error {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		stmt, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := p.db.Exec(string(stmt)); err != nil {
			return fmt.Errorf("exec %s: %w", name, err)
		}
	}
	return nil
}

// DB returns the underlying *sql.DB for read-only queries. Callers must not
// use it for writes; use WithWrite instead.
func (p *Pool) DB() *sql.DB { return p.db }

// WithWrite acquires the write mutex and runs fn inside a transaction. The
// transaction commits on a nil return from fn and rolls back otherwise.
func (p *Pool) WithWrite(ctx context.Context, fn func(*sql.Tx) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Close closes the underlying database.
func (p *Pool) Close() error { return p.db.Close() }
