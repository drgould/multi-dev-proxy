# Concepts

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

## The switcher widget

The floating widget mentioned above is auto-injected into every HTML response routed through a proxy. A few things worth knowing:

- It runs inside a **Shadow DOM**, so your page's CSS can't style it and its styles can't leak into your page.
- Selection is persisted per proxy port via the `__mdp_upstream_<port>` cookie — each proxy gets its own cookie to avoid collisions.
- It subscribes to a server-sent events stream so the list of available servers updates live as services come and go.
- There's currently no flag to disable injection; if you don't want it, route traffic to the upstream port directly instead of through the proxy.

## Service groups

Services are automatically grouped by their git branch name (or an explicit `--group` flag). All services sharing the same group name form a switchable group. Switching to a group sets the default upstream on every proxy at once.

```sh
# Start services under the "main" group
mdp run --group main

# Switch all proxies to the "feature-auth" group
mdp switch --group feature-auth
```

See [`mdp switch`](./cli.md#mdp-switch) for CLI details.

## How switching works

Each proxy has its own cookie (e.g., `__mdp_upstream_3000`) to avoid collisions when multiple proxies run on localhost. The resolution order for each request is:

1. **Auto-route** — if only one server is registered, route to it (skips every other step)
2. **Query parameter** — `?__mdp_upstream=<name>` wins over cookie. Useful for per-iframe or per-tab routing where a shared cookie would collide
3. **Cookie** — if a valid cookie is present, route to that server
4. **Default upstream** — a server-side default set via `mdp switch` or the TUI
5. **Redirect** — redirect to the switch page at `/__mdp/switch`

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

---

[← Back to docs index](./index.md)
