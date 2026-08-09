# Public SDK repository

**Status:** Accepted
**Author:** OpenAI Codex with Curtis Einsmann
**Date:** 2026-08-08
**Workflow:** Standard-tier spec, written and built in parallel

## Context

The private `deepnoodle-ai/nvoken-cloud` repository now owns the nvoken service,
including the authoritative OpenAPI contracts. The public
`deepnoodle-ai/nvoken` repository needs to become the distribution and
development home for the Go, Python, TypeScript, and Rust SDKs and the Go
`nvoken` client CLI. Mature implementations of those clients already exist in
`nvoken-cloud`; leaving them there would couple public package development and
release access to the private backend and would make the public repository a
misleading placeholder.

This split follows the useful part of the `mobius` / `mobius-cloud` precedent:
the service owns the API, while the public repository owns client ergonomics,
generated artifacts, conformance tests, packaging, and releases.

## Goals

- Make this repository the source of every published nvoken SDK and the
  `nvoken` CLI.
- Port the current Go, Python, TypeScript, and Rust clients without losing their
  reliability facades, tests, examples, or package metadata.
- Generate low-level transports and models from synchronized snapshots of the
  authoritative `nvoken-cloud/openapi` contracts.
- Make generation reproducible in public CI without requiring access to the
  private service repository.
- Give contributors one local gate that verifies generated drift,
  cross-language behavior, package contents, examples, and the CLI.
- Keep SDK and CLI release automation independent of backend deployment.

## Non-goals

- Move the daemon, persistence, provider adapters, deployment configuration, or
  other backend implementation into this repository.
- Make generated clients the primary user-facing APIs. Generated code remains
  a transport foundation underneath handwritten, idiomatic facades.
- Introduce compatibility layers for the old combined repository layout. There
  are no external repository consumers that require it.
- Publish packages, create tags, or configure registry credentials as part of
  the scaffold.

## Proposal

Use a small polyglot monorepo organized by deliverable:

```text
nvoken/
├── cmd/nvoken/                 # Go CLI; depends on the Go SDK
├── internal/authstore/         # CLI-only local credential persistence
├── sdk/
│   ├── go/                     # independent Go module
│   ├── python/                 # PyPI package
│   ├── typescript/             # npm package
│   ├── rust/                   # crates.io package
│   ├── conformance/            # shared fixtures and local HTTP test server
│   ├── scripts/                # generation and validation orchestration
│   └── operations.json         # generated operation-coverage manifest
├── openapi/                    # synchronized, non-authoritative snapshots
├── examples/                   # executable SDK examples
├── docs/                       # repository and SDK development guidance
└── scripts/                    # contract sync and CLI release tooling
```

This is deliberately simpler than Clean or Hexagonal Architecture. Each SDK is
already a natural module with its ecosystem's native package boundary. Within
each SDK, dependencies flow from handwritten facade to generated transport;
generated code never imports the facade. The CLI depends on the public Go SDK
and one private local-storage package. It does not import service internals.

### Contract ownership and synchronization

`nvoken-cloud/openapi/runtime.yaml` and `identity.yaml` are authoritative. This
repository commits byte-for-byte snapshots under `openapi/` so a public pull
request can regenerate and test clients without access to a private repository.
`openapi/SOURCE.json` records the upstream repository and commit.

`make openapi-sync` copies the two contracts from `NVOKEN_CLOUD_REPO`
(defaulting to `../nvoken-cloud`) and refreshes provenance. `make
openapi-sync-check` proves that the committed snapshots match a local upstream
checkout. The normal public CI gate validates generation from the committed
snapshot. Synchronization is intentionally manual for now: a maintainer with
both repositories checked out runs the sync and generation targets, reviews the
result, and opens a public pull request. The public repository does not receive
a credential that can read private repositories.

The invariant is:

```text
nvoken-cloud OpenAPI
        │ sync + review
        ▼
public OpenAPI snapshot
        │ pinned generators
        ▼
generated transports
        │ handwritten composition
        ▼
idiomatic SDK facades ──► CLI / applications
```

Generated directories are committed so normal package installs do not require
Java, Go code generators, network access, or a private repository checkout.
Generator versions and the OpenAPI Generator jar checksum remain pinned.

### SDK boundaries

Each language package contains:

1. Generated models and endpoint clients derived from the synchronized
   contract.
2. A handwritten facade for durable invocation admission, waiting, resumable
   streaming, cancellation, ToolCall submission, callbacks, validation, and
   common media inputs.
3. Language-specific tests backed by shared JSON fixtures and the same local
   conformance server.

Reliability belongs in the facade, not in generated templates. Admission
retries reuse the exact body and idempotency key; stream recovery uses durable
cursors and authoritative reads; ToolCall IDs are preserved; `Retry-After` is
honored; local wait cancellation is distinct from remote Invocation
cancellation.

### CLI boundary

`cmd/nvoken` is the only binary in this repository. It uses the Go SDK for API
operations and keeps filesystem credential profiles in `internal/authstore`.
The daemon remains in `nvoken-cloud/cmd/nvokend`. CLI releases therefore contain
only the `nvoken` binary and the license.

### Validation and releases

`make check` runs formatting, Go build/vet/tests, OpenAPI lint, generator drift,
all four SDK suites, package-content checks, examples, and CLI tests. Individual
targets remain available for faster iteration.

Releases remain independently retryable:

- `vX.Y.Z` publishes cross-platform CLI archives and updates Homebrew.
- `sdk/go/vX.Y.Z` identifies the Go module version.
- `npm-vX.Y.Z`, `pypi-vX.Y.Z`, and `crates-vX.Y.Z` publish the corresponding
  language package after verifying the manifest version.

## Alternatives considered

### Generate directly from the private repository in every public CI run

This removes the snapshot but makes ordinary external pull requests impossible
to validate without exposing a private-repository credential. It also makes old
SDK commits irreproducible when the backend contract advances.

### Keep SDKs in `nvoken-cloud` and mirror only release artifacts

This preserves one source tree but defeats the purpose of a public SDK
repository: contributors cannot inspect generation, tests, examples, or
handwritten behavior, and registry source links point at the wrong project.

### Split each language into its own repository

Independent repositories reduce checkout size, but shared contract updates and
cross-language conformance changes would require coordinated multi-repository
pull requests. The current team and release volume do not justify that cost.

## Tradeoffs and consequences

- The public OpenAPI snapshot is deliberate duplication. Provenance and sync
  checks are required to prevent it from being mistaken for the authority.
- Generated code makes the repository larger, but package builds and external
  contributions become deterministic and self-contained.
- A single repository makes contract-wide changes easy but means contributors
  may download toolchains for languages they are not editing; focused targets
  mitigate this.
- Independent release tags can temporarily expose different package versions.
  This is preferable to making a partial registry outage block every other
  deliverable, and release documentation will identify the intended aligned
  version.

## Rollout

1. Port the current SDK, conformance, CLI, examples, and release files from the
   current `nvoken-cloud` main branch.
2. Remove daemon/backend dependencies and adapt contract generation to the
   synchronized snapshot boundary.
3. Run the complete local gate, then stop changing SDK and CLI sources in
   `nvoken-cloud`.
4. Configure registry trusted publishers and the Homebrew token.
5. Publish the first aligned release from this repository after review.

## Open questions

- Whether future hosted-only identity endpoints should remain in the same SDK
  package or become a separate operator package. Keep the current combined
  client until real consumers demonstrate that the extra surface is confusing.
