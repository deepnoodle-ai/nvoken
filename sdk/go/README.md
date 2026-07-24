# nvoken Go SDK

An Invocation is one durable agent turn. The host supplies `agent_key`,
optional `tenant_key`, `session_key`, and `idempotency_key`; instructions,
model, and tools travel inline with the turn.

The package has three deliberate levels:

- `Agent` is the ordinary workflow facade: `Text`, `Run`, `Invoke`, `Stream`,
  and locally serialized bound Sessions.
- `Client` and `InvocationHandle` expose durable operations, facade-owned
  collection types, transcript drains, configurable waits, and resumable
  streams.
- `Client.Raw()` is the complete generated Runtime transport and low-level
  escape hatch.

```bash
go get github.com/deepnoodle-ai/nvoken/sdk/go
NVOKEN_BASE_URL=http://localhost:8080 NVOKEN_API_KEY=... \
  go run ./examples/quickstart
```

The SDK is a separate Go module and does not bring the daemon's database,
provider, or deployment dependencies into host applications.

Use an Agent for the common path:

```go
agent, err := client.Agent(nvoken.AgentOptions{
	AgentKey: "support",
	Spec: nvoken.ExecutionSpec{
		Instructions: "Help with billing questions.",
		Model: nvoken.Model{
			Provider: "anthropic",
			ID:       "claude-sonnet-5",
		},
	},
})
answer, err := agent.Text(ctx, "Why was I charged twice?", nvoken.AgentInvocationOptions{})
```

`ToolModeHost` tools may carry a local `ToolHandler`; the Agent automatically
executes parked calls and submits results. A missing handler cancels before
returning `MissingToolHandlerError` by default. Set
`LeaveWaitingOnMissingHandler` only when another worker deliberately owns the
call. `NoOutputTextError.ResultKind` distinguishes structured, tool-only, and
empty completions. `DecodeStructuredOutput[T]` decodes an `AgentResult` while
`AgentResult.StructuredOutput` keeps the raw JSON.

A bound Session serializes admission only within the local client:

```go
session, err := agent.Session(nvoken.SessionBinding{SessionKey: "customer-123"})
answer, err = session.Text(ctx, "What should I do next?", nvoken.AgentInvocationOptions{})
```

The Runtime remains authoritative across processes and rejects a second
nonterminal turn. Context cancellation or `WaitOptions.Timeout` stops only the
local operation; use `handle.Cancel` for durable cancellation.

Select a stored or one-turn credential source without dropping to generated
types:

```go
request.ProviderCredentials = []nvoken.ProviderCredentialSelection{{
	Provider: "openai",
	Source:   nvoken.ProviderCredentialCallerEphemeral,
	APIKey:   providerKey,
}}
```

Use `ProviderCredentialAccountBYOK`, `ProviderCredentialTenantBYOK`, or
`ProviderCredentialPlatform` for nonsecret stored selections.

Discover models through the same facade:

```go
catalog, err := client.ListModels(ctx, nvoken.ListModelsOptions{})
selected, err := client.GetModel(ctx, nvoken.Model{
	Provider: "openai",
	ID:       catalog.Items[0].ID,
})
```

`ListModels` returns nvoken's curated catalog; `GetModel` also tolerantly
inspects uncataloged exact IDs. Catalog membership does not prove provider
account access.
