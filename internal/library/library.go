// Package library hosts the library_entries CRUD + find + cross-reference
// operations.
//
// The library_entries.index_pointers column stores a JSON-serialised
// []IndexPointer; serialisation happens on every read/write through this
// module. Generic taxonomy: section and role are plain strings — projects
// supply their own vocabulary, this server ships no domain content.
package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sophdn/library-mcp/internal/db"
)

// ── Types ─────────────────────────────────────────────────────────────

// Citation pairs the raw citation string with structured author/year fields.
type Citation struct {
	Raw           string  `json:"raw"`
	PrimaryAuthor string  `json:"primary_author"`
	Year          *uint32 `json:"year"`
}

// EntryStatus is one of Active or Retired{reason}. Serialised with a tagged
// `type` discriminator:
//
//	{"type":"active"}
//	{"type":"retired","reason":"…"}
type EntryStatus struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

// IndexPointer points from an entry into one (section, question, role) slot.
type IndexPointer struct {
	Section  string `json:"section"`
	Question string `json:"question"`
	Role     string `json:"role"`
}

// LibraryEntry is one row of library_entries. Projects is the card's set of
// project tags (many-to-many via library_entry_projects); an empty Projects
// means the card is universal — in scope for every project.
type LibraryEntry struct {
	Dewey         string         `json:"dewey"`
	Citation      Citation       `json:"citation"`
	Status        EntryStatus    `json:"status"`
	Establishes   string         `json:"establishes"`
	WhatItAnswers string         `json:"what_it_answers"`
	InvokeWhen    string         `json:"invoke_when"`
	Tags          []string       `json:"tags"`
	IndexPointers []IndexPointer `json:"index_pointers"`
	LastUpdated   string         `json:"last_updated"`
	Projects      []string       `json:"projects"`
}

// ReprojectMode selects how library_reproject changes a card's project tags.
type ReprojectMode int

const (
	// ReprojectAdd adds the given projects to the card's tag set.
	ReprojectAdd ReprojectMode = iota
	// ReprojectRemove removes the given projects from the card's tag set.
	ReprojectRemove
	// ReprojectSet replaces the card's tag set with exactly the given projects.
	ReprojectSet
)

// EntryUpdate is a partial update payload. nil fields are skipped.
type EntryUpdate struct {
	Citation      *Citation       `json:"citation,omitempty"`
	Status        *EntryStatus    `json:"status,omitempty"`
	Establishes   *string         `json:"establishes,omitempty"`
	WhatItAnswers *string         `json:"what_it_answers,omitempty"`
	InvokeWhen    *string         `json:"invoke_when,omitempty"`
	Tags          *[]string       `json:"tags,omitempty"`
	IndexPointers *[]IndexPointer `json:"index_pointers,omitempty"`
}

// UpdateResult names the dewey of the updated entry and the fields changed.
type UpdateResult struct {
	Dewey         string   `json:"dewey"`
	FieldsChanged []string `json:"fields_changed"`
}

// KeywordMatch is one library_find keyword-mode hit.
type KeywordMatch struct {
	Dewey         string  `json:"dewey"`
	PrimaryAuthor string  `json:"primary_author"`
	Year          *uint32 `json:"year"`
	Establishes   string  `json:"establishes"`
	MatchedField  string  `json:"matched_field"`
}

// ManifestEntry is one library_find manifest-mode hit.
type ManifestEntry struct {
	Dewey                string  `json:"dewey"`
	PrimaryAuthor        string  `json:"primary_author"`
	Year                 *uint32 `json:"year"`
	WhatItAnswersSummary string  `json:"what_it_answers_summary"`
}

// ListAllEntry is one slim row of the whole-catalogue view (library_list_all):
// enough to orient — dewey, author, year, a one-line summary, and the project
// tag set — without the full establishes / index_pointers payload.
type ListAllEntry struct {
	Dewey                string   `json:"dewey"`
	PrimaryAuthor        string   `json:"primary_author"`
	Year                 *uint32  `json:"year"`
	WhatItAnswersSummary string   `json:"what_it_answers_summary"`
	Projects             []string `json:"projects"`
}

// CrossRefMode selects how cross-reference grouping happens. Section shares any
// section; Question shares any (section, question) pair.
type CrossRefMode int

const (
	// CrossRefModeSection groups by shared section.
	CrossRefModeSection CrossRefMode = iota
	// CrossRefModeQuestion groups by shared (section, question) pair.
	CrossRefModeQuestion
)

// CrossRefEntry is one cross-reference hit.
type CrossRefEntry struct {
	Dewey         string  `json:"dewey"`
	PrimaryAuthor string  `json:"primary_author"`
	Year          *uint32 `json:"year"`
	Establishes   string  `json:"establishes"`
	Role          *string `json:"role,omitempty"`
}

// CrossRefResult carries the target entry plus grouped neighbours.
type CrossRefResult struct {
	Dewey      string                     `json:"dewey"`
	BySection  map[string][]CrossRefEntry `json:"by_section"`
	ByQuestion map[string][]CrossRefEntry `json:"by_question"`
}

// ── Errors ────────────────────────────────────────────────────────────

var (
	// ErrAlreadyExists fires when the globally unique dewey is already taken.
	ErrAlreadyExists = errors.New("library entry already exists")
	// ErrNotFound fires when a get/update/retire target doesn't exist.
	ErrNotFound = errors.New("library entry not found")
	// ErrValidation fires for required-field / shape failures.
	ErrValidation = errors.New("library validation")
	// ErrInvalidDewey fires when a dewey string fails ValidateDewey.
	ErrInvalidDewey = errors.New("invalid dewey number")
)

// ── DeweyNumber validation ────────────────────────────────────────────

// ValidateDewey enforces the dewey-number shape: ≥3 leading digits, optional
// `.<digits>` decimal. Mirrors DeweyNumber::new.
func ValidateDewey(s string) error {
	if len(s) < 3 {
		return fmt.Errorf("%w: too short: %q", ErrInvalidDewey, s)
	}
	for i := 0; i < 3; i++ {
		if s[i] < '0' || s[i] > '9' {
			return fmt.Errorf("%w: non-digit prefix: %q", ErrInvalidDewey, s)
		}
	}
	if len(s) == 3 {
		return nil
	}
	if s[3] != '.' {
		return fmt.Errorf("%w: missing decimal point: %q", ErrInvalidDewey, s)
	}
	if len(s) == 4 {
		return fmt.Errorf("%w: empty decimal: %q", ErrInvalidDewey, s)
	}
	for i := 4; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return fmt.Errorf("%w: non-digit decimal: %q", ErrInvalidDewey, s)
		}
	}
	return nil
}

// ── CRUD ──────────────────────────────────────────────────────────────

// Add inserts a new library entry with a globally unique Dewey number and, when
// projectID is non-empty, tags the card with that project. Validates required
// fields and refuses to overwrite an existing Dewey.
func Add(ctx context.Context, pool *db.Pool, projectID string, entry LibraryEntry) error {
	if err := validateEntry(entry); err != nil {
		return err
	}
	var existing int64
	row := pool.DB().QueryRowContext(ctx,
		`SELECT id FROM library_entries WHERE dewey = ?`, entry.Dewey)
	switch err := row.Scan(&existing); {
	case err == nil:
		return fmt.Errorf("%w: %s", ErrAlreadyExists, entry.Dewey)
	case errors.Is(err, sql.ErrNoRows):
		// fall through
	default:
		return fmt.Errorf("library add: existence check: %w", err)
	}

	pointersJSON, err := json.Marshal(entry.IndexPointers)
	if err != nil {
		return fmt.Errorf("library add: marshal pointers: %w", err)
	}
	tagsJSON, err := json.Marshal(entry.Tags)
	if err != nil {
		return fmt.Errorf("library add: marshal tags: %w", err)
	}
	status := entry.Status.Type
	if status == "" {
		status = "active"
	}
	// entry.Citation.Year is already *uint32; database/sql serialises a nil
	// pointer as SQL NULL and dereferences a non-nil one to its underlying
	// value — no wrapper needed.
	year := entry.Citation.Year
	return pool.WithWrite(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO library_entries
				(dewey, primary_author, year, citation,
				 establishes, what_it_answers, invoke_when, tags,
				 status, index_pointers, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			         COALESCE(NULLIF(?, ''), datetime('now')))`,
			entry.Dewey, entry.Citation.PrimaryAuthor, year, entry.Citation.Raw,
			entry.Establishes, entry.WhatItAnswers, entry.InvokeWhen, string(tagsJSON),
			status, string(pointersJSON), entry.LastUpdated,
		)
		if err != nil {
			return err
		}
		if projectID == "" {
			return nil
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("library add: last insert id: %w", err)
		}
		_, err = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO library_entry_projects (entry_id, project_id) VALUES (?, ?)`,
			id, projectID)
		return err
	})
}

// Get returns one entry by its globally unique dewey, or ErrNotFound. Dewey
// validation failures silently return ErrNotFound (an invalid dewey
// can't exist). The lookup is project-agnostic — Dewey is the global identity.
func Get(ctx context.Context, pool *db.Pool, dewey string) (LibraryEntry, error) {
	if err := ValidateDewey(dewey); err != nil {
		return LibraryEntry{}, fmt.Errorf("%w: %s", ErrNotFound, dewey)
	}
	row := pool.DB().QueryRowContext(ctx,
		`SELECT `+entrySelectCols+` FROM library_entries le WHERE le.dewey = ?`,
		dewey)
	entry, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryEntry{}, fmt.Errorf("%w: %s", ErrNotFound, dewey)
	}
	if err != nil {
		return LibraryEntry{}, fmt.Errorf("library get %s: %w", dewey, err)
	}
	return entry, nil
}

// Update applies a partial update, addressing the card by its global dewey.
// Dewey is immutable; passing a new dewey is out of contract.
func Update(ctx context.Context, pool *db.Pool, dewey string, upd EntryUpdate) (UpdateResult, error) {
	entry, err := Get(ctx, pool, dewey)
	if err != nil {
		return UpdateResult{}, err
	}
	var changed []string
	if upd.Citation != nil {
		entry.Citation = *upd.Citation
		changed = append(changed, "citation")
	}
	if upd.Status != nil {
		entry.Status = *upd.Status
		changed = append(changed, "status")
	}
	if upd.Establishes != nil {
		entry.Establishes = *upd.Establishes
		changed = append(changed, "establishes")
	}
	if upd.WhatItAnswers != nil {
		entry.WhatItAnswers = *upd.WhatItAnswers
		changed = append(changed, "what_it_answers")
	}
	if upd.InvokeWhen != nil {
		entry.InvokeWhen = *upd.InvokeWhen
		changed = append(changed, "invoke_when")
	}
	if upd.Tags != nil {
		entry.Tags = *upd.Tags
		changed = append(changed, "tags")
	}
	if upd.IndexPointers != nil {
		for i, ptr := range *upd.IndexPointers {
			if strings.TrimSpace(ptr.Question) == "" {
				return UpdateResult{}, fmt.Errorf("%w: index_pointers[%d].question must not be empty", ErrValidation, i)
			}
		}
		entry.IndexPointers = *upd.IndexPointers
		changed = append(changed, "index_pointers")
	}

	status := entry.Status.Type
	if status == "" {
		status = "active"
	}
	pointersJSON, err := json.Marshal(entry.IndexPointers)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("library update: marshal pointers: %w", err)
	}
	tagsJSON, err := json.Marshal(entry.Tags)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("library update: marshal tags: %w", err)
	}
	// entry.Citation.Year is already *uint32; database/sql serialises a nil
	// pointer as SQL NULL and dereferences a non-nil one to its underlying
	// value — no wrapper needed.
	year := entry.Citation.Year
	err = pool.WithWrite(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE library_entries SET
				primary_author = ?, year = ?, citation = ?,
				establishes = ?, what_it_answers = ?, invoke_when = ?,
				tags = ?, status = ?, index_pointers = ?,
				updated_at = datetime('now')
			 WHERE dewey = ?`,
			entry.Citation.PrimaryAuthor, year, entry.Citation.Raw,
			entry.Establishes, entry.WhatItAnswers, entry.InvokeWhen,
			string(tagsJSON), status, string(pointersJSON),
			dewey,
		)
		return err
	})
	if err != nil {
		return UpdateResult{}, fmt.Errorf("library update: %w", err)
	}
	return UpdateResult{Dewey: dewey, FieldsChanged: changed}, nil
}

// Retire flips an entry's status to retired{reason}. Returns ErrValidation if
// the entry is already retired.
func Retire(ctx context.Context, pool *db.Pool, dewey, reason string) error {
	entry, err := Get(ctx, pool, dewey)
	if err != nil {
		return err
	}
	if entry.Status.Type == "retired" {
		return fmt.Errorf("%w: library entry %s already retired", ErrValidation, dewey)
	}
	_, err = Update(ctx, pool, dewey, EntryUpdate{
		Status: &EntryStatus{Type: "retired", Reason: reason},
	})
	return err
}

// Reproject changes the project tag set of the card identified by dewey. Add
// unions the given projects in, Remove deletes them, Set replaces the whole set.
// Returns the resulting sorted tag set. An empty result means the card is now
// universal. Returns ErrNotFound if no card carries the dewey.
func Reproject(ctx context.Context, pool *db.Pool, dewey string, mode ReprojectMode, projects []string) ([]string, error) {
	if err := ValidateDewey(dewey); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, dewey)
	}
	var entryID int64
	row := pool.DB().QueryRowContext(ctx,
		`SELECT id FROM library_entries WHERE dewey = ?`, dewey)
	switch err := row.Scan(&entryID); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("%w: %s", ErrNotFound, dewey)
	case err != nil:
		return nil, fmt.Errorf("library reproject: lookup %s: %w", dewey, err)
	}

	err := pool.WithWrite(ctx, func(tx *sql.Tx) error {
		if mode == ReprojectSet {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM library_entry_projects WHERE entry_id = ?`, entryID); err != nil {
				return err
			}
		}
		for _, p := range projects {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			switch mode {
			case ReprojectRemove:
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM library_entry_projects WHERE entry_id = ? AND project_id = ?`,
					entryID, p); err != nil {
					return err
				}
			default: // ReprojectAdd, ReprojectSet
				if _, err := tx.ExecContext(ctx,
					`INSERT OR IGNORE INTO library_entry_projects (entry_id, project_id) VALUES (?, ?)`,
					entryID, p); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("library reproject: %w", err)
	}
	return projectsForEntry(ctx, pool, entryID)
}

// projectsForEntry returns the sorted project tag set for one entry id.
func projectsForEntry(ctx context.Context, pool *db.Pool, entryID int64) ([]string, error) {
	rows, err := pool.DB().QueryContext(ctx,
		`SELECT project_id FROM library_entry_projects WHERE entry_id = ? ORDER BY project_id`,
		entryID)
	if err != nil {
		return nil, fmt.Errorf("library reproject: read tags: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListActive returns the active entries in scope for projectID, sorted by
// dewey: every card tagged with projectID plus every universal (untagged) card.
// Universal cards are in scope for every project, so untagging a card (see
// Reproject) keeps it visible here.
func ListActive(ctx context.Context, pool *db.Pool, projectID string) ([]LibraryEntry, error) {
	rows, err := pool.DB().QueryContext(ctx,
		`SELECT `+entrySelectCols+`
		 FROM library_entries le
		 WHERE le.status = 'active'
		   AND (EXISTS (SELECT 1 FROM library_entry_projects lep
		                WHERE lep.entry_id = le.id AND lep.project_id = ?)
		        OR NOT EXISTS (SELECT 1 FROM library_entry_projects lep2
		                       WHERE lep2.entry_id = le.id))
		 ORDER BY le.dewey`,
		projectID)
	if err != nil {
		return nil, fmt.Errorf("library list_active: %w", err)
	}
	return scanEntries(rows, "library list_active")
}

// ListAll returns every active entry across all projects, sorted by dewey,
// regardless of project tags. This is the whole-catalogue view.
func ListAll(ctx context.Context, pool *db.Pool) ([]LibraryEntry, error) {
	rows, err := pool.DB().QueryContext(ctx,
		`SELECT `+entrySelectCols+`
		 FROM library_entries le
		 WHERE le.status = 'active'
		 ORDER BY le.dewey`)
	if err != nil {
		return nil, fmt.Errorf("library list_all: %w", err)
	}
	return scanEntries(rows, "library list_all")
}

// ListAllManifest returns the whole-catalogue view as slim rows (no full-text
// fields), so an agent can orient over every project without a large payload.
func ListAllManifest(ctx context.Context, pool *db.Pool) ([]ListAllEntry, error) {
	entries, err := ListAll(ctx, pool)
	if err != nil {
		return nil, err
	}
	out := make([]ListAllEntry, len(entries))
	for i, e := range entries {
		out[i] = ListAllEntry{
			Dewey:                e.Dewey,
			PrimaryAuthor:        e.Citation.PrimaryAuthor,
			Year:                 e.Citation.Year,
			WhatItAnswersSummary: summarizeFirstLine(e.WhatItAnswers, 160),
			Projects:             e.Projects,
		}
	}
	return out, nil
}

// scanEntries drains rows into entries, closing rows. label names the caller
// for error wrapping.
func scanEntries(rows *sql.Rows, label string) ([]LibraryEntry, error) {
	defer rows.Close()
	// Non-nil zero-length slice so JSON marshals as `[]`, not `null`.
	out := []LibraryEntry{}
	for rows.Next() {
		entry, err := scanEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: scan: %w", label, err)
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// ListSections returns every distinct section name across active entries'
// index_pointers, sorted alphabetically. Lightweight alternative to
// ListActive for agents that need section names before find.
func ListSections(ctx context.Context, pool *db.Pool, projectID string) ([]string, error) {
	entries, err := ListActive(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, e := range entries {
		for _, p := range e.IndexPointers {
			set[p.Section] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

// ListDeweyByPrefix returns active deweys in scope for projectID (its tags plus
// universal) that start with prefix, sorted.
func ListDeweyByPrefix(ctx context.Context, pool *db.Pool, projectID, prefix string) ([]string, error) {
	entries, err := ListActive(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		if strings.HasPrefix(e.Dewey, prefix) {
			out = append(out, e.Dewey)
		}
	}
	return out, nil
}

// ── Find ──────────────────────────────────────────────────────────────

// FindKeyword scans active entries for case-insensitive substring matches
// against (in priority order): establishes, invoke_when, primary_author, tags,
// index_pointer.question. Returns one KeywordMatch per matching entry.
func FindKeyword(ctx context.Context, pool *db.Pool, projectID, q string) ([]KeywordMatch, error) {
	entries, err := ListActive(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}
	qLower := strings.ToLower(q)
	out := []KeywordMatch{}
	for _, e := range entries {
		field := keywordMatchField(e, qLower)
		if field == "" {
			continue
		}
		out = append(out, KeywordMatch{
			Dewey:         e.Dewey,
			PrimaryAuthor: e.Citation.PrimaryAuthor,
			Year:          e.Citation.Year,
			Establishes:   e.Establishes,
			MatchedField:  field,
		})
	}
	return out, nil
}

func keywordMatchField(e LibraryEntry, qLower string) string {
	if strings.Contains(strings.ToLower(e.Establishes), qLower) {
		return "establishes"
	}
	if strings.Contains(strings.ToLower(e.InvokeWhen), qLower) {
		return "invoke_when"
	}
	if strings.Contains(strings.ToLower(e.Citation.PrimaryAuthor), qLower) {
		return "primary_author"
	}
	for _, t := range e.Tags {
		if strings.Contains(strings.ToLower(t), qLower) {
			return "tags"
		}
	}
	for _, ptr := range e.IndexPointers {
		if strings.Contains(strings.ToLower(ptr.Question), qLower) {
			return "index_pointer.question"
		}
	}
	return ""
}

// FindSemantic returns active entries with at least one index_pointer in the
// given section.
func FindSemantic(ctx context.Context, pool *db.Pool, projectID, section string) ([]LibraryEntry, error) {
	entries, err := ListActive(ctx, pool, projectID)
	if err != nil {
		return nil, err
	}
	out := []LibraryEntry{}
	for _, e := range entries {
		for _, p := range e.IndexPointers {
			if p.Section == section {
				out = append(out, e)
				break
			}
		}
	}
	return out, nil
}

// FindManifest returns compact manifest views of active entries with at least
// one index_pointer in the given section.
func FindManifest(ctx context.Context, pool *db.Pool, projectID, section string) ([]ManifestEntry, error) {
	entries, err := FindSemantic(ctx, pool, projectID, section)
	if err != nil {
		return nil, err
	}
	out := make([]ManifestEntry, len(entries))
	for i, e := range entries {
		out[i] = ManifestEntry{
			Dewey:                e.Dewey,
			PrimaryAuthor:        e.Citation.PrimaryAuthor,
			Year:                 e.Citation.Year,
			WhatItAnswersSummary: summarizeFirstLine(e.WhatItAnswers, 160),
		}
	}
	return out, nil
}

func summarizeFirstLine(text string, maxChars int) string {
	for _, line := range strings.Split(text, "\n") {
		first := strings.TrimSpace(line)
		if first == "" {
			continue
		}
		runes := []rune(first)
		if len(runes) <= maxChars {
			return first
		}
		return string(runes[:maxChars]) + "…"
	}
	return ""
}

// ── Cross-reference ──────────────────────────────────────────────────

// CrossReference returns other active entries that share at least one
// section (Section mode) or one (section, question) pair (Question mode)
// with the target dewey.
func CrossReference(ctx context.Context, pool *db.Pool, dewey string, mode CrossRefMode) (CrossRefResult, error) {
	target, err := Get(ctx, pool, dewey)
	if err != nil {
		return CrossRefResult{}, err
	}
	entries, err := ListAll(ctx, pool)
	if err != nil {
		return CrossRefResult{}, err
	}
	result := CrossRefResult{
		Dewey:      target.Dewey,
		BySection:  map[string][]CrossRefEntry{},
		ByQuestion: map[string][]CrossRefEntry{},
	}

	switch mode {
	case CrossRefModeSection:
		targetSections := make(map[string]struct{}, len(target.IndexPointers))
		for _, p := range target.IndexPointers {
			targetSections[p.Section] = struct{}{}
		}
		for _, e := range entries {
			if e.Dewey == target.Dewey {
				continue
			}
			cross := CrossRefEntry{
				Dewey:         e.Dewey,
				PrimaryAuthor: e.Citation.PrimaryAuthor,
				Year:          e.Citation.Year,
				Establishes:   e.Establishes,
			}
			seen := make(map[string]struct{})
			for _, p := range e.IndexPointers {
				if _, ok := targetSections[p.Section]; !ok {
					continue
				}
				if _, dup := seen[p.Section]; dup {
					continue
				}
				seen[p.Section] = struct{}{}
				result.BySection[p.Section] = append(result.BySection[p.Section], cross)
			}
		}
	case CrossRefModeQuestion:
		type pair struct{ section, question string }
		targetPairs := make(map[pair]struct{}, len(target.IndexPointers))
		for _, p := range target.IndexPointers {
			targetPairs[pair{p.Section, p.Question}] = struct{}{}
		}
		for _, e := range entries {
			if e.Dewey == target.Dewey {
				continue
			}
			seen := make(map[string]struct{})
			for _, p := range e.IndexPointers {
				if _, ok := targetPairs[pair{p.Section, p.Question}]; !ok {
					continue
				}
				key := p.Section + "::" + p.Question
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				role := p.Role
				result.ByQuestion[key] = append(result.ByQuestion[key], CrossRefEntry{
					Dewey:         e.Dewey,
					PrimaryAuthor: e.Citation.PrimaryAuthor,
					Year:          e.Citation.Year,
					Establishes:   e.Establishes,
					Role:          &role,
				})
			}
		}
	}
	return result, nil
}

// ── Row scanning + validation helpers ────────────────────────────────

// entrySelectCols is the column list every full-entry read shares, aliasing the
// table as le. The final `projects` column is a comma-joined list of the card's
// project tags (NULL when the card is universal), produced by a correlated
// subquery over library_entry_projects. Column order matches scanEntry.
const entrySelectCols = `le.dewey, le.primary_author, le.year, le.citation,
	        le.establishes, le.what_it_answers, le.invoke_when, le.tags,
	        le.status, le.index_pointers, le.updated_at,
	        (SELECT GROUP_CONCAT(project_id) FROM library_entry_projects
	         WHERE entry_id = le.id) AS projects`

func scanEntry(s db.Scanner) (LibraryEntry, error) {
	var (
		dewey, primaryAuthor, citation, establishes string
		whatItAnswers, invokeWhen, tagsJSON         string
		status, pointersJSON, updatedAt             string
		year                                        sql.NullInt64
		projectsCSV                                 sql.NullString
	)
	if err := s.Scan(&dewey, &primaryAuthor, &year, &citation, &establishes,
		&whatItAnswers, &invokeWhen, &tagsJSON, &status, &pointersJSON, &updatedAt,
		&projectsCSV); err != nil {
		return LibraryEntry{}, err
	}
	if err := ValidateDewey(dewey); err != nil {
		return LibraryEntry{}, fmt.Errorf("library entry has invalid dewey: %w", err)
	}
	var pointers []IndexPointer
	if pointersJSON != "" {
		if err := json.Unmarshal([]byte(pointersJSON), &pointers); err != nil {
			return LibraryEntry{}, fmt.Errorf("decode index_pointers: %w", err)
		}
	}
	var tags []string
	if tagsJSON != "" {
		if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
			// Malformed tags JSON falls back to empty rather than
			// surfacing the error.
			tags = nil
		}
	}
	es := EntryStatus{Type: "active"}
	switch status {
	case "active":
		es = EntryStatus{Type: "active"}
	case "retired":
		es = EntryStatus{Type: "retired"}
	default:
		es = EntryStatus{Type: "retired", Reason: "unknown status '" + status + "'"}
	}
	var yearPtr *uint32
	if year.Valid {
		v := uint32(year.Int64)
		yearPtr = &v
	}
	// Non-nil zero-length slice so JSON marshals as `[]`, not `null`. A NULL or
	// empty GROUP_CONCAT means the card is universal (no project tags).
	projects := []string{}
	if projectsCSV.Valid && projectsCSV.String != "" {
		projects = strings.Split(projectsCSV.String, ",")
		sort.Strings(projects)
	}
	return LibraryEntry{
		Dewey: dewey,
		Citation: Citation{
			Raw:           citation,
			PrimaryAuthor: primaryAuthor,
			Year:          yearPtr,
		},
		Status:        es,
		Establishes:   establishes,
		WhatItAnswers: whatItAnswers,
		InvokeWhen:    invokeWhen,
		Tags:          tags,
		IndexPointers: pointers,
		LastUpdated:   updatedAt,
		Projects:      projects,
	}, nil
}

func validateEntry(e LibraryEntry) error {
	if err := ValidateDewey(e.Dewey); err != nil {
		return err
	}
	if strings.TrimSpace(e.Citation.Raw) == "" {
		return fmt.Errorf("%w: citation.raw is required", ErrValidation)
	}
	if strings.TrimSpace(e.Citation.PrimaryAuthor) == "" {
		return fmt.Errorf("%w: citation.primary_author is required", ErrValidation)
	}
	for i, p := range e.IndexPointers {
		if strings.TrimSpace(p.Question) == "" {
			return fmt.Errorf("%w: index_pointers[%d].question is required", ErrValidation, i)
		}
	}
	return nil
}
