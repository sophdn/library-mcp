package library

import (
	"context"
	"testing"

	"github.com/sophdn/library-mcp/internal/testutil"
)

func TestExportImport_RoundTrip(t *testing.T) {
	src := testutil.NewTestDB(t)
	setupProject(t, src)
	ctx := context.Background()

	// A universal card with an authored date.
	u := sampleEntry("500.1")
	u.LastUpdated = "2026-04-11"
	if err := Add(ctx, src, "", u); err != nil {
		t.Fatalf("add universal: %v", err)
	}
	// A multi-project card.
	if err := Add(ctx, src, testProject, sampleEntry("500.2")); err != nil {
		t.Fatalf("add tagged: %v", err)
	}
	if _, err := Reproject(ctx, src, "500.2", ReprojectAdd, []string{"proj-b"}); err != nil {
		t.Fatalf("reproject: %v", err)
	}
	// A retired card.
	if err := Add(ctx, src, testProject, sampleEntry("500.3")); err != nil {
		t.Fatalf("add retired: %v", err)
	}
	if err := Retire(ctx, src, "500.3", "superseded"); err != nil {
		t.Fatalf("retire: %v", err)
	}

	dir := t.TempDir()
	n, err := Export(ctx, src, dir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 3 {
		t.Fatalf("expected 3 exported, got %d", n)
	}

	// Import into a fresh database.
	dst := testutil.NewTestDB(t)
	m, err := Import(ctx, dst, dir)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if m != 3 {
		t.Fatalf("expected 3 imported, got %d", m)
	}

	// Universal card: no projects, authored date preserved.
	gotU, err := Get(ctx, dst, "500.1")
	if err != nil {
		t.Fatalf("get 500.1: %v", err)
	}
	if len(gotU.Projects) != 0 {
		t.Errorf("500.1 should be universal, got %v", gotU.Projects)
	}
	if gotU.LastUpdated != "2026-04-11" {
		t.Errorf("500.1 last_updated not preserved: %q", gotU.LastUpdated)
	}

	// Multi-project card: full tag set preserved.
	gotM, _ := Get(ctx, dst, "500.2")
	if !equalStrings(gotM.Projects, []string{"proj-b", testProject}) {
		t.Errorf("500.2 projects: %v", gotM.Projects)
	}

	// Retired card: status preserved, present in the full listing.
	all, _ := listEverything(ctx, dst)
	if !deweySet(all, "500.1", "500.2", "500.3") {
		t.Errorf("listEverything after import: %v", deweys(all))
	}
	gotR, _ := Get(ctx, dst, "500.3")
	if gotR.Status.Type != "retired" {
		t.Errorf("500.3 should be retired, got %v", gotR.Status)
	}

	// Re-import is additive and idempotent: every card already exists.
	again, err := Import(ctx, dst, dir)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if again != 0 {
		t.Errorf("re-import should skip all existing, imported %d", again)
	}
}
