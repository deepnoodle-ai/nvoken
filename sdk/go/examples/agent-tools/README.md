# Go Agent and host tools

This example creates a tenant-owned Agent with one host tool, `lookup_order`,
binds a Go handler for it, and sends two Turns through one Conversation. The
first Turn makes nvoken call the handler in this process. The second Turn
answers from the committed transcript without calling it again.

For a live run, start nvoken and run from `sdk/go`:

```bash
NVOKEN_API_KEY='<app-key>' go run ./examples/agent-tools
```

`NVOKEN_BASE_URL` defaults to `http://localhost:8080` and `NVOKEN_MODEL` to
`anthropic/claude-sonnet-5`.

The handler uses the stable ToolCall ID as the host-side idempotency key.
That is what makes its own effects safe when tool-result submission or process
recovery causes the handler to run again.

See the [TypeScript version](../../../../examples/typescript-agent-tools/README.md)
for the same flow in TypeScript.
