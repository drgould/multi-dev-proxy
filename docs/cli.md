# CLI reference

## `mdp`

Starts the orchestrator with an interactive TUI. Manages all proxy instances and shows their registered services.

Keys: `↑`/`↓` (or `j`/`k`) navigate, `Enter` switch active server, `Tab` / `h` / `l` switch tabs, `d` detach (leave daemon running), `q` quit (stop daemon). Mouse clicks are supported.

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
```

If no orchestrator is running, `mdp run` falls back to solo mode and just runs the command directly.

## `mdp register`

Manually registers an already-running service.

```sh
mdp register myapp/main --port 4000 -P 3000
mdp register myapp/main --port 4000 --pid 12345
mdp register --list
```

## `mdp switch`

Switch active upstream service or group from the command line. See [how switching works](./concepts.md#how-switching-works) for the resolution order.

```sh
mdp switch app/main -P 3000          # switch individual server
mdp switch --group main              # switch all services in a group
mdp switch --clear -P 3000           # clear default
```

## `mdp --stop`

Stop the background orchestrator.

```sh
mdp --stop
```

## Configuration

**Environment variables:**


| Variable         | Description                                                                         |
| ---------------- | ----------------------------------------------------------------------------------- |
| `MDP_PROXY_PORT` | Default proxy port for `mdp run` and `mdp register` (overrides the default of 3000) |


`**mdp` flags:**


| Flag               | Default   | Description                                 |
| ------------------ | --------- | ------------------------------------------- |
| `--control-port`   | `13100`   | Control API port                            |
| `--dashboard-port` | `6370`    | Dashboard web UI port                       |
| `-d, --daemon`     |           | Run as background daemon (no TUI)           |
| `--stop`           |           | Stop the background daemon                  |
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

---

[← Back to docs index](./index.md)
