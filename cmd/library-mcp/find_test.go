package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func ptrTo[T any](v T) *T { return &v }

// decodeFind unmarshals a library_find result's structured payload.
func decodeFind(t *testing.T, raw string) findOutput {
	t.Helper()
	var out findOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode find output: %v (%s)", err, raw)
	}
	return out
}

// An absent mode means keyword, so {query} alone works, and the rows are
// compact by default (top_k defaults to 5, above the threshold of 3).
func TestFind_DefaultModeIsKeywordWithCompactRows(t *testing.T) {
	cs := newConnectedClient(t)
	seedCard(t, cs, "005.1", "proj-a", "Persistence")

	out := decodeFind(t, structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Query: "Establishes 005.1"})))
	if out.Mode != "keyword" || !out.Compact || len(out.Results) != 1 || len(out.Keyword) != 0 {
		t.Fatalf("want one compact keyword row, got %+v", out)
	}
	row := out.Results[0]
	if row.Dewey != "005.1" || row.Title != "Cite 005.1" || row.Status != "active" || row.MatchedField != "establishes" {
		t.Errorf("unexpected compact row: %+v", row)
	}
}

// compact=false, or a top_k of 3 or less, returns the full keyword rows.
func TestFind_FullRowsOnRequest(t *testing.T) {
	cs := newConnectedClient(t)
	seedCard(t, cs, "005.1", "proj-a", "Persistence")
	for _, in := range []findInput{
		{Project: "proj-a", Query: "005.1", Compact: ptrTo(false)},
		{Project: "proj-a", Query: "005.1", TopK: ptrTo(3)},
	} {
		out := decodeFind(t, structuredJSON(t, mustCall(t, cs, "library_find", in)))
		if out.Compact || len(out.Results) != 0 || len(out.Keyword) != 1 || out.Keyword[0].Dewey != "005.1" {
			t.Errorf("input %+v: want one full keyword row, got %+v", in, out)
		}
	}
}

// Semantic mode with no section lists every section, capped at top_k; a named
// section is returned whole.
func TestFind_SemanticWithoutSectionSpansSectionsCappedAtTopK(t *testing.T) {
	cs := newConnectedClient(t)
	seedCard(t, cs, "005.1", "proj-a", "Persistence")
	seedCard(t, cs, "005.2", "proj-a", "Networking")
	seedCard(t, cs, "005.3", "proj-a", "Persistence")

	out := decodeFind(t, structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Mode: "semantic", Compact: ptrTo(false)})))
	if len(out.Semantic) != 3 {
		t.Errorf("no section: want all 3 cards, got %d", len(out.Semantic))
	}
	out = decodeFind(t, structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Mode: "semantic", TopK: ptrTo(2)})))
	if out.Compact || len(out.Semantic) != 2 {
		t.Errorf("no section, top_k 2: want 2 full rows, got %+v", out)
	}
	out = decodeFind(t, structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Mode: "semantic", Section: "Persistence", TopK: ptrTo(1), Compact: ptrTo(true)})))
	if len(out.Results) != 2 || out.Results[0].MatchedField != "" {
		t.Errorf("named section: want both Persistence cards as compact rows, got %+v", out)
	}
}

// A keyword miss returns no rows: with no ranker, there is no fallback listing.
func TestFind_KeywordMissStaysEmpty(t *testing.T) {
	cs := newConnectedClient(t)
	seedCard(t, cs, "005.1", "proj-a", "Persistence")
	raw := structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Query: "zyzzyva"}))
	out := decodeFind(t, raw)
	if out.Mode != "keyword" || len(out.Results)+len(out.Keyword)+len(out.Semantic) != 0 {
		t.Errorf("a miss must return no rows, got %s", raw)
	}
}

func TestFindOptions_ClampsTopK(t *testing.T) {
	for _, tc := range []struct {
		topK        *int
		wantK       int
		wantCompact bool
	}{
		{nil, 5, true}, {ptrTo(0), 1, false}, {ptrTo(-4), 1, false},
		{ptrTo(3), 3, false}, {ptrTo(4), 4, true}, {ptrTo(99), 20, true},
	} {
		k, c := findOptions(findInput{TopK: tc.topK})
		if k != tc.wantK || c != tc.wantCompact {
			t.Errorf("top_k %v: got (%d, %v), want (%d, %v)", tc.topK, k, c, tc.wantK, tc.wantCompact)
		}
	}
	if _, c := findOptions(findInput{TopK: ptrTo(10), Compact: ptrTo(false)}); c {
		t.Error("an explicit compact=false must win over the top_k default")
	}
}

// The tool description names the required card fields, so a client sees them
// before its first failed add.
func TestAddDescriptionNamesRequiredFields(t *testing.T) {
	cs := newConnectedClient(t)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "library_add" {
			continue
		}
		for _, f := range []string{"establishes", "what_it_answers", "invoke_when"} {
			if !strings.Contains(tool.Description, f) {
				t.Errorf("library_add description does not name %s: %q", f, tool.Description)
			}
		}
		return
	}
	t.Fatal("library_add not listed")
}
