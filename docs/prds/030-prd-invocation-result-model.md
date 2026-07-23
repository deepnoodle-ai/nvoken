# One Invocation result model and the composed result read

**Status:** Implemented
**Sequence:** 030
**Depends on:** `007-prd-recovery-and-transcript-reads.md`,
`013-prd-structured-output.md`,
`015-prd-durable-host-tools.md`, and
`026-prd-multi-language-sdks-and-go-cli.md`
**Source review:**
[`2026-07-22-invoke-api-review.md`](../reviews/2026-07-22-invoke-api-review.md)
(R1)

## ELI5

Today, asking nvoken "what did the agent say" takes an invoke, status polls,
transcript pages, and a client-side filter. This PRD adds one read that
returns the whole answer for one Invocation: the authoritative Invocation
state, the turn's canonical messages, and the assistant text as one plain
string. Nothing is stored twice; the read composes the transcript at read
time. The misleading `output` field is renamed `structured_output` so text
turns stop looking like they produced nothing.

## Why

The invoke API review found the read side of the golden path inverted. A
schema-bearing Invocation gets its answer in one poll through the decision-26
projection. A plain text turn, the default case, never gets its answer on the
Invocation resource at all. The TypeScript SDK pages the entire Session and
filters client-side to recover one turn's reply. Mistral returns the answer
in the body of the one call its users make.

No decision chose this outcome. Decision 14 locked content into the canonical
transcript and decision 26 punched exactly one stored projection for
structured output. Text never got an equivalent. The single-representation
law bans storing a second copy of content. It does not ban composing a
read-time projection over the canonical rows. That distinction is the whole
fix.

This PRD is step one of the review's three-step arc: define the result, order
it, deliver it. The watermark and wait PRD, then the streaming PRD, reuse
this exact object as the payload of their delivery modes. Defining it once
prevents the text-versus-structured asymmetry from reappearing between
delivery modes.

## Outcome

`GET /v1/invocations/{invocation_id}/result` returns one slim
`InvocationResult`: the full authoritative Invocation, the turn's canonical
messages composed at read time, and an `output_text` convenience projection.
The read works at any status and is internally consistent. The blocking
JSON golden path becomes invoke, then one result read, then print.
`Invocation.output` and `Invocation.output_provenance` become
`structured_output` and `structured_output_provenance` everywhere the
resource appears, as a recorded pre-freeze breaking revision. SDK facades and
the CLI adopt the read; full-session paging disappears from the golden path.

## Scope

**In:** the `InvocationResult` contract object; the composed result read with
single-snapshot consistency, tenancy, and error semantics; the `output_text`
projection rules; the coordinated `structured_output` rename across
`Invocation` and `InvocationChange` and every surface embedding them;
OpenAPI, SDK facade, CLI, conformance-fixture, docs, and decision-log
updates.

**Out:** the Invocation watermark, `wait_seconds`, and `after_watermark`
(next PRD); SSE on create and the Invocation-scoped stream (streaming PRD);
an `invocation_id` filter on the Session messages list (demoted by the
review; it also requires a cursor-binding change); any stored text
projection (review R1d, composition first); renaming the request-side
`spec.output` declaration (fingerprint-material, deferred to the naming
sweep); queueing and busy-session changes.

## Requirements

- **R1 — One slim `InvocationResult`.** Define the contract object with
  exactly three required fields and `additionalProperties: false`:

  ```json
  {
    "invocation": { "...": "authoritative Invocation state" },
    "messages": [],
    "output_text": "Hello"
  }
  ```

  `invocation` is the full authoritative resource. After the R4 rename it
  already carries `structured_output`, its provenance, and
  `pending_tool_calls`; the result does not repeat any of them at the top
  level. Two copies of one value in one payload would be a new equality
  obligation. `messages` holds every canonical `SessionMessage` row owned by
  this Invocation, all roles, ascending `sequence`, composed at read time
  from the transcript. Nothing is persisted for this read, so decision 14 is
  intact. The shape must accept additive fields later: the watermark PRD adds
  the ordering field and the streaming PRD carries this same object as the
  terminal frame.

- **R2 — The composed result read.**
  `GET /v1/invocations/{invocation_id}/result` returns `InvocationResult`
  at any status, not only terminal. The Invocation row and its message rows
  are read in one repeatable-read snapshot so the payload cannot show a
  terminal status with a missing message tail or messages from a newer state
  than `invocation`. Authentication, tenant scoping, and the
  nondisclosing `not_found` rule match `getInvocation` exactly, as does the
  error response set. The read is side-effect free and cacheable by no one:
  no ETag or conditional semantics in this PRD, because ordering arrives
  with the watermark PRD.

- **R3 — `output_text` semantics.** `output_text` is the concatenation of
  the `text` content blocks of this Invocation's assistant-role canonical
  messages, in transcript order, joined without separators, exactly matching
  the existing SDK `Handle.text()` rule. It is non-null only when the
  Invocation is `completed` and at least one assistant text block exists;
  a completed schema-only turn with no assistant text yields null, and the
  host reads `invocation.structured_output`. Failed and cancelled
  Invocations keep their messages readable in `messages` as evidence, but
  `output_text` stays null: evidence must not masquerade as successful
  output. The projection is derived from canonical rows only, never from
  ephemeral deltas.

- **R4 — The `structured_output` rename.** Rename `Invocation.output` to
  `structured_output` and `Invocation.output_provenance` to
  `structured_output_provenance`, and the same two fields on
  `InvocationChange`, in one coordinated change. Every representation that
  embeds either schema follows: the Invocation get and admission-conflict
  details, the fixed-cut `TranscriptSnapshot`, and SSE frames carrying
  Invocation changes. No compatibility alias or dual field is served. The
  request-side `spec.output` declaration is untouched; it is
  fingerprint-material and its naming belongs to the pre-freeze sweep. The
  rename is a deliberate pre-freeze breaking revision recorded in the
  decision log, landed now while the adopter count is low. Internal
  database and Go identifiers may keep their names; this is a wire-contract
  rename.

- **R5 — SDK, CLI, and conformance adoption.** Regenerate the Go,
  TypeScript, Python, and Rust clients from the updated contract. Facades
  add `result()` on the durable handle and serve `text()` and the golden
  path from the one result read instead of paging the full Session;
  `listMessages()` returns `InvocationResult.messages`. The `nvoken` CLI
  exposes the result read beside the existing Invocation get. Cross-language
  conformance fixtures cover `InvocationResult`, the `output_text` rules,
  and the renamed fields, and the drift checks fail on any client still
  emitting `output`. Quickstarts print the answer from `output_text`.

- **R6 — Docs and decision log.** `docs/design/api.md` rewrites the golden
  path as invoke, result read, print, and documents the result read beside
  the existing recovery reads. `docs/design/architecture.md` and `claims.md`
  state the composition rule: read-time projection over canonical rows is
  permitted; decision 26 remains the only stored content projection.
  `docs/design/decisions.md` records two entries: the composition
  clarification against decisions 14 and 26, and the rename as a pre-freeze
  breaking revision. The root README and guides update their examples.

## Acceptance

- [x] **A1 (R1, R2):** Contract tests read the result at `queued`,
  `running`, `waiting`, `completed`, `failed`, and `cancelled`, and prove
  the three-field shape, strict unknown-field rejection, all-roles message
  composition in ascending sequence, and tenancy plus nondisclosing
  `not_found` parity with `getInvocation`. A settlement race test proves the
  snapshot rule: a result read concurrent with terminal settlement returns
  either the pre-terminal state or the terminal state with its complete
  message tail, never a mix.

- [x] **A2 (R3):** Projection fixtures prove concatenation order across
  multiple assistant messages and interleaved tool-use blocks, null for
  every non-`completed` status, null for a completed turn with no assistant
  text while `structured_output` is populated, and readable evidence
  messages with null `output_text` for failed and cancelled turns.

- [x] **A3 (R4):** No surface serves `output` or `output_provenance` on
  `Invocation` or `InvocationChange`: get and list reads, conflict details,
  the fixed-cut snapshot, and SSE frames all emit the renamed fields, proven
  by grep-level OpenAPI assertions and end-to-end reads. The decision log
  records the breaking revision.

- [x] **A4 (R5):** Conformance fixtures pass in all four SDKs; the
  TypeScript `text()` result equals the wire `output_text` for the same
  fixture; drift checks fail against a stale client; the CLI prints a
  result; the quickstarts run against the result read.

- [x] **A5 (R1–R6):** `make check` and the full Postgres suite pass, and
  the api, architecture, claims, decisions, README, and guide updates land
  together with the contract change.

## Risks and open decisions

- **Response size.** `messages` is complete and unpaginated. A tool-heavy
  turn with large bounded tool results can make the payload multiple MiB.
  Accepted for now: turns are bounded by iteration caps, token limits, and
  per-result size caps, and the paginated Session transcript remains the
  bulk read. Revisit with an opt-out parameter or size guard only on
  evidence, and before the watermark PRD makes this the polled read.
- **Null versus empty string.** A completed turn with no assistant text
  returns null, not `""`. Chosen so "no text existed" and "empty text" are
  not conflated and so SDK helpers can keep failing loudly on missing text.
  The wire alone carries that distinction: every SDK `text()` helper
  deliberately treats the empty string like missing text and fails with
  `unexpected_response`, and a host that needs the distinction reads
  `output_text` from the result. This helper behavior is contract, not
  accident; do not "fix" the helpers to return `""`.
- **Separator-free concatenation.** Joining text blocks with no separator
  can glue paragraphs across assistant messages. It matches the shipped SDK
  rule; hosts needing structure read `messages`. A joining rule change later
  would be a breaking projection change, so it must be decided before
  freeze.
- **Declare-read asymmetry.** Hosts declare `spec.output` and read
  `structured_output` until the naming sweep decides whether the request
  side becomes `spec.structured_output`. That rename touches fingerprint
  canonicalization, so it rides with a fingerprint version, not this PRD.
- **Separate path versus query flag.** The result could have been a
  `?include=result` variant of the plain get. A distinct path was chosen
  because the next PRD gives it wait semantics, and the plain get must stay
  a cheap status probe.
