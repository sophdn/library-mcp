package main

import (
	"context"
	"encoding/json"
	"testing"
)

// wantHints is the expected MCP behavior-hint set for one tool.
type wantHints struct {
	readOnly    bool
	destructive bool
	idempotent  bool
	openWorld   bool
}

// TestTools_AllFourHintsDeclared asserts every registered tool declares all four
// MCP behavior hints as booleans on the wire: readOnlyHint, destructiveHint,
// idempotentHint, and openWorldHint. The OpenAI tool directory rejects a tool
// when any hint is missing or non-boolean, and the SDK marshals two of them
// through a pointer, so this guards the serialized JSON rather than the Go
// struct. It also pins each hint's value to the tool's observed behavior.
func TestTools_AllFourHintsDeclared(t *testing.T) {
	want := map[string]wantHints{
		"library_add":             {readOnly: false, destructive: false, idempotent: false, openWorld: false},
		"library_get":             {readOnly: true, destructive: false, idempotent: true, openWorld: false},
		"library_update":          {readOnly: false, destructive: true, idempotent: true, openWorld: false},
		"library_retire":          {readOnly: false, destructive: true, idempotent: false, openWorld: false},
		"library_find":            {readOnly: true, destructive: false, idempotent: true, openWorld: false},
		"library_cross_reference": {readOnly: true, destructive: false, idempotent: true, openWorld: false},
		"library_reproject":       {readOnly: false, destructive: true, idempotent: true, openWorld: false},
		"library_list_active":     {readOnly: true, destructive: false, idempotent: true, openWorld: false},
		"library_list_all":        {readOnly: true, destructive: false, idempotent: true, openWorld: false},
		"library_list_sections":   {readOnly: true, destructive: false, idempotent: true, openWorld: false},
		"library_list_dewey":      {readOnly: true, destructive: false, idempotent: true, openWorld: false},
	}

	cs := newConnectedClient(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(res.Tools), len(want))
	}

	hintKeys := []string{"readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"}
	seen := make(map[string]bool, len(want))
	for _, tool := range res.Tools {
		seen[tool.Name] = true
		w, ok := want[tool.Name]
		if !ok {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		if tool.Annotations == nil {
			t.Errorf("%s: no annotations", tool.Name)
			continue
		}

		raw, err := json.Marshal(tool.Annotations)
		if err != nil {
			t.Fatalf("%s: marshal annotations: %v", tool.Name, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("%s: unmarshal annotations: %v", tool.Name, err)
		}

		for _, k := range hintKeys {
			v, present := got[k]
			if !present {
				t.Errorf("%s: hint %q missing from %s", tool.Name, k, raw)
				continue
			}
			if _, isBool := v.(bool); !isBool {
				t.Errorf("%s: hint %q is %T, want bool", tool.Name, k, v)
			}
		}

		if b, _ := got["readOnlyHint"].(bool); b != w.readOnly {
			t.Errorf("%s: readOnlyHint = %v, want %v", tool.Name, b, w.readOnly)
		}
		if b, _ := got["destructiveHint"].(bool); b != w.destructive {
			t.Errorf("%s: destructiveHint = %v, want %v", tool.Name, b, w.destructive)
		}
		if b, _ := got["idempotentHint"].(bool); b != w.idempotent {
			t.Errorf("%s: idempotentHint = %v, want %v", tool.Name, b, w.idempotent)
		}
		if b, _ := got["openWorldHint"].(bool); b != w.openWorld {
			t.Errorf("%s: openWorldHint = %v, want %v", tool.Name, b, w.openWorld)
		}
	}

	for name := range want {
		if !seen[name] {
			t.Errorf("tool %q not returned by ListTools", name)
		}
	}
}
