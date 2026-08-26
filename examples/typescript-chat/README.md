# TypeScript chat example

This command-line app creates one Agent and binds a durable Conversation. Each
line becomes a Turn, and `conversation.text()` waits for its final assistant
answer before accepting the next line.

```bash
npm install
npm run build
```

Run it against nvoken with an App credential and a model your provider account
can use:

```bash
NVOKEN_API_KEY='<app-key>' \
NVOKEN_MODEL_PROVIDER='anthropic' \
NVOKEN_MODEL='claude-sonnet-5' \
npm start
```

`NVOKEN_BASE_URL` defaults to `http://localhost:8080`.
`NVOKEN_TENANT_KEY` defaults to `local-chat`. Set
`NVOKEN_CONVERSATION_KEY` to a stable host-owned key when a later process must
continue the same Conversation.

The SDK generates an idempotency key for every admission and preserves it
across ambiguous retries. A production host should supply a key derived from
its durable message record when it must recover the exact same admission after
a process restart. A local timeout or stopped app detaches the caller; it does
not cancel the durable Turn.
