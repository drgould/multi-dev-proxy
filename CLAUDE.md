# CLAUDE.md

## Project

`mdp` — a cross-platform Go CLI that runs an orchestrator managing multiple reverse proxies in front of dev servers across branches and repos. Binary name is `mdp`.

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
npm run test:e2e         # run Vitest + Puppeteer tests (separate terminal)
```

## Architecture

```
cmd/mdp/              CLI entrypoints (root orchestrator, run, register, deregister, switch, status, logs)
internal/
  api/                HTTP handlers for /__mdp/* per-proxy endpoints + CORS + config
  certs/              TLS cert generation helpers (unused — proxy inherits certs from services)
  config/             mdp.yaml parser (service definitions, env vars, port mappings)
  detect/             Git repo/branch detection, stdout port detection
  inject/             HTML response injection (<script> tag for widget)
  orchestrator/       Core orchestrator: proxy instance management, service runner, control API, groups
  ports/              Free port allocation within a configurable range
  process/            Process group management, signal handling (build-tagged unix/windows)
  proxy/              httputil.ReverseProxy wrapper with cookie-based routing
  registry/           In-memory server registry with RWMutex, dead server pruner, default upstream
  routing/            Cookie parsing, upstream resolution (cookie → default → redirect)
  tui/                Bubbletea TUI: groups + proxies/services, keyboard nav, live updates
  ui/                 Switch page HTML renderer; widget.js (go:embed) Shadow DOM script
```

## Key Conventions

- **No external test frameworks** — stdlib `testing` only
- **No external logging** — `log/slog` only
- **No global mutable state** — dependency injection via structs
- **Build tags** for platform code: `//go:build unix`, `//go:build windows` (not runtime.GOOS switches)
- **`httputil.ReverseProxy.Rewrite`** — never use the deprecated `Director` field
- **Connect upstreams via `localhost`** — not hardcoded IP addresses
- **Shadow DOM** for the injected widget — prevents CSS leaking in either direction
- Cookie name: `__mdp_upstream_<port>` (port-specific to avoid collisions), API prefix: `/__mdp/`
- Server names use `repo/branch` format
- PID is optional when registering servers (for externally managed processes like Docker)
- Control API on port 13100 (configurable via `--control-port`)
- Service groups derived dynamically from registered services' group fields (typically git branch)

## Releases

Automated via [release-please](https://github.com/googleapis/release-please). Commits to `main` must use **conventional commit** prefixes:

- `feat:` → minor bump, `fix:` → patch bump, `feat!:` / `fix!:` → major bump
- `docs:`, `test:`, `chore:`, `ci:` → no release, hidden from changelog

On push to `main`, release-please creates/updates a Release PR. Merging it creates a GitHub Release + `v*` tag, which triggers goreleaser to build and publish binaries.

## Dependencies

- `github.com/spf13/cobra` — CLI framework
- `github.com/andybalholm/brotli` — brotli decompression for HTML injection
- `gopkg.in/yaml.v3` — YAML config file parsing
- `github.com/charmbracelet/bubbletea` — TUI framework
- `github.com/charmbracelet/lipgloss` — TUI styling
