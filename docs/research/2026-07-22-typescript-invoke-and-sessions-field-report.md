# TypeScript invoke and Sessions field report

- Date: 2026-07-22
- Repository revision: `7d7ea81d0f616749ec0e50d6f58c5bde7561ebf4` (`main`)
- Source SDK: `@deepnoodle/nvoken` `0.1.1`
- Live provider/model: OpenAI / `gpt-5.4-mini`
- Evidence run: `typescript-showcase-c48c69e0-af23-41ac-9375-720bf181ff52`
- Improvement verification run:
  `typescript-showcase-9e15c1d6-7ca1-4ba3-8757-fb8bb97a3751`
- Earlier onboarding work:
  [first pass](2026-07-21-local-typescript-onboarding.md) and
  [second pass](2026-07-22-local-typescript-onboarding-second-pass.md)

## Bottom line

The implemented Runtime and source TypeScript SDK worked across every scenario
in this pass. I started the source daemon through the documented quickstart,
ran the standard two-turn SDK proof, then built a separate TypeScript app that
exercised ordinary chat, exact idempotent replay, client tools, structured
output, tenant and Agent references, authoritative Session reads, pagination,
incremental transcript cursors, and SSE. I found no Runtime correctness failure
in those paths.

At the tested revision, the remaining developer-experience gap was depth, not
first-run viability. The TypeScript README made basic invoke/wait/text
pleasant, but gave no TypeScript path for client tools, structured output,
tenant partitioning, or Session recovery APIs. The HTTP Runtime guide contained
the semantics, and the generated client contained most operations, but a
TypeScript user had to combine both and inspect SDK types or source.

The added
[TypeScript invoke showcase](../../examples/typescript-invoke-showcase/README.md)
is a repeatable live exercise and an executable record of the expected
behavior.

## Improvements made from this exercise

Every concrete SDK gap below was addressed in the same change as this report:

| Finding | Improvement |
| --- | --- |
| Terminal-only wait hid client-tool work | `handle.wait({ until: "actionable" })` now returns on `waiting` or a terminal status; explicit status sets are also supported. |
| Advanced TypeScript flows were undocumented | The SDK guide now covers typed client tools, structured output, identity scopes, Session ownership, pagination, transcript recovery, and SSE; the live showcase uses only the public facade. |
| `sessionKey` was absent from the facade | `listSessions({ sessionKey })` is supported, with exact `getSessionByKey(sessionKey, scope)` recovery. |
| Collection and transcript traversal were asymmetric | `sessionPages`, `messagePages`, `getTranscriptPage`, and fixed-cut `drainTranscript` were added alongside `invocationPages`. |
| Admission metadata was discarded | New Handles retain `agentId` and `deduplicated`; resumed Handles expose the recovered Agent ID and leave acknowledgement-only deduplication undefined. |
| Schema-bearing values degraded to `any` | `defineClientTool`, `toolInput`, and `defineJsonSchema<T>` carry application types through tool dispatch and structured Invocation results. OpenAPI singleton constants now generate as literal enums in all SDKs. |

The normal SDK gate compiles the showcase so future facade or documentation
drift fails before merge. The live provider exercise remains opt-in because it
makes billable calls. The improvement verification run passed the complete
showcase against the local source Runtime using only the supported facade.

## Method

I started from the root README and the linked source-development and TypeScript
SDK guides. I did not begin from backend implementation code. After completing
the documented source quickstart, I used the Runtime admission guide, the
TypeScript declarations, and finally the facade implementation when the guide
did not show how to express the requested advanced workflows.

The host was macOS on arm64 with:

- Go 1.26.2;
- Node.js 22.17.1;
- npm 10.9.2;
- Docker 29.6.1; and
- disposable PostgreSQL 17 created by `nvokend quickstart`.

The source daemon was launched with:

```bash
go run ./cmd/nvokend quickstart \
  --provider openai \
  --model gpt-5.4-mini
```

It generated the ignored `.env`, applied migrations, and served on
`http://localhost:8080`. The documented SDK source build and standard two-turn
quickstart then succeeded and recalled `cedar`.

The showcase is a separate strict TypeScript package with
`@deepnoodle/nvoken: file:../../sdk/typescript`. Its final run made seven small
provider requests. It prints IDs and assertions but no credentials.

## What worked

| Scenario | Result | Evidence |
| --- | --- | --- |
| Pricing preflight | Passed | The exact OpenAI/model pair was `priced` under registry `v1.16.0`. |
| Basic text result | Passed | `invoke()`, `wait()`, and `text()` returned the completed assistant response. |
| Multi-turn chat | Passed | Turn two recalled `ORCHID-724` from turn one when invoked with the returned `sessionId`. |
| Session resume without repeated tenant | Passed | An Account-wide Runtime credential could omit `tenantRef` when continuing by `sessionId`; the Session retained its original tenant. |
| Exact idempotent replay | Passed | Replaying the exact first request returned the same Invocation and Session IDs without another transcript append. |
| Changed idempotent replay | Passed | Changing only the input under the same scoped key returned `idempotency_conflict`. |
| Client tool | Passed | `lookup_order` parked the Invocation in `waiting` with one stable pending ToolCall and no active owner. |
| Waiting Session visibility | Passed | Both Invocation and Session reads exposed the same ToolCall ID, input, and waiting state. |
| Busy Session rule | Passed | A second turn on the waiting Session returned `session_invocation_active` and appended no input. |
| Tool result acceptance | Passed | The first result was accepted, an equal retry was deduplicated, and a changed retry returned `tool_result_conflict`. |
| Tool continuation | Passed | The same Invocation resumed, completed, and produced user/assistant/tool/assistant canonical messages. |
| Later tool-aware turn | Passed | A later Invocation in the Session recalled the tool result and answered `tomorrow`. |
| Structured output | Passed | The terminal object was `{"category":"billing","needs_human":true,"priority":"high"}` with ToolCall ID and schema-digest provenance. |
| Composed result | Passed | Structured output and ordinary assistant text were both available on the completed Invocation/result. |
| Agent reference identity | Passed | One `agentRef` resolved to the same `agentId` in two tenants. A different `agentRef` produced a different Agent. |
| Tenant partitioning | Passed | The same Agent reference, Session key, and idempotency key produced independent Sessions and Invocations in different tenants. |
| Agent partitioning | Passed | The same tenant, Session key, and idempotency key produced independent work under a different Agent reference. |
| Scope mismatch | Passed | Using the wrong Agent or explicit tenant with an existing Session ID returned nondisclosing `not_found`. |
| Session reads and filters | Passed | Get, tenant/Agent list filtering, default-tenant filtering, and exact raw lookup agreed with invoke results. |
| Message pagination | Passed | One-message pages produced the canonical sequence `1,2,3,4` and roles `user,assistant,user,assistant`. |
| Invocation pagination | Passed | The facade async iterator returned both turns with a page size of one. |
| Incremental transcript | Passed | Resuming from the cursor after turn one returned only turn-two messages and lifecycle changes. |
| Session SSE | Passed | The facade reduced durable snapshot frames and ended only after authoritative terminal reconciliation. |

## Agent and tenant reference behavior

The behavior is coherent once the identity tuple is understood:

```text
Agent identity:    Account + agent_ref
Session identity:  Account + tenant partition + Agent + session_key
Idempotency scope: Account + tenant partition + agent_ref + idempotency_key
```

Consequences confirmed by the run:

- `agent_ref` is not tenant-local. The same reference maps to one Account-wide
  Agent anchor across tenant partitions.
- `tenant_ref` partitions Sessions and idempotency. Reusing a Session key and
  idempotency key in another tenant does not collide.
- changing `agent_ref` also isolates Session and idempotency scope, even inside
  one tenant.
- a host with Account-wide authority can continue a known Session ID without
  resending its tenant reference.
- an explicit incompatible tenant or Agent reference does not disclose that
  the Session exists.

This is a good multi-tenant contract. It needs one TypeScript-oriented
explanation because the current root request example comments on the fields but
does not show the tuple or its practical consequences.

## Session API experience

The wire APIs behaved as expected and agreed with Invocation results. In
particular:

- terminal Sessions cleared `activeInvocationId` and
  `activeInvocationStatus`;
- waiting Sessions exposed the same pending calls as the Invocation;
- message pagination was stable and ascending;
- Invocation collection pagination was newest-first but complete;
- a transcript resume cursor produced only state committed after the earlier
  cut; and
- SSE reduced to the same durable messages and lifecycle state before its
  terminal end.

At the tested revision, the TypeScript facade did not make all of those paths
equally accessible. `getSession`, `listSessions`, `listMessages`, and
`invocationPages` were first-class. Exact Session-key lookup and fixed-cut
transcript reads required `client.raw().sessions`. Message pagination required
a handwritten loop. The improvements above close those gaps.

## Findings from the tested revision

### P1 (addressed): document advanced TypeScript workflows

The TypeScript README has a strong minimal application and result-read section,
but contains no `Tool`, `outputSchema`, `submitToolResults`, `tenantRef`,
`listSessions`, transcript, or Session-pagination example. The Runtime guide
explains most of these through JSON and curl. A TypeScript user has to translate
snake_case wire fields to facade names and discover which operations are
wrapped versus raw.

The resulting task-oriented TypeScript guide covers:

1. invoke and inspect `waiting`;
2. dispatch pending client ToolCalls and replay their results;
3. request and read typed structured output;
4. resolve/list Sessions by tenant, Agent, and host key; and
5. recover by message pages, transcript cursor, and SSE.

The new showcase supplies tested code for that guide and is compiled by the SDK
gate.

### P1 (addressed): make `waiting` actionable in the facade

`Handle.wait()` waits only for `completed`, `failed`, or `cancelled`.
`waiting` is intentionally nonterminal, so calling `wait()` on a client-tool
Invocation blocks while the application action needed to make progress is
hidden behind `refresh()`. The current TypeScript README does not warn about
this.

The showcase needed a custom polling loop that stopped on either `waiting` or
a terminal status. This is the most likely client-tool onboarding trap.

The facade now provides:

```ts
const invocation = await handle.wait({ until: "actionable" });
```

The existing terminal-only behavior remains the default, and the guide states
the distinction explicitly. A future watermark/long-poll transport can back
the same API without changing host code.

### P1 (addressed): complete Session facade coverage

The generated `SessionsApi.listSessions` accepts `sessionKey`, but
`Client.listSessions` omits that option. Exact host-key resolution therefore
requires the raw client. This is a concrete facade parity gap.

The facade also has:

- `invocationPages`, but no `sessionPages` or `messagePages`;
- `listMessages`, but no async traversal helper;
- no fixed-cut transcript drain helper over `resumeCursor` and
  `nextPageToken`; and
- no read-only `getSessionByKey` convenience over the exact list filters.

The SDK README's opening claim of “async pagination” is therefore only partly
true in the high-level surface.

The missing filter, symmetric iterators, exact lookup, and transcript drain
helper were added to the facade.

### P2 (addressed): retain useful admission acknowledgement fields

The wire acknowledgement contains `agent_id` and `deduplicated`, but
`Client.invoke()` returns a `Handle` containing only Invocation ID, Session ID,
and status. The showcase could prove replay only by comparing IDs, and it had
to read the Session to learn the Agent ID needed by `listSessions({agentId})`.

`agentId` and `deduplicated` are now preserved on newly admitted Handles,
making idempotency observability and Agent-scoped Session listing direct.

### P2 (addressed): strengthen tool and structured-output types

The facade accepts schemas as `Record<string, unknown>`, pending ToolCall input
is generated as `any`, and terminal structured output is
`{[key: string]: any}`. `StructuredOutputProvenance.source` is also generated
as `any | null` even though the wire contract fixes it to `tool_call`.

The raw generated client remains the untyped escape hatch. Generic schema and
tool helpers now bind application types without casts, and the OpenAPI
singleton constants generate as literal enums rather than `any`.

### P2 (addressed): surface the one-active-Invocation rule near chat examples

A Session with a queued, running, or waiting Invocation rejects a new turn with
`session_invocation_active`. The rule is explicit in design documents and the
generated operation description, but not in the Runtime guide's ordinary turn
flow or the TypeScript README.

The TypeScript guide now puts the host posture next to the Session examples:
serialize turn admission per Session, preserve the same idempotency key for
uncertain admission, and treat this conflict as “the earlier turn is still
active,” not as a retry with a new key.

## What felt good

- The source quickstart is now genuinely one command after exporting a provider
  key. Database creation, secrets, migrations, and startup are cohesive.
- The local SDK build and `file:` package install were predictable and fully
  documented.
- `Client.invoke`, camelCase facade fields, replay-safe transport behavior,
  `Handle.result`, and `Handle.text` make the common text path small.
- Errors were typed and actionable: conflict cases exposed stable codes without
  leaking Session existence across scope.
- Durable ToolCall behavior matched the intended model exactly. Parking,
  Session visibility, idempotent acceptance, continuation, transcript
  preservation, and later context all agreed.
- Structured output was not a prose parsing convention; the validated object
  carried durable ToolCall and schema provenance.
- Session message reads, transcript snapshots, and SSE converged on one
  authoritative history.

## Suggested acceptance gate

Keep the basic packed-package onboarding gate short. Add a separate opt-in live
integration profile based on the showcase that proves:

- one multi-turn Session;
- one parked client ToolCall plus equal and changed result replay;
- one structured output result with provenance;
- same/different Agent and tenant reference scoping;
- busy-Session rejection;
- message and Invocation pagination;
- incremental transcript recovery; and
- terminal Session SSE.

The live profile should remain explicit because it makes billable provider
calls. Its build should run in the normal SDK gate so type drift in the example
cannot land unnoticed.
