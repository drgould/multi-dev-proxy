# Quick start

```sh
# Terminal 1 — start the orchestrator (opens TUI)
mdp

# Terminal 2 — run your frontend dev server
mdp run -P 3000 -- npm run dev

# Terminal 3 — run your backend
mdp run -P 4000 -- go run ./cmd/server

# Open http://localhost:3000 — use the widget to switch branches
# (switch to https once you've set up TLS certs — see the recipes page)
```

Next steps:

- Declare services in an [`mdp.yaml`](./config.md) so `mdp run` starts them all at once.
- Learn how [switching](./concepts.md#how-switching-works) resolves which upstream a request goes to.
- Put services with their own proxy ports behind a [Docker Compose](./recipes.md#docker-compose) stack.
- Set up [HTTPS with mkcert](./recipes.md#https) so OAuth flows Just Work.

---

[← Back to docs index](./index.md)
