# API reference

## Per-proxy endpoints

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


**Dead server pruning:** Each proxy checks registered servers every 10 seconds. Any server whose PID is no longer alive is removed automatically.

---

[← Back to docs index](./index.md)
