# Execute remote MCP server tools during Invocations

**Status:** Implemented
**Sequence:** 029
**Depends on:** `012-prd-durable-toolcall-and-checkpoint-model.md`,
`014-prd-checkpoint-crash-recovery.md`,
`015-prd-durable-client-tools.md`,
`016-prd-durable-callback-tools.md`,
`026-prd-multi-language-sdks-and-go-cli.md`,
`027-prd-machine-credentials-and-cli-device-auth.md`,
`028-prd-per-provider-credential-modes.md`, and
`034-prd-sdk-and-cli-foundation.md`
**Source proposal:**
[`2026-07-24-api-sdk-excellence.md`](../proposals/2026-07-24-api-sdk-excellence.md)
(Phase 2B)
**Independent review:** Claude Fable 5 on 2026-07-24; its findings about
error-code namespaces, aggregate discovery bounds, reserved headers, cleanup
sweeping, and SDK/CLI dependencies are incorporated.

## ELI5

A host can attach remote MCP servers to an Invocation. nvoken connects to each
server as an MCP client, shows the server's tools to the model, and executes
the selected calls itself during the turn. The host brings the server URL and
credentials. nvoken does not run OAuth consent or refresh flows, launch local
MCP processes, or hold long-lived MCP sessions; those stay with the host or a
later PRD.

## Why

Remote MCP servers have become the standard way products expose capabilities
to agents. Today nvoken can reach one only if the host proxies every call
through client or callback tools: the host must discover the server's tools
itself, stay reachable mid-turn, and forward each call to an endpoint it
already trusts nvoken to call on its behalf. That round trip erases much of
the value of a durable runtime for the common case where the integration is
"call this HTTPS MCP endpoint with this token." The harness catalog already
names "MCP servers adapted into tools" as an intended capability.

Mobius Cloud proves the narrow seam in production: remote-only MCP over
streamable HTTP, static bearer/API-key/header auth, one short-lived MCP
session per call, and host-owned OAuth token refresh. It also shows the costs
to avoid: re-discovering tools on every call, a tool surface that flaps when
a server is slow, and no durable evidence tying a call to its attempt. nvoken
ports the proven client seam (config shape, per-call sessions, annotation
mapping), not Mobius's integration and catalog resource model or its OAuth
broker.

The durable ToolCall spine, checkpoint crash recovery, tool declarations,
fingerprinting, the parked external-tool wait, guarded public egress, and
encrypted per-Invocation credential bindings now exist, so a server-executed
remote tool call can be an ordinary
fenced, checkpointed boundary instead of a new trust or recovery mechanism.
This is a deliberate, host-opt-in amendment to the "every tool with side
effects executes on your side" boundary, and it goes through the decision log.
The exception covers remote MCP servers the host explicitly attaches, nothing
broader: nvoken still executes no host or end-user code.

## Outcome

An inline spec may declare up to eight remote MCP servers. During execution,
nvoken discovers each server's tools once, durably snapshots the filtered
catalog, presents the projected tools to the model alongside declared tools,
and executes selected calls in-turn under the engine lease as mode `mcp`
ToolCalls. Requests, results, attempts, and checkpoints land in the same
canonical transcript and recovery model as every other tool. Server
credentials are encrypted per-Invocation and destroyed at terminal settlement.
A stateless discovery endpoint lets hosts preview a server's projected tools
without creating an Invocation. Every SDK exposes declarations and stateless
discovery, the TypeScript facade adds an ergonomic server helper, and the CLI
provides the same preview workflow.

## Scope

**In:** inline `spec.mcp_servers` declarations, validation, and fingerprint v8;
encrypted per-Invocation MCP server credential bindings with terminal cleanup;
one durable tool-discovery snapshot per Invocation with allowlist and
projection rules; in-turn fenced execution through the coordinator boundary as
mode `mcp` with origin `mcp`; guarded public-only egress with per-call
deadlines and bounded text/structured results; annotation-gated crash-retry
policy; ToolCall read and stream exposure; a stateless host-facing discovery
endpoint sharing the execution-time discovery implementation; generated SDK
surface in all four languages; handwritten declaration and discovery helpers;
the CLI discovery probe; one scripted example; OpenAPI, design-doc, and
operational evidence.

**Out:** stdio or local-process MCP servers; the legacy SSE transport; OAuth
flows owned by nvoken (authorization, dynamic client registration, refresh);
stored or reusable MCP credential resources; mid-Invocation credential
rotation; persistent MCP sessions, notifications, subscriptions, resources,
prompts, sampling, elicitation, and roots; nvoken-side validation of results
against tool output schemas; image, audio, and binary result content;
per-tenant egress policy beyond the existing guard; cloud staging proof.
Dedicated per-tenant discovery quotas and rate limits remain deployment
policy; this slice bounds one authenticated request to one server, one
discovery deadline, and at most 64 projected tools.

## Requirements

- **R1 — Bounded server declarations.** `spec.mcp_servers` may contain at most
  8 entries, each with exactly `name`, `url`, optional `transport` (const
  `streamable_http`, the default), optional `allowed_tools` (1–32 unique
  remote tool names), optional `headers` (secret auth material), and optional
  `timeouts` (`discovery_seconds` default 10, max 30; `call_seconds` default
  30, max 120). Server names are unique, contain 1–24 ASCII letters, digits,
  underscores, or hyphens, and may not begin with `nvoken`. URLs are HTTPS, at
  most 2,048 bytes, with no userinfo or fragment. Headers hold at most 16
  entries of RFC 7230 token names and at most 8 KiB encoded. Hop-by-hop
  headers plus `Host`, `Content-Length`, `Transfer-Encoding`,
  `Proxy-Connection`, `Proxy-Authorization`, `Cookie`, `Set-Cookie`, and
  `Mcp-Session-Id` are rejected because routing, framing, proxy, cookie, and
  MCP session state belong to the HTTP transport or MCP client.
  `mcp_servers` may coexist with `spec.tools` and `spec.output`; declaring it
  makes the spec tools-bearing, so the existing two-iteration floor and
  default resolution apply. Unknown fields and transports are rejected before
  durable writes.

- **R2 — Admission identity without secret material.** New admissions use
  fingerprint v8. It preserves v7 order and inserts `mcp_servers` after
  `tools`; each ordered entry encodes `name`, `url`, `transport`, ordered
  `allowed_tools`, and resolved `timeouts`. `headers` is excluded entirely
  from canonicalization and from the durable spec snapshot, following the
  caller-ephemeral precedent of `028-prd-per-provider-credential-modes.md`:
  requests differing only in secret material are equal, and a deduplicated
  replay does not rebind credentials on the existing Invocation. An
  `mcp_servers`-bearing request cannot replay a retained v1–v7 row; earlier
  shapes remain comparable by their recorded versions. Compatibility vectors
  live in `docs/design/admission-fingerprint-v8.json`.

- **R3 — Encrypted per-Invocation server credentials.** Declared `headers`
  persist only as one credential binding per server per Invocation, sealed
  with the application-layer authenticated encryption and versioned external
  key introduced by `028-prd-per-provider-credential-modes.md`, and readable
  only by the execution path. It is a distinct MCP binding record that reuses
  028's encryption machinery, not an extension of the model-provider binding
  schema or lifecycle. No read, list, transcript, fixed-cut snapshot,
  SSE frame, error, or log surface ever discloses them. Terminal settlement
  destroys the bindings in the same transaction. The 028 cleanup sweeper is
  extended to clear expired MCP binding material under the same expiry-grace
  rule, covering the crash window. There is no stored credential resource and
  no fallback: execution uses exactly what the admission bound, or the server
  is unauthenticated.

- **R4 — One durable discovery per Invocation.** Before the first model
  iteration that could observe MCP tools, the owning engine performs one
  discovery per declared server: a fresh MCP session (initialize, `tools/list`,
  close) through guarded egress under the discovery deadline, draining any
  `tools/list` pagination cursors to completion within it. The engine starts
  the at-most-eight discoveries concurrently, retains declaration order in
  the snapshot, and applies both each server's deadline and one aggregate
  deadline: the minimum of the largest declared `discovery_seconds`, the
  remaining segment ceiling less the settlement reserve, and the Invocation
  wall deadline. Aggregate wall duration, not the sum of concurrent request
  durations, accrues to active execution and segment budget accounting.
  Every declared server must complete discovery: an unreachable,
  guard-rejected, timed-out, aggregate-deadline, or protocol-failed server
  settles the Invocation `failed` with terminal error
  `mcp_discovery_failed` before any provider call, and there is no partial
  success across declared servers. With `allowed_tools`, every listed name
  must additionally be discovered and projectable, or the Invocation settles
  the same way. Without it, all discovered tools project, and non-projectable
  tools are excluded with recorded reasons. Projection exposes
  `{server}__{tool}`; the projected name must fit the existing tool-name
  charset within 64 characters and be unique across projected tools, declared
  tools, and reserved builtins. Descriptions truncate at 4,096 characters.
  Input schemas must be object-rooted, at most 32 KiB and 32 levels, and are
  forwarded to the provider without subset validation. MCP annotations
  (read-only, idempotent, destructive) are captured. More than 64 projected
  tools across servers is `mcp_discovery_failed`. The snapshot (projected
  name, remote name, description, schema, annotations, exclusions) commits
  under the Invocation fence before the first model checkpoint; replacement
  engines reuse it and never re-discover, so the model-visible catalog cannot
  change mid-Invocation. A crash before the snapshot commit re-runs discovery
  on the replacement owner.

- **R5 — In-turn fenced execution.** A model-selected MCP call passes through
  the existing model-checkpoint transaction with mode `mcp`: canonical
  assistant `tool_use` content, stable nvoken ToolCall IDs, request digests,
  usage receipt, and checkpoint commit before any egress. Execution then runs
  inside the active segment under the lease through the coordinator boundary:
  a fenced attempt row commits before dispatch, one fresh MCP session serves
  the single `tools/call` against the snapshot's pinned remote name, and the
  per-call deadline is the minimum of `call_seconds`, the remaining segment
  ceiling less the settlement reserve, and the Invocation wall deadline. MCP
  siblings in one model batch execute serially in batch order; nvoken never
  dispatches concurrent MCP calls under a single lease.
  Result acceptance appends the canonical tool message, settles the call with
  origin `mcp`, and appends a tool checkpoint in one fenced transaction under
  the first-accepted-result rule. MCP time is active execution time and
  accrues to segment and budget accounting. Model batch order is preserved;
  MCP siblings settle with a result or error before any park for client or
  callback siblings in the same batch; cancellation and wall-deadline
  settlement close open MCP calls with the existing canonical system-owned
  evidence.

- **R6 — Guarded egress and bounded results.** All MCP traffic uses the
  public-only guarded HTTP client from
  `016-prd-durable-callback-tools.md`: HTTPS only, post-resolution rejection
  of loopback, private, and link-local addresses, no redirects, and bounded
  DNS, connect, TLS, and total time. Text and structured content project into
  one bounded JSON `tool_result` value of at most 256 KiB and 32 nesting
  levels; the server's `isError` maps to `is_error` so the model can recover.
  Image, audio, and embedded-resource blocks, oversized or malformed
  responses, and transport, protocol, or timeout failures settle the call as
  a failed result with bounded canonical evidence that never echoes
  credentials or raw oversized payloads.

- **R7 — Recovery without double effects.** After lease loss or crash, the
  replacement engine rebuilds from transcript plus checkpoints. An open mode
  `mcp` ToolCall with no dispatched attempt executes normally. A call whose
  latest attempt was dispatched but never settled is in the uncertainty
  window: it re-executes only when the snapshot carries an explicit positive
  read-only or idempotent hint that is not contradicted by a positive
  destructive hint. Omitted, false, or contradictory annotations are treated
  as potentially mutating; those calls settle `failed` with system-origin
  evidence that the outcome is unknown, so the model decides how to proceed.
  Every execution write revalidates the fence; a stale engine's late result
  cannot append after a replacement has settled the call, and accepted
  results are never re-applied.

- **R8 — Stable contract, reads, and hygiene.** Mode `mcp` and origin `mcp`
  appear wherever ToolCall lifecycle is already exposed; the canonical
  transcript remains the single content record and no new pending projection
  is added, because MCP calls require no host action. OpenAPI documents the
  `McpServerSpec` shape, extends the tools-bearing spec definition to cover
  `mcp_servers`, and deliberately extends both closed error namespaces after
  PRD 033: `InvocationFailure.code` and request-level `ErrorCode` each gain
  `mcp_discovery_failed`. Admission failures are `400 invalid_request`,
  Invocation discovery failure settles with terminal
  `mcp_discovery_failed`, stateless discovery uses that request-level code on
  a documented `502`, and per-call failures surface as tool results, not
  Invocation errors. Structured logs record server name, ToolCall ID,
  attempt, duration, byte counts, and outcome codes only, and never URLs,
  headers, tool inputs, results, schemas, or descriptions.
  `docs/design/architecture.md`, `claims.md`, `api.md`, and
  `docs/product/overview.md` describe the fourth execution mode as a
  host-opt-in exception to the host-side-effects boundary, and
  `docs/design/decisions.md` records the amendment.

- **R9 — Stateless host-facing discovery.** `POST /v1/mcp/list-tools` accepts
  exactly one server descriptor in the R1 shape and runs one guarded
  discovery session through the same implementation as R4, so its projection
  matches what an identical Invocation declaration would snapshot. The
  response returns the projected catalog (projected name, remote name,
  description, schema, annotations) and recorded exclusions, bounded by the
  same 64-tool projection cap. The command is authenticated with existing
  runtime credentials, writes no rows, binds no credential material, and uses
  supplied headers once without logging them. Malformed requests return
  `400 invalid_request`; a server that is unreachable, guard-rejected, times
  out, fails the protocol, or is missing an allowlisted tool returns the same
  stable `mcp_discovery_failed` code an Invocation would settle with, carried
  on `502` with bounded, credential-free detail.

- **R10 — SDK, CLI, and example surface.** Generated transports in Go,
  TypeScript, Python, and Rust must expose the R1 declaration and R9 operation.
  Each handwritten facade must let callers add MCP servers to an execution
  spec and invoke stateless discovery without hand-building HTTP. TypeScript
  additionally exposes `mcpServer({name, url, headers, allowedTools, timeouts})`.
  `nvoken mcp list-tools --url ... [--header ...]` must use the same facade
  declaration and stable projected response as R9; headers are accepted from
  repeated flags or a named environment variable without being printed.
  A documented scripted-server example must prove discovery, one durable MCP
  call, fault-injected engine replacement, and authoritative result/transcript
  recovery. `make sdk-check` compiles or tests every public helper.

## Acceptance

- [x] **A1 (R1, R2):** Strict admission tests accept 8 servers at every stated
  boundary and reject the ninth, bad names, the `nvoken` prefix, non-HTTPS or
  userinfo URLs, token-invalid, oversized, hop-by-hop, routing, framing,
  proxy, cookie, and MCP session headers, timeout violations, unknown fields
  and transports, and confirm the two-iteration floor and coexistence with
  `tools` and `output`. `docs/design/admission-fingerprint-v8.json` fixtures
  prove ordering materiality, secret exclusion, equal replay across differing
  headers, changed non-secret conflict, and v1–v7 compatibility.

- [x] **A2 (R3):** Credential bindings are unreadable from raw storage,
  absent from every read, stream, error, and log surface, usable by the
  execution path across process restart, and destroyed in the terminal
  transaction, with the extended sweeper proven against the distinct MCP
  binding table over an injected crash window.

- [x] **A3 (R4):** Against a scripted MCP server, the discovery snapshot
  commits before the first model call; a missing allowlisted tool settles
  `mcp_discovery_failed` with no provider call; without an allowlist,
  non-projectable tools are excluded with recorded reasons; a declared tool
  named `alpha__beta` colliding with server `alpha` tool `beta` resolves at
  snapshot time (allowlisted: `mcp_discovery_failed`; otherwise excluded with
  reason), never at model selection; an unreachable second server fails the
  whole Invocation with no partial catalog; a paginated `tools/list` is
  drained across cursors; killing the
  engine before and after snapshot commit yields exactly one durable catalog;
  and mutating the server's tools mid-Invocation does not change the
  model-visible catalog. Eight concurrent slow servers remain within one
  bounded aggregate segment deadline, accrue aggregate wall duration to
  active execution, and fail before provider egress when that deadline is
  exhausted.

- [x] **A4 (R5, R6):** An end-to-end turn proves `tool_use`, ToolCall row,
  and checkpoint precede egress; one fresh session per call; in-bounds
  results settle with origin `mcp`; `is_error` passthrough lets the model
  continue; oversized, unsupported-content, timeout, redirect, private-IP,
  and plain-HTTP cases settle as documented without leaking payloads; and a
  mixed batch parks for a client sibling only after the MCP sibling settles.

- [x] **A5 (R7):** Killing the engine at pre-dispatch, mid-call,
  post-response, and post-settle boundaries in both execution modes proves a
  read-only tool re-executes at most once more, a non-idempotent tool settles
  unknown-outcome without a second server hit (asserted by scripted server
  counts), and stale engines cannot append or settle.

- [x] **A6 (R5, R8):** Cancellation and wall-deadline races against an active
  MCP call yield one winner; MCP time accrues to active execution and
  limits; ToolCall reads and SSE expose mode and origin; logs contain IDs,
  counts, and codes only.

- [x] **A7 (R9):** For the same declaration against the scripted server, the
  discovery endpoint returns a projection identical to the execution-time
  snapshot, including exclusions; it commits no rows and retains no
  credential material; malformed shapes return `invalid_request`; and
  unreachable, guard-rejected, timed-out, and allowlist-missing cases return
  `mcp_discovery_failed` with credential-free detail and nothing logged
  beyond IDs, counts, and codes.

- [x] **A8 (R1–R9):** `make check` and the full Postgres suite pass. OpenAPI,
  `docs/design/api.md`, `architecture.md`, `claims.md`, the fingerprint
  fixtures, `docs/product/overview.md`, and `harness.md` describe the
  declaration, discovery endpoint, execution, recovery, and error semantics,
  and `docs/design/decisions.md` records the boundary amendment and the
  client library choice.

- [x] **A9 (R9, R10):** Go, TypeScript, Python, and Rust tests construct an
  MCP-bearing spec and call stateless discovery through their documented
  facades; TypeScript proves the ergonomic helper; CLI integration proves
  repeated secret headers reach discovery but never output; and the scripted
  example survives engine replacement, then recovers the settled tool result
  through the composed result and fixed-cut transcript. `make sdk-check`
  compiles and exercises those surfaces at each package's documented level.

## Implementation evidence

Completed on 2026-07-24. Boundary and fingerprint fixtures live in
`internal/services/mcp_specs_test.go` and
`docs/design/admission-fingerprint-v8.json`. The disposable-Postgres cases in
`internal/adapters/postgres/mcp_integration_test.go` and
`toolcalls_integration_test.go` prove encrypted binding cleanup, fenced
catalog creation, safe retry, unsafe unknown-outcome settlement, and stale
owner rejection. `internal/services/mcp_execution_test.go`,
`internal/adapters/divegen/mcp_tool_test.go`, the official-client scripted
server tests, HTTP API tests, and the shared SDK conformance server cover
projection, egress ordering, bounds, stable errors, and credential-free
surfaces.

`make check`, `make test-postgres`, and `make sdk-check` pass. The latter
constructs MCP-bearing requests and stateless discovery through Go,
TypeScript, Python, Rust, and the CLI. The runnable
`sdk/go/examples/mcp-recovery` exercise supplies the delayed authenticated
server, process-loss injection point, Invocation-ID resume, composed result,
and fixed-cut transcript proof used by the recovery walkthrough.

## Risks and open decisions

- Client library: Mobius Cloud uses the official
  `modelcontextprotocol/go-sdk`; Dive's `experimental/mcp` module wraps
  `mark3labs/mcp-go`, is labeled experimental, and its OAuth path is
  interactive desktop-only. Default is the official Go SDK behind a small
  nvoken port, with MCP tools reaching Dive as ordinary tools rather than
  through Dive's MCP module. Recorded in the decision log at implementation.
- Token expiry: active turns finish well inside typical access-token
  lifetimes, so mid-turn expiry is not a practical concern. The real window
  is a parked Invocation: waiting time counts against the wall clock, not
  active execution, so an Invocation can resume MCP calls hours after
  admission bound the token. Expiry surfaces as recoverable `is_error`
  results. Hosts mitigate by keeping token lifetime above the wall-clock
  deadline; a credential-update command is deferred until that proves
  insufficient.
- Discovery pinning versus server drift: the snapshot guarantees a stable
  model-visible catalog, but the server executes its current tool. A tool
  renamed or removed after discovery settles as an error result. Accepted.
- Retry gating trusts server-asserted annotations: a server that wrongly
  marks a destructive tool read-only or idempotent can obtain a double
  effect after lease loss. This is a sharper trust assumption than callback
  tools, where the host owns the effect and its idempotency. The host
  chooses its servers, and nvoken cannot promise exactly-once over HTTP
  regardless. A host-controlled override (all tools non-retryable unless
  allowlisted as safe) is the natural follow-up if this proves too trusting.
- Effective call deadlines can sit far below `call_seconds`: on Cloud Tasks
  segments the remaining segment ceiling less the settlement reserve often
  dominates, and nvoken never checkpoints and chains mid `tools/call`.
  Hosts must size `call_seconds` and wall deadlines against the installation
  segment ceiling; the guides call this out explicitly.
- Tool descriptions and results are untrusted model input from a third
  party, a prompt-injection surface like any tool result. nvoken bounds and
  never executes this content; the host chooses which servers to attach.
- Provider-native remote MCP connectors (Anthropic, OpenAI) were considered
  and rejected for v1: the provider would execute calls outside nvoken's
  ToolCall evidence, limits, and recovery, with per-provider semantics.
- Local and stdio servers stay host-side behind host tools; a managed
  stdio posture raises process isolation questions that deserve their own
  PRD if demand appears.
