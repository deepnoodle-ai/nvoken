# Deliver signed callback tools durably

**Status:** Implemented
**Sequence:** 016
**Depends on:** `012-prd-durable-toolcall-and-checkpoint-model.md`,
`014-prd-checkpoint-crash-recovery.md`, and
`015-prd-durable-host-tools.md`

## ELI5

An agent can ask nvoken to call a host-owned HTTPS endpoint. nvoken first saves
the ToolCall, then sends a signed, retryable request with stable IDs. A valid
response becomes the same durable tool result used by host tools, so any
engine can continue the parked Invocation after a crash or restart.

## Why

Hosts need server-to-server tools without polling and submitting every result.
The difficult part is not the POST: it is preserving one ToolCall identity
across crashes, ambiguous network outcomes, retries, cancellation, and another
engine's recovery. PRD 015 now provides the safe parked-Invocation and result
spine on which callback delivery can terminate.

Mobius Cloud provides useful narrow precedent: HMAC-SHA256 over the exact raw
body plus stable delivery ID and timestamp; explicit secret reference/version
headers; refusal of redirects; dial-time rejection of private or reserved
addresses; and retries for transport errors, 408, 429, and 5xx. nvoken ports
those seams, not Mobius's Action, integration, org, project, or secret-resource
models.

## Outcome

An inline spec may declare a callback tool with a public HTTPS endpoint. When
selected, nvoken parks the Invocation, durably delivers a signed request from a
combined-process worker, validates the response, commits the result through the
existing checkpoint path, and queues the same Invocation for continuation.

## Scope

**In:** callback declarations and fingerprinting; one installation HMAC key;
versioned request envelope and signature; public-only egress; a Postgres
delivery outbox with leases, retries, recovery, and retention; strict response
validation; mixed callback/client waits; cancellation/deadline convergence;
combined-role worker; Google Secret Manager wiring; OpenAPI and operations.

**Out:** callback CRUD; per-tool credentials; raw secrets in specs or Postgres;
JWKS/public-key signing and automatic key rotation; private-network callbacks;
OAuth; delegated actor claims not yet present in admission; callback streaming;
Cloud Tasks for callback transport; exactly-once external effects; SDKs; cloud
staging proof.

## Requirements

- **R1 — Immutable callback declarations.** `spec.tools` continues to allow at
  most 32 ordered tools. A callback item has the existing `name`,
  `description`, `input_schema`, and `mode: "callback"` fields plus exactly
  `callback: {"url": "https://..."}`. Client items must omit `callback`.
  Callback URLs are at most 2,048 bytes, contain no userinfo or fragment, and
  use HTTPS. Unknown fields, unsupported modes, or callback declarations when
  callback signing is not configured are rejected before writes. Callback and
  host tools may coexist. All new admissions use fingerprint v5. It preserves
  v4 order and encodes `callback` after `input_schema` as `null` for client
  tools or `{"url": ...}` for callback tools. A request containing no callback
  tool may replay a retained v1–v4 row using that row's recorded version; a
  callback-bearing request cannot. Language-neutral vectors live in
  `docs/design/admission-fingerprint-v5.json`. The exact snapshotted
  declaration drives delivery.

- **R2 — Persist, park, then deliver.** For each callback ToolCall in a model
  batch, the model-checkpoint transaction must commit the canonical assistant
  request, stable ToolCall, and exactly one stable callback-delivery row unique
  by ToolCall ID before an outbound request is possible. Rows start blocked.
  The fenced `running → waiting` transaction activates every callback row in
  the batch only after clearing Invocation ownership and settling any execution
  dispatch. A crash before parking lets a replacement park and activate the
  same rows without another model call. Postgres polling is the correctness
  path; process-local notification may only reduce latency.

- **R3 — Stable signed protocol.** Every transport retry uses the same delivery
  ID and ToolCall ID. The exact compact JSON body has `schema_version: 1`, a
  runtime context containing delivery, ToolCall, Invocation, Session, Agent
  reference, and optional tenant key, plus the canonical tool `input`.
  nvoken signs `v1.<delivery_id>.<unix_timestamp>.<raw_body>` with HMAC-SHA256
  and sends `X-Nvoken-Signature`, `X-Nvoken-Signature-Version`,
  `X-Nvoken-Timestamp`, `X-Nvoken-Delivery-Id`,
  `X-Nvoken-Signing-Key-Id`, and `X-Nvoken-Signing-Key-Version` headers.
  `Idempotency-Key` is the ToolCall ID, because the host must deduplicate its
  external effect by ToolCall, not by one transport attempt. Delivery IDs are
  prefixed UUIDv7 values using `cbdy_`. Each retry keeps both IDs and the exact
  body stable but generates a fresh timestamp and signature; receiver guidance
  requires verification before parsing and a five-minute clock-skew window.
  The v1 context reserves an optional `actor` object for future admission-owned
  delegated identity and omits it today. The signing key is installation
  configuration, never persisted or logged; the configured key ID/version are
  nonsecret evidence. JWKS rotation is the next signing scheme, not an unproved
  claim in this slice.

- **R4 — Public-only bounded egress.** Admission performs structural URL
  validation, while every actual dial resolves the hostname under a bounded
  setup context and rejects loopback, private, link-local, carrier-grade NAT,
  benchmark, multicast, unspecified, and reserved addresses. Ambient proxies
  and redirects are disabled. A request has independent DNS/connect/TLS bounds
  and a 10-second total deadline. Responses are capped at 256 KiB and drained
  or closed promptly. Logs never contain URLs, bodies, schemas, results,
  signing material, or response snippets.

- **R5 — Lease-fenced bounded delivery.** A combined-role worker claims due
  rows with `FOR UPDATE SKIP LOCKED`, a unique owner, monotonically increasing
  attempt, and expiring lease. Network I/O occurs outside a transaction.
  Transport errors, 408, 425, 429, and 5xx return the same row to pending with
  persisted exponential backoff, capped at five attempts. Other non-2xx
  responses and malformed successful responses are permanent tool errors.
  An expired delivery lease is recoverable; only its current fence may retry
  or settle. A crash after the host effect but before acknowledgement can cause
  another POST with the same IDs, so the host owns idempotency. Terminal rows
  are retained seven days and pruned in bounded batches. A callback ToolCall's
  deadline is the Invocation wall-clock deadline already persisted on every
  ToolCall. No claim or retry may begin at or after it; a due or in-flight row
  that reaches it is settled through the same bounded model-visible error path
  as retry exhaustion, unless Invocation deadline settlement wins first.

- **R6 — Response becomes canonical tool evidence.** A 2xx response must use
  JSON and contain exactly `content` plus optional `is_error`; `content` follows
  the client-result 256 KiB and depth-32 bounds. Under Session → Invocation →
  ToolCall → delivery locks, the current delivery fence appends one canonical
  tool-result message, settles the ToolCall with callback origin, appends its
  checkpoint, and terminalizes the delivery in one transaction. Permanent or
  exhausted failures commit a bounded, model-visible `is_error` result without
  embedding remote response content. First terminal result wins.

- **R7 — Reuse the parked-Invocation transition.** Waiting recovery accepts
  pending callback and client calls from the current iteration; only client
  calls appear on the public pending-result projection. Partial callback or
  client results leave the Invocation waiting. Closing the final external call
  atomically queues the same Invocation and, in external execution mode,
  creates its successor execution dispatch. A later engine reconstructs
  results in original model batch order and continues cumulative limits.

- **R8 — Terminal controls dominate undelivered work.** Cancellation or wall
  deadline settlement abandons every active callback delivery and closes its
  ToolCall in the same terminal transaction. A late HTTP response or stale
  delivery fence cannot append evidence, queue work, or overwrite terminal
  state. Shutdown stops new claims, allows bounded in-flight requests to
  settle within the process budget, then leaves uncertain rows for lease
  recovery.

- **R9 — Paved configuration and observability.** Callback delivery is disabled
  unless the combined process has a signing key, nonsecret key ID, and positive
  key version. The Google module accepts an existing Secret Manager secret,
  grants only the runtime service account access, and never exposes the key to
  the private Invocation executor or migration job. Configurable worker
  concurrency, intervals, leases, and retry bounds have safe defaults and
  startup validation. Structured events and counts distinguish claim, retry,
  success, permanent failure, exhaustion, stale fence, recovery, and prune by
  IDs and reason codes only.

## Acceptance

- [x] **A1 (R1, R3):** Strict admission/OpenAPI tests cover client/callback
  coexistence, all field/count/URL boundaries, disabled signing, and v5 replay
  fixtures in `docs/design/admission-fingerprint-v5.json`. Signature vectors
  prove exact-body verification and body,
  timestamp, delivery-ID, and key tampering failure; headers expose stable IDs
  and no secret.
- [x] **A2 (R2, R5):** Postgres tests prove checkpoint plus blocked delivery is
  atomic, parking activates it atomically, 20 workers claim it once, and a
  checkpoint/park/process restart sends the same IDs without another model
  call. No request occurs while the Invocation is running.
- [x] **A3 (R4, R5):** Transport tests reject every prohibited address after
  DNS resolution, redirects, and proxy use; bound DNS/connect/TLS/request/body
  handling; and prove retry classification, persisted backoff, five-attempt
  exhaustion, lease takeover, and bounded pruning.
- [x] **A4 (R6, R7):** Valid, error, malformed, oversized, deep, permanent-4xx,
  and exhausted responses produce the required atomic evidence. Mixed callback
  and client batches remain waiting until the last result, then queue once;
  another engine continues with batch-ordered results and unchanged limits.
- [x] **A5 (R5–R8):** Duplicate/late delivery, cancellation, deadline, lease
  expiry, and crash-at-send/settle races converge to one ToolCall result and at
  most one resume dispatch. Stale delivery owners commit nothing, and ambiguous
  retries retain the same ToolCall idempotency key.
- [x] **A6 (R9):** Role/config and Terraform tests prove the signing secret is
  combined-only, optional when callbacks are unused, and required before
  callback admission. The combined Cloud Run service retains nonzero capacity
  and instance-based CPU for its Postgres worker; the private executor has no
  signing key.
- [x] **A7 (R1–R9):** `make check` and the full Postgres suite pass. API,
  architecture, claims, decisions, deployment, and callback receiver docs
  describe verification-before-parse, timestamp tolerance, ToolCall
  idempotency, retry ambiguity, result shape, egress limits, HMAC rollout, and
  JWKS deferral. The decision log and packet also qualify delegated actor
  delivery as conditional on a future admission-owned claim and record the
  optional v1 reservation. A log-capture test finds no URL, input, output, or
  key material.

## Risks and open decisions

- HMAC verification requires distributing one shared installation key to the
  receiver. JWKS/public-key signing should replace that distribution problem in
  a later, separately proven scheme while retaining versioned headers.
- Exactly-once HTTP effects are impossible across a lost response. Receivers
  must store the first result by ToolCall ID and replay it for equal retries.
- Public-only egress intentionally excludes private VPC callbacks. A future
  deployment policy may add explicit private destinations without weakening
  the default dial-time checks.
