# Getting started

This walkthrough gets `library-mcp` built, running, and exercised end to end.
It is self-contained: you do not need a pre-configured MCP client, and you do
not need anyone to hand you a project to fill it from. A built-in sample card
and a raw JSON-RPC transcript let you drive one write and one read yourself.
Follow the steps in order; each says what to run and what a good result looks
like. Stop and report if a check fails.

## 0. What you are setting up

`library-mcp` is a Model Context Protocol server. It stores a reference library
in a local SQLite file and exposes eleven tools over stdio. You will build it,
prove it passes its own tests, then run it and file and read back one card over
the wire — no MCP client required.

## 1. Check the toolchain

**This project needs the Go 1.26.4 toolchain or newer.** Nothing else — no C
compiler, no database server. Check your version:

```sh
go version
```

If it prints `go1.26.4` or higher, you are set. If Go is missing or older, stop
and say so; the build in step 3 will not work on an older toolchain.

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

A good result is `ok` for each package. The tests are hermetic — each opens a
fresh temporary database — so they need no running server and no network. If a
test fails, report it and stop.

## 5. Exercise it yourself over stdio (no MCP client)

You can drive the server directly by speaking its wire protocol on stdin and
reading replies on stdout. This proves one write and one read without any MCP
client wired up.

### Build a runnable binary first

```sh
go build -o /tmp/library-mcp ./cmd/library-mcp
```

Drive this built binary, not `go run` — a first-time `go run` compiles before it
starts, and that compile can outlast the short timing the runs below rely on.

### How the wire works

- **Framing is newline-delimited JSON**: one complete JSON-RPC message per line,
  and one line per message.
- **A handshake is mandatory before any tool call.** You must send an
  `initialize` request and then a `notifications/initialized` notification.
  Only after that will the server accept a `tools/call`.
- **stdin must stay open until the replies arrive.** The server shuts down when
  its stdin reaches end-of-file, so a bare `printf '…' | library-mcp` can close
  stdin before the server flushes its replies. Keep stdin open past the last
  message with a trailing `sleep`, as the runs below do.

### Define the messages

`ADD` files a **built-in sample card** under Dewey `005.74`; `GET` reads it back.

```sh
INIT='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}'
INITD='{"jsonrpc":"2.0","method":"notifications/initialized"}'
ADD='{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"library_add","arguments":{"dewey":"005.74","primary_author":"library-mcp sample","citation_raw":"Built-in sample card.","establishes":"A card is a source filed under a Dewey number.","what_it_answers":"What does one library card look like?","invoke_when":"When trying the server for the first time.","index_pointers":[{"section":"Getting started","question":"What does a card look like?","role":"primary"}]}}}'
GET='{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"library_get","arguments":{"dewey":"005.74"}}}'
```

### Write, then read

Do the write and the read as **two separate runs against the same database
file**. The card persists to the file, so the second run reads what the first
wrote, and two separate processes cannot race each other:

```sh
# Run 1 — write the card
{ printf '%s\n' "$INIT" "$INITD" "$ADD"; sleep 0.5; } | /tmp/library-mcp -db /tmp/library-demo.db

# Run 2 — read it back from the same file
{ printf '%s\n' "$INIT" "$INITD" "$GET"; sleep 0.5; } | /tmp/library-mcp -db /tmp/library-demo.db
```

A good result:

1. Run 1 prints the `initialize` reply (`id:1`) and the `library_add` reply
   (`id:2`), `{"dewey":"005.74"}`, confirming the write.
2. Run 2 prints the `initialize` reply and the `library_get` reply (`id:3`),
   whose `structuredContent.entry` is the full sample card. Because run 2 is a
   fresh process reading the file, this also proves the write persisted to disk.

Each run exits 0 on its own when stdin closes — the normal way a stdio MCP
server stops.

### (Optional) Confirm on disk

The cards live in one SQLite table, `library_entries`. The `citation_raw`
argument is stored in a column named `citation`:

```sh
sqlite3 /tmp/library-demo.db 'SELECT dewey, status, citation FROM library_entries;'
```

You should see one row: `005.74|active|Built-in sample card.`

## 6. (Optional) Register it with an MCP client

To use the server from an agent instead of by hand, point an MCP client at the
binary you built in step 5 (`/tmp/library-mcp`). Add a server entry to the
client's configuration. For Claude Code:

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
`library_*` tools become available, including `library_add`, `library_get`,
`library_find`, `library_cross_reference`, and `library_list_sections`. From
there an agent can fill the library from a real codebase — one card per
load-bearing decision or main module — and then search and cross-reference the
cards it filed. Vary the `section` names on your index pointers so section
search and cross-referencing have something to group.
