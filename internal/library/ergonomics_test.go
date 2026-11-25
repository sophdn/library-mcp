package library

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sophdn/library-mcp/internal/db"
	"github.com/sophdn/library-mcp/internal/testutil"
)

func newErgoDB(t *testing.T) (*db.Pool, context.Context) {
	t.Helper()
	return testutil.NewTestDB(t), context.Background()
}

func addOrFail(t *testing.T, ctx context.Context, pool *db.Pool, e LibraryEntry) {
	t.Helper()
	if err := Add(ctx, pool, testProject, e); err != nil {
		t.Fatalf("add %s: %v", e.Dewey, err)
	}
}

// ── Required card fields ──

func TestAdd_RequiresCardBodyFields(t *testing.T) {
	pool, ctx := newErgoDB(t)
	for _, tc := range []struct {
		field string
		clear func(*LibraryEntry)
	}{
		{"establishes", func(e *LibraryEntry) { e.Establishes = "  " }},
		{"what_it_answers", func(e *LibraryEntry) { e.WhatItAnswers = "" }},
		{"invoke_when", func(e *LibraryEntry) { e.InvokeWhen = "\n" }},
	} {
		e := sampleEntry("500.1")
		tc.clear(&e)
		err := Add(ctx, pool, testProject, e)
		if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("missing %s: want a validation error naming it, got %v", tc.field, err)
		}
	}
}

// ── Per-token keyword matching ──

func TestFindKeyword_TwoWordQueryMatchesAnyOrder(t *testing.T) {
	pool, ctx := newErgoDB(t)
	e := sampleEntry("500.1")
	e.Citation.PrimaryAuthor = "Grice"
	e.Establishes = "Conversational implicature arises from the cooperative principle."
	addOrFail(t, ctx, pool, e)
	other := sampleEntry("500.2")
	other.Establishes = "Something about implicature only."
	addOrFail(t, ctx, pool, other)

	for _, q := range []string{"implicature Grice", "grice IMPLICATURE", "cooperative implicature"} {
		matches, err := FindKeyword(ctx, pool, testProject, q)
		if err != nil {
			t.Fatalf("find %q: %v", q, err)
		}
		if len(matches) != 1 || matches[0].Dewey != "500.1" {
			t.Errorf("query %q: want only 500.1, got %+v", q, matches)
		}
	}
}

func TestFindKeyword_AllTokensRequired(t *testing.T) {
	pool, ctx := newErgoDB(t)
	addOrFail(t, ctx, pool, sampleEntry("500.1"))
	matches, err := FindKeyword(ctx, pool, testProject, "baseline zyzzyva")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("a missing token must exclude the entry, got %+v", matches)
	}
	// A query with no letters or digits has no tokens and matches nothing.
	if matches, _ := FindKeyword(ctx, pool, testProject, "--"); len(matches) != 0 {
		t.Errorf("a token-free query must match nothing, got %+v", matches)
	}
}

func TestFindKeyword_TokensSearchCitationAndWhatItAnswers(t *testing.T) {
	pool, ctx := newErgoDB(t)
	e := sampleEntry("500.1")
	e.Citation.Raw = "Grice, H. P. (1975). Logic and conversation."
	e.Citation.PrimaryAuthor = "Grice"
	e.WhatItAnswers = "What is a zorblax?"
	addOrFail(t, ctx, pool, e)

	for _, tc := range []struct{ q, field string }{
		{"Grice 1975", "primary_author"},
		{"logic conversation", "citation"},
		{"zorblax what", "what_it_answers"},
		{"does work", "index_pointer.question"},
	} {
		matches, err := FindKeyword(ctx, pool, testProject, tc.q)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 || matches[0].MatchedField != tc.field {
			t.Errorf("query %q: want one match on %s, got %+v", tc.q, tc.field, matches)
		}
	}
}

func TestFindKeyword_WholePhraseKeepsPriorityField(t *testing.T) {
	pool, ctx := newErgoDB(t)
	e := sampleEntry("500.1")
	e.InvokeWhen = "when picking a cromulent baseline"
	addOrFail(t, ctx, pool, e)
	matches, _ := FindKeyword(ctx, pool, testProject, "cromulent baseline")
	if len(matches) != 1 || matches[0].MatchedField != "invoke_when" {
		t.Errorf("whole phrase must report invoke_when, got %+v", matches)
	}
}

// ── FindSemantic: an empty section means every section ──

func TestFindSemantic_EmptySectionReturnsAllSections(t *testing.T) {
	pool, ctx := newErgoDB(t)
	addOrFail(t, ctx, pool, sampleEntry("500.1"))
	other := sampleEntry("500.2")
	other.IndexPointers = []IndexPointer{{Section: "OtherSection", Question: "?", Role: "primary"}}
	addOrFail(t, ctx, pool, other)
	bare := sampleEntry("500.3")
	bare.IndexPointers = nil
	addOrFail(t, ctx, pool, bare)

	res, err := FindSemantic(ctx, pool, testProject, "")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range res {
		got = append(got, e.Dewey)
	}
	if strings.Join(got, ",") != "500.1,500.2,500.3" {
		t.Errorf("empty section must return every in-scope entry, got %v", got)
	}
}

// ── Compact rows ──

func TestCompact_RowIsUnder300Chars(t *testing.T) {
	e := sampleEntry("500.1234")
	e.Citation.Raw = strings.Repeat("Verylongauthorname, A. B. ", 30) + "(2020). A title."
	e.Establishes = strings.Repeat("establishes a great deal of detail ", 40)
	e.Status = EntryStatus{Type: "retired", Reason: strings.Repeat("r", 500)}
	for _, field := range []string{"", "index_pointer.question"} {
		row := Compact(e, field)
		b, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		if n := utf8.RuneCount(b); n >= 300 {
			t.Errorf("compact row is %d chars (field=%q): %s", n, field, b)
		}
		if row.Dewey != "500.1234" || row.Status != "retired" || row.Title == "" || row.Establishes == "" {
			t.Errorf("compact row lost a field: %+v", row)
		}
	}
}

func TestCompact_ShortFieldsPassThrough(t *testing.T) {
	e := sampleEntry("500.1")
	e.Establishes = "Line one.\nLine two."
	row := Compact(e, "tags")
	if row.Title != "Doe, J. (2025). Foo bar." || row.Establishes != "Line one." || row.Status != "active" || row.MatchedField != "tags" {
		t.Errorf("unexpected compact row: %+v", row)
	}
}
