# Execute remote MCP server tools during Invocations

**Status:** Draft
**Sequence:** 029
**Depends on:** `012-prd-durable-toolcall-and-checkpoint-model.md`,
`014-prd-checkpoint-crash-recovery.md`,
`015-prd-durable-client-tools.md`,
`016-prd-durable-callback-tools.md`, and
`028-prd-per-provider-credential-modes.md`

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

The durable ToolCall spine, checkpoint crash recovery, tool declarations and
fingerprinting, guarded public egress, and encrypted per-Invocation credential
bindings now exist, so a server-executed remote tool call can be an ordinary
fenced, checkpointed boundary instead of a new trust or recovery mechanism.
This is a deliberate, host-opt-in amendment to the "every tool with side
effects executes on your side" boundary, and it goes through the decision log.

## Outcome

An inline spec may declare up to eight remote MCP servers. During execution,
nvoken discovers each server's tools once, durably snapshots the filtered
catalog, presents the projected tools to the model alongside declared tools,
and executes selected calls in-turn under the engine lease as mode `mcp`
ToolCalls. Requests, results, attempts, and checkpoints land in the same
canonical transcript and recovery model as every other tool. Server
credentials are encrypted per-Invocation and destroyed at terminal settlement.
A stateless discovery endpoint lets hosts preview a server's projected tools
without creating an Invocation.

## Scope

**In:** inline `spec.mcp_servers` declarations, validation, and fingerprint v7;
encrypted per-Invocation MCP server credential bindings with terminal cleanup;
one durable tool-discovery snapshot per Invocation with allowlist and
projection rules; in-turn fenced execution through the coordinator boundary as
mode `mcp` with origin `mcp`; guarded public-only egress with per-call
deadlines and bounded text/structured results; annotation-gated crash-retry
policy; ToolCall read and stream exposure; a stateless host-facing discovery
endpoint sharing the execution-time discovery implementation; OpenAPI,
design-doc, and operational evidence.

**Out:** stdio or local-process MCP servers; the legacy SSE transport; OAuth
flows owned by nvoken (authorization, dynamic client registration, refresh);
stored or reusable MCP credential resources; mid-Invocation credential
rotation; persistent MCP sessions, notifications, subscriptions, resources,
prompts, sampling, elicitation, and roots; nvoken-side validation of results
against tool output schemas; image, audio, and binary result content;
per-tenant egress policy beyond the existing guard; SDK generation; cloud
staging proof.

## Requirements

- **R1 — Bounded server declarations.** `spec.mcp_servers` may contain at most
  8 entries, each with exactly `name`, `url`, optional `transport` (const
  `streamable_http`, the default), optional `allowed_tools` (1–32 unique
  remote tool names), optional `headers` (secret auth material), and optional
  `timeouts` (`discovery_seconds` default 10, max 30; `call_seconds` default
  30, max 120). Server names are unique, contain 1–24 ASCII letters, digits,
  underscores, or hyphens, and may not begin with `nvoken`. URLs are HTTPS, at
  most 2,048 bytes, with no userinfo or fragment. Headers hold at most 16
  entries of RFC 7230 token names, at most 8 KiB encoded, and hop-by-hop
  header names are rejected. `mcp_servers` may coexist with `spec.tools` and
  `spec.output`; declaring it makes the spec tools-bearing, so the existing
  two-iteration floor and default resolution apply. Unknown fields and
  transports are rejected before durable writes.

- **R2 — Admission identity without secret material.** New admissions use
  fingerprint v7. It preserves v6 order and inserts `mcp_servers` after
  `tools`; each ordered entry encodes `name`, `url`, `transport`, ordered
  `allowed_tools`, and resolved `timeouts`. `headers` is excluded entirely
  from canonicalization and from the durable spec snapshot, following the
  caller-ephemeral precedent of `028-prd-per-provider-credential-modes.md`:
  requests differing only in secret material are equal, and a deduplicated
  replay does not rebind credentials on the existing Invocation. An
  `mcp_servers`-bearing request cannot replay a retained v1–v6 row; earlier
  shapes remain comparable by their recorded versions. Compatibility vectors
  live in `docs/design/admission-fingerprint-v7.json`.

- **R3 — Encrypted per-Invocation server credentials.** Declared `headers`
  persist only as one credential binding per server per Invocation, sealed
  with the application-layer authenticated encryption and versioned external
  key introduced by `028-prd-per-provider-credential-modes.md`, and readable
  only by the execution path. No read, list, transcript, fixed-cut snapshot,
  SSE frame, error, or log surface ever discloses them. Terminal settlement
  destroys the bindings in the same transaction, and the 028 cleanup sweeper
  covers the crash window. There is no stored credential resource and no
  fallback: execution uses exactly what the admission bound, or the server is
  unauthenticated.

- **R4 — One durable discovery per Invocation.** Before the first model
  iteration that could observe MCP tools, the owning engine performs one
  discovery per declared server: a fresh MCP session (initialize, `tools/list`,
  close) through guarded egress under the discovery deadline. With
  `allowed_tools`, every listed name must be discovered and projectable or the
  Invocation settles `failed` with terminal error `mcp_discovery_failed`
  before any provider call. Without it, all discovered tools project, and
  non-projectable tools are excluded with recorded reasons. Projection exposes
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
  ceiling less the settlement reserve, and the Invocation wall deadline.
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
  window: it re-executes only when the snapshot marks the tool read-only or
  idempotent; otherwise it settles `failed` with system-origin evidence that
  the outcome is unknown, so the model decides how to proceed. Every
  execution write revalidates the fence; a stale engine's late result cannot
  append after a replacement has settled the call, and accepted results are
  never re-applied.

- **R8 — Stable contract, reads, and hygiene.** Mode `mcp` and origin `mcp`
  appear wherever ToolCall lifecycle is already exposed; the canonical
  transcript remains the single content record and no new pending projection
  is added, because MCP calls require no host action. OpenAPI documents the
  `McpServerSpec` shape and the error surface: admission failures are
  `400 invalid_request`, discovery failure is the terminal
  `mcp_discovery_failed`, and per-call failures surface as tool results, not
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

## Acceptance

- [ ] **A1 (R1, R2):** Strict admission tests accept 8 servers at every stated
  boundary and reject the ninth, bad names, the `nvoken` prefix, non-HTTPS or
  userinfo URLs, header and timeout violations, unknown fields and
  transports, and confirm the two-iteration floor and coexistence with
  `tools` and `output`. `docs/design/admission-fingerprint-v7.json` fixtures
  prove ordering materiality, secret exclusion, equal replay across differing
  headers, changed non-secret conflict, and v1–v6 compatibility.

- [ ] **A2 (R3):** Credential bindings are unreadable from raw storage,
  absent from every read, stream, error, and log surface, usable by the
  execution path across process restart, and destroyed in the terminal
  transaction, with the sweeper proven over an injected crash window.

- [ ] **A3 (R4):** Against a scripted MCP server, the discovery snapshot
  commits before the first model call; a missing allowlisted tool settles
  `mcp_discovery_failed` with no provider call; without an allowlist,
  non-projectable tools are excluded with recorded reasons; killing the
  engine before and after snapshot commit yields exactly one durable catalog;
  and mutating the server's tools mid-Invocation does not change the
  model-visible catalog.

- [ ] **A4 (R5, R6):** An end-to-end turn proves `tool_use`, ToolCall row,
  and checkpoint precede egress; one fresh session per call; in-bounds
  results settle with origin `mcp`; `is_error` passthrough lets the model
  continue; oversized, unsupported-content, timeout, redirect, private-IP,
  and plain-HTTP cases settle as documented without leaking payloads; and a
  mixed batch parks for a client sibling only after the MCP sibling settles.

- [ ] **A5 (R7):** Killing the engine at pre-dispatch, mid-call,
  post-response, and post-settle boundaries in both execution modes proves a
  read-only tool re-executes at most once more, a non-idempotent tool settles
  unknown-outcome without a second server hit (asserted by scripted server
  counts), and stale engines cannot append or settle.

- [ ] **A6 (R5, R8):** Cancellation and wall-deadline races against an active
  MCP call yield one winner; MCP time accrues to active execution and
  budgets; ToolCall reads and SSE expose mode and origin; logs contain IDs,
  counts, and codes only.

- [ ] **A7 (R9):** For the same declaration against the scripted server, the
  discovery endpoint returns a projection identical to the execution-time
  snapshot, including exclusions; it commits no rows and retains no
  credential material; malformed shapes return `invalid_request`; and
  unreachable, guard-rejected, timed-out, and allowlist-missing cases return
  `mcp_discovery_failed` with credential-free detail and nothing logged
  beyond IDs, counts, and codes.

- [ ] **A8 (R1–R9):** `make check` and the full Postgres suite pass. OpenAPI,
  `docs/design/api.md`, `architecture.md`, `claims.md`, the fingerprint
  fixtures, `docs/product/overview.md`, and `harness.md` describe the
  declaration, discovery endpoint, execution, recovery, and error semantics,
  and `docs/design/decisions.md` records the boundary amendment and the
  client library choice.

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
- Tool descriptions and results are untrusted model input from a third
  party, a prompt-injection surface like any tool result. nvoken bounds and
  never executes this content; the host chooses which servers to attach.
- Provider-native remote MCP connectors (Anthropic, OpenAI) were considered
  and rejected for v1: the provider would execute calls outside nvoken's
  ToolCall evidence, budgets, and recovery, with per-provider semantics.
- Local and stdio servers stay host-side behind client tools; a managed
  stdio posture raises process isolation questions that deserve their own
  PRD if demand appears.
