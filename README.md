# SpyDB

Postgres desktop client built with [Wails v3](https://v3.wails.io/) (Go backend, React + TypeScript frontend).

Fill in host, port, user, password and database, then:

- **Test** — `DBService.TestConnection` opens a connection with [pgx](https://github.com/jackc/pgx), runs `SELECT 1` and closes it.
- **Connect** — `DBService.Connect` opens a `pgxpool` pool and keeps a connection open until **Disconnect** (or the app quits).
- **Active connections** — `DBService.ActiveConnections` lists the server's client connections from `pg_stat_activity`, through the open connection, or a temporary one (like **Test**) when not connected. SpyDB's own connections are tagged by their `application_name`, `SpyDB`.

Failures show a plain-language summary, with pgx's raw error under **Details**.

## Workspace

Once connected, SpyDB shows a sidebar and tabs.

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

Table rows, and query results that come straight from one table with its primary key, can be edited in place:

- Click a cell to select it; double-click, press Enter, or just type to edit. Enter moves down, Tab moves sideways, Esc cancels, **NULL** sets NULL.
- Edited cells are tinted and wait in a bar at the bottom: **Discard** or **Save changes** (⌘S). The tab shows a dot while edits are unsaved.
- `DBService.SaveRows` sends them as `UPDATE … WHERE <primary key>` in one transaction on its own connection: either every row saves or none does. A bad value (e.g. text in a number column) shows Postgres's error and keeps your edits.
- Views, tables without a primary key, joins and computed columns are read-only.

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

## Build

```sh
wails3 build
```

The binary is written to `bin/`.

## Layout

- `main.go` — app and window setup
- `dbservice.go` — `DBService`, the Go service exposed to the frontend
- `dbconfig.go` — `DBConfig`, form defaults from `db.config.json`, and the connection string
- `dberrors.go` — turns pgx errors into plain-language summaries
- `query.go` — `RunQuery` / `FetchRows`: runs SQL on the editor connection and pages results
- `schema.go` — `ListTables`: schemas with their tables and views
- `tabledata.go` — `TableData`: one page of a table, sorted and filtered
- `edits.go` — `SaveRows` (cell edits → UPDATEs) and which query results are editable
- `frontend/src/App.tsx` — window shell (title bar)
- `frontend/src/ConnectionForm.tsx` — connection form (shown while disconnected)
- `frontend/src/SqlEditor.tsx` — the SQL editor (in table tabs and Query tabs)
- `frontend/src/DataGrid.tsx` — the grid: colours, selection, cell editing, Save bar
- `frontend/src/QueryTab.tsx` — a Ctrl+T Query tab
- `frontend/src/SchemaSidebar.tsx` — schema / table sidebar and connection strip
- `frontend/src/TabBar.tsx`, `TableTab.tsx`, `TablePalette.tsx` — tabs, the table grid (and its </> toggle), the Ctrl+P switcher
- `frontend/src/ServerConnectionsPanel.tsx` — active connections list
- `frontend/bindings/` — generated TypeScript bindings for Go services
