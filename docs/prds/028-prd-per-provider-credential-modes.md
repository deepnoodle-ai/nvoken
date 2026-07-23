# Resolve model credentials per Invocation and provider

**Status:** Implemented
**Sequence:** 028
**Depends on:** `003-prd-durable-invocation-admission.md`,
`008-prd-invocation-controls-and-limits.md`,
`014-prd-checkpoint-crash-recovery.md`, and
`027-prd-machine-credentials-and-cli-device-auth.md`

## ELI5

A host can choose who pays for the model provider used by each durable
Invocation: the caller, the host Account, one host tenant, or nvoken Cloud.
nvoken keeps only the secret material needed to survive background execution
and recovery, then removes ephemeral material. This does not create a general
vault for integrations or tool credentials.

## Why

The current executor reads one Anthropic or OpenAI key from installation
configuration and records fixed provenance `installation_byok`. That works for
self-hosting but not nvoken Cloud, where one multi-tenant host may use its own
OpenAI key, a particular tenant's Anthropic key, an Invocation-supplied key,
and platform-funded models at different times.

An Invocation returns `202` before execution and may resume on another worker.
A caller-supplied credential therefore cannot live only in admission-handler
memory. Credential selection and any ephemeral ciphertext must be durable
without entering the execution spec, transcript, logs, or general secret
storage. Mobius Cloud proves encrypted BYOK plus platform credential resolution,
but nvoken requires explicit source selection rather than Mobius's automatic
BYOK-first fallback because an unexpected fallback changes who pays.

## Outcome

The provider selected by an admitted Invocation has one durable credential
binding. nvoken can execute and recover with caller-ephemeral, reusable Account
BYOK, reusable tenant BYOK, platform, or existing self-hosted installation
credentials while preserving isolation, explicit billing provenance, and
bounded secret retention.

## Scope

**In:** canonical provider identity; encrypted, versioned Account and tenant
model-provider credentials; credential lifecycle APIs; per-provider source
selection at Invocation admission; encrypted caller-ephemeral credentials;
durable Invocation bindings; revocation, rotation, expiry, recovery, cleanup,
provenance, authorization, and platform-funding hooks; migration of existing
`installation_byok` behavior.

**Out:** a general secret or integration vault; OAuth consent and refresh-token
flows; direct end-user nvoken authentication; per-tool credentials; automatic
credential-source fallback; credential reattachment to an admitted Invocation;
multiple simultaneously active named credentials for the same scope and
provider; interactive provider validation; pricing, checkout, or credit-ledger
implementation; and physical erasure from retained database backups.

## Credential contract

The four API sources are `caller_ephemeral`, `account_byok`, `tenant_byok`, and
`platform`. Self-hosted `installation_byok` remains a fifth deployment source
for compatibility. The current execution spec names exactly one provider, so
each current Invocation has one binding. An Account may nevertheless hold a
different reusable credential for every supported provider and select the
appropriate source on each Invocation. The provider-scoped binding key allows
a future multi-provider spec without adding that behavior in this PRD.

Account BYOK belongs to nvoken's customer, the host application. Tenant BYOK is
a reusable model credential the host manages on behalf of one `tenant_key`; it
does not make the host's end-user an nvoken principal. Caller-ephemeral is
supplied for one Invocation. Platform and installation secrets remain outside
Postgres in deployment or Cloud control-plane secret storage. Self-hosted
deployments may enable caller, Account, and tenant BYOK when credential
encryption is configured, plus `installation_byok`; `platform` is Cloud-only,
and `installation_byok` is unavailable in Cloud.

## Requirements

- **R1 — One reusable resource with two scopes.** A model-provider credential
  must belong to one Account, one canonical provider, and either Account scope
  or one effective tenant partition. At most one version may be active for a
  scope/provider tuple outside an explicit rotation overlap. The resource must
  retain nonsecret ID, scope, provider, status, version lineage, expiry, creator,
  timestamps, and audit metadata while secret reads return no credential
  material. Account BYOK requires Operator authority. A host Runtime credential
  may manage tenant BYOK only when its effective tenant and allowed operations
  match that exact partition. Provider identifiers must resolve through the
  closed registry of installed execution adapters, with aliases normalized to
  one canonical value. The initial registry remains `anthropic` and `openai`;
  adding Gemini or another provider is a separate adapter capability that uses
  this credential model without a schema change.

- **R2 — Application-layer encryption.** Reusable and caller-ephemeral secret
  payloads must be authenticated-encrypted before Postgres persistence under a
  versioned key-encryption mechanism whose usable key material is outside the
  database. Plaintext may exist only while lifecycle create/rotate or
  Invocation admission validates and encrypts it, or while the fenced executor
  constructs the selected provider adapter. It must never enter the spec
  snapshot, fingerprint, transcript, lifecycle state,
  ToolCall data, usage receipt, logs, traces, metrics, errors, or generated test
  fixtures. Self-hosted and paved Cloud deployments must fail safe when the
  required encryption key is unavailable.

- **R3 — One durable binding for the selected provider.** Admission must create
  exactly one `InvocationProviderCredential` binding for the current spec's
  canonical model provider, unique by `(invocation_id, provider)`. A later spec
  that can reference more providers may add bindings under the same invariant;
  this PRD does not add that spec behavior. The binding records source and
  either caller-ephemeral ciphertext, an immutable Account
  or tenant credential version, or a nonsecret platform/installation selector.
  `POST /v1/invocations` must accept these selections outside the execution
  spec, including bounded provider-shaped secret input only for
  `caller_ephemeral`. Unknown providers, unused secret attachments, missing
  selections, duplicate
  aliases, cross-Account references, and tenant-scope mismatches must be
  rejected before work becomes claimable.

- **R4 — Admission remains atomic and idempotent.** Invocation, input, spec
  snapshot, provider bindings, and any dispatch intent must commit together or
  not become visible. Fingerprint v6 must encode the request's literal
  nonsecret source selection, including omission, in
  `docs/design/admission-fingerprint-v6.json`; raw credential bytes, a
  materialized default, and resolved reusable credential versions are not
  fingerprinted. Equal idempotent replay returns the original
  Invocation and bindings and never compares, replaces, or extends the original
  secret. A changed explicit source selection returns `idempotency_conflict`.
  Retained requests remain comparable by their recorded algorithm.

- **R5 — Explicit resolution without fallback.** At every model call, the
  fenced executor must load the binding for that canonical provider and use
  only its selected source. Missing, expired, revoked, unauthorized,
  undecryptable, or platform-funding-denied credentials must make no provider
  call and settle visibly as `credential_unavailable` with safe internal
  diagnostics. nvoken must never fall through to Account, tenant, platform, or
  installation credentials after an explicit source fails. An installation may
  define a default source for omitted legacy selections, but admission must
  materialize that choice only on the binding before acknowledgement. Because
  fingerprint v6 preserves literal omission, replay after a default change
  returns the original Invocation and binding rather than conflicting or
  admitting new work.

- **R6 — Durable recovery and live revocation.** Any replacement execution
  owner must resolve the same binding and credential version after process loss.
  Rotation may keep an old version usable only for its explicit overlap.
  Revocation or expiry must prevent the next provider call for every bound
  nonterminal Invocation; an already-started provider request may complete but
  gains no authority to bypass existing Invocation fences. Caller-ephemeral
  expiry must cover the Invocation deadline plus bounded cleanup grace; if it
  expires first, the Invocation fails visibly rather than requesting another
  source. A parked `waiting` Invocation retains encrypted ephemeral material
  until that same wall-clock deadline because waiting remains part of the
  Invocation lifetime; hosts that require shorter retention must choose a
  shorter deadline.

- **R7 — Terminal cleanup and honest retention.** Every terminal settlement,
  including completion, failure, cancellation, and deadline, must clear its
  caller-ephemeral ciphertext in the same transaction or make cleanup
  independently retryable from authoritative terminal state. An expiry reaper
  must clear abandoned ephemeral ciphertext even if settlement is delayed.
  Nonsecret binding metadata and model-call provenance may remain with the
  Invocation trace. Documentation must say that live ciphertext becomes
  unavailable after cleanup while retained backups may contain encrypted bytes
  until their normal expiration; no stronger physical-erasure claim is made.

- **R8 — Source-aware provenance and funding.** Every model usage receipt must
  record canonical provider and one of `caller_ephemeral`, `account_byok`,
  `tenant_byok`, `platform`, or `installation_byok`, plus only a nonsecret
  credential/version identifier when applicable. Platform calls must pass the
  Cloud control plane's authoritative allow/deny funding gate before credential
  resolution and remain distinguishable for metering. This PRD consumes that
  decision but does not implement pricing or a credit ledger. BYOK calls must
  never consume platform provider quota or be relabeled as platform-funded
  after settlement.

- **R9 — Narrow lifecycle API.** The generated provider-credential contract
  must support safe list, create, get, rotate, and revoke operations for Account
  and tenant BYOK. Create and rotate accept provider-shaped static credential
  input but return metadata only. Equal idempotent retries must converge without
  extra active versions. Revocation destroys live encrypted material while
  retaining safe lineage and audit evidence. Caller-ephemeral, platform, and
  installation bindings are not reusable credential resources and cannot be
  managed through these endpoints.

## Acceptance

- [x] **A1 (R1, R2, R9):** An authorized host creates OpenAI Account BYOK and
  Anthropic tenant BYOK records. Metadata is readable and filterable by provider
  and scope, but database/API/log searches expose no plaintext.
  Cross-Account, mismatched-tenant, duplicate-active, and unauthorized Runtime
  operations fail without disclosing whether another credential exists.

- [x] **A2 (R3–R5):** Separate Invocations bind OpenAI to Account BYOK,
  Anthropic to caller-ephemeral, OpenAI to platform, and Anthropic to tenant
  BYOK. Each deterministic adapter receives only its selected credential.
  Missing, corrupt, revoked, expired, or funding-denied sources produce
  `credential_unavailable` and zero calls to all alternate sources.

- [x] **A3 (R3, R4):** Fault injection at every admission write leaves either
  no claimable Invocation or the Invocation plus its complete provider-binding
  set and dispatch. Equal replay with the same or a different supplied secret
  returns the original bindings without replacement; changing a source with the
  same idempotency key conflicts. A literally omitted source replayed after the
  installation default changes returns its original materialized binding.

- [x] **A4 (R5, R6):** After a model checkpoint and executor loss, another
  process decrypts the same bound version and continues under a new fence.
  Rotation overlap preserves a bound old version only until its deadline;
  revocation blocks the next call, and neither case changes credential source.

- [x] **A5 (R6, R7):** Completion, provider failure, cancellation, deadline,
  and delayed-settlement expiry tests make caller-ephemeral ciphertext
  unavailable while retaining safe source metadata. Restoring a backup follows
  the documented encrypted-retention boundary rather than claiming immediate
  physical erasure.

- [x] **A6 (R1, R5, R8):** Across separate Invocations, two tenants using the
  same provider resolve different tenant BYOK versions; an Account BYOK binding
  is usable across those tenants;
  caller-ephemeral remains usable only by its Invocation. Usage receipts report
  the exact source, and only platform fixtures invoke the funding/metering seam.

- [x] **A7 (R2, R8, R9):** Provider, HTTP, Postgres, recovery, and rotation tests
  capture requests, persistence, structured logs, metrics, and errors. They find
  credential source and safe IDs but no raw secret, authorization header,
  decrypted payload, or persisted copy of the intentionally secret-bearing
  admission input.

- [x] **A8 (R1–R9):** `make check` passes with migration drift, OpenAPI lint,
  authorization matrix, idempotency, race, encryption-key failure, redaction,
  recovery, cleanup, and provenance coverage for every supported credential
  source. Fingerprint v6 compatibility tests consume
  `docs/design/admission-fingerprint-v6.json`, and the design fixture index
  names both v5 and v6.

## Risks and open decisions

- Go cannot guarantee immediate zeroization of every plaintext copy in process
  memory. The enforceable boundary is minimal lifetime, no persistence outside
  encrypted storage, no observability exposure, and process isolation for the
  executor role.
- Provider adapters may require different static credential shapes. The
  contract keeps those payloads provider-defined and bounded; interactive OAuth,
  refresh, and service-account delegation remain separate capabilities.
