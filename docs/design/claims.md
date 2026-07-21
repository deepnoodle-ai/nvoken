# nvoken Claims

**Status:** Draft register — no claim is confirmed yet
**Date:** 2026-07-20

Two sets of claims:

- **External claims (`E-`)** — what nvoken advertises to customers. The
  website and primary documentation are driven from confirmed external
  claims. They bind marketing and engineering.
- **Internal claims (`I-`)** — commitments that determine how nvoken is
  architected and built but are not advertised: scope boundaries, contract
  semantics, mechanism guarantees, honesty caveats. They bind engineering.

Each claim is atomic, stated in present tense, and confirmable on its own.
Supplementary documents (`vision.md` narrative, `architecture.md`,
`api.md`) elaborate claims with mechanics, story, and process.

---

# External claims

## Thesis

- nvoken is an agent runtime as a service. It is a responses API that includes
  session persistence, cross-provider model routing, and execution observability.
- nvoken is the basis for an agentic harness within a host application. This
  allows the AI app developers to focus on their use case and not on building
  the robust agent harness.
- In terms of application layers, nvoken sits above the LLM generation API and
  below the host application.
- nvoken presents a unified API to the host application so that switching
  between model providers is trivial.
- Each agent turn may take seconds or up to tens of minutes,
  progressing through multiple rounds of tool calls.
- nvoken durably admits each turn so API disconnects, API process loss, engine
  loss, and deploys cannot erase accepted work. A replacement engine resumes
  from the last committed model or builtin checkpoint under a new fence.
- nvoken maintains the conversation state in a Session. A session primarily
  consists of a sequence of messages and the content blocks within those.
- nvoken is primarily used embedded within a host application.
- nvoken may be deployed in a self-hosted manner, or used within multi-tenant
  nvoken Cloud (planned).
- The ideal customers for nvoken are startups and engineering teams looking to
  add agentic capabilities to their application.

## Scope and ownership

- nvoken owns execution of the agent turn and provides streaming updates to the
  host application. The updates include the generated text responses and tool
  call invocations to the host.
- nvoken stores the current and historical execution history. Every execution is
  associated with a session. Sessions may be reused across multiple executions.
- nvoken is otherwise designed to be stateless and know little about the host
  application. Each invocation request provides the entire configuration for the
  agent, rather than the agent definition being stored within nvoken.
- In some cases, agent memory may be stored within nvoken. In others, agent
  memory may be handled by the application. Either mode is supported.
- In API requests to nvoken, the host app may pass tags or other metadata to
  help associate the agent or session with resources in the app. For example,
  the host could pass the account ID with each request. A limited number of
  metadata items are indexed to provide fast lookup later.
- We recommend that the host app relies on the nvoken API to retrieve past
  sessions and the messages within, rather than maintaining its own duplicate
  storage of this information. This is often relevant for showing dropdown
  selectors in the app UI, for example.
- As with LLM generation APIs, tool definitions are provided per invocation
  request. When the agent chooses to invoke a tool, there are three execution
  modes. Builtin tools are invoked directly by nvoken on the server side.
  Callback tools are defined by the host app, and when these are invoked
  nvoken makes a signed HTTP request to a remote endpoint to execute the
  call. Client tool calls are durably exposed to the client (the host app)
  through Invocation/Session reads and the Session stream so they can be
  executed there and their output returned idempotently to nvoken.

## API

- The primary operation is one call:
  `invoke(execution_spec, input, optional_session, optional_tenant_ref) -> durable invocation`
- No provisioning call precedes the first Invocation. The agent and session
  are looked up and automatically created if not found.
- The runtime OpenAPI has a deliberately small number of operations.
- nvoken may snapshot the exact execution spec with each invocation, calculate
  the hash/digest of this, and cache it. This supports configuration reuse via
  the cache and allows the client to avoid sending a large spec repeatedly over
  the wire.
- The API surface also includes session CRUD operations, optional CRUD for
  agent memory management, custom tool CRUD, invocation history, and traces.
- In addition to those "runtime" API endpoints, there are two additional
  endpoint categories: identity/admin, internal.
- The identity/admin API covers the current Account (a whoami read), API
  credential management, usage monitoring, and observability. Browser login
  endpoints are installation plumbing excluded from the generated contract.
- There is no portable membership CRUD at launch. Operator membership comes
  from installation configuration (an allowlist matched by issuer and email
  claim, with the subject bound at first login) or the nvoken Cloud control
  plane.

## Durability

- Accepted Invocations remain durable across API deploys, API process crashes,
  connection loss, and execution-owner loss; recovery remains visible through
  lifecycle revisions and authoritative reads.
- A client disconnect never erases authoritative state. Transports are never the
  source of truth.
- A disconnected host recovers transcript, invocation state, and pending tool
  calls from durable reads.
- Session messages are the sole durable transcript content record. Lifecycle
  revisions and streams may project or reference messages but do not persist a
  second copy of their content.
- Checkpoint-based crash continuation reclaims the same Invocation after an
  execution lease expires. Progress is reusable only after its transcript,
  receipt, and checkpoint transaction commits; uncommitted model work may run
  again. Future external effects must use stable ToolCall idempotency because a
  completed effect whose result was not persisted may also run again.

## Trust and security

- nvoken never executes host or end-user code; every tool with side effects executes on the host's side of the boundary.
- In a future version of nvoken, an Environment concept may be introduced, which is a secure, isolated sandbox for code execution. This is deferred for now.
- Tools execute in exactly three modes: builtin (server-side), signed callback, and client.
- The model receives tool capability, not ambient credentials.
- Embedded end-users never authenticate to nvoken directly. The host app makes requests to nvoken on behalf of the end-users behind the scenes.
- In a future version of nvoken, host app end-users may be able to make direct requests to nvoken using a new form of credentials. This is deferred for now.
- Host app developers log into nvoken to manage their API keys, view usage, and use the nvoken observability tools.
- In nvoken Cloud, Clerk may be used for authentication.
- In self-hosted nvoken, a trimmed down authentication setup may be used, and/or OIDC support may be able to leverage an existing auth provider that the host app developers have access to.
- nvoken may support using an external OIDC provider to authenticate app developers.

## Neutrality and cost

- No Runtime contract assumes a single LLM model vendor
- Self-hosted nvoken uses a bring-your-own-key (BYOK) approach.
- nvoken Cloud offers BYOK and platform credits to pay for usage. Platform credits carry a small markup on tokens.
- Our library deepnoodle-ai/dive is used for multi-provider support in nvoken.
- Execution specs carry token, cost, iteration, and wall-clock ceilings, and budget consumption is visible while the turn runs.

## Deployment and observability

- The open-source product is the actual runtime — durable turn execution, responses API, session storage.
- Postgres is used for the primary database. In self-hosted nvoken, the developer must provide the database URL for nvoken to initialize.
- Self-hosting has no Clerk, Stripe, plan, credit, or billing checkout dependency at runtime. It could be the case that support is still compiled into the binary if that is easiest from an internal development point of view.
- One binary plus Postgres (and optionally Redis) is a complete installation.
- The session viewer and the invocation trace are primary product surfaces. An operator answers "what did this agent do and why" from these pages.
- nvoken ships recipes and examples for host-owned patterns without adding runtime surfaces for these, at this time.

---

# Internal claims

## Scope boundaries

- Loops, schedules, triggers, and sandboxes are NOT runtime features. This contrasts against the predecessor runtime, where these were first class product features.
- The nvoken web console is an operator observability, health, and management surface. It is NOT an agent builder, end-user product, marketplace, or workflow editor.

## Contract semantics

- Agent identity anchors are Account-wide, unique by caller-controlled
  `agent_ref`, and store no mutable execution configuration or tenant data.
- Invocation creates or resolves Sessions; there is no separate Session
  provisioning. A host session key resolves uniquely within `(account_id,
  tenant_partition, agent_id, session_key)`. The Session's Agent and tenant
  partition are immutable.
- Agent, Session, and Invocation primary keys are UUIDv7 values with `agnt_`,
  `sesn_`, and `invk_` prefixes. Internal runtime records use the same
  prefixed-UUIDv7 convention and explicit sequence or revision fields for
  ordering.
- At most one queued, running, or waiting Invocation exists for a Session at a
  time.
- Invocation states are exactly `queued`, `running`, `waiting`, `completed`,
  `failed`, and `cancelled`; the last three are terminal, the first terminal
  settlement wins, and deadline or budget exhaustion is a typed failure.
- Terminal Invocations stay terminal; there is no public retry or resume, and a new turn is a new Invocation.
- Invocation admission is one Postgres transaction covering Agent and Session
  resolution or creation, the immutable inline spec snapshot, one caller-input
  message, and one queued Invocation. No execution owner can claim work before
  all related records are visible.
- Admission idempotency is scoped to Account, effective tenant partition,
  Agent reference, and caller key. An equal replay returns the original work
  before the Session concurrency check; a materially changed replay conflicts.
- Invocation supports structured output, in which a tool call internally is made against the provided output schema and this JSON is returned to the client.
- A well-defined set of streaming events communicates invocation updates to the client.
- nvoken cannot reverse an external effect that completed before a network failure; hosts make business effects idempotent by ToolCall ID.

## Durability mechanics

- A stale engine instance cannot commit after losing its lease.
- A turn segment executes entirely on one harness version. Request-bound split
  executors drain on deploy; if either execution topology loses an owner, the
  lease reaper makes the same Invocation queued and a replacement continues
  from its last validated checkpoint. Retained `execution_lost` failures are
  historical records, not the current recoverable-lease policy.
- nvoken supports the same public admission and read semantics in two execution
  topologies: a self-contained engine in `nvokend`, or a separate Cloud Tasks to
  Cloud Run executor. Delivery is only a wake-up mechanism; Postgres claims,
  leases, and fences are the execution authority.

## Security and identity

- Tool callback requests are runtime-signed and verified through either JWKS or a signing secret shared between nvoken and the host app.
- The Account is the hard security boundary; `tenant_ref` narrows authorization only when a credential is constrained to it.
- A Session's `tenant_ref` is immutable after creation.
- Human operator roles are fixed (Owner, Operator, Viewer); Owner is human-only and never assignable to an API credential.
- API credentials are one resource with two kinds: machine credentials carry one fixed profile (Operator, Viewer, or Runtime) with constraints that only narrow; user credentials are issued through device authorization and resolve their effective role at authentication time from the owner's current membership role intersected with an optional cap.
