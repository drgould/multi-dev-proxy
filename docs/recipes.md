# Recipes

## Docker Compose

For projects using Docker Compose where multiple services need their own proxy ports, use the `ports` mapping with `auto` port assignment:

```yaml
# mdp.yaml
services:
  frontend:
    command: npm run dev
    proxy: 3000

  infra:
    command: docker compose up
    env:
      API_PORT: auto       # mdp assigns a free port
      AUTH_PORT: auto
    ports:
      - env: API_PORT
        proxy: 4000
        name: api          # registered as "<branch>/api"
      - env: AUTH_PORT
        proxy: 5000
        name: auth
```

In your `docker-compose.yml`, reference the environment variables:

```yaml
# docker-compose.yml
services:
  api:
    build: ./api
    ports:
      - "${API_PORT:-8080}:8080"
  auth:
    build: ./auth
    ports:
      - "${AUTH_PORT:-8081}:8080"
```

When you run `mdp run`, mdp assigns free ports, sets them as environment variables, and registers each port mapping with the appropriate proxy.

**Non-HTTP ports (no proxy):** omit `proxy:` (and optionally `name:`) on a `ports:` entry to allocate a free port for `${svc.env.VAR}` interpolation without starting a reverse-proxy listener for it. Useful for databases, caches, and other non-HTTP services other services just need to connect to directly.

```yaml
db:
  command: docker compose up db --wait
  env:
    DB_PORT: auto
  ports:
    - env: DB_PORT    # allocated & interpolatable, no proxy
```

## HTTPS

mdp inherits TLS certificates from the services it proxies. When a service registers with `--tls-cert` and `--tls-key`, the proxy automatically starts accepting HTTPS connections using that certificate. Each proxy port serves both HTTP and HTTPS on the same port.

```sh
# Service provides its own cert — proxy inherits it
mdp run --tls-cert ./certs/localhost.pem --tls-key ./certs/localhost-key.pem -- npm run dev

# Auto-detect mkcert certs
mdp run --auto-tls -- npm run dev
```

Generate a local cert with [mkcert](https://github.com/FiloSottile/mkcert):

```sh
mkcert localhost
mdp run --tls-cert localhost.pem --tls-key localhost-key.pem -- npm run dev
```

---

[← Back to docs index](./index.md)
