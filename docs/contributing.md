# Contributing

## Getting set up

```sh
git clone https://github.com/drgould/multi-dev-proxy
cd multi-dev-proxy
go build ./...
go test ./...
```

## Build and test

```sh
go build ./...             # build all packages
go build -o mdp ./cmd/mdp  # build the CLI binary
go test ./...              # run all tests
go test -race ./...        # run with the race detector
go vet ./...               # static analysis
```

### End-to-end tests

E2E tests require the testbed running in a separate terminal. See [Testbed](./testbed.md) for what's in there.

```sh
cd testbed && ./run.sh   # terminal 1: start proxy + demo servers
npm run test:e2e         # terminal 2: run Playwright tests
```

## Conventional commits

Commits to `main` must use [conventional commit](https://www.conventionalcommits.org) prefixes — they drive the next version bump. PR titles must carry the same prefix (release-please reads them when squash-merging).

| Prefix | Effect |
| --- | --- |
| `feat:` | minor bump |
| `fix:` | patch bump |
| `feat!:` / `fix!:` | major bump |
| `perf:` | no release, shown in changelog |
| `docs:` / `test:` / `chore:` / `ci:` / `refactor:` | no release, hidden from changelog |

## Releases

Releases are managed by [release-please](https://github.com/googleapis/release-please) — **do not tag manually**. The flow is:

1. Land conventional commits on `main`.
2. release-please opens (or updates) a "Release PR" that bumps the version and rewrites `CHANGELOG.md`.
3. Merge the Release PR. That creates a GitHub Release and pushes the `v*` tag.
4. The tag triggers [GoReleaser](https://goreleaser.com), which builds and publishes the binaries.

---

[← Back to docs index](./index.md)
