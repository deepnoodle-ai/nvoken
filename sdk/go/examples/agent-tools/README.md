<!-- ABOUTME: Explains how to run the Go host tools example against Nvoken. -->
<!-- ABOUTME: Shows where the tool handler runs and how its result carries into a later Turn. -->

# Go Agent and host tools

This example creates a tenant-owned Agent whose tool contract names one host
tool, `lookup_order`, binds a Go handler for that tool in this process, and
sends two Turns through one Conversation.

The first Turn asks about an order. Nvoken runs the Agent, decides that it
needs `lookup_order`, and parks the Turn as waiting. The SDK sees the waiting
host call, runs the bound handler here, submits its result, and waits for the
final answer. The second Turn asks a follow-up question. Nvoken replays the
committed transcript, including the earlier tool call and its result, so the
Agent answers without calling the tool again.

The program prints one line when the handler runs, then both answers. It exits
with an error if the handler ran more than once or the second answer does not
carry the estimated delivery forward.

## Prerequisites

You need:

* Go 1.26 or newer
* a running Nvoken endpoint
* an App API key
* a configured model provider

For a local quickstart backend, the base URL is `http://localhost:8080`.

## Run

From the `sdk/go` directory:

```bash
NVOKEN_API_KEY='<app-key>' \
NVOKEN_MODEL_PROVIDER='anthropic' \
NVOKEN_MODEL='claude-sonnet-5' \
go run ./examples/agent-tools
```

`NVOKEN_BASE_URL` defaults to `http://localhost:8080`. The model defaults to
`anthropic/claude-sonnet-5`. Set `NVOKEN_MODEL_PROVIDER` and `NVOKEN_MODEL`
when your endpoint uses another configured model.

Each run generates a fresh run ID and uses it in the tenant, Agent key, and
Conversation key, so repeated runs never share state.

## Test without a live service

The example ships with a test that stands up an in-process fake Nvoken and
drives the same code path, so the repository gate compiles and exercises it:

```bash
cd sdk/go
go test ./examples/agent-tools
```

## What to look at

* `lookupOrderContract` publishes the tool's name, description, and input
  schema with the Agent. Nvoken stores that contract and shows it to the model.
* `BindTools` attaches the Go handler. Binding sends nothing to Nvoken. The
  handler runs only when a Turn reports a waiting host call for that name.
* `Conversation` with `ContinueOrCreateConversation` gives both Turns the same
  durable history. That is what lets the second Turn answer from the first
  Turn's tool result.

The handler returns the stable ToolCall ID as its idempotency key. Tool-result
submission or process recovery can run the handler again for the same call,
and that ID is what makes the handler's own side effects safe to repeat.

See the [TypeScript version](../../../../examples/typescript-agent-tools/README.md)
of this example for the same flow in TypeScript.
