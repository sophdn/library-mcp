package library

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/sophdn/library-mcp/internal/db"
)

// closedPool opens a real database, seeds one valid card, then closes the pool
// so every subsequent query returns a driver error. This drives the defensive
// error-wrapping branches that the happy path never reaches.
func closedPool(t *testing.T) *db.Pool {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := Add(context.Background(), pool, "proj", sampleEntry("500.1")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := pool.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return pool
}

// TestQueryErrorPaths asserts each read/list/find/export entry point surfaces a
// non-nil error when the database is unavailable, exercising the error returns
// after each query.
func TestQueryErrorPaths(t *testing.T) {
	ctx := context.Background()
	pool := closedPool(t)

	checks := []struct {
		name string
		run  func() error
	}{
		{"Add", func() error { return Add(ctx, pool, "p", sampleEntry("500.2")) }},
		{"Get", func() error { _, err := Get(ctx, pool, "500.1"); return err }},
		{"ListActive", func() error { _, err := ListActive(ctx, pool, "p"); return err }},
		{"ListAll", func() error { _, err := ListAll(ctx, pool); return err }},
		{"ListAllManifest", func() error { _, err := ListAllManifest(ctx, pool); return err }},
		{"ListSections", func() error { _, err := ListSections(ctx, pool, "p"); return err }},
		{"ListDeweyByPrefix", func() error { _, err := ListDeweyByPrefix(ctx, pool, "p", "5"); return err }},
		{"FindKeyword", func() error { _, err := FindKeyword(ctx, pool, "p", "x"); return err }},
		{"FindSemantic", func() error { _, err := FindSemantic(ctx, pool, "p", "S"); return err }},
		{"FindManifest", func() error { _, err := FindManifest(ctx, pool, "p", "S"); return err }},
		{"CrossReference", func() error { _, err := CrossReference(ctx, pool, "500.1", CrossRefModeSection); return err }},
		{"Reproject", func() error { _, err := Reproject(ctx, pool, "500.1", ReprojectAdd, []string{"x"}); return err }},
		{"Update", func() error { _, err := Update(ctx, pool, "500.1", EntryUpdate{}); return err }},
		{"Retire", func() error { return Retire(ctx, pool, "500.1", "r") }},
		{"Export", func() error { _, err := Export(ctx, pool, t.TempDir()); return err }},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if err := c.run(); err == nil {
				t.Errorf("%s should error against a closed pool, got nil", c.name)
			}
		})
	}
}

// dropProjectsTable removes library_entry_projects so any write that touches it
// fails inside its transaction. This injects a schema fault to drive the
// in-transaction error-return branches that a healthy schema never hits.
func dropProjectsTable(t *testing.T, pool *db.Pool) {
	t.Helper()
	err := pool.WithWrite(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`DROP TABLE library_entry_projects`)
		return err
	})
	if err != nil {
		t.Fatalf("drop projects table: %v", err)
	}
}

// TestAdd_ProjectInsertError drives Add's tagging-write error path: with the
// projects table gone, tagging a card fails and the whole insert rolls back.
func TestAdd_ProjectInsertError(t *testing.T) {
	pool, err := db.Open(filepath.Join(t.TempDir(), "drop.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	dropProjectsTable(t, pool)

	if err := Add(context.Background(), pool, "proj", sampleEntry("500.5")); err == nil {
		t.Fatal("Add with a project should fail when the projects table is gone")
	}
	// The transaction rolled back, so the card must not have landed either.
	if _, err := Get(context.Background(), pool, "500.5"); err == nil {
		t.Error("failed tagging write should roll back the card insert")
	}
}

// TestReproject_WriteError drives Reproject's in-transaction error path: the
// lookup succeeds against library_entries, but the tag write fails because the
// projects table is gone.
func TestReproject_WriteError(t *testing.T) {
	pool, err := db.Open(filepath.Join(t.TempDir(), "drop2.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := Add(context.Background(), pool, "", sampleEntry("500.6")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	dropProjectsTable(t, pool)

	for _, mode := range []ReprojectMode{ReprojectAdd, ReprojectRemove, ReprojectSet} {
		if _, err := Reproject(context.Background(), pool, "500.6", mode, []string{"x"}); err == nil {
			t.Errorf("Reproject mode %d should fail when the projects table is gone", mode)
		}
	}
}

// TestScanEntry_BadPointersJSON drives scanEntry's index_pointers decode-error
// branch: a row whose stored pointers are not valid JSON makes the read fail.
func TestScanEntry_BadPointersJSON(t *testing.T) {
	pool, err := db.Open(filepath.Join(t.TempDir(), "badptr.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	err = pool.WithWrite(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO library_entries (dewey, status, tags, index_pointers)
			 VALUES ('500.7', 'active', '[]', '{not json}')`)
		return err
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := Get(context.Background(), pool, "500.7"); err == nil {
		t.Fatal("Get should fail on a row with malformed index_pointers JSON")
	}
}

// TestScanEntries_InvalidStoredDewey drives scanEntry's validation branch and
// scanEntries' scan-error return: a row with a malformed dewey makes a list read
// fail rather than yield a broken entry.
func TestScanEntries_InvalidStoredDewey(t *testing.T) {
	pool, err := db.Open(filepath.Join(t.TempDir(), "bad.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	err = pool.WithWrite(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO library_entries (dewey, status, tags, index_pointers)
			 VALUES ('12', 'active', '[]', '[]')`)
		return err
	})
	if err != nil {
		t.Fatalf("insert bad row: %v", err)
	}
	if _, err := ListAll(context.Background(), pool); err == nil {
		t.Fatal("ListAll should reject a row with an invalid stored dewey")
	}
}
