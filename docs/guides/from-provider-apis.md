# Coming from provider APIs

nvoken replaces a process-owned provider loop with one durable Invocation per
agent turn. Your application still chooses the provider, exact model,
instructions, tools, and output schema on every turn; nvoken owns admission,
Session history, retries, tool parking, and recovery.

## Concept mapping

| Provider API concept | nvoken concept |
| --- | --- |
| OpenAI Chat Completions or Responses request; Anthropic Messages request | One durable Invocation |
| Application-managed `messages[]` | Canonical messages in a durable Session |
| `previous_response_id`, conversation ID, or an application chat key | Caller-owned `session_key`, or the resolved durable `session_id` |
| `model` | `spec.model.provider` plus exact `spec.model.id` |
| System/developer prompt or Anthropic `system` | `spec.instructions` |
| Function/tool declaration | `spec.tools[]` with explicit `host` or `callback` mode |
| Local function loop | Agent-facade host handler, or explicit wait → submit → resume |
| `response_format`, `text.format`, or structured-output schema | `spec.output.schema` |
| Provider stream delta | Provisional `output_text.delta` or `thinking.delta` |
| Final response text | Composed-result `output_text` and canonical assistant messages |
| Usage/model metadata | Terminal Invocation `usage` and `provenance` |
| Replaying reasoning/tool items between provider calls | Automatic inside the Invocation execution checkpoint |

The host identity tuple is
`Account → agent_key → tenant_key → session_key`. The execution spec is not a
registered Agent resource: it travels with every Invocation and is frozen into
that turn's durable snapshot.

## Before

A provider loop typically persists messages, calls a model, executes tool
calls, appends their results, calls the model again, and tries to recover after
every partial failure.

## After

```ts
import { Client, defineHostTool, defineJsonSchema } from "@deepnoodle/nvoken";

const lookupOrder = defineHostTool({
  name: "lookup_order",
  description: "Look up one order.",
  inputSchema: defineJsonSchema<{ orderId: string }>({
    type: "object",
    properties: { orderId: { type: "string" } },
    required: ["orderId"],
    additionalProperties: false,
  }),
  async handler({ orderId }) {
    return orders.lookup(orderId);
  },
});

const support = new Client().agent({
  agentKey: "support",
  instructions: "Use lookup_order for order questions.",
  tools: [lookupOrder],
});
const chat = support.session({ tenantKey: "acme", sessionKey: "ticket-483" });

console.log(await chat.text("Where is order 42?"));
```

The Agent facade admits, streams, dispatches matching host tools, submits
stable ToolCall results, resumes, and reconciles the authoritative result.
Use a lower-level Invocation handle when a queue or another process owns tool
execution.

## Migration checklist

- Choose a stable `agent_key`; do not create a new key for every request.
- Reuse a caller `session_key` only for one logical conversation and tenant.
  The Runtime permits one nonterminal Invocation per Session.
- Keep the spec inline. Do not build a separate Agent-registration database to
  imitate a provider assistant object.
- Generate or persist one idempotency key per logical turn. Replay the exact
  request after an uncertain acknowledgement; the same key with changed
  material returns `idempotency_conflict`.
- Replace local message persistence with Session reads only after the Runtime
  has become the conversation owner. Do not send old history again as one new
  input string.
- Mark each tool `host` or `callback`. A host tool without a handler parks the
  Invocation; Agent facades cancel missing handlers by default.
- Treat deltas as previews. Use composed `output_text`, structured output, and
  terminal usage/provenance as authoritative.
- Use explicit `cancel` for durable cancellation. A dropped connection,
  language task cancellation, or local timeout stops only the caller.
- Choose provider credentials explicitly when the installation default is not
  appropriate. Only `caller_ephemeral` sends secret material per turn.

## Verify the model before migrating traffic

Catalog discovery does not prove that the configured provider credential can
use a model. Run the bounded, billed probe:

```bash
nvoken model list --provider openai
nvoken model check openai/gpt-5.4-mini
```

`model check` first reports local catalog and pricing evidence, then admits a
one-iteration, eight-output-token Invocation against the configured credential.
`PASS` proves that the selected provider/model completed that probe. `FAIL`
includes the persisted provider-facing failure message. The check costs a
small provider request and creates normal durable Agent, Session, and
Invocation records.

For a complete high-level tool and Session example, use
[TypeScript Agent and host tools](../../examples/typescript-agent-tools/README.md).
For stream authority and reconnect behavior, use
[Streaming and recovery](streaming-and-recovery.md).
