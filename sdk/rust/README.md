# nvoken Rust SDK

An Invocation is one durable turn by a deliberately created, tenant-scoped
Agent. An Agent binds your `agent_key` to one App-owned, versioned Agent
Definition; a Session is one conversation with that Agent.

The handwritten level covers both transport plus durable handle, and a
high-level Agent facade on top of it:

- `Client` covers Invocation admission, explicit resource reads and lists,
  model discovery, per-turn provider-key selection, and ToolCall submission.
- `InvocationHandle` covers refresh, configurable terminal/actionable waits,
  composed results, cancellation, and resumable Invocation SSE. Streaming and
  ToolCall submission use shared borrows, so a consumer can act while its
  stream is alive.
- `Agent` (from `client.agent(...)`) fixes one identity and flat execution controls
  and exposes `text`/`run`/`invoke`/`stream`/`session`, a host-tool dispatch
  loop, and structured-output access, matching the Go and Python bindings.
- `nvoken::apis` and `nvoken::models` are the complete generated Runtime
  transport and raw escape hatch.

Callback verification failures use the typed `CallbackError` enum.

```bash
cargo add nvoken
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  cargo run --example quickstart
```

List or read the Agent record without admitting work:

```rust
let agents = client
    .list_agents(ListAgentsOptions {
        agent_key: Some("support".to_owned()),
        ..Default::default()
    })
    .await?;
let record = client.get_agent(&agents.items[0].id).await?;
```

`models::Agent` is that record: tenant, key, display name, Definition binding,
optional revision pin, lifecycle timestamps, and archive state. `Agent` is the
object that runs its turns, and it corresponds to the same row — declare one
from the keys you already own and it creates its record on first use:

```rust
let agent = client.agent(AgentOptions::declared("support", "support"))?;
```

An Agent's identity and configuration live on the server; its tool handlers are
supplied by whichever process runs the turn. `agent.ensure().await?` creates the
record at a moment you choose instead of on first use, and never mutates: the
same keys and Definition resolve onto what exists, a different Definition is
`agent_key_conflict`, an archived record is `agent_archived`, and a declared
`pinned_revision` the record does not follow is refused. `agent.resource()` and
`agent.id()` report the record once it is known.

Opt into the fixed guarded public-web reader with `fetch_tool()`:

```rust
// The deliberately created research Agent's Definition declares fetch_tool().
let request = InvokeRequest::new("research", "Summarize the URL.");
```

The Runtime accepts only `{"name":"nvoken_fetch","mode":"builtin"}`. It owns
public-address checks, up to five guarded redirects, one transient retry,
HTML-to-Markdown conversion, and the ten-second and 64 KiB limits.

Set `InvokeRequest::provider_keys` to choose a one-turn or stored
provider key without using generated transport types:

```rust
request.provider_keys = vec![ProviderKeySelection {
    provider: "openai".to_owned(),
    source: ProviderKeySource::CallerEphemeral {
        api_key: provider_key,
    },
}];
```

The other source variants are `InstallationByok`, `TenantByok`, and `Platform`.
`Model::new`, `InvokeRequest` builders, `fetch_tool`, and `Tool::host` /
`Tool::callback` cover Definition setup and the core admission path
without generated constructors.

`WaitOptions` configures the condition, overall local timeout, and polling
cadence. Dropping or timing out a future is local only; call `cancel` for a
durable cancellation.

Recovery reads accept a status union, and a known Invocation can stream only
durable frames:

```rust
let page = client
    .list_invocations(ListInvocationsOptions {
        statuses: vec![
            InvocationStatus::Queued,
            InvocationStatus::Running,
            InvocationStatus::Waiting,
        ],
        ..Default::default()
    })
    .await?;
let stream = handle.stream_with_options(StreamOptions {
    deltas: Some(false),
});
```

Equivalent status sets share cursor identity regardless of input order.
Session get/list models expose typed optional usage, computed from durable
Invocation usage as a convenience estimate rather than a billing ledger.

### Child Invocations

Record the exact ToolCall that caused another turn. A one-off child can stay
standalone and expire automatically:

```rust
use nvoken::{models, InvokeRequest};

let mut child = InvokeRequest::new("researcher", "Investigate this branch");
child.triggered_by = Some(models::InvocationTrigger::new(
    models::invocation_trigger::Type::ToolCall,
    parent.id,
    call.id,
));
child.retention = Some(nvoken::SessionRetention { ttl_seconds: 3600 });
```

Set `parent_invocation_id` on `ListInvocationsOptions` to an Invocation ID for
its direct children, to the literal `"null"` for top-level Invocations, or
leave it absent for the unfiltered collection. Lineage does not propagate
cancellation, budgets, results, or lifecycle state.

Install restart-stable compaction on a new or existing Session:

```rust
let request = InvokeRequest::new("support", "hello")
    .session(InvocationSession::continue_or_create("support:123"))
    .compaction(ContextCompaction {
        trigger_tokens: ContextCompactionTrigger::Auto,
        model: None,
    });
```

`ContextCompactionTrigger::Tokens(32768)` selects an exact trigger. An optional
summary model must use the primary provider. A Session without a policy accepts
late opt-in; once installed, the policy is immutable. Supplied options on an
existing Session must equal stored values or admission returns
`session_options_conflict`.

Summary usage appears in Session usage rather than Invocation usage. Read
applied and fell-through diagnostics with
`client.list_session_compactions(session_id, options)`.

A Session still admits one nonterminal Invocation at a time. Omission preserves
the reject default. For an intentional replace/regenerate action, use a new
idempotency key and the typed builder:

```rust
let request = InvokeRequest::new("support", "Try that answer again.")
    .session(
        InvocationSession::continue_or_create("customer-123")
            .if_active(IfActivePolicy::Supersede),
    )
    .idempotency_key("customer-123:regenerate-2");
let handle = client.invoke(request).await?;
```

Supersession atomically cancels active work and admits the successor; a full
App key is required because read-only App keys cannot cancel work.

`IfActivePolicy::Interrupt` is the keep-the-work variant: the active Invocation
stops at its next execution seam and settles `Completed` with `stop_reason`
`Interrupted`, so the replacement turn builds on what it already produced.
`handle.interrupt()` asks for the same graceful stop without admitting a
replacement, and `Invocation::stop_reason` names why any turn ended.

A turn can also stop without ending: `InvocationStatus::Incomplete` means the
Runtime enforced a budget at a seam, with `stop_reason` naming which one. It is
terminal — the wait helpers stop there — and its work is kept, so treat it as
an unfinished answer rather than an error. `SessionMessage::phase` says which
assistant message was the reply: `MessagePhase::FinalAnswer` on the one that
ended a completed turn, `MessagePhase::Commentary` on everything else, so an
incomplete turn has none.

`Agent` fixes one identity and execution controls, and admits through it:

```rust
let agent = client.agent(AgentOptions::new("support"))?;
let answer = agent
    .text("Why was I charged twice?", AgentInvocationOptions::default())
    .await?;
```

A `Tool::host(...)` may carry a `.handler(...)` closure; the Agent
automatically dispatches parked calls and submits results. A missing handler
cancels the Invocation before returning an error coded
`missing_tool_handler`; set `leave_waiting_on_missing_handler` only when
another worker deliberately owns the call. `AgentResult::structured_output`
holds the decoded value, and `AgentResult::raw` keeps the full
`InvocationResult`.

A bound Session serializes admission only within the local client:

```rust
let session = agent.bind_session(
    SessionBinding::by_key("customer-123"),
    SessionOptions::default(),
)?;
let answer = session
    .text("What should I do next?", AgentInvocationOptions::default())
    .await?;
```

Unbound `start`, `run`, and `text` calls are standalone: they create no
reusable conversation and return `session_id: None`. Their private content is
retained for at least an hour after settlement; read `content_expires_at` for
the scheduled boundary. Bind a Session only when later turns should reuse
conversation history.

For anything the Agent facade does not cover, Session SSE, transcript
draining, and provider-key lifecycle operations remain available
through the generated APIs. Hosts may still implement a manual durable loop
with `wait_for_action`, `submit_tool_results`, and `wait_for_result` directly
on an `InvocationHandle`.

Discover models through the same facade:

```rust
let catalog = client.list_models(ListModelsOptions::default()).await?;
let selected = client
    .get_model(&Model {
        provider: "openai".to_owned(),
        id: catalog.items[0].id.clone(),
    })
    .await?;
```

The list is curated discovery metadata, not proof of provider-account access.
Exact inspection also accepts uncataloged IDs.

Set an explicit portable temperature on the Agent Definition or a safe
per-turn override:

```rust
let request = InvokeRequest::new("support", "hello").overrides(
    AgentDefinitionOverrides::default()
        .model(Model::new("anthropic", "claude-haiku-4-5"))
        .sampling(Sampling { temperature: 0.0 }),
);
```

Omit `sampling` to preserve the provider default. Check
`selected.controls.as_ref().map(|c| c.sampling.temperature)` first; missing
controls are unknown, and unsupported or unknown selections fail before
durable admission. The portable range is `[0,1]`. `top_p` and stop sequences
are intentionally absent; `limits.max_output_tokens` is the output guardrail.

Reasoning is typed and fail closed:

```rust
let request = InvokeRequest::new("support", "hello").overrides(
    AgentDefinitionOverrides::default()
        .model(Model::new("anthropic", "claude-opus-5"))
        .reasoning(Reasoning {
            effort: Some(ReasoningEffort::High),
            budget_tokens: None,
        }),
);
```

Check `selected.controls.as_ref().map(|c| &c.reasoning)` first. A manual
`budget_tokens` requires a larger explicit `limits.max_output_tokens`.
Omission preserves provider defaults; unsupported values and combinations are
rejected without aliasing. OpenAI reasoning remains unavailable until its
complete continuation representation is durable.

## Structured-output schema preflight

`Client::invoke` calls `preflight_output_schema(&schema)` before transport when
the request's `AgentDefinitionOverrides::output_schema` is present. Rejection is an `NvokenError` with
code `schema_preflight_failed`; its safe `details` contain the portable issue
`code`, RFC 6901 `path`, and optional `keyword`. A successful local check means
eligible for admission. Generated APIs reached through `client.raw()` still
rely on the authoritative Runtime check.

## Define and instantiate an Agent

Every turn runs against an App-owned, versioned Agent Definition. Create a
tenant-scoped Agent instance that binds to the template before admitting work:

```rust
let definition = AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
    .definition_key("support")
    .name("Support")
    .instructions("Help with billing questions.");
let resource = client
    .create_agent_definition(definition, CreateAgentDefinitionOptions::default())
    .await?;

let agent = client.agent(AgentOptions::declared("support", resource.definition_key))?;
let handle = agent
    .start("Why was I charged twice?", AgentInvocationOptions::default())
    .await?;
```

`AgentDefinition` is flat and matches the wire, and a read gives back the same
fields plus `id`, `revision`, and timestamps, so a change is a read, an edit,
and a write:

```rust
let current = client.get_agent_definition(&resource.id).await?;
let mut definition = AgentDefinition::from_resource(&current)?;
definition.instructions = Some("Be concise.".to_string());
client
    .update_agent_definition(
        &current.id,
        definition,
        UpdateAgentDefinitionOptions::new(current.revision as u32),
    )
    .await?;
```

An update replaces the whole resource, so send back everything you want kept;
`from_resource` is what keeps it whole. The expected revision travels as
`If-Match`, so a concurrent write fails rather than overwriting.

Both writes are ensure-shaped: restating what nvoken already holds publishes
nothing and returns the current revision. So a deploy step that owns its
definitions in source does not read anything first — `sync_definitions` writes
them all and reports what moved:

```rust
for synced in client.sync_definitions(definitions).await? {
    if synced.outcome != DefinitionSyncOutcome::Unchanged {
        println!("{}: {:?}", synced.definition_key, synced.outcome);
    }
}
```

Each definition costs one call, or two when its contents differ: the create
conflict names the resource to replace, so nothing has to be looked up. Do not
compare a definition against what you read back to decide whether to write —
nvoken canonicalizes one before comparing it, and a second copy of that rule in
your code is free to disagree the first time either side gains a field. Write
unconditionally and read the outcome.

Creating a Definition starts no turn. It has an immutable `definition_key`, a
stable ID, and an increasing revision; `get_agent_definition_revision` reads
historical revisions. Updating a Definition does not rewrite an Agent's
binding. An Agent or Session may pin a revision, while an Invocation may select
one revision for that turn. Safe overrides cover model, sampling, reasoning,
tool choice, limits, and output schema; they cannot expand tools, data access,
memory authority, or instructions. Host tool handlers remain local to the SDK
facade.

## Record changing application state

Keep `instructions` static. Product state that changes between turns — a board
snapshot, customer facts, the current policy — belongs in `context`:

```rust
let conversation = agent.bind_session(
    SessionBinding::by_key("ticket-483"),
    SessionOptions::default(),
)?;
let answer = conversation
    .text(
        "Can I refund the duplicate charge?",
        AgentInvocationOptions {
            context: vec![
                ContextItem::new("customer", ContextTier::Contextual, "plan: pro"),
                ContextItem::new(
                    "refund-policy",
                    ContextTier::Operator,
                    "Self-serve refunds cap at 50 USD",
                ),
            ],
            ..Default::default()
        },
    )
    .await?;
```

A name is a stable identity. Send it once and nvoken records it as a leading
message the model reads as `app-customer`; omit that reserved prefix here.
Send the same name again only when its value changes — a byte-identical resend
is accepted but adds no message, so a stateless host may resend its whole
snapshot every turn and get the same transcript as a host that tracks changes.

Use `ContextTier::Contextual` for conversation-adjacent facts and
`ContextTier::Operator` for policy or other application-authoritative state.
Context is Session history, not an Agent Definition field: it never changes
`definition_id`, and later turns keep sending it to the model even when
you omit it. That is what keeps the prompt prefix stable enough for provider
caching, which rewriting the same state into `instructions` would break on every
turn.

The list is order-sensitive and part of idempotency, so a replay that reorders
or edits an item conflicts rather than updating it. A request accepts at most
eight items, 8 KiB per item, and 16 KiB in total; the SDK checks all three
before the request leaves the process. A Session may accumulate at most 16
distinct names, which only the service can check. Retire a name by superseding
it with a short current value such as `"ticket: closed"`.

## Remote MCP tools

The durable-handle facade also covers server declarations and stateless
discovery:

```rust
let server = McpServer::new("support", "https://mcp.example.com/rpc")
    .allowed_tool("lookup_order");
let headers = HashMap::from([
    ("Authorization".to_owned(), format!("Bearer {mcp_token}")),
]);

let catalog = client.list_mcp_tools(&server, Some(headers.clone())).await?;
// The support Agent's Definition stores `server`; the one-turn request stores
// only the secret headers.
let request = InvokeRequest::new("support", "hello")
    .mcp_server_headers(McpServerHeaders::new("support", headers));
```

The declaration carries no secrets. An Agent Definition may be reused across
turns, so authentication headers travel per Invocation in
`mcp_server_headers`, keyed to the server name. They are one-Invocation secret
material and never appear in durable Agent Definitions or public recovery
surfaces.

## Callback tools

A `Tool::callback(...)` runs on an HTTPS endpoint nvoken posts to. Verify the
signed delivery with `verify_callback`, then answer with one of two replies.
`callback_result(content, is_error)` settles the ToolCall inline and the turn
resumes as soon as nvoken records the reply. `acknowledge_callback()` returns
`202` with no body instead: it accepts the delivery without settling the call,
for work that will outlive this tool's reply deadline — its declared
`timeout_seconds`, or the App's default when it declares none. Settle it later
with `Client::submit_tool_results`, reusing the delivery's ToolCall id.

Every delivery names its tool inside the signed body, so one endpoint can serve
several tools: dispatch on `VerifiedCallback::tool_name` rather than on a path
suffix nothing signs.

Acknowledging trades away the fail-loud guarantee. nvoken marks an
unacknowledged delivery failed once its retries are exhausted, so the turn
always moves on. An acknowledged call instead waits under your responsibility,
bounded only by the Invocation's `limits.waiting_timeout_seconds`. Acknowledge
only when something durable will settle the call. Such a call appears in a
waiting Invocation's pending calls the same way a host call does, so
`answerable_tool_calls` includes it. `host_tool_calls` is the narrower set you
must run yourself — answerable and `mode` `Host` — and an `Agent` dispatches
exactly that, whatever its own definition declares.

## A callback endpoint, whole

`verify_callback` is the signature. Around it every receiver writes the same key
table, the same deduplication, and the same reply discipline —
`CallbackReceiver` is that, so you write the tools:

```rust
use nvoken::{callback_result, CallbackReceiver, DeliverySigningKey, VerifiedCallback};

let receiver = CallbackReceiver::builder(vec![
    // Two entries span a rotation: nvoken mints the next version while still
    // signing with the current one.
    DeliverySigningKey::new(&key_id, 2, secret),
    DeliverySigningKey::new(&key_id, 1, previous_secret),
])
.tool("open_ticket", |delivery: &VerifiedCallback| {
    let board = delivery.authorization_context.get("board").cloned();
    let input = delivery.envelope.input.clone();
    async move {
        match open_ticket(board, input).await {
            Ok(ticket) => callback_result(ticket, false).map_err(|e| e.to_string()),
            // The tool failed, not the receiver: settle it so the model can
            // read the error and correct itself.
            Err(error) => callback_result(json!({"error": error}), true).map_err(|e| e.to_string()),
        }
    }
})
.store(ticket_replies)
.build()?;

let answered = receiver.handle(request.headers(), &body, SystemTime::now()).await;
tracing::info!(outcome = ?answered.outcome, reason = answered.reason, "nvoken callback");
```

`handle` never returns `Err`. Everything that can go wrong is a status nvoken
understands, and `outcome` — `Settled`, `Acknowledged`, `Replayed`, `Refused`,
`Failed` — is what the status alone cannot tell you, with a stable `reason`
token for the log line.

The statuses are decisions about whether nvoken tries again:

| situation | status |
| --- | --- |
| no keys configured | 503 — an operator error, still fixable in the window |
| signing identity not held | 401 — redelivery reproduces it |
| signature or envelope invalid | 401 — the same bytes fail the same way |
| no handler for the signed tool name | 400 — nothing here can ever run it |
| a tool answered, or failed | 200 — settle it, carrying `is_error` if it failed |
| your handler returned `Err` | 503 — you failed, not the tool |

`store` is a `find`-then-`put_if_absent` pair, in that order. `find` runs before
your tool does, because delivery is at least once and re-running the tool
repeats every effect it had; `put_if_absent` runs after, because two deliveries
of one ToolCall can be in flight at once and only one reply may win. Leave the
store unset only when every tool on the endpoint is safe to run twice.

`build()` validates the key table: a zero version, a secret under 32 bytes, or
the same `(key_id, version)` twice fails at startup rather than refusing a live
delivery, where the refusal would be permanent.

### Authorizing a delivery

Verification proves the delivery came from nvoken. It does not say what the work
belongs to, and the tool input cannot either — the model wrote it.
`SessionOptions::authorization_context` is what you asserted when the Session
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
it. Set `InvokeRequest::webhook` to a `WebhookTarget` when you start the turn.
Omitting `events` selects all three: `invocation.ended` fires once when the turn
reaches a terminal status, `invocation.waiting` when it needs a host tool run,
`invocation.budget_hold` when a spending limit stopped it.

Receiving one is the same verification you already wrote for callbacks — the
signature scheme is identical, and `verify_webhook` is the same code path with a
different body check:

```rust
let delivery = nvoken::verify_webhook(&webhook_signing_key, &headers, &raw_body, SystemTime::now())?;
if delivery.supersedes(last_applied_sequence(&delivery.invocation_id).await?) {
    settle(&delivery.invocation_id, &delivery.envelope.invocation, delivery.sequence).await?;
}
Ok(nvoken::accept_webhook())
```

The key is the App's `webhook`-purpose signing key, not its `callback` key. A
receiver serving both endpoints holds two, and must not try either against the
other's deliveries.

Three rules make a receiver correct:

**Fold by sequence, not by arrival.** Delivery is at least once, so the same
transition can arrive twice and a redelivery can land after a later one. Keep
the highest `sequence` you have applied per Invocation and apply only what
`VerifiedWebhook::supersedes` accepts. That is the deduplication too — a repeat
carries a sequence you already applied — so nothing else is needed to make
handling idempotent.

**The payload is a pointer, not a copy.** `WebhookSubject` carries `status`,
`stop_reason`, `failure_code`, `waiting_tool_call_ids`, and `credit_block`, and
deliberately nothing else: no transcript, no output text, no usage. Read
`Client::get_invocation` or `Client::get_invocation_result` when you need more,
so you are reconciling against the authoritative record rather than a staler
copy of it.

**Answer `retry_webhook()` when you could not record it.** Any 5xx is
redelivered, as are 408, 425, and 429. Every other non-2xx answer is permanent
and that transition is never delivered again, so a 400 from a receiver that was
merely busy is a settlement you silently lost.

Retries are bounded, so webhooks alone are not a settlement guarantee.
`Client::list_ended_invocations` is the backstop: it walks turns in the order
they ended, so a delivery that never landed is one you still find.

`WebhookReceiver` is `CallbackReceiver`'s twin — same key table, same reply
discipline — for the endpoint that has more than one event:

```rust
let receiver = WebhookReceiver::builder(vec![DeliverySigningKey::new(&key_id, 1, secret)])
    .event(WebhookEvent::Ended, settle_in_one_transaction)
    .event(WebhookEvent::BudgetHold, alert_on_budget_hold)
    .build()?;
```

A handler returning `Ok` answers 200; one returning `Err` answers 503 and nvoken
delivers again. An event you subscribed to but registered no handler for answers
200 with outcome `Ignored` — retrying it would only spend nvoken's bounded
attempts reaching the same absent handler.

The sequence fold stays yours, deliberately: the compare and the write have to
happen in the same transaction as the state they guard, and the receiver cannot
open it. Call `delivery.supersedes(applied)` inside yours, and record nothing
when it says no — a superseded delivery is still a delivery, and still answers
200.

## Browser-direct access

Your page can talk to nvoken itself, with no server of yours in the path. Mint
a short-lived grant in backend code and hand the browser that:

```rust
let token = mint_client_token(&client_key_seed, &ClientTokenClaims {
    app_id,
    key_id: client_key_id,
    subject: user.id,          // from your session, never from the request
    tenant_key: Some(user.workspace_id),
    agent_id: None,
    agent_key: Some("support".to_string()),
    definition_revision: None,
    session_id: None,
    issued_at: None,
    lifetime: Duration::from_secs(600),
})?;
```

`nvoken client-key generate <app-id> --name web` produces the keypair and
registers its public half in one step. The private seed is the App's browser
authority — whoever holds it can mint a grant for any end user — so it belongs
in backend configuration and never in a bundle.

Two things are worth deciding rather than defaulting. `session_id` confines the
token to one conversation, which a single-conversation UI should set. A
lifetime is capped at fifteen minutes, because short lifetimes are the whole
safety story of a bearer token in a page. The browser route ceiling is fixed by
nvoken; tokens cannot select or expand it.

**Invocation webhooks stop being optional here.** The browser holds the stream,
so your backend never observes settlement any other way.

## Acting for one tenant or one end user

An app-wide credential can reach every tenant in its App, so an id that arrives
from the wrong place — a stale link, a mixed-up webhook, a tampered form field
— is an id it can act on. Say the scope once instead of re-reading the resource
before every call:

```rust
let tenant = client.scoped(Scope::tenant("acme").user("user-7c1f"))?;
let session = tenant.get_session(&session_id_from_the_request).await?;
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
