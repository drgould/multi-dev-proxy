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

See [docs/concepts.md](docs/concepts.md) for service groups, switching resolution order, and multi-repo naming.

## Quick start

```sh
# Terminal 1 — start the orchestrator (opens TUI)
mdp

# Terminal 2 — run your frontend dev server
mdp run -P 3000 -- npm run dev

# Terminal 3 — run your backend
mdp run -P 4000 -- go run ./cmd/server

# Open http://localhost:3000 — use the widget to switch branches
```

## Install

```sh
brew install drgould/mdp/mdp
```

Other methods (curl, Scoop): see [docs/installation.md](docs/installation.md).

## Documentation

- [Concepts](docs/concepts.md) — the problem, how it works, groups, switching, multi-repo
- [Installation](docs/installation.md) — Homebrew, curl, Scoop
- [Quick start](docs/quick-start.md) — tutorial flow
- [CLI reference](docs/cli.md) — every command and flag
- [Config (`mdp.yaml`)](docs/config.md) — services, hooks, `depends_on`, `env_file`, ports, TLS
- [mdp.yaml reference](docs/mdp-yaml-reference.md) — every field in the config schema
- [Recipes](docs/recipes.md) — Docker Compose, HTTPS / mkcert
- [API reference](docs/api.md) — per-proxy and orchestrator endpoints
- [Testbed](docs/testbed.md) — demo servers
- [Contributing](docs/contributing.md) — build, test, release

## License

GPL-3.0. See [LICENSE](LICENSE).
