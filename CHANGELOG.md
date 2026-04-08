# Changelog

## v1.1.1

- Fix macOS Gatekeeper warnings for installed binary

## v1.1.0

- Add /release slash command
- Smart HTTP/HTTPS proxy with per-service TLS and last-path tracking
- Dynamic TLS cert inheritance from services
- Fix bugs and hardening from code review
- Add comprehensive test coverage across packages
- Skip port override in solo mode, rename runSoloWithHealth to runProxied

## v1.0.1

### Changed

- **Group switcher hidden with single proxy** — the groups UI (widget pill, switch page, TUI, `mdp status`) is now hidden when there is only one proxy, since groups are only useful for coordinating multiple proxies
- **`MDP=1` env var** — proxied child processes receive `MDP=1` in their environment so build tooling can detect it and adjust config dynamically
- **Registration errors are fatal** — `mdp run` now exits immediately if service registration with the orchestrator fails, instead of silently continuing
- **Health watchdog** — services started via `mdp run` automatically shut down when the orchestrator/proxy becomes unreachable

## v1.0.0

### Features

- **Orchestrator** — new `mdp.yaml` config drives multi-proxy setups with named groups, sibling awareness, and coordinated group switching across proxies
- **Daemon mode** — `mdp start` daemonizes the process; `mdp status`, `mdp logs`, and `mdp switch` control it from separate terminals
- **Interactive TUI** — live dashboard with tabs (Groups, Proxies, Services), mouse support, hover highlights, clickable rows, and keyboard navigation
- **Group switching** — switch all proxies to a named group (e.g. `dev`, `staging`) from the TUI, widget pill, switch page, or `mdp switch` CLI command
- **Switch page sibling support** — the `/__mdp/switch` page now lists servers from sibling proxies with direct switch buttons

### Changed

- **Widget pill group switching** — correctly sets the browser cookie after switching groups so the page reloads to the right upstream
- **Switch page group switching** — same cookie fix; navigates to `/` after switching instead of staying on the switch page
- **Switch handler** — redirects to `/` after switching instead of back to `/__mdp/switch`
- **E2E tests** migrated from Playwright to Puppeteer + Vitest; run headed locally, headless in CI, serial execution

### New commands

- `mdp start` — start proxy in daemon mode
- `mdp status` — show daemon status
- `mdp logs` — tail daemon logs
- `mdp switch <group>` — switch active group from CLI
- `mdp deregister` — remove a registered server

## v0.1.1

### Changed

- **Widget pill** shows **repo · branch** (branch names with slashes preserved), not branch alone
- **Widget script** lives in `internal/ui/widget.js` and is embedded at build time with `go:embed`
- **README** — widget behavior and install paths aligned with current Homebrew/Scoop layout

## v0.1.0

Initial release.

### Features

- **Reverse proxy** on a single stable port (default `:3000`) with cookie-based routing between multiple upstream dev servers
- **`mdp start`** — runs the proxy with control API, switch page, and injected widget
- **`mdp run <cmd>`** — wraps any dev command, auto-assigns a port via `PORT` env, registers with the proxy; falls back to solo mode if no proxy is running
- **`mdp register`** — manually registers an already-running service (useful for Docker, external processes)
- **Floating widget** injected into every HTML response via Shadow DOM — switch branches without leaving the page
- **Switch page** at `/__mdp/switch` with light/dark/auto theme toggle
- **HTML injection** — decompresses gzip/brotli responses, injects `<script>` tag before `</body>`, updates Content-Length, strips CSP headers that would block it
- **WebSocket proxying** with header casing fix for Vite HMR compatibility
- **HTTPS by default** — auto-generates TLS certs using mkcert (if installed) or self-signed fallback with system trust store integration
- **Multi-repo support** — server names use `repo/branch` format, auto-detected from git remote; widget and switch page group by repo
- **Dead server pruning** — checks registered PIDs every 10 seconds, removes dead servers automatically
- **Process group management** — spawns child processes in their own process group (`Setpgid`) for clean teardown on exit
- **PID-optional registration** — servers without a PID (e.g. Docker containers) are accepted and skip liveness pruning
- **Port detection from stdout** — parses `http://localhost:<port>` from child process output to handle frameworks that ignore `PORT`
- **Location header rewriting** — rewrites upstream `Location` headers (including `127.0.0.1` and `[::1]` variants) to point back through the proxy

### Distribution

- **GoReleaser** — cross-compiled binaries for macOS, Linux, Windows (amd64 + arm64)
- **Homebrew** — `brew install derekgould/mdp/mdp`
- **npm** — `npm install -g mdp`
- **curl installer** — `curl -fsSL https://raw.githubusercontent.com/derekgould/multi-dev-proxy/main/install.sh | sh`
- **Scoop** (Windows) — `scoop install mdp`

### Testbed

- 6 demo servers: Go (WebSocket), Vite + TypeScript, Next.js, Vue 3, SvelteKit, Docker (nginx + Go API + Postgres)
- Playwright E2E tests covering proxy routing, switch page, widget injection, and all server reachability
