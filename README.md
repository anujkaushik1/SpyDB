# SpyDB

Postgres desktop client built with [Wails v3](https://v3.wails.io/) (Go backend, React + TypeScript frontend).

Click **Test connection** and the Go `DBService.TestConnection` method connects with [pgx](https://github.com/jackc/pgx) and runs `SELECT 1`.

## Database config

Connection settings live in `db.config.json` in the project root. It is gitignored because it holds the password, so create it from the example:

```sh
cp db.config.example.json db.config.json
```

Then set `host`, `port`, `user`, `password` and `database`. The file is re-read on every test, so edits apply without restarting the app.

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
- `dbconfig.go` — loads `db.config.json` and builds the connection string
- `frontend/src/App.tsx` — UI
- `frontend/bindings/` — generated TypeScript bindings for Go services
