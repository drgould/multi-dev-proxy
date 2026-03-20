# CLAUDE.md

## Project

`mdp` — a cross-platform Go CLI that runs a reverse proxy in front of multiple dev servers across branches and repos. Binary name is `mdp`.

## Build & Test

```sh
go build ./...          # build all packages
go test ./...           # run all tests
go test -race ./...     # run with race detector
go vet ./...            # static analysis
```

Binary: `go build -o mdp ./cmd/mdp`

### E2E tests

Require the testbed running in a separate terminal:

```sh
cd testbed && ./run.sh   # start proxy + all demo servers
npm run test:e2e         # run Playwright tests (separate terminal)
```

## Architecture

```
cmd/mdp/           CLI entrypoints (start, run, register)
internal/
  api/             HTTP handlers for /__mdp/* control endpoints
  certs/           TLS cert generation (mkcert preferred, self-signed fallback)
  detect/          Git repo/branch detection, stdout port detection
  inject/          HTML response injection (<script> tag for widget)
  ports/           Free port allocation within a configurable range
  process/         Process group management, signal handling (build-tagged unix/windows)
  proxy/           httputil.ReverseProxy wrapper with cookie-based routing
  registry/        In-memory server registry with RWMutex, dead server pruner
  routing/         Cookie parsing, upstream resolution
  ui/              Switch page HTML renderer; widget.js (go:embed) Shadow DOM script
```

## Key Conventions

- **No external test frameworks** — stdlib `testing` only
- **No external logging** — `log/slog` only
- **No global mutable state** — dependency injection via structs
- **Build tags** for platform code: `//go:build unix`, `//go:build windows` (not runtime.GOOS switches)
- **`httputil.ReverseProxy.Rewrite`** — never use the deprecated `Director` field
- **Connect upstreams via `localhost`** — not hardcoded IP addresses
- **Shadow DOM** for the injected widget — prevents CSS leaking in either direction
- Cookie name: `__mdp_upstream`, API prefix: `/__mdp/`
- Server names use `repo/branch` format
- PID is optional when registering servers (for externally managed processes like Docker)

## Dependencies

Only two direct dependencies — keep it minimal:

- `github.com/spf13/cobra` — CLI framework
- `github.com/andybalholm/brotli` — brotli decompression for HTML injection
