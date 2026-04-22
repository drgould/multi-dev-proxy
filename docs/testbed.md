# Testbed

The `testbed/` directory contains demo servers across different frameworks to verify the proxy works end-to-end:


| Server       | Framework                 | Features                                     |
| ------------ | ------------------------- | -------------------------------------------- |
| go-websocket | Go                        | WebSocket echo, counter                      |
| vite-ts      | Vite + TypeScript         | Todo list, HMR                               |
| nextjs       | Next.js                   | SSR, API routes                              |
| vue          | Vue 3 + TypeScript        | Reactivity, color picker                     |
| svelte       | SvelteKit + TypeScript    | Reactivity, live filter                      |
| docker       | nginx + Go API + Postgres | GraphQL API, reverse proxy (requires Docker) |


Run them all behind the proxy:

```sh
cd testbed
./run.sh
```

Open `https://localhost:3000` and use the widget to switch between them.

---

[← Back to docs index](./index.md)
