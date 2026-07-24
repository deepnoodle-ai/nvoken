# Proposal: the nvoken API & SDK, from B+ to excellent

- Status: Proposal (decision-ready)
- Date: 2026-07-24
- Responds to: [API & SDK design evaluation](../research/2026-07-23-api-sdk-design-evaluation.md)
- Scope: `openapi/runtime.yaml`, all four SDKs, the CLI, examples, README
  positioning, and the vocabulary itself.

---

## Why now

The 2026-07-23 evaluation graded the core contract B+ and it is right about
why: the durable-idempotent-turn model, the credential model, and the
streaming-recovery model are better than industry standard. It is also right
that the gaps are breadth and honesty, not foundations.

This proposal goes one step further than the evaluation. We have **zero
external users**. That is a closing window: every rename, every contract
tightening, every "we should have made this a hard error" is nearly free today
and nearly impossible in six months. The evaluation asked "is this good?"; this
document asks "what would we ship if we treated every name and every behavior
as a deliberate, permanent choice?" — and then chooses.

Everything below is written to be decided, not discussed. Each item carries a
recommendation and a cost. Where I would overrule the evaluation, I say so and
why.

## The bar we are holding ourselves to

Four principles, used as the test for every item in this document:

1. **The name is the contract.** A reader who knows only the noun should
   predict the behavior. Where a name misleads ("Agent" that stores no agent
   definition, "harness" that runs no tools), either the behavior or the name
   changes — never the reader's expectations.
2. **One obvious path, honestly layered.** `text()` → `run()` → `invoke()` →
   `raw()` is the right ladder. Each rung must be the *best* way to do its job,
   not merely the shortest. A convenience that silently costs 2s of latency or
   pins a Session for 30 minutes is not a convenience.
3. **Every behavior is a solid contract.** No "usually", no "without
   separators" surprises, no list endpoint that cannot paginate, no enum that
   is closed in one schema and open in another. If we document it, we fixture
   it; if we can't fixture it, we don't ship it.
4. **Fail closed, explain precisely.** Anything we cannot honor (a sampling
   parameter a provider rejects, a schema keyword outside the subset) is
   rejected *before admission* with the offending path named — never silently
   dropped, never discovered mid-turn.

---

## Part 1 — Vocabulary: name every noun deliberately

### 1.1 Keep `Invocation`, and make the turn/Invocation relationship a rule (flagship decision)

An earlier draft of this proposal recommended renaming the resource to
`Turn`, on the grounds that our own prose calls it a turn everywhere the
wire calls it an Invocation. The recommendation is reversed, for a reason
the first draft underweighted: **the verb is the brand, and the verb is
already in the code.**

The product is named nvoken. The SDKs' core method is `invoke()`. The CLI's
core command is `nvoken invoke`. The resource is the durable record of an
invocation. That is a rare, fully aligned chain — brand → verb → noun — and
it is self-teaching in exactly the way principle 1 demands: the product's
name tells you the verb, and the verb tells you the noun. A rename to
`Turn` would break the chain at its most visible link. `client.invoke()`
returning a `TurnHandle` trades a prose/wire mismatch for a verb/noun
mismatch *inside the SDK*, where developers spend their time; renaming the
verb instead would discard `invoke()` from a product called nvoken.

What still needs fixing is the drift the first draft correctly diagnosed:
README and design docs speak "turn" while the contract speaks "Invocation",
and the reader is left to infer the relationship. Fix it with one definition
and two discipline rules, applied everywhere:

- **The definition, stated once and early** in the README, `api.md`, and
  each SDK README: **"An Invocation is one durable agent turn."**
- **"turn" is the lowercase conceptual word.** It may (and should) appear in
  descriptive prose — "nvoken runs the whole agent turn" stays. It never
  appears capitalized, and never names an API identifier, event, error code,
  or type.
- **"Invocation" is the resource.** Capitalized, used wherever the durable
  record is meant; never used for the conversational concept ("three
  Invocations of small talk" is wrong; "three turns" is right).

This is the Git pattern — a commit records a snapshot — and it works because
the relationship is defined once instead of inferred per page. The
concurrency law gets the phrasing that makes it self-evident under this
rule: **"a Session runs one turn at a time: at most one nonterminal
Invocation."** (`session_invocation_active` and the `invk_` prefix keep
their names.)

Record the decision and its reasoning in `docs/design/decisions.md`, so the
`Turn` question — which is a good question — is answered once instead of
annually. Cost: prose-only edits; nothing on the wire moves.

### 1.2 Keep `agent_key`, fix the "Agent" expectation problem structurally

Evaluation F8 is real: everyone arriving from Managed Agents, Bedrock, or
LangChain reads "Agent" as model+prompt+tools, and ours stores identity only.
But I would not rename `agent_key`. The `*_key` family — `agent_key`,
`tenant_key`, `session_key`, `idempotency_key` — is one of the API's quietly
excellent decisions: four host-owned identifiers with identical semantics
(you name it, we scope by it). Renaming one to `agent_ref` or `agent_slug`
breaks the symmetry that teaches the model.

Fix it with structure instead:

1. **Give the Agent a read surface.** Today the Agent resource is write-only
   by side effect: it has *no endpoint at all*. A host that keys everything by
   `agent_key` cannot list its Agents, cannot resolve an `agent_id` without
   admitting work, and cannot filter Session lists by the key it actually owns
   (`listSessions` filters by `agent_id` only). Add:

   | Method | Endpoint | Purpose |
   | --- | --- | --- |
   | `GET` | `/v1/agents` | List Agent identity anchors (filter: `agent_key`). |
   | `GET` | `/v1/agents/{agent_id}` | One anchor: id, key, created_at, session/invocation counts later. |

   and accept `agent_key` as a filter on `GET /v1/sessions` and
   `GET /v1/invocations` alongside `agent_id`. The moment the resource is readable
   and visibly tiny — `{id, agent_key, created_at}` — its "identity only"
   nature is self-documenting. Nothing teaches "the Agent stores no config"
   faster than fetching one.

2. **Lead every doc with the identity tuple.** The four-key diagram
   (`Account → agent_key → tenant_key → session_key`, spec travels per-Invocation)
   goes in the README request example, the SDK READMEs, and the top of
   `docs/design/api.md` — not only in a field report.

3. **Name the resource "Agent identity" in prose** ("the Agent identity
   anchor"), reserving bare "agent" for the host-side concept.

### 1.3 Field-level naming audit (the small deliberate choices)

Sweep-level items, each cheap, each worth doing before freeze:

| Surface | Today | Problem | Decision |
| --- | --- | --- | --- |
| TS `Agent.runImmediately()` | public | Public synonym of `run()`; two names for one behavior invites "which one?" questions | Make private (`#runLoop`) — `run()` is the name |
| TS `Client.replaySafe()` | public | Internal retry wrapper leaked into the public surface with a name only we understand | Make private; if users need wrapped calls, expose as documented `request()` later |
| TS error category `"timeout"` for local aborts | `sleep()`/stream cancel reject with category `timeout` | An `AbortSignal` cancel is not a timeout; miscategorization breaks caller retry logic | Add category `"cancelled"`; reserve `"timeout"` for actual deadline expiry |
| TS `AgentResult.deduplicated` | `boolean \| undefined` | The ack always carries it; `undefined` is a modeling leak | Type as `boolean` |
| Wire `ErrorCategory` mapping | 403 → `"authentication"` | Authentication ≠ authorization; hosts branch differently (re-auth vs report) | Map 403 → new `"permission"` category |
| `ProviderCredentialList` | `{items}` + `limit` param, no cursor | The only list endpoint that cannot paginate: a bounded `limit` with no way to get the rest is a broken contract at >100 credentials | Adopt the standard `{items, has_more, next_cursor}` envelope now, while it's free |
| `ModelProvider` (closed enum) vs `ModelCatalogProvider` (open pattern) | two provider types | Codegen consumers get incompatible types for the same concept (evaluation F5) | One open-with-validation type everywhere; the server rejects uninstalled providers at admission — the *enum* stops being the gatekeeper |
| `output_text` join rule | concatenated "without separators" | Multi-block replies glue words together silently (evaluation F10) | Contract: blocks within one assistant message concatenate directly (they are one intentional text run); *distinct assistant messages* join with `"\n\n"`. Document with a fixture |
| SSE event field `emitted_at` on deltas only | — | fine | keep |
| ID prefixes `invk_`, `agnt_`, `sesn_`, `smsg_`, `tcal_`, `pcrd_`, `pcvr_` | 4-char, vowel-dropped | consistent scheme | keep |

### 1.4 Reset the fingerprint lineage to v1 at first public release

We carry seven admission-fingerprint versions (v1–v7) whose only purpose is
equality-preservation for already-admitted durable rows — rows that today
exist only in our own development databases. Before the first public tag:
collapse to a single v1 that records the final launch vocabulary (including
the new fields from Part 2), delete `admission-fingerprint-v{1..6}.json`,
and rewrite the `api.md` history section to one paragraph. Shipping a public
v1 with a fossil record of six internal iterations is confusing archaeology
for integrators and permanent test surface for us. This is a
now-or-never cleanup.

One rule makes the timing coherent: **the post-reset v1 remains freely
amendable until the first public tag.** No external durable rows exist
before that tag, so a new material field amends v1 in place rather than
minting v2. The reset itself happens in Wave 1; the Part 2 fields
(`mcp_servers`, sampling/reasoning, outcome, `spec.context`, `fork_from`)
join v1 as they land. The first public tag ends the amnesty and version
bumps resume — so the "lands in the single post-reset v1" claims in 2.8
and 2.10 hold exactly for fields that ship before the tag; anything later
starts the v2 lineage honestly.

---

## Part 2 — Contract: close the flexibility holes

### 2.1 `spec.sampling` and `spec.reasoning` (evaluation F3) — confirmed cheap

I verified the threading path: Dive's `llm.Config` already models
`Temperature`, `ReasoningBudget`, and `ReasoningEffort`
(`none|minimal|low|medium|high|xhigh|max`) with per-provider normalization.
The runtime simply never passes them. This is the highest-leverage new value a
turn API can add over raw generation APIs — normalize the knobs the providers
each spell differently — and for us it is mostly plumbing, not provider work.

Proposed spec addition:

```yaml
SamplingSpec:
  type: object
  additionalProperties: false
  properties:
    temperature: { type: number, minimum: 0, maximum: 2 }
    top_p:       { type: number, exclusiveMinimum: 0, maximum: 1 }
    stop_sequences:
      type: array
      maxItems: 8
      items: { type: string, minLength: 1, maxLength: 255 }

ReasoningSpec:
  type: object
  additionalProperties: false
  properties:
    effort:
      type: string
      enum: [none, minimal, low, medium, high, xhigh, max]
    budget_tokens: { type: integer, minimum: 1 }
```

Contract rules (the part that makes this *better* than the raw APIs):

- **Fail closed at admission** on any (provider, model, parameter) pair the
  installation knows to be unsupported, with the offending path named:
  `invalid_request`, `details.field = "spec.reasoning.effort"`,
  `details.reason = "openai/gpt-5.4 does not accept effort: max"`. Never
  silently drop a knob.
- Both objects are optional and omitted-means-provider-default; omission
  stays materially different from explicit values in the fingerprint (same
  rule as limits today).
- `thinking.delta` stream events finally get their missing other half: a way
  to turn thinking on. Ship `spec.reasoning` in the same release that
  documents those events.
- `max_output_tokens` stays in `limits` (it is a guardrail, not a sampling
  preference); the docs cross-reference it from `sampling`.
- **Reasoning round-tripping is nvoken's job — write it into the contract.**
  The Responses API documents that stateless reasoning callers must replay
  *every* output item — encrypted reasoning items, assistant `phase` fields
  — or multi-turn quality silently degrades (their own troubleshooting
  guide covers the model treating an intermediate update as the final
  answer when `phase` is dropped). nvoken composes provider context
  server-side, so this footgun is ours to delete: when `spec.reasoning` is
  active, nvoken preserves and replays provider reasoning artifacts
  (encrypted reasoning items, thinking blocks and signatures, phase
  markers) across iterations *and across Session turns*, losslessly, per
  each provider's rules — and conformance fixtures assert the round-trip.
  This is the sharpest single example of the value a turn API adds: the
  raw-API user must learn this; the nvoken user never sees it. It is also
  the stress test of the transcript's "content blocks preserved
  losslessly" claim — opaque encrypted provider payloads are exactly what
  that claim must survive.

### 2.2 Multimodal input blocks (evaluation F4)

The catalog advertises `input_modalities` the contract cannot accept — a
promissory note. Land `image` and `document` input blocks:

```yaml
ImageInputBlock:
  type: object
  additionalProperties: false
  required: [type, source]
  properties:
    type: { enum: [image] }
    source:
      type: object
      additionalProperties: false
      required: [media_type, data]
      properties:
        media_type: { enum: [image/jpeg, image/png, image/gif, image/webp] }
        data: { type: string, description: base64, maxLength: 20000000 }
```

(`document` follows the same shape with `application/pdf`.) Admission
validates the block against the *selected model's* `input_modalities` and
fails closed — which turns the catalog field from advertisement into contract.
URL-sourced blocks are deliberately deferred (fetching them makes admission
non-atomic and drags in SSRF surface; hosts can fetch and inline).
The 1 MiB admission body limit needs a carve-out or raise for this; propose a
separate 20 MiB limit for requests carrying binary blocks, kept out of the
fingerprint preimage by hashing block digests instead of bytes.

### 2.3 Provider breadth honestly (evaluation F5)

Dive's provider tree today: `anthropic`, `openai`, `openaicompletions`,
`openrouter`, `ollama`, `mistral` — no native Gemini yet. So "add Google now"
is real work in Dive, not configuration. The honest sequence:

1. **Now:** unify on one open provider type (per 1.3) and change the README
   footer from implying breadth to stating it: "Anthropic and OpenAI today;
   the contract and Dive are provider-neutral."
2. **Next:** expose `openrouter` and `ollama` as installation-enabled
   providers. This is cheap (Dive already ships them), and `ollama` in
   particular is a *strategic* fit: the self-hosted, air-gapped story in our
   comparison table gets "run fully local models" for near-zero effort. An
   evaluator running the quickstart against a local model with no API key is
   a demo none of the closed competitors can match. (`mistral` is also
   already in Dive; it waits on demand rather than shipping by default —
   recorded here so the skip is deliberate, not an oversight.)
3. **Then:** native Gemini in Dive, which also unlocks it here.

### 2.4 Schema subset: pre-flight validation, then widening (evaluation F7)

Two-stage answer:

- **(a) SDK pre-flight validator (all four languages), now.** A pure function
  over the bounded keyword set that rejects `$ref`, `oneOf`/`anyOf`,
  multi-type positions *client-side* with the keyword and JSON path named:
  `outputSchema.properties.status uses "anyOf", which the runtime's bounded
  subset does not accept`. The Zod/Pydantic conversion path makes this
  urgent: today a clean-looking local conversion fails server-side with the
  user none the wiser about which construct sank it. Ship the same validator
  in the runtime so the error is identical at both layers (one conformance
  fixture set).
- **(b) Server-side `$ref` inlining + closed-world `anyOf` lowering, next.**
  Bounded expansions only (inline until the 16-position/32 KiB caps, reject
  recursion). This recovers most real Zod/Pydantic output — discriminated
  unions and reused sub-objects — without reopening the
  three-providers-three-dialects mess the subset exists to avoid.

### 2.5 Un-pin the stuck Session: `supersede` (evaluation F6)

Keep the one-turn-at-a-time law — it is the right durability call. But give
the host an escape hatch that doesn't cost a 30-minute `waiting_timeout` when
a tool result is never coming:

```jsonc
POST /v1/invocations
{
  ...,
  "if_active": "supersede"   // default: "reject" (today's behavior)
}
```

Semantics: inside the admission transaction, if the Session's active
Invocation is nonterminal, cancel it (same first-terminal-wins cancellation
as today, pending ToolCalls closed with the canonical synthetic error), then
admit the new Invocation. One transaction, no race window, no client-side
cancel-then-retry-loop choreography. `"reject"` stays the default because
silent supersession is the wrong default for concurrent distinct writers —
but a chat UI where the user hits "regenerate" wants exactly this, in one
call.

Also per the evaluation: the one-turn-at-a-time rule gets documented next to
every chat example, not only in design docs (Part 4).

### 2.6 Settled-Invocation notifications: reuse the callback channel

The background-job thesis has a hole: the *only* ways to learn an Invocation
settled are polling and holding an SSE connection. A serverless host (the
Cloud Run/Lambda shape we explicitly court) must burn a connection or a poll
loop per Invocation. We already built the hard parts of the answer — signed,
idempotent, retried HTTPS delivery — for callback tools. Reuse it:

```jsonc
POST /v1/invocations
{
  ...,
  "notify": { "url": "https://host.example/nvoken/settled" }
}
```

One delivery per terminal transition, payload = the same `invocation.result`
envelope the SSE stream emits, HMAC-signed with the existing v1 protocol,
`Idempotency-Key` = the Invocation ID, same five-attempt retry ladder. No
webhook CRUD, no subscription resource — the URL travels on the Invocation
exactly like callback tools travel on the spec, consistent with "your app
owns the state." (The explicitly-absent "webhook endpoint CRUD" row in
api.md §9 stays absent; this is per-Invocation delivery, not an endpoint
registry.) `notify` is delivery plumbing, not agent behavior, so it
follows the per-Invocation credential precedent: excluded from the
idempotency fingerprint — a retried admission may change the delivery URL
without forging a new identity.

### 2.7 Small contract tightenings

- **`GET /v1/invocations` accepts `status` repeated** (`status=queued&status=running`)
  — "all nonterminal" is the query every dashboard wants and today takes
  three calls.
- **`Retry-After` on 503** (currently only 429 documents it) — we already
  tell SDKs to honor it; make the server contract say when it appears.
- **`deltas=` on both stream endpoints.** Preview frames become a
  per-connection choice (borrowed from Managed Agents' `event_deltas[]`):
  a connection that opts out receives only durable frames — complete,
  correct, and cheaper. Dashboards, reducers, and audit consumers tailing
  `transcript.update` today pay `output_text.delta`/`thinking.delta`
  bandwidth for nothing. Additive; the default preserves current behavior.
- **Cumulative usage on the Session read.** `GET /v1/sessions/{id}` gains a
  read-time usage aggregate across the Session's Invocations. The
  usage-events export stays the reconciliation ledger; this is the
  budget-UI convenience so hosts stop summing it themselves.
- **Session `metadata`**: stays out (deliberate; hosts own product data), but
  say so in api.md §9 with the replacement pattern (host DB keyed by
  `session_key`), because every evaluator asks within the first hour.

### 2.8 Remote MCP tools: pull PRD 029 forward — this is the traction feature

The evaluation's P0 finding (F1) is that "harness-as-a-service" oversells a
runtime that executes no tools. Part 5 fixes the *words*; MCP fixes the
*fact* — and it is the only tool-breadth investment that doesn't grow
nvoken's surface per capability. That resolves the real tension here
(support people rapidly building all kinds of apps, without over-building
before traction): every MCP server anyone publishes becomes an nvoken
capability, and our scope grows by **one** feature, once. Built-in tools
grow scope per tool forever; MCP grows reach per tool for free. That
asymmetry is why MCP is the highest-priority capability item in this
proposal, ahead of everything else in Part 2.

[PRD 029](../prds/029-prd-remote-mcp-tools.md) already exists in draft and
has the design **right** — endorse its shape as-is rather than reopening it:

- **Remote streamable-HTTP only; the host brings the URL and token.** No
  OAuth broker, no stdio process management, no stored MCP credential
  resources. This keeps the credential philosophy intact (host owns
  integrations; secrets bind per-Invocation, encrypted, destroyed at
  settlement) and keeps the scope honest. The Mobius production seam it
  ports is proof the narrow version is the useful version.
- **Spec-carried server declarations** (`spec.mcp_servers`) are exactly the
  "stateless about definitions" thesis extended to tools: nothing to
  register, per-tenant MCP servers are just a query in the host's database.
  No competitor's registered-connector model can say that.
- **Durable discovery snapshot + fenced in-turn execution + annotation-gated
  crash retry** makes MCP calls first-class citizens of the recovery model
  instead of a bolt-on — the differentiator competitors' MCP support lacks.
- **`POST /v1/mcp/list-tools`** (R9) is a quiet DX gem: "point nvoken at
  your MCP server and see the projected tools in one command, before
  writing any code." Lead the MCP docs with it.

What this proposal adds on top of the PRD (which scopes SDK work out):

1. **SDK surface in the same release, not after.** All facades get
   `mcpServers` on the spec (TS helper `mcpServer({name, url, headers,
   allowedTools})`), and `client.listMcpTools(server)` wrapping R9. An MCP
   feature that requires hand-built JSON is half-shipped.
2. **CLI: `nvoken mcp list-tools --url … [--header …]`** — the R9 probe as
   a shell command, next to `model check` in the onboarding story.
3. **Fingerprint coordination with 1.4.** The PRD specifies fingerprint v8;
   with the Wave 1 lineage reset, `mcp_servers` lands in the single public
   v1 instead — a concrete payoff of doing the reset first.
4. **Positioning payoff** (amends Part 5): once shipped, the comparison
   table's tools cell becomes an honest "any remote MCP server, durably
   executed" and the README can say "bring your tools as host tools,
   callback tools, or any MCP server." Until it ships, we claim host and
   callback tools only.
5. **Confirmation gating as the trust answer.** The PRD's stated risk —
   retry gating trusts server-asserted annotations, so a mislabeled
   destructive tool can double-fire after lease loss — deserves a stronger
   fast-follow than a retry blacklist: a per-server or per-tool
   `confirmation: required` flag (borrowed from Managed Agents'
   `user.tool_confirmation`). A gated call parks the Invocation in
   `waiting` with a pending approval the host resolves allow/deny —
   reusing the existing host-tool park/resume machinery wholesale, no new
   infrastructure. nvoken executing tools for the first time is exactly
   when human-in-the-loop control earns its place.

**Sequencing: start the moment Wave 1 merges, on its own track** (nothing
else in Wave 2 blocks it, and it is the longest pole in the tent).

### 2.9 Built-in tools: a strict admission test, not a roadmap

With MCP in hand, most "should nvoken bundle tool X?" questions answer
themselves: point at an MCP server. So built-ins are governed by an
admission test rather than a wishlist. A tool ships inside nvoken only if
**all four** hold:

1. It needs no host-side state and no credentials beyond installation
   config.
2. Executing it host-side would add a round trip with no product value (the
   host would just proxy it).
3. It materially strengthens the zero-config quickstart/demo path.
4. A well-known public MCP server could not serve it equally well.

Applying the test today:

- **`fetch` (guarded HTTP GET → bounded text) — passes; ship it.** The
  SSRF-guarded, redirect-refusing, public-only HTTP client already exists
  (built for callback tools, reused by MCP egress). Zero credentials, and
  it makes the quickstart demo self-sufficient: "summarize this URL" with
  no tool code written. It uses the already-reserved `nvoken_` tool-name
  prefix and the already-real internal `builtin` mode (the structured-output
  submit tool) — plumbing exists end to end.
- **Web search — defer, reassess after MCP ships.** Fails condition 1
  (needs a search-provider key and a provider choice) and probably
  condition 4 (hosted search MCP servers exist and multiply). If demand
  says otherwise, it enters as installation-configured, not default-on.
- **Code execution — rejected for now, explicitly.** Sandbox infrastructure
  is a different product with its own security posture; the design's answer
  stands: host sandboxes are host tools. Revisit only on strong, repeated
  demand — and record that in the decision log so it isn't relitigated
  monthly.

This is the focus discipline in one sentence: **MCP is how nvoken supports
everyone else's tools; built-ins exist only where the runtime is the only
sane place to run them.**

### 2.10 Outcome-graded Invocations: declare what done looks like

Managed Agents' most interesting capability is outcomes: the caller declares
a goal and a rubric, and the harness runs a work → grade → revise loop — a
grader with a *separate context window* (so it isn't anchored on the agent's
implementation choices) scores the deliverable per criterion and hands the
feedback back until the rubric is satisfied or an iteration cap is hit.
Decision: **support it.** It fits nvoken better than it first appears:

- **It is a stopping condition, not a scheduler.** nvoken already owns the
  within-turn loop — `max_iterations`, the structured-output retry floor.
  An outcome is a smarter answer to "is this turn done?", running inside
  the same fenced, checkpointed loop. The boundary holds: cross-turn
  orchestration, schedules, and re-grading remain the host's job. Outcomes
  bound **one Invocation**.
- **Even the rubric obeys "your app owns the state."** The rubric travels
  inline in the spec as bounded markdown text — no file resource, no
  registered rubric library. A per-tenant rubric is a query in the host's
  database, exactly like instructions. (Managed Agents needs a Files API
  round-trip for rubric reuse; we don't.)
- **The existing guardrails already contain it.** Grader usage accrues to
  the Invocation's usage aggregate, so `max_estimated_cost_usd` bounds a
  runaway grade-revise loop with no new machinery. Each evaluation is a
  fenced, checkpointed boundary like a tool call, so crash recovery is the
  recovery model we already have.

Design sketch for the PRD:

```yaml
spec.outcome:
  description: Build a triage summary for this ticket        # what
  rubric: |                                                  # how it's judged
    ## Coverage
    - Names the affected charge IDs and dates
    - States refund eligibility with the policy clause cited
  max_evaluations: 3        # default 3, installation-capped
  grader:                   # optional; defaults to spec.model
    model: { provider: anthropic, id: claude-sonnet-5 }
```

Contract decisions worth fixing now:

- **What gets graded is what nvoken can see**: the turn's declared
  deliverable — `structured_output` when `spec.output` is present, else the
  assistant text — with the transcript as context. Host-side artifacts stay
  host-verified (a host tool returning the artifact's content is the
  bridge). Schema says *shape*; rubric says *quality* — the two compose.
- **An unmet rubric is not an infrastructure failure.** The Invocation
  settles `completed` with a durable `outcome_result`
  (`satisfied | unsatisfied | rubric_inapplicable`, per-evaluation trace,
  explanation) mirroring structured-output provenance; `failed` stays
  reserved for infra. Hosts branch on `outcome_result.status`.
- Grader verdicts are durable evidence rows excluded from future provider
  context (the failed-checkpoint precedent); the stream gains a durable
  `outcome.evaluation` frame; outcome fields are material in the
  post-reset fingerprint v1.

**Action: draft PRD 033 (outcome-graded Invocations).** Sequenced after MCP
— it is the second-largest capability in this proposal and the most
differentiating one: durable, recoverable, budget-bounded outcome loops on
a runtime the host fully controls is a combination none of the closed
competitors offer.

### 2.11 Server-side compaction: Sessions that never run out of context

**Decision: committed capability, not roadmap.** A conversation-owning
runtime that lets long Sessions die at the model's context window has an
expiration date built into its core product. OpenAI now ships compaction
two ways (`context_management` with `compact_threshold` on create, plus a
standalone `/responses/compact` endpoint); Mobius already runs server-side
compaction in production, and — exactly as PRD 029 ports the proven Mobius
MCP seam — the compaction PRD ports that proven implementation rather than
designing from a blank page.

nvoken's shape is *structurally better* than what OpenAI can offer, and the
PRD should lean into why: OpenAI's two modes carry opposite client
obligations (input-array chaining says you *may* prune items before the
latest compaction item; the standalone endpoint says *never* prune its
output; `previous_response_id` chaining says don't prune manually at all) —
window bookkeeping pushed to every caller, with three rulebooks. nvoken
owns the transcript, so compaction can be **invisible**:

- **The canonical transcript never mutates** (the single-representation
  law). Compaction produces a durable *projection artifact* — a summary
  plus a retained-tail cut point — recorded as evidence like any
  checkpoint. Only the provider-context projection composed at generation
  time uses it; hosts reading the transcript still see everything.
- **Recompaction supersedes**: later artifacts replace earlier ones in the
  projection; all remain readable evidence.
- **Spec-carried policy**, consistent with everything else:
  `spec.context: { compaction: { trigger_tokens | "auto", model? } }` —
  per-tenant compaction policy is a query, like instructions and rubrics.
  Like sampling (2.1) and outcome fields (2.10), `spec.context` is
  material in the post-reset fingerprint v1.
- **Interlock with 2.1**: what a compaction artifact does with provider
  reasoning items (carry encrypted, or summarize provider-neutrally — the
  portability trade) is a named open decision for the PRD; Mobius's answer
  is the starting point.
- **Near-term honesty step, ahead of the PRD**: define what happens
  *today* — a typed `context_window_exceeded` failure with token counts in
  `details`, documented next to Sessions, instead of a raw
  `provider_error`. No Session should hit an undefined edge while the real
  fix is being built.

**Action: draft PRD 034 (server-side compaction), porting the Mobius
implementation.** The host-visible pitch writes itself: *Sessions never run
out of context, and you never manage a window.*

### 2.12 Session seeding and forking

Two product needs share one missing primitive. **Edit-and-regenerate**: a
chat UI where the user edits an earlier message needs to branch from turn
N — `previous_response_id` gives Responses callers forking for free, and
nvoken's strictly linear Sessions have no answer. **Migration import**:
every prospect arrives with existing conversations in their database, and
because Invocation-creates-Session is the *only* write path, that history
cannot enter nvoken at all today — an adoption blocker hiding in the
contract.

Sketch: an admission-time option, not a new endpoint —

```jsonc
POST /v1/invocations
{
  ...,
  "fork_from": { "session_id": "sesn_…", "through_sequence": 12 }
}
```

creates the new Session by copying the canonical prefix in the admission
transaction, with fork provenance recorded on the Session; a sibling
seeded-creation form accepts imported messages (marked with an `imported`
origin so provenance never lies). Both respect tenant scoping and the
idempotency fingerprint. The one-turn-at-a-time law is preserved per
Session — a fork is a *new* Session, and this deliberately does not become
a generic Session append endpoint (Part 6's rejection stands: seeding
happens once, at creation, in one transaction).

Division of labor with 2.5: `if_active: supersede` regenerates the turn
*in flight* on the same Session; `fork_from` branches a new Session from
an earlier point in a settled transcript. A chat UI's "regenerate" button
is the former; its "edit an earlier message" flow is the latter.

One question is named for the PRD rather than answered here: whether
seeded creation stays coupled to admitting a turn. `fork_from` rides
`POST /v1/invocations`, but a migration importing ten thousand dormant
conversations must not require ten thousand billed model turns — the
seeded-creation form likely needs a generation-free admission shape, and
that is PRD 035's hardest decision.

**Action: draft PRD 035 (session seeding and forking).** The
migration-import half may deserve pulling forward if a design partner shows
up with history to import.

---

## Part 3 — SDKs: one design, four languages

A fresh audit of all four SDKs (2026-07-24) confirms evaluation F2 and
sharpens it: TypeScript is a full generation ahead (`agent()`, auto-dispatch,
`AgentSession`, typed structured output, pagination generators, env
defaults); Go is contract-complete but facade-less; Python has the
friendliest core ergonomics but the largest operation-coverage holes; Rust is
the thinnest and its handle design fights the borrow checker. Same audit also
surfaced real bugs (3.4). The plan: fix the reference, write the design spec
down, port it, and hold it with conformance fixtures.

### 3.1 The SDK design spec: one vocabulary, per-language idiom

Cross-language parity failures today are not casing (each language is
internally idiomatic — good) but *vocabulary and shape*. Decisions, applied
everywhere:

| Concept | Decision | Fixes |
| --- | --- | --- |
| Facade text read vs. text run | `Agent.text(input)` **runs** a turn; the handle's read-only accessor is renamed **`outputText()`** (`output_text` / `OutputText` / `output_text()`), matching the wire field exactly | TS currently has `text()` meaning two different things on two types; Go/Py/Rust have only the read form under the run form's name |
| Invocation-scoped vs Session-scoped message reads | Handle keeps `listMessages()`; the *Session*-scoped client method becomes `listSessionMessages(sessionId, …)` | today `client.ListMessages(sessionID)` and `handle.ListMessages()` collide in name while reading different resources — identical trap in all four SDKs |
| Transcript read | One name everywhere: `getTranscriptPage` + `drainTranscript` (Go: `TranscriptPage`/`DrainTranscript`) | Go says `GetTranscript`, Python/Rust have nothing |
| Callback tool shape | One shape: tool struct with nested `callback: {url}` object (mirrors the wire) | four SDKs currently have four shapes (nested object / pointer / flattened `callback_url` string / enum variant) |
| Tool `mode` typing | Closed type in every language (union, enum, `Literal`) — never a bare string | Go accepts any string and fails server-side |
| Wait options | `until` + poll bounds + cancellation everywhere; Rust *adds* poll bounds and `until`, keeps its overall-`timeout` as an extra (it's a good idea — adopt it in the others as `timeout`/`timeoutMs`) | Rust today has *only* timeout with hardcoded poll cadence; TS/Go/Py have only poll bounds with no overall deadline |
| Local cancellation | Its own error category `cancelled` in every SDK; never `timeout` | TS, Go (`context.Canceled` → `ErrorTimeout`), and Python (swallows `CancelledError` — see 3.4) all mislabel today |
| Pagination | Auto-paging iterators for all three collections in TS/Python/Go (`*Pages` / `*_pages` / iterator func); Rust later | TS has 3, Python has 1 (named differently: `invocation_items`), Go/Rust 0 |
| `raw()` escape hatch | Same name, and per-language the *most useful* shape; document its stability contract ("generated, may shift with regeneration") | today: 3-API object (TS), whole client (Go), positional tuple (Py), bare `Configuration` (Rust) |
| Error type name | `NvokenError` in TS/Py/Rust; Go stays `nvoken.Error` (idiomatic) — but field names align (`Category`, `Code`, `RequestID`, `RetryAfter`, `Details`) | drift is otherwise inevitable |
| Per-turn provider credentials | Exposed in every facade `InvokeRequest` | wire field exists in all generated layers; reachable from TS only |
| Stream delta accumulation | Every SDK's `Reducer` also folds `output_text.delta`/`thinking.delta` previews into a live text snapshot keyed by (invocation, attempt, iteration, content_index), discards on `stream.resync` or attempt increase, and swaps in the canonical message when it arrives (the Managed Agents accumulator pattern) | today the Reducer handles `transcript.update` only and every consumer hand-rolls delta accumulation — the showcase's 20-line stream dance is the symptom |

This table becomes a checked-in document (`docs/codebase/sdk-design.md`) that
PRs adding SDK surface must update — the cross-language analog of
`operations.json`.

### 3.2 TypeScript: polish the reference before porting it

The reference must be worth copying four times. Changes beyond the renames in
1.3:

1. **`run()`/`text()` ride the SSE admission path** (evaluation F9). The
   contract already supports `POST` + `Accept: text/event-stream`; the
   ergonomic path should get the completion *push*, falling back to polling
   on stream failure. Removes up to ~2s of tail latency from the
   first-impression call, and the auto-dispatch loop already exists in
   `stream()` form.
2. **Missing tool handler must not pin the Session.** Today
   `MissingToolHandlerError` throws client-side and leaves the Invocation
   parked in `waiting` until the 1800s timeout — a typo in a tool name costs
   a 30-minute stuck Session. New behavior: before throwing, the facade
   cancels the Invocation (opt-out flag for hosts that will attach a handler
   from another process). The error message says which of the two happened.
3. **`text()` empty-output semantics** (evaluation F11): keep the throw,
   fix the error. A caller of `text()` asked for text; `null` just moves the
   crash. But the common legitimate case is a structured-output or pure-tool
   turn, so throw a dedicated `NoOutputTextError` whose message says what the
   turn *did* produce ("completed with structured output; call run() and
   read structuredOutput"). This diverges from the evaluation's
   return-null recommendation deliberately: `run().text` is already the
   nullable form — the ladder should offer both, not two nullables.
4. **Export the nouns.** The invoke-showcase example has to write
   `type Invocation = Awaited<ReturnType<Client["getInvocation"]>>` because
   the core resource types aren't (discoverably) exported from the package
   root. Export `Invocation`, `InvocationResult`, `Session`,
   `SessionMessage`, `PendingHostToolCall`, `Model*` types from `index.ts`.
5. **Tighten handle types after terminal reads.** Example code is littered
   with `handle.sessionId!`. `waitForResult()`/`run()` should return types
   whose `sessionId`/`agentId` are non-optional (they are always known by
   then) — e.g. a `SettledInvocation` view — so consumer code drops the
   `!`s.
6. **`stream({ timeoutMs })`.** The showcase needs ~20 lines of
   AbortController choreography to consume one stream safely with a
   timeout. One option kills the boilerplate.
7. Housekeeping from 1.3: `runImmediately` and `replaySafe` go private,
   `deduplicated: boolean`, category `cancelled`.

### 3.3 Parity: Python first, then Go; Rust stays honest

The audit confirms the port is **composition over existing primitives**:
`waitForAction`, `submitToolResults`, `waitForResult`, and typed
`PendingHostToolCall` already exist in Go and Python. The only genuinely new
plumbing anywhere is the create-and-stream admission path (Go/Python
currently stream only existing Invocations). Structural additions are one
`handler` slot on each language's `Tool` type.

- **Python (~250–400 lines + tests), first.** Smallest port (asyncio makes
  dispatch and `AgentSession` serialization an `asyncio.Lock` and ~30
  lines), and the language where the audience gap hurts most. Bundle the
  prerequisite fixes from 3.4 (cancellation semantics, `stream_session`
  repair, stop calling private `_*_serialize` generated methods) and close
  the operation-coverage holes (transcript reads, session streaming,
  provider-credential methods — 6 of 19 operations are unreachable from the
  Python facade today).
- **Go (~400–600 lines + tests), second.** `AgentSession` collapses to a
  `sync.Mutex` held until terminal. Structured output exposes
  `json.RawMessage` + a `DecodeStructuredOutput[T]` helper (method-level
  generics can't mirror TS end-to-end typing). Also: stop leaking
  `*generated.*` list types from facade signatures, and validate tool mode
  at the type level.
- **Rust: document the level, fix the frame.** Until someone funds the full
  facade, the README says plainly: transport + durable handle, no Agent
  facade yet. Before that ships, fix what makes the current crate
  frustrating even at its level: interior-mutability (or
  snapshot-returning) handle so `&mut self` stops forbidding
  stream-plus-act, builders (or `Default`) for `InvokeRequest`/
  `ExecutionSpec`, poll-interval knobs, and typed callback errors instead of
  `Result<_, String>`.

And per Part 5: until Python/Go land, the README and
`docs/guides/sdks-and-cli.md` scope the claim — "generated typed transport +
durable handles in four languages; the high-level Agent facade is TypeScript
today, Python next."

### 3.4 Bugs found in the audit (fix now, independent of everything above)

| SDK | Bug | Consequence |
| --- | --- | --- |
| Python | `_replay_safe`, `wait`, `wait_for_action` catch `asyncio.CancelledError` and re-raise as `NvokenError("timeout")` | Breaks structured concurrency: cancelled tasks appear to fail with a retryable-looking SDK error instead of propagating cancellation |
| Rust | `Reducer::apply` unconditionally overwrites the resume cursor, including with an empty string | A resumed Session stream can reconnect from an empty `Last-Event-ID` and replay or miss events |
| Rust | `ResponseMetadataObserver` map only evicts on matched errors | Slow memory leak, one entry per errored `x-request-id`, for the client's lifetime |
| Rust | `wait_for_result` fabricates an HTTP 409 (`status: Some(409)`) for a locally-detected non-completed terminal | Callers see a wire status for an error that never came off the wire; Go/Py report category-only for the same case |
| Go/Py/Rust | `provider_credentials` on create is unreachable from the facade `InvokeRequest` (wire + generated layers have it) | Per-turn credential selection — a headline feature — is TS-only |
| Python | `stream_session` takes an `InvocationHandle` where every other SDK keys by `session_id`, ends when *that Invocation* terminates, is unexported, and calls private generated serializers | Category error (Sessions outlive Invocations) plus brittleness against regeneration |
| Go | `context.Canceled` categorized as `ErrorTimeout` | Same mislabel as TS/Python; fixed by the `cancelled` category (3.1) |

### 3.5 Conformance: how four SDKs stay one design

`sdk/conformance/` today pins transport shape. Extend it with
*behavior-level* fixtures the facades must pass identically: error-category
mapping (including `cancelled`), reducer cursor rules (the Rust bug above is
exactly what these prevent), delta-accumulation snapshots (same scripted
frame sequence in, same snapshot out, in all four languages), wait-until
semantics, `output_text` join rule, idempotency-key auto-generation shape,
and the auto-dispatch loop against a
scripted mock server (park → submit → resume → settle). This is the
mechanism that makes "four SDKs" a fact rather than a claim that decays.

---

## Part 4 — The developer journey

The 2026-07-24 journey audit found the onboarding path genuinely excellent —
brew → `nvokend quickstart` → npx, two commands and one npx from zero to a
model answer, with every first- and second-pass field-report finding
verifiably closed. The remaining work is three specific debts.

### 4.1 The CLI is one DX generation behind the SDK

The `nvoken` CLI correctly stays a thin layer over the Go SDK, but it
reproduces traps the SDK already fixed and cannot render the product's own
output:

1. **No one-shot answer.** `nvoken invoke` prints IDs and exits; getting the
   text takes three commands. Add `--wait` and `--text` (admit → follow →
   print `output_text`), making the CLI demo self-sufficient:
   `nvoken invoke --agent support --text "why was I charged twice?"`.
2. **Streams don't stream.** `invocation stream` / `session stream` print
   `event.Type\tevent.ID` per frame — you cannot watch an answer generate.
   Text mode renders `output_text.delta` as it arrives (and a `--events` flag
   keeps the frame view).
3. **The terminal-only-wait trap, again.** `invocation wait` has no
   `--until actionable`, recreating exactly the host-tool trap the
   2026-07-22 field report called the most likely onboarding failure. Add
   the flag; make `wait` print a pointed hint when it returns a `waiting`
   Invocation.
4. **Recovery filters missing.** `session list` lacks `--tenant`,
   `--session-key`, `--default-tenant` — exact host-key recovery from the
   shell is impossible even though the API and Go SDK support it.
5. **Transcripts unreadable in text mode.** `session messages` prints
   `sequence role message-id` with no content; reading a conversation
   requires `--json` + jq. Text mode prints role-prefixed text blocks.
6. **`invoke` can't express the spec.** No limits, tools, or
   `--output-schema` flags, so `tool-result submit` can only ever service
   Invocations admitted by an SDK app. Add `--spec-file spec.json` (one flag, full
   spec fidelity, no flag-explosion).

### 4.2 Document contracts at the point of use

The audit's sharpest docs finding: each load-bearing contract behavior is
documented in exactly one place, and rarely where the developer is standing
when they hit it. Adopt a rule — **every contract rule appears wherever the
reader first collides with it** — and apply it:

- **One turn at a time per Session**: currently in the TS SDK README only.
  Belongs next to *every* chat example, the CLI `invoke` docs, and the
  `session_invocation_active` error's own message (link to docs in the error
  `details`).
- **The identity tuple** (`Account → agent_key → tenant_key → session_key`,
  spec travels per-Invocation): the single most important mental model, currently
  living only inside a research report. Diagram goes in the README's request
  example, every SDK README, and the top of `docs/design/api.md` (per 1.2).
- **`waiting_timeout` / stuck sessions**: an unanswered host tool pins the
  Session for 1800s by default. Belongs in the host-tools guide next to the
  first `waiting` example, with the `if_active: supersede` escape hatch
  (2.5) cross-referenced.
- **Idempotency conflicts**: the CLI `--idempotency-key` help and the guides
  say "stable retry identity" but never "same key + changed body ⇒
  `409 idempotency_conflict`" — the half of the contract that surprises
  people.
- **Which layer to imitate**: the SDK README teaches `agent()` while the
  showcase example uses only low-level `client.invoke()` and claims that as
  "the supported facade." Resolution: a third example —
  `examples/typescript-agent-tools` — showing `agent()` + host-tool
  auto-dispatch + `session()`, and one sentence in each example README
  saying which rung of the ladder it demonstrates and why.
- **Stale guide text**: `docs/guides/sdks-and-cli.md` still says device
  login doesn't exist (it does, and is documented elsewhere) and still
  makes the four-SDK ergonomics claim (Part 3.3 rescopes it).
- **Write stream contracts the way Managed Agents does.** Two documentation
  forms worth adopting for our streaming docs: an explicit "guarantees the
  pattern relies on" block (ours would state: durable frames are the only
  cursor carriers; delta concatenation per (attempt, iteration,
  content_index) yields a prefix of the canonical text; `stream.resync`
  invalidates every buffered preview) and a troubleshooting table ("you
  see X → it means Y"). Our streaming semantics are as carefully designed
  as theirs and less explicitly guaranteed in prose.
- **A "coming from the provider APIs" migration guide.** OpenAI's Chat
  Completions → Responses guide is the template: a concept-mapping table
  plus a common-errors checklist. Ours maps Chat Completions / Responses /
  Anthropic Messages concepts to nvoken (`messages[]` → durable Session;
  `previous_response_id` → `session_key`; `response_format`/`text.format` →
  `spec.output.schema`; reasoning-item replay → automatic; the agent loop →
  one Invocation) with the errors newcomers actually make. Every prospect
  arrives from one of those APIs; this is the doc that meets them there.

### 4.3 Kill the last onboarding unknowns

- **Model access is guess-and-pray.** `--model '<model-you-can-access>'`
  requires knowledge the tool has and the user doesn't; a wrong guess
  surfaces as a failed (possibly billed) first Invocation. Add
  `nvoken model check <provider>/<id>`: admits a one-iteration,
  minimal-token probe Invocation against the configured credential and reports
  pass/fail with the provider's error verbatim. Costs a fraction of a cent,
  runs in seconds, converts the number-one remaining friction into a
  command. (`model list` stays discovery-only, as designed.)
- **Ambient provider keys**: quickstart warns you to `unset` conflicting
  keys manually. `nvokend quickstart` should print which provider keys it
  *sees* and which one it will use at startup — observability instead of a
  footnote.
- Keep the `.env` marker-file mechanism as-is; it earned its keep in the
  audits.

---

## Part 5 — Positioning: say what it is

Adopting the evaluation's F1 recommendation (a), with sharper language:

- **Headline:** "harness-as-a-service and AI gateway" → **"the durable agent
  runtime for multi-tenant apps."** Sub-line: "Your app sends the agent spec
  and input; nvoken runs the turn durably — streaming, checkpoints, tool
  parking, recovery — and your app executes the tools."
- **Own the tools stance as a feature, not a gap.** New README section,
  three sentences: nvoken runs no tools of its own *because your tools are
  your product* — they live in your backend with your data and your
  credentials; nvoken parks the turn durably while your code runs and resumes
  it on any engine. Remote MCP support (2.8) is the sanctioned breadth path
  and is in flight now; until it ships, the README claims host and callback
  tools only, then upgrades to "bring your tools as host tools, callback
  tools, or any remote MCP server." An evaluator told this upfront reads the
  tool model as a philosophy; discovering it after "harness" reads as a
  bait-and-switch.
- **Add a "Built-in tools" column to the comparison table** and take the ✗
  ourselves until 2.8 ships (the honest cell then becomes "via MCP", and
  "`fetch` + any remote MCP server" once 2.9's built-in lands). A
  comparison table that flatters us on chosen axes and hides the axis
  competitors lead on costs more credibility than the ✗ does.
- **Fix "stateless."** The README's "nvoken stays out of your way by being
  stateless" is the single most misleading sentence in the repo — the runtime
  is *proudly* stateful about conversations. Replace with the sentence the
  evaluation already wrote: "**stateless about definitions, stateful about
  conversations**" — no registration, no migration, specs travel per turn;
  Sessions and running Invocations are durable.
- **Scope the four-SDK claim** to match Part 3's actual delivery ladder until
  parity lands.
- **Use competitor field evidence, specifically.** A 2026-07-24 review of the
  Managed Agents Sessions API yielded two concrete proof points worth citing
  in the "your app owns the state" story rather than asserting abstractly:
  their `agent` parameter needs three forms (plain ID, pinned-version
  object, overrides object) and an override-rules table with three
  interlocking exceptions — complexity that exists *only because*
  definitions are registered, and that nvoken's inline spec dissolves
  entirely; and their stream reconnect procedure is "reopen, list full
  history, dedupe by event ID client-side" — no cursor, no replay — where
  nvoken's durable SSE cursors resume exactly. A same-day review of the
  OpenAI Responses API adds three more: OpenAI maintains *three*
  overlapping conversation-state mechanisms (manual item replay,
  `previous_response_id`, the Conversations API), each with its own rules;
  `previous_response_id` silently drops top-level `instructions` — a
  documented footgun that nvoken's per-Invocation spec makes impossible by
  construction; and the Assistants API — the industry's flagship
  registered-agents-and-threads product — is deprecated with an August
  2026 sunset, replaced by flexible primitives. Claims with named evidence
  age better than adjectives.

---

## Part 6 — What we deliberately do not do

Discipline is part of the proposal. Explicitly rejected, with reasons, so
they stop resurfacing:

- **No open-ended built-in tool growth.** Every candidate passes the 2.9
  four-condition admission test; `fetch` is the only tool that passes today.
  Code execution is explicitly rejected for now — sandbox infrastructure is
  a different product, and host sandboxes remain host tools — recorded in
  the decision log so it isn't relitigated monthly.
- **No generic Session append endpoint.** The transcript stays
  single-writer-per-turn; steering arrives as a narrow command with its own
  PRD, not as an open POST. Seeding at creation (2.12) is the once-only
  sanctioned exception — it writes history inside the admission
  transaction, before the Session lives; there is still no append to a
  live Session.
- **No retry/resume endpoint for terminal Invocations.** Terminal is
  terminal; the host admits a new Invocation. This asymmetry is what makes
  the state machine
  auditable.
- **No agent-definition storage, ever** — including "just optional defaults."
  The moment a spec fragment lives server-side, every competitor's migration
  problem becomes ours and the comparison table's only ✅ column dies.
- **No managed credential vault / OAuth broker.** Managed Agents' vaults
  (stored OAuth credentials with Anthropic-managed token refresh) are what
  "stored MCP credentials" grow into. PRD 029's stance stands: the host
  obtains and refreshes tokens, nvoken binds them per-Invocation and
  destroys them at settlement. Revisit only if per-turn tokens prove
  insufficient in practice — and then as its own PRD, not a quiet field.
- **No multiagent session threads.** Sub-agent orchestration is host
  composition: a host tool or MCP call can invoke another Agent's Session.
  A first-class thread model is a product decision for a later contract,
  not an add-on field.
- **No SDK-invented behavior.** The facade may sequence contract calls
  (auto-dispatch, serialization) but never invent semantics the wire doesn't
  have — the TS `AgentSession` local queue is the outer limit of acceptable.

---

## Part 7 — Sequenced plan

Ordering principle: **breaking changes first** (they get more expensive by
the week), **honesty second** (cheap, protects every evaluator interaction),
**capability third** (each lands as an additive minor).

**Wave 0 — bug fixes (now, independent of every decision below)**
The 3.4 table: Python cancellation swallowing, Rust reducer-cursor clobber
and metadata leak, fabricated 409, per-turn credentials unreachable outside
TS, `stream_session` repair. These are wrong today regardless of what we
decide about anything else.

**Wave 1 — the freeze-worthy core (do immediately)**
1. Vocabulary rule (1.1): the "An Invocation is one durable agent turn"
   definition lands in README, `api.md`, and every SDK README; sweep all
   docs for capitalized/identifier uses of "turn"; record the
   Invocation-vs-Turn decision in `docs/design/decisions.md`.
2. Naming/consistency audit items (1.3 + 3.1): error categories incl.
   `cancelled`, provider-type unification, credential-list pagination,
   `output_text` join contract, `outputText()`/`listSessionMessages`
   renames.
3. Fingerprint lineage reset to v1 (1.4).
4. README/positioning rewrite (Part 5) — same PR as the vocabulary sweep so
   the story and the wire agree — including rescoping the four-SDK claim
   and the two stale statements in `sdks-and-cli.md` (4.2).

**Track M — remote MCP tools (starts the moment Wave 1 merges)**
PRD 029 implementation (2.8) runs as its own track in parallel with Waves
2–3: it is the longest pole and the highest-demand capability, and nothing
in those waves blocks it. Its fingerprint additions land in the single
post-reset v1 (1.4), which is why Wave 1 precedes it. The track is done
when the SDK surface (`mcpServers` + `listMcpTools`), the CLI probe
(`nvoken mcp list-tools`), and the R9-led docs ship *with* it — an MCP
feature that requires hand-built JSON is half-shipped.

**Wave 2 — contract completeness (additive)**
5. `spec.sampling` + `spec.reasoning` with fail-closed normalization and
   reasoning round-trip fixtures (2.1).
6. Agent read surface + `agent_key` list filters (1.2).
7. Small contract additions (2.5, 2.7, 2.11): `if_active: supersede`,
   repeated `status` filter, `deltas=` stream-preview opt-in, cumulative
   Session usage, typed `context_window_exceeded`.
8. SDK pre-flight schema validator, shared fixtures (2.4a).

**Wave 3 — breadth (additive)**
9. `fetch` built-in tool (2.9) — small, and its guarded egress client is
   shared with the MCP track.
10. **Outcome-graded Invocations (2.10)** — PRD 033 first; the largest
    capability after MCP and the most differentiating. PRD drafting can
    start during Wave 2; implementation follows MCP so both share the
    post-reset fingerprint v1 vocabulary.
11. **Server-side compaction (2.11)** — PRD 034 first, porting the Mobius
    implementation; the typed overflow error already shipped in Wave 2, so
    no Session hits an undefined edge while this is built.
12. Session seeding and forking (2.12) — PRD 035; pull the import half
    forward if a design partner arrives with history to migrate.
13. Multimodal input blocks + modality-validated admission (2.2).
14. `notify` settled-delivery (2.6).
15. `openrouter`/`ollama` provider exposure (2.3).
16. Server-side `$ref` inlining / `anyOf` lowering (2.4b).

**Wave 4 — parity & journey**
17. TS reference polish (3.2): SSE-backed `run()`/`text()`,
    missing-handler auto-cancel, `NoOutputTextError`, exported nouns,
    settled-view types, `stream({timeoutMs})`, and the delta accumulator
    in the Reducer (3.1).
18. Python `Agent` facade + coverage holes, then Go (3.3), each including
    the delta accumulator; Rust ergonomics floor (interior-mutability
    handle, builders) with honestly documented level.
19. Behavior-level conformance fixtures (3.5) — lands *with* the first
    port, not after it.
20. CLI generation catch-up (4.1): `invoke --wait/--text`, delta-rendering
    streams, `--until actionable`, session filters, readable transcripts,
    `--spec-file`.
21. Point-of-use documentation rule + `model check` + agent-facade example
    + stream-contract guarantees blocks + provider-API migration guide
    (4.2, 4.3).

Wave 1 is a few days of focused work and is the only wave with a deadline
logic: it must precede any public push, announcement, or external
evaluation, because it is the wave we can never run again.

---

## Appendix A — `output_text` fixture (1.3)

```jsonc
// Two assistant messages, the first with two text blocks.
messages: [
  { role: "assistant", content: [ {type:"text", text:"The charge was"},
                                  {type:"text", text:" duplicated."} ] },
  { role: "assistant", content: [ {type:"text", text:"A refund is queued."} ] }
]
// output_text — blocks concatenate directly, messages join with \n\n:
"The charge was duplicated.\n\nA refund is queued."
```

## Appendix B — sampling fail-closed error fixture (2.1)

```jsonc
400 invalid_request
{
  "code": "invalid_request",
  "message": "spec.reasoning.effort is not supported for openai/gpt-5.4.",
  "request_id": "req_…",
  "details": {
    "field": "spec.reasoning.effort",
    "provider": "openai",
    "model": "gpt-5.4",
    "supported": ["none", "minimal", "low", "medium", "high"]
  }
}
```

---

## Appendix C — execution program and acceptance gates

This appendix turns Part 7 into an executable delivery program. Where its
ordering differs from Part 7, this appendix governs sequencing; it does not
approve a contract change that still requires a PRD or decision-log entry.
Work IDs (`EX-*`) and acceptance IDs (`AC-*`) are stable tracking identifiers.
A phase is complete only when every acceptance criterion for the work selected
into that phase's release and its phase gate are checked. Phase 5 items remain
independent backlog until selected; selecting one does not silently commit the
others.

### Program rules

1. Contract, schema, durability, and public compatibility changes receive a
   focused PRD before implementation. The next new sequence is 033; later
   proposal PRD numbers move forward without renumbering existing PRDs.
2. OpenAPI, server behavior, generated transports, handwritten facades,
   behavior fixtures, CLI behavior, and documentation move together whenever
   a public contract changes.
3. New admissions retain the existing fingerprint lineage and use v8 for the
   next material shape. Resetting to a new v1 requires a separate approved
   compatibility and retained-data migration decision; absence of known
   external users is not sufficient proof after public releases.
4. SDK behavior-level conformance begins before parity work and grows with
   every language port. It is not a cleanup phase after the ports.
5. A capability is not complete when only raw JSON can reach it. Its intended
   SDK, CLI, examples, documentation, failure behavior, and recovery proof ship
   in the same release unless its PRD explicitly establishes a narrower
   supported surface.
6. Phase 2A (SDK and CLI foundation) and Phase 2B (remote MCP) may run in
   parallel after Phase 1. Other phases are ordered by dependency, but
   independent PRDs within a phase may overlap after their governing contracts
   are settled.

### Phase 0 — correctness and public truth

**Outcome:** known SDK defects are removed and every public claim describes
behavior that exists in the released repository.

| Work ID | Slice | Depends on |
| --- | --- | --- |
| `EX-0.1` | Repair Python cancellation and Session-stream behavior. | None |
| `EX-0.2` | Repair Rust reducer, response-metadata, and local-error behavior. | None |
| `EX-0.3` | Correct Go cancellation and expose per-turn provider credentials through the Go, Python, and Rust facades. | None |
| `EX-0.4` | Reconcile README, guides, roadmap, and changelog claims with the implemented surface, especially remote MCP status. | None |
| `EX-0.5` | Add cross-language regression fixtures for the corrected behavior. | `EX-0.1`–`EX-0.3` |

**Acceptance gate:**

- [x] **AC-0.1 (`EX-0.1`):** Cancelling Python `_replay_safe`, `wait`, or
  `wait_for_action` propagates `asyncio.CancelledError`; it is never converted
  to an SDK timeout, and a conformance test proves the behavior.
- [x] **AC-0.2 (`EX-0.1`):** Python Session streaming accepts a Session ID,
  follows the Session beyond one Invocation, uses a supported public generated
  operation seam, and retains only durable non-empty resume cursors.
- [x] **AC-0.3 (`EX-0.2`):** Rust reducer fixtures prove an empty or ephemeral
  event ID cannot overwrite the last durable cursor, including after reconnect.
- [x] **AC-0.4 (`EX-0.2`):** Rust response metadata is removed after both
  matched success and error handling, and a bounded repeated-error test shows
  the observer does not grow one retained entry per request.
- [x] **AC-0.5 (`EX-0.2`):** A locally detected Rust terminal-state error has no
  fabricated HTTP status; callers can distinguish it from a wire `409`.
- [x] **AC-0.6 (`EX-0.3`):** Go `context.Canceled` maps to `cancelled`, while an
  actual deadline maps to `timeout`, with tests covering both.
- [x] **AC-0.7 (`EX-0.3`):** Equivalent Go, Python, Rust, and TypeScript facade
  admissions can select caller-ephemeral or stored per-turn provider
  credentials without using a generated transport escape hatch.
- [x] **AC-0.8 (`EX-0.4`):** No README, guide, release note, or comparison table
  says nvoken executes remote MCP tools until Phase 2B is complete; historical
  release notes state what those releases actually contained.
- [x] **AC-0.9 (`EX-0.5`):** `make check` and `make sdk-check` pass with the new
  cancellation, cursor, metadata, local-error, and credential fixtures.

**Completion evidence (2026-07-24):** `make check` and `make sdk-check` pass.
The shared conformance server asserts credential-bearing admissions in every
SDK; `sdk/conformance/fixtures/reducer.json` covers empty and ephemeral cursor
IDs; focused Python, Go, and Rust tests cover cancellation, Session continuity,
metadata retention, and locally created terminal errors. README, guides, SDK
READMEs, roadmap, product-direction labeling, and historical release notes were
reconciled against the implemented surface.

### Phase 1 — pre-1.0 contract stabilization

**Outcome:** the launch vocabulary, compatibility policy, generated contract,
SDK names, and positioning form one deliberate public surface before additive
capability work expands it.

| Work ID | Slice | Depends on |
| --- | --- | --- |
| `EX-1.1` | Write PRD 033 for pre-1.0 Runtime and SDK contract stabilization. | Phase 0 |
| `EX-1.2` | Record the Invocation/turn vocabulary and fingerprint compatibility decisions. | `EX-1.1` |
| `EX-1.3` | Land the coordinated OpenAPI and server contract revision. | `EX-1.1`, `EX-1.2` |
| `EX-1.4` | Regenerate transports and update all handwritten SDK facades and fixtures. | `EX-1.3` |
| `EX-1.5` | Update positioning, SDK claims, examples, and migration guidance. | `EX-1.3`, `EX-1.4` |

**Acceptance gate:**

- [x] **AC-1.1 (`EX-1.1`):** PRD 033 states one stabilization outcome, maps
  every binding requirement to an observable acceptance proof, receives the
  required independent review once, and has no unresolved blocking finding.
- [ ] **AC-1.2 (`EX-1.2`):** `docs/design/decisions.md` defines “An Invocation
  is one durable agent turn,” the conceptual/resource naming rule, and the
  reason `Invocation` remains the public noun.
- [ ] **AC-1.3 (`EX-1.2`):** Existing v1–v7 durable rows remain replay
  comparable, unchanged admissions continue to use v7, and the next material
  admission shape uses v8 with fixtures proving legacy equality and v8
  conflict behavior. Any alternative reset has an approved retained-data
  migration and rollback proof before replacing this criterion.
- [ ] **AC-1.4 (`EX-1.3`):** The wire and server map `401` to
  `authentication`, `403` to `permission`, local cancellation to `cancelled`,
  and actual deadline expiry to `timeout`.
- [ ] **AC-1.5 (`EX-1.3`):** Provider credential listing uses the standard
  `{items, has_more, next_cursor}` envelope; a test with more than one page
  reaches every item exactly once.
- [ ] **AC-1.6 (`EX-1.3`):** Model provider identifiers have one
  additive-compatible public type, while admission still rejects a provider
  the installation cannot execute.
- [ ] **AC-1.7 (`EX-1.3`):** The Appendix A fixture passes end to end: text
  blocks within one assistant message concatenate directly and distinct
  assistant messages join with exactly `"\n\n"`.
- [ ] **AC-1.8 (`EX-1.4`):** All four SDKs expose the agreed
  `outputText`/`output_text`/`OutputText` and
  `listSessionMessages` equivalents; the displaced public names and leaked
  TypeScript retry/run-loop helpers are absent from their documented surfaces.
- [ ] **AC-1.9 (`EX-1.4`):** OpenAPI regeneration is clean, generated drift
  checks pass, and the same behavior fixtures pass in Go, TypeScript, Python,
  and Rust.
- [ ] **AC-1.10 (`EX-1.5`):** README, `api.md`, every SDK README, examples, and
  migration notes use the identity tuple and Invocation/turn rule
  consistently, scope the four-SDK claim honestly, and contain no stale
  identifier from this revision.

### Phase 2A — SDK and CLI foundation

**Outcome:** TypeScript is a stable reference implementation, the other SDKs
share its meanings at their documented level, and the CLI demonstrates the
same durable workflow without forcing raw API use.

| Work ID | Slice | Depends on |
| --- | --- | --- |
| `EX-2A.1` | Make the existing SDK/CLI architecture guide the checked public design and add behavior-level conformance fixtures. | Phase 1 |
| `EX-2A.2` | Polish the TypeScript reference facade. | `EX-2A.1` |
| `EX-2A.3` | Add the Python Agent facade and close facade operation gaps. | `EX-2A.1`, `EX-2A.2` |
| `EX-2A.4` | Add the Go Agent facade and close facade operation gaps. | `EX-2A.1`, `EX-2A.2` |
| `EX-2A.5` | Establish the documented Rust ergonomics floor. | `EX-2A.1` |
| `EX-2A.6` | Bring the Go CLI and point-of-use documentation to the same workflow level. | `EX-2A.2`, `EX-2A.4` |

**Acceptance gate:**

- [ ] **AC-2A.1 (`EX-2A.1`):** The checked SDK design names every shared
  concept once and specifies language-idiomatic mappings for waits,
  cancellation, pagination, callback tools, errors, raw access, provider
  credentials, and reducer previews.
- [ ] **AC-2A.2 (`EX-2A.1`):** Shared fixtures cover error mapping, durable
  cursor retention, delta accumulation/resync, wait-until behavior,
  `output_text`, and the park → submit → resume → settle dispatch loop.
- [ ] **AC-2A.3 (`EX-2A.2`):** TypeScript `run()` and `text()` use
  create-and-stream with authoritative-read fallback, and a forced stream
  disconnect still produces the same settled result.
- [ ] **AC-2A.4 (`EX-2A.2`):** A missing TypeScript tool handler cancels before
  throwing by default, supports the documented opt-out, and
  `NoOutputTextError` distinguishes structured-only or tool-only completion.
- [ ] **AC-2A.5 (`EX-2A.2`):** TypeScript exports the principal Runtime nouns,
  terminal result types no longer require non-null assertions for known IDs,
  `stream({timeoutMs})` is bounded, and reducer fixtures prove preview
  replacement and resync behavior.
- [ ] **AC-2A.6 (`EX-2A.3`):** Python exposes the five Agent verbs, bound
  Session serialization, transcript and Session-stream reads,
  provider-credential operations, host-tool dispatch, structured output, and
  the shared wait and reducer semantics.
- [ ] **AC-2A.7 (`EX-2A.4`):** Go exposes the five Agent verbs, bound Session
  serialization, typed tool modes, facade-owned list types, structured-output
  decoding, host-tool dispatch, and the shared wait and reducer semantics.
- [ ] **AC-2A.8 (`EX-2A.5`):** Rust handles allow stream-plus-act without an
  exclusive mutable borrow, core request types have builders or defaults,
  polling is configurable, callback errors are typed, and the README states
  the exact supported level.
- [ ] **AC-2A.9 (`EX-2A.6`):** The CLI can admit and print one answer, render
  text deltas, wait until actionable, recover Sessions by host keys, display
  readable transcript text, and accept a complete spec file; JSON output
  remains stable.
- [ ] **AC-2A.10 (`EX-2A.6`):** The Agent-facade example, stream guarantees and
  troubleshooting table, point-of-use concurrency/idempotency guidance,
  provider-API migration guide, and `model check` workflow pass their
  documented smoke paths.
- [ ] **AC-2A.11 (phase gate):** `make sdk-check` proves the documented common
  behavior in every language at its supported level, and no SDK README claims
  a higher-level facade that the package does not expose.

### Phase 2B — remote MCP flagship

**Outcome:** a host can attach a remote streamable-HTTP MCP server and nvoken
discovers and executes its tools through the durable recovery model, with a
usable SDK and CLI surface.

| Work ID | Slice | Depends on |
| --- | --- | --- |
| `EX-2B.1` | Reconcile and complete PRD 029. | Phase 1 |
| `EX-2B.2` | Build declaration, admission, encrypted credentials, discovery snapshot, and stateless discovery. | `EX-2B.1` |
| `EX-2B.3` | Build fenced MCP execution, settlement, and crash recovery. | `EX-2B.2` |
| `EX-2B.4` | Ship all SDK helpers, CLI probe, examples, and R9-led documentation. | `EX-2B.2`, `EX-2B.3`, Phase 2A design |

**Acceptance gate:**

- [ ] **AC-2B.1 (`EX-2B.1`):** PRD 029 uses the approved fingerprint policy,
  names the actual PRD 015 dependency, includes or explicitly companions the
  required SDK/CLI surface, receives its independent review once, and has no
  unresolved blocking finding.
- [ ] **AC-2B.2 (`EX-2B.2`):** Strict admission, secret exclusion,
  encrypted-binding cleanup, stable discovery projection, allowlist handling,
  and stateless discovery pass PRD 029’s normal and failure-path acceptance
  fixtures.
- [ ] **AC-2B.3 (`EX-2B.3`):** ToolCall evidence and a checkpoint exist before
  every egress attempt; cancellation, deadlines, lease loss, duplicate
  delivery, stale owners, and unknown non-idempotent outcomes settle exactly
  as PRD 029 specifies in embedded and external execution modes.
- [ ] **AC-2B.4 (`EX-2B.3`):** Logs and every read, stream, error, and
  transcript surface remain credential-free under success, protocol error,
  oversized result, guarded-egress rejection, and crash recovery.
- [ ] **AC-2B.5 (`EX-2B.4`):** Every SDK can declare MCP servers and invoke
  stateless tool discovery without hand-built HTTP; TypeScript also exposes the
  documented ergonomic helper.
- [ ] **AC-2B.6 (`EX-2B.4`):** `nvoken mcp list-tools` returns the same projected
  catalog as execution-time discovery for an identical server declaration.
- [ ] **AC-2B.7 (phase gate):** One documented example discovers a scripted
  remote server, completes a durable MCP tool turn, survives a fault-injected
  engine replacement, and is recoverable through the authoritative result and
  transcript reads.

### Phase 3 — additive contract completeness

**Outcome:** resource discovery, Session control, efficient recovery, schema
preflight, and current context-overflow behavior are complete without coupling
independent additions into one release.

| Work ID | Slice | Depends on |
| --- | --- | --- |
| `EX-3.1` | Add Agent identity list/get and `agent_key` filters. | Phase 1 |
| `EX-3.2` | Add atomic `if_active: supersede`. | Phase 1 |
| `EX-3.3` | Add repeated status filters, stream delta selection, and cumulative Session usage. | Phase 1 |
| `EX-3.4` | Add one runtime/SDK structured-output schema preflight contract. | Phase 1 |
| `EX-3.5` | Add typed `context_window_exceeded` failure. | Phase 1 |

**Acceptance gate:**

- [ ] **AC-3.1 (`EX-3.1`):** A host can resolve an Agent identity by
  `agent_key`, read it without admitting work, and list Sessions and
  Invocations by either `agent_key` or `agent_id` with equivalent scoped
  results.
- [ ] **AC-3.2 (`EX-3.2`):** Concurrent admission tests prove supersession
  closes the prior Invocation and pending ToolCalls and admits the successor
  in one transaction, while the default `reject` path remains unchanged.
- [ ] **AC-3.3 (`EX-3.3`):** Repeated status filters return the union without
  duplicates, `deltas=false` yields only durable stream frames without
  changing cursor recovery, and Session usage equals the authoritative sum of
  its Invocation usage.
- [ ] **AC-3.4 (`EX-3.4`):** The same fixture set in the runtime and four SDKs
  accepts the bounded schema subset and rejects each unsupported keyword with
  the same keyword and JSON path before admission.
- [ ] **AC-3.5 (`EX-3.5`):** A context overflow settles with
  `context_window_exceeded` and safe token-count details, remains readable
  after restart, and is not collapsed into a generic provider failure.
- [ ] **AC-3.6 (phase gate):** Each slice passes `make check`,
  `make sdk-check`, OpenAPI drift checks, and its own focused PRD acceptance
  criteria without requiring another Phase 3 slice to ship.

### Phase 4 — execution intelligence

**Outcome:** provider controls fail closed, reasoning state is durable, and
quality/context loops build on explicit checkpoint evidence rather than hidden
provider behavior.

| Work ID | Slice | Depends on |
| --- | --- | --- |
| `EX-4.1` | Define provider/model capability validation and its source of truth. | Phase 1 |
| `EX-4.2` | Add only sampling controls the installed stack can guarantee. | `EX-4.1` |
| `EX-4.3` | Add reasoning controls and lossless provider-artifact replay. | `EX-4.1` |
| `EX-4.4` | Add outcome-graded Invocations in a focused successor PRD. | `EX-4.3`; Phase 2B for proposed priority |
| `EX-4.5` | Add server-side compaction in a focused successor PRD. | `EX-3.5`, `EX-4.3` |

**Acceptance gate:**

- [ ] **AC-4.1 (`EX-4.1`):** For every installed provider/model/control tuple,
  admission can prove support or reject it with a stable field-level reason;
  no requested control reaches a path that silently ignores it.
- [ ] **AC-4.2 (`EX-4.2`):** Omitted sampling controls preserve provider
  defaults, explicit controls are fingerprint-material, and cross-provider
  fixtures prove either honored normalization or pre-admission rejection.
- [ ] **AC-4.3 (`EX-4.3`):** Effort and budget validation is fail closed, and
  encrypted reasoning items, signatures, phase markers, and other required
  opaque artifacts survive iteration checkpoints, engine replacement, and the
  next turn without leaking into public transcript content.
- [ ] **AC-4.4 (`EX-4.4`):** Outcome evaluation is budget bounded and
  checkpointed; satisfied, unsatisfied, and inapplicable results settle as
  completed durable evidence, while infrastructure failure remains a failed
  Invocation.
- [ ] **AC-4.5 (`EX-4.5`):** Compaction never mutates the canonical transcript,
  provider context uses one durable projection artifact and retained-tail cut,
  recompaction supersedes predictably, and restart produces the same context
  projection.
- [ ] **AC-4.6 (phase gate):** Outcome and compaction each have an independently
  approved PRD and can ship separately after the shared capability and
  reasoning invariants are proven.

### Phase 5 — adoption-driven breadth

**Outcome:** additional reach ships as independently justified capabilities,
not as an undifferentiated breadth wave.

| Work ID | Slice | Depends on |
| --- | --- | --- |
| `EX-5.1` | Add the guarded `fetch` builtin. | Phase 2B guarded-egress proof |
| `EX-5.2` | Add Session seeding and forking when migration evidence warrants it. | Phase 1 |
| `EX-5.3` | Add multimodal image and document input. | `EX-4.1` |
| `EX-5.4` | Add per-Invocation settled notification delivery. | Existing callback delivery; Phase 1 |
| `EX-5.5` | Expose additional providers through the full credential, catalog, pricing, and deployment contract. | `EX-4.1` |
| `EX-5.6` | Widen structured-output schemas with bounded `$ref` and `anyOf` handling. | `EX-3.4` |

**Acceptance gate:**

- [ ] **AC-5.1 (`EX-5.1`):** `fetch` permits only guarded public HTTPS GETs,
  refuses redirects and private/link-local destinations, bounds response size
  and time, and leaves durable ToolCall/checkpoint evidence before and after
  execution.
- [ ] **AC-5.2 (`EX-5.2`):** A fork creates a new tenant-scoped Session from an
  immutable canonical prefix with provenance; seeded import preserves message
  origins without admitting a billed model turn; neither path becomes a
  generic live-Session append.
- [ ] **AC-5.3 (`EX-5.3`):** Image/PDF requests enforce media, decoded-size, and
  request limits; fingerprint digests are stable without embedding raw bytes;
  admission rejects unsupported model modalities before durable work becomes
  claimable.
- [ ] **AC-5.4 (`EX-5.4`):** Terminal settlement commits notification intent
  durably, delivery is signed and idempotent by Invocation ID, duplicate
  attempts cannot produce different payloads, and exhausted delivery does not
  change the Invocation’s terminal state.
- [ ] **AC-5.5 (`EX-5.5`):** Each additional provider passes credential
  resolution, exact model inspection, catalog/pricing policy, generation,
  failure classification, limits, and documented deployment configuration;
  merely existing in Dive is insufficient.
- [ ] **AC-5.6 (`EX-5.6`):** Bounded nonrecursive references and closed-world
  unions accepted by SDK preflight are lowered identically by the runtime;
  recursion and expansion beyond position/byte limits fail with the same path
  before admission.
- [ ] **AC-5.7 (phase gate):** Every shipped breadth item has its own evidence
  or approved demand rationale, focused PRD where required, conformance proof,
  and honest documentation; unfinished items remain independent backlog rather
  than blocking completion of shipped siblings.

### Program completion

The API and SDK excellence program is complete when:

- [ ] **AC-PROGRAM-1:** Every shipped work item above has all mapped acceptance
  criteria checked with repository or deployment evidence.
- [ ] **AC-PROGRAM-2:** The governing design packet, PRD roadmap, OpenAPI,
  generated transports, handwritten facades, CLI, examples, README, SDK
  READMEs, and changelog describe the same current surface.
- [ ] **AC-PROGRAM-3:** `make check`, `make sdk-check`, OpenAPI lint, generated
  drift checks, fingerprint fixtures, and all applicable profile qualification
  gates pass from the release commit.
- [ ] **AC-PROGRAM-4:** No public claim depends on an unfinished backlog item;
  deferred capabilities are named as deferred, and completed capabilities have
  a documented normal path, failure path, retry/recovery behavior, and
  point-of-use example.
