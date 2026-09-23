# Getting started (for an agent)

This file is written for an AI coding agent. If someone has pointed you here,
your job is to get `library-mcp` built, running, and exercised so they can see
it work. Follow the steps in order. Each step says what to run and what a good
result looks like. Stop and report if a check fails.

## 0. What you are setting up

`library-mcp` is a Model Context Protocol server. It stores a reference library
in a local SQLite file and exposes eleven tools over stdio. You will build it,
prove it passes its own tests, run it, fill it with cards read from one of the
user's own projects, and read those cards back.

## 1. Check the toolchain

You need Go 1.26.4 or newer. Run:

```sh
go version
```

If Go is missing or older, tell the user and stop. This project needs no C
compiler and no database server.

## 2. Get the code

If you do not already have the repository, clone it and enter it:

```sh
git clone https://github.com/sophdn/library-mcp.git
cd library-mcp
```

If you were pointed at a local copy, `cd` into it instead.

## 3. Build

```sh
go build ./...
```

A good result is no output and exit code 0. If the build fails, report the
error verbatim and stop.

## 4. Run the tests

```sh
go test ./...
```

A good result is `ok` for each package. The tests are hermetic, so they need
no running server. If a test fails, report it and stop.

## 5. Run the server against a scratch database

Start the server on a throwaway database so you do not touch anything real:

```sh
go run ./cmd/library-mcp -db /tmp/library-demo.db
```

The server speaks MCP over stdin and stdout and prints nothing on a healthy
start. It creates `/tmp/library-demo.db` on first run. Leave it running while
you drive it from an MCP client, or stop it with Ctrl-C when you are done.

## 6. Register it with your MCP client

Point your MCP client at the built binary. First build a named binary:

```sh
go build -o /tmp/library-mcp ./cmd/library-mcp
```

Then add a server entry to the client's configuration. For Claude Code:

```json
{
  "mcpServers": {
    "library": {
      "command": "/tmp/library-mcp",
      "args": ["-db", "/tmp/library-demo.db"]
    }
  }
}
```

Reload the client so it picks up the new server. You should see eleven
`library_*` tools become available.

## 7. Fill the shelves from one of the user's projects

The library starts empty. Now put real cards on it. Ask the user to point you at
one of their own local projects — an app, a library, whatever is handy — or pick
one they already have open. Read it and record what a future agent would want to
know about it: one card per load-bearing decision or main module.

For each card, call `library_add`. Give it a Dewey number that groups the topic,
the source you read as the citation, the three short notes (`establishes`,
`what_it_answers`, `invoke_when`), and at least one index pointer so section
search and cross-referencing have something to work with. Here is a card built
from a project's decision to store state in SQLite:

```json
{
  "dewey": "005.74",
  "primary_author": "project ADR-003",
  "citation_raw": "docs/adr/003-persistence.md",
  "establishes": "The service stores its state in one SQLite file, with no separate database server.",
  "what_it_answers": "Why is there no database server in this stack?",
  "invoke_when": "When someone proposes adding a database server, or questions the persistence choice.",
  "tags": ["persistence"],
  "index_pointers": [
    {"section": "Persistence", "question": "What stores the service's state?", "role": "primary"}
  ]
}
```

Add a handful — five to ten cards covering the project's main decisions and
modules. Vary the section names, so the next step has something to group.

## 8. Read the shelf back

Now use what you filed:

1. **Search** with `library_find`, argument
   `{"mode": "keyword", "query": "SQLite"}`. It returns the cards whose text
   matches.
2. **List the sections** with `library_list_sections`, argument `{}`. It returns
   the section names you created, such as `Persistence`.
3. **Cross-reference** with `library_cross_reference`, argument
   `{"dewey": "005.74", "mode": "section"}`. It returns the other cards that
   share a section with that one.

If the cards you filed come back, the library works as a reference an agent
builds up from a real codebase and then queries. Report what you filed, what the
searches returned, and anything that surprised you.
