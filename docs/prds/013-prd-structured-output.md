# Return Validated Structured Output

**Status:** Implemented

**Sequence:** 013

**Depends on:** `003-prd-durable-invocation-admission.md`,
`007-prd-recovery-and-transcript-reads.md`,
`008-prd-invocation-controls-and-budgets.md`,
`010-prd-cloud-tasks-invocation-execution.md`,
`011-prd-resumable-streaming.md`, and
`012-prd-durable-toolcall-and-checkpoint-model.md`

## ELI5

A host may attach a JSON shape to an Invocation and receive one validated
object instead of parsing assistant prose. The model submits that object
through a built-in nvoken tool, so the durable ToolCall path proves where the
value came from. This does not add host tools or crash resumption.

## Why

Hosts need machine-readable results for classification, extraction, routing,
and orchestration. Treating prose as JSON is ambiguous and makes validation a
host-side afterthought. PRD 012 now provides the persist-before-execution,
fencing, and transcript evidence needed to make structured output a reserved
builtin rather than a provider-specific response-format shortcut.

Mobius Cloud establishes useful precedent: validate a per-turn object schema
before admission, expose it as a reserved submit tool, and project the accepted
value on the terminal turn. nvoken keeps that product shape but uses its
first-class ToolCall/checkpoint records, includes the schema in idempotency,
and does not accept final-text JSON as a fallback.

## Outcome

An Invocation may declare `spec.output.schema`. A completed Invocation exposes
one schema-valid `output` plus provenance that binds it to the accepted durable
ToolCall. An Invocation that never submits a valid value fails with a stable,
diagnosable error instead of completing with an unverified result.

## Scope

**In:** the public inline output contract; a bounded self-contained schema
subset; admission validation and fingerprinting; one production reserved
builtin; correction after rejected submissions; durable ToolCall evidence;
atomic terminal output projection; Invocation, list, transcript, and stream
reads; Anthropic/OpenAI adapter projection; tests and design documentation.

**Out:** arbitrary tools; provider-native structured-response modes; non-object
root values; schema references or remote resolution; generated typed SDK
models; streaming partial objects; output repair after engine loss; automatic
Invocation retry; crash resumption; output indexing or querying.

## Requirements

- **R1 — Immutable public output contract.** `spec.output`, when present, must
  contain exactly one `schema` object and becomes part of the immutable
  execution-spec snapshot. Its compact canonical JSON must be at most 32 KiB,
  contain no more than 16 nested schema positions, and have an object root.
  Submitted request JSON may nest to 64 levels. `type` must be one string, and
  schemas may use only the documented self-contained subset: `type`,
  `title`, `description`, `properties`, `required`, `additionalProperties`,
  `items`, `enum`, `pattern`, `minLength`, `maxLength`, `minItems`,
  `maxItems`, `minimum`, and `maximum`. A `pattern` is limited to 1,024 UTF-8
  bytes. Keywords outside that list, malformed
  constraints, non-object roots, references, and empty schemas must be rejected
  before any durable admission write. Property names that equal keyword text
  remain valid.

- **R2 — Idempotent admission includes output.** A canonical digest of the
  schema must be stored with the Invocation. All new admissions, including
  output-free ones, must use fingerprint v3, which includes `spec.output` while
  leaving v1/v2 records comparable. Replaying an
  idempotency key with a semantically equal schema must return the original
  Invocation; changing, adding, or removing the output contract must return
  `409 idempotency_conflict`. Source member order and numerically equivalent
  JSON spellings must not change either digest. A request carrying output
  against a pre-v3 row must conflict even when its legacy fields match; a
  schema-free replay may still use that row's recorded v1/v2 algorithm.

- **R3 — One reserved durable builtin.** A schema-bearing generation must
  expose exactly one production builtin named `nvoken_submit_output`, with the
  host schema as its input schema and explicit instructions to call it for the
  final value. The builtin must use the PRD 012 coordinator: its assistant
  request commits before validation runs, and its success or error result
  commits before another model request. It performs no external side effect.
  Public `spec.tools` remains unsupported and the reserved name cannot be
  supplied or overridden by a host.

- **R4 — Validation and bounded correction.** Each submission must be a JSON
  object no larger than 256 KiB, nest to no more than 32 levels, and validate
  server-side against the snapshotted schema, independent of provider
  enforcement. A rejected value
  must become a durable ToolCall error that tells the model it may correct the
  submission without exposing the value in logs. The first valid submission is
  authoritative. A later reserved ToolCall must receive a durable success result
  saying output was already accepted and instructing the model to finish,
  without replacing the value. Final prose, including a JSON-looking or fenced
  object, is never accepted as the structured output.

- **R5 — Budget semantics support the tool round trip.** A structured-output
  Invocation must have room for the submit call and a following model response:
  an explicit `max_iterations` below two is invalid. When omitted, the resolved
  budget is `min(3, installation maximum)`; an installation maximum below two
  rejects schema-bearing admission. This allows one rejection and correction
  when the maximum is at least three. Normal iteration, token, cost, wall-clock, active,
  and segment limits remain authoritative; exhausting one after a valid
  submission still fails the Invocation and exposes no terminal output.

- **R6 — Atomic terminal value and provenance.** Only a `completed`
  schema-bearing Invocation may expose `output`. Settlement must revalidate the
  object and prove that it equals the accepted reserved ToolCall request before
  atomically writing the value, `source: "tool_call"`, ToolCall ID, and schema
  digest with the terminal status, usage, provenance, and lifecycle revision.
  This is an immutable caller-facing projection; the transcript remains the
  canonical conversation/replay record. Completion also requires Dive to end
  on a normal final assistant message after acceptance; repeated tools, empty
  output, or budget exhaustion still fail. Failed, cancelled, output-free, or
  stale settlement must leave output and output provenance null.

- **R7 — Clear unsatisfied-contract failure.** If generation ends without a
  valid submission, or only rejected submissions exist, settlement must fail
  with `error.code: "structured_output_unsatisfied"` and a bounded reason that
  distinguishes missing, invalid, and oversized submission without copying the
  schema or candidate value. Any checkpointed messages, tool errors, usage, and
  model provenance remain visible under the existing partial-output rules.

- **R8 — Authoritative recovery projection.** `GET /v1/invocations/{id}` and
  Invocation lists must return nullable `output` and `output_provenance` fields.
  The terminal Invocation change in the fixed-cut transcript and SSE snapshot
  must carry the same fields, ordered after the ToolCall transcript evidence by
  the existing message-before-lifecycle rule. Reads must never reconstruct the
  public value by parsing assistant text. Both fields are present and null on
  schema-free and non-completed Invocations.

## Acceptance

- [x] **A1 (R1, R2):** Admission tests accept schemas at the 32 KiB and
  16-schema-level boundaries and reject an empty/non-object schema, an
  oversized schema, every
  unsupported keyword in a schema position, invalid constraint types, and
  duplicate JSON members without creating a Session, snapshot, message, or
  Invocation. The next schema/request nesting levels fail, while a property
  literally named `const` or `if` remains valid.

- [x] **A2 (R2):** Fingerprint-v3 fixtures prove stable canonical bytes and
  digests. Equal schemas with reordered members and equivalent numbers dedupe;
  a changed, added, or removed output contract conflicts. Existing v1/v2
  fixtures and rows remain readable and comparable; specifically, adding
  output to a matching v2-era row conflicts. Canonical vectors live in
  `docs/design/admission-fingerprint-v3.json`.

- [x] **A3 (R3, R4, R5):** A scripted two-iteration model submits a valid
  object and then finishes. Before validation runs, Postgres contains the
  assistant request, reserved ToolCall, usage receipt, and model checkpoint;
  before the second model request it contains the completed attempt, tool
  result, and tool checkpoint. The public request cannot enable any other tool.

- [x] **A4 (R4, R7):** A scripted model first submits a missing-required-field
  object, receives a durable validation error, then corrects it within the
  default output-aware iteration budget. Submissions at or below 32 levels may
  be corrected; the next level and other missing, invalid, or 256 KiB-plus
  submissions that are not corrected fail with
  `structured_output_unsatisfied`; prose and fenced-JSON endings do not pass.
  Logs contain IDs, reason class, and sizes only—not schemas or values.

- [x] **A5 (R4, R6):** Concurrent or repeated equal completion of the accepted
  reserved call converges on one ToolCall result. A changed duplicate, stale
  lease, cancellation, deadline, budget stop, or reaper cannot publish output.
  A second valid ToolCall receives the durable already-accepted result and
  cannot replace it. Database constraints reject output on a
  non-completed Invocation, missing/mismatched provenance, or a completed
  schema-bearing Invocation without output.

- [x] **A6 (R6, R8):** Successful settlement writes output and provenance in
  the same transaction as `completed`; injected failure at each terminal write
  boundary rolls all of it back. Get/list responses and a restart-time
  fixed-cut transcript expose the identical object and ToolCall/schema binding,
  with the tool request/result messages preceding the terminal change.

- [x] **A7 (R1–R8):** The same scripted structured-output flow passes through
  embedded execution and the exact Cloud Tasks attempt handler. Anthropic and
  OpenAI tool-schema projections are covered without a live provider call.
  `make check` and the full Postgres integration suite pass. OpenAPI adds
  `spec.output`, nullable `output`/`output_provenance` on Invocation and
  InvocationChange, and `structured_output_unsatisfied`. A decision-log entry,
  architecture amendment, and sequencing-rule update define the equality-proven
  terminal projection as the single sanctioned exception to transcript-only
  content and distinguish it from crash resumption deferred to PRD 014.

## Risks and open decisions

- Provider tool-schema support is not identical. The accepted subset is
  deliberately no broader than nvoken can both project through Dive and
  validate itself; expanding it is a versioned contract change.
- The structured value appears inside the provider-required transcript
  ToolCall request and as a verified terminal projection. The transcript is
  authoritative for replay; the projection is authoritative for the host API
  and may only be written after equality proof in terminal settlement.
- A provider request may complete before its checkpoint commits. PRD 014 still
  owns reclaim and replay after engine loss; this PRD preserves evidence but
  does not resume the Invocation.
