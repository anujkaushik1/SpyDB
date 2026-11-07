# SpyDB

Desktop app built with [Wails v3](https://v3.wails.io/) (Go backend, React + TypeScript frontend).

Type a name, click **Greet**, and the Go `GreetService.Greet` method replies `Hello <name>`.

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
- `greetservice.go` — Go service exposed to the frontend
- `frontend/src/App.tsx` — UI
- `frontend/bindings/` — generated TypeScript bindings for Go services
