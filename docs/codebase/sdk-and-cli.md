# Multi-language SDK and Go CLI architecture

**Status:** Accepted
**Author:** Codex
**Date:** 2026-07-21
**Workflow:** Prototype, then spec, then build.

## Context

[PRD 026](../prds/026-prd-multi-language-sdks-and-go-cli.md) makes
`openapi/runtime.yaml` the only public Runtime HTTP contract and requires
independently consumable Go, TypeScript, Python, and Rust packages plus a Go
client CLI. The repository currently has only the daemon module and contract.
A generation spike against the exact OpenAPI 3.1 document showed that
`oapi-codegen` v2.8.0 and OpenAPI Generator v7.22.0 produce compiling clients,
provided the Go response types receive a suffix to avoid a schema/operation
name collision. Generator handling of JSON Schema `const` and unions varies,
so generated clients are a transport foundation rather than the supported
workflow API.

## Goals

- Regenerate all Runtime transports byte-for-byte from one command and fail CI
  when committed output drifts.
- Expose every `operationId` through generated clients while keeping normal
  quickstarts on small, handwritten durable-workflow APIs.
- Share language-neutral reliability, reducer, callback-signing, and error
  fixtures across all four SDKs.
- Keep the `nvoken` client binary distinct from `nvokend` and make it consume
  only the public Go SDK.

## Non-goals

- No new Runtime, identity, administration, or device-login endpoints.
- No registry publication automation or long-term compatibility policy.
- No generated server code and no CLI-owned HTTP routes or payload schemas.
- No framework-specific callback receiver adapters.

## Proposal

`sdk/scripts/generate.sh` owns generation. It runs `oapi-codegen` v2.8.0 for
Go and the pinned OpenAPI Generator v7.22.0 JAR for TypeScript Fetch, Python
HTTPX, and Rust Reqwest clients. Generation occurs in a temporary directory;
the script replaces only explicitly named generated directories or files so
handwritten facades and package metadata cannot be overwritten. Generated
files remain committed and carry their generator warning. `make sdk-generate`
updates them, while `make sdk-generate-check` regenerates the explicit targets
from temporary generator output and detects any committed diff.

The packages live under `sdk/go`, `sdk/typescript`, `sdk/python`, and
`sdk/rust`. Go is a nested module so importing
`github.com/deepnoodle-ai/nvoken/sdk/go` does not pull daemon dependencies.
The root module uses a local `replace` only so `cmd/nvoken` can import that
module while the repositories are released together. TypeScript publishes
`@deepnoodle-ai/nvoken`; Python and Rust use `nvoken`. Registry publication is
deferred.

Each SDK has three layers:

1. A read-only generated transport exposing every Runtime operation and model.
2. A handwritten client with `invoke`, `get`, `list`, `wait`, `stream`,
   `resume`, `submitToolResults`, and `cancel`, plus typed errors and bounded
   replay-safe retries.
3. A durable handle carrying Invocation and Session IDs. Local timeout or
   cancellation stops only the caller's wait; explicit `cancel` is the only
   remote cancellation path.

Facades use generated operations rather than duplicating routes. Admission
retries reuse the same immutable request and idempotency key. Result retries
reuse ToolCall IDs. Only transport failures, `408`, `425`, `429`, `500`, `502`,
`503`, and `504` are retried; `Retry-After` wins over bounded exponential
backoff with jitter. Typed errors preserve category, status, request ID,
machine code, safe details, and retry delay.

Streaming helpers use generated request builders or operation metadata, parse
SSE incrementally, retain only durable event IDs, reconnect after disconnect
or rotation with `Last-Event-ID`, and reconcile terminal state with an
authoritative get. The
shared reducer deduplicates messages by sequence and Invocation changes by
Invocation ID plus revision. Ephemeral generation deltas never advance the
resume cursor.

Callback verification is handwritten because the callback receiver wire
contract is intentionally outside the Runtime OpenAPI document. Every language
uses the same checked-in signing vectors, verifies the exact raw bytes and
header bindings before decoding, applies the five-minute timestamp window, and
offers a small first-result store interface keyed by ToolCall ID.

The Go CLI uses Wonton's command builder and the Go facade. Global endpoint
resolution is `--base-url`, `NVOKEN_BASE_URL`, the local config file, then
`http://localhost:8080`; `NVOKEN_API_KEY` is required before any Runtime call.
Commands provide stable JSON and concise text output for Invocation and Session
workflows. Baseline coverage is checked against generated operation metadata,
while handwritten command names organize the durable workflows.

`sdk/conformance` contains one fault-injecting HTTP server and shared JSON
fixtures. `make sdk-check` compiles all packages, runs each facade against that
server, verifies callback/reducer vectors, and runs the Go CLI tests. The root
`make check` remains the daemon gate and CI runs both targets.

## Alternatives considered

- **OpenAPI Generator for Go too.** One generator is simpler operationally,
  but `oapi-codegen` produces a smaller, standard-library-friendly Go client
  and PRD 026 already records its OpenAPI 3.1 compatibility probe.
- **Four generated-only packages.** This gives broad endpoint coverage but
  leaves retry safety, durable handles, SSE recovery, callback verification,
  and typed error policy to every adopter—the precise problem the PRD owns.
- **One custom cross-language generator.** It could normalize every language
  shape but would make nvoken responsible for a large code generator and
  weaken ecosystem-native output. Small facades are a cheaper compatibility
  boundary.
- **Implement CLI HTTP calls directly.** This would make the CLI a second
  Runtime contract and allow retry/error behavior to drift, so the CLI depends
  on the separate Go SDK instead.

## Tradeoffs and consequences

Committing generated clients creates a larger diff and pins nvoken to generator
quirks. In exchange, releases are inspectable and consumers do not need Java or
generator tooling. Four facades still require language-specific maintenance;
shared behavior fixtures constrain semantics without forcing identical public
shapes. The Rust generator's OpenAPI 3.1 and union support remains less mature
than its transport support, so facade types and fixtures are the deliberate
stability boundary.

## Rollout

This is additive. Existing daemon imports and deployment paths do not change.
The initial packages remain version `0.1.0`; publication is a later explicit
release operation. PRD 027 may add a separate generated identity/admin client
and CLI credential profiles without expanding the Runtime facade.
