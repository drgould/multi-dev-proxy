# CLI reference

## `mdp`

Starts the orchestrator with an interactive TUI. Manages all proxy instances and shows their registered services.

Keys: `↑`/`↓` (or `j`/`k`) navigate, `Enter` switch active server, `Tab`/`Shift+Tab` (or `←`/`→`, `1`-`4`) switch tabs, `/` filter rows (`Esc` clears), `x` stop the selected service (with confirmation; unavailable for externally managed processes), `d` detach (leave daemon running), `q` quit (stop daemon). Mouse is supported: click to select/activate, hover to highlight, wheel to scroll.

The Logs tab tails the daemon log and any detached `mdp run` logs: `f` toggles follow, `[`/`]` switch sources, `/` filters lines.

```sh
mdp
mdp --control-port 13100
```

A web dashboard is also served on `--dashboard-port` (default `6370`) — open `http://localhost:6370` if you prefer a browser UI to the TUI.

## `mdp --daemon` / `mdp -d`

Starts the orchestrator as a background daemon (no TUI). Useful for CI or when you don't need the interactive interface.

```sh
mdp -d
# prints: mdp orchestrator started (PID 12345, ctrl :13100)
```

## `mdp run`

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
mdp run -i                     # prompt for declared inputs (e.g. cross-repo branch names)
mdp run --service api          # start only `api` (and its depends_on)
mdp run --service api,worker   # start a subset (also accepts repeated --service)
MDP_SERVICES=api,worker mdp run  # same, via env var (flag wins if both are set)
```

Add `-i` to prompt for the [`inputs:`](./mdp-yaml-reference.md#inputs) declared in `mdp.yaml` — handy for choosing a peer's branch interactively instead of typing `--link repo=branch`. Without `-i`, inputs use their defaults (an input with no default errors). With `-i`, each input is prompted through an interactive terminal UI (arrow keys to browse pick-lists, typing for free text) — this requires both stdin and stderr to be a real terminal (it reads keys from stdin and renders to stderr); it errors if either is piped or redirected.

The `--service` selector restricts batch mode to a subset of the services declared in `mdp.yaml`. Names must match the service keys in the file; transitive `depends_on` entries are auto-included so dependency waits still work. Empty/unset = start everything (default).

When a command is given, `mdp run` picks its mode in this order:

1. **Orchestrator mode** — if an orchestrator is running on `--control-port`, register with it.
2. **Standalone proxy mode** — otherwise, probe `--proxy-port` for a bare `mdp` proxy (no orchestrator) and register with it if found.
3. **Solo mode** — otherwise, run the command directly with no proxy.

Batch mode (`mdp run` with no command) requires an orchestrator — it errors out if one isn't running.

## `mdp register`

Manually registers an already-running service.

```sh
mdp register myapp/main --port 4000 -P 3000
mdp register myapp/main --port 4000 --pid 12345
mdp register --list
```

## `mdp deregister`

Remove a server from all proxies. Useful when an externally managed service (e.g. Docker) stops without notifying the orchestrator.

```sh
mdp deregister myapp/main
```

## `mdp switch`

Switch active upstream service or group from the command line. See [how switching works](./concepts.md#how-switching-works) for the resolution order.

```sh
mdp switch app/main -P 3000          # switch individual server
mdp switch --group main              # switch all services in a group
mdp switch --clear -P 3000           # clear default
```

## `mdp status`

Print daemon status, proxies, registered servers, and groups. Add `--json` for machine-readable output.

```sh
mdp status
mdp status --json
```

## `mdp logs`

Show the daemon's log output. Defaults to the last 50 lines; use `-f` to follow.

```sh
mdp logs
mdp logs -n 200
mdp logs -f
```

## `mdp stop`

Stop the background orchestrator.

```sh
mdp stop
```

`mdp --stop` still works but is deprecated in favor of `mdp stop`.

## Configuration

**Environment variables:**


| Variable         | Description                                                                         |
| ---------------- | ----------------------------------------------------------------------------------- |
| `MDP_PROXY_PORT` | Default proxy port for `mdp run` and `mdp register` (overrides the default of 3000) |
| `MDP_SERVICES`   | Comma-separated subset of services for `mdp run` batch mode (overridden by `--service`) |


`**mdp` flags:**


| Flag               | Default   | Description                                 |
| ------------------ | --------- | ------------------------------------------- |
| `--control-port`   | `13100`   | Control API port                            |
| `--dashboard-port` | `6370`    | Dashboard web UI port                       |
| `-d, --daemon`     |           | Run as background daemon (no TUI)           |
| `--stop`           |           | Stop the background daemon (deprecated, use `mdp stop`) |
| `--config`         |           | Path to mdp.yaml (auto-detected if not set) |
| `--host`           | `0.0.0.0` | Host for proxy listeners                    |


`**mdp run` flags:**


| Flag               | Default       | Description                                      |
| ------------------ | ------------- | ------------------------------------------------ |
| `-P, --proxy-port` | `3000`        | Proxy port to connect to                         |
| `--repo`           |               | Repository name override                         |
| `--name`           |               | Full server name override (skips auto-detection) |
| `--group`          |               | Group name override (default: git branch)        |
| `--env`            | `PORT`        | Env var name for the assigned port               |
| `--port-range`     | `10000-60000` | Port range for spawned services                  |
| `--no-stable-ports`| `false`       | Allocate fresh ports each run instead of reusing this branch's previous ports. See [stable ports](./recipes.md#stable-ports). |
| `--tls-cert`       |               | TLS certificate file (serves this service over HTTPS; see [recipes](./recipes.md)) |
| `--tls-key`        |               | TLS key file (paired with `--tls-cert`)          |
| `--auto-tls`       | `false`       | Auto-detect TLS certs from mkcert                |
| `--control-port`   | `13100`       | Orchestrator control port                        |
| `--link`           |               | Override peer-lookup group: `repo=group` (repeatable). See [cross-repo refs](./mdp-yaml-reference.md#cross-group-lookups-via---link). |
| `-i, --interactive`| `false`       | Prompt for the [`inputs:`](./mdp-yaml-reference.md#inputs) declared in `mdp.yaml`; without it, inputs use their defaults. |
| `--service`        |               | Batch mode only: start only the listed services (repeatable or comma-separated). Transitive `depends_on` are auto-included. Falls back to `MDP_SERVICES`. |
| `--restart`        | `false`       | Auto-restart a service's process after it exits (crash or clean exit). In batch mode this applies to every service, in addition to any per-service `restart: true` in mdp.yaml — see [`restart`](./mdp-yaml-reference.md#restart--auto-restart-on-crash). |


`**mdp register` flags:**


| Flag               | Default | Description                               |
| ------------------ | ------- | ----------------------------------------- |
| `-p, --port`       |         | Port the service is running on (required) |
| `--pid`            | `0`     | Process ID for liveness tracking          |
| `-P, --proxy-port` | `3000`  | Proxy port to connect to                  |
| `--group`          |         | Group name override                       |
| `-l, --list`       |         | List registered services                  |
| `--tls-cert`       |         | TLS certificate file (registers as HTTPS) |
| `--tls-key`        |         | TLS key file (paired with `--tls-cert`)   |
| `--control-port`   | `13100` | Orchestrator control port                 |


`**mdp switch` flags:**


| Flag               | Default | Description                                    |
| ------------------ | ------- | ---------------------------------------------- |
| `-P, --proxy-port` |         | Proxy port (required for individual switches)  |
| `--group`          |         | Switch all services in a group                 |
| `--clear`          |         | Clear the default upstream (needs `-P`)        |
| `--control-port`   | `13100` | Orchestrator control port                      |

---

[← Back to docs index](./index.md)
