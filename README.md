# library-mcp

A small [Model Context Protocol](https://modelcontextprotocol.io) server that
keeps a reference library an agent can read and write. Each entry is a card:
a source, filed under a Dewey number, with notes on what it establishes, the
question it answers, and when to reach for it. The server speaks MCP over
stdio and stores everything in a single SQLite file. Point an agent at one of
your projects and it fills the library from what it reads, then searches and
cross-references the cards it filed. See [GETTING-STARTED.md](GETTING-STARTED.md)
for that walkthrough.

It began as one subsystem inside a larger private agent operating system. This
repository is that subsystem lifted out on its own, with a fresh MCP front end
and its own database schema, so it builds and runs with nothing else attached.

## What a card holds

- **Dewey number** — the card's identity, globally unique (three or more
  digits, with an optional decimal, such as `006.31`).
- **Citation** — the raw citation string, a primary author, and an optional
  year.
- **Establishes / what it answers / invoke when** — three short notes that let
  an agent decide whether the card is worth opening.
- **Tags** — free-text labels.
- **Index pointers** — links from the card into `(section, question, role)`
  slots, which drive section search and cross-referencing.
- **Project tags** — zero or more project labels. A card with no tags is
  universal, meaning it is in scope for every project.

## Tools

| Tool | What it does |
| --- | --- |
| `library_add` | Add a card under a new Dewey number. |
| `library_get` | Fetch one card by Dewey. |
| `library_update` | Change some fields of a card; Dewey stays fixed. |
| `library_retire` | Retire a card with a reason. |
| `library_find` | Search: `keyword` substring match, or `semantic` / `manifest` filter by section. |
| `library_cross_reference` | Find cards that share a section, or a `(section, question)` pair. |
| `library_reproject` | Change a card's project tags (`add`, `remove`, `set`). |
| `library_list_active` | List active cards in scope for a project (its own plus universal). |
| `library_list_all` | List every active card as slim rows. |
| `library_list_sections` | List the section names a project's cards use. |
| `library_list_dewey` | List Dewey numbers under a prefix. |

## Build and run

The server is pure Go and needs no C toolchain, because it uses the
`modernc.org/sqlite` driver.

```sh
go build ./...
go run ./cmd/library-mcp -db ./library.db
```

The database path comes from the `-db` flag or the `LIBRARY_MCP_DB`
environment variable, and defaults to `library.db` in the working directory.
The file is created on first run.

To register the server with an MCP client, point the client at the built
binary. For example, in a Claude Code configuration:

```json
{
  "mcpServers": {
    "library": {
      "command": "/absolute/path/to/library-mcp",
      "args": ["-db", "/absolute/path/to/library.db"]
    }
  }
}
```

## Tests

```sh
go test ./...
```

The tests are hermetic. Each opens a fresh temporary database, so they need no
server and no network.

**Coverage:** the two logic packages carry `internal/library` 95.9% and
`internal/db` 93.0% of statements, 95.6% aggregate over the two (the pure test
helper `internal/testutil` is excluded). Reproduce with:

```sh
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out | tail -1
```

## License

MIT. See [LICENSE](LICENSE).
