# Issue machine credentials and authenticate the CLI

**Status:** Draft
**Sequence:** 027
**Depends on:** `002-prd-postgres-runtime-spine.md`,
`003-prd-durable-invocation-admission.md`,
`019-prd-compatible-upgrades-and-rollback.md`, and
`026-prd-multi-language-sdks-and-go-cli.md`

## ELI5

Host applications and CI need revocable API keys, while a person using the
`nvoken` CLI should sign in through a browser instead of copying one. This PRD
replaces the single configured bearer with durable machine and user
credentials and gives the CLI one consistent way to use either. It does not
add end-user authentication, member administration, or a full operator
console.

## Why

The embedded golden path currently has one installation Account and one
configured `RUNTIME_API_KEY`, optionally constrained by `RUNTIME_TENANT_REF`.
That is enough for one host backend but cannot safely support separate host and
CI credentials, expiry, rotation, revocation, attribution, or a human-operated
CLI.

The current Go CLI in `../mobius` demonstrates a useful pattern: RFC 8628
device authorization feeds named local profiles; flags and environment
variables feed the same resolver; endpoint and credential precedence are
independent; and logout is distinct from server-side revocation. nvoken adopts
that interaction model but uses the packet's unified credential resource
instead of separate API-key and CLI-credential types.

## Outcome

- Host backends and CI can use independently managed Runtime-profile machine
  credentials.
- A human can run `nvoken auth login`, approve the device in a browser, and use
  the resulting user credential from a named local profile.
- Every request resolves to one durable credential record whose current
  profile or role, constraints, expiry, revocation, and actor are enforced at
  authentication time.
- The existing configured Runtime key has a fail-safe migration path rather
  than becoming an unrevokable compatibility bypass.

## Scope

**In:** a separate `openapi/identity.yaml` for `GET /v1/account`, the five
`/v1/account/credentials` operations, and the three RFC 8628 device operations;
durable machine and user credentials; a minimal durable operator-subject and
membership substrate; profile and narrowing constraints; expiry, rotation,
revocation, last-use and audit metadata; a bootstrap-Owner browser approval
path; replacement of static Runtime authentication; generated Go
identity/admin transport; and `nvoken` credential and auth commands with
Mobius-style local profiles.

**Out:** Account creation; portable members CRUD, invitations, or directories;
external OIDC and its operator allowlist; direct host end-user credentials;
MFA and recovery; a general operator console; usage-event export; multi-language
identity/admin SDKs; OS-keychain storage; provider, integration, callback, or
business secrets; and automatic secret-manager publication.

## Requirements

- **R1 — One durable credential model.** API credentials must be Account-owned
  records of kind `machine` or `user`. Records retain a nonsecret prefix,
  status, creation and expiry times, creator and owner, last-use metadata,
  rotation lineage, profile or role cap, and narrowing constraints. Raw opaque
  secrets are returned only through bounded issuance delivery and are never
  exposed by later reads, logs, or audit records. Authentication uses a
  nonreversible verifier appropriate for high-entropy secrets and constant-time
  comparison.

- **R2 — Explicit authorization profiles.** A machine credential has exactly
  one fixed `Runtime`, `Viewer`, or `Operator` profile; `Owner` is never an API
  credential profile. Constraints may only narrow that profile by
  `tenant_ref`, Session, operation subset, and expiry. A user credential belongs
  to the approving operator and resolves its effective role on every request
  from the owner's current membership role, clamped to at most `Operator`, and
  intersected with its `Operator` or `Viewer` cap; the cap defaults to
  `Operator`. An installation-owned, nonportable provisioning seam can create,
  change, or remove the durable membership; bootstrap uses it for the local
  Owner and later OIDC or Cloud adapters may use it without changing bearer
  semantics. The portable contract documents and tests the exact operation
  matrix.

- **R3 — Machine credential lifecycle.** Authorized operators can list,
  create, read, rotate, and revoke machine credentials through
  `/v1/account/credentials`. Create and rotate return the new secret once;
  rotation links predecessor and replacement and supports a bounded explicit
  overlap. Revocation and expiry prevent the next request while preserving the
  record and actor history. A caller may always inspect and revoke its own
  user credential, subject to Account isolation.

- **R4 — Reliable secret issuance.** Credential creation, rotation, and the
  approved device exchange must converge after a lost success response: an
  idempotent replay during a bounded delivery window returns the same issuance
  result and never creates a second credential. Any temporarily recoverable
  delivery secret is protected at rest and destroyed when the delivery window
  expires; the long-lived credential store retains only verification material.

- **R5 — Current Account explains authentication.** `GET /v1/account` returns
  Account identity plus the caller's credential ID and kind, resolved subject,
  effective profile or role, constraints, authentication method, and assurance.
  The same resolved context authorizes Runtime and identity/admin operations,
  so status reporting cannot disagree with request enforcement. Requests
  outside an Account or tenant constraint retain the Runtime contract's
  nondisclosure behavior.

- **R6 — Complete device authorization.** `POST /v1/auth/device/code`,
  `/device/token`, and browser-authenticated `/device/confirm` implement RFC
  8628 success and error semantics, including `authorization_pending`,
  `slow_down`, `access_denied`, `expired_token`, and polling cadence. Challenges
  are durable, short-lived, single-approval grants with high-entropy device
  codes, human-readable user codes, bounded confirmation attempts, and cleanup.
  Approval displays and confirms the Account, approving human, cap,
  constraints, and device label before issuing one user credential. Repeated
  token polls during the delivery window return the same success; after that
  window an exchanged grant returns `expired_token`.

- **R7 — Bootstrap approval without an identity platform.** A fresh
  self-hosted installation has an installation-owned bootstrap Owner secret
  that can establish a short-lived secure browser session solely for operator
  and device approval work. Bootstrap creates one durable local Owner
  membership under an installation issuer and stable bootstrap subject; user
  credentials approved this way belong to that installation principal, not a
  separately identified person. The secret itself is not a Runtime bearer, is
  never persisted in Postgres or exposed to the CLI device, and is protected
  by rate limiting, CSRF defenses, secure cookie policy, and safe logging. The
  approval seam remains replaceable by later external OIDC and allowlisted
  per-human memberships without changing the device or credential APIs.

- **R8 — One CLI authentication resolver.** The Go CLI resolves the effective
  bearer once per invocation. `--api-key` or `NVOKEN_API_KEY` overrides a saved
  profile; `--base-url` or `NVOKEN_BASE_URL` independently overrides the
  profile endpoint; `--profile` or `NVOKEN_PROFILE` selects a named profile.
  This lets the same Runtime and identity/admin commands use a human device
  credential, a host/CI machine credential, or a self-hosted endpoint without
  duplicating HTTP contracts. Environment and flag credentials are never
  written locally unless the user explicitly requests it. Identity/admin
  commands expose machine credential list, create, get, rotate, and revoke with
  safe text and stable JSON handling for one-time secrets.

- **R9 — Safe, usable local profiles.** `nvoken auth login` prints the user
  code and verification URL, attempts to open the browser, honors
  `--no-browser`, polls safely, and saves the user credential as a named
  profile. `auth status`, `list`, `use`, `logout`, and `revoke` distinguish
  local removal from remote revocation; status verifies the active credential
  through `GET /v1/account`. The first profile becomes the sole default;
  `auth use` changes it; an explicit profile works without a default; and zero
  or multiple defaults otherwise fail with repair guidance. Profiles live in
  the single file `~/.nvoken/credentials`, with explicit path override, and
  store endpoint, token, credential ID, Account and subject metadata, creation
  time, and last use. Its parent directory is `0700`; file writes are atomic
  `0600`; unsafe existing permissions produce a warning. Plaintext file storage
  protected by filesystem permissions is the deliberate portable baseline;
  OS-keychain support is deferred.

- **R10 — Safe static-key transition.** On an existing single-Account
  installation, the configured `RUNTIME_API_KEY` and optional tenant constraint
  are imported exactly once as a durable Runtime machine credential. After
  import, the new release authenticates it only through database state; changing
  configuration does not create a second credential. During the explicit
  pre-027 rollback window, the configured key remains present and unrevoked so
  the previous binary can still start. Revoking or rotating away from that key
  is the documented cutover that ends rollback support to the configuration-only
  authenticator; a revoked key is never silently recreated by a 027-or-later
  start. Fresh provisioning documents how the bootstrap Owner establishes a
  human profile and issues separate host and CI credentials.

## Acceptance

- [ ] **A1 (R1–R3, R5):** An Operator creates independent Account-wide and
  tenant-constrained Runtime machine credentials. Each secret is shown only on
  issuance; later list/get responses contain metadata only. Valid requests
  resolve the documented actor, profile, and constraints, while cross-Account,
  constraint, and disallowed-operation probes fail without disclosure.

- [ ] **A2 (R3, R4):** Fault injection loses the response after credential
  creation and after rotation commits. Replaying the same idempotent operation
  returns the same issuance result within the delivery window, creates no
  duplicate, and leaves exactly the documented predecessor/replacement overlap;
  after the window, no raw secret is recoverable.

- [ ] **A3 (R3, R5):** Expiring or revoking a credential causes its next
  Runtime and identity/admin requests to fail. Rotation overlap ends at the
  promised time. Reads retain creator, owner, lineage, prefix, status, and safe
  last-use/audit evidence without any raw bearer value.

- [ ] **A4 (R6, R7):** From a clean self-hosted installation, a human starts
  `nvoken auth login`, authenticates the browser with the bootstrap Owner,
  verifies the displayed Account/device/cap, approves, and receives exactly one
  user credential. Pending, too-fast, denied, expired, cancelled, duplicate
  confirmation, brute-force, process-restart, and lost-token-response cases all
  produce the specified durable result and RFC error.

- [ ] **A5 (R2, R5, R6):** A user credential's effective permissions change on
  the next request when its durable membership is changed through the
  installation-owned membership seam, and never exceed `Operator` or its lower
  cap. The bootstrap flow records the stable installation Owner as its owner;
  Owner authority is not present in the bearer credential. The owner can still
  inspect and revoke that credential wherever the governing policy permits.

- [ ] **A6 (R8, R9):** CLI contract tests prove profile login and selection,
  independent endpoint override, `NVOKEN_API_KEY` and `--api-key` precedence,
  active verification, multiple-default rejection, local logout without remote
  revocation, remote revocation cleanup, permission warnings, and stable JSON
  output. CI runs Runtime commands with only `NVOKEN_BASE_URL` and
  `NVOKEN_API_KEY` and creates no credentials file.

- [ ] **A7 (R8, R9):** The CLI's Runtime commands still consume the Go Runtime
  SDK and identity/admin commands consume the generated transport from
  `openapi/identity.yaml`. A source check finds no independently maintained
  route or payload definitions; only browser launch, polling orchestration, and
  profile storage are handwritten.

- [ ] **A8 (R10):** Upgrading a fixture containing the current configured key
  preserves its Account, tenant constraint, and Runtime access as one imported
  credential. Repeated starts and configuration changes do not duplicate it.
  Before cutover, the previous release can start with the unchanged key; after
  explicit cutover, revocation survives every 027-or-later restart and the
  documented procedure rejects rollback to the configuration-only release.

- [ ] **A9 (R1–R10):** `make check` passes with migration, authentication,
  authorization-matrix, device-flow, HTTP contract, CLI profile, race, and
  secret-redaction tests. Logs and generated fixtures contain no raw credential,
  bootstrap, device, or browser-session secrets.

## Risks and open decisions

- The bootstrap Owner is intentionally a recovery-grade installation secret.
  Deployment guidance must keep it separate from Runtime credentials and make
  external OIDC the natural follow-up for shared installations with multiple
  operators. Until then, bootstrap-approved user credentials are attributable
  to the installation Owner, not to distinct people who know that secret.
- Replayable one-time delivery closes the Mobius lost-success window but needs
  a narrowly protected transient-secret design in the technical specification.
- Writing every CLI `last_used_at` locally is useful but must remain best-effort
  and concurrency-safe; authoritative use metadata belongs to the server.
