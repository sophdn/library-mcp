// Command library-mcp is a standalone Model Context Protocol server that hosts
// a Dewey-classified reference library. It exposes eleven tools over stdio for
// adding, retrieving, updating, retiring, searching, and cross-referencing
// library cards, backed by a local SQLite file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sophdn/library-mcp/internal/db"
	"github.com/sophdn/library-mcp/internal/library"
)

// version is the reported server version.
const version = "0.1.0"

// codeServerClosing is the JSON-RPC error code the SDK surfaces when the
// transport shuts down. An MCP stdio client stops the server by closing its
// stdin; the SDK reports that clean end-of-stream as this code wrapping io.EOF.
// See isCleanShutdown.
const codeServerClosing = -32004

func main() {
	if err := run(context.Background(), os.Args[1:], &mcp.StdioTransport{}); err != nil {
		log.Fatalf("library-mcp: %v", err)
	}
}

// run parses flags from args, opens the database, registers the tools, and
// serves over transport until the connection ends. It returns nil on a clean
// shutdown — including the client closing stdin (EOF), which is how an MCP
// stdio client stops a stdio server — and a non-nil error only on a genuine
// failure. Splitting this out of main keeps os.Exit at the edge so a test can
// drive a real serve loop and inspect the returned error.
func run(ctx context.Context, args []string, transport mcp.Transport) error {
	fs := flag.NewFlagSet("library-mcp", flag.ContinueOnError)
	defaultDB := os.Getenv("LIBRARY_MCP_DB")
	if defaultDB == "" {
		defaultDB = "library.db"
	}
	dbPath := fs.String("db", defaultDB, "path to the SQLite database file (env: LIBRARY_MCP_DB)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := db.Open(*dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer pool.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "library-mcp", Version: version}, nil)
	registerTools(server, pool)

	if err := server.Run(ctx, transport); err != nil && !isCleanShutdown(err) {
		return fmt.Errorf("server exited: %w", err)
	}
	return nil
}

// isCleanShutdown reports whether err is the ordinary end-of-stream signal an
// MCP stdio client produces when it closes the server's stdin, rather than a
// genuine transport failure. A clean stdin EOF unwinds through the SDK as a
// "server is closing" JSON-RPC error (code -32004) wrapping io.EOF; a real
// read/write fault surfaces as a different error, which stays fatal.
func isCleanShutdown(err error) bool {
	if err == nil {
		return true
	}
	var wire *jsonrpc.Error
	if errors.As(err, &wire) && wire.Code == codeServerClosing {
		return true
	}
	return errors.Is(err, io.EOF)
}

// ── Tool input and output shapes ──────────────────────────────────────

type addInput struct {
	Project       string                 `json:"project,omitempty" jsonschema:"project tag to scope this card to; empty means universal (in scope for every project)"`
	Dewey         string                 `json:"dewey" jsonschema:"Dewey number: three or more leading digits with an optional decimal, e.g. 500 or 006.31"`
	PrimaryAuthor string                 `json:"primary_author" jsonschema:"the source's primary author"`
	CitationRaw   string                 `json:"citation_raw" jsonschema:"the full citation string"`
	Year          *uint32                `json:"year,omitempty" jsonschema:"publication year"`
	Establishes   string                 `json:"establishes" jsonschema:"what this source establishes"`
	WhatItAnswers string                 `json:"what_it_answers" jsonschema:"the question this source answers"`
	InvokeWhen    string                 `json:"invoke_when" jsonschema:"when an agent should reach for this card"`
	Tags          []string               `json:"tags,omitempty" jsonschema:"free-text tags"`
	IndexPointers []library.IndexPointer `json:"index_pointers,omitempty" jsonschema:"pointers into (section, question, role) slots"`
	LastUpdated   string                 `json:"last_updated,omitempty" jsonschema:"authored date; defaults to now when empty"`
}

type deweyOutput struct {
	Dewey string `json:"dewey"`
}

type getInput struct {
	Dewey string `json:"dewey" jsonschema:"the Dewey number to fetch"`
}

type getOutput struct {
	Entry library.LibraryEntry `json:"entry"`
}

type updateInput struct {
	Dewey  string              `json:"dewey" jsonschema:"the Dewey number to update"`
	Update library.EntryUpdate `json:"update" jsonschema:"partial fields to change; omitted fields are left as-is"`
}

type updateOutput struct {
	Dewey         string   `json:"dewey"`
	FieldsChanged []string `json:"fields_changed"`
}

type retireInput struct {
	Dewey  string `json:"dewey" jsonschema:"the Dewey number to retire"`
	Reason string `json:"reason,omitempty" jsonschema:"why the card is retired"`
}

type findInput struct {
	Project string `json:"project,omitempty" jsonschema:"project scope; empty searches universal cards only"`
	Mode    string `json:"mode,omitempty" jsonschema:"one of keyword, semantic, manifest; empty means keyword"`
	Query   string `json:"query,omitempty" jsonschema:"search text; required for keyword mode. Every word must appear in some field of the card, in any order"`
	Section string `json:"section,omitempty" jsonschema:"section name; required for manifest mode. For semantic mode, empty means every section"`
	TopK    *int   `json:"top_k,omitempty" jsonschema:"result size, default 5, clamped to 1..20. It caps the every-section semantic listing, and compact rows are the default when it is above 3"`
	Compact *bool  `json:"compact,omitempty" jsonschema:"return compact rows (dewey, title, establishes first line, status, matched_field) for keyword and semantic modes; default true when top_k is above 3. Use library_get for a full card"`
}

type findOutput struct {
	Mode     string                  `json:"mode"`
	Compact  bool                    `json:"compact,omitempty"`
	Results  []library.CompactEntry  `json:"compact_results,omitempty"`
	Keyword  []library.KeywordMatch  `json:"keyword_results,omitempty"`
	Semantic []library.LibraryEntry  `json:"semantic_results,omitempty"`
	Manifest []library.ManifestEntry `json:"manifest_results,omitempty"`
}

// Defaults and bounds for library_find's top_k, and the size above which
// compact rows are the default.
const (
	defaultFindTopK  = 5
	maxFindTopK      = 20
	compactThreshold = 3
)

// findOptions resolves top_k (default 5, clamped to [1, 20]) and compact
// (default true when top_k is above 3) from a findInput.
func findOptions(in findInput) (topK int, compact bool) {
	topK = defaultFindTopK
	if in.TopK != nil {
		topK = min(max(*in.TopK, 1), maxFindTopK)
	}
	compact = topK > compactThreshold
	if in.Compact != nil {
		compact = *in.Compact
	}
	return topK, compact
}

type crossRefInput struct {
	Dewey string `json:"dewey" jsonschema:"the Dewey number to relate"`
	Mode  string `json:"mode,omitempty" jsonschema:"section (default) groups by shared section; question groups by shared (section, question)"`
}

type crossRefOutput struct {
	Result library.CrossRefResult `json:"result"`
}

type reprojectInput struct {
	Dewey    string   `json:"dewey" jsonschema:"the Dewey number to re-tag"`
	Mode     string   `json:"mode" jsonschema:"add, remove, or set"`
	Projects []string `json:"projects" jsonschema:"project tags to apply; may be empty only for mode set, which makes the card universal"`
}

type reprojectOutput struct {
	Dewey    string   `json:"dewey"`
	Projects []string `json:"projects"`
}

type projectInput struct {
	Project string `json:"project,omitempty" jsonschema:"project scope; empty lists universal cards only"`
}

type entriesOutput struct {
	Entries []library.LibraryEntry `json:"entries"`
}

type emptyInput struct{}

type listAllOutput struct {
	Entries []library.ListAllEntry `json:"entries"`
}

type sectionsOutput struct {
	Sections []string `json:"sections"`
}

type listDeweyInput struct {
	Project string `json:"project,omitempty" jsonschema:"accepted for consistency with the other tools; Dewey numbers are global, so it does not narrow the list"`
	Prefix  string `json:"prefix,omitempty" jsonschema:"Dewey prefix to filter by; empty returns every taken number"`
}

type listDeweyOutput struct {
	DeweyNumbers []string `json:"dewey_numbers"`
	Prefix       string   `json:"prefix"`
	Count        int      `json:"count"`
}

// ptr returns a pointer to v. The MCP tool hints DestructiveHint and
// OpenWorldHint are *bool, so a deliberate false serializes into the tool's
// annotations; a nil pointer would be omitted. ptr lets a tool set them inline.
func ptr[T any](v T) *T { return &v }

// ── Registration ──────────────────────────────────────────────────────

// registerTools installs the eleven library_* tools on the server, each
// backed by pool. It is separate from main so a test can register against a
// temporary database.
func registerTools(server *mcp.Server, pool *db.Pool) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_add",
		Description: "Add a card to the library under a globally unique Dewey number. dewey, primary_author, citation_raw, establishes, what_it_answers and invoke_when are required. Refuses a duplicate Dewey.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: ptr(false),
			IdempotentHint:  false,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in addInput) (*mcp.CallToolResult, deweyOutput, error) {
		entry := library.LibraryEntry{
			Dewey:         in.Dewey,
			Citation:      library.Citation{Raw: in.CitationRaw, PrimaryAuthor: in.PrimaryAuthor, Year: in.Year},
			Status:        library.EntryStatus{Type: "active"},
			Establishes:   in.Establishes,
			WhatItAnswers: in.WhatItAnswers,
			InvokeWhen:    in.InvokeWhen,
			Tags:          in.Tags,
			IndexPointers: in.IndexPointers,
			LastUpdated:   in.LastUpdated,
		}
		if err := library.Add(ctx, pool, in.Project, entry); err != nil {
			return nil, deweyOutput{}, err
		}
		return nil, deweyOutput{Dewey: in.Dewey}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_get",
		Description: "Fetch one card by its Dewey number. Dewey is the card's global identity, so no project is needed.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in getInput) (*mcp.CallToolResult, getOutput, error) {
		entry, err := library.Get(ctx, pool, in.Dewey)
		if err != nil {
			return nil, getOutput{}, err
		}
		return nil, getOutput{Entry: entry}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_update",
		Description: "Apply a partial update to a card, addressed by its Dewey number. Dewey itself is immutable.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: ptr(true),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, updateOutput, error) {
		res, err := library.Update(ctx, pool, in.Dewey, in.Update)
		if err != nil {
			return nil, updateOutput{}, err
		}
		return nil, updateOutput{Dewey: res.Dewey, FieldsChanged: res.FieldsChanged}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_retire",
		Description: "Retire a card, flipping its status to retired with a reason. Retired cards drop out of the active listings.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: ptr(true),
			IdempotentHint:  false,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in retireInput) (*mcp.CallToolResult, deweyOutput, error) {
		if err := library.Retire(ctx, pool, in.Dewey, in.Reason); err != nil {
			return nil, deweyOutput{}, err
		}
		return nil, deweyOutput{Dewey: in.Dewey}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_find",
		Description: "Search active cards. keyword mode (the default) matches every word of the query in any field; semantic mode filters by section (empty: every section); manifest mode returns slim rows for one section. keyword and semantic return compact rows by default.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findInput) (*mcp.CallToolResult, findOutput, error) {
		out, err := find(ctx, pool, in)
		return nil, out, err
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_cross_reference",
		Description: "Find other cards that share a section, or a (section, question) pair, with the target Dewey.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in crossRefInput) (*mcp.CallToolResult, crossRefOutput, error) {
		mode := library.CrossRefModeSection
		if in.Mode == "question" {
			mode = library.CrossRefModeQuestion
		}
		res, err := library.CrossReference(ctx, pool, in.Dewey, mode)
		if err != nil {
			return nil, crossRefOutput{}, err
		}
		return nil, crossRefOutput{Result: res}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_reproject",
		Description: "Change a card's project tags. add unions tags in, remove deletes them, set replaces the whole set (empty set makes the card universal).",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: ptr(true),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in reprojectInput) (*mcp.CallToolResult, reprojectOutput, error) {
		var mode library.ReprojectMode
		switch in.Mode {
		case "add":
			mode = library.ReprojectAdd
		case "remove":
			mode = library.ReprojectRemove
		case "set":
			mode = library.ReprojectSet
		default:
			return nil, reprojectOutput{}, fmt.Errorf("library_reproject mode must be add, remove, or set")
		}
		if mode != library.ReprojectSet && len(in.Projects) == 0 {
			return nil, reprojectOutput{}, fmt.Errorf("library_reproject needs a non-empty projects list for mode add or remove")
		}
		res, err := library.Reproject(ctx, pool, in.Dewey, mode, in.Projects)
		if err != nil {
			return nil, reprojectOutput{}, err
		}
		return nil, reprojectOutput{Dewey: in.Dewey, Projects: res}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_list_active",
		Description: "List active cards in scope for a project: its tagged cards plus every universal card, sorted by Dewey.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in projectInput) (*mcp.CallToolResult, entriesOutput, error) {
		entries, err := library.ListActive(ctx, pool, in.Project)
		if err != nil {
			return nil, entriesOutput{}, err
		}
		return nil, entriesOutput{Entries: entries}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_list_all",
		Description: "List every active card across all projects as slim manifest rows.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, listAllOutput, error) {
		entries, err := library.ListAllManifest(ctx, pool)
		if err != nil {
			return nil, listAllOutput{}, err
		}
		return nil, listAllOutput{Entries: entries}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_list_sections",
		Description: "List the distinct section names across a project's active cards, sorted alphabetically.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in projectInput) (*mcp.CallToolResult, sectionsOutput, error) {
		sections, err := library.ListSections(ctx, pool, in.Project)
		if err != nil {
			return nil, sectionsOutput{}, err
		}
		return nil, sectionsOutput{Sections: sections}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_list_dewey",
		Description: "List every Dewey number already taken that starts with a prefix, in any project and whether active or retired. These are the codes library_add rejects.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: ptr(false),
			IdempotentHint:  true,
			OpenWorldHint:   ptr(false),
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listDeweyInput) (*mcp.CallToolResult, listDeweyOutput, error) {
		numbers, err := library.ListOccupiedDeweyByPrefix(ctx, pool, in.Prefix)
		if err != nil {
			return nil, listDeweyOutput{}, err
		}
		return nil, listDeweyOutput{DeweyNumbers: numbers, Prefix: in.Prefix, Count: len(numbers)}, nil
	})
}

// find implements library_find. An empty mode means keyword, so {query} alone
// works. keyword needs a query; manifest needs a section; semantic takes an
// optional section, and with none it lists every section's cards, capped at
// top_k. keyword and semantic return compact rows unless compact is false.
//
// A keyword query that matches nothing returns no rows. This server has no
// ranker, so it does not fall back to a semantic listing: an unranked list
// would read as matches for an unrelated query.
func find(ctx context.Context, pool *db.Pool, in findInput) (findOutput, error) {
	topK, compact := findOptions(in)
	switch in.Mode {
	case "", "keyword":
		if in.Query == "" {
			return findOutput{}, fmt.Errorf("library_find keyword mode requires query")
		}
		hits, err := library.FindKeywordHits(ctx, pool, in.Project, in.Query)
		if err != nil {
			return findOutput{}, err
		}
		out := findOutput{Mode: "keyword", Compact: compact}
		for _, h := range hits {
			if compact {
				out.Results = append(out.Results, library.Compact(h.Entry, h.MatchedField))
			} else {
				out.Keyword = append(out.Keyword, h.Match())
			}
		}
		return out, nil
	case "semantic":
		entries, err := library.FindSemantic(ctx, pool, in.Project, in.Section)
		if err != nil {
			return findOutput{}, err
		}
		if in.Section == "" && len(entries) > topK {
			entries = entries[:topK]
		}
		out := findOutput{Mode: "semantic", Compact: compact}
		if !compact {
			out.Semantic = entries
			return out, nil
		}
		for _, e := range entries {
			out.Results = append(out.Results, library.Compact(e, ""))
		}
		return out, nil
	case "manifest":
		if in.Section == "" {
			return findOutput{}, fmt.Errorf("library_find manifest mode requires section")
		}
		manifest, err := library.FindManifest(ctx, pool, in.Project, in.Section)
		if err != nil {
			return findOutput{}, err
		}
		return findOutput{Mode: "manifest", Manifest: manifest}, nil
	default:
		return findOutput{}, fmt.Errorf("library_find mode must be keyword, semantic, or manifest")
	}
}
