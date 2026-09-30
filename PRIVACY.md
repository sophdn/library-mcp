# Privacy Policy

_Last updated: 2026-09-30_

library-mcp is a Model Context Protocol server that runs on your own machine.
This policy explains what it does with data. The short version: it collects
nothing, sends nothing, and keeps everything on your computer.

## What library-mcp does with your data

library-mcp stores the library cards you or your agent create. A card holds the
text you give it: a citation, a Dewey number, notes, tags, and index pointers.
It writes these to a single SQLite database file on your computer, at the path
you choose with the `-db` flag or the `LIBRARY_MCP_DB` environment variable.
That file is the only place library-mcp keeps data.

## What it does not do

- **No network.** library-mcp communicates only over local standard input and
  output with the program that launched it. It has no network transport, opens
  no ports, and makes no outbound connections.
- **No telemetry or analytics.** It does not measure, log to a remote service,
  or report usage.
- **No third parties.** It sends your data to no one. There are no external
  services, accounts, or integrations.
- **No access by the author.** The maintainer of library-mcp receives none of
  your data and has no access to your database file.

## Your control over your data

Your data lives in your SQLite file, under your control. You can read it, back
it up, move it, or delete it with ordinary file tools. Deleting the file erases
every card. Retiring or updating a card through the tools changes only your
local file.

## Data from your agent

An AI agent connected to library-mcp may read files in your projects and file
what it finds as cards. That is the agent's behavior, under your direction.
Whatever it files still goes only into your local database and stays there.

## Changes to this policy

If this policy changes, the update appears in this file in the repository, with
a new date above.

## Contact

For any privacy question, open an issue at
<https://github.com/sophdn/library-mcp/issues>.
