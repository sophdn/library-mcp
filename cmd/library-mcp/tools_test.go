package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sophdn/library-mcp/internal/db"
	"github.com/sophdn/library-mcp/internal/library"
)

// newConnectedClient registers the tools against a fresh temp database and
// returns a client session connected to the server over an in-memory transport.
func newConnectedClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	pool, err := db.Open(filepath.Join(t.TempDir(), "tools.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = pool.Close() })

	server := mcp.NewServer(&mcp.Implementation{Name: "library-mcp", Version: "test"}, nil)
	registerTools(server, pool)

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call invokes a tool and fails the test on a transport error. It returns the
// result so a caller can inspect IsError and the payload.
func call(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

// mustCall invokes a tool and fails if the handler reported an error.
func mustCall(t *testing.T, cs *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res := call(t, cs, name, args)
	if res.IsError {
		t.Fatalf("%s reported an error: %s", name, contentText(res))
	}
	return res
}

func seedCard(t *testing.T, cs *mcp.ClientSession, dewey, project, section string) {
	t.Helper()
	mustCall(t, cs, "library_add", addInput{
		Project:       project,
		Dewey:         dewey,
		PrimaryAuthor: "Author " + dewey,
		CitationRaw:   "Cite " + dewey,
		Establishes:   "Establishes " + dewey,
		WhatItAnswers: "What " + dewey,
		InvokeWhen:    "When " + dewey,
		Tags:          []string{"tag-" + dewey},
		IndexPointers: []library.IndexPointer{{Section: section, Question: "Q " + dewey, Role: "primary"}},
	})
}

// TestTools_AllElevenRoundTrip drives every registered tool through the wire so
// each handler and its schema is exercised end to end.
func TestTools_AllElevenRoundTrip(t *testing.T) {
	cs := newConnectedClient(t)

	seedCard(t, cs, "005.1", "proj-a", "Persistence")
	seedCard(t, cs, "005.2", "proj-a", "Persistence")
	seedCard(t, cs, "006.3", "", "Retrieval") // universal card

	// library_get
	if got := structuredJSON(t, mustCall(t, cs, "library_get", getInput{Dewey: "005.1"})); !strings.Contains(got, "Author 005.1") {
		t.Errorf("library_get missing author: %s", got)
	}

	// library_update
	newEstablishes := "Revised establishes."
	upd := mustCall(t, cs, "library_update", updateInput{
		Dewey:  "005.1",
		Update: library.EntryUpdate{Establishes: &newEstablishes},
	})
	if got := structuredJSON(t, upd); !strings.Contains(got, "establishes") {
		t.Errorf("library_update should report the changed field: %s", got)
	}

	// library_find keyword / semantic / manifest
	if got := structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Mode: "keyword", Query: "Revised"})); !strings.Contains(got, "005.1") {
		t.Errorf("keyword find missed the updated card: %s", got)
	}
	if got := structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Mode: "semantic", Section: "Persistence"})); !strings.Contains(got, "005.2") {
		t.Errorf("semantic find missed a Persistence card: %s", got)
	}
	if got := structuredJSON(t, mustCall(t, cs, "library_find", findInput{Project: "proj-a", Mode: "manifest", Section: "Persistence"})); !strings.Contains(got, "005.1") {
		t.Errorf("manifest find missed a Persistence card: %s", got)
	}

	// library_cross_reference (both modes)
	if got := structuredJSON(t, mustCall(t, cs, "library_cross_reference", crossRefInput{Dewey: "005.1"})); !strings.Contains(got, "005.2") {
		t.Errorf("section cross-reference missed the sibling: %s", got)
	}
	mustCall(t, cs, "library_cross_reference", crossRefInput{Dewey: "005.1", Mode: "question"})

	// library_reproject (all three modes)
	mustCall(t, cs, "library_reproject", reprojectInput{Dewey: "005.1", Mode: "add", Projects: []string{"proj-b"}})
	mustCall(t, cs, "library_reproject", reprojectInput{Dewey: "005.1", Mode: "remove", Projects: []string{"proj-b"}})
	if got := structuredJSON(t, mustCall(t, cs, "library_reproject", reprojectInput{Dewey: "005.1", Mode: "set", Projects: []string{"proj-a"}})); !strings.Contains(got, "proj-a") {
		t.Errorf("reproject set should report the new tag set: %s", got)
	}

	// library_list_active / list_all / list_sections / list_dewey
	if got := structuredJSON(t, mustCall(t, cs, "library_list_active", projectInput{Project: "proj-a"})); !strings.Contains(got, "006.3") {
		t.Errorf("list_active should include the universal card: %s", got)
	}
	if got := structuredJSON(t, mustCall(t, cs, "library_list_all", emptyInput{})); !strings.Contains(got, "006.3") {
		t.Errorf("list_all should include every card: %s", got)
	}
	if got := structuredJSON(t, mustCall(t, cs, "library_list_sections", projectInput{Project: "proj-a"})); !strings.Contains(got, "Persistence") {
		t.Errorf("list_sections missing Persistence: %s", got)
	}
	if got := structuredJSON(t, mustCall(t, cs, "library_list_dewey", listDeweyInput{Project: "proj-a", Prefix: "005"})); !strings.Contains(got, "005.2") {
		t.Errorf("list_dewey prefix 005 missed a card: %s", got)
	}

	// library_retire
	mustCall(t, cs, "library_retire", retireInput{Dewey: "005.2", Reason: "superseded"})
	if got := structuredJSON(t, mustCall(t, cs, "library_list_active", projectInput{Project: "proj-a"})); strings.Contains(got, "005.2") {
		t.Errorf("retired card should drop out of list_active: %s", got)
	}
}

// TestTools_ErrorBranches exercises the argument-validation error paths the
// handlers own: the find-mode and reproject-mode switches, and the
// required-argument guards.
func TestTools_ErrorBranches(t *testing.T) {
	cs := newConnectedClient(t)
	seedCard(t, cs, "005.1", "proj-a", "Persistence")

	errorCases := []struct {
		name string
		tool string
		args any
	}{
		{"find unknown mode", "library_find", findInput{Mode: "bogus"}},
		{"find keyword needs query", "library_find", findInput{Mode: "keyword"}},
		{"find semantic needs section", "library_find", findInput{Mode: "semantic"}},
		{"find manifest needs section", "library_find", findInput{Mode: "manifest"}},
		{"reproject unknown mode", "library_reproject", reprojectInput{Dewey: "005.1", Mode: "bogus", Projects: []string{"x"}}},
		{"reproject add needs projects", "library_reproject", reprojectInput{Dewey: "005.1", Mode: "add"}},
		{"add duplicate dewey", "library_add", addInput{Dewey: "005.1", PrimaryAuthor: "A", CitationRaw: "C"}},
		{"get unknown dewey", "library_get", getInput{Dewey: "999.99"}},
		{"update unknown dewey", "library_update", updateInput{Dewey: "999.99"}},
		{"retire unknown dewey", "library_retire", retireInput{Dewey: "999.99"}},
	}
	for _, tc := range errorCases {
		t.Run(tc.name, func(t *testing.T) {
			res := call(t, cs, tc.tool, tc.args)
			if !res.IsError {
				t.Errorf("%s: expected an error result, got success: %s", tc.tool, structuredJSON(t, res))
			}
		})
	}
}
