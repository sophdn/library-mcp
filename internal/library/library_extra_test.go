package library

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/sophdn/library-mcp/internal/testutil"
)

// ── Update: every field + validation ────────────────────────────────

func TestUpdate_AllFields(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	_ = Add(ctx, pool, testProject, sampleEntry("500.42"))

	newCitation := Citation{Raw: "New (2027).", PrimaryAuthor: "Newton", Year: ptrU32(2027)}
	newStatus := EntryStatus{Type: "active"}
	establishes := "New establishes."
	answers := "New answer."
	invoke := "New invoke."
	tags := []string{"a", "b"}
	pointers := []IndexPointer{{Section: "NewSection", Question: "New question?", Role: "primary"}}

	res, err := Update(ctx, pool, "500.42", EntryUpdate{
		Citation:      &newCitation,
		Status:        &newStatus,
		Establishes:   &establishes,
		WhatItAnswers: &answers,
		InvokeWhen:    &invoke,
		Tags:          &tags,
		IndexPointers: &pointers,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(res.FieldsChanged) != 7 {
		t.Errorf("expected 7 fields changed, got %v", res.FieldsChanged)
	}
	got, _ := Get(ctx, pool, "500.42")
	if got.Citation.PrimaryAuthor != "Newton" || got.Citation.Year == nil || *got.Citation.Year != 2027 {
		t.Errorf("citation not updated: %+v", got.Citation)
	}
	if got.WhatItAnswers != answers || got.InvokeWhen != invoke {
		t.Errorf("string fields not updated: %+v", got)
	}
	if len(got.IndexPointers) != 1 || got.IndexPointers[0].Section != "NewSection" {
		t.Errorf("index_pointers not updated: %v", got.IndexPointers)
	}
}

func TestUpdate_RejectsEmptyPointerQuestion(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	_ = Add(ctx, pool, testProject, sampleEntry("500.42"))
	pointers := []IndexPointer{{Section: "S", Question: "  ", Role: "r"}}
	_, err := Update(ctx, pool, "500.42", EntryUpdate{IndexPointers: &pointers})
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation on empty pointer question, got %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	pool := testutil.NewTestDB(t)
	_, err := Update(context.Background(), pool, "999.99", EntryUpdate{})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAdd_DefaultsBlankStatusToActive(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	e := sampleEntry("500.42")
	e.Status = EntryStatus{} // blank type
	if err := Add(ctx, pool, "", e); err != nil {
		t.Fatalf("add: %v", err)
	}
	got, _ := Get(ctx, pool, "500.42")
	if got.Status.Type != "active" {
		t.Errorf("blank status on add should default to active, got %q", got.Status.Type)
	}
}

// ── CrossReference: duplicate-slot dedupe branches ──────────────────

func TestCrossReference_DedupesRepeatedSectionAndQuestion(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	target := sampleEntry("500.1") // section FoundationalDecisions, question "How does foo work?"
	// A neighbour that points into the same section twice, and the same
	// (section, question) pair twice, so each dedupe branch fires.
	neighbour := sampleEntry("500.2")
	neighbour.IndexPointers = []IndexPointer{
		{Section: "FoundationalDecisions", Question: "How does foo work?", Role: "primary"},
		{Section: "FoundationalDecisions", Question: "How does foo work?", Role: "secondary"},
	}
	_ = Add(ctx, pool, testProject, target)
	_ = Add(ctx, pool, testProject, neighbour)

	sec, err := CrossReference(ctx, pool, "500.1", CrossRefModeSection)
	if err != nil {
		t.Fatalf("section: %v", err)
	}
	if len(sec.BySection["FoundationalDecisions"]) != 1 {
		t.Errorf("repeated section should appear once, got %v", sec.BySection["FoundationalDecisions"])
	}
	q, err := CrossReference(ctx, pool, "500.1", CrossRefModeQuestion)
	if err != nil {
		t.Fatalf("question: %v", err)
	}
	key := "FoundationalDecisions::How does foo work?"
	if len(q.ByQuestion[key]) != 1 {
		t.Errorf("repeated question pair should appear once, got %v", q.ByQuestion[key])
	}
}

// ── Get / Reproject: invalid-dewey branches ─────────────────────────

func TestGet_InvalidDeweyIsNotFound(t *testing.T) {
	pool := testutil.NewTestDB(t)
	_, err := Get(context.Background(), pool, "xx")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid dewey should be ErrNotFound, got %v", err)
	}
}

func TestReproject_InvalidDeweyIsNotFound(t *testing.T) {
	pool := testutil.NewTestDB(t)
	_, err := Reproject(context.Background(), pool, "xx", ReprojectAdd, []string{"p"})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid dewey should be ErrNotFound, got %v", err)
	}
}

func TestReproject_SkipsBlankProjects(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	_ = Add(ctx, pool, "", sampleEntry("500.42"))
	got, err := Reproject(ctx, pool, "500.42", ReprojectAdd, []string{"  ", "real", ""})
	if err != nil {
		t.Fatalf("reproject: %v", err)
	}
	if len(got) != 1 || got[0] != "real" {
		t.Errorf("blank project tags should be skipped, got %v", got)
	}
}

// ── ListAllManifest ─────────────────────────────────────────────────

func TestListAllManifest_SlimRows(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	_ = Add(ctx, pool, "proj-a", sampleEntry("500.1"))
	_ = Add(ctx, pool, "", sampleEntry("500.2"))
	rows, err := ListAllManifest(ctx, pool)
	if err != nil {
		t.Fatalf("list_all_manifest: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].Dewey != "500.1" || rows[0].PrimaryAuthor != "Doe" {
		t.Errorf("row 0 wrong: %+v", rows[0])
	}
	if rows[0].WhatItAnswersSummary == "" {
		t.Errorf("summary should be populated: %+v", rows[0])
	}
}

// ── keywordMatchField priority ladder ───────────────────────────────

func TestFindKeyword_MatchesEachPriorityField(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(*LibraryEntry)
		query string
		want  string
	}{
		{"invoke_when", func(e *LibraryEntry) { e.Establishes = "x"; e.InvokeWhen = "trigger-word" }, "trigger-word", "invoke_when"},
		{"primary_author", func(e *LibraryEntry) { e.Establishes = "x"; e.InvokeWhen = "y"; e.Citation.PrimaryAuthor = "Knuth" }, "knuth", "primary_author"},
		{"question", func(e *LibraryEntry) {
			e.Establishes = "x"
			e.InvokeWhen = "y"
			e.Citation.PrimaryAuthor = "z"
			e.Tags = nil
			e.IndexPointers = []IndexPointer{{Section: "S", Question: "askable-thing", Role: "r"}}
		}, "askable-thing", "index_pointer.question"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testutil.NewTestDB(t)
			ctx := context.Background()
			e := sampleEntry("500.1")
			tc.mut(&e)
			if err := Add(ctx, pool, testProject, e); err != nil {
				t.Fatalf("add: %v", err)
			}
			matches, err := FindKeyword(ctx, pool, testProject, tc.query)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			if len(matches) != 1 || matches[0].MatchedField != tc.want {
				t.Errorf("want field %q, got %+v", tc.want, matches)
			}
		})
	}
}

func TestFindKeyword_NoMatch(t *testing.T) {
	pool := testutil.NewTestDB(t)
	ctx := context.Background()
	_ = Add(ctx, pool, testProject, sampleEntry("500.1"))
	matches, err := FindKeyword(ctx, pool, testProject, "no-such-substring-anywhere")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("expected no matches, got %v", matches)
	}
}

// ── summarizeFirstLine edge cases ───────────────────────────────────

func TestSummarizeFirstLine(t *testing.T) {
	if got := summarizeFirstLine("", 10); got != "" {
		t.Errorf("empty text should summarize to empty, got %q", got)
	}
	if got := summarizeFirstLine("\n\n  first line\nsecond", 40); got != "first line" {
		t.Errorf("leading blank lines should be skipped, got %q", got)
	}
	if got := summarizeFirstLine("   \n\t\n", 40); got != "" {
		t.Errorf("all-blank text should summarize to empty, got %q", got)
	}
}

// ── scanEntry defensive branches (white-box row injection) ──────────

// insertRaw writes a row straight into library_entries, bypassing Add's
// validation, so a test can exercise scanEntry's handling of odd stored values.
func insertRaw(t *testing.T, pool interface {
	WithWrite(context.Context, func(*sql.Tx) error) error
}, dewey, status, tagsJSON string, year sql.NullInt64) {
	t.Helper()
	err := pool.WithWrite(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO library_entries (dewey, primary_author, year, citation, status, tags, index_pointers)
			 VALUES (?, 'A', ?, 'C', ?, ?, '[]')`,
			dewey, year, status, tagsJSON)
		return err
	})
	if err != nil {
		t.Fatalf("insertRaw: %v", err)
	}
}

func TestScanEntry_UnknownStatusBecomesRetired(t *testing.T) {
	pool := testutil.NewTestDB(t)
	insertRaw(t, pool, "500.1", "gibberish", "[]", sql.NullInt64{})
	got, err := Get(context.Background(), pool, "500.1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.Type != "retired" || got.Status.Reason == "" {
		t.Errorf("unknown status should map to retired with a reason, got %+v", got.Status)
	}
}

func TestScanEntry_MalformedTagsFallBackToEmpty(t *testing.T) {
	pool := testutil.NewTestDB(t)
	insertRaw(t, pool, "500.2", "active", "{not valid json", sql.NullInt64{})
	got, err := Get(context.Background(), pool, "500.2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Tags) != 0 {
		t.Errorf("malformed tags should fall back to empty, got %v", got.Tags)
	}
}

func TestScanEntry_NullYear(t *testing.T) {
	pool := testutil.NewTestDB(t)
	insertRaw(t, pool, "500.3", "active", "[]", sql.NullInt64{})
	got, err := Get(context.Background(), pool, "500.3")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Citation.Year != nil {
		t.Errorf("null year should scan to nil, got %v", got.Citation.Year)
	}
}

func TestScanEntry_PresentYear(t *testing.T) {
	pool := testutil.NewTestDB(t)
	insertRaw(t, pool, "500.4", "active", "[]", sql.NullInt64{Int64: 1999, Valid: true})
	got, err := Get(context.Background(), pool, "500.4")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Citation.Year == nil || *got.Citation.Year != 1999 {
		t.Errorf("present year should scan through, got %v", got.Citation.Year)
	}
}
