-- Library schema: a Dewey-classified reference catalogue.
--
-- library_entries holds one card per Dewey number, which is globally unique.
-- library_entry_projects tags a card with zero or more project labels; an
-- untagged card is universal, meaning it is in scope for every project. The
-- project_id is a free-text label, not a foreign key, so the library runs as
-- a self-contained store with no separate projects registry.

CREATE TABLE IF NOT EXISTS library_entries (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    dewey             TEXT    NOT NULL UNIQUE,
    primary_author    TEXT    NOT NULL DEFAULT '',
    year              INTEGER,
    citation          TEXT    NOT NULL DEFAULT '',
    establishes       TEXT    NOT NULL DEFAULT '',
    what_it_answers   TEXT    NOT NULL DEFAULT '',
    invoke_when       TEXT    NOT NULL DEFAULT '',
    tags              TEXT    NOT NULL DEFAULT '',
    status            TEXT    NOT NULL DEFAULT 'active',
    index_pointers    TEXT    NOT NULL DEFAULT '[]',
    created_at        TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS library_entry_projects (
    entry_id    INTEGER NOT NULL REFERENCES library_entries(id) ON DELETE CASCADE,
    project_id  TEXT    NOT NULL,
    PRIMARY KEY (entry_id, project_id)
);

CREATE INDEX IF NOT EXISTS idx_library_status ON library_entries (status);
CREATE INDEX IF NOT EXISTS idx_library_entry_projects_project ON library_entry_projects (project_id);
