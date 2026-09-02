# nvoken Rust SDK

The Rust SDK provides an idiomatic facade over nvoken's durable Agent runtime.
Reusable behavior is an `Agent`; one execution is a `Turn`; continuity is an
explicit `Conversation`; memory is selected independently.

```toml
[dependencies]
nvoken = "0.29"
```

## Run a stored Agent

`Client::agent` is awaited because it performs an exact key lookup and fails
immediately if the Agent is absent. `None` selects the App-owned namespace.

```rust,no_run
use nvoken::{Client, TurnOptions};

# async fn example() -> Result<(), Box<dyn std::error::Error>> {
let client = Client::new(std::env::var("NVOKEN_API_KEY")?);
let analyst = client.agent("real-estate-analyst", None).await?;
let answer = analyst
    .text(
        "Compare these two listings",
        TurnOptions::new("acme").user("alice"),
    )
    .await?;
# Ok(()) }
```

Tenant- and user-owned Agent keys are explicit:

```rust,no_run
# use nvoken::{Client, OwnedBy};
# async fn example(client: Client) -> Result<(), nvoken::NvokenError> {
let custom_owner = OwnedBy::tenant("acme");
let custom = client.agent("assistant", Some(&custom_owner)).await?;

let personal_owner = OwnedBy::user("acme", "alice");
let personal = client.agent("assistant", Some(&personal_owner)).await?;
# Ok(()) }
```

The Agent owner, Turn actor, Conversation owner, and memory scope remain
independent. Every execution explicitly states its tenant.

```rust,no_run
# use nvoken::{Client, ConversationOwner, ConversationRef, Memory, TurnOptions};
# async fn example(client: Client) -> Result<(), nvoken::NvokenError> {
# let analyst = client.agent("analyst", None).await?;
let turn = analyst
    .start(
        "Analyze the offer",
        TurnOptions::new("acme")
            .user("alice")
            .memory(Memory::tenant("deal-team"))
            .conversation(ConversationRef::by_key(
                "deal-42",
                ConversationOwner::User,
            )),
    )
    .await?;

let result = turn.result(None).await?;
# Ok(()) }
```

`start` returns after durable admission. `run` is `start` followed by
`Turn::result`; `text` additionally requires text output. Dropping a result
future or update stream detaches without cancelling durable work. Set a local
deadline with `TurnOptions::new("acme").timeout(Duration::from_secs(60))`;
`TurnTimeoutError` retains the admitted `Turn`, or the idempotency key when
admission itself timed out, so callers can recover without duplicate work.

## Inline behavior and host tools

`Client::inline` is local. Tool contracts belong to behavior; `bind_tools`
attaches only process-local implementations and returns a new wrapper.

```rust,no_run
# use nvoken::{Behavior, Client, InlineTurnOptions, Tool};
# use serde_json::json;
# async fn example(client: Client) -> Result<(), nvoken::NvokenError> {
let behavior = Behavior::new("Look up properties when needed.", "anthropic/claude-sonnet-5")
    .host_tool(
        "lookup_property",
        "Find a property by address",
        json!({
            "type": "object",
            "properties": {"address": {"type": "string"}},
            "required": ["address"],
            "additionalProperties": false
        }),
    )?;

let ready = client.inline(behavior).bind_tools([Tool::new(
    "lookup_property",
    |arguments, _context| async move {
        Ok(json!({"address": arguments["address"], "status": "active"}))
    },
)])?;

let answer = ready
    .text("Check 10 Main St", InlineTurnOptions::new("acme"))
    .await?;
# Ok(()) }
```

Handler names must match the admitted behavior exactly. They are never sent as
behavior overrides or included in the Turn's idempotency fingerprint. Handlers
receive `ToolContext` with the exact Turn and ToolCall IDs. Replayed projections
do not repeat a handled call; failed submission or future cancellation clears
the local guard so it can be retried. A Turn with no compatible handler remains
durably waiting for another process.

## Conversation binding and Turn recovery

```rust,no_run
# use nvoken::{Client, ConversationContext, ConversationOwner, ConversationRef, ConversationTurnOptions};
# async fn example(client: Client, saved_turn_id: String) -> Result<(), nvoken::NvokenError> {
# let analyst = client.agent("analyst", None).await?;
let conversation = analyst.conversation(
    ConversationRef::by_key("deal-42", ConversationOwner::Tenant),
    ConversationContext::new("acme").user("alice"),
);
let answer = conversation
    .text("What changed since yesterday?", ConversationTurnOptions::default())
    .await?;

// Synchronous: no request until status/result/updates is used.
let mut recovered = client.turn(saved_turn_id, "acme", Some("alice".into()));
// Stop a running Turn and keep what it produced.
let stopping = recovered.interrupt().await?;
let result = recovered.result(None).await?;
# Ok(()) }
```

`interrupt` returns the Turn's state as of the request, which is often still
running: mid-step the runtime records the request and stops at the next
checkpoint. Follow `updates` or `result` for settlement rather than reading that
status as final. Interrupting a Turn that already ended returns it unchanged and
is not an error.

## Agent lifecycle and exact OpenAPI access

```rust,no_run
# use nvoken::{Behavior, Client};
# async fn example(client: Client) -> Result<(), nvoken::NvokenError> {
let agent = client.agents().create(
    "real-estate-analyst",
    Some("Real Estate Analyst"),
    Behavior::new(
        "Analyze properties and local market conditions.",
        "anthropic/claude-sonnet-5",
    ),
    None,
    None,
).await?;

let revision = agent.publish(
    Behavior::new(
        "Analyze properties, market conditions, and financing.",
        "anthropic/claude-sonnet-5",
    ),
    None,
).await?;
# Ok(()) }
```

Create and publish generate idempotency keys when the final argument is `None`;
pass `Some("your-key")` when the host needs to coordinate retries itself.

`client.raw()` returns a discoverable `RawClient` door. Pass
`client.raw().configuration()` to
functions in `nvoken::apis::{agents_api, conversations_api,
memory_spaces_api, turns_api}` for wire-level operations the facade does not
simplify.
