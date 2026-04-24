# Config file (`mdp.yaml`)

Place an `mdp.yaml` in your project root to declaratively define services. When present, `mdp run` (without a command) starts all configured services.

> For the full field-by-field schema, see the [**mdp.yaml reference**](./mdp-yaml-reference.md).

```yaml
services:
  frontend:
    setup:
      - bun install
    command: bun dev
    shutdown:
      - rm -rf .cache/dev
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

**Hooks:** `setup` commands run sequentially before `command` — if any exits non-zero the service is marked failed and `command` is not started. `shutdown` commands run sequentially after `command` exits (for any reason), best-effort with a 30s per-step timeout. Both share the same `dir` and `env` as `command`.

**Other service fields:**

- `scheme:` — `http` (default) or `https`. Auto-inferred as `https` when `tls_cert` is set.
- `tls_cert:` / `tls_key:` — Paths to a TLS cert and key. The proxy serves HTTPS on this port using them. See [HTTPS](./recipes.md#https).
- `env_file:` — Optional path for writing the service's resolved env vars as a `.env` file. See [Exporting env vars](#exporting-env-vars-to-env-files) below.
- `depends_on:` — See [Startup dependencies](#startup-dependencies) below.
- `ports:` — See [Docker Compose](./recipes.md#docker-compose).

## Exporting env vars to `.env` files

`mdp` generates free ports and resolves `${svc.port}` refs at startup, so the final env vars aren't known until the orchestrator is up. To make those values visible to tools that run outside of `mdp` (your editor's run config, `psql`, `curl`, a standalone shell), export them to `.env` files.

**Per-service** — `env_file:` writes exactly what that service's process sees:

```yaml
services:
  api:
    command: ./api
    proxy: 4000
    env_file: ./.mdp.api.env   # relative to the service's dir
    env:
      DATABASE_URL: "postgres://localhost:${db.port}/app"
```

**Project-wide** — a top-level `global:` block writes an aggregate file with any keys you pick:

```yaml
global:
  env_file: ./.mdp.env          # relative to the mdp.yaml dir
  env:
    # Scalar form — any string, with ${svc.key} / ${svc.env.VAR} interpolation.
    API_URL: "http://localhost:${api.port}"
    DB_URL:  "postgres://localhost:${db.env.DB_PORT}/app"
    # Mapping form — pass through another service's port or env var as-is.
    API_PORT:
      ref: api.env.PORT
    DB_PORT:
      ref: db.env.DB_PORT

services:
  # ...
```

Files are written after port resolution, before any service `command` runs. Per-service paths resolve against the service's `dir`; `global.env_file` resolves against the `mdp.yaml` directory.

## Referencing another service's port

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

## Startup dependencies

Use `depends_on` to declare that a service needs other services to be up first. `mdp` waits for each dependency to be TCP-reachable on its assigned port(s) before launching dependents:

```yaml
services:
  db:
    command: docker compose up db --wait
    env:
      DB_PORT: auto
    ports:
      - env: DB_PORT

  api:
    command: ./api
    proxy: 4000
    depends_on:
      - db

  web:
    command: npm run dev
    proxy: 3000
    depends_on:
      - api
      - db
```

Services without `depends_on` start in parallel. Independent branches of the dependency graph run in parallel too — only direct dependents wait. Each dependency has a 60s readiness timeout; if a dependency fails to become ready, its dependents are marked `failed` and skipped. Cycles and references to undefined services are rejected at config-load time.

## Detached commands and health checks

By default, `mdp` keeps a service's proxy registration alive as long as either its process is running or its port is still answering a TCP probe. That makes detached commands like `docker compose up -d` work out of the box — the foreground process exits, but the probe keeps the entry alive while the containers keep listening.

Override the probe with `health_check`:

```yaml
services:
  db:
    command: docker compose up -d
    dir: ./db
    port: 5432
    health_check: docker               # shorthand for `docker compose ps -q`

  api:
    command: bun run dev
    proxy: 4000
    health_check:
      http: http://localhost:4000/health
```

Supported variants: `tcp: <port>`, `http: <url>`, `command: <shell tokens>`, and the `docker` shorthand. See the [`mdp.yaml` reference](./mdp-yaml-reference.md#detached-services-and-health-checks) for details.

---

[← Back to docs index](./index.md)
