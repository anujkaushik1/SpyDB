# SpyDB

A fast, lightweight Postgres desktop client for macOS and Ubuntu, built with Go ([Wails v3](https://v3.wails.io/)) and React. A 21 MB app that uses about 110 MB of memory.

## Features

- **Connect** from a form or a pasted `postgres://` URL. Test first, save connections by name, and reconnect from the 10 most recent. Errors are in plain language ("Nothing is accepting connections on localhost:5433").
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
- **Saved queries** in a left rail, shared by all connections.
- **Active connections:** everyone connected to the server, with their state and current or last query.

## How it works

```
Window (React, in the OS web view)  ⇄  Go (DBService)  ⇄  Postgres (pgx)
```

- **Your SQL runs on one held "editor" connection**, a real Postgres session. Everything else (sidebar, table tabs, saves, exports) borrows from a pool. So browsing works while a query runs, and a save never lands inside a transaction you left open.
- **Big results stay cheap.** Go keeps up to 100,000 rows per statement and sends the window 1,000 at a time. Next pages come from memory, with no new query. Table tabs fetch one page at a time.
- **No table scans for row counts.** The sidebar's `~20k` comes from Postgres's statistics, in one query for all tables.
- **Editing finds rows safely.** Rows are matched by primary key, adding it as a hidden column if needed. Tables without a key use each row's address plus a fingerprint of the whole row. Every `UPDATE` must match exactly one row still holding its old values, or nothing is saved.

**What's stored on disk:** only saved connections (passwords included) and saved queries. They're JSON files in `~/Library/Application Support/SpyDB/` (macOS) or `~/.config/SpyDB/` (Ubuntu), readable only by your user. Open tabs, results and unsaved edits end when the app quits.

## Benchmarks vs Beekeeper Studio

Measured on 9 Nov 2026 against Beekeeper Studio 5.2.12 (Electron 31, Node 20). Setup: Apple M4 (10 cores, 16 GB), macOS 26.5.1, local Postgres 15.15, a 1M-row table among 257 tables.

**How:** each app's real database code ran the same jobs:
- SpyDB: its `DBService` methods.
- Beekeeper: its own bundled database process (`dist/utility.js`), run on its own Electron binary and sent the same requests its window sends.

Times are medians of 7 runs after a warm-up, and include handing the data to the window (JSON for SpyDB, Electron's structured clone for Beekeeper). Drawing the grid on screen isn't measured. App figures come from launching each app 3 times in the background.

### The apps

| | SpyDB | Beekeeper | |
|---|---:|---:|---|
| Install size | 21 MB | 778 MB | SpyDB 36× smaller |
| Window on screen after launch | 0.46 s | 1.29 s | SpyDB 2.8× faster |
| CPU used while launching | 0.76 s | 2.55 s | SpyDB 3.3× less |
| Memory after launch, all processes | 112 MB | 532 MB | SpyDB 4.8× less |
| CPU while idle | 0.12 % | 3.47 % | SpyDB 29× less |

### Speed

| Job | SpyDB | Beekeeper | |
|---|---:|---:|---|
| Connect + load the sidebar (257 tables) | 11.5 ms | 57.3 ms | SpyDB 5.0× faster |
| Open a 1M-row table (first 100 rows) | 4.2 ms | 6.7 ms | SpyDB 1.6× faster |
| Jump to page 5,000 | 17.4 ms | 18.7 ms | even |
| Save one edited cell | 2.8 ms | 1.6 ms | Beekeeper 1.8× faster |
| Query returning 1,000 rows | 4.8 ms | 2.8 ms | Beekeeper 1.7× faster |
| Query returning 10,000 rows | 11.3 ms | 14.4 ms | SpyDB 1.3× faster |
| Query returning 100,000 rows | 45.7 ms | 146 ms | SpyDB 3.2× faster |
| Query returning 1,000,000 rows | 0.40 s | 1.95 s | SpyDB 4.9× faster |

### Resources per job (SpyDB / Beekeeper)

| Job | CPU time | Peak memory | Data sent to the window | Round trips to Postgres |
|---|---:|---:|---:|---:|
| Connect + sidebar | 3.6 / 19.5 ms | 28 / 258 MB | 52 / 35 KB | 9 / 10 |
| Open a 1M-row table | 0.5 / 5.2 ms | 26 / 258 MB | 13 / 17 KB | 3 / 4 |
| Jump to page 5,000 | 0.8 / 3.4 ms | 26 / 257 MB | 14 / 16 KB | 3 / 1 |
| Save one cell | 0.5 / 1.3 ms | 26 / 257 MB | 53 / 171 B | 6 / 6 |
| Query, 1,000 rows | 2.1 / 3.5 ms | 28 / 260 MB | 129 / 136 KB | 5 / 2 |
| Query, 10,000 rows | 11 / 20 ms | 35 / 299 MB | 129 KB / 1.3 MB | 5 / 2 |
| Query, 100,000 rows | 50 / 188 ms | 94 / 719 MB | 129 KB / 13 MB | 5 / 2 |
| Query, 1,000,000 rows | 0.20 / 3.18 s | 95 / 2,210 MB | 129 KB / 135 MB | 6 / 2 |

Peak memory is the measuring process's peak. That's Go's own ~20 MB baseline for SpyDB, and ~254 MB for Beekeeper's database process before any work. Round trips were counted by a relay between each app and Postgres, in a separate pass.

### Why

- **Big results:** SpyDB sends the window at most 1,000 rows and keeps at most 100,000. Beekeeper sends every row to its window, then keeps 50,000. That's the gap in time, memory and data at 100k and 1M rows.
- **Small queries and saves:** after each query SpyDB makes three small lookups so results can be edited (5 round trips against 2). Its saves also run safety checks. That costs 1–2 ms locally, and more on a remote database, where every round trip is a network delay.
- **Paging** in a table tab takes 3 round trips against Beekeeper's 1, because SpyDB re-reads the table's columns and key for each page.
- **The app itself:** SpyDB uses the system web view. Beekeeper bundles its own Chromium and Node and runs 5 processes.

## Requirements

- Go 1.26+, Node.js 22.12+
- Wails CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`
- On macOS, cgo must use Apple clang: if your shell sets `CC` to GNU gcc, prefix commands with `CC=clang CXX=clang++`.

## Develop, test, build

```sh
wails3 dev                                # run with hot reload
cd frontend && npm test                   # window unit tests
SPYDB_TEST_DATABASE=mydb go test ./...    # Go tests against a local Postgres (they create and drop their own schemas)
wails3 build                              # binary in bin/
wails3 task package                       # macOS app bundle in bin/spydb.app
```

To pre-fill the connect form while developing, copy `db.config.example.json` to `db.config.json` (gitignored, since it can hold a password).

## Ubuntu

SpyDB runs on Ubuntu 22.04 and newer. Build with Docker (works from a Mac too):

```sh
build/linux/build-docker.sh             # Ubuntu 22.04+ (GTK3), amd64
build/linux/build-docker.sh gtk4        # Ubuntu 24.04+ (GTK4)
build/linux/build-docker.sh gtk3 arm64  # ARM machines
```

Then install the runtime libraries on the Ubuntu machine and run `./spydb`:

| Build | Ubuntu 22.04 | Ubuntu 24.04 |
|---|---|---|
| `gtk3` (default) | `sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0` | `sudo apt install libgtk-3-0t64 libwebkit2gtk-4.1-0` |
| `gtk4` | not supported | `sudo apt install libgtk-4-1 libwebkitgtk-6.0-4` |

To build on Ubuntu itself, install `gcc pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev` (or `libgtk-4-dev libwebkitgtk-6.0-dev`), then:

```sh
cd frontend && npm ci && npm run build && cd ..
go build -tags production,gtk3 -trimpath -buildvcs=false -ldflags="-w -s" -o bin/spydb .   # drop ",gtk3" for GTK4
```

If the GTK4 build fails with `bwrap: … Operation not permitted`, the system is blocking WebKit's sandbox; use the GTK3 build.

## Project layout

| Go (backend) | |
|---|---|
| `dbservice.go` | `DBService`: connect, disconnect, the editor connection and pool, active connections |
| `query.go` | Runs SQL, keeps results for paging |
| `editable.go`, `edits.go` | Decides which results are editable; saves edits safely |
| `tabledata.go`, `schema.go` | Table pages; the sidebar's schemas, tables and columns |
| `export.go` | CSV and Excel export |
| `connections.go`, `queries.go`, `files.go` | Saved connections and queries on disk |
| `dbconfig.go`, `dberrors.go` | Connection settings; plain-language errors |

| React (window), in `frontend/src/` | |
|---|---|
| `App.tsx` | Connections, tabs per connection, shortcuts |
| `SqlEditor.tsx`, `SqlCodeEditor.tsx`, `sqlCompletion.ts` | The SQL editor and autocomplete |
| `DataGrid.tsx` | The grid: selection, editing, saving, copy |
| `TableTab.tsx`, `QueryTab.tsx` | Table and query tabs |
| `SchemaSidebar.tsx`, `DatabasePicker.tsx`, `TablePalette.tsx` | Sidebar, switcher, ⌘P |
| `ConnectionForm.tsx`, `ConnectionsSidebar.tsx` | The connect screen |
| `JsonEditorModal.tsx`, `ExportMenu.tsx`, `ServerConnectionsPanel.tsx` | JSON editor, export, active connections |
