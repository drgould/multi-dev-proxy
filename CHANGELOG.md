# Changelog

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
- Playwright e2e tests covering proxy routing, switch page, widget injection, and all server reachability
