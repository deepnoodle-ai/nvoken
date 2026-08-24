# nvoken Go SDK

An Invocation is one durable turn by a deliberately created, tenant-scoped
Agent. An Agent binds your `agent_key` to one App-owned, versioned Agent
Definition; a Session is one conversation with that Agent.

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

List or read the full Agent instance without admitting work:

```go
key := "support"
agents, err := client.ListAgents(ctx, nvoken.ListAgentsOptions{AgentKey: &key})
record, err := client.GetAgent(ctx, agents.Items[0].ID)
```

`AgentResource` is that record: tenant, key, display name, Definition binding,
optional revision pin, lifecycle timestamps, and archive state. `*Agent` is the
object that runs its turns, and it corresponds to the same row — declare one
from the keys you already own and it creates its record on first use:

```go
agent, err := client.Agent(nvoken.AgentOptions{
	TenantKey:     &userID,
	AgentKey:      "support",
	DefinitionKey: "support", // the Definition this instance follows
	Tools:         []nvoken.Tool{lookupOrder},
})
```

An Agent's identity and configuration live on the server; its tool handlers are
supplied by whichever process runs the turn. `Ensure` creates the record at a
moment you choose instead of on first use, and never mutates: the same keys and
Definition resolve onto what exists, a different Definition is
`agent_key_conflict`, an archived record is `agent_archived`, and a declared
`PinnedRevision` the record does not follow is refused. `Resource` and `ID`
report the record once it is known.

Opt into the fixed guarded public-web reader with `nvoken.FetchTool()`:

```go
definition, err := client.CreateAgentDefinition(ctx, nvoken.AgentDefinition{
	DefinitionKey: "public-summary",
	Name:          "Public Summary",
	Model:         nvoken.Model{Provider: "anthropic", ID: "claude-sonnet-5"},
	Tools:         []nvoken.Tool{nvoken.FetchTool()},
}, nvoken.CreateAgentDefinitionOptions{})
agent, err := client.Agent(nvoken.AgentOptions{
	AgentKey:      "public-summary",
	DefinitionKey: definition.DefinitionKey,
})
answer, err := agent.Text(ctx, "Summarize the supplied URL.", nvoken.AgentInvocationOptions{})
```

The Runtime accepts only `{"name":"nvoken_fetch","mode":"builtin"}`. It owns
public-address checks, up to five guarded redirects, one transient retry,
HTML-to-Markdown conversion, and the ten-second and 64 KiB limits.

Use an Agent for the common path:

```go
agent, err := client.Agent(nvoken.AgentOptions{AgentKey: "support"})
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
the delivery without settling the call, for work that will outlive this tool's
reply deadline — its declared `timeout_seconds`, or the App's default when it
declares none. Settle it later with `client.SubmitToolResults`, reusing the
delivery's ToolCall ID.

Every delivery names its tool inside the signed body, so one endpoint can serve
several tools: dispatch on `VerifiedCallback.ToolName` rather than on a path
suffix nothing signs.

Acknowledging trades away the fail-loud guarantee. nvoken marks an
unacknowledged delivery failed once its retries are exhausted, so the turn
always moves on. An acknowledged call instead waits under your responsibility,
bounded only by the Invocation's `Limits.WaitingTimeoutSeconds`. Acknowledge
only when something durable will settle the call. Such a call appears in a
waiting Invocation's pending calls the same way a host call does, so
`AnswerableToolCalls` includes it. `HostToolCalls` is the narrower set you must
run yourself — answerable and `mode` `host` — and an `Agent` dispatches exactly
that, whatever its own definition declares.

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
cursor := "cursor-from-a-previous-process"
err = handle.StreamWithOptions(ctx, nvoken.StreamOptions{
	Deltas: &deltas,
	Cursor: &cursor,
}, consume)

invocationID := handle.InvocationID
err = client.StreamSessionWithOptions(ctx, handle.SessionID, nvoken.StreamOptions{
	Deltas:       &deltas,
	Cursor:       &cursor,
	InvocationID: &invocationID,
}, consumeSession)
```

An Invocation-filtered Session stream exits after that Invocation settles.
Equivalent status sets share cursor identity regardless of input order.
Session get/list values expose typed nullable `Usage`, computed from durable
Invocation usage; it is an estimate, not a billing ledger.

### Child Invocations

When a ToolCall starts another turn, record the exact cause on admission. A
child remains an ordinary Invocation in its own ordinary Session; give that
Session a short retention window when its transcript is temporary:

```go
trigger := &nvoken.InvocationTrigger{
	Type:               "tool_call",
	ParentInvocationID: parent.ID,
	ToolCallID:         call.ID,
}
handle, err := client.Invoke(ctx, nvoken.InvokeRequest{
	AgentKey:   "researcher",
	Input:      "Investigate this branch",
	TriggeredBy: trigger,
	SessionOptions: &nvoken.SessionOptions{
		Retention: &nvoken.SessionRetention{TTLSeconds: 3600},
	},
})
```

Set `ParentInvocationID` on `ListInvocationsOptions` to an Invocation ID for
its direct children, to the literal `"null"` for top-level Invocations, or
leave it nil for the unfiltered collection. Lineage does not propagate
cancellation, budgets, results, or lifecycle state.

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

Manage nvoken API credentials from the console or with an installation-admin
key through the same client:

```go
issued, err := client.CreateCredential(ctx, nvoken.CreateCredentialInput{
    Name:           "production worker",
    Type:           nvoken.CredentialTypeApp,
    AppID:          &appID,
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
`nvoken.PreflightOutputSchema(schema)` before transport when a safe per-turn
`InvokeRequest.Overrides.OutputSchema` is present. Rejection is an `*nvoken.Error` with
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

## Define and instantiate an Agent

An App-owned Agent Definition is a reusable, versioned template. Create a
tenant-scoped Agent instance that binds to it before admitting work:

```go
resource, err := client.CreateAgentDefinition(ctx, nvoken.AgentDefinition{
	DefinitionKey: "support",
	Name:          "Support",
	Instructions:  "Help with billing questions.",
	Model: nvoken.Model{
		Provider: "anthropic",
		ID:       "claude-sonnet-5",
	},
}, nvoken.CreateAgentDefinitionOptions{})

agent, err := client.Agent(nvoken.AgentOptions{
	AgentKey:      "support",
	DefinitionKey: resource.DefinitionKey,
})

handle, err := agent.Invoke(ctx, "Why was I charged twice?", nvoken.AgentInvocationOptions{})
```

`AgentDefinition` is flat and matches the wire, and a read gives back the same
fields plus `ID`, `Revision`, and timestamps, so a change is a read, an edit,
and a write:

```go
current, err := client.GetAgentDefinition(ctx, resource.ID)
definition, err := nvoken.AgentDefinitionFromResource(current)
definition.Instructions = "Be concise."
updated, err := client.UpdateAgentDefinition(ctx, current.ID, definition,
	nvoken.UpdateAgentDefinitionOptions{ExpectedRevision: current.Revision})
```

An update replaces the whole resource, so send back everything you want kept;
`AgentDefinitionFromResource` is what keeps it whole. `ExpectedRevision`
travels as `If-Match`, so a concurrent write fails rather than overwriting.

Both writes are ensure-shaped: restating what nvoken already holds publishes
nothing and returns the current revision. So a deploy step that owns its
definitions in source does not read anything first — `SyncDefinitions` writes
them all and reports what moved:

```go
synced, err := client.SyncDefinitions(ctx, definitions)
for _, one := range synced {
	if one.Outcome != nvoken.DefinitionUnchanged {
		log.Printf("%s: %s", one.DefinitionKey, one.Outcome)
	}
}
```

Each definition costs one call, or two when its contents differ: the create
conflict names the resource to replace, so nothing has to be looked up. Do not
compare a definition against what you read back to decide whether to write —
nvoken canonicalizes one before comparing it, and a second copy of that rule in
your code is free to disagree the first time either side gains a field. Write
unconditionally and read the outcome.

Creating a Definition starts no turn. It has an immutable `DefinitionKey`, a
stable ID, and an increasing revision; `GetAgentDefinitionRevision` reads
historical revisions. Updating a Definition does not rewrite an Agent's
binding. An Agent or Session may pin a revision, while an Invocation may select
one revision for that turn. Safe per-turn overrides cover model, sampling,
reasoning, tool choice, limits, and output schema; they cannot expand tools,
data access, memory authority, or instructions. Host tool handlers remain
local to the SDK facade.

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
`DefinitionID`, and later turns keep sending it to the model even when you
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

## Invocation webhooks

A turn that ends tells you so, without you holding a connection open to hear
it. Set `InvokeRequest.Webhook` when you start the turn; omitting `Events`
selects all three. `invocation.ended` fires once when the turn reaches a
terminal status, `invocation.waiting` when it needs a host tool run, and
`invocation.budget_hold` when a spending limit stopped it.

nvoken signs Invocation webhooks and tool callbacks identically, so
`VerifyWebhook` and `VerifyCallback` are one verification path with different
body checks:

```go
delivery, err := nvoken.VerifyWebhook(webhookSigningKey, r.Header, rawBody, time.Now())
if err != nil {
	// A body that genuinely failed verification is permanent: 401 is right.
	http.Error(w, "unverified", http.StatusUnauthorized)
	return
}
if delivery.Supersedes(lastApplied[delivery.InvocationID]) {
	settle(delivery.InvocationID, delivery.Envelope.Invocation, delivery.Sequence)
}
w.WriteHeader(nvoken.AcceptWebhook().Status)
```

The key is the App's `webhook`-purpose signing key, not its `callback` key. A
receiver serving both endpoints holds two, and must not try either against the
other's deliveries.

Three rules make a receiver correct:

**Fold by sequence, not by arrival.** Delivery is at least once, so the same
transition can arrive twice and a redelivery can land after a later one. Keep
the highest `Sequence` you have applied per Invocation and apply only what
`Supersedes` accepts. That is the deduplication too — a repeat carries a
sequence you already applied — so nothing else is needed to make handling
idempotent.

**The payload is a pointer, not a copy.** `WebhookSubject` carries `Status`,
`StopReason`, `FailureCode`, `WaitingToolCallIDs`, and `CreditBlock`, and
deliberately nothing else: no transcript, no output text, no usage. Read
`GetInvocation` or `GetInvocationResult` when you need more, so you are
reconciling against the authoritative record rather than a staler copy of it.

**Answer `RetryWebhook()` when you could not record it.** Any 5xx is
redelivered, as are 408, 425, and 429 — `WebhookStatusIsRetried` answers for any
status. Every other non-2xx answer is permanent and that transition is never
delivered again, so a 400 from a receiver that was merely busy is a settlement
you silently lost.

Retries are bounded, so webhooks alone are not a settlement guarantee.
`ListEndedInvocations` is the backstop: it walks turns in the order they ended,
so a delivery that never landed is one you still find.

`NewWebhookReceiver` is `NewCallbackReceiver`'s twin — same key table, same
reply discipline — for the endpoint that has more than one event. A handler
returning `nil` answers 200; one returning an error answers 503 and nvoken
delivers again. An event you subscribed to but registered no handler for
answers 200 with outcome `WebhookIgnored`: retrying it would only spend
nvoken's bounded attempts reaching the same absent handler. The sequence fold
stays yours, deliberately — the compare and the write have to happen in the
same transaction as the state they guard, and the receiver cannot open it.

## Receiving callbacks

`VerifyCallback` is the signature. Around it every receiver writes the same key
table, the same deduplication, and the same reply discipline —
`NewCallbackReceiver` is that, so you write the tools:

```go
receiver, err := nvoken.NewCallbackReceiver(nvoken.CallbackReceiverOptions{
	// Two entries span a rotation: nvoken mints the next version while still
	// signing with the current one.
	Keys: []nvoken.DeliverySigningKey{
		{KeyID: keyID, Version: 2, Secret: secret},
		{KeyID: keyID, Version: 1, Secret: previousSecret},
	},
	Store: ticketReplies,
	Tools: map[string]nvoken.CallbackToolHandler{
		"open_ticket": func(ctx context.Context, delivery nvoken.VerifiedCallback) (nvoken.CallbackReply, error) {
			board := delivery.AuthorizationContext["board"]
			ticket, err := openTicket(ctx, board, delivery.Envelope.Input)
			if err != nil {
				// The tool failed, not the receiver: settle it so the model can
				// read the error and correct itself.
				return nvoken.CallbackResult(map[string]string{"error": err.Error()}, true)
			}
			return nvoken.CallbackResult(ticket, false)
		},
	},
})

http.HandleFunc("POST /nvoken/callbacks", func(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	answered := receiver.Handle(r.Context(), r.Header, body)
	slog.Info("nvoken callback", "outcome", answered.Outcome, "reason", answered.Reason)
	w.WriteHeader(answered.Reply.Status)
	_, _ = w.Write(answered.Reply.Body)
})
```

`Handle` never returns an error. Everything that can go wrong is a status nvoken
understands, and `Outcome` — `CallbackSettled`, `CallbackAcknowledged`,
`CallbackReplayed`, `CallbackRefused`, `CallbackFailed` — is what the status
alone cannot tell you, with a stable `Reason` token for the log line.

The statuses are decisions about whether nvoken tries again:

| situation | status |
| --- | --- |
| no keys configured | 503 — an operator error, still fixable in the window |
| signing identity not held | 401 — redelivery reproduces it |
| signature or envelope invalid | 401 — the same bytes fail the same way |
| no handler for the signed tool name | 400 — nothing here can ever run it |
| a tool answered, or failed | 200 — settle it, carrying `is_error` if it failed |
| your handler returned an error | 503 — you failed, not the tool |

`Store` is a `Find`-then-`PutIfAbsent` pair, in that order. `Find` runs before
your tool does, because delivery is at least once and re-running the tool
repeats every effect it had; `PutIfAbsent` runs after, because two deliveries of
one ToolCall can be in flight at once and only one reply may win. Leave `Store`
nil only when every tool on the endpoint is safe to run twice.

The key table is validated when the receiver is built: a non-positive version, a
secret under 32 bytes, or the same `(KeyID, Version)` twice fails at startup
rather than refusing a live delivery, where the refusal would be permanent.

### Authorizing a delivery

Verification proves the delivery came from nvoken. It does not say what the work
belongs to, and the tool input cannot either — the model wrote it.
`SessionOptions.AuthorizationContext` is what you asserted when the Session was
created, and it arrives inside the signed body as a **sibling** of `nvoken`:

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
removes the per-delivery `GetInvocation` a receiver otherwise needs to recover
which of your objects the work is for.

[Receiving signed deliveries](../../docs/reference/callback-receivers.md) is the
long form, language-neutral.

## Browser-direct access

Your page can talk to nvoken itself, with no server of yours in the path. Mint
a short-lived grant in backend code and hand the browser that:

```go
token, err := nvoken.MintClientToken(clientKeySeed, nvoken.ClientTokenClaims{
	AppID:      appID,
	KeyID:      clientKeyID,
	Subject:    user.ID,          // from your session, never from the request
	TenantKey:  user.WorkspaceID,
	AgentKey:   "support",
	Lifetime: 10 * time.Minute,
})
```

`nvoken client-key generate <app-id> --name web` produces the keypair and
registers its public half in one step. The private seed is the App's browser
authority — whoever holds it can mint a grant for any end user — so it belongs
in backend configuration and never in a bundle.

Two things are worth deciding rather than defaulting. `SessionID` confines the
token to one conversation, which a single-conversation UI should set. A
lifetime is capped at fifteen minutes, because short lifetimes are the whole
safety story of a bearer token in a page. The browser route ceiling is fixed by
nvoken; tokens cannot select or expand it.

Minting refuses anything nvoken would refuse, so a bad grant fails in your
tests rather than in a browser as an unexplained `invalid client token`.

**Invocation webhooks stop being optional here.** The browser holds the stream,
so your backend never observes settlement any other way.

## Acting for one tenant or one end user

An app-wide credential can reach every tenant in its App, so an id that arrives
from the wrong place — a stale link, a mixed-up webhook, a tampered form field
— is an id it can act on. Say the scope once instead of re-reading the resource
before every call:

```go
tenant, err := client.Scoped(nvoken.Scope{
	TenantKey: "acme",
	UserKey:   "user-7c1f",
})
if err != nil {
	return err
}
session, err := tenant.GetSession(ctx, sessionIDFromTheRequest)
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
