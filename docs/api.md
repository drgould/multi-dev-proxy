# API reference

## Per-proxy endpoints

Each proxy instance serves these endpoints on its listen port (e.g. `:3000`, `:4000`):


| Method   | Path                          | Description                                                        |
| -------- | ----------------------------- | ------------------------------------------------------------------ |
| `GET`    | `/__mdp/health`               | Health check. Returns `{"ok":true,"servers":N}`                    |
| `GET`    | `/__mdp/servers`              | List all servers grouped by repo                                   |
| `POST`   | `/__mdp/register`             | Register a server. Body: `{"name":"repo/branch","port":N,"pid":N}` |
| `DELETE` | `/__mdp/register/{name}`      | Deregister a server by name                                        |
| `POST`   | `/__mdp/switch/{name}`        | Switch active server. Sets cookie + default, redirects             |
| `GET`    | `/__mdp/switch`               | Server switcher UI page                                            |
| `GET`    | `/__mdp/widget.js`            | Floating switcher widget script                                    |
| `GET`    | `/__mdp/sw.js`                | Service worker that powers per-tab routing via `__mdp_upstream`    |
| `GET`    | `/__mdp/events`               | Server-sent events stream of registry changes (used by the widget) |
| `GET`    | `/__mdp/config`               | Proxy config (cookie name, siblings, groups)                       |
| `GET`    | `/__mdp/default`              | Get current default upstream                                       |
| `DELETE` | `/__mdp/default`              | Clear default upstream                                             |
| `POST`   | `/__mdp/default/{name}`       | Set default upstream                                               |
| `POST`   | `/__mdp/groups/{name}/switch` | Switch every proxy's default to the named group                    |
| `GET`    | `/__mdp/last-path/{name}`     | Last path observed for a server (used by the widget to restore position) |


## Orchestrator control API (port 13100)


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


### Internal client protocol

These endpoints are used by `mdp run` to talk to the orchestrator and by the built-in UIs. They are not considered a stable public API and may change without notice.


| Method   | Path                      | Description                                                    |
| -------- | ------------------------- | -------------------------------------------------------------- |
| `PATCH`  | `/__mdp/register/{name}`  | Update PID for a registered service (after async start)        |
| `POST`   | `/__mdp/heartbeat`        | Keep-alive from `mdp run` clients                              |
| `POST`   | `/__mdp/disconnect`       | Client disconnect — deregisters all of the client's servers    |
| `GET`    | `/__mdp/shutdown/watch`   | Long-poll; returns when the orchestrator shuts down            |
| `GET`    | `/__mdp/events`           | Server-sent events stream used by the dashboard and widget     |


## Dead server pruning

Each proxy sweeps its registry every 10 seconds:

- Servers with a PID are dropped as soon as the process is no longer alive.
- Servers registered without a PID but with a client ID (e.g. `mdp run` wraps) are cleaned up when their client disconnects or stops heartbeating.
- Servers registered without a PID or client ID (e.g. `mdp register` for externally managed processes) are dropped after they fail a TCP health check 3 times in a row, following a 30-second grace period after registration.

---

[← Back to docs index](./index.md)
