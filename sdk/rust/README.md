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

List or read the full Agent instance without admitting work:

```rust
let agents = client
    .list_agents(ListAgentsOptions {
        agent_key: Some("support".to_owned()),
        ..Default::default()
    })
    .await?;
let instance = client.get_agent(&agents.items[0].id).await?;
```

The Agent records its tenant, key, display name, Definition binding, optional
revision pin, lifecycle timestamps, and archive state.

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

Install restart-stable compaction on a new or existing Session:

```rust
let request = InvokeRequest::new("support", "hello")
    .session_key("support:123")
    .session_options(SessionOptions {
        compaction: ContextCompaction {
            trigger_tokens: ContextCompactionTrigger::Auto,
            model: None,
        },
        ..Default::default()
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
    .session_key("customer-123")
    .idempotency_key("customer-123:regenerate-2")
    .if_active(IfActivePolicy::Supersede);
let handle = client.invoke(request).await?;
```

Supersession atomically cancels active work and admits the successor; the
Runtime credential must also allow cancellation.

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
let session = agent.session(SessionBinding::by_key("customer-123"))?;
let answer = session
    .text("What should I do next?", AgentInvocationOptions::default())
    .await?;
```

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
    .instructions("Help with billing questions.");
let resource = client
    .create_agent_definition(
        "support-definition-v1",
        "support",
        "Support",
        definition,
    )
    .await?;

let instance = client
    .create_agent(CreateAgentInput {
        tenant_key: None,
        agent_key: "support".to_owned(),
        name: "Support".to_owned(),
        agent_definition_id: resource.id,
        pinned_revision: None,
    })
    .await?;
let request = InvokeRequest::from_agent_id(instance.id, "Why was I charged twice?");
```

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
let answer = agent
    .text(
        "Can I refund the duplicate charge?",
        AgentInvocationOptions {
            session_key: Some("ticket-483".to_owned()),
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
`agent_definition_id`, and later turns keep sending it to the model even when
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
