# nvoken

nvoken (github.com/deepnoodle-ai/nvoken) is an open-source (Apache-2.0) agent
runtime as a service: durable agent turn execution, Session ownership and
persistence, tool execution, and cross-provider model routing. Host applications
use it as their agentic backend. nvoken owns the conversations (Sessions) while
the host remains source of truth for agent definitions, orchestration, and
product data.

## Design context

Repo documentation lives in `docs/`, organized by document type; see
`docs/README.md` for the layout. The root `README.md` stays a crisp
distillation, with depth in `docs/product/`.

## Architecture

Hexagonal architecture for the Go backend:

- `internal/domain/` — Pure domain types (zero external deps)
- `internal/ports/` — Interface definitions (repositories, infrastructure)
- `internal/services/` — Business logic (depends only on ports)
- `internal/adapters/` — Concrete implementations (`adapters/httpapi`, database adapters, …)
- `internal/daemon/` — Server bootstrap & DI
- `cmd/` — Small main packages: env/flag parsing plus a call into `internal/daemon`; no business logic

`cmd/nvokend` is the service binary.

## Tech stack

- Go; standard library first, dependencies added deliberately.
- github.com/deepnoodle-ai/wonton for env config parsing (`env.Parse`), CLI building, and test assertions.
- Multi-provider LLM support via github.com/deepnoodle-ai/dive.
- Postgres for durable state: pgx for access, sqlc for adapter query generation,
  and golang-migrate for embedded forward migrations.

## Go style

- In multiline keyed struct literals, put exactly one field assignment on each
  line.

## Scripts

- Do not write large Bash scripts. Use Python for non-trivial orchestration,
  qualification, smoke, load, and operational tooling; keep shell usage to
  small, direct lifecycle commands or thin wrappers.

## Pre-commit checks

Run the gate before commit:

```bash
make check   # build + vet + test + sqlc drift + OpenAPI lint + gofmt
```

## Releases

Releases are cut by pushing tags (see `.github/workflows/release.yml` for Go
binaries + Homebrew, `release-npm.yml` for the npm SDK). The Go tag `vX.Y.Z` and
the npm tag `npm-vX.Y.Z` share one version, and the `v*` workflow fails unless
the tag, `sdk/typescript/package.json`, and the `examples/typescript-chat`
dependency all match.

Update `CHANGELOG.md` in advance of every release: move the accumulated
`[Unreleased]` notes under a new `[X.Y.Z] - YYYY-MM-DD` heading before tagging.
Keep entries CONCISE — one tight line per change under the Keep a Changelog
headings (`Added` / `Changed` / `Fixed` / `Removed`), lead breaking changes with
**Breaking:**, and cite the PR number. Add notes to `[Unreleased]` as
user-facing changes land, not only at release time.
