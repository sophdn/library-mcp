# Getting started (for an agent)

This file is written for an AI coding agent. If someone has pointed you here,
your job is to get `library-mcp` built, running, and exercised so they can see
it work. Follow the steps in order. Each step says what to run and what a good
result looks like. Stop and report if a check fails.

## 0. What you are setting up

`library-mcp` is a Model Context Protocol server. It stores a reference library
in a local SQLite file and exposes eleven tools over stdio. You will build it,
prove it passes its own tests, run it, and call three tools against it.

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

## 7. Exercise three tools

Call these in order and confirm each result.

1. **Add a card** with `library_add`:

   ```json
   {
     "dewey": "005.1",
     "primary_author": "Brooks",
     "citation_raw": "Brooks, F. (1975). The Mythical Man-Month.",
     "year": 1975,
     "establishes": "Adding people to a late project makes it later.",
     "what_it_answers": "Why does adding staff slow a late project?",
     "invoke_when": "When someone proposes adding people to recover a slipping schedule.",
     "tags": ["software", "management"],
     "index_pointers": [
       {"section": "Estimation", "question": "Does more staff mean faster delivery?", "role": "primary"}
     ]
   }
   ```

   A good result names the Dewey number `005.1`.

2. **Fetch it back** with `library_get`, argument `{"dewey": "005.1"}`. The
   result should be the card you just added, with the author `Brooks` and the
   year `1975`.

3. **List the sections** with `library_list_sections`, argument `{}`. The
   result should include `Estimation`.

If all three behave as described, the server works. Report success, the Go
version you used, and anything that surprised you.
