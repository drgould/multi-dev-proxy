# mdp

Run multiple dev servers on different branches behind a single port.

## The problem

OAuth providers like Google require you to allowlist exact redirect URLs (`http://localhost:3000/callback`). When every branch runs on a different random port, auth breaks — you'd need to register every port with your provider. `mdp` solves this by putting all your dev servers behind a single stable port. One allowlisted URL works for every branch.

Beyond auth, switching branches to test a feature normally means killing your dev server, checking out, waiting for it to restart, and then switching back. `mdp` lets you run each branch as its own server simultaneously and switch between them instantly in the browser — no restart, no port juggling. It works across multiple repos too, so you can proxy your frontend and API branches through the same port.

## How it works

`mdp start` runs a reverse proxy on port 3000. Each branch gets its own dev server on a random port (10000-60000). The proxy reads a cookie (`__mdp_upstream`) to decide which upstream to forward requests to. A small widget is injected into every HTML response via a `<script>` tag, giving you a floating switcher in the corner of the page. No changes to your app needed.

```
browser → :3000 (mdp proxy) → :42301 (main branch)
                             → :38847 (feature/auth branch)
                             → :51203 (fix/bug-123 branch)
```

## Quick start

```sh
# Terminal 1 — start the proxy
mdp start

# Terminal 2 — wrap your dev command on main
mdp run npm run dev

# Terminal 3 — wrap your dev command on a feature branch
mdp run npm run dev

# Open http://localhost:3000 — use the widget to switch branches
```

## Installation

**curl (macOS/Linux):**
```sh
curl -fsSL https://raw.githubusercontent.com/derekgould/multi-dev-proxy/main/install.sh | sh
```

**Homebrew:**
```sh
brew install derekgould/mdp/mdp
```

**Scoop (Windows):**
```powershell
scoop bucket add mdp https://github.com/derekgould/scoop-mdp
scoop install mdp
```

**npm:**
```sh
npm install -g mdp
```

## Usage

### `mdp start`

Starts the proxy server. Run this once; it stays running in the background.

```sh
mdp start
mdp start --port 8080
mdp start --tls-cert cert.pem --tls-key key.pem
```

### `mdp run`

Wraps a dev command. Picks a free port, sets it as `PORT` in the child process environment, and registers the server with the proxy. When the process exits, it deregisters automatically.

```sh
mdp run npm run dev
mdp run yarn dev
mdp run python manage.py runserver
mdp run -- go run ./cmd/server --verbose
```

If no proxy is running, `mdp run` falls back to solo mode and just runs the command directly with no proxy involvement.

### `mdp register`

Manually registers an already-running service. Useful when you can't wrap the start command with `mdp run`.

```sh
# Register a service running on port 4000
mdp register myapp/feature-branch --port 4000

# Register with a PID so the proxy can prune it when the process dies
mdp register myapp/feature-branch --port 4000 --pid 12345

# List all registered services
mdp register --list
```

## How switching works

The proxy injects `/__mdp/widget.js` into every HTML response. The widget renders as a small pill at the top-center of the page showing the current branch name. Clicking it opens a dropdown of all registered servers.

Selecting a server sets the `__mdp_upstream` cookie and reloads the page. The proxy reads that cookie on every request and forwards to the matching upstream. The widget polls `/__mdp/servers` every 5 seconds to stay current.

You can also switch via the full switcher page at `http://localhost:3000/__mdp/switch`.

## Multi-repo support

Server names use the format `repo/branch`. The repo name is auto-detected from `git remote get-url origin`, falling back to the directory name. This means you can run servers from completely different repos through the same proxy and the widget will group them by repo in the dropdown.

```sh
# In ~/code/frontend on branch feature/nav
mdp run npm run dev
# Registers as: frontend/feature/nav

# In ~/code/api on branch main
mdp run go run ./cmd/server
# Registers as: api/main
```

Override the name with `--name` or just the repo with `--repo`:

```sh
mdp run --name myapp/staging npm run dev
mdp run --repo frontend npm run dev
```

## Solo mode

If `mdp start` isn't running when you call `mdp run`, the command runs directly without any proxy involvement. Stdin, stdout, and stderr pass through unchanged. This means you can always use `mdp run` as your dev command wrapper without requiring the proxy to be running.

## HTTPS

Pass certificate and key files to `mdp start`:

```sh
mdp start --tls-cert ./certs/localhost.pem --tls-key ./certs/localhost-key.pem
```

Both flags are required together. You can generate a local cert with [mkcert](https://github.com/FiloSottile/mkcert):

```sh
mkcert localhost
mdp start --tls-cert localhost.pem --tls-key localhost-key.pem
```

## Configuration

**Environment variables:**

| Variable | Description |
|----------|-------------|
| `MDP_PROXY_PORT` | Default proxy port for `mdp run` and `mdp register` (overrides the default of 3000) |

**`mdp start` flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-p, --port` | `3000` | Port to listen on |
| `--host` | `0.0.0.0` | Host to listen on |
| `--tls-cert` | | Path to TLS certificate file |
| `--tls-key` | | Path to TLS key file |
| `--port-range` | `10000-60000` | Port range for spawned services |

**`mdp run` flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-P, --proxy-port` | `3000` | Proxy port to connect to |
| `--repo` | | Repository name override |
| `--name` | | Full server name override (skips auto-detection) |
| `--port-range` | `10000-60000` | Port range for spawned services |

**`mdp register` flags:**

| Flag | Default | Description |
|------|---------|-------------|
| `-p, --port` | | Port the service is running on (required) |
| `--pid` | `0` | Process ID for liveness tracking |
| `-P, --proxy-port` | `3000` | Proxy port to connect to |
| `-l, --list` | | List registered services |

## API reference

All endpoints are served under `/__mdp/` by the proxy.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/__mdp/health` | Health check. Returns `{"ok":true,"servers":N}` |
| `GET` | `/__mdp/servers` | List all servers grouped by repo |
| `POST` | `/__mdp/register` | Register a server. Body: `{"name":"repo/branch","port":N,"pid":N}` |
| `DELETE` | `/__mdp/register/{name}` | Deregister a server by name |
| `POST` | `/__mdp/switch/{name}` | Switch active server. Sets cookie and redirects (302) |
| `GET` | `/__mdp/switch` | Server switcher UI page |
| `GET` | `/__mdp/widget.js` | Floating switcher widget script |

**Cookie:** `__mdp_upstream` — URL-encoded server name (`repo%2Fbranch`). Set by the widget or the switch endpoint.

**Dead server pruning:** The proxy checks registered servers every 10 seconds. Any server whose PID is no longer alive is removed automatically.

## Testbed

The `testbed/` directory contains demo servers across different frameworks to verify the proxy works end-to-end:

| Server | Framework | Features |
|--------|-----------|----------|
| go-websocket | Go | WebSocket echo, counter |
| vite-ts | Vite + TypeScript | Todo list, HMR |
| nextjs | Next.js | SSR, API routes |
| vue | Vue 3 + TypeScript | Reactivity, color picker |
| svelte | SvelteKit + TypeScript | Reactivity, live filter |
| docker | nginx + Go API + Postgres | GraphQL API, reverse proxy (requires Docker) |

Run them all behind the proxy:

```sh
cd testbed
./run.sh
```

Open `http://localhost:3000` and use the widget to switch between them.

## Contributing

```sh
git clone https://github.com/derekgould/multi-dev-proxy
cd multi-dev-proxy
go build ./...
go test ./...
```

Releases are built with [GoReleaser](https://goreleaser.com). To cut a release, tag and push:

```sh
git tag v0.x.0
git push origin v0.x.0
```

## License

GPL-3.0. See [LICENSE](LICENSE).
