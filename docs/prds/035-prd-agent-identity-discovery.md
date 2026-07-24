# Make Agent identity anchors readable

**Status:** Ready
**Sequence:** 035
**Depends on:** `033-prd-api-sdk-contract-stabilization.md`
**Source proposal:**
[`2026-07-24-api-sdk-excellence.md`](../proposals/2026-07-24-api-sdk-excellence.md)
(`EX-3.1`)
**Independent review:** Claude Fable 5 on 2026-07-24; its findings about
design-packet authority, constrained-credential visibility, normalized cursor
binding, pagination indexing, and complete CLI filter coverage are
incorporated.

## ELI5

Hosts already name an Agent on every turn, but nvoken only exposes that
identity as a side effect of doing work. This slice lets a host list and fetch
those small identity anchors and use its own `agent_key` on recovery lists. It
does not turn Agent into stored configuration or add Agent mutations.

## Why

An Agent is an Account-wide identity anchor with an nvoken ID, a host-owned
key, and a creation time. Instructions, models, tools, and credentials remain
per-Invocation. Today a host cannot inspect that distinction directly: there
is no Agent read endpoint, and Session and Invocation lists accept only the
nvoken-owned `agent_id`.

That forces callers to admit work merely to resolve an identity and makes
recovery depend on an ID they do not naturally own. A small read surface makes
the existing resource model observable without adding a configuration
lifecycle.

## Outcome

- A host can list Agent identity anchors and fetch one by `agent_id`.
- Exact `agent_key` lookup resolves an anchor without admitting work.
- Session and Invocation recovery lists accept either Agent identifier with
  equivalent Account-scoped results.
- Generated clients and the CLI expose the complete addition.

## Scope

**In:** Account-scoped Agent list/get reads; exact `agent_key` list filtering;
opaque cursor pagination and its supporting index; `agent_key` filters on
Session and Invocation lists; constrained-credential visibility,
authorization, generated clients, CLI commands, design-packet and decision-log
updates, documentation, and cross-surface conformance.

**Out:** Agent create/update/delete endpoints; stored instructions, models,
tools, credentials, metadata, counts, tenant ownership, or a host-side Agent
definition registry; Session supersession and the other Phase 3 additions.

## Contract

| Method | Endpoint | Result |
| --- | --- | --- |
| `GET` | `/v1/agents` | `{items, has_more, next_cursor}` with optional exact `agent_key`, `cursor`, and `limit` filters |
| `GET` | `/v1/agents/{agent_id}` | One `{id, agent_key, created_at}` identity anchor |

`agent_id` and `agent_key` are alternate filters on Session and Invocation
lists. Supplying both is invalid even when they identify the same anchor.
Unknown exact keys produce an empty list, while unknown by-ID resource reads
retain the standard `not_found` behavior. Agent list cursors are bound to the
Account and exact key filter.

Agent visibility follows existing credential constraints without pretending
that an Agent belongs to a tenant. An unconstrained Runtime, Viewer, or
Operator credential may read Account anchors. A tenant-constrained credential
may read only anchors referenced by a Session in that effective tenant
partition. A Session-constrained credential may read only the anchor attached
to that Session. Undisclosable anchors behave as absent.

## Requirements

- **R1 — Identity-only reads.** The Runtime API must expose Account-scoped
  Agent list and get operations whose public representation contains only
  `id`, `agent_key`, and `created_at`. A read must not create an Agent, Session,
  Invocation, or execution record.

- **R2 — Stable list semantics.** Agent listing must use the standard bounded
  `{items, has_more, next_cursor}` envelope, newest-first stable ordering, and
  opaque cursors bound to the authenticated Account and filter set. An exact
  `agent_key` filter must return zero or one item. Ordering is
  `(created_at DESC, id DESC)`, backed by an Account-leading database index.

- **R3 — Equivalent recovery filters.** Session and Invocation list operations
  must accept `agent_key` as an alternative to `agent_id`. The service must
  resolve the key within the authenticated Account before querying recovery
  records; either identifier must produce the same ordered rows and tenant or
  Session constraints must remain unchanged. Supplying both identifiers must
  fail before lookup. Both spellings normalize to the resolved `agent_id`
  before cursor binding, so a cursor issued under one spelling is valid under
  the equivalent other spelling.

- **R4 — Authorization does not broaden.** Agent reads must require explicit
  Runtime read operations granted to Runtime, Viewer, and Operator profiles
  and must not disclose an anchor from another Account. Tenant-constrained
  credentials may see only anchors referenced by a Session in that partition;
  Session-constrained credentials may see only that Session's anchor. Existing
  constraints must not gain access to Session or Invocation rows through the
  new filter. Undisclosable by-ID reads use the existing `not_found` response.

- **R5 — One public contract everywhere.** OpenAPI must define both operations,
  the Agent identity schema, and the new filters. All four generated clients
  must compile with the operations, and the Go CLI must provide scriptable
  Agent list/get commands plus `--agent-key` on Invocation list, Session list,
  and Session resolve while retaining stable JSON output and standard error
  behavior.

- **R6 — The identity model is explicit and governed.**
  `docs/design/api.md` must govern the Agent operations and recovery filters
  and re-qualify the absence of Agent configuration as a readable identity
  anchor. `docs/design/decisions.md` must record the contract addition.
  `README.md`, `docs/guides/runtime-admission.md`, and
  `docs/guides/sdks-and-cli.md` must lead with the host-owned identity tuple
  and describe Agent as an identity anchor whose behavior still travels per
  Invocation.

## Acceptance

- [ ] **A1 (R1, R4):** Two Accounts contain different anchors. Each caller can
  list and fetch only its own `{id, agent_key, created_at}` values; cross-Account
  IDs return `not_found`, and storage evidence proves the reads admitted no
  work. Tenant- and Session-constrained callers see only anchors evidenced by
  Sessions within their existing constraint.

- [ ] **A2 (R2):** More than one Agent is listed across two pages with no
  duplicates or gaps. Reusing a cursor under another Account or `agent_key`
  filter is rejected, and an unknown exact key returns an empty page.

- [ ] **A3 (R3):** For the same authenticated scope, Session list results by
  `agent_key` exactly equal results by its `agent_id`, including order and
  pagination under default, explicit-tenant, and Session-constrained scopes.
  A next cursor can resume using the equivalent other spelling; an unknown key
  returns an empty page and both filters together return `invalid_request`.

- [ ] **A4 (R3):** The equivalent Invocation-list proof passes with default,
  explicit-tenant, and Session-constrained scopes without widening the
  pre-existing constraint behavior.

- [ ] **A5 (R5):** Shared API conformance exercises Agent list/get and both
  recovery filters; generated Go, TypeScript, Python, and Rust clients compile,
  and CLI JSON tests cover `agent list`, `agent get`, pagination, failures, and
  `--agent-key` on Invocation list, Session list, and Session resolve.

- [ ] **A6 (R6):** Root, Runtime API, and SDK documentation state the four
  host-owned keys, show Agent resolution without admission, and never imply
  that Agent stores instructions, models, tools, or credentials. The governing
  API packet and decision log record the same identity-read boundary.

- [ ] **A7 (R1-R6):** `make check`, `make test-postgres`, and `make sdk-check`
  pass with OpenAPI, generated-code, CLI-operation, and documentation drift
  checks clean.
