# Changelog

## [1.12.1](https://github.com/drgould/multi-dev-proxy/compare/v1.12.0...v1.12.1) (2026-06-15)


### Features

* add dashboard command and report dashboard URL on daemon start ([#72](https://github.com/drgould/multi-dev-proxy/issues/72)) ([2b269fd](https://github.com/drgould/multi-dev-proxy/commit/2b269fd1dcac117429e01fb0661f19c9cb97011d))
* focus interactive hook prompts and buffer other output ([#74](https://github.com/drgould/multi-dev-proxy/issues/74)) ([db33918](https://github.com/drgould/multi-dev-proxy/commit/db339180c7c7515392c38f3868364fd8f708dd08))


### Miscellaneous

* release 1.12.1 ([c53f74d](https://github.com/drgould/multi-dev-proxy/commit/c53f74d197a2486d92f02d6ffcc516821d07eca5))

## [1.12.0](https://github.com/drgould/multi-dev-proxy/compare/v1.11.2...v1.12.0) (2026-06-12)


### Features

* forward interactive setup/shutdown/post_start hook prompts to the terminal ([#71](https://github.com/drgould/multi-dev-proxy/issues/71)) ([6dec340](https://github.com/drgould/multi-dev-proxy/commit/6dec3405ce29da4c79318fc71a4ef8e1128c85ec))
* gate readiness on named docker compose services via health_check ([#68](https://github.com/drgould/multi-dev-proxy/issues/68)) ([bdf42e9](https://github.com/drgould/multi-dev-proxy/commit/bdf42e9f8ec905a011d9a4a82cd4af6ae13e09c3))
* run post_start hooks after service readiness ([#70](https://github.com/drgould/multi-dev-proxy/issues/70)) ([ef76e1d](https://github.com/drgould/multi-dev-proxy/commit/ef76e1dba8689403529c3934436a28e54e3a5ea5))

## [1.11.2](https://github.com/drgould/multi-dev-proxy/compare/v1.11.1...v1.11.2) (2026-06-10)


### Bug Fixes

* scope group inputs by repo and skip the prompt when no groups match ([#66](https://github.com/drgould/multi-dev-proxy/issues/66)) ([3b57aae](https://github.com/drgould/multi-dev-proxy/commit/3b57aaead5977385f83112702241d4cc1e4efefe))

## [1.11.1](https://github.com/drgould/multi-dev-proxy/compare/v1.11.0...v1.11.1) (2026-06-10)


### Bug Fixes

* let cross-repo links fall back to the current group via @{current} ([#63](https://github.com/drgould/multi-dev-proxy/issues/63)) ([2697a38](https://github.com/drgould/multi-dev-proxy/commit/2697a389fa2e73f2bb53b028e366f7e09958cf48))
* merge env file writes instead of overwriting ([#65](https://github.com/drgould/multi-dev-proxy/issues/65)) ([11661bd](https://github.com/drgould/multi-dev-proxy/commit/11661bd25ef73f2e63c0004871009e62c0fa4638))

## [1.11.0](https://github.com/drgould/multi-dev-proxy/compare/v1.10.0...v1.11.0) (2026-06-10)


### Features

* drive UI refresh via SSE instead of 5s polling ([#61](https://github.com/drgould/multi-dev-proxy/issues/61)) ([dfef47c](https://github.com/drgould/multi-dev-proxy/commit/dfef47c5e68beb2373e51028748a138d0bff7a4e))

## [1.10.0](https://github.com/drgould/multi-dev-proxy/compare/v1.9.0...v1.10.0) (2026-06-05)


### Features

* interactive inputs and cross-repo links for mdp run ([#60](https://github.com/drgould/multi-dev-proxy/issues/60)) ([b2c689e](https://github.com/drgould/multi-dev-proxy/commit/b2c689ea8cedaaac3e61e096d30c2e3e3b9eab88))
* select batch services with --service flag and MDP_SERVICES env ([#55](https://github.com/drgould/multi-dev-proxy/issues/55)) ([3f03ce8](https://github.com/drgould/multi-dev-proxy/commit/3f03ce878e596b2a60672572e8ca64114f492a7f))
* stable per-branch port allocation across mdp runs ([#59](https://github.com/drgould/multi-dev-proxy/issues/59)) ([4ec66ab](https://github.com/drgould/multi-dev-proxy/commit/4ec66ab4ddea78e74171c9f6673f888d7d8242f2))


### Bug Fixes

* drop deprecated goreleaser brews block, guard cask xattr hook for linux ([#57](https://github.com/drgould/multi-dev-proxy/issues/57)) ([11b44e3](https://github.com/drgould/multi-dev-proxy/commit/11b44e3231d47674830d00d480de4cb0017d46e1))

## [1.9.0](https://github.com/drgould/multi-dev-proxy/compare/v1.8.0...v1.9.0) (2026-05-21)


### Features

* add Homebrew formula for Linux support ([#53](https://github.com/drgould/multi-dev-proxy/issues/53)) ([b9aadc6](https://github.com/drgould/multi-dev-proxy/commit/b9aadc6fbe1dd665f687ebb80be04d22af283189))

## [1.8.0](https://github.com/drgould/multi-dev-proxy/compare/v1.7.3...v1.8.0) (2026-05-15)


### ⚠ BREAKING CHANGES

* register multi-port services under the parent service key ([#49](https://github.com/drgould/multi-dev-proxy/issues/49))

### Bug Fixes

* hide service name in pill for single-service groups ([#51](https://github.com/drgould/multi-dev-proxy/issues/51)) ([492a34e](https://github.com/drgould/multi-dev-proxy/commit/492a34ec6ff9375ceeee09d9031dfd6cac4d105a))
* register multi-port services under the parent service key ([#49](https://github.com/drgould/multi-dev-proxy/issues/49)) ([051477c](https://github.com/drgould/multi-dev-proxy/commit/051477ca595f98583c3e0d624ca121b544f012d4))


### Miscellaneous

* release as 1.8.0 ([#52](https://github.com/drgould/multi-dev-proxy/issues/52)) ([1651067](https://github.com/drgould/multi-dev-proxy/commit/1651067f92cab7f0a4bdeab779e510f2405bba9f))

## [1.7.3](https://github.com/drgould/multi-dev-proxy/compare/v1.7.2...v1.7.3) (2026-05-14)


### Bug Fixes

* cross-repo @-refs against orchestrator-managed services ([#47](https://github.com/drgould/multi-dev-proxy/issues/47)) ([a4e2087](https://github.com/drgould/multi-dev-proxy/commit/a4e208797f30282cfd4b95b647ca0c3ccdcc6f5c))

## [1.7.2](https://github.com/drgould/multi-dev-proxy/compare/v1.7.1...v1.7.2) (2026-05-14)


### Features

* log cross-repo link connect/disconnect during mdp run ([#45](https://github.com/drgould/multi-dev-proxy/issues/45)) ([46e2212](https://github.com/drgould/multi-dev-proxy/commit/46e22120e627566e5bc90e876676be3540e082d2))


### Miscellaneous

* release 1.7.2 ([44fad4d](https://github.com/drgould/multi-dev-proxy/commit/44fad4d2e8106c31dd3f88eef9752defea496c45))

## [1.7.1](https://github.com/drgould/multi-dev-proxy/compare/v1.7.0...v1.7.1) (2026-05-07)


### Bug Fixes

* hide single-service child rows in switcher pill dropdown ([#43](https://github.com/drgould/multi-dev-proxy/issues/43)) ([fb3eb4e](https://github.com/drgould/multi-dev-proxy/commit/fb3eb4ea91c9e88515aeee7766553f4773e6011f))
* show branch and service on switch page for mdp.yaml services ([#44](https://github.com/drgould/multi-dev-proxy/issues/44)) ([61d3ca9](https://github.com/drgould/multi-dev-proxy/commit/61d3ca96529f2f0b8a3ef97a79463802beb19eba))
* show branch/group in switcher pill for mdp.yaml services ([#41](https://github.com/drgould/multi-dev-proxy/issues/41)) ([21beacb](https://github.com/drgould/multi-dev-proxy/commit/21beacbbceeaabf8dab1ab8e965c0d13a5e91b73))

## [1.7.0](https://github.com/drgould/multi-dev-proxy/compare/v1.6.0...v1.7.0) (2026-04-29)


### Features

* --link flag to override peer-lookup group per repo ([#38](https://github.com/drgould/multi-dev-proxy/issues/38)) ([2bd59ab](https://github.com/drgould/multi-dev-proxy/commit/2bd59abd0bd30337612bcb2c17988c85978aff5b))

## [1.6.0](https://github.com/drgould/multi-dev-proxy/compare/v1.5.2...v1.6.0) (2026-04-25)


### Features

* add protocol: udp to port mappings ([#35](https://github.com/drgould/multi-dev-proxy/issues/35)) ([1e281db](https://github.com/drgould/multi-dev-proxy/commit/1e281db71ba458a0701a4d7a8d2b05c3300fb1e8))
* cross-repo @&lt;repo&gt; env references via orchestrator ([#37](https://github.com/drgould/multi-dev-proxy/issues/37)) ([ce5e90a](https://github.com/drgould/multi-dev-proxy/commit/ce5e90aced5a1bb6e1ac22c6149608afbc7a22ae))
* health check fallback for detached services ([#34](https://github.com/drgould/multi-dev-proxy/issues/34)) ([2d08c82](https://github.com/drgould/multi-dev-proxy/commit/2d08c82720f212b1caf3428a1018e5e0272942c4))
* split combined-stream logs for compose and regex prefixes ([#36](https://github.com/drgould/multi-dev-proxy/issues/36)) ([0ca73b8](https://github.com/drgould/multi-dev-proxy/commit/0ca73b80174d9d77db3a19049fd62e96531c36c1))


### Bug Fixes

* auto-shutdown empty proxies to release their port ([#32](https://github.com/drgould/multi-dev-proxy/issues/32)) ([c63d971](https://github.com/drgould/multi-dev-proxy/commit/c63d97136157ab23f38631379f97b62c5ee678b1))

## [1.5.2](https://github.com/drgould/multi-dev-proxy/compare/v1.5.1...v1.5.2) (2026-04-23)


### Bug Fixes

* resolve relative TLS paths against caller cwd ([#27](https://github.com/drgould/multi-dev-proxy/issues/27)) ([3b0e248](https://github.com/drgould/multi-dev-proxy/commit/3b0e2486f69acf512e5baada95eb42578bdda6cc))
* write env files in mdp run batch mode ([#29](https://github.com/drgould/multi-dev-proxy/issues/29)) ([4d4cab3](https://github.com/drgould/multi-dev-proxy/commit/4d4cab39f8be7f14a451f6a51d8b99eecabb12f4))

## [1.5.1](https://github.com/drgould/multi-dev-proxy/compare/v1.5.0...v1.5.1) (2026-04-22)


### Bug Fixes

* load TLS certs across all register paths ([#24](https://github.com/drgould/multi-dev-proxy/issues/24)) ([cf0c8d0](https://github.com/drgould/multi-dev-proxy/commit/cf0c8d09876a165f7edb628b4544c028ce3f6f93))
* thread batch readiness knobs through launchBatchService ([#26](https://github.com/drgould/multi-dev-proxy/issues/26)) ([159edc4](https://github.com/drgould/multi-dev-proxy/commit/159edc46da000bf661f85ad11d222001a96b7f69))

## [1.5.0](https://github.com/drgould/multi-dev-proxy/compare/v1.4.1...v1.5.0) (2026-04-22)


### Features

* add setup and shutdown hooks per service ([#14](https://github.com/drgould/multi-dev-proxy/issues/14)) ([d4cb083](https://github.com/drgould/multi-dev-proxy/commit/d4cb083d59c74b795aa27f97758a4c81536025d7))
* export generated env vars to .env files ([#16](https://github.com/drgould/multi-dev-proxy/issues/16)) ([0b8bd14](https://github.com/drgould/multi-dev-proxy/commit/0b8bd1494a5f3dd489cdcc7ad8018696a1aae06c))
* service dependencies via depends_on in mdp.yaml ([#15](https://github.com/drgould/multi-dev-proxy/issues/15)) ([cce87bc](https://github.com/drgould/multi-dev-proxy/commit/cce87bc72bdf0b7fff4e9d345dc2dd54365f4d41))

## [1.4.1](https://github.com/drgould/multi-dev-proxy/compare/v1.4.0...v1.4.1) (2026-04-21)


### Bug Fixes

* make proxy optional on multi-port service ports ([#12](https://github.com/drgould/multi-dev-proxy/issues/12)) ([d091aa6](https://github.com/drgould/multi-dev-proxy/commit/d091aa6df1b50398efcdcc173f0a201a7be182a9))

## [1.4.0](https://github.com/drgould/multi-dev-proxy/compare/v1.3.1...v1.4.0) (2026-04-21)


### Features

* interpolate ${svc.port} references in service env values ([d50ec99](https://github.com/drgould/multi-dev-proxy/commit/d50ec996d73ce9295e8f26e38bc0efdb3721dbdc))
* redirect clients to the upstream's declared scheme ([a3eda1b](https://github.com/drgould/multi-dev-proxy/commit/a3eda1b49b181a2c40ae891bc764f687f390e319))

## [1.3.1](https://github.com/drgould/multi-dev-proxy/compare/v1.3.0...v1.3.1) (2026-04-14)


### Bug Fixes

* run goreleaser in release-please workflow ([#8](https://github.com/drgould/multi-dev-proxy/issues/8)) ([08b4c3d](https://github.com/drgould/multi-dev-proxy/commit/08b4c3d81acf7ed2da002320cb3cf1e432de246d))

## [1.3.0](https://github.com/drgould/multi-dev-proxy/compare/v1.2.0...v1.3.0) (2026-04-14)


### Features

* add API handlers, WS header fix, port detection (Tasks 7, 9, 14) ([09fc0b7](https://github.com/drgould/multi-dev-proxy/commit/09fc0b7e7d7259f788594b2da2d0781b4560716c))
* add client session lifecycle cleanup ([460598e](https://github.com/drgould/multi-dev-proxy/commit/460598e6886b1e83359a2d4d5d8b3457147739db))
* add HTML dashboard, service worker routing, and SSE updates ([#4](https://github.com/drgould/multi-dev-proxy/issues/4)) ([740d7e2](https://github.com/drgould/multi-dev-proxy/commit/740d7e27e40a405e7f7507f806cd6b0eca6fcd4d))
* add proxy core, HTML injection, process manager (Tasks 8, 10, 13) ([4a96bb9](https://github.com/drgould/multi-dev-proxy/commit/4a96bb957933e0d40450b90c11ce570e466e61a7))
* add Scoop bucket distribution ([99fdfbc](https://github.com/drgould/multi-dev-proxy/commit/99fdfbc552e489f1fbdb65e6f357894f65509de3))
* add switch page, widget UI (Tasks 11, 12) ([2c798dc](https://github.com/drgould/multi-dev-proxy/commit/2c798dcc981f47c0ca5958a8863404e23f8485e3))
* add TLS cert forwarding, auto-detect upstream scheme, dynamic HTTPS upgrade ([a1b6195](https://github.com/drgould/multi-dev-proxy/commit/a1b61950d7e4c3be1fe141479160c7fb67e35079))
* add Wave 1 internal packages (registry, routing, ports, detect, process) ([747b111](https://github.com/drgould/multi-dev-proxy/commit/747b111e70cfe70e5d304067e374e7365bd780ac))
* implement mdp start, run, register commands and pruner (Tasks 15-19) ([8a833c5](https://github.com/drgould/multi-dev-proxy/commit/8a833c5e300d04dcb1941cc453d8b3cd90927cf8))


### Bug Fixes

* correct license references from MIT to GPL-3.0 ([ca244b2](https://github.com/drgould/multi-dev-proxy/commit/ca244b22f3a2a0d1a946fdc38a68f20b86cff3b7))
* exclude component name from release tags ([#6](https://github.com/drgould/multi-dev-proxy/issues/6)) ([26b5514](https://github.com/drgould/multi-dev-proxy/commit/26b55141df46cfea128cf8c4aaa13ff3a31a7f54))
* **proxy:** eliminate ModifyResponse race by moving location rewrite to NewProxy ([2756268](https://github.com/drgould/multi-dev-proxy/commit/2756268843f97249488c73a5cf3a745ec37a94da))
* put replace_existing_artifacts under release (not release.github) ([c4a9533](https://github.com/drgould/multi-dev-proxy/commit/c4a953331583c89f08bcbd685c829aa4c0c1d041))
* remove unused registeredNames variable ([8ddd130](https://github.com/drgould/multi-dev-proxy/commit/8ddd1309470d1a0e6c7a6104f967e26e2d1ad759))
* simplify indicator pill to show only groups with member services ([#2](https://github.com/drgould/multi-dev-proxy/issues/2)) ([807ee1b](https://github.com/drgould/multi-dev-proxy/commit/807ee1be789d2b4f32148500692544879db1fc0e))

## [1.2.0](https://github.com/drgould/multi-dev-proxy/compare/mdp-v1.1.2...mdp-v1.2.0) (2026-04-13)


### Features

* add API handlers, WS header fix, port detection (Tasks 7, 9, 14) ([09fc0b7](https://github.com/drgould/multi-dev-proxy/commit/09fc0b7e7d7259f788594b2da2d0781b4560716c))
* add client session lifecycle cleanup ([460598e](https://github.com/drgould/multi-dev-proxy/commit/460598e6886b1e83359a2d4d5d8b3457147739db))
* add HTML dashboard, service worker routing, and SSE updates ([#4](https://github.com/drgould/multi-dev-proxy/issues/4)) ([740d7e2](https://github.com/drgould/multi-dev-proxy/commit/740d7e27e40a405e7f7507f806cd6b0eca6fcd4d))
* add proxy core, HTML injection, process manager (Tasks 8, 10, 13) ([4a96bb9](https://github.com/drgould/multi-dev-proxy/commit/4a96bb957933e0d40450b90c11ce570e466e61a7))
* add Scoop bucket distribution ([99fdfbc](https://github.com/drgould/multi-dev-proxy/commit/99fdfbc552e489f1fbdb65e6f357894f65509de3))
* add switch page, widget UI (Tasks 11, 12) ([2c798dc](https://github.com/drgould/multi-dev-proxy/commit/2c798dcc981f47c0ca5958a8863404e23f8485e3))
* add TLS cert forwarding, auto-detect upstream scheme, dynamic HTTPS upgrade ([a1b6195](https://github.com/drgould/multi-dev-proxy/commit/a1b61950d7e4c3be1fe141479160c7fb67e35079))
* add Wave 1 internal packages (registry, routing, ports, detect, process) ([747b111](https://github.com/drgould/multi-dev-proxy/commit/747b111e70cfe70e5d304067e374e7365bd780ac))
* implement mdp start, run, register commands and pruner (Tasks 15-19) ([8a833c5](https://github.com/drgould/multi-dev-proxy/commit/8a833c5e300d04dcb1941cc453d8b3cd90927cf8))


### Bug Fixes

* correct license references from MIT to GPL-3.0 ([ca244b2](https://github.com/drgould/multi-dev-proxy/commit/ca244b22f3a2a0d1a946fdc38a68f20b86cff3b7))
* **proxy:** eliminate ModifyResponse race by moving location rewrite to NewProxy ([2756268](https://github.com/drgould/multi-dev-proxy/commit/2756268843f97249488c73a5cf3a745ec37a94da))
* put replace_existing_artifacts under release (not release.github) ([c4a9533](https://github.com/drgould/multi-dev-proxy/commit/c4a953331583c89f08bcbd685c829aa4c0c1d041))
* remove unused registeredNames variable ([8ddd130](https://github.com/drgould/multi-dev-proxy/commit/8ddd1309470d1a0e6c7a6104f967e26e2d1ad759))
* simplify indicator pill to show only groups with member services ([#2](https://github.com/drgould/multi-dev-proxy/issues/2)) ([807ee1b](https://github.com/drgould/multi-dev-proxy/commit/807ee1be789d2b4f32148500692544879db1fc0e))

## v1.1.2

- Deregister servers from orchestrator on shutdown

## v1.1.1

- Fix macOS Gatekeeper warnings for installed binary

## v1.1.0

- Add /release slash command
- Smart HTTP/HTTPS proxy with per-service TLS and last-path tracking
- Dynamic TLS cert inheritance from services
- Fix bugs and hardening from code review
- Add comprehensive test coverage across packages
- Skip port override in solo mode, rename runSoloWithHealth to runProxied

## v1.0.1

### Changed

- **Group switcher hidden with single proxy** — the groups UI (widget pill, switch page, TUI, `mdp status`) is now hidden when there is only one proxy, since groups are only useful for coordinating multiple proxies
- **`MDP=1` env var** — proxied child processes receive `MDP=1` in their environment so build tooling can detect it and adjust config dynamically
- **Registration errors are fatal** — `mdp run` now exits immediately if service registration with the orchestrator fails, instead of silently continuing
- **Health watchdog** — services started via `mdp run` automatically shut down when the orchestrator/proxy becomes unreachable

## v1.0.0

### Features

- **Orchestrator** — new `mdp.yaml` config drives multi-proxy setups with named groups, sibling awareness, and coordinated group switching across proxies
- **Daemon mode** — `mdp start` daemonizes the process; `mdp status`, `mdp logs`, and `mdp switch` control it from separate terminals
- **Interactive TUI** — live dashboard with tabs (Groups, Proxies, Services), mouse support, hover highlights, clickable rows, and keyboard navigation
- **Group switching** — switch all proxies to a named group (e.g. `dev`, `staging`) from the TUI, widget pill, switch page, or `mdp switch` CLI command
- **Switch page sibling support** — the `/__mdp/switch` page now lists servers from sibling proxies with direct switch buttons

### Changed

- **Widget pill group switching** — correctly sets the browser cookie after switching groups so the page reloads to the right upstream
- **Switch page group switching** — same cookie fix; navigates to `/` after switching instead of staying on the switch page
- **Switch handler** — redirects to `/` after switching instead of back to `/__mdp/switch`
- **E2E tests** migrated from Playwright to Puppeteer + Vitest; run headed locally, headless in CI, serial execution

### New commands

- `mdp start` — start proxy in daemon mode
- `mdp status` — show daemon status
- `mdp logs` — tail daemon logs
- `mdp switch <group>` — switch active group from CLI
- `mdp deregister` — remove a registered server

## v0.1.1

### Changed

- **Widget pill** shows **repo · branch** (branch names with slashes preserved), not branch alone
- **Widget script** lives in `internal/ui/widget.js` and is embedded at build time with `go:embed`
- **README** — widget behavior and install paths aligned with current Homebrew/Scoop layout

## v0.1.0

Initial release.

### Features

- **Reverse proxy** on a single stable port (default `:3000`) with cookie-based routing between multiple upstream dev servers
- **`mdp start`** — runs the proxy with control API, switch page, and injected widget
- **`mdp run <cmd>`** — wraps any dev command, auto-assigns a port via `PORT` env, registers with the proxy; falls back to solo mode if no proxy is running
- **`mdp register`** — manually registers an already-running service (useful for Docker, external processes)
- **Floating widget** injected into every HTML response via Shadow DOM — switch branches without leaving the page
- **Switch page** at `/__mdp/switch` with light/dark/auto theme toggle
- **HTML injection** — decompresses gzip/brotli responses, injects `<script>` tag before `</body>`, updates Content-Length, strips CSP headers that would block it
- **WebSocket proxying** with header casing fix for Vite HMR compatibility
- **HTTPS by default** — auto-generates TLS certs using mkcert (if installed) or self-signed fallback with system trust store integration
- **Multi-repo support** — server names use `repo/branch` format, auto-detected from git remote; widget and switch page group by repo
- **Dead server pruning** — checks registered PIDs every 10 seconds, removes dead servers automatically
- **Process group management** — spawns child processes in their own process group (`Setpgid`) for clean teardown on exit
- **PID-optional registration** — servers without a PID (e.g. Docker containers) are accepted and skip liveness pruning
- **Port detection from stdout** — parses `http://localhost:<port>` from child process output to handle frameworks that ignore `PORT`
- **Location header rewriting** — rewrites upstream `Location` headers (including `127.0.0.1` and `[::1]` variants) to point back through the proxy

### Distribution

- **GoReleaser** — cross-compiled binaries for macOS, Linux, Windows (amd64 + arm64)
- **Homebrew** — `brew install derekgould/mdp/mdp`
- **npm** — `npm install -g mdp`
- **curl installer** — `curl -fsSL https://raw.githubusercontent.com/derekgould/multi-dev-proxy/main/install.sh | sh`
- **Scoop** (Windows) — `scoop install mdp`

### Testbed

- 6 demo servers: Go (WebSocket), Vite + TypeScript, Next.js, Vue 3, SvelteKit, Docker (nginx + Go API + Postgres)
- Playwright E2E tests covering proxy routing, switch page, widget injection, and all server reachability
