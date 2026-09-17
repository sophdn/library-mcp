// portable.go implements a file-backed export/import of the library catalogue.
// The export writes one TOML file per card (the archived-snapshot shape plus a
// projects tag set) into a directory. It is a portable, human-readable copy of
// the catalogue that round-trips: export to files, import into a fresh
// database, and every card returns with its tags and status intact.
package library

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/sophdn/library-mcp/internal/db"
)

// portableEntry is the on-disk TOML shape: the archived card fields plus the
// project tag set. An empty projects list means the card is universal.
type portableEntry struct {
	Dewey         string                 `toml:"dewey"`
	Establishes   string                 `toml:"establishes"`
	WhatItAnswers string                 `toml:"what_it_answers"`
	InvokeWhen    string                 `toml:"invoke_when"`
	Tags          []string               `toml:"tags"`
	LastUpdated   string                 `toml:"last_updated"`
	Projects      []string               `toml:"projects"`
	Citation      portableCitation       `toml:"citation"`
	Status        portableStatus         `toml:"status"`
	IndexPointers []portableIndexPointer `toml:"index_pointers"`
}

type portableCitation struct {
	Raw           string  `toml:"raw"`
	PrimaryAuthor string  `toml:"primary_author"`
	Year          *uint32 `toml:"year"`
}

type portableStatus struct {
	Type   string `toml:"type"`
	Reason string `toml:"reason,omitempty"`
}

type portableIndexPointer struct {
	Section  string `toml:"section"`
	Question string `toml:"question"`
	Role     string `toml:"role"`
}

// Export writes every card (active and retired, all projects) to dir as one
// <dewey>.toml file each, creating dir if needed. Returns the count written.
func Export(ctx context.Context, pool *db.Pool, dir string) (int, error) {
	entries, err := listEverything(ctx, pool)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return 0, fmt.Errorf("library export: mkdir %s: %w", dir, err)
	}
	for _, e := range entries {
		p := toPortable(e)
		var sb strings.Builder
		if err := toml.NewEncoder(&sb).Encode(p); err != nil {
			return 0, fmt.Errorf("library export: encode %s: %w", e.Dewey, err)
		}
		path := filepath.Join(dir, e.Dewey+".toml")
		if err := os.WriteFile(path, []byte(sb.String()), 0o600); err != nil {
			return 0, fmt.Errorf("library export: write %s: %w", path, err)
		}
	}
	return len(entries), nil
}

// Import reads every *.toml in dir and loads it into the catalogue: each card is
// created with its stored status and last_updated, then tagged with its project
// set. Cards whose dewey already exists are skipped (import is additive, safe to
// re-run). Returns the count imported.
func Import(ctx context.Context, pool *db.Pool, dir string) (int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil {
		return 0, fmt.Errorf("library import: glob %s: %w", dir, err)
	}
	sort.Strings(matches)
	imported := 0
	for _, path := range matches {
		var p portableEntry
		if _, err := toml.DecodeFile(path, &p); err != nil {
			return imported, fmt.Errorf("library import: decode %s: %w", path, err)
		}
		entry := fromPortable(p)
		err := Add(ctx, pool, "", entry)
		if err != nil {
			if strings.Contains(err.Error(), ErrAlreadyExists.Error()) {
				continue // additive: leave the existing card untouched
			}
			return imported, fmt.Errorf("library import: add %s: %w", p.Dewey, err)
		}
		if len(p.Projects) > 0 {
			if _, err := Reproject(ctx, pool, entry.Dewey, ReprojectSet, p.Projects); err != nil {
				return imported, fmt.Errorf("library import: tag %s: %w", p.Dewey, err)
			}
		}
		imported++
	}
	return imported, nil
}

// listEverything returns all cards regardless of status, sorted by dewey.
func listEverything(ctx context.Context, pool *db.Pool) ([]LibraryEntry, error) {
	rows, err := pool.DB().QueryContext(ctx,
		`SELECT `+entrySelectCols+` FROM library_entries le ORDER BY le.dewey`)
	if err != nil {
		return nil, fmt.Errorf("library export: %w", err)
	}
	return scanEntries(rows, "library export")
}

func toPortable(e LibraryEntry) portableEntry {
	ptrs := make([]portableIndexPointer, len(e.IndexPointers))
	for i, p := range e.IndexPointers {
		ptrs[i] = portableIndexPointer{Section: p.Section, Question: p.Question, Role: p.Role}
	}
	projects := e.Projects
	if projects == nil {
		projects = []string{}
	}
	tags := e.Tags
	if tags == nil {
		tags = []string{}
	}
	return portableEntry{
		Dewey:         e.Dewey,
		Establishes:   e.Establishes,
		WhatItAnswers: e.WhatItAnswers,
		InvokeWhen:    e.InvokeWhen,
		Tags:          tags,
		LastUpdated:   e.LastUpdated,
		Projects:      projects,
		Citation: portableCitation{
			Raw:           e.Citation.Raw,
			PrimaryAuthor: e.Citation.PrimaryAuthor,
			Year:          e.Citation.Year,
		},
		Status:        portableStatus{Type: e.Status.Type, Reason: e.Status.Reason},
		IndexPointers: ptrs,
	}
}

func fromPortable(p portableEntry) LibraryEntry {
	ptrs := make([]IndexPointer, len(p.IndexPointers))
	for i, ip := range p.IndexPointers {
		ptrs[i] = IndexPointer{Section: ip.Section, Question: ip.Question, Role: ip.Role}
	}
	status := EntryStatus{Type: p.Status.Type, Reason: p.Status.Reason}
	if status.Type == "" {
		status.Type = "active"
	}
	return LibraryEntry{
		Dewey:         p.Dewey,
		Citation:      Citation{Raw: p.Citation.Raw, PrimaryAuthor: p.Citation.PrimaryAuthor, Year: p.Citation.Year},
		Status:        status,
		Establishes:   p.Establishes,
		WhatItAnswers: p.WhatItAnswers,
		InvokeWhen:    p.InvokeWhen,
		Tags:          p.Tags,
		IndexPointers: ptrs,
		LastUpdated:   p.LastUpdated,
		Projects:      p.Projects,
	}
}
