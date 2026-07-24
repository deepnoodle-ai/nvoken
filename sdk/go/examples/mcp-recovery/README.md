# Remote MCP recovery example

This example exercises the complete remote-MCP path: authenticated stateless
discovery, a durable model-selected tool call, executor replacement, and
authoritative result plus fixed-cut transcript recovery.

The scripted server deliberately binds plain HTTP on loopback. nvoken correctly
rejects that destination. Expose it through a public HTTPS endpoint (for
example, your normal development tunnel) and give nvoken only that public URL.

## 1. Start the scripted server

```bash
cd sdk/go
MCP_TOKEN=replace-me MCP_CALL_DELAY=20s \
  go run ./examples/mcp-recovery serve
```

Expose `http://127.0.0.1:8090` through a public HTTPS endpoint, then verify the
exact execution-time projection before admitting work:

```bash
export MCP_URL=https://your-public-endpoint.example/mcp
export MCP_TOKEN=replace-me
nvoken mcp list-tools \
  --name scripted \
  --url "$MCP_URL" \
  --allowed-tool lookup \
  --header-env MCP_HEADERS
```

`MCP_HEADERS` is a JSON object:

```bash
export MCP_HEADERS='{"Authorization":"Bearer replace-me"}'
```

The command prints `scripted__lookup` and never prints the header.

## 2. Admit the durable turn

Run nvokend against Postgres with a configured model credential, then:

```bash
export NVOKEN_BASE_URL=http://127.0.0.1:8080
export NVOKEN_API_KEY=...
export NVOKEN_PROVIDER=anthropic
export NVOKEN_MODEL=...
cd sdk/go
go run ./examples/mcp-recovery run
```

The client first calls stateless discovery, then prints the accepted
`invocation_id` before waiting. Save the printed recovery command.

## 3. Inject replacement

When the scripted server logs `scripted MCP call 1 started`, terminate the
current nvokend process before the 20-second response and restart it against
the same Postgres database. This injects process loss after the fenced attempt
but before settlement. Because the tool advertises positive read-only and
idempotent annotations, a replacement owner may make at most one recovery
attempt. The scripted server's monotonically increasing call count makes that
visible.

If the original client exits while the API restarts, recover without
readmitting:

```bash
export NVOKEN_INVOCATION_ID=invk_...
go run ./examples/mcp-recovery run
```

The final JSON is assembled from `GET /v1/invocations/{id}/result` and the
fixed-cut Session transcript. It includes the settled tool message, assistant
answer, terminal Invocation, transcript count, and durable resume cursor.
Changing `NVOKEN_INVOCATION_ID` never creates work.

For the unsafe branch, remove the tool's positive `ReadOnlyHint` and
`IdempotentHint`, repeat the kill, and observe one server hit plus the canonical
unknown-outcome tool result. nvoken does not redispatch a possibly mutating
call after ownership is uncertain.
