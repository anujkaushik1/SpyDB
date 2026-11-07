# SpyDB

Postgres desktop client built with [Wails v3](https://v3.wails.io/) (Go backend, React + TypeScript frontend).

Fill in host, port, user, password and database, then:

- **Test** — `DBService.TestConnection` opens a connection with [pgx](https://github.com/jackc/pgx), runs `SELECT 1` and closes it.
- **Connect** — `DBService.Connect` opens a `pgxpool` pool and keeps a connection open until **Disconnect** (or the app quits).
- **Active connections** — `DBService.ActiveConnections` lists the server's client connections from `pg_stat_activity`, through the open connection, or a temporary one (like **Test**) when not connected. SpyDB's own connections are tagged by their `application_name`, `SpyDB`.

Failures show a plain-language summary, with pgx's raw error under **Details**.

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
- `frontend/src/App.tsx` — window shell (title bar)
- `frontend/src/ConnectionForm.tsx` — connection form
- `frontend/bindings/` — generated TypeScript bindings for Go services
