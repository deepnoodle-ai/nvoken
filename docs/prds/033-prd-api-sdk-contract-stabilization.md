# Stabilize the pre-1.0 Runtime and SDK contract

**Status:** Ready
**Sequence:** 033
**Depends on:** `026-prd-multi-language-sdks-and-go-cli.md`,
`028-prd-per-provider-credential-modes.md`,
`030-prd-invocation-result-model.md`, and
`032-prd-model-catalog.md`
**Source proposal:**
[`2026-07-24-api-sdk-excellence.md`](../proposals/2026-07-24-api-sdk-excellence.md)
(Phase 1)
**Independent review:** Claude Fable 5 on 2026-07-24; its important
fingerprint-version finding is resolved by retaining v7 until the next
fingerprint-material addition, and its precision findings are incorporated.

## ELI5

Before nvoken adds more API breadth, this PRD makes the existing launch surface
say one thing everywhere. It fixes misleading error categories, an incomplete
list contract, provider-type drift, text joining, and SDK names while breaking
changes are still inexpensive. It does not add a new runtime capability.

## Why

The durable Invocation, credential binding, result read, and four generated
clients are already implemented, but several public seams teach conflicting
rules. A `403` looks like failed authentication, local cancellation can look
like a timeout, provider identifiers are closed in one schema and extensible in
another, and provider credentials cannot be listed past the first bounded
page. The `output_text` projection can silently glue separate assistant
messages together. Handwritten SDK facades also use the same names for
different scopes or behaviors.

These are contract defects rather than missing features. Each becomes more
expensive to correct after broader capability work and public adoption. This
slice resolves them together, preserves replay comparison for already durable
Invocations, and establishes the vocabulary that later PRDs must extend.

## Outcome

The Runtime contract, server, generated transports, handwritten SDK facades,
examples, and documentation present one deliberate pre-1.0 surface:
an Invocation is the durable resource for one agent turn; compatibility
preserves every admitted fingerprint version; errors, provider identifiers,
pagination, text composition, and SDK names have one observable meaning.

## Scope

**In:** Invocation/turn vocabulary; the admission-fingerprint compatibility
policy and v8 reservation; SDK error categories; provider-credential cursor
pagination; one extensible provider-identifier schema; `output_text`
composition; facade renames for Invocation text and Session message reads;
removal of leaked TypeScript run-loop and retry helpers; coordinated OpenAPI,
server, generated-client, conformance, example, decision-log, and migration
documentation updates.

**Out:** Agent identity endpoints; sampling or reasoning controls; multimodal
input; remote MCP tools; schema preflight; Session supersession; new stream
delivery modes; SDK Agent parity and broader ergonomics; new providers; and any
reset or deletion of retained fingerprint algorithms.

## Requirements

- **R1 — One Invocation vocabulary.** Public documentation must define
  **“An Invocation is one durable agent turn.”** Lowercase “turn” is the
  conceptual unit and never an API identifier or capitalized resource noun.
  `Invocation` is the durable resource and remains the public noun because it
  preserves the nvoken → invoke → Invocation naming chain. The concurrency
  rule must read: “a Session runs one turn at a time: at most one nonterminal
  Invocation.” The decision and rationale must be recorded once in the
  governing decision log by reconciling the existing terminology decision,
  not by creating a second definitional home.

- **R2 — Retained fingerprint lineage.** Fingerprint algorithms v1 through v7
  and their fixtures must remain available for equality comparison with rows
  admitted under those versions. Because this PRD changes no
  fingerprint-material request field, new admissions remain stamped v7; the
  next material shape must use v8 and add
  `docs/design/admission-fingerprint-v8.json` in that shape's governing PRD.
  A replay always uses the algorithm recorded on the durable row, so an equal
  legacy request remains equal and a materially changed request remains a
  conflict across process restart and upgrade. This PRD authorizes no lineage
  reset; deleting or rewriting a retained algorithm requires a separate
  retained-data migration and rollback decision.

- **R3 — Precise SDK error categories.** Every handwritten SDK must expose the
  same categories and mapping: HTTP `401` is `authentication`; HTTP `403` is
  `permission`; caller cancellation is `cancelled`; actual deadline expiry is
  `timeout`. Permission and cancellation are not retryable merely because of
  their category. This is facade normalization, not a wire-code rename:
  existing server codes `unauthenticated` and `forbidden` remain authoritative
  and are not collapsed by the SDK mapping.

- **R4 — Complete provider-credential listing.** `GET
  /v1/provider-credentials` must accept an opaque `cursor` and return the
  standard strict envelope `{items, has_more, next_cursor}`. `next_cursor` is
  non-null exactly when `has_more` is true. A cursor is bound to the Account,
  effective tenant scope, filters, and limit that created it; malformed or
  mismatched cursors fail as `400 invalid_request`. Ordering is fixed and not
  caller-selectable: descending `(created_at, id)` keyset order. Page traversal
  over an unchanged collection must return every matching credential exactly
  once without exposing secret material.

- **R5 — One extensible provider identifier.** Every Runtime contract position
  that names a model provider must reference one `ModelProvider` schema: an
  extensible, validated canonical string that older generated clients can
  decode without losing an unknown value. The `ModelProvider` schema name is
  retained and adopts the extensible pattern currently carried by
  `ModelCatalogProvider`; `ModelCatalogProvider` is deleted and its references
  are repointed. Schema openness does not imply executable support: Invocation
  admission and credential lifecycle requests must still reject a
  syntactically valid provider that the installation has not registered.

- **R6 — Deliberate `output_text` composition.** Within one assistant message,
  text blocks concatenate directly in content order. Distinct assistant
  messages belonging to the Invocation join with exactly `"\n\n"` in
  transcript order. Non-text blocks do not introduce separators. Existing
  completion and nullability rules remain unchanged: only a completed
  Invocation with assistant text has non-null `output_text`, and only canonical
  transcript messages participate. The language-neutral cases live in
  `sdk/conformance/fixtures/invocation-result.json` and are exercised by the
  server and all four facades.

- **R7 — Unambiguous facade names.** The read-only Invocation-handle accessor
  must be `outputText()` in TypeScript, `output_text()` in Python,
  `OutputText()` in Go, and `output_text()` in Rust. The Session-scoped client
  read must be `listSessionMessages`, `list_session_messages`,
  `ListSessionMessages`, and `list_session_messages`, respectively. A handle
  may retain its Invocation-scoped `listMessages` equivalent. The displaced
  handle `text` and Session-scoped client `listMessages` names must not remain
  as public compatibility aliases. TypeScript `Agent.runImmediately()` and
  `Client.replaySafe()` must become private implementation seams; `run()` is
  the only public run name. The TypeScript run-form `Agent.text(input)` and its
  bound-Session equivalent remain public; only the handle's read accessor is
  renamed. Generated transport operation IDs already named
  `listSessionMessages` remain unchanged; this requirement renames handwritten
  Session-scoped facade methods.

- **R8 — One coordinated release surface.** OpenAPI, server behavior, generated
  transports, SDK facade tests, shared behavior fixtures, CLI/example call
  sites, and active documentation must move together. The root README,
  `docs/design/api.md`, every SDK README, and relevant examples must lead with
  the host-owned identity tuple (`agent_key`, `tenant_key`, `session_key`,
  `idempotency_key`) and apply the Invocation/turn rule. Migration guidance
  in `docs/guides/api-sdk-migration.md` must list every intentional breaking
  rename and the error, pagination, provider, and `output_text` behavior
  changes; the `[Unreleased]` changelog records the user-facing revision when
  its PR number is known. The four-SDK claim must distinguish generated
  transport coverage from each handwritten facade's documented level.

## Acceptance

- [ ] **A1 (R1):** A repository-wide contract and documentation check finds the
  exact Invocation definition in the root README, `api.md`, and all four SDK
  READMEs; the existing terminology decision is updated to record the
  resource/concept rule and rationale; no public identifier or resource noun
  named `Turn` is introduced.

- [ ] **A2 (R2):** Fingerprint fixtures and service tests replay equal requests
  admitted as each of v1–v7 after upgrade, reject a material change for every
  retained version, stamp a new admission as v7, and identify v8 as the next
  version for the next fingerprint-material contract without deleting or
  rewriting any retained fixture.

- [ ] **A3 (R3):** Shared facade tests in Go, TypeScript, Python, and Rust prove
  `401 → authentication`, `403 → permission`, caller cancellation →
  `cancelled`, and elapsed deadline → `timeout`, including the category and
  original wire status where one exists.

- [ ] **A4 (R4):** An end-to-end list with more than two pages follows only
  returned cursors and reaches every credential exactly once; empty and final
  pages satisfy the envelope invariant, while a malformed cursor or a cursor
  reused with changed filters, scope, or limit returns `400 invalid_request`.

- [ ] **A5 (R5):** OpenAPI exposes one provider schema and generated drift is
  clean: `ModelProvider` is extensible and `ModelCatalogProvider` is absent.
  All four generated clients decode a fixture containing a future valid
  provider identifier unchanged; admission and provider-credential creation
  reject that same unregistered provider before durable work or secret storage.

- [ ] **A6 (R6):** `sdk/conformance/fixtures/invocation-result.json` carries the
  Appendix A case and passes through the server result read and all four SDK
  accessors as
  `"The charge was duplicated.\n\nA refund is queued."`; companion fixtures
  prove direct same-message block concatenation, non-text-block behavior, and
  unchanged nullability for nonterminal, failed, cancelled, and textless
  completed Invocations.

- [ ] **A7 (R7):** Compile-time or public-surface tests use the new names in all
  four SDKs and fail against the displaced facade names. The TypeScript package
  no longer exposes `runImmediately` or `replaySafe`, while `Agent.run()` and
  Invocation-scoped handle message reads still work.

- [ ] **A8 (R1–R8):** OpenAPI generation and drift checks, shared conformance
  in all four languages, `make check`, and `make sdk-check` pass. README,
  design API, SDK READMEs, examples, and
  `docs/guides/api-sdk-migration.md` contain no stale identifier or contradicted
  behavior from this revision.

## Risks and open decisions

- **Cursor stability under concurrent writes.** Exact-once acceptance applies
  to an unchanged matching collection. The cursor must use stable keyset
  ordering so inserts do not cause duplicate traversal; snapshot pagination is
  not required by this slice.
- **Generated raw clients are intentionally less stable than facades.** Schema
  and operation renames necessarily move generated identifiers. The migration
  guide must name them, while `raw()` remains the documented escape hatch
  whose exact generated shape may change on regeneration.
- **v8 is reserved, not minted by this slice.** Remote MCP currently owns the
  next fingerprint-material addition, so PRD 029 may retain v8 if reconciliation
  confirms it remains next. This reservation is not a lineage reset.
