# mdp docs

Run multiple dev servers from different branches and even repos behind a single port.

```sh
# Terminal 1 — start the orchestrator (opens TUI)
mdp

# Terminal 2 — run your frontend dev server
mdp run -P 3000 -- npm run dev

# Terminal 3 — run your backend
mdp run -P 4000 -- go run ./cmd/server

# Open http://localhost:3000 — use the widget to switch branches
```

## Documentation

- [Concepts](./concepts.md) — the problem, how it works, groups, switching, multi-repo
- [Installation](./installation.md) — Homebrew, curl, Scoop
- [Quick start](./quick-start.md) — tutorial flow
- [CLI reference](./cli.md) — every command and flag
- [Config (`mdp.yaml`)](./config.md) — services, hooks, `depends_on`, `env_file`, ports, TLS
- [mdp.yaml reference](./mdp-yaml-reference.md) — every field in the config schema
- [Recipes](./recipes.md) — Docker Compose, HTTPS / mkcert
- [API reference](./api.md) — per-proxy and orchestrator endpoints
- [Testbed](./testbed.md) — demo servers
- [Contributing](./contributing.md) — build, test, release
