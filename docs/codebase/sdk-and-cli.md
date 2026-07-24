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
`@deepnoodle/nvoken`; Python and Rust use `nvoken`. Registry publication is
deferred.

Each SDK has three layers:

1. A read-only generated transport exposing every Runtime operation and model.
2. A handwritten client with `invoke`, explicit resource reads and lists,
   bounded waits, Invocation and Session streams, ToolCall result submission,
   cancellation, and composed result reads, plus typed errors and bounded
   replay-safe retries.
3. A durable handle carrying Invocation and Session IDs. Local timeout or
   cancellation stops only the caller's wait; explicit `cancel` is the only
   remote cancellation path.

### Cross-language public convention

The facades use one product vocabulary even when a language's casing or
iteration syntax differs:

- `Client` is the configured entry point; `client.invocation(id)` creates a
  lazy `InvocationHandle` without a network request.
- `InvocationHandle` exposes `refresh`, `wait`, `waitForAction`,
  `waitForResult`, `result`, `outputText`, `listMessages`, `stream`, ToolCall
  submission, and `cancel` using native language casing. `outputText` is the
  read accessor; Agent `text(input)` runs a turn.
- Retry controls are `maxAttempts`, `minDelay`, and `maxDelay`; polling
  controls are `minPollInterval` and `maxPollInterval`, with the language's
  ordinary duration type or unit suffix.
- JSON admission accepts a string shorthand while preserving text-block input,
  generates an idempotency key when omitted from a facade call, and exposes
  the actual key and acknowledgement metadata on the handle.
- Session selectors are mutually exclusive types in handwritten facades.
  Session pagination, Session-scoped `listSessionMessages`, and fixed-cut
  transcript draining have symmetric helpers. A handle's `listMessages`
  remains Invocation-scoped.
- Model discovery uses `listModels` and `getModel` (with native language
  casing). Every provider position uses the same extensible validated string
  for additive compatibility; the server remains the authority that rejects an
  uninstalled provider.
- Invocation streams expose the wire's discriminated event union directly.
  `output_text.delta` plus `invocation.result` is the minimum useful consumer;
  Session reducers are an advanced multi-turn primitive, not the golden path.
- Typed errors distinguish transport/API failures, terminal Invocation
  failures, Session-busy conflicts, and missing host-tool handlers while
  retaining safe wire details and request IDs.
- Typed SDK categories map `401` to `authentication`, `403` to `permission`,
  caller cancellation to `cancelled` when represented as an SDK error, and
  elapsed deadlines to `timeout`. Native task cancellation remains native in
  languages whose cancellation model requires propagation.
- The generated transport remains an explicit raw escape hatch and owns
  one-to-one operation coverage.

### Checked public mappings

This document is the single normative home for the handwritten SDK design.
`make sdk-check` holds the mappings below against shared fixtures and each
package's documented level. Generated names may shift with regeneration; the
facade names and meanings in this table are the supported contract.

| Concept | Shared meaning | TypeScript | Python | Go | Rust |
| --- | --- | --- | --- | --- | --- |
| Supported level | Never infer Agent parity from generated operation coverage. | Agent + bound Session + durable handle | Agent + bound Session + durable handle | Agent + bound Session + durable handle | Transport + durable handle; no Agent facade |
| Wait | Stop locally when `until` matches, the overall timeout expires, or the caller cancels. Poll bounds affect cadence only; no local stop cancels the Invocation. | `WaitOptions { until, timeoutMs, minPollIntervalMs, maxPollIntervalMs, signal }` | `WaitOptions(until, timeout, min_poll_interval, max_poll_interval)` plus native task cancellation | `WaitOptions{Until, Timeout, MinPollInterval, MaxPollInterval}` plus `context.Context` | `WaitOptions { until, timeout, min_poll_interval, max_poll_interval }` |
| Cancellation | Explicit handle cancellation is remote. Caller cancellation is local and never a timeout. | `AbortSignal`; SDK `cancelled` error | native `asyncio.CancelledError` | `context.Canceled`; SDK `ErrorCancelled` | SDK `Cancelled` when wrapped |
| Pagination | Opaque cursors are forwarded unchanged; iterators stop only when `has_more` is false and never invent cursors. | async generators for Invocations, Sessions, messages | async iterators for the same collections | iterator callbacks for the same collections | explicit page reads at the documented handle level |
| Callback tool | One nested wire-aligned callback target and no local handler. | `{ mode: "callback", callback: { url } }` | `CallbackTool(..., callback=CallbackTarget(url=...))` | `Tool{Mode: ToolModeCallback, Callback: &CallbackTarget{URL: ...}}` | `ToolMode::Callback { callback: CallbackTarget { url } }` |
| Errors | Preserve category, machine code, status when received from HTTP, request ID, retry delay, and safe details. | `NvokenError` | `NvokenError` | `nvoken.Error` | `NvokenError` |
| Raw access | Generated and intentionally less stable than the facade. | `raw` package namespace and `client.raw()` APIs | `client.raw()` generated API group | `client.Raw()` generated client | `client.raw()` generated configuration/APIs |
| Per-turn credentials | A facade admission selects one stored source or caller-ephemeral key; secret material never enters logs or results. | `InvokeRequest.providerCredentials` | `InvokeRequest.provider_credentials` | `InvokeRequest.ProviderCredentials` | `InvokeRequest::provider_credentials` |
| Bound Session | Serialize local admission until the preceding Invocation is terminal; the server remains authoritative across processes. | promise chain in `AgentSession` | `asyncio.Lock` | `sync.Mutex` | Not available without an Agent facade |
| Missing host handler | Cancel the parked Invocation before the typed error by default; an explicit opt-out leaves it waiting for another process. | `MissingToolHandlerError` | `MissingToolHandlerError` | `MissingToolHandlerError` | Manual handle workflow; no automatic dispatch |
| Text-only result | `text` requires assistant text. A typed no-output error identifies structured-only or tool-only completion and points to `run`. | `NoOutputTextError` | `NoOutputTextError` | `NoOutputTextError` | No Agent `text` verb |
| Stream preview | Provisional only; key by `(invocation_id, attempt, iteration, content_index)`, concatenate matching deltas, discard older attempts and resync gaps, and remove when canonical assistant state arrives. It never advances a durable cursor. | `ReducedSnapshot.previews` | `ReducedSnapshot.previews` | `ReducedSnapshot.Previews` | `ReducedSnapshot::previews` |

The shared `sdk/conformance/fixtures/reducer.json` vector proves durable cursor
retention separately from preview accumulation, attempt replacement, resync
discard, canonical-message replacement, and rejection of late deltas after a
terminal revision. The conformance server proves wait conditions and
park → submit → resume → settle behavior. TypeScript, Python, and Go perform
the dispatch loop automatically; Rust proves the same durable state changes
through wait-for-action and manual ToolCall result submission.

The ergonomic Agent layer is specified as five verbs: `text`, `run`, `invoke`,
`stream`, and `session`. A bound Session serializes turn admission in-process;
the server remains authoritative across bindings and processes. Schema-capable
languages accept an ecosystem-neutral schema protocol where one exists
(Standard Schema in TypeScript) and retain raw JSON Schema. TypeScript is the
reference implementation for this high-level layer; other languages adopt the
same meanings as their facades grow rather than inventing different workflows.

Facades use generated operations rather than duplicating routes. Admission
retries reuse the same immutable request and idempotency key. Result retries
reuse ToolCall IDs. Only transport failures, `408`, `425`, `429`, `500`, `502`,
`503`, and `504` are retried; `Retry-After` wins over bounded exponential
backoff with jitter. Typed errors preserve category, status, request ID,
machine code, safe details, and retry delay.

Streaming helpers use generated request builders or operation metadata, parse
SSE incrementally, retain only durable event IDs, reconnect after disconnect
or rotation with `Last-Event-ID`, and reconcile terminal state with an
authoritative read. Invocation streams yield typed
`invocation.accepted`, `output_text.delta`, `thinking.delta`,
`invocation.update`, `invocation.result`, `stream.resync`, and `stream.end`
events. The Session-stream reducer deduplicates messages by sequence and
Invocation changes by Invocation ID plus revision. Ephemeral deltas never
advance the resume cursor.

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

The initial packages remain pre-GA and may make coordinated contract breaks
while there are no external adopters. Runtime OpenAPI, generated transports,
handwritten facades, conformance fixtures, examples, and guides move together.
PRD 027 may add a separate generated identity/admin client and CLI credential
profiles without expanding the Runtime facade.
