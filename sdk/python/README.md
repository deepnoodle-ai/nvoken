# nvoken Python SDK

An Invocation is one durable agent turn. The host supplies `agent_key`,
optional `tenant_key`, `session_key`, and `idempotency_key`; instructions,
model, and tools travel inline with the turn.

The package has three deliberate levels:

- `Agent` is the ordinary workflow facade: `text`, `run`, `invoke`, `stream`,
  and locally serialized bound Sessions.
- `Client` and `InvocationHandle` expose durable operations, transcript drains,
  credential lifecycle, iterators, configurable waits, and resumable streams.
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

Use an Agent for the common path:

```python
agent = client.agent(AgentOptions(
    agent_key="support",
    spec=ExecutionSpec(
        instructions="Help with billing questions.",
        model=Model(provider="anthropic", id="claude-sonnet-5"),
    ),
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

`InvocationOptions(timeout=...)` is one overall local deadline. Cancelling the
calling task still raises native `asyncio.CancelledError`; it does not imply a
durable Runtime cancellation. Call `handle.cancel()` when that is intended.

Pass a stored or one-turn provider credential directly through
`InvokeRequest`:

```python
request = InvokeRequest(
    agent_key="support",
    input="hello",
    spec=spec,
    provider_credentials=(
        ProviderCredentialSelection(
            provider="openai",
            source="caller_ephemeral",
            api_key=provider_key,
        ),
    ),
)
```

Stored sources are `account_byok`, `tenant_byok`, and `platform` and do not
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

## Remote MCP tools

Use the handwritten declaration for discovery and Invocation admission:

```python
server = MCPServer(
    name="support",
    url="https://mcp.example.com/rpc",
    allowed_tools=("lookup_order",),
    headers={"Authorization": f"Bearer {mcp_token}"},
    timeouts=MCPTimeouts(discovery_seconds=10, call_seconds=30),
)

catalog = await client.list_mcp_tools(server)
spec = ExecutionSpec(
    model=Model(provider="anthropic", id="claude-sonnet-5"),
    mcp_servers=(server,),
)
```

Headers are hidden from dataclass representation and are one-Invocation secret
material. They never appear in durable specs or public recovery surfaces.
