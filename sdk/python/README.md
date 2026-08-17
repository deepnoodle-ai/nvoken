# nvoken Python SDK

An Invocation is one durable turn by a deliberately created, tenant-scoped
Agent. An Agent binds your `agent_key` to one App-owned, versioned Agent
Definition; a Session is one conversation with that Agent.

The package has three deliberate levels:

- `Agent` is the ordinary workflow facade: `text`, `run`, `invoke`, `stream`,
  and locally serialized bound Sessions.
- `Client` and `InvocationHandle` expose durable operations, transcript drains,
  provider-key lifecycle, iterators, configurable waits, and resumable streams.
- `nvoken_generated` is the complete generated Runtime transport and raw
  escape hatch.

```bash
python -m pip install nvoken
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  python examples/quickstart.py
```

The async facade provides durable handles, replay-safe retries, typed errors,
cursor iterators, resumable SSE, composed result reads (`result`,
`list_messages`, `output_text`), and callback verification. Session-scoped
messages use `Client.list_session_messages`.

List or read the Agent record without admitting work:

```python
agents = await client.list_agents(agent_key="support")
record = await client.get_agent(agents.items[0].id)
```

`AgentResource` is that record: tenant, key, display name, Definition binding,
optional revision pin, lifecycle timestamps, and archive state. `Agent` is the
object that runs its turns, and it corresponds to the same row — declare one
from the keys you already own and it creates its record on first use:

```python
agent = client.agent(AgentOptions(
    tenant_key=user_id,
    agent_key="support",
    definition_key="support",   # the Definition this instance follows
    tools=(lookup_order,),      # this process's handlers; never on the server
))
```

An Agent's identity and configuration live on the server; its tool handlers are
supplied by whichever process runs the turn. `await agent.ensure()` creates the
record at a moment you choose instead of on first use, and never mutates: the
same keys and Definition resolve onto what exists, a different Definition is
`agent_key_conflict`, an archived record is `agent_archived`, and a declared
`pinned_revision` the record does not follow is refused. `agent.resource` and
`agent.id` report the record once it is known, and `agent.with_tools()` attaches
handlers to an Agent read back from the server.

Opt into the fixed guarded public-web reader with `fetch_tool()`:

```python
from nvoken import AgentDefinition, AgentOptions, Model, fetch_tool

definition = await client.create_agent_definition(AgentDefinition(
    definition_key="research",
    name="Research",
    instructions="Use nvoken_fetch for public URLs, then summarize the source.",
    model=Model(provider="anthropic", id="claude-sonnet-5"),
    tools=(fetch_tool(),),
))
options = AgentOptions(agent_key="research", definition_key=definition.definition_key)
```

The Runtime accepts only `{"name":"nvoken_fetch","mode":"builtin"}`. It owns
public-address checks, up to five guarded redirects, one transient retry,
HTML-to-Markdown conversion, and the ten-second and 64 KiB limits. Run
`python examples/fetch.py` to summarize `NVOKEN_FETCH_URL`.

Use an Agent for the common path:

```python
agent = client.agent(AgentOptions(
    agent_key="support",
    definition_key="support",
))

print(await agent.text("Why was I charged twice?"))
continued = agent.session(session_key="customer-123")
print(await continued.text("What should I do next?"))
```

A bound Session serializes admission only within that local binding. The
Runtime remains authoritative across processes and rejects a second
nonterminal turn. Agent operations dispatch configured host-tool handlers.
If a waiting call has no handler, the Agent cancels before raising
`MissingToolHandlerError` by default; set
`InvocationOptions(leave_waiting_on_missing_handler=True)` only when another
worker deliberately owns it. `NoOutputTextError.result_kind` distinguishes
structured, tool-only, and empty completions.

For an intentional replace/regenerate action, use a new idempotency key and
the typed option:

```python
handle = await agent.invoke(
    "Try that answer again.",
    options=InvocationOptions(
        idempotency_key="customer-123:regenerate-2",
        if_active="supersede",
        session_key="customer-123",
    ),
)
```

Omission or `"reject"` preserves the default conflict response. Low-level
callers set the same policy on `InvokeRequest.if_active`.

`if_active="interrupt"` is the keep-the-work variant: the active Invocation
stops at its next execution seam and settles `completed` with `stop_reason`
`"interrupted"`, so the replacement turn builds on what it already produced.
`await handle.interrupt()` asks for the same graceful stop without admitting a
replacement, and `Invocation.stop_reason` names why any turn ended.

A turn can also stop without ending: `"incomplete"` means the Runtime enforced
a budget at a seam, with `stop_reason` naming which one. It is terminal — the
wait helpers stop there — and its work is kept, so treat it as an unfinished
answer rather than an error. `SessionMessage.phase` says which assistant
message was the reply: `"final_answer"` on the one that ended a completed
turn, `"commentary"` on everything else, so an incomplete turn has none.

`InvocationOptions(timeout=...)` is one overall local deadline. Cancelling the
calling task still raises native `asyncio.CancelledError`; it does not imply a
durable Runtime cancellation. Call `handle.cancel()` when that is intended.

Recovery reads accept a status union, and a known Invocation can stream only
durable frames:

```python
page = await client.list_invocations(
    status=["queued", "running", "waiting"],
)
async for event in handle.events(deltas=False):
    ...
```

Equivalent status sets share cursor identity regardless of input order.
Session get/list models expose typed nullable `usage`, computed from durable
Invocation usage as a convenience estimate rather than a billing ledger.

Install restart-stable compaction on a new or existing Session:

```python
from nvoken import ContextCompaction, SessionOptions

request = InvokeRequest(
    agent_key="support",
    session_key="support:123",
    session_options=SessionOptions(
        compaction=ContextCompaction(trigger_tokens="auto"),
    ),
    input="hello",
)
```

Use an integer trigger and optional same-provider `model` for explicit policy.
A Session without a policy accepts late opt-in; once installed, the policy is
immutable. Supplied options on an existing Session must equal stored values or
admission returns `session_options_conflict`.

Summary usage appears in Session usage rather than Invocation usage. Read
applied and fell-through diagnostics with
`client.list_session_compactions(session_id)`.

`InvocationOptions.metadata` correlates a turn with your own records from the
Agent binding. It is part of the admitted input, so it is immutable and material
to idempotency: a replay carrying different metadata conflicts rather than
updating it. That is why it is per-call rather than an `AgentOptions` default.

Pass a stored or one-turn provider key directly through
`InvokeRequest`:

```python
request = InvokeRequest(
    agent_key="support",
    input="hello",
    provider_keys=(
        ProviderKeySelection(
            provider="openai",
            source="caller_ephemeral",
            api_key=provider_key,
        ),
    ),
)
```

Stored sources are `app_byok`, `tenant_byok`, and `platform` and do not
accept an `api_key`. `Client.stream_session(session_id, reducer, consume)`
follows the Session until its task is cancelled; a terminal turn does not end
the Session stream. For catch-up reads, use `get_transcript_page` when
checkpointing each page or `drain_transcript` to consume one fixed cut.

Discover models through the same async facade:

```python
catalog = await client.list_models(provider="openai")
selected = await client.get_model(
    Model(provider="openai", id=catalog.items[0].id)
)
print(selected.cataloged, selected.pricing.status)
```

The list is curated discovery metadata, not proof of provider-account access.
Exact inspection also accepts uncataloged IDs.

Set an explicit portable temperature on the Agent Definition or a safe
per-turn override:

```python
from nvoken import AgentDefinitionOverrides, InvokeRequest, Model, Sampling

request = InvokeRequest(
    agent_key="support",
    input="hello",
    overrides=AgentDefinitionOverrides(
        model=Model(provider="anthropic", id="claude-haiku-4-5"),
        sampling=Sampling(temperature=0),
    ),
)
```

Omit `sampling` to preserve the provider default. Check
`selected.controls.sampling.temperature` first; missing controls are unknown,
and unsupported or unknown selections fail before durable admission. The
portable range is `[0,1]`. `top_p` and stop sequences are intentionally absent;
`limits.max_output_tokens` is the output guardrail.

Reasoning is typed and fail closed:

```python
from nvoken import AgentDefinitionOverrides, InvokeRequest, Model, Reasoning

request = InvokeRequest(
    agent_key="support",
    input="hello",
    overrides=AgentDefinitionOverrides(
        model=Model(provider="anthropic", id="claude-opus-5"),
        reasoning=Reasoning(effort="high"),
    ),
)
```

Check `selected.controls.reasoning` first. `budget_tokens` requires a larger
explicit `limits.max_output_tokens`. Omission preserves provider defaults;
unsupported values and combinations are rejected without aliasing. OpenAI
reasoning remains unavailable until its complete continuation representation
is durable.

## Structured-output schema preflight

`Client.invoke` and Agent operations call
`preflight_output_schema(schema)` before transport when
`InvokeRequest.overrides.output_schema` is present. Rejection is an `NvokenError` with
code `schema_preflight_failed`; its safe `details` contain the portable issue
`code`, RFC 6901 `path`, and optional `keyword`. A successful local check means
eligible for admission. Generated APIs reached through `client.raw()` still
rely on the authoritative Runtime check.

## Define and instantiate an Agent

Every turn runs against an App-owned, versioned Agent Definition. Create a
tenant-scoped Agent instance that binds to the template before admitting work:

```python
resource = await client.create_agent_definition(AgentDefinition(
    definition_key="support",
    name="Support",
    instructions="Help with billing questions.",
    model=Model(provider="anthropic", id="claude-sonnet-5"),
))
agent = client.agent(AgentOptions(
    agent_key="support",
    definition_key=resource.definition_key,
))

handle = await agent.invoke("Why was I charged twice?")
```

`AgentDefinition` is flat and matches the wire, and a read gives back the same
fields plus `id`, `revision`, and timestamps, so a change is a read, a
`replace()`, and a write:

```python
from dataclasses import replace

current = await client.get_agent_definition(resource.id)
await client.update_agent_definition(
    current.id,
    replace(AgentDefinition.from_resource(current), instructions="Be concise."),
    expected_revision=current.revision,
)
```

An update replaces the whole resource, so send back everything you want kept;
`from_resource` is what keeps it whole. `expected_revision` travels as
`If-Match`, so a concurrent write fails rather than overwriting.

Creating a Definition starts no turn. It has an immutable `definition_key`, a
stable ID, and an increasing revision; `get_agent_definition_revision()` reads
historical revisions. Updating a Definition does not rewrite an Agent's
binding. An Agent or Session may pin a revision, while an Invocation may select
one revision for that turn. Safe `overrides` cover model, sampling, reasoning,
tool choice, limits, and output schema; they cannot expand tools, data access,
memory authority, or instructions. Host tool handlers remain local to the SDK
facade.

## Record changing application state

Keep `instructions` static. Product state that changes between turns — a board
snapshot, customer facts, the current policy — belongs in `context`:

```python
answer = await agent.text(
    "Can I refund the duplicate charge?",
    InvocationOptions(
        session_key="ticket-483",
        context=(
            ContextItem(name="customer", tier="contextual", content="plan: pro"),
            ContextItem(
                name="refund-policy",
                tier="operator",
                content="Self-serve refunds cap at 50 USD",
            ),
        ),
    ),
)
```

A name is a stable identity. Send it once and nvoken records it as a leading
message the model reads as `app-customer`; omit that reserved prefix here.
Send the same name again only when its value changes — a byte-identical resend
is accepted but adds no message, so a stateless host may resend its whole
snapshot every turn and get the same transcript as a host that tracks changes.

Use `contextual` for conversation-adjacent facts and `operator` for policy or
other application-authoritative state. Context is Session history, not an Agent
Definition field: it never changes `definition_id`, and later turns keep
sending it to the model even when you omit it. That is what keeps the prompt
prefix stable enough for provider caching, which rewriting the same state into
`instructions` would break on every turn.

The list is order-sensitive and part of idempotency, so a replay that reorders
or edits an item conflicts rather than updating it. A request accepts at most
eight items, 8 KiB per item, and 16 KiB in total; the SDK checks all three
before the request leaves the process. A Session may accumulate at most 16
distinct names, which only the service can check. Retire a name by superseding
it with a short current value such as `"ticket: closed"`.

## Remote MCP tools

Use the handwritten declaration for discovery. Store the same declaration on
the Agent Definition, then pass only its one-turn secret headers at admission:

```python
server = MCPServer(
    name="support",
    url="https://mcp.example.com/rpc",
    allowed_tools=("lookup_order",),
    timeouts=MCPTimeouts(discovery_seconds=10, call_seconds=30),
)
headers = {"Authorization": f"Bearer {mcp_token}"}

catalog = await client.list_mcp_tools(server, headers)
request = InvokeRequest(
    agent_key="support",
    input="hello",
    mcp_server_headers=(MCPServerHeaders(name="support", headers=headers),),
)
```

The declaration carries no secrets. An Agent Definition may be reused across
turns, so authentication headers travel per Invocation in
`mcp_server_headers`, keyed to the server name. They are hidden from dataclass
representation, are one-Invocation secret material, and never appear in durable
Agent Definitions or public recovery surfaces.

## Callback tools

A callback tool runs on an HTTPS endpoint nvoken posts to. Verify the signed
delivery with `verify_callback`, then answer with one of two replies.
`callback_result(content, is_error=False)` settles the ToolCall inline and the
turn resumes as soon as nvoken records the reply. `acknowledge_callback()`
returns `202` with no body instead: it accepts the delivery without settling the
call, for work that will outlive this tool's reply deadline — its declared
`timeout_seconds`, or the App's default when it declares none. Settle it later
with `client.submit_tool_results`, reusing the delivery's ToolCall ID.

Every delivery names its tool inside the signed body, so one endpoint can serve
several tools: dispatch on `verified.tool_name` rather than on a path suffix
nothing signs.

Acknowledging trades away the fail-loud guarantee. nvoken marks an
unacknowledged delivery failed once its retries are exhausted, so the turn
always moves on. An acknowledged call instead waits under your responsibility,
bounded only by the Invocation's `limits.waiting_timeout_seconds`. Acknowledge
only when something durable will settle the call. Such a call appears in a
waiting Invocation's pending calls the same way a host call does, so
`answerable_tool_calls` includes it. `host_tool_calls` is the narrower set you
must run yourself — answerable and `mode` `host` — and an `Agent` dispatches
exactly that, whatever its own definition declares.

## A callback endpoint, whole

`verify_callback` is the signature. Around it every receiver writes the same key
table, the same deduplication, and the same reply discipline —
`CallbackReceiver` is that, so you write the tools:

```python
from nvoken import CallbackReceiver, DeliverySigningKey, callback_result


async def open_ticket(delivery):
    board = delivery.authorization_context.get("board")
    return callback_result(await create_ticket(board, delivery.envelope["input"]))


receiver = CallbackReceiver(
    # Two entries span a rotation: nvoken mints the next version while still
    # signing with the current one.
    keys=[
        DeliverySigningKey(key_id=KEY_ID, version=2, secret=SECRET),
        DeliverySigningKey(key_id=KEY_ID, version=1, secret=PREVIOUS_SECRET),
    ],
    tools={"open_ticket": open_ticket},
    store=ticket_replies,
)


@app.post("/nvoken/callbacks")
async def callbacks(request):
    answered = await receiver.handle(dict(request.headers), await request.body())
    log.info("nvoken callback", extra={"outcome": answered.outcome, "reason": answered.reason})
    return Response(answered.reply.body, status_code=answered.reply.status)
```

`handle` never raises. Everything that can go wrong is a status nvoken
understands, and `outcome` — `settled`, `acknowledged`, `replayed`, `refused`,
`failed` — is what the status alone cannot tell you, with a stable `reason`
token for the log line.

The statuses are decisions about whether nvoken tries again:

| situation | status |
| --- | --- |
| no keys configured | 503 — an operator error, still fixable in the window |
| signing identity not held | 401 — redelivery reproduces it |
| signature or envelope invalid | 401 — the same bytes fail the same way |
| no handler for the signed tool name | 400 — nothing here can ever run it |
| a tool answered, or failed | 200 — settle it, carrying `is_error` if it failed |
| your handler raised | 503 — you failed, not the tool |

A failed tool is not a failed receiver: settle it with
`callback_result(reason, True)` and the model can correct itself, where a 5xx
only has nvoken deliver the same doomed call again.

`store` is a `find`-then-`put_if_absent` pair, in that order. `find` runs before
your tool does, because delivery is at least once and re-running the tool
repeats every effect it had; `put_if_absent` runs after, because two deliveries
of one ToolCall can be in flight at once and only one reply may win. Omit
`store` only when every tool on the endpoint is safe to run twice.

The key table is validated when the receiver is built: a non-positive version, a
secret under 32 bytes, or the same `(key_id, version)` twice raises at startup
rather than refusing a live delivery, where the refusal would be permanent.

### Authorizing a delivery

Verification proves the delivery came from nvoken. It does not say what the work
belongs to, and the tool input cannot either — the model wrote it.
`session_options.authorization_context` is what you asserted when the Session
was created, and it arrives inside the signed body as a **sibling** of `nvoken`:

```json
{
  "nvoken": { "tool_name": "open_ticket", "tenant_key": "acme" },
  "authorization_context": { "board": "brd_9f21" },
  "input": { "board": "brd_9f21", "ticket": "A-42" }
}
```

Everything inside `nvoken` is a fact nvoken minted; this is a value you asserted
and nvoken carried unchanged. Signing proves it reached you as recorded, not
that it is true. Which gives the rule:

> **A value repeated in tool input may only agree with the authorization
> context, never establish it.**

Checking that the two agree is reasonable. Reading the board out of `input` when
the context is absent is not. Authorizing from the signed sibling is also what
removes the per-delivery `get_invocation` a receiver otherwise needs to recover
which of your objects the work is for.

[Receiving signed deliveries](../../docs/reference/callback-receivers.md) is the
long form, language-neutral.

## Invocation webhooks

A turn that ends tells you so, without you holding a connection open to hear
it. Ask for it when you start the turn:

```python
invocation = await client.create_invocation(
    agent_key="support",
    input="Where is my order?",
    webhook=WebhookTarget(url="https://example.com/nvoken/webhooks"),
)
```

Omitting `events` selects all three. `invocation.ended` fires once when the
turn reaches a terminal status; `invocation.waiting` fires when it needs a host
tool run; `invocation.paused` fires when a spending limit stopped it.

Receiving one is the same verification you already wrote for callbacks — the
signature scheme is identical, and `verify_webhook` is the same code path with
a different body check:

```python
from nvoken import accept_webhook, retry_webhook, verify_webhook

delivery = verify_webhook(webhook_signing_key, headers, raw_body)
if delivery.supersedes(await last_applied_sequence(delivery.invocation_id)):
    await settle(delivery.invocation_id, delivery.envelope["invocation"], delivery.sequence)
return accept_webhook()
```

The key is the App's `webhook`-purpose signing key, not its `callback` key. A
receiver serving both endpoints holds two, and must not try either against the
other's deliveries.

Three rules make a receiver correct:

**Fold by sequence, not by arrival.** Delivery is at least once, so the same
transition can arrive twice and a redelivery can land after a later one. Keep
the highest `sequence` you have applied per Invocation and apply only what
`supersedes` accepts. That is the deduplication too — a repeat carries a
sequence you already applied — so nothing else is needed to make handling
idempotent.

**The payload is a pointer, not a copy.** It carries `status`, `stop_reason`,
`failure_code`, `waiting_tool_call_ids`, and `credit_block`, and deliberately
nothing else: no transcript, no output text, no usage. Read `get_invocation` or
`get_invocation_result` when you need more, so you are reconciling against the
authoritative record rather than a staler copy of it.

**Answer `retry_webhook()` when you could not record it.** Any 5xx is
redelivered, as are 408, 425, and 429. Every other non-2xx answer is permanent
and that transition is never delivered again, so a 400 from a receiver that was
merely busy is a settlement you silently lost.

Retries are bounded, so webhooks alone are not a settlement guarantee.
`client.list_ended_invocations` is the backstop: it walks turns in the order
they ended, so a delivery that never landed is one you still find.

`WebhookReceiver` is `CallbackReceiver`'s twin — same key table, same reply
discipline — for the endpoint that has more than one event:

```python
receiver = WebhookReceiver(
    keys=[DeliverySigningKey(key_id=WEBHOOK_KEY_ID, version=1, secret=WEBHOOK_SECRET)],
    events={
        "invocation.ended": settle_in_one_transaction,
        "invocation.paused": alert_on_funding_hold,
    },
)
```

A handler that returns answers 200; one that raises answers 503 and nvoken
delivers again. An event you subscribed to but registered no handler for
answers 200 with outcome `ignored` — retrying it would only spend nvoken's
bounded attempts reaching the same absent handler.

The sequence fold stays yours, deliberately: the compare and the write have to
happen in the same transaction as the state they guard, and the receiver cannot
open it. Call `delivery.supersedes(applied)` inside yours, and record nothing
when it says no — a superseded delivery is still a delivery, and still answers
200.

## Browser-direct access

Your page can talk to nvoken itself, with no server of yours in the path. Mint
a short-lived grant in backend code and hand the browser that:

```python
token = mint_client_token(
    client_key_seed,
    ClientTokenClaims(
        app_id=app_id,
        key_id=client_key_id,
        subject=user.id,          # from your session, never from the request
        tenant_key=user.workspace_id,
        agent_key="support",
        operations=["create_invocation", "get_session_transcript"],
        lifetime=timedelta(minutes=10),
    ),
)
```

Signing needs Ed25519, which the standard library does not provide, so install
`nvoken[client-tokens]` for it.

`nvoken client-key generate <app-id> --name web` produces the keypair and
registers its public half in one step. The private seed is the App's browser
authority — whoever holds it can mint a grant for any end user — so it belongs
in backend configuration and never in a bundle.

Three things are worth deciding rather than defaulting. `operations` is
required: nvoken reads an absent list as every operation a browser may perform,
so this SDK refuses to spell "I did not think about scope" the same way as
`all_browser_operations()`. `session_id` confines the token to one
conversation, which a single-conversation UI should set. And a lifetime is
capped at fifteen minutes, because short lifetimes are the whole safety story
of a bearer token in a page.

**Invocation webhooks stop being optional here.** The browser holds the stream,
so your backend never observes settlement any other way.

## Acting for one tenant or one end user

An app-wide credential can reach every tenant in its App, so an id that arrives
from the wrong place — a stale link, a mixed-up webhook, a tampered form field
— is an id it can act on. Say the scope once instead of re-reading the resource
before every call:

```python
from nvoken import Scope

tenant = client.scoped(Scope(tenant_key="acme", user_key="user-7c1f"))
session = await tenant.get_session(session_id_from_the_request)
```

Anything outside the scope is reported as `not_found`, so a Session or
Invocation belonging to somebody else cannot be read, cancelled, interrupted,
forked, answered, or erased — and you learn nothing about whether the id
exists. Writes take the same scope: an omitted tenant or user key in the body
inherits it, and one naming somebody else is refused.

A scope may only narrow. A credential already bound to one tenant refuses a
scope naming another with `forbidden` rather than silently returning nothing.
The client it was derived from is unchanged, so the unscoped one keeps doing
administrative reads. Browser tokens already carry their tenant and end user
and neither need this nor may send it.
