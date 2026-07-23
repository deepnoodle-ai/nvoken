# nvoken Vision — Agent Runtime as a Service

**Date:** 2026-07-20
**Horizon:** Next development cycle; sequencing is by dependency, not calendar
**Status:** Draft for review
**Companions:** `claims.md` (the core-claims register this narrative
introduces), `decisions.md`, `architecture.md`, `api.md`

---

## 1. Thesis

nvoken is an agent runtime as a service.

> nvoken is a durable Responses API with session persistence, tool
> execution, cross-provider model routing, and execution observability.

Model providers ship one stateless generation call. nvoken is the layer
above the LLM generation API and below the host application: the turn that
runs the model against tools until the work is done, the Session that gives
the agent memory and identity, and the durability, routing, and
observability a production agent needs. These are the harness features
every agentic product otherwise builds itself; nvoken supplies them so AI
app developers focus on their use case, not on building a robust harness.
One unified API covers every provider, so switching between model providers
is trivial.

An agent turn may take seconds or tens of minutes, progressing through
multiple rounds of tool calls. nvoken durably admits that work so an API
disconnect or process restart cannot erase it. If an execution owner is lost,
another owner resumes the same Invocation from its last committed model or
builtin checkpoint. Work completed outside Postgres but not checkpointed may
run again. Conversation state lives in the Session: a
sequence of messages and the content blocks within them. nvoken runs agent
turns, manages their execution, and maintains sessions; application state
remains entirely the host's responsibility.

The ideal customers are startups and engineering teams looking to add
agentic capabilities to their application.

Success is a team saying:

> “We kept our users, data, definitions, tools, and deployment workflow. nvoken
> replaced the agent harness and reliability infrastructure we were about to
> build.”

## 2. Product law

> nvoken stores execution state, not product configuration.

nvoken stores what execution, recovery, inspection, and accounting require:
Sessions and transcripts; Invocations, lifecycle revisions, and output;
ToolCalls and results; checkpoints, leases, and fences; execution-spec
snapshots and digests; normalized usage and provenance. Four opt-in conveniences extend
this: agent memory (a host may instead keep memory entirely on its side —
either mode is supported), named custom tool definitions, reusable
model-provider credentials at Account or tenant scope, and a limited number of
indexed metadata items per request that link Sessions and Invocations to host
resources for fast lookup.

nvoken is never the source of truth for agent definitions or releases; host
tenants, users, or permissions; integrations, OAuth connections, or non-model
business credentials; workflows, schedules, or triggers; tables, files, or
application records; rollout selection or product entitlements.

## 3. The product at 100% embedded

The primary API operation:

```text
invoke(execution_spec, input, optional_session, optional_tenant_key)
  -> durable invocation
```

No Project, Agent, Release, Integration, or secret resource is provisioned
first. The host can:

- send the specification inline, or reference immutable bytes by URL + digest;
- create or continue a Session by host session key;
- receive ordinary output, generation streaming, or structured output
  against a host-provided schema;
- receive host ToolCalls over a later stream and submit results through a
  narrow durable command;
- expose server-to-server capabilities as signed callback tools;
- tag requests with indexed metadata that links Sessions and Invocations to
  host resources;
- recover accepted work after any disconnect;
- read normalized usage for attribution or rebilling.

We recommend the host rely on the nvoken API to retrieve past Sessions and
their messages — populating a session dropdown in the app UI, for example —
rather than maintaining duplicate storage of that history.

The console is an operator observability, health, and management surface —
not an agent builder, end-user product, marketplace, or workflow editor.

## 4. Always embedded, two deployments

nvoken is always embedded: a host application calls the Runtime API. It
deploys two ways:

1. **Self-hosted** — the runtime in the customer's infrastructure, BYOK,
   open commercial entitlements.
2. **nvoken Cloud** — managed operation of the same runtime (planned).

One execution product, not a platform of adjacent agent features. Loops,
schedules, triggers, and sandboxes are not runtime features — a deliberate
contrast with the predecessor runtime, where they were first-class product
surfaces. Hosts bring their own scheduler and call `invoke()`, and attach
their own sandbox provider, keyed by Session ID, exposed through
host-executed tools. nvoken ships recipes and examples for these host-owned
patterns without adding runtime surfaces for them at this time. A future
version of nvoken may introduce an Environment concept — a secure, isolated
sandbox for code execution — but it is deferred for now.

## 5. The public nouns

- **Agent** — an Account-wide lightweight identity anchor, auto-created on
  first Invocation from the caller-controlled `agent_key`. It groups Sessions
  and Invocations across tenant partitions for lookup and observability and
  carries no configuration. Its ID is UUIDv7 with an `agnt_` prefix; the
  execution specification still arrives with every Invocation.
- **Session** — durable conversation and execution context: a sequence of
  messages and the content blocks within them. Owns transcript, Invocation
  history, and retention. Its primary key is a UUIDv7 with a `sesn_`
  prefix; a host-built session key, unique per Account, effective tenant
  partition, Agent, and key, resolves it. The Session's tenant partition and
  Agent never change.
- **Invocation** — one durable agent turn, identified by UUIDv7 with an `invk_`
  prefix. It progresses through `queued`,
  `running`, or the tool-reserved `waiting` state and settles exactly once as
  `completed`, `failed`, or `cancelled`. It records input,
  resolved spec and digest, model/tool activity, output (including
  structured output), error, usage, and recovery state.
- **ToolCall** — the durable governance boundary between what the model
  requested and what actually happened. Modes: builtin, callback, host.

Optional adjacent resources: agent memory records (when the host opts in)
and named custom tool definitions. Everything else is caller input,
internal implementation, deployment operation, or nvoken Cloud account
state.

## 6. Caller-owned agent specifications

An agent is a specification the host supplies per Invocation: instructions,
model preference, tool schemas and modes, output constraints, limits. The
host's Git is source of truth; its CI decides which version each tenant
receives. nvoken may snapshot the exact resolved bytes and digest for
reproducibility and cache by digest — the client avoids resending a large
spec on every request — but exposes no registry, publish, adopt, pin, or
deployment-track resources. Dev, staging, production, canary, and rollback
are all "send a different digest."

## 7. Tenancy without provisioning

Embedded end-users never authenticate to nvoken directly; the host
application makes requests to nvoken on their behalf. A future version of
nvoken may let end-users make direct requests with a new form of credentials —
deferred for now. The host may supply a stable `tenant_key` per Invocation,
used for Session partitioning, credential scoping, usage attribution, operator
filtering, callback context, and audit. A tenant-constrained credential fixes
that partition; otherwise an explicit reference or the Account default applies
when a Session is created or resolved by key. There is no public Project
lifecycle; internal partitions are materialized automatically.

## 8. The harness is the product

The differentiator is execution quality, not configuration CRUD.

**Durability.** Accepted work survives API processes and deploys; engines claim
work with leases and fencing tokens; stale engines cannot commit; a client
disconnect never erases authoritative state. Engine loss requeues the same
Invocation, and a replacement validates and continues from the last committed
model or builtin checkpoint. If the system fails between a tool call completing
and its result persisting, the call may run again on resumption — hosts make
business effects idempotent by ToolCall ID.

**Tool governance.** Three modes: `builtin` (small trusted runtime
capabilities), `callback` (signed durable delivery to the host), `host`
(generation-style ToolCall; the parked turn resumes on the result). The
model receives tool capability, not ambient credentials. Every tool with side
effects executes on the host's side of the boundary: nvoken runs the turn
but never executes host or end-user code.

**Recovery.** Session messages are the sole durable transcript. Incremental
views compose those messages with append-only Invocation state revisions;
neither a change feed nor a live stream stores a second copy of content.
Invocations expose authoritative state, output, error, usage, and provenance;
pending host ToolCalls are queryable after reconnect. Transports are never
the source of truth.

**Provider neutrality.** The spec selects models per Invocation and may route
steps of one turn across providers. Multi-provider support is built on our
deepnoodle-ai/dive library. No Runtime contract assumes a single vendor.

**Cost alignment.** Self-hosted nvoken is bring-your-own-key. For each model
provider, nvoken Cloud accepts an Invocation-supplied ephemeral credential,
reusable Account BYOK, reusable tenant BYOK, or a platform-funded credential.
Platform credentials never silently replace an explicitly selected BYOK
source and carry a small markup on tokens. The spec carries token, cost,
iteration, total-time, active-time, and waiting-time limits; consumption is
visible in usage events while the turn runs.

**Observability.** The session viewer and the invocation trace are primary
product surfaces: transcript, ToolCall attempts and results, spec digest,
model provenance, normalized usage. An operator answers "what did this
agent do and why" from these pages.
