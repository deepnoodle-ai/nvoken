# nvoken API & SDK design evaluation

- Date: 2026-07-23
- Reviewer persona: Principal Engineer, external / adversarial read
- Scope reviewed: `openapi/runtime.yaml`, `sdk/typescript/src/*`, `sdk/{go,python,rust}`,
  `docs/design/api.md`, `README.md`, field reports in `docs/research/`
- Framing: is nvoken a well-designed "agent turn API" — one layer above the
  raw generation APIs (OpenAI Completions/Responses, Anthropic Messages)?

---

## Bottom line

**Grade: B+ on the core contract, C+ on breadth, and a real parity problem in the SDKs.**

nvoken has made the single most important decision correctly: it models an
agent turn as a **durable, idempotent, recoverable background job** ("Invocation")
rather than a synchronous HTTP call that dies with the connection. That is the
right abstraction for the layer it claims — it is genuinely *above* the
generation APIs, not a re-skin of them. The idempotency model, the credential
model, the structured-output-via-provenance model, and the progressive-disclosure
TypeScript facade are all better-than-industry-standard.

But the surface is **narrow where the market is wide**, and it **oversells its
own breadth** in two places that will burn evaluators:

1. The "agent harness" runs **no tools of its own**. Every tool is executed by
   the host (host tools) or a host HTTPS endpoint (callback tools). There are no
   built-in tools (web search, code exec, file ops) and no MCP in the frozen
   contract. Competitors (Responses API, Managed Agents, Bedrock AgentCore) lead
   with exactly that. This is a defensible *philosophy* but it is not what
   "harness-as-a-service" signals, and the README doesn't own the gap.
2. The rich, delightful ergonomics (`agent()`, `run()`, `text()`, auto
   tool-dispatch, session serialization) exist **only in TypeScript**. Go,
   Python, and Rust ship the low-level handle API. The README's claim that all
   four SDKs "wrap that surface with workflow helpers" is true only for the
   generated transport, not the ergonomics.

Fix the honesty gaps and close a handful of flexibility holes and this is an
A‑tier contract. The bones are excellent.

---

## What the abstraction actually is (and why it's right)

The generation APIs (per the pasted comparison) are stateless request/response:
you own history, you re-send it every call, the call is synchronous, and
provider quirks leak (JSON-Schema dialects, `tool_choice` + thinking conflicts,
manual cache markers, temperature constraints). nvoken sits one level up:

| Concern | Generation APIs | nvoken |
| --- | --- | --- |
| Execution | Synchronous, connection-bound | `POST /v1/invocations` → `202 queued`, runs off the request handler (`runtime.yaml:85-115`) |
| History | Caller re-sends every turn | Durable Sessions + canonical transcript, composed server-side |
| Retry safety | Optional / best-effort | Mandatory `idempotency_key` with material-equality fingerprinting (`api.md:48-99`) |
| Crash recovery | None | Checkpoint + requeue from last committed boundary |
| Tool pauses | Caller loops | Invocation *parks* at `waiting`, host submits results by ID, any engine resumes |
| Structured output | 3 providers, 3 dialects | One bounded schema subset + server-validated object with schema-digest provenance |
| Credentials | Your key in your process | BYOK lifecycle: caller/account/tenant/platform/installation, encrypted, rotated |

This is a coherent thesis: **"stateless about *definitions*, stateful about
*conversations*."** The README's "stateless like the LLM APIs" line
(`README.md:30-35`) is initially jarring — the contract is *deeply* stateful —
but the intended meaning (no agent definitions to register/migrate; spec is
inline per call) is a genuinely strong multi-tenant position and the one thing
every competitor in the comparison table fails. Keep this. Just stop calling it
"stateless" without the qualifier in the first sentence a reader sees.

---

## What is genuinely strong (do not regress)

- **Durable idempotent admission.** `idempotency_key` required, scoped to
  (Account, tenant, agent_key), material-equality with versioned fingerprints
  (v1–v7) and language-neutral fixtures. This is production infrastructure-grade
  and better than anything the generation APIs offer. `api.md:71-99`.
- **The credential model is the standout feature.** Five sources, encryption,
  rotation with bounded overlap, revocation, per-call re-check, and provenance
  that never falls through to a different payer (`runtime.yaml:1326-1458`,
  `1846-1867`). No generation API and few harnesses model multi-tenant BYOK this
  carefully. This alone is a reason to adopt.
- **Structured output as a first-class, provider-neutral, *verifiable*
  primitive.** Server-validated terminal object + `schema_sha256` provenance
  (`runtime.yaml:1868-1885`) sidesteps the entire "three providers, three JSON-
  Schema interpretations" mess from the FutureSearch piece by defining one
  bounded subset and rejecting the rest. Pragmatic and correct.
- **Progressive disclosure in the TS SDK.** `text()` → `run()` → `invoke()` +
  `InvocationHandle` → `client.raw()` is a textbook good layering
  (`client.ts:509-624`, `815-1007`). `client.invocation(id)` as a *lazy* handle
  that recovers work in another process is exactly the right primitive for a
  durable system (`client.ts:587-594`).
- **Error model.** Typed `category` + wire `code` + `request_id` +
  `Retry-After` + non-disclosing `not_found` + dedicated `SessionBusyError`/
  `InvocationError`/`MissingToolHandlerError` (`client.ts:37-125`,
  `1389-1474`). Clean, consistent, actionable.
- **Streaming recovery is honest about durability.** Durable frames carry SSE
  `id` cursors; deltas are explicitly ephemeral; `stream.resync` tells you to
  discard provisional output; disconnect never cancels. The SDK reconnects on
  the durable cursor (`stream.ts:154-214`). This is the correct hard-won design.
- **Self-evident iteration.** The `docs/research` field reports show the team
  found and fixed real DX traps (terminal-only `wait()` hiding tool work,
  `sessionKey` missing from the facade, `any`-typed schemas). Healthy process.

---

## Findings (actionable, severity-ranked)

### P0 — Fix the positioning/honesty gaps before external eval

**F1. "Agent harness as a service" oversells; the harness executes no tools.**
The only tool modes are `host` (your backend runs it, Invocation parks) and
`callback` (nvoken POSTs your HTTPS endpoint) — `runtime.yaml:1526-1596`. There
are **no built-in server tools and no MCP** in the frozen contract (PRD 029
exists but isn't in `runtime.yaml`). Every competitor in your own comparison
table (`README.md:160-169`) leads with built-in web search / code exec /
computer use. An evaluator reading "harness-as-a-service" will expect the agent
to *do things*, find it can only pause and ask the host to do things, and feel
misled.
→ **Action:** Either (a) reframe the headline to what it is — a *durable agent
turn runtime / orchestration layer* where the host owns tools — or (b) ship at
least one built-in tool (web search or a sandboxed fetch) so the word "harness"
is load-bearing. Pick one before the next public push. My recommendation: (a)
now, (b) as roadmap. Honesty here is cheaper than a bad first impression.

**F2. SDK ergonomics parity is TS-only but marketed as four-language.**
`README.md:70-73` says the Go/TS/Python/Rust SDKs "wrap that surface with
workflow helpers." Measured facade surface (`agent()`, `AgentSession`, `run()`,
auto `dispatchTools`, session serialization): **TS ~11 hits, Go 0, Rust 0,
Python ~1.** Go/Python/Rust have `Client` + `InvocationHandle` (Invoke/Wait/
Result/Text/SubmitToolResults) but *no* auto tool-dispatch loop and *no* agent
abstraction. A Go user must hand-roll the wait/park/submit/resume loop that
`Agent.runImmediately` gives TS users for free (`client.ts:937-1006`).
→ **Action:** Either bring `Agent`/`run`/auto-dispatch to at least Python and Go,
or scope the README claim to "generated typed transport + streaming/callback
helpers; the high-level Agent facade is TypeScript today." Do not imply parity
that doesn't exist.

### P1 — Flexibility holes that block advanced use cases

**F3. No model/sampling parameters. At all.** `InlineExecutionSpec` is
`{instructions, model, limits, output, tools}` (`runtime.yaml:1500-1525`).
There is no `temperature`, `top_p`, `max_tokens` beyond a cost/iteration guard,
no reasoning/thinking-effort control, no `stop` sequences, no seed. The pasted
articles spend paragraphs on exactly these knobs (thinking budgets, temperature
constraints for reasoning models). You emit `thinking.delta` events
(`runtime.yaml:2235-2267`) but give no way to *turn thinking on* or budget it.
For a "one layer above generation" API, omitting the generation knobs entirely
is a serious flexibility gap.
→ **Action:** Add an optional, provider-neutral `spec.sampling` (temperature,
top_p, max_output_tokens already partly in limits, stop) and a
`spec.reasoning` (effort/budget) with normalization + fail-closed on
unsupported (provider, param) pairs — the same "normalize the quirks" service
the FutureSearch team had to build by hand. This is precisely the value a
turn-API should add.

**F4. Input is text-only; multimodal is defined-but-forbidden.** `InvocationInput`
accepts only `TextInputBlock` (`runtime.yaml:1468-1499`) and the launch contract
"supports text only," yet the catalog advertises `input_modalities`
(`runtime.yaml:1230-1236`). Images/PDFs are table stakes for support-triage and
document workloads — the exact examples in the spec.
→ **Action:** Land an `image`/`document` input block behind a capability flag,
or the catalog's `input_modalities` is a promissory note the contract can't
honor.

**F5. Provider enum is hard-locked to two; the "multi-provider" story is
aspirational.** `ModelProvider: [anthropic, openai]` (`runtime.yaml:1155-1157`)
and the TS SDK hard-validates the same two (`client.ts:1373-1380`). Meanwhile
`ModelCatalogProvider` is an open pattern (`runtime.yaml:1158-1163`) and the
README leans on Dive for "never assumes a single vendor." Today it assumes
exactly two, and Gemini — a first-class citizen in every pasted article — is
absent. The split between the closed `ModelProvider` enum and the open
`ModelCatalogProvider` pattern is also an internal inconsistency that will bite
codegen consumers.
→ **Action:** Either add Google now or soften the multi-provider claim to
"Anthropic + OpenAI today, pluggable via Dive." And reconcile the two provider
types — pick open-with-validation everywhere.

**F6. One-nonterminal-Invocation-per-Session is a sharp, under-advertised
constraint.** A Session with any queued/running/**waiting** Invocation rejects
new turns with `session_invocation_active` (`api.md:55-57`). A host tool that
parks and never gets a result pins the whole Session until `waiting_timeout`
(default 1800s — `runtime.yaml:303-307`). No parallel turns in a session, ever.
The TS `AgentSession` hides this by serializing locally (`client.ts:1009-1046`),
but cross-process/cross-node races throw, and non-TS users get no help.
→ **Action:** Keep the rule (it's the right durability call) but (a) document it
next to every "chat" example, not only in design docs, and (b) consider a
per-Session `cancel-and-supersede` admission option so a stuck `waiting` turn
doesn't require waiting out a 30-minute timeout.

**F7. Structured-output / tool-input schema subset is very small.** 16 schema
positions, 32 KiB, no `$ref`, no `oneOf`/`anyOf`, single-type-per-position,
bounded keyword set (`runtime.yaml:1597-1616`). This cleanly dodges the
provider-dialect quirks — but real product schemas (discriminated unions,
reused sub-objects, nested arrays-of-objects) won't fit, and Zod/Pydantic
models routinely emit `$ref`/`anyOf`. The TS SDK strips `$schema`
(`client.ts:1283`) but does nothing about `$ref`/`anyOf`, so those schemas fail
at the Runtime after a clean-looking local conversion.
→ **Action:** Either implement server-side `$ref` inlining + `anyOf`→`enum`/
union lowering (the same normalizations the FutureSearch piece describes), or
give the SDK a pre-flight validator that rejects unsupported constructs *before*
admission with a message naming the offending keyword and path.

### P2 — Ergonomics & consistency polish

**F8. "Agent" is an overloaded, confusing noun.** In nvoken an "Agent" is an
*identity anchor* storing "identity only" (`runtime.yaml:1271-1278`); the actual
agent *definition* is the inline `spec` re-sent every call. Everyone arriving
from Managed Agents / Bedrock / LangChain reads "Agent" as model+prompt+tools.
The collision guarantees a "wait, where do I put the system prompt on the Agent?"
support thread.
→ **Action:** Rename the anchor (`agent_key` → `agent_ref`/`agent_slug`, or call
the resource an "Agent identity"/"conversation namespace") or lead every doc
with the identity-tuple diagram from the field report (`...field-report.md:118-122`).
At minimum, the root README request example should show the tuple.

**F9. The simple path still costs a poll loop.** `run()`/`text()` admit then
`wait()` with exponential backoff to 2s (`client.ts:1134-1158`). After
completion you can eat up to ~2s of latency before the terminal read fires.
Fine for background work; not great for an interactive "just answer me" call.
→ **Action:** Prefer the streaming admission path (`POST` + `Accept:
text/event-stream`, already in the contract) under the hood for `text()`/`run()`
so the simple case gets the completion push, not a poll — or expose a
long-poll/watermark wait. The API already supports the better path; the default
ergonomic doesn't use it.

**F10. `output_text` concatenation without separators is a footgun.**
`InvocationResult.output_text` joins assistant text blocks "without separators"
(`runtime.yaml:1785-1795`). Multi-block replies silently glue words together.
→ **Action:** Join with `\n` (or document the exact contract loudly). Most
callers expect the Managed-Agents-style print loop and won't notice until a
customer does.

**F11. `agent.text()` throws on empty output.** `Agent.text` treats
completed-without-text as an error (`client.ts:877-886`). A valid completion
(e.g. a pure tool/structured-output turn) is not an exception.
→ **Action:** Return `null`/empty and let the caller decide, or reserve the
throw for genuinely anomalous terminal states.

---

## Simple case vs. advanced case scorecard

| Dimension | Verdict | Notes |
| --- | --- | --- |
| Trivial "one turn, get text" | **Strong (TS)** | 4-line quickstart (`README.md`/SDK README) is genuinely clean. |
| Multi-turn chat | **Strong** | Session binding + local serialization is elegant. |
| Structured output | **Strong but narrow** | Best-in-class provenance; schema subset too small (F7). |
| Host-orchestrated tools | **Good** | Durable park/resume is correct and rare. Auto-dispatch TS-only (F2). |
| Built-in / MCP tools | **Absent** | The headline gap vs. competitors (F1). |
| Sampling / reasoning control | **Absent** | No generation knobs at all (F3). |
| Multimodal input | **Absent** | Text-only despite catalog claims (F4). |
| Multi-provider | **Two only** | No Gemini; claim outruns contract (F5). |
| Durability / recovery | **Excellent** | The core differentiator; keep. |
| Multi-tenant / BYOK | **Excellent** | Standout feature. |
| Cross-language DX | **Uneven** | TS delightful; Go/Py/Rust are transport-level (F2). |
| Error handling | **Excellent** | Typed, non-disclosing, actionable. |

---

## Prioritized action list

1. **(P0, cheap) Reframe the headline and the four-SDK claim** to match reality
   (F1, F2). Honesty now; features later.
2. **(P1) Add `spec.sampling` + `spec.reasoning`** with per-provider
   normalization and fail-closed (F3). This is the highest-leverage *new value*
   a turn-API can add over the raw APIs.
3. **(P1) Land multimodal input** or drop the `input_modalities` advertisement
   (F4).
4. **(P1) Add Google/Gemini** or soften the multi-provider claim, and reconcile
   `ModelProvider` vs `ModelCatalogProvider` (F5).
5. **(P1) Grow the schema subset or add a pre-admission validator** that names
   the unsupported keyword/path (F7).
6. **(P1) Bring `Agent`/`run`/auto-dispatch to Python and Go** (F2 follow-through).
7. **(P2) Default the ergonomic simple path to streaming admission** (F9), fix
   `output_text` separators (F10), soften `text()`'s empty-output throw (F11),
   document the one-active-Invocation rule inline and consider
   cancel-and-supersede (F6), and de-overload "Agent" (F8).

The core contract is one of the better agent-turn designs I've read: the
durability, idempotency, and credential models are things most teams get wrong
and this team got right. The work left is **breadth and honest framing**, not
foundations.

---

## Appendix A: Code index (fast lookup)

Anchors captured at revision on 2026-07-23; line numbers drift, so treat them as
"start here," not exact addresses. Layout follows the hexagonal architecture in
`CLAUDE.md` (domain → ports → services → adapters).

### The contract (start here)

| Area | Location |
| --- | --- |
| Full Runtime OpenAPI spec | `openapi/runtime.yaml` |
| — Create/admit Invocation (202 model) | `runtime.yaml:85-270` |
| — Result read (`output_text` projection) | `runtime.yaml:327-407`, schema `1770-1795` |
| — Invocation SSE stream | `runtime.yaml:408-453` |
| — Host tool-results submit (park/resume) | `runtime.yaml:486-561` |
| — Provider credentials lifecycle | `runtime.yaml:562-708` |
| — Model catalog | `runtime.yaml:709-837` |
| — Sessions / messages / transcript | `runtime.yaml:838-1066` |
| — `InlineExecutionSpec` (F3/F4 gaps live here) | `runtime.yaml:1500-1525` |
| — Tool specs (host / callback) | `runtime.yaml:1526-1596` |
| — Structured-output schema subset (F7) | `runtime.yaml:1597-1616` |
| — `ModelProvider` enum vs open `ModelCatalogProvider` (F5) | `runtime.yaml:1155-1163` |
| — Stream event union + delta events | `runtime.yaml:2179-2390` |
| — Error codes / responses | `runtime.yaml:2391-2500` |
| Identity/admin OpenAPI | `openapi/identity.yaml` |
| Narrative contract + fingerprint versions | `docs/design/api.md` |
| Fingerprint canonical fixtures | `docs/design/admission-fingerprint-v{1..7}.json` |

### HTTP adapter (wire → service)

| Area | Location |
| --- | --- |
| Route table (all endpoints, one place) | `internal/adapters/httpapi/server.go:193-220` |
| Invocation handlers | `httpapi/server.go` (`h.invocations`, `getInvocation`, `getInvocationResult`, `cancelInvocation`, `submitHostToolResults`) |
| SSE streaming handler | `internal/adapters/httpapi/stream.go` |
| Auth / credential identity resolution | `internal/adapters/httpapi/identity.go` |
| JSON encode/decode + error mapping | `internal/adapters/httpapi/json.go` |

### Domain types (pure, zero-dep)

| Concept | Location |
| --- | --- |
| Agent / Session / Invocation / status | `internal/domain/runtime.go:8-106` |
| `InvocationStatus.Terminal()` | `internal/domain/runtime.go:46-64` |
| Messages, lifecycle changes, claims | `internal/domain/runtime.go:107-172` |
| Generation request/response/resume | `internal/domain/runtime.go:174-267` |
| ToolCall, checkpoints, callback delivery | `internal/domain/toolcall.go` |
| Provider credentials (sources/scopes/versions) | `internal/domain/provider_credentials.go` |
| Structured output + provenance | `internal/domain/runtime.go:214-245` |
| Streaming deltas / resync / end | `internal/domain/streaming.go` |
| Execution dispatch (engine handoff) | `internal/domain/dispatch.go` |
| Model catalog + pricing | `internal/domain/model_catalog.go` |
| Stable ID prefixes (`invk_`, `sesn_`, …) | `internal/domain/ids.go` |
| Auth profiles / credential kinds / device auth | `internal/domain/auth.go` |

### Services (business logic — the actual harness)

| Responsibility | Location |
| --- | --- |
| Admission / runtime orchestration | `internal/services/runtime.go` |
| **Idempotency fingerprint (v1–v7)** | `internal/services/fingerprint.go:13-19` (dispatch by version) |
| **Generation executor + iteration loop** | `internal/services/generation.go:85-183` (`Execute`), `427-471` (`generate`) — where sampling/reasoning params (F3) would be threaded |
| Structured-output submit tool + validation | `internal/services/structured_output.go`, schema subset `client_tool_schema.go` |
| Host/callback tool orchestration | `internal/services/toolcalls.go`, `callbacks.go`, `client_tools.go` |
| Crash recovery / checkpoint continuation | `internal/services/recovery.go`, `generation_recovery.go`, `recovery_cursor.go` |
| Credential resolution (BYOK selection) | `internal/services/credential_resolver.go`, `invocation_credentials.go`, `provider_credentials.go` |
| Limits / budgets / cost guardrail | `internal/services/controls.go` |
| Result composition (`output_text`) | `internal/services/invocation_result.go` |
| Engine dispatch | `internal/services/dispatch.go` |
| Identity / account | `internal/services/identity.go`, `bootstrap.go` |

### Provider integration (multi-provider seam)

| Area | Location |
| --- | --- |
| Dive-based provider adapter (Anthropic/OpenAI; F5 lives here) | `internal/adapters/divegen/` |
| Secret encryption for BYOK | `internal/adapters/secretcrypto/` |
| Callback HTTP delivery (HMAC signing) | `internal/adapters/callbackhttp/` |
| Postgres repos / sqlc queries / migrations | `internal/adapters/postgres/{queries,sqlc,migrations}` |

### SDKs

**TypeScript (the reference facade — richest surface):**

| Area | Location |
| --- | --- |
| `Client` (transport, retry, config resolution) | `sdk/typescript/src/client.ts:450-813` |
| `Client.invoke` / lazy `invocation()` | `client.ts:541-594` |
| **`Agent` facade (`run`/`text`/`invoke`/`stream`)** | `client.ts:815-935` |
| **Auto host-tool dispatch loop** (TS-only, F2) | `client.ts:937-1006` |
| `AgentSession` (per-Session serialization, F6) | `client.ts:1009-1086` |
| `InvocationHandle` (`wait`/`waitForResult`/`result`) | `client.ts:1088-1214` |
| `wait({ until })` semantics | `client.ts:1134-1172`, `1265-1272` |
| Schema handling (Standard Schema, `$schema` strip; F7) | `client.ts:154-206`, `1274-1311` |
| Model validation (2-provider lock; F5) | `client.ts:1373-1380` |
| Error normalization / typed errors | `client.ts:37-125`, `1389-1494` |
| SSE parse + resumable stream loop | `sdk/typescript/src/stream.ts:137-214`, parser `374-427` |
| Callback verification helper | `sdk/typescript/src/callback.ts` |
| Public exports | `sdk/typescript/src/index.ts` |

**Other languages (transport + handle level; no `Agent`/auto-dispatch — F2):**

| SDK | Entry / notes |
| --- | --- |
| Go | `sdk/go/client.go` (`Client`, `InvocationHandle`), `stream.go`, `callback.go`, `errors.go` |
| Python | `sdk/python/src/nvoken/client.py` (`Client:142`, `invoke:202`, `stream:520`), `stream.py`, `callback.py` |
| Rust | `sdk/rust/src/client.rs` (`stream:743`), `stream.rs`, `callback.rs` |
| Shared generation manifest / conformance | `sdk/operations.json`, `sdk/conformance/`, `sdk/internal/genmanifest/` |

### Examples & prior field reports

| Area | Location |
| --- | --- |
| Minimal chat example | `examples/typescript-chat/` |
| Full showcase (tools, structured output, tenancy, SSE) | `examples/typescript-invoke-showcase/` |
| TS invoke + Sessions field report (DX gaps + fixes) | `docs/research/2026-07-22-typescript-invoke-and-sessions-field-report.md` |
| Local onboarding passes | `docs/research/2026-07-2{1,2}-*onboarding*.md` |
| PRDs (e.g. remote MCP `029`, structured output `013`) | `docs/prds/` |
