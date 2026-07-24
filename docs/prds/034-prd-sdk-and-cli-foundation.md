# Make the SDKs and CLI one durable-workflow product

**Status:** Ready
**Sequence:** 034
**Depends on:** `026-prd-multi-language-sdks-and-go-cli.md` and
`033-prd-api-sdk-contract-stabilization.md`
**Source proposal:**
[`2026-07-24-api-sdk-excellence.md`](../proposals/2026-07-24-api-sdk-excellence.md)
(Phase 2A)
**Independent review:** Claude Fable 5 on 2026-07-24; its findings about the
checked-design authority, Rust's supported conformance level, shared
missing-handler behavior, and the post-Phase-1 Python baseline are
incorporated.

## ELI5

nvoken already has four generated clients, but their friendly layers do not
yet teach the same workflow. This PRD makes TypeScript the proven reference,
ports its meanings to Python and Go, gives Rust an honest ergonomic floor, and
brings the CLI along. It does not add a Runtime endpoint or claim identical
high-level APIs where a language has not implemented them.

## Why

PRD 026 established complete generated transports and durable handles, and
PRD 033 stabilized their vocabulary. TypeScript has since grown a high-level
Agent workflow, typed structured output, host-tool dispatch, bound Session
serialization, and collection iterators. Python, Go, Rust, and the CLI expose
different subsets and shapes. Several remaining reference-path defects also
matter on first use: `run()` polls after admission instead of using the
available completion stream, a missing local tool handler can leave a Session
parked, and text-only convenience calls cannot explain a valid structured-only
result.

This slice establishes one checked SDK design and proves common behavior with
language-neutral fixtures before expanding the individual facades. Parity
means shared concepts have one meaning; it does not require unidiomatic
signatures or pretending Rust has an Agent layer.

## Outcome

TypeScript, Python, Go, Rust, and the Go CLI present documented,
language-appropriate levels of one durable Invocation workflow. Shared
fixtures constrain waits, errors, cursors, previews, composed results, and
host-tool dispatch. Every package claim is executable at the level it names.

## Scope

**In:** the checked cross-language SDK design; behavior-level conformance;
TypeScript reference-facade completion; Python and Go Agent facades; Rust
handle/request ergonomics; facade operation-coverage gaps; Go CLI durable
workflow commands; examples, point-of-use guidance, troubleshooting, and
smoke checks.

**Out:** Runtime OpenAPI additions; new durable server state; remote MCP
execution; Agent identity endpoints; Session supersession; new model controls;
registry publication; a Rust Agent facade; framework-specific tool adapters;
and device-auth changes.

## Requirements

- **R1 — One checked design and behavior floor.**
  `docs/codebase/sdk-and-cli.md` is the sole public SDK design and must assign
  every shared concept one meaning with idiomatic mappings for all four
  languages. It must define wait conditions and overall local timeout, native
  cancellation, pagination, callback tools, error fields, generated raw
  access, per-turn credentials, Session serialization, missing-handler
  cancellation and opt-out, no-output-text errors, and reducer preview
  lifecycle. Language-neutral fixtures must prove error mapping, durable
  cursor retention, delta accumulation and discard, wait conditions,
  `output_text`, and the park → submit → resume → settle loop at each
  package's documented level.

- **R2 — TypeScript is a complete reference.** `Agent.run()` and `text()` must
  use create-and-stream for the ordinary path, dispatch host tools during the
  stream, and reconcile through authoritative reads after a disconnect or
  incomplete terminal stream. Missing handlers cancel the parked Invocation
  before throwing by default, with an explicit opt-out for another process.
  Text-only calls must throw a typed `NoOutputTextError` that identifies
  structured-only and tool-only completions. Principal Runtime nouns must be
  exported, settled results must carry non-optional known IDs, stream
  consumption must accept an overall timeout, and reducer previews must be
  replaced by canonical messages or discarded on resync and attempt change.

- **R3 — Python reaches Agent parity.** The async Python facade must expose
  `text`, `run`, `invoke`, `stream`, and `session` with the TypeScript meanings;
  serialize bound Session admission in-process; dispatch host tools; expose
  typed structured output without hiding raw JSON; cancel on a missing handler
  by default with an opt-out; and distinguish structured-only or tool-only
  completion in `text`. It must add transcript page/drain reads,
  provider-credential lifecycle operations, the remaining collection
  iterators, shared wait controls, and reducer previews.
  `asyncio.CancelledError` remains native.

- **R4 — Go reaches Agent parity idiomatically.** The Go facade must expose the
  same five Agent verbs with a bound Session mutex, typed tool modes,
  facade-owned collection types, host-tool dispatch, configurable waits and
  reducer previews, missing-handler cancellation with opt-out, and typed
  no-output-text behavior. Structured output remains `json.RawMessage` and has
  a generic decoding helper rather than requiring generated models.

- **R5 — Rust has a usable, honestly scoped floor.** The Rust facade remains
  transport plus durable handle, not an Agent facade. Handles must allow
  stream-plus-act without an exclusive mutable borrow; core request/spec types
  must have builders or defaults; polling cadence and conditions must be
  configurable; callback failures must be typed; and the README must state the
  exact supported level and remaining gaps.

- **R6 — The CLI demonstrates the same workflow.** Text mode must admit and
  print one answer, render text deltas, wait until actionable, recover a
  Session by host keys, and display readable transcript text. Admission must
  accept a complete spec file containing the exact public wire `spec` object,
  without introducing a second schema or changing fingerprint material.
  Existing JSON output remains stable. Point-of-use help must state concurrency,
  idempotency, streaming recovery, and actionable failure rules.

- **R7 — Claims and examples are executable.** The repository must include an
  Agent-facade example, stream guarantees and troubleshooting, provider-API
  migration guidance, and a model-capability check workflow. SDK READMEs must
  distinguish generated transport, durable-handle facade, and Agent facade.
  `make sdk-check` must exercise every common behavior at each package's
  documented level.

## Acceptance

- [ ] **A1 (R1):** `docs/codebase/sdk-and-cli.md` names each shared concept
  once and records idiomatic mappings for waits and local timeout,
  cancellation, pagination, callbacks, errors, raw access, credentials,
  Session serialization, missing handlers, no-text results, and previews.
- [ ] **A2 (R1):** Shared fixtures pass at every package's documented level for
  error mapping, durable cursor retention, delta accumulation/resync,
  wait-until, and `output_text`. TypeScript, Python, and Go prove automatic
  park → submit → resume → settle dispatch; Rust proves wait-for-action plus
  manual durable ToolCall result submission through its handle.
- [ ] **A3 (R2):** TypeScript `run()` and `text()` settle through
  create-and-stream; forced disconnect and deliberate stream rotation produce
  the same authoritative result without duplicate host-tool dispatch.
- [ ] **A4 (R2):** A missing TypeScript handler cancels before its typed error
  by default, the opt-out preserves waiting work, and `NoOutputTextError`
  distinguishes structured-only and tool-only completion.
- [ ] **A5 (R2):** TypeScript exports the principal Runtime nouns; settled
  result IDs are non-optional; `stream({timeoutMs})` stops locally; and shared
  reducer vectors prove preview replacement and discard.
- [ ] **A6 (R3):** Python tests prove the five Agent verbs, bound Session
  serialization, transcript/Session reads, provider-credential lifecycle,
  host-tool dispatch, missing-handler policy, no-text results, structured
  output, wait controls, previews, collection iterators, and native
  cancellation.
- [ ] **A7 (R4):** Go tests prove the five Agent verbs, bound Session
  serialization, typed tool modes, facade-owned list types, structured-output
  decoding, host-tool dispatch, missing-handler policy, no-text results, wait
  controls, and previews.
- [ ] **A8 (R5):** Rust tests prove shared handle access during streaming,
  request/spec builders or defaults, configurable polling, and typed callback
  errors; its README makes no Agent-facade claim.
- [ ] **A9 (R6):** CLI integration tests prove answer printing, delta
  rendering, actionable waits, host-key Session recovery, readable
  transcripts, exact-wire spec-file admission with fingerprint-equivalent
  material, and unchanged JSON output.
- [ ] **A10 (R7):** The Agent example, stream/troubleshooting guide,
  concurrency/idempotency point-of-use help, provider migration, and model
  check workflows pass their documented smoke paths.
- [ ] **A11 (R1–R7):** `make check` and `make sdk-check` pass, and no SDK README
  claims a higher-level facade than its package exports.

## Risks and open decisions

- Create-and-stream completion is an optimization over durable authoritative
  state. Disconnect recovery must never make provisional stream state the
  result authority or duplicate a ToolCall result.
- Bound Session serialization coordinates only one local binding. The server's
  one-nonterminal-Invocation rule remains authoritative across processes.
- The shared reducer preview is explicitly provisional. It must not advance a
  durable cursor, enter the canonical transcript, or survive a resync boundary.
