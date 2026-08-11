# nvoken Python SDK

An Invocation is one durable agent turn. The host supplies `agent_key`,
optional `tenant_key`, `session_key`, and `idempotency_key`; instructions,
model, and tools travel with the turn as an `agent_definition`, either inline or
referenced by a registered `agent_definition_id`.

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

Resolve the identity-only Agent anchor without admitting work:

```python
agents = await client.list_agent_identities(agent_key="support")
identity = await client.get_agent_identity(agents.items[0].id)
```

The identity contains only its nvoken ID, host-owned key, and creation time.
Instructions, models, tools, and provider keys remain per Invocation.

Opt into the fixed guarded public-web reader with `fetch_tool()`:

```python
from nvoken import AgentOptions, Model, fetch_tool

options = AgentOptions(
    agent_key="research",
    model=Model(provider="anthropic", id="claude-sonnet-5"),
    tools=(fetch_tool(),),
)
```

The Runtime accepts only `{"name":"nvoken_fetch","mode":"builtin"}`. It owns
public-address checks, up to five guarded redirects, one transient retry,
HTML-to-Markdown conversion, and the ten-second and 64 KiB limits. Run
`python examples/fetch.py` to summarize `NVOKEN_FETCH_URL`.

Use an Agent for the common path:

```python
agent = client.agent(AgentOptions(
    agent_key="support",
    instructions="Help with billing questions.",
    model=Model(provider="anthropic", id="claude-sonnet-5"),
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
    agent_definition=AgentDefinition(
        model=Model(provider="anthropic", id="claude-sonnet-5"),
    ),
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
    agent_definition=AgentDefinition(
        model=Model(provider="openai", id="gpt-test"),
    ),
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

Set an explicit portable temperature on the request or Agent:

```python
from nvoken import AgentDefinition, InvokeRequest, Model, Sampling

request = InvokeRequest(
    agent_key="support",
    input="hello",
    agent_definition=AgentDefinition(
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
from nvoken import AgentDefinition, InvokeRequest, Model, Reasoning

request = InvokeRequest(
    agent_key="support",
    input="hello",
    agent_definition=AgentDefinition(
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
`InvokeRequest.agent_definition.output_schema` is present. Rejection is an `NvokenError` with
code `schema_preflight_failed`; its safe `details` contain the portable issue
`code`, RFC 6901 `path`, and optional `keyword`. A successful local check means
eligible for admission. Generated APIs reached through `client.raw()` still
rely on the authoritative Runtime check.

## Reuse an Agent Definition

Sending the definition inline is the ordinary path. Register it instead when
many turns share one configuration and you would rather send a short ID:

```python
registration = await client.register_agent_definition(AgentDefinition(
    instructions="Help with billing questions.",
    model=Model(provider="anthropic", id="claude-sonnet-5"),
))

handle = await client.invoke(InvokeRequest(
    agent_key="support",
    input="Why was I charged twice?",
    agent_definition_id=registration.agent_definition_id,
))
```

A definition is content-addressed and immutable, so registering the same one
twice returns the same `agent_definition_id`, and so does registering one an
earlier inline turn already stored. Registering starts no turn and creates no
Agent, Session, or message. There is no list, update, or delete: to change a
definition, register the new one and reference that.

Supply exactly one of `agent_definition` and `agent_definition_id`; the facade
rejects a request carrying both or neither before it reaches the network. An
`Agent` always sends its definition inline, because it serves the host tool
handlers declared in it, which is why `AgentOptions` spells the definition's
fields flat rather than nesting them: there is no choice there to express.

## Remote MCP tools

Use the handwritten declaration for discovery and Invocation admission:

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
    agent_definition=AgentDefinition(
        model=Model(provider="anthropic", id="claude-sonnet-5"),
        mcp_servers=(server,),
    ),
    mcp_server_headers=(MCPServerHeaders(name="support", headers=headers),),
)
```

The declaration carries no secrets. An Agent Definition is content-addressed and
reused across turns, so authentication headers travel per Invocation in
`mcp_server_headers`, keyed to the server name. They are hidden from dataclass
representation, are one-Invocation secret material, and never appear in durable
Agent Definitions or public recovery surfaces.

## Callback tools

A callback tool runs on an HTTPS endpoint nvoken posts to. Verify the signed
delivery with `verify_callback`, then answer with one of two replies.
`callback_result(content, is_error=False)` settles the ToolCall inline and the
turn resumes as soon as nvoken records the reply. `acknowledge_callback()`
returns `202` with no body instead: it accepts the delivery without settling the
call, for work that will outlive the App's callback reply deadline. Settle it
later with `client.submit_tool_results`, reusing the delivery's ToolCall ID.

Acknowledging trades away the fail-loud guarantee. nvoken marks an
unacknowledged delivery failed once its retries are exhausted, so the turn
always moves on. An acknowledged call instead waits under your responsibility,
bounded only by the Invocation's `limits.waiting_timeout_seconds`. Acknowledge
only when something durable will settle the call. Such a call appears in a
waiting Invocation's pending calls the same way a host call does; an `Agent`
skips the callback tools its own definition declares rather than dispatching
them locally.
