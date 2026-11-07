# SpyDB

Postgres desktop client built with [Wails v3](https://v3.wails.io/) (Go backend, React + TypeScript frontend).

Fill in host, port, user, password and database, then:

- **Test** — `DBService.TestConnection` opens a connection with [pgx](https://github.com/jackc/pgx), runs `SELECT 1` and closes it.
- **Connect** — `DBService.Connect` opens a `pgxpool` pool and keeps a connection open until **Disconnect** (or the app quits).
- **Active connections** — `DBService.ActiveConnections` lists the server's client connections from `pg_stat_activity`, through the open connection, or a temporary one (like **Test**) when not connected. SpyDB's own connections are tagged by their `application_name`, `SpyDB`.

Failures show a plain-language summary, with pgx's raw error under **Details**.

## Query editor

While connected, write SQL and press **Run** (⌘↵). `DBService.RunQuery` runs it on one connection that Connect reserves for the editor, so `SET`, temp tables and open transactions carry over between runs.

- Several statements per run (`BEGIN; UPDATE …; COMMIT;`); each gets its own result.
- With text selected, only the selection runs.
- Results show 1,000 rows per page, with **‹ Previous** / **Next ›** to page through them. Up to 100,000 rows per statement are kept in memory for paging (`DBService.FetchRows`, no re-run); the row count is always exact.
- **Cancel** (Esc) asks Postgres to stop the query; the connection stays open.
- Errors show Postgres's message, hint and line/column, and move the cursor there.

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
- `query.go` — `RunQuery`: runs SQL on the editor connection
- `frontend/src/App.tsx` — window shell (title bar)
- `frontend/src/ConnectionForm.tsx` — connection form (a slim bar while connected)
- `frontend/src/QueryEditor.tsx` — SQL editor and results
- `frontend/src/ServerConnectionsPanel.tsx` — active connections list
- `frontend/bindings/` — generated TypeScript bindings for Go services
