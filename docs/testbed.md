# Testbed

The `testbed/` directory is a full end-to-end playground for `mdp` — it exercises group switching, TLS, Docker-managed services, and non-HTTP ports in one setup.

`testbed/run.sh` builds `mdp`, generates a self-signed cert, starts the orchestrator as a daemon, and batch-starts every service declared in `testbed/mdp.yaml`.

## Services

Three groups (`main`, `feature-a`, `feature-b`), each with a `web` + `api` pair, plus a non-HTTP `db-main` service to exercise port-only declarations:

| Service         | Group     | Proxy | Notes                                                  |
| --------------- | --------- | ----- | ------------------------------------------------------ |
| `web-main`      | main      | 3000  | Go demo server with TLS (self-signed cert)             |
| `api-main`      | main      | 3001  | `docker compose up` — Go API built from `testbed/docker` |
| `db-main`       | main      | —     | Fake DB; demonstrates an allocated port with no proxy  |
| `web-feature-a` | feature-a | 3000  | Go demo server (plain HTTP)                            |
| `api-feature-a` | feature-a | 3001  | Go demo server with `setup` + `shutdown` hooks         |
| `web-feature-b` | feature-b | 3000  | Go demo server                                         |
| `api-feature-b` | feature-b | 3001  | Go demo server                                         |

## Run it

```sh
cd testbed
./run.sh
```

Then:

- Open `http://localhost:3000` (frontend proxy — `https://` also works after TLS kicks in for `main`).
- Open `http://localhost:3001` (API proxy).
- Use the widget or `mdp switch --group feature-a` to flip both proxies at once.
- Open `http://localhost:6370` for the dashboard.

The legacy framework folders (`go-websocket/`, `vite-ts/`, `nextjs/`, `vue/`, `svelte/`, `echo-api/`) are still in the tree but are not wired into `run.sh` — leave them alone unless you're repurposing them.

---

[← Back to docs index](./index.md)
