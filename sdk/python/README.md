# nvoken Python SDK

The Python SDK gives an application a local-feeling facade over nvoken's durable
Agent runtime. Reusable behavior is an `Agent`; one execution is a `Turn`;
continuity is an explicit `Conversation`; memory is selected independently.

```bash
pip install nvoken
```

## Run a stored Agent

Looking up an Agent is awaited because it performs a remote read and fails
immediately when the exact key does not exist. An omitted `owned_by` means the
App-owned namespace.

```python
import os
from nvoken import Client

async with Client(os.environ["NVOKEN_API_KEY"]) as client:
    analyst = await client.agent("real-estate-analyst")
    answer = await analyst.text(
        "Compare these two listings",
        tenant="acme",
        user="alice",
    )
```

Tenant- and user-owned Agent keys are explicit:

```python
from nvoken import OwnedBy

custom = await client.agent("assistant", owned_by=OwnedBy("acme"))
personal = await client.agent("assistant", owned_by=OwnedBy("acme", "alice"))
```

The Agent's owner, the Turn actor, Conversation ownership, and memory scope are
independent. Every execution states its tenant; `user`, `memory`,
`conversation`, and narrowed `limits` are optional per-execution coordinates.

```python
from nvoken import ConversationRef, Memory

turn = await analyst.start(
    "Analyze the offer",
    tenant="acme",
    user="alice",
    memory=Memory.tenant("deal-team"),
    conversation=ConversationRef.by_key("deal-42", owner="user"),
    limits={"max_iterations": 6},
)

snapshot = await turn.status()
result = await turn.result(timeout=60)
```

`start()` returns after durable admission. `run()` is `start()` followed by
`result()`. `text()` additionally requires text output. Dropping a waiter or
update iterator never cancels the Turn. A local `timeout=` covers admission and
waiting; `TurnTimeoutError` retains the admitted `Turn`, or the idempotency key
when admission itself timed out, so callers can recover without starting
duplicate work.

## Inline behavior

`inline()` is local and makes no request until execution:

```python
from nvoken import Behavior, InlineMemory

classifier = client.inline(Behavior(
    instructions="Classify the request as sales, support, or billing.",
    model="anthropic/claude-sonnet-5",
    output_schema={
        "type": "object",
        "properties": {"queue": {"type": "string"}},
        "required": ["queue"],
        "additionalProperties": False,
    },
))

result = await classifier.run(
    "I need a copy of last month's invoice",
    tenant="acme",
    memory=InlineMemory.none(),
)
```

Inline tenant or user memory requires an explicit namespace. Anonymous Turns
are memoryless.

## Host tools

Tool contracts belong to behavior. `bind_tools()` attaches only process-local
handlers by exact name and returns a new wrapper; it does not change durable
behavior or the idempotency fingerprint.

```python
async def lookup_property(arguments, context):
    return {"address": arguments["address"], "status": "active"}

ready = analyst.bind_tools({"lookup_property": lookup_property})
answer = await ready.text("Check 10 Main St", tenant="acme", user="alice")
```

The handler context carries the exact `turn_id`, `tool_call_id`, and local
cancellation state. Replayed projections do not repeat a handled call; a failed
submission clears the local guard so it can be retried. If a compatible handler
is absent, the durable Turn remains waiting for another process.

## Conversation binding and Turn recovery

```python
conversation = analyst.conversation(
    ConversationRef.by_key("deal-42", owner="tenant"),
    tenant="acme",
    user="alice",
    memory=Memory.tenant("deal-team"),
)
answer = await conversation.text("What changed since yesterday?")

# Synchronous: no request until status/result/updates is used.
recovered = client.turn(saved_turn_id, tenant="acme", user="alice")
result = await recovered.bind_tools({"lookup_property": lookup_property}).result()
```

## Agent lifecycle and exact HTTP access

```python
from nvoken import Behavior

agent = await client.agents.create(
    "real-estate-analyst",
    name="Real Estate Analyst",
    behavior=Behavior(
        instructions="Analyze properties and local market conditions.",
        model="anthropic/claude-sonnet-5",
    ),
)

revision = await agent.publish(
    Behavior(
        instructions="Analyze properties, market conditions, and financing.",
        model="anthropic/claude-sonnet-5",
    ),
)
```

Create and publish generate idempotency keys when omitted. Pass an explicit
key when the host needs to coordinate retries itself.

`client.raw` exposes the exact generated OpenAPI APIs, including `agents`,
`conversations`, `memory_spaces`, and `turns`, for management or wire-level
operations the facade does not simplify.
