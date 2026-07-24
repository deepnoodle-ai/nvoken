# nvoken Rust SDK

An Invocation is one durable agent turn. The host supplies `agent_key`,
optional `tenant_key`, `session_key`, and `idempotency_key`; instructions,
model, and tools travel inline with the turn.

The supported handwritten level is transport plus durable handle. It is not an
Agent facade:

- `Client` covers Invocation admission, explicit resource reads and lists,
  model discovery, per-turn credential selection, and ToolCall submission.
- `InvocationHandle` covers refresh, configurable terminal/actionable waits,
  composed results, cancellation, and resumable Invocation SSE. Streaming and
  ToolCall submission use shared borrows, so a consumer can act while its
  stream is alive.
- `nvoken::apis` and `nvoken::models` are the complete generated Runtime
  transport and raw escape hatch.

Callback verification failures use the typed `CallbackError` enum.

```bash
cargo add nvoken
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  cargo run --example quickstart
```

Set `InvokeRequest::provider_credentials` to choose a one-turn or stored
credential without using generated transport types:

```rust
request.provider_credentials = vec![ProviderCredentialSelection {
    provider: "openai".to_owned(),
    source: ProviderCredentialSource::CallerEphemeral {
        api_key: provider_key,
    },
}];
```

The other source variants are `AccountByok`, `TenantByok`, and `Platform`.
`Model::new`, `ExecutionSpec` builders, `Tool::host` / `Tool::callback`, and
`InvokeRequest` builders cover the core admission path without generated
constructors.

`WaitOptions` configures the condition, overall local timeout, and polling
cadence. Dropping or timing out a future is local only; call `cancel` for a
durable cancellation.

Remaining handwritten gaps are explicit: Rust has no Agent verbs, bound
Session serialization, or automatic host-tool dispatch. Session SSE,
transcript draining, and provider-credential lifecycle operations remain
available through the generated APIs. Hosts implement the manual durable loop
with `wait_for_action`, `submit_tool_results`, and `wait_for_result`.

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

## Remote MCP tools

The durable-handle facade also covers server declarations and stateless
discovery:

```rust
let server = McpServer::new("support", "https://mcp.example.com/rpc")
    .allowed_tool("lookup_order")
    .header("Authorization", format!("Bearer {mcp_token}"));

let catalog = client.list_mcp_tools(&server).await?;
let spec = ExecutionSpec::new(Model::new("anthropic", "claude-sonnet-5"))
    .mcp_server(server);
```

Headers are one-Invocation secret material and never appear in durable specs or
public recovery surfaces.
