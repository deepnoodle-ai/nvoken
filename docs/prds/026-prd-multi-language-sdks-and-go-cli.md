# Generate four polished SDKs and the Go client CLI

**Status:** Draft
**Sequence:** 026
**Depends on:** `007-prd-recovery-and-transcript-reads.md`,
`011-prd-resumable-streaming.md`, `015-prd-durable-client-tools.md`, and
`016-prd-durable-callback-tools.md`

## ELI5

Hosts should not have to rebuild nvoken's HTTP, retry, cursor, tool, and
callback rules. nvoken generates complete low-level clients for Go,
TypeScript, Python, and Rust from one OpenAPI contract, then wraps them with
small idiomatic APIs for real workflows. A new Go `nvoken` CLI uses that same
client surface; the existing `nvokend` remains the service daemon.

## Why

The Runtime OpenAPI contract is authoritative, but adopters still must build
idempotent admission, typed errors, polling, pagination, SSE recovery, ToolCall
handling, callback verification, deduplication, and retry policy. Generated
bindings remove transcription, but alone do not provide excellent DevEx.

Mobius proves the pattern: generate wire types and operations, keep them
read-only, then add handwritten clients and CLI overrides. nvoken extends it to
Rust and OpenAPI 3.1. A local `oapi-codegen` v2.8.0 probe compiles with
configuration, but some 3.1 `const` fields lose specificity. Generated code is
the transport foundation, not the primary public design.

## Outcome

- One command generates models and every Runtime operation for all four
  languages reproducibly.
- Each language exposes an idiomatic durable-invocation facade plus a raw-client
  escape hatch.
- A Go `nvoken` CLI covers the same public Runtime workflows without
  maintaining a second HTTP contract.
- Shared tests prove equivalent wire and reliability semantics.

## Scope

**In:** the existing Runtime HTTP and callback-signing contracts; generated
request, response, schema, header, and operation clients in four languages;
handwritten workflow and safety helpers; a Go client CLI; drift checks; shared
fixtures; quickstarts; and package metadata.

**Out:** new runtime endpoints or semantics; generated server handlers;
identity/admin SDKs and CLI device login before that contract exists;
`nvokend` replacement; broad framework adapters; registry automation; and
long-term versioning policy.

## Requirements

- **R1 — One HTTP contract, complete generated transport.**
  `openapi/runtime.yaml` is the only source for public Runtime HTTP types and
  operations. Pinned generators produce models plus callable clients for every
  `operationId` in four languages, including typed headers and unions.
  Generated files are marked, committed, and never hand-edited.
  Internal execution, deployment, and Cloud control-plane routes produce no
  client code.

- **R2 — OpenAPI 3.1 is proven, not assumed.** Go generation uses
  `oapi-codegen` v2.8.0 or a later deliberately adopted version with runtime
  v1.6.0 or newer. Each generator must parse the checked-in 3.1
  contract and pass fixtures for nullable values, unions, `const`, binary SSE,
  and typed headers. Gaps use configuration, contract-compatible overlays, or
  facade types, never patches to generated output.

- **R3 — Packages are independently consumable.** Each language ships a
  consumable package. The Go SDK is a separate module from the
  daemon, so importing it does not inherit daemon dependencies. The `nvoken`
  binary consumes that module and remains distinct from `nvokend`. Repository
  layout and registry coordinates belong in the technical spec.

- **R4 — Curated APIs are the supported entry point.** Each package presents
  idiomatic naming, options, cancellation, and documentation rather than
  exporting the generator's shape as the product.
  The shared concepts are `invoke`, `get`, `list`, `wait`, `stream`, `resume`,
  `submitToolResults`, and `cancel`. `resume` reattaches to durable state by ID;
  it does not invent an execution-resume endpoint. Raw operation clients remain
  accessible for advanced use.

- **R5 — Reliability policy is built in.** Admission retries reuse the exact
  request and idempotency key. Tool-result retries preserve ToolCall identity.
  Automatic retries are limited to replay-safe requests and documented
  transient failures, honor `Retry-After`, use bounded jitter, and never retry
  semantic conflicts. Timeouts or caller cancellation stop local waiting only;
  they never imply Invocation cancellation.

- **R6 — Durable handles hide polling mechanics.** `invoke` returns a durable
  handle containing Invocation and Session identity. The handle can refresh,
  wait to a terminal state, stream, submit pending client-tool results, cancel,
  or be reconstructed later. Polling uses bounded backoff and server hints.
  List iterators preserve opaque cursors.

- **R7 — Streaming and callback safety are first-class.** Stream helpers parse
  SSE, reconnect from the last durable cursor, handle deliberate rotation, and
  reconcile terminal state from authoritative reads. A reusable reducer builds
  the same snapshot from replayed events in every language. Callback helpers
  verify signatures against the exact raw body before decoding, enforce the
  timestamp window, expose stable IDs, and provide a framework-neutral
  deduplication interface. The callback wire source is
  `docs/guides/callback-receivers.md` backed by `internal/signing/v1`; shared
  vectors live in `docs/design/callback-signing-v1.json`.

- **R8 — Errors are typed and actionable.** Facades normalize authentication,
  validation, not-found, conflict, rate-limit, server, transport, timeout, and
  unexpected-response failures while preserving status, request ID,
  `Retry-After`, machine code, and safe details. Users branch on categories,
  not messages.

- **R9 — The Go CLI consumes the Go SDK.** A new `nvoken` binary, separate from
  `nvokend`, provides human-readable and stable JSON modes for invoke,
  invocation inspection/wait/cancel, Session transcript/list/stream, and client
  ToolCall result submission. Baseline operation coverage derives from
  `operationId`; handwritten commands may improve names and workflows. The CLI
  must not duplicate transport rules. Before device auth, it reads the Runtime
  credential from `NVOKEN_API_KEY`; endpoint precedence is `--base-url`,
  `NVOKEN_BASE_URL`, config, then the documented local default.

- **R10 — Drift and parity fail CI.** A generation check fails on stale output.
  Shared language-neutral fixtures cover serialization, errors, callbacks,
  cursor replay, and reducer output. Pinned language toolchains run through a
  dedicated `make sdk-check`; CI requires it and the existing `make check`.
  Each language has an executable facade-only quickstart.

## Acceptance

- [ ] **A1 (R1, R2, R10):** A clean generation command produces byte-identical
  outputs. Contract changes update all four clients; stale output fails CI. The
  stale description saying callback tools are absent is corrected first.
- [ ] **A2 (R1–R4, R8):** All Runtime operations and schemas are
  reachable through compiling generated clients. 3.1 fixtures retain their
  intended constraints, quickstarts avoid generated-type leakage, and a Go SDK
  consumer does not inherit daemon-only dependencies.
- [ ] **A3 (R4–R6, R8):** Against one shared fault-injecting server, every SDK
  proves lost-ack admission retry, wait timeout without remote cancellation,
  resume-by-ID, cursor pagination, ToolCall result replay, explicit cancel, and
  typed 409/429/5xx behavior.
- [ ] **A4 (R7, R10):** Every SDK survives stream disconnect and rotation,
  resumes from the durable cursor, reconciles terminal state, and reduces the
  shared event fixture to an identical snapshot without duplicating messages
  or ToolCalls.
- [ ] **A5 (R7, R10):** Shared callback vectors pass for valid requests and fail
  for body, timestamp, delivery-ID, and signature tampering. The deduplication
  contract returns the stored result for a repeated ToolCall identity.
- [ ] **A6 (R9):** CLI tests cover Runtime workflows, configuration precedence,
  missing credentials, and text/JSON modes. No independently maintained route
  or payload definitions exist outside the generated client/SDK seam.
- [ ] **A7 (R1–R10):** Both check targets pass. Documentation records required
  toolchains and distinguishes generated transport, facade, raw client,
  `nvoken`, and `nvokend`.

## Risks and open decisions

- TypeScript, Python, and Rust operation-client generators against this exact
  3.1 contract require gated, recorded selection spikes; gaps are not a reason
  to weaken the contract or hand-maintain endpoints.
- Identical semantics matter more than identical language shapes. Cross-language
  fixtures should constrain behavior while allowing each ecosystem to feel
  native.
- Broad callback adapters and registry automation may follow after the core and
  package boundaries stabilize.
- Identity/admin and device auth remain a separate generated surface rather
  than turning the Runtime client into a control-plane catch-all.
