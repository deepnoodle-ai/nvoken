# nvoken Go SDK

The Go SDK makes an nvoken Agent feel like a local runnable object while the
service keeps each Turn durable. The ordinary path is intentionally small:

```go
client, err := nvoken.NewClient(os.Getenv("NVOKEN_URL"), os.Getenv("NVOKEN_API_KEY"))
if err != nil {
	log.Fatal(err)
}

analyst, err := client.Agent(ctx, "real-estate-analyst")
if err != nil {
	log.Fatal(err)
}

answer, err := analyst.Text(ctx, "Compare these listings.", nvoken.TurnOptions{
	TenantKey: "acme",
	UserKey:   "alice",
})
```

`client.Agent` performs a lookup. App ownership is the default; pass
`AgentLookupOptions{OwnedBy: nvoken.TenantOwned("acme")}` or
`nvoken.UserOwned("acme", "alice")` for another owner namespace. Ownership
selects which Agent you mean. The tenant and user on `TurnOptions` describe the
actor for this execution; they do not change Agent ownership.

## Start, run, and text

An Agent supports three levels of convenience:

```go
turn, err := analyst.Start(ctx, "Compare these listings.", options)
result, err := analyst.Run(ctx, "Compare these listings.", options)
text, err := analyst.Text(ctx, "Compare these listings.", options)
```

- `Start` admits a durable Turn and returns immediately.
- `Run` waits for the Turn result and runs bound host tools when needed.
- `Text` returns the final-answer text or `NoOutputTextError`.

Go cannot spell a parameter type that accepts both a bare `string` and
`[]nvoken.InputBlock`. `TurnInput` therefore keeps the compact string call and
validates at runtime that the value is one of those two forms before sending a
request. Other dynamic values fail locally with a validation error.

High-level create, publish, and Turn calls generate an idempotency key when you
omit one. The same generated key is retained across automatic retries. Supply
your own when the operation must be recovered across process boundaries.

## Conversations and memory

Conversation continuity, Turn actor attribution, and MemorySpace selection are
independent:

```go
conversation := analyst.Conversation(nvoken.ConversationOptions{
	TenantKey: "acme",
	UserKey:   "alice",
	Selection: *nvoken.ContinueOrCreateConversation(
		"property-42",
		nvoken.TenantConversation(),
	),
	Memory: nvoken.UserMemory("analyst"),
})

answer, err := conversation.Text(ctx, "What changed?")
```

Use `ContinueConversation(id)` for an existing Conversation, or omit
Conversation selection from ordinary `TurnOptions` for a standalone Turn. The
Conversation handle binds its tenant, optional user, memory, and maximum limits
together with its continuity selection. Per-call `ConversationTurnOptions` may
add metadata, waiting behavior, an idempotency key, or narrower limits; it
cannot replace the bound actor or memory.

Use `NoneMemory()`, `TenantMemory(namespace)`, or `UserMemory(namespace)` to
choose memory explicitly. A tenant MemorySpace can be intentionally shared
across several users; user memory requires a Turn user.

Calls through Conversation handles for the same effective Conversation are
serialized by their shared Client inside the current Go process. Durable
concurrency policy remains a service concern; wire-exact conflict controls stay
under raw Turn admission.

## Inline behavior

An Agent is optional. Run behavior directly when it should not be published as
a reusable AgentRevision:

```go
classifier := client.Inline(nvoken.Behavior{
	Instructions: "Classify the message as billing, sales, or support.",
	Model:        model,
})

result, err := classifier.Run(ctx, message, nvoken.TurnOptions{
	TenantKey: "acme",
	Memory:    nvoken.NoneMemory(),
})
```

## Host tools

Bind process-local handlers to an Agent or inline runner. Binding returns a new
handle, so a shared Agent can safely use different handlers in different
parts of an application.

```go
analyst = analyst.BindTools(nvoken.Tool{
	Name: "lookup_listing",
	Handler: func(ctx context.Context, input any, call nvoken.TurnToolContext) (any, error) {
		log.Printf("handling %s for %s", call.ToolCallID, call.TurnID)
		return listings.Lookup(ctx, input)
	},
})
```

`Run`, `Text`, and `Updates` settle waiting host calls when a matching handler
is bound. If no matching local handler is available, the durable Turn remains
waiting; the caller can stop waiting and later recover the Turn with the right
handler. Callback, builtin, and MCP calls are never executed by these local
handlers.

## Recovery and streaming

A Turn handle is local and performs no lookup until you use it:

```go
turn := client.Turn("turn_...", nvoken.TurnAccess{
	TenantKey: "acme",
	UserKey:   "alice",
})

snapshot, err := turn.Status(ctx)
result, err := turn.Result(ctx)
stopping, err := turn.Interrupt(ctx)
```

`Status` is passive: it reads the current Turn plus its produced messages and
final-answer text without driving tools. `Result` drives bound host tools and
waits for a terminal Turn. `Interrupt` asks the Turn to stop at its next clean
stopping point and keep what it produced; it returns the Turn's state as of the
request, which is often still running, so follow `Updates` or `Result` for
settlement. Interrupting a Turn that already ended returns it unchanged and is
not an error. Failed and cancelled work returns a
`TurnExecutionError` carrying the complete terminal result. A local timeout
returns `TurnTimeoutError` with the Turn and idempotency key needed for recovery.
If the admission transport fails before its outcome is known,
`TurnAdmissionError` retains the generated idempotency key so the exact request
can be retried without creating a second Turn.

Persist the Turn ID and, when useful, `turn.IdempotencyKey()`. Bind the same
host tools to a recovered handle before calling `Result` if it may be waiting
for host work.

`turn.Updates` follows the direct Turn stream, folds replayable transcript
updates with provisional message deltas, reconnects from the last durable
cursor, and returns once the Turn's terminal change arrives:

```go
err = turn.Updates(ctx, nvoken.UpdatesOptions{}, func(
	update nvoken.TurnUpdate,
) error {
	render(update.Snapshot, update.Previews)
	return nil
})
```

Each callback receives a reduced Turn snapshot, not a raw SSE frame. `NewReducer`
remains available for applications that deliberately consume a Conversation
stream through `Raw()` and want the same fold behavior.

## Exact API access

The facade covers ordinary Agent execution. Administrative operations and any
wire-exact feature are available without a second client:

```go
response, err := client.Raw().ListConversationsWithResponse(ctx, params)
```

`Raw()` is the generated OpenAPI client. Its request and response types match
the published HTTP contract exactly.

Nullable update fields preserve write intent. Use `nullable.NewNullNullable`
to send JSON `null`, `nullable.NewNullableWithValue` to replace a value, or the
zero value to omit the field and leave it unchanged:

```go
request := generated.UpdateConversationJSONRequestBody{
	Retention: nullable.NewNullNullable[generated.RetentionPolicy](),
}
response, err := client.Raw().UpdateConversationWithResponse(
	ctx,
	conversationID,
	request,
)
```
