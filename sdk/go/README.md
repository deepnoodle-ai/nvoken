# nvoken Go SDK

An Invocation is one durable agent turn. The host supplies `agent_key`,
optional `tenant_key`, `session_key`, and `idempotency_key`; instructions,
model, and tools travel with the turn as an `AgentDefinition`, either inline or
referenced by a reusable `AgentDefinitionID`.

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

Resolve the identity-only Agent anchor without admitting work:

```go
key := "support"
agents, err := client.ListAgents(ctx, nvoken.ListAgentsOptions{AgentKey: &key})
identity, err := client.GetAgent(ctx, agents.Items[0].ID)
```

`AgentIdentity` contains only the nvoken ID, host-owned key, and creation time.
Instructions, models, tools, and provider keys remain per Invocation.

Opt into the fixed guarded public-web reader with `nvoken.FetchTool()`:

```go
request := nvoken.InvokeRequest{
	AgentKey: "public-summary",
	Input:    "Summarize the supplied URL.",
	AgentDefinition: &nvoken.AgentDefinition{
		Model: nvoken.Model{
			Provider: "anthropic",
			ID:       "claude-sonnet-5",
		},
		Tools: []nvoken.Tool{nvoken.FetchTool()},
	},
}
```

The Runtime accepts only `{"name":"nvoken_fetch","mode":"builtin"}`. It owns
public-address checks, up to five guarded redirects, one transient retry,
HTML-to-Markdown conversion, and the ten-second and 64 KiB limits.

Use an Agent for the common path:

```go
agent, err := client.Agent(nvoken.AgentOptions{
	AgentKey:     "support",
	Instructions: "Help with billing questions.",
	Model: nvoken.Model{
		Provider: "anthropic",
		ID:       "claude-sonnet-5",
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

A `ToolModeCallback` tool runs on an HTTPS endpoint nvoken posts to. Verify the
signed delivery with `nvoken.VerifyCallback`, then answer with one of two
replies. `nvoken.CallbackResult(content, isError)` settles the ToolCall inline
and the turn resumes as soon as nvoken records the reply.
`nvoken.AcknowledgeCallback()` returns `202` with no body instead: it accepts
the delivery without settling the call, for work that will outlive the App's
callback reply deadline. Settle it later with `client.SubmitToolResults`,
reusing the delivery's ToolCall ID.

Acknowledging trades away the fail-loud guarantee. nvoken marks an
unacknowledged delivery failed once its retries are exhausted, so the turn
always moves on. An acknowledged call instead waits under your responsibility,
bounded only by the Invocation's `Limits.WaitingTimeoutSeconds`. Acknowledge
only when something durable will settle the call. Such a call appears in a
waiting Invocation's pending calls the same way a host call does; an `Agent`
skips the callback tools its own definition declares rather than dispatching
them locally.

A bound Session serializes admission only within the local client:

```go
session, err := agent.Session(nvoken.SessionBinding{SessionKey: "customer-123"})
answer, err = session.Text(ctx, "What should I do next?", nvoken.AgentInvocationOptions{})
```

The Runtime remains authoritative across processes and rejects a second
nonterminal turn. Context cancellation or `WaitOptions.Timeout` stops only the
local operation; use `handle.Cancel` for durable cancellation.

Recovery reads can select several states at once and streams can omit
provisional previews:

```go
page, err := client.ListInvocations(ctx, nvoken.ListInvocationsOptions{
	Statuses: []nvoken.InvocationStatus{
		nvoken.InvocationQueued,
		nvoken.InvocationRunning,
		nvoken.InvocationWaiting,
	},
})
deltas := false
err = handle.StreamWithOptions(ctx, nvoken.StreamOptions{Deltas: &deltas}, consume)
```

Equivalent status sets share cursor identity regardless of input order.
Session get/list values expose typed nullable `Usage`, computed from durable
Invocation usage; it is an estimate, not a billing ledger.

For an intentional replace/regenerate action, use a new idempotency key and
the typed policy:

```go
handle, err := agent.Invoke(ctx, "Try that answer again.", nvoken.AgentInvocationOptions{
	IdempotencyKey: "customer-123:regenerate-2",
	IfActive:       nvoken.IfActiveSupersede,
})
```

Omission or `IfActiveReject` preserves the default conflict response.
Low-level callers set the same policy on `InvokeRequest.IfActive`.

`IfActiveInterrupt` is the keep-the-work variant: the active Invocation stops
at its next execution seam and settles `completed` with `StopReason`
`interrupted`, so the replacement turn builds on what it already produced.
`handle.Interrupt(ctx)` asks for the same graceful stop without admitting a
replacement, and `Invocation.StopReason` names why any turn ended.

A turn can also stop without ending: `InvocationIncomplete` means the Runtime
enforced a budget at a seam, with `StopReason` naming which one. It is
terminal — the wait helpers stop there — and its work is kept, so treat it as
an unfinished answer rather than an error. `SessionMessage.Phase` says which
assistant message was the reply: `MessagePhaseFinalAnswer` on the one that
ended a completed turn, `MessagePhaseCommentary` on everything else, so an
incomplete turn has none.

Select a stored or one-turn provider-key source without dropping to generated
types:

```go
request.ProviderKeys = []nvoken.ProviderKeySelection{{
	Provider: "openai",
	Source:   nvoken.ProviderKeyCallerEphemeral,
	APIKey:   providerKey,
}}
```

Use `ProviderKeyAppBYOK`, `ProviderKeyTenantBYOK`, or
`ProviderKeyPlatform` for nonsecret stored selections.

Manage nvoken API credentials with an Operator key through the same client:

```go
issued, err := client.CreateCredential(ctx, nvoken.CreateCredentialInput{
    Name:           "production worker",
    Profile:        nvoken.CredentialProfileRuntime,
    Operations:     []nvoken.RuntimeOperation{nvoken.OperationCreateInvocation},
    IdempotencyKey: "production-worker-v1",
})
page, err := client.ListCredentials(ctx, nvoken.ListCredentialsOptions{})
```

`GetCurrentIdentity`, `GetCredential`, `RotateCredential`, and
`RevokeCredential` complete the lifecycle. Create and rotate return the secret
only through `CredentialIssuance`, alongside `DeliveryExpiresAt` and `Replayed`;
store it before the delivery deadline. `Raw()` exposes the generated transport
when the facade is intentionally too narrow.

## Structured-output schema preflight

`Client.Invoke` and Agent operations call
`nvoken.PreflightOutputSchema(schema)` before transport when
`InvokeRequest.AgentDefinition.OutputSchema` is present. Rejection is an `*nvoken.Error` with
code `schema_preflight_failed`; `Details` contain the portable issue `code`,
RFC 6901 `path`, and optional `keyword`. A successful local check means
eligible for admission. `Client.Raw()` remains the exact-wire escape hatch and
relies on the authoritative Runtime check.

## Context compaction

Install restart-stable Session compaction on a new or existing Session:

```go
options.SessionKey = &sessionKey
options.SessionOptions = &nvoken.SessionOptions{
	Compaction: &nvoken.ContextCompaction{
		TriggerTokens: nvoken.AutoContextCompaction(),
	},
}
```

Use `nvoken.ContextCompactionAt(32768)` for an exact trigger and optionally set
a same-provider summary model. A Session without a policy accepts late opt-in;
once installed, the policy is immutable. Supplied options on an existing
Session must equal stored values or admission returns
`session_options_conflict`.

Summary usage appears in Session usage, not Invocation usage; transcript,
stream, and result reads remain canonical. Read applied and fell-through pass
diagnostics with `client.ListSessionCompactions(ctx, sessionID, options)`.

`AgentInvocationOptions.Metadata` correlates a turn with your own records. It is
part of the admitted input, so it is immutable and material to idempotency: a
replay carrying different metadata conflicts rather than updating it. That is
why it is per-call rather than an `AgentOptions` default.

## Reuse an Agent Definition

Sending the definition inline is the ordinary path. Register it instead when
many turns share one configuration and you would rather send a short ID:

```go
resource, err := client.CreateAgentDefinition(ctx, nvoken.CreateAgentDefinitionInput{
	IdempotencyKey: "support-definition-v1",
	Definition: nvoken.AgentDefinition{
		Instructions: "Help with billing questions.",
		Model: nvoken.Model{
			Provider: "anthropic",
			ID:       "claude-sonnet-5",
		},
	},
})

handle, err := client.Invoke(ctx, nvoken.InvokeRequest{
	AgentKey:          "support",
	Input:             "Why was I charged twice?",
	AgentDefinitionID: resource.ID,
})
```

Creating a definition starts no turn and creates no Agent, Session, or message.
The resource has a stable ID and an increasing revision. Read it with
`GetAgentDefinition`; replace its configuration with `UpdateAgentDefinition`
and the revision you last read. An idempotency key makes create retries safe,
while equal content under another key creates an independent resource.

Supply exactly one of `AgentDefinition` and `AgentDefinitionID`; the facade
rejects a request carrying both or neither before it reaches the network.
`AgentOptions` supports the same choice. Host tool handlers remain local even
when the server resolves the matching declarations from a reusable resource.

## Record changing application state

Keep `Instructions` static. Product state that changes between turns — a board
snapshot, customer facts, the current policy — belongs in `Context`:

```go
sessionKey := "ticket-483"
answer, err := agent.Text(ctx, "Can I refund the duplicate charge?", nvoken.AgentInvocationOptions{
	SessionKey: &sessionKey,
	Context: []nvoken.ContextItem{
		{Name: "customer", Tier: nvoken.ContextTierContextual, Content: "plan: pro"},
		{Name: "refund-policy", Tier: nvoken.ContextTierOperator, Content: "Self-serve refunds cap at 50 USD"},
	},
})
```

A name is a stable identity. Send it once and nvoken records it as a leading
message the model reads as `app-customer`; omit that reserved prefix here.
Send the same name again only when its value changes — a byte-identical resend
is accepted but adds no message, so a stateless host may resend its whole
snapshot every turn and get the same transcript as a host that tracks changes.

Use `ContextTierContextual` for conversation-adjacent facts and
`ContextTierOperator` for policy or other application-authoritative state.
Context is Session history, not an Agent Definition field: it never changes
`AgentDefinitionID`, and later turns keep sending it to the model even when you
omit it. That is what keeps the prompt prefix stable enough for provider
caching, which rewriting the same state into `Instructions` would break on every
turn.

The list is order-sensitive and part of idempotency, so a replay that reorders
or edits an item conflicts rather than updating it. A request accepts at most
eight items, 8 KiB per item, and 16 KiB in total; the SDK checks all three
before the request leaves the process. A Session may accumulate at most 16
distinct names, which only the service can check. Retire a name by superseding
it with a short current value such as `"ticket: closed"`.

## Remote MCP tools

Probe the exact projected catalog, then reuse the same declaration on an
Invocation:

```go
server := nvoken.MCPServer{
	Name:         "support",
	URL:          "https://mcp.example.com/rpc",
	AllowedTools: []string{"lookup_order"},
}
headers := map[string]string{"Authorization": "Bearer " + mcpToken}
catalog, err := client.ListMCPTools(ctx, server, headers)

request.AgentDefinition.MCPServers = []nvoken.MCPServer{server}
request.MCPServerHeaders = []nvoken.MCPServerHeaders{{
	Name:    "support",
	Headers: headers,
}}
handle, err := client.Invoke(ctx, request)
```

The declaration carries no secrets. An Agent Definition may be reused across
turns, so authentication headers travel per Invocation in
`MCPServerHeaders`, keyed to the server name. They are one-Invocation secret
material and never appear in durable Agent Definitions or public reads. The
[recovery example](examples/mcp-recovery/README.md) exercises discovery,
executor replacement, composed result recovery, and fixed-cut transcript
recovery.

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

Set an explicit portable temperature on the request:

```go
request.AgentDefinition.Sampling = &nvoken.Sampling{Temperature: 0}
```

Omit `Sampling` to preserve the provider default. Before setting it, check
`selected.Controls.Sampling.Temperature`; absent controls are unknown and an
unsupported or unknown selection is rejected before durable admission.
Temperature is limited to `[0,1]`. `top_p` and stop sequences are intentionally
absent, while `Limits.MaxOutputTokens` remains the output guardrail.

Reasoning is also typed and fail closed:

```go
effort := nvoken.ReasoningEffortHigh
request.AgentDefinition.Reasoning = &nvoken.Reasoning{Effort: &effort}
```

Check `selected.Controls.Reasoning` before admission. A manual
`BudgetTokens` requires a larger explicit `Limits.MaxOutputTokens`. Omission
preserves provider defaults; unsupported values and combinations are rejected
without aliasing. OpenAI reasoning is intentionally unavailable until its
complete continuation representation is durable.
