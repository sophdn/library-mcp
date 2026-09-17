// Command library-mcp is a standalone Model Context Protocol server that hosts
// a Dewey-classified reference library. It exposes eleven tools over stdio for
// adding, retrieving, updating, retiring, searching, and cross-referencing
// library cards, backed by a local SQLite file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sophdn/library-mcp/internal/db"
	"github.com/sophdn/library-mcp/internal/library"
)

// version is the reported server version.
const version = "0.1.0"

func main() {
	defaultDB := os.Getenv("LIBRARY_MCP_DB")
	if defaultDB == "" {
		defaultDB = "library.db"
	}
	dbPath := flag.String("db", defaultDB, "path to the SQLite database file (env: LIBRARY_MCP_DB)")
	flag.Parse()

	pool, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("library-mcp: open database: %v", err)
	}
	defer pool.Close()

	server := mcp.NewServer(&mcp.Implementation{Name: "library-mcp", Version: version}, nil)
	registerTools(server, pool)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("library-mcp: server exited: %v", err)
	}
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
	Mode    string `json:"mode" jsonschema:"one of keyword, semantic, manifest"`
	Query   string `json:"query,omitempty" jsonschema:"search text; required for keyword mode"`
	Section string `json:"section,omitempty" jsonschema:"section name; required for semantic and manifest modes"`
}

type findOutput struct {
	Mode     string                  `json:"mode"`
	Keyword  []library.KeywordMatch  `json:"keyword_results,omitempty"`
	Semantic []library.LibraryEntry  `json:"semantic_results,omitempty"`
	Manifest []library.ManifestEntry `json:"manifest_results,omitempty"`
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
	Project string `json:"project,omitempty" jsonschema:"project scope"`
	Prefix  string `json:"prefix,omitempty" jsonschema:"Dewey prefix to filter by; empty returns all in scope"`
}

type listDeweyOutput struct {
	DeweyNumbers []string `json:"dewey_numbers"`
	Prefix       string   `json:"prefix"`
	Count        int      `json:"count"`
}

// ── Registration ──────────────────────────────────────────────────────

// registerTools installs the eleven library_* tools on the server, each
// backed by pool. It is separate from main so a test can register against a
// temporary database.
func registerTools(server *mcp.Server, pool *db.Pool) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_add",
		Description: "Add a card to the library under a globally unique Dewey number. Refuses a duplicate Dewey.",
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in retireInput) (*mcp.CallToolResult, deweyOutput, error) {
		if err := library.Retire(ctx, pool, in.Dewey, in.Reason); err != nil {
			return nil, deweyOutput{}, err
		}
		return nil, deweyOutput{Dewey: in.Dewey}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_find",
		Description: "Search active cards. keyword mode substring-matches text; semantic and manifest modes filter by section (manifest returns slim rows).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in findInput) (*mcp.CallToolResult, findOutput, error) {
		switch in.Mode {
		case "keyword":
			if in.Query == "" {
				return nil, findOutput{}, fmt.Errorf("library_find keyword mode requires query")
			}
			matches, err := library.FindKeyword(ctx, pool, in.Project, in.Query)
			if err != nil {
				return nil, findOutput{}, err
			}
			return nil, findOutput{Mode: "keyword", Keyword: matches}, nil
		case "semantic":
			if in.Section == "" {
				return nil, findOutput{}, fmt.Errorf("library_find semantic mode requires section")
			}
			entries, err := library.FindSemantic(ctx, pool, in.Project, in.Section)
			if err != nil {
				return nil, findOutput{}, err
			}
			return nil, findOutput{Mode: "semantic", Semantic: entries}, nil
		case "manifest":
			if in.Section == "" {
				return nil, findOutput{}, fmt.Errorf("library_find manifest mode requires section")
			}
			manifest, err := library.FindManifest(ctx, pool, in.Project, in.Section)
			if err != nil {
				return nil, findOutput{}, err
			}
			return nil, findOutput{Mode: "manifest", Manifest: manifest}, nil
		default:
			return nil, findOutput{}, fmt.Errorf("library_find mode must be keyword, semantic, or manifest")
		}
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_cross_reference",
		Description: "Find other cards that share a section, or a (section, question) pair, with the target Dewey.",
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
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in projectInput) (*mcp.CallToolResult, sectionsOutput, error) {
		sections, err := library.ListSections(ctx, pool, in.Project)
		if err != nil {
			return nil, sectionsOutput{}, err
		}
		return nil, sectionsOutput{Sections: sections}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "library_list_dewey",
		Description: "List active Dewey numbers in scope for a project that start with a prefix.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listDeweyInput) (*mcp.CallToolResult, listDeweyOutput, error) {
		numbers, err := library.ListDeweyByPrefix(ctx, pool, in.Project, in.Prefix)
		if err != nil {
			return nil, listDeweyOutput{}, err
		}
		return nil, listDeweyOutput{DeweyNumbers: numbers, Prefix: in.Prefix, Count: len(numbers)}, nil
	})
}
