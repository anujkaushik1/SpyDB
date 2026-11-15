# SpyDB

A fast, lightweight desktop client for Postgres and DynamoDB on macOS, Ubuntu and Windows, built with Go ([Wails v3](https://v3.wails.io/)) and React. The whole app is 21 MB.

## Install or update

The same commands install SpyDB the first time and update it later; saved connections and queries are kept. Each [release](https://github.com/anujkaushik1/SpyDB/releases/latest) has the files for all three.

**Mac** (Apple silicon, macOS 12 or newer). Paste in Terminal:

```sh
curl -fsSL https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-mac.sh | sh
```

(Downloaded with a browser instead, the first open needs System Settings → Privacy & Security → Open Anyway.)

**Ubuntu** (22.04 or newer). Paste in Terminal:

```sh
wget -qO- https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-ubuntu.sh | sh
```

Or download the `.deb` from the release page and double-click it.

**Windows** (10 or 11, 64-bit Intel/AMD). Paste in PowerShell:

```powershell
irm https://raw.githubusercontent.com/anujkaushik1/SpyDB/main/install-windows.ps1 | iex
```

It installs for your user only, no administrator needed. (Downloaded with a browser instead, the first open shows "Windows protected your PC": click More info → Run anyway.)

SpyDB checks for a newer release each time it opens and offers to install it. To require an update, raise the platform's minimum in [`updates.json`](updates.json).

## Features

- **Connect** from a form or a pasted `postgres://` URL. Test first, and save connections by name. Errors are in plain language ("Nothing is accepting connections on localhost:5433").
- **Switch** databases or saved connections from the top of the sidebar. Each connection keeps its own tabs.
- **Browse** every schema's tables and views with approximate row counts. Filter by name, or press ⌘P / Ctrl+P to jump to any table.
- **Table tabs:** 100 rows per page, sort by any column, filters (equals, contains, >, is null and 6 more), jump to a page, or write SQL right on the table with `</>`.
- **Query tabs** (⌘T / Ctrl+T):
  - Run everything, the selection, or the statement at the cursor (⌘⇧↵).
  - Several statements can run at once, each with its own result.
  - Autocomplete knows your tables and columns. You can cancel a running query, and errors point at the exact spot in your SQL.
  - All query tabs share one session, so `SET`, `BEGIN … COMMIT` and temp tables carry over.
- **Edit in place:**
  - Works on table rows, and on the results of any plain single-table `SELECT`, even when the primary key isn't selected or the table has none.
  - Saves are checked: one transaction, each row must still hold the values you saw, and the save runs with your session's role and settings.
  - Results that can't map a cell to exactly one row (joins, `GROUP BY`, views…) are read-only, with the reason shown.
- **JSON:** an editor for `json`/`jsonb` cells that keeps big numbers exact, and View as JSON to read or edit whole rows.
- **Copy** rows as JSON, CSV, tab-separated text or Markdown.
- **Export** query results, filtered rows or whole tables to CSV or Excel, streamed straight to the file.
- **Import** a CSV or Excel file into an existing table (columns matched by name) or as a new table (names, types and a primary key suggested from the file). It loads with `COPY` in one transaction: every row goes in, or none does, and a bad row is named ("Row 2, column balance: …").
- **UPDATE shows what it changed:** SpyDB adds `RETURNING *` when that's safe, and the returned rows are editable once committed.
- **Saved queries** in a left rail, shared by all connections.
- **Active connections:** everyone connected to the server, with their state and current or last query.

### DynamoDB

- **Sign in** with an AWS profile from `~/.aws` (keys, SSO or browser sign-in); SpyDB stores no keys.
- **Tables:** 50 items at a time with Load more, sort the loaded items by any column, and filters that use a table or index key when they can (it says which) instead of reading every item.
- **Edit in place:** type in a cell, or edit an item as JSON, where adding a key adds the field (a new column) and deleting one removes it; ✕ in a cell removes a field. Fields keep their DynamoDB type (a number stays a number; sets stay sets). Saves are checked like Postgres's: every item must still hold what you saw, and several items save in one transaction, all or none.
- **Delete** items from the right-click menu, after a confirmation.
- **PartiQL** (DynamoDB's SQL): `</>` in a table tab, or ⌘T / Ctrl+T for a PartiQL tab. `SELECT` reads 50 items at a time and its results from one table are editable; `INSERT`, `UPDATE` and `DELETE` work too.

### The two together

- **One connect screen:** the left sidebar lists every saved connection under its database's heading (PostgreSQL, DynamoDB's AWS profiles), each with its logo. Click one to see it in its form, double-click to connect.
- **The left rail** has a button per database, lit in its colour when that's the one on screen, with a green dot on each that's connected.
- **Switch without disconnecting:** each database stays connected, with its tabs as you left them, while you use another. Postgres and DynamoDB are separate parts of the app that share only the grid, tabs and dialogs: nothing one does reaches another.
- **Dark or light:** the sun / moon button at the bottom of the rail; SpyDB remembers your choice.

## Benchmarks vs Beekeeper Studio

Measured on 9 Nov 2026 against Beekeeper Studio 5.2.12: each app's own database code ran the same jobs on an Apple M4 (16 GB), local Postgres 15, a 1M-row table among 257 tables. Medians of 7 runs. Lower is better.

| | SpyDB | Beekeeper | Better |
|---|---:|---:|---|
| Install size | 21 MB | 778 MB | SpyDB 36× smaller |
| Window on screen after launch | 0.46 s | 1.29 s | SpyDB 2.8× faster |
| CPU used while launching | 0.76 s | 2.55 s | SpyDB 3.3× less |
| Memory after launch, all processes | 112 MB | 532 MB | SpyDB 4.8× less |
| CPU while idle | 0.12 % | 3.47 % | SpyDB 29× less |

### Speed

| Job | SpyDB | Beekeeper | Better |
|---|---:|---:|---|
| Connect + load the sidebar (257 tables) | 11.5 ms | 57.3 ms | SpyDB 5.0× faster |
| Open a 1M-row table (first 100 rows) | 4.2 ms | 6.7 ms | SpyDB 1.6× faster |
| Jump to page 5,000 | 17.4 ms | 18.7 ms | even |
| Save one edited cell | 2.8 ms | 1.6 ms | Beekeeper 1.8× faster |
| Query returning 1,000 rows | 4.8 ms | 2.8 ms | Beekeeper 1.7× faster |
| Query returning 10,000 rows | 11.3 ms | 14.4 ms | SpyDB 1.3× faster |
| Query returning 100,000 rows | 45.7 ms | 146 ms | SpyDB 3.2× faster |
| Query returning 1,000,000 rows | 0.40 s | 1.95 s | SpyDB 4.9× faster |

SpyDB sends the window at most 1,000 rows at a time and uses the system web view; Beekeeper sends every row to its window and bundles its own Chromium and Node. Small queries and saves are a millisecond or two slower in SpyDB because it does extra lookups and checks so results can be edited safely.

## Benchmark: DynamoDB

The real app in headless Chrome against DynamoDB Local (2,000 items), timed from the click to the frame that shows the result, so it's the wait a person sees. Every step is checked against DynamoDB Local itself. Run it with `node e2e/bench.mjs`.

| What a person does | Screen (ms) | DynamoDB Local (ms) | Runs | Checked against DynamoDB Local |
|---|---:|---:|---:|---|
| Sign in and list the tables | 549 | — | 1 | ✓ the table is listed |
| Open a table: its first 50 items on screen | 29 | 4.0 | 5 | ✓ 50 rows shown, each an item in the table with its city |
| Load more: the next 50 items | 38 | — | 5 | ✓ 300 rows, all different, all items in the table |
| Filter by the key (userId = u01000) | 28 | 3.0 | 5 | ✓ the one item, with its amount, read by its key |
| Filter on a field (city = Goa): first page of matches | 33 | 4.0 | 1 | ✓ 400 found of 2000, the same 400 a direct scan finds |
| …and Load more with the filter: the next 50 checked (40 pages, until every item was) | 37 | — | 40 | ✓ every match shown once, none missing |
| Edit a cell and save | 24 | — | 5 | ✓ the 5 edited items hold the new value; the other items are untouched |
| Delete an item | 34 | — | 3 | ✓ the 3 items are gone from the table, 1997 remain |
| PartiQL: SELECT one item by its key | 39 | — | 5 | ✓ the one item comes back |
| Open an item as JSON | 20 | — | 5 | ✓ the JSON matches the item, numbers as numbers |

Measured on 15 Nov 2026, Apple M4 (16 GB), DynamoDB Local in Docker. "DynamoDB Local (ms)" is what the database itself reported; the rest is SpyDB and the grid. Signing in runs the AWS CLI once.

## Requirements

- Go 1.26+, Node.js 22.12+
- Wails CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`
- On macOS, cgo must use Apple clang: if your shell sets `CC` to GNU gcc, prefix commands with `CC=clang CXX=clang++`.

## Develop, test, build

```sh
wails3 dev                                # run with hot reload
cd frontend && npm test                   # window unit tests
SPYDB_TEST_DATABASE=mydb go test ./...    # Go tests against a local Postgres (they create and drop their own schemas)
SPYDB_DYNAMO_LOCAL=http://127.0.0.1:8000 go test ./dynamo/   # DynamoDB writes, against DynamoDB Local (docker run -p 8000:8000 amazon/dynamodb-local)
go build -tags server -o bin/spydb-server . && E2E_PG_USER=me E2E_PG_DB=mydb node e2e/run.mjs   # the app in headless Chrome
E2E_DDB=http://127.0.0.1:8000 node e2e/bench.mjs   # the DynamoDB benchmark above, against DynamoDB Local
wails3 build                              # binary in bin/
wails3 task package                       # macOS app bundle in bin/spydb.app
```

The end-to-end tests click through the real app in headless Chrome against test databases with made-up data; GitHub runs them on Ubuntu 22.04 and 24.04, Windows and macOS on every push.
## Building for Ubuntu and Windows

```sh
build/linux/build-docker.sh             # Ubuntu 22.04+ (GTK3), amd64; "gtk4" for 24.04+, "gtk3 arm64" for ARM
build/windows/package.sh 0.1.9          # bin/SpyDB.exe and its installer (needs makensis)
```

On Ubuntu, SpyDB needs `libgtk-3-0 libwebkit2gtk-4.1-0` (`libgtk-3-0t64` on 24.04). The release workflow builds and checks all three from the Actions tab.
