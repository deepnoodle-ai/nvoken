# Nvoken

Nvoken (github.com/deepnoodle-ai/nvoken) is an open-source (Apache-2.0) agent
runtime as a service: durable agent turn execution, Session ownership and
persistence, tool execution, and cross-provider model routing. Host applications
use it as their agentic backend. Nvoken owns the conversations (Sessions) while
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
- Postgres for durable state.

## Pre-commit checks

Run the gate before commit:

```bash
make check   # build + vet + test + gofmt
```
