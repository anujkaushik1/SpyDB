# SpyDB

Postgres desktop client built with [Wails v3](https://v3.wails.io/) (Go backend, React + TypeScript frontend).

Fill in host, port, user, password and database, then:

- **Test** — `DBService.TestConnection` opens a connection with [pgx](https://github.com/jackc/pgx), runs `SELECT 1` and closes it.
- **Connect** — `DBService.Connect` opens a `pgxpool` pool and keeps a connection open until **Disconnect** (or the app quits).
- **Active connections** — `DBService.ActiveConnections` lists the server's client connections from `pg_stat_activity`, through the open connection, or a temporary one (like **Test**) when not connected. SpyDB's own connections are tagged by their `application_name`, `SpyDB`.

Failures show a plain-language summary, with pgx's raw error under **Details**.

## Saved connections

The connect screen lists **Saved** connections (ones you name with **Save**) and **Recent** ones (every connection you make, newest first). Click one to load it into the form, double-click to connect; saved ones can be renamed (**Save changes**) or deleted, and Recent can be cleared.

They live in `~/Library/Application Support/SpyDB/connections.json` on macOS (`~/.config/SpyDB/connections.json` on Linux), **including passwords in plain text**, in a folder and file only your user can read (`700` / `600`).

**Import from URL** fills the form from a `postgres://` or `postgresql://` URL (pasting one imports it straight away). Passwords with `@`, `:` or `/` work; parameters that aren't Postgres connection settings (e.g. `pool_size`, `TrustServerCertificate`) are skipped and listed.

## Workspace

Once connected, SpyDB shows a sidebar and tabs.

**Database picker** — the menu at the top of the sidebar lists the server's databases; picking one reconnects with the same login (`DBService.SwitchDatabase`) and closes open tabs, asking first if any have unsaved edits. A failed switch keeps the current connection.

**Sidebar** — every schema (public first; system schemas hidden) with its tables, views and materialized views from `DBService.ListTables`, with estimated row counts from Postgres's statistics. Filter by name (Esc clears), collapse schemas, refresh. The strip at the bottom shows the connection, opens **Active connections** and **Disconnects**.

**Tabs** — clicking a table (or picking it with **Ctrl+P / ⌘P**) opens it in its own tab, or switches to it if open. **Ctrl+T / ⌘T** opens a Query tab: a SQL editor with its results below. Tabs keep their state when you switch; close with × or middle-click. With no tab open, the main area says how to open one.

**Table tabs** — `DBService.TableData` loads 100 rows per page, ordered by the primary key, with:

- Click a column header to sort (ascending → descending → off); Postgres sorts the whole table.
- Filter bar: column · operator (equals, not equal, contains, starts with, >, ≥, <, ≤, is null, is not null) · value. Add rows with +; they combine with AND.
- **</>** swaps the filter bar for a SQL editor (see below); the table's rows stay underneath. Click it again for the filters; the rows reload in case your SQL changed them.
- Colour by type in soft, cool tones: numbers, dates/times, booleans, JSON and NULL each have their own colour; the primary key has a key icon.
- Column names come only from the catalog and are quoted; filter values are sent as parameters.

## SQL in a table tab

Click **</>** in a table tab to write SQL: the filter bar becomes an editor pre-filled with `SELECT * FROM schema.table;`, and the table's rows stay below it. Edit it into any query and press **Run** (⌘↵). A query that returns rows shows them in the grid in place of the table's (**Show table rows** brings those back); errors and commands like `UPDATE` show a strip above the table's rows, which reload. `DBService.RunQuery` runs it on one connection that Connect reserves for SQL, so `SET`, temp tables and open transactions carry over between runs and across tabs (queries from different tabs take turns). Each tab keeps its own SQL and results.

- Several statements per run (`BEGIN; UPDATE …; COMMIT;`); each gets its own result.
- With text selected, only the selection runs.
- Results show 1,000 rows per page, with **‹** / **›** to page. Up to 100,000 rows per statement are kept in memory for paging (`DBService.FetchRows`, no re-run); the row count is always exact.
- **Cancel** (Esc) asks Postgres to stop the query; the connection stays open.
- Errors show Postgres's message, hint and line/column, and move the cursor there.

## Editing cells

Table rows, and query results of a plain `SELECT` from one table, can be edited in place, even when the primary key isn't among the columns:

- Click a cell to select it; double-click, press Enter, or just type to edit. Enter moves down, Tab moves sideways, Esc cancels, **NULL** sets NULL.
- Edited cells are tinted and wait in a bar at the bottom: **Discard** or **Save changes** (⌘S). The tab shows a dot while edits are unsaved.
- Each row is found again by its primary key. If the query doesn't select it, `RunQuery` adds it as hidden columns (never shown, copied or exported). A table without a primary key uses each row's `ctid` (its address) plus an MD5 of the whole row, so an edit never lands on a different row, even one that later reuses the address.
- `DBService.SaveRows` sends them as `UPDATE … WHERE <that row> AND <each edited cell still holds its old value>` in one transaction on its own connection: either every row saves or none does. If someone changed or deleted the row since it loaded, nothing is saved and the bar says so. A bad value (e.g. text in a number column) shows Postgres's error and keeps your edits.
- Saves run as the query tab's role (after `SET ROLE`) with its `search_path` and date/number settings, so values read the same way and privileges apply as in the tab. A date or time shown in a form Postgres reads back differently (e.g. `IST`, which it reads as Israel time, under `DateStyle = SQL`) is refused rather than saved wrong; `SET DateStyle = ISO` (the default) avoids that.
- Read-only, with the reason in the footer: joins (self-joins too), `GROUP BY`, `DISTINCT`, `UNION`, `WITH`, subqueries in `FROM`, several statements in one run, views, system catalogs, temporary tables, tables other tables inherit from, tables your role can't update, rows that repeat (e.g. `unnest` in the columns), and computed columns. Inside an open transaction only results that show the whole primary key are editable.

## Export

**Export** in a grid's footer saves rows as **CSV** or **Excel (.xlsx)** through the system Save dialog:

- Query results: **These N rows** — exactly what the query returned (from memory; a result bigger than 100,000 rows runs again in a read-only transaction and streams into the file, so a query that changes data is never run twice). **Full table** when the result comes from one table.
- Table tabs: **Full table**, and **Filtered rows** (every page) when filters are on, in the order shown.
- Rows stream from Postgres into the file, so big tables don't fill memory. Excel stores numbers and booleans as real values (numbers longer than 15 digits stay text) and is limited to 1,048,576 rows per sheet.

## Pre-filling the form (optional)

If `db.config.json` exists in the project root, the form starts with its values. It is gitignored because it can hold a password; create it from the example:

```sh
cp db.config.example.json db.config.json
```

Without it, the form starts at `localhost:5432`.

## Prerequisites

- Go 1.26+
- Node.js + npm
- Wails CLI: `go install github.com/wailsapp/wails/v3/cmd/wails3@latest`

On macOS, cgo must use Apple clang. If your shell sets `CC` to GNU gcc, prefix commands with `CC=clang CXX=clang++`.

## Develop

```sh
wails3 dev
```

Unit tests (Node's built-in test runner, no extra packages):

```sh
cd frontend && npm test
```

## Build

```sh
wails3 build
```

The binary is written to `bin/`.

## Ubuntu (Linux)

SpyDB runs on Ubuntu 22.04 and newer. Build it with Docker, which works from a Mac too:

```sh
build/linux/build-docker.sh             # Ubuntu 22.04+ (GTK3), amd64 → bin/linux/ubuntu-22.04-gtk3-amd64/spydb
build/linux/build-docker.sh gtk4        # Ubuntu 24.04+ (GTK4)       → bin/linux/ubuntu-24.04-gtk4-amd64/spydb
build/linux/build-docker.sh gtk3 arm64  # ARM machines
```

On the Ubuntu machine, install the libraries the build needs (a desktop install usually has them), then run it:

| Build | Ubuntu 22.04 | Ubuntu 24.04 |
|---|---|---|
| `gtk3` (default) | `sudo apt install libgtk-3-0 libwebkit2gtk-4.1-0` | `sudo apt install libgtk-3-0t64 libwebkit2gtk-4.1-0` |
| `gtk4` | not supported (GTK too old) | `sudo apt install libgtk-4-1 libwebkitgtk-6.0-4` |

```sh
chmod +x spydb && ./spydb
```

To build on Ubuntu itself, install Go 1.26+, Node.js 22.12+ and `gcc pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev` (or `libgtk-4-dev libwebkitgtk-6.0-dev` for GTK4), then:

```sh
cd frontend && npm ci && npm run build && cd ..
go build -tags production,gtk3 -trimpath -buildvcs=false -ldflags="-w -s" -o bin/spydb .   # drop ",gtk3" for GTK4
```

Notes:

- The GTK3 build uses Wails' legacy backend, which a future Wails release may remove; Ubuntu 24.04 (GTK4) would then be the minimum.
- The GTK4 build runs WebKit's sandbox (bubblewrap). If it fails to start with a `bwrap: … Operation not permitted` error, the system is blocking it; use the GTK3 build.
- On Linux the window keeps the system title bar, and shortcut hints say Ctrl instead of ⌘.

## Layout

- `main.go` — app and window setup
- `dbservice.go` — `DBService`, the Go service exposed to the frontend
- `dbconfig.go` — `DBConfig`, form defaults from `db.config.json`, and the connection string
- `dberrors.go` — turns pgx errors into plain-language summaries
- `query.go` — `RunQuery` / `FetchRows`: runs SQL on the editor connection and pages results
- `schema.go` — `ListTables`: schemas with their tables and views
- `tabledata.go` — `TableData`: one page of a table, sorted and filtered
- `edits.go` — `SaveRows` (cell edits → UPDATEs) and which query results are editable
- `editable.go` — reads a query's SQL to tell a plain single-table `SELECT` from everything else, and adds hidden key columns
- `export.go` — CSV / Excel export (`ChooseExportFile`, `ExportResult`, `ExportTable`)
- `connections.go` — saved and recent connections (`connections.json`)
- `frontend/src/App.tsx` — window shell (title bar)
- `frontend/src/ConnectionForm.tsx` — connection form (shown while disconnected)
- `frontend/src/SqlEditor.tsx` — the SQL editor (in table tabs and Query tabs)
- `frontend/src/DataGrid.tsx` — the grid: colours, selection, cell editing, Save bar
- `frontend/src/QueryTab.tsx` — a Ctrl+T Query tab
- `frontend/src/ConnectionsSidebar.tsx`, `connectionUrl.ts` — saved / recent connections, Import from URL
- `frontend/src/DatabasePicker.tsx`, `connectionSearch.ts` — the database / connection switcher
- `frontend/src/ExportMenu.tsx` — the Export button and menu
- `frontend/src/SchemaSidebar.tsx` — schema / table sidebar and connection strip
- `frontend/src/TabBar.tsx`, `TableTab.tsx`, `TablePalette.tsx` — tabs, the table grid (and its </> toggle), the Ctrl+P switcher
- `frontend/src/ServerConnectionsPanel.tsx` — active connections list
- `frontend/src/platform.ts` — macOS vs Linux labels (⌘ / Ctrl, Finder / folder)
- `frontend/tests/` — unit tests (`npm test`)
- `build/linux/build-docker.sh`, `Dockerfile.ubuntu` — Ubuntu builds with Docker
- `frontend/bindings/` — generated TypeScript bindings for Go services
