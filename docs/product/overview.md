# Product Overview

nvoken is an agent runtime deployed as a service. It is built to be used
by your application, often a SaaS, as its agentic backend: your app calls
nvoken, and nvoken does the agent work behind a delightful agentic
experience for your users. Model providers ship one stateless generation
call; nvoken is the layer above the LLM API and below your application:
the turn that runs the model against tools until the work is done, the
Session that gives the agent memory and identity, and the durability,
routing, and observability a production agent needs. These
are the harness features every agentic product otherwise builds itself.
nvoken supplies them so you can focus on your app, not on building an
agent harness.

That harness runs deeper than a loop. nvoken builds on
[dive](https://github.com/deepnoodle-ai/dive), Deep Noodle's agent
library, and [harness.md](harness.md) catalogs the full depth, from the
loop and the tool system through context engineering, human-in-the-loop,
provider portability, and observability.

## The primary operation

```text
invoke(execution_spec, input, optional_session) -> durable invocation
```

No provisioning first. Your application sends the agent specification with
the request: instructions, model preferences, tool schemas, output
contract, limits. nvoken resolves or creates the Session, runs the turn
durably, and streams output and tool calls back.

An agent turn may take seconds or tens of minutes, progressing through many
rounds of tool calls. Durable admission means an API disconnect, API process
loss, or deploy cannot erase accepted work. If an execution owner is lost, the
same Invocation is requeued and a replacement continues from its last committed
model or builtin checkpoint. Uncommitted provider work may run again.

## Boundaries

nvoken stores execution state, not product configuration. It owns the
Sessions and the execution state of running turns; your application
remains the source of truth for agent definitions and their versions,
users and tenants, integrations and credentials, orchestration, and
product data. Agent behavior arrives with each request as the execution
spec; nothing is provisioned or registered first.

Every tool with side effects executes on your side of the boundary, either as
a host tool call recoverable through reads and the stream or as a signed
callback to your endpoints.

## Deployment

nvoken is always embedded: a host application calls its API. It is
self-hostable and bring-your-own-key, and a complete installation is one
binary plus Postgres.
