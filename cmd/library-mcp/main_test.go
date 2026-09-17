package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sophdn/library-mcp/internal/db"
	"github.com/sophdn/library-mcp/internal/library"
)

// TestServer_AddGetListSections drives the server end to end over an in-memory
// transport: it registers the tools, connects a client, and calls three tools.
// This proves the tool schemas infer without panicking and that the handlers
// round-trip a card through the database.
func TestServer_AddGetListSections(t *testing.T) {
	pool, err := db.Open(filepath.Join(t.TempDir(), "smoke.db"))
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

	// library_add
	addRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name: "library_add",
		Arguments: addInput{
			Dewey:         "005.1",
			PrimaryAuthor: "Brooks",
			CitationRaw:   "Brooks, F. (1975). The Mythical Man-Month.",
			Establishes:   "Adding people to a late project makes it later.",
			WhatItAnswers: "Why does adding staff slow a late project?",
			InvokeWhen:    "When someone proposes adding staff to a slipping schedule.",
			IndexPointers: []library.IndexPointer{{Section: "Estimation", Question: "More staff, faster?", Role: "primary"}},
		},
	})
	if err != nil {
		t.Fatalf("call library_add: %v", err)
	}
	if addRes.IsError {
		t.Fatalf("library_add reported an error: %s", contentText(addRes))
	}

	// library_get
	getRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "library_get",
		Arguments: map[string]any{"dewey": "005.1"},
	})
	if err != nil {
		t.Fatalf("call library_get: %v", err)
	}
	if getRes.IsError {
		t.Fatalf("library_get reported an error: %s", contentText(getRes))
	}
	if got := structuredJSON(t, getRes); !strings.Contains(got, "Brooks") {
		t.Errorf("library_get result missing author Brooks: %s", got)
	}

	// library_list_sections
	secRes, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "library_list_sections",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call library_list_sections: %v", err)
	}
	if got := structuredJSON(t, secRes); !strings.Contains(got, "Estimation") {
		t.Errorf("library_list_sections missing Estimation: %s", got)
	}
}

func contentText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func structuredJSON(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	return string(data)
}
