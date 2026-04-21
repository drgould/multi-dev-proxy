# Multi-dev Proxy (AKA mdp)

Run multiple dev servers from different branches and even repos behind a single port.

## The problem

If you use **git worktrees** (or just multiple clones) to work on several branches at once, every dev server ends up on a different port. This creates two headaches:

1. **OAuth breaks.** Providers like Google, GitHub, and Auth0 require you to allowlist exact redirect URLs (e.g. `http://localhost:3000/callback`). A different port means a different origin, which means your OAuth flow fails unless you register every port with every provider and those ports change every time you restart.
2. **Port juggling.** You have `main` on `:5173`, your feature branch on `:5174`, and a backend in another worktree on `:9091`. Remembering which port is which, updating `.env` files, and wiring frontend→backend URLs correctly becomes a constant distraction.

`mdp` solves both by putting all your dev servers behind **stable, fixed ports**. One allowlisted OAuth redirect URL works for every branch. Switch between branches is instant either in the browser via a floating widget or from the terminal without restarting anything or touching a single config file.

## How it works

`mdp` runs an orchestrator that manages multiple reverse proxies, one per port you want to expose. Each proxy routes requests to registered dev servers using a cookie. A small widget is injected into every HTML response giving you a floating switcher at the top of the page. A TUI shows all proxies and services and lets you switch between them from the terminal.

```
browser → :3000 (frontend proxy) → :42301 (main branch)
                                  → :38847 (feature/auth branch)
        → :4000 (api proxy)      → :20234 (api/main)
                                  → :20235 (api/feature-auth)
```

## Quick start

```sh
# Terminal 1 — start the orchestrator (opens TUI)
mdp

# Terminal 2 — run your frontend dev server
mdp run -P 3000 -- npm run dev

# Terminal 3 — run your backend
mdp run -P 4000 -- go run ./cmd/server

# Open https://localhost:3000 — use the widget to switch branches
```

## Installation

**curl (macOS/Linux):**

```sh
curl -fsSL https://raw.githubusercontent.com/drgould/multi-dev-proxy/main/install.sh | sh
```

**Homebrew:**

```sh
brew install drgould/mdp/mdp
```

**Scoop (Windows):**

```powershell
scoop bucket add mdp https://github.com/drgould/scoop-mdp
scoop install mdp
```

**npm:**

```sh
npm install -g mdp
```

## Usage

### `mdp`

Starts the orchestrator with an interactive TUI. Manages all proxy instances and shows their registered services. The TUI lets you navigate with arrow keys and switch active servers with Enter.

```sh
mdp
mdp --control-port 13100
```

### `mdp --daemon` / `mdp -d`

Starts the orchestrator as a background daemon (no TUI). Useful for CI or when you don't need the interactive interface.

```sh
mdp -d
# prints: mdp orchestrator started (PID 12345, ctrl :13100)
```

### `mdp run`

Wraps a dev command. Picks a free port, sets it as an environment variable in the child process, and registers with the orchestrator. When the process exits, it deregisters automatically.

```sh
mdp run -- npm run dev
mdp run -P 4000 -- go run ./cmd/server
mdp run --env API_PORT -- docker compose up
```

Without a command, reads `mdp.yaml` and batch-starts all configured services:

```sh
mdp run                        # uses current git branch as group
mdp run --group feature-auth   # override group name
```

If no orchestrator is running, `mdp run` falls back to solo mode and just runs the command directly.

### `mdp register`

Manually registers an already-running service.

```sh
mdp register myapp/main --port 4000 -P 3000
mdp register myapp/main --port 4000 --pid 12345
mdp register --list
```

### `mdp switch`

Switch active upstream service or group from the command line.

```sh
mdp switch app/main -P 3000          # switch individual server
mdp switch --group main              # switch all services in a group
mdp switch --clear -P 3000           # clear default
```

### `mdp --stop`

Stop the background orchestrator.

```sh
mdp --stop
```

## Config file (`mdp.yaml`)

Place an `mdp.yaml` in your project root to declaratively define services. When present, `mdp run` (without a command) starts all configured services.

```yaml
services:
  frontend:
    command: npm run dev
    dir: ./frontend
    proxy: 3000
    env:
      NEXT_PUBLIC_API_URL: https://localhost:4000

  api:
    command: go run ./cmd/server
    dir: ./backend
    proxy: 4000
    env:
      DATABASE_URL: postgres://localhost:5432/dev

  auth:
    port: 8080          # fixed port, externally managed
    proxy: 5000

port_range: "10000-60000"  # optional
```

### Docker Compose

For projects using Docker Compose where multiple services need their own proxy ports, use the `ports` mapping with `auto` port assignment:

```yaml
# mdp.yaml
services:
  frontend:
    command: npm run dev
    proxy: 3000

  infra:
    command: docker compose up
    env:
      API_PORT: auto       # mdp assigns a free port
      AUTH_PORT: auto
    ports:
      - env: API_PORT
        proxy: 4000
        name: api          # registered as "<branch>/api"
      - env: AUTH_PORT
        proxy: 5000
        name: auth
```

In your `docker-compose.yml`, reference the environment variables:

```yaml
# docker-compose.yml
services:
  api:
    build: ./api
    ports:
      - "${API_PORT:-8080}:8080"
  auth:
    build: ./auth
    ports:
      - "${AUTH_PORT:-8081}:8080"
```

When you run `mdp run`, mdp assigns free ports, sets them as environment variables, and registers each port mapping with the appropriate proxy.

### Referencing another service's port

Use `${service.port}` inside any `env` value to inject the assigned port of another service. `mdp` resolves these before launching, so every worktree can allocate truly random ports and still wire services together:

```yaml
services:
  db:
    command: docker compose up db --wait
    # no proxy; internal only

  api:
    command: ./api
    proxy: 4000
    env:
      DATABASE_URL: "postgres://app:app@localhost:${db.port}/app"

  web:
    command: npm run dev
    proxy: 3000
    env:
      API_URL: "http://localhost:${api.port}"
```

For multi-port services, reference the specific env key: `${infra.API_PORT}`.

## Service groups

Services are automatically grouped by their git branch name (or an explicit `--group` flag). All services sharing the same group name form a switchable group. Switching to a group sets the default upstream on every proxy at once.

```sh
# Start services under the "main" group
mdp run --group main

# Switch all proxies to the "feature-auth" group
mdp switch --group feature-auth
```

## How switching works

Each proxy has its own cookie (e.g., `__mdp_upstream_3000`) to avoid collisions when multiple proxies run on localhost. The resolution order for each request is:

1. **Cookie** — if a valid cookie is present, route to that server
2. **Default upstream** — a server-side default set via `mdp switch` or the TUI
3. **Auto-route** — if only one server is registered, route to it automatically
4. **Redirect** — redirect to the switch page at `/__mdp/switch`

The default upstream is especially useful for backend proxies where cookies aren't available (dev-server proxies, curl, API clients).

## Multi-repo support

Server names use the format `repo/branch`. The repo name is auto-detected from `git remote get-url origin`, falling back to the directory name.

```sh
# In ~/code/frontend on branch feature/nav
mdp run -P 3000 -- npm run dev
# Registers as: frontend/feature/nav

# In ~/code/api on branch main
mdp run -P 4000 -- go run ./cmd/server
# Registers as: api/main
```

Override the name with `--name` or just the repo with `--repo`:

```sh
mdp run --name myapp/staging -- npm run dev
mdp run --repo frontend -- npm run dev
```

## HTTPS

mdp inherits TLS certificates from the services it proxies. When a service registers with `--tls-cert` and `--tls-key`, the proxy automatically starts accepting HTTPS connections using that certificate. Each proxy port serves both HTTP and HTTPS on the same port.

```sh
# Service provides its own cert — proxy inherits it
mdp run --tls-cert ./certs/localhost.pem --tls-key ./certs/localhost-key.pem -- npm run dev

# Auto-detect mkcert certs
mdp run --auto-tls -- npm run dev
```

Generate a local cert with [mkcert](https://github.com/FiloSottile/mkcert):

```sh
mkcert localhost
mdp run --tls-cert localhost.pem --tls-key localhost-key.pem -- npm run dev
```

## Configuration

**Environment variables:**


| Variable         | Description                                                                         |
| ---------------- | ----------------------------------------------------------------------------------- |
| `MDP_PROXY_PORT` | Default proxy port for `mdp run` and `mdp register` (overrides the default of 3000) |


`**mdp` flags:**


| Flag             | Default   | Description                                 |
| ---------------- | --------- | ------------------------------------------- |
| `--control-port` | `13100`   | Control API port                            |
| `-d, --daemon`   |           | Run as background daemon (no TUI)           |
| `--stop`         |           | Stop the background daemon                  |
| `--config`       |           | Path to mdp.yaml (auto-detected if not set) |
| `--host`         | `0.0.0.0` | Host for proxy listeners                    |


`**mdp run` flags:**


| Flag               | Default       | Description                                      |
| ------------------ | ------------- | ------------------------------------------------ |
| `-P, --proxy-port` | `3000`        | Proxy port to connect to                         |
| `--repo`           |               | Repository name override                         |
| `--name`           |               | Full server name override (skips auto-detection) |
| `--group`          |               | Group name override (default: git branch)        |
| `--env`            | `PORT`        | Env var name for the assigned port               |
| `--port-range`     | `10000-60000` | Port range for spawned services                  |
| `--control-port`   | `13100`       | Orchestrator control port                        |


`**mdp register` flags:**


| Flag               | Default | Description                               |
| ------------------ | ------- | ----------------------------------------- |
| `-p, --port`       |         | Port the service is running on (required) |
| `--pid`            | `0`     | Process ID for liveness tracking          |
| `-P, --proxy-port` | `3000`  | Proxy port to connect to                  |
| `--group`          |         | Group name override                       |
| `-l, --list`       |         | List registered services                  |
| `--control-port`   | `13100` | Orchestrator control port                 |


## API reference

### Per-proxy endpoints

Each proxy instance serves these endpoints:


| Method   | Path                     | Description                                                        |
| -------- | ------------------------ | ------------------------------------------------------------------ |
| `GET`    | `/__mdp/health`          | Health check. Returns `{"ok":true,"servers":N}`                    |
| `GET`    | `/__mdp/servers`         | List all servers grouped by repo                                   |
| `POST`   | `/__mdp/register`        | Register a server. Body: `{"name":"repo/branch","port":N,"pid":N}` |
| `DELETE` | `/__mdp/register/{name}` | Deregister a server by name                                        |
| `POST`   | `/__mdp/switch/{name}`   | Switch active server. Sets cookie + default, redirects             |
| `GET`    | `/__mdp/switch`          | Server switcher UI page                                            |
| `GET`    | `/__mdp/widget.js`       | Floating switcher widget script                                    |
| `GET`    | `/__mdp/config`          | Proxy config (cookie name, siblings, groups)                       |
| `GET`    | `/__mdp/default`         | Get current default upstream                                       |
| `DELETE` | `/__mdp/default`         | Clear default upstream                                             |
| `POST`   | `/__mdp/default/{name}`  | Set default upstream                                               |


### Orchestrator control API (port 13100)


| Method   | Path                                   | Description                       |
| -------- | -------------------------------------- | --------------------------------- |
| `GET`    | `/__mdp/health`                        | Orchestrator health check         |
| `GET`    | `/__mdp/proxies`                       | List all proxy instances          |
| `POST`   | `/__mdp/register`                      | Register (with `proxyPort` field) |
| `DELETE` | `/__mdp/register/{name}`               | Deregister from all proxies       |
| `POST`   | `/__mdp/proxies/{port}/default/{name}` | Set default on a specific proxy   |
| `DELETE` | `/__mdp/proxies/{port}/default`        | Clear default on a specific proxy |
| `GET`    | `/__mdp/groups`                        | List all groups                   |
| `POST`   | `/__mdp/groups/{name}/switch`          | Switch group (set defaults)       |
| `GET`    | `/__mdp/services`                      | List managed services             |
| `POST`   | `/__mdp/shutdown`                      | Graceful shutdown                 |


**Dead server pruning:** Each proxy checks registered servers every 10 seconds. Any server whose PID is no longer alive is removed automatically.

## Testbed

The `testbed/` directory contains demo servers across different frameworks to verify the proxy works end-to-end:


| Server       | Framework                 | Features                                     |
| ------------ | ------------------------- | -------------------------------------------- |
| go-websocket | Go                        | WebSocket echo, counter                      |
| vite-ts      | Vite + TypeScript         | Todo list, HMR                               |
| nextjs       | Next.js                   | SSR, API routes                              |
| vue          | Vue 3 + TypeScript        | Reactivity, color picker                     |
| svelte       | SvelteKit + TypeScript    | Reactivity, live filter                      |
| docker       | nginx + Go API + Postgres | GraphQL API, reverse proxy (requires Docker) |


Run them all behind the proxy:

```sh
cd testbed
./run.sh
```

Open `https://localhost:3000` and use the widget to switch between them.

## Contributing

```sh
git clone https://github.com/drgould/multi-dev-proxy
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