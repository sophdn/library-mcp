package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophdn/library-mcp/internal/testutil"
)

// TestExport_MkdirError checks Export reports a failure when the target dir
// cannot be created because a path component is a regular file.
func TestExport_MkdirError(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	_ = Add(ctx, pool, "", sampleEntry("500.1"))

	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := Export(ctx, pool, filepath.Join(blocker, "sub")); err == nil {
		t.Fatal("expected a mkdir error, got nil")
	}
}

// TestImport_DecodeError checks Import reports a failure on a malformed TOML
// file rather than silently skipping it.
func TestImport_DecodeError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.toml"), []byte("this is = not [valid toml"), 0o600); err != nil {
		t.Fatalf("write bad toml: %v", err)
	}
	pool := testutil.NewTestDB(t)
	if _, err := Import(context.Background(), pool, dir); err == nil {
		t.Fatal("expected a decode error, got nil")
	}
}

// TestImport_AddError drives Import's non-duplicate add-failure path: a TOML
// card whose dewey is malformed fails validation inside Add, and Import reports
// it (rather than skipping it like an already-present card).
func TestImport_AddError(t *testing.T) {
	dir := t.TempDir()
	// A syntactically valid TOML card with an invalid (too short) dewey.
	card := "dewey = \"12\"\n\n[citation]\nraw = \"R\"\nprimary_author = \"A\"\n\n[status]\ntype = \"active\"\n"
	if err := os.WriteFile(filepath.Join(dir, "12.toml"), []byte(card), 0o600); err != nil {
		t.Fatalf("write card: %v", err)
	}
	pool := testutil.NewTestDB(t)
	if _, err := Import(context.Background(), pool, dir); err == nil {
		t.Fatal("expected an add error for a malformed dewey, got nil")
	}
}

// TestImport_EmptyDir returns zero with no error.
func TestImport_EmptyDir(t *testing.T) {
	pool := testutil.NewTestDB(t)
	n, err := Import(context.Background(), pool, t.TempDir())
	if err != nil {
		t.Fatalf("import empty: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 imported from empty dir, got %d", n)
	}
}

// TestToPortable_NilSlicesBecomeEmpty covers the nil-guard branches: a card with
// no projects and no tags exports with empty (not nil) slices, which keeps the
// TOML stable.
func TestToPortable_NilSlicesBecomeEmpty(t *testing.T) {
	p := toPortable(LibraryEntry{Dewey: "500.1", Projects: nil, Tags: nil})
	if p.Projects == nil || len(p.Projects) != 0 {
		t.Errorf("nil projects should export as empty slice, got %v", p.Projects)
	}
	if p.Tags == nil || len(p.Tags) != 0 {
		t.Errorf("nil tags should export as empty slice, got %v", p.Tags)
	}
}

// TestFromPortable_DefaultsBlankStatusToActive covers the status-default branch.
func TestFromPortable_DefaultsBlankStatusToActive(t *testing.T) {
	e := fromPortable(portableEntry{Dewey: "500.1", Status: portableStatus{Type: ""}})
	if e.Status.Type != "active" {
		t.Errorf("blank stored status should default to active, got %q", e.Status.Type)
	}
}
