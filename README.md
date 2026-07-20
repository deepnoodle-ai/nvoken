<p align="center">
  <img src="assets/header.jpg" alt="Nvoken" width="880">
</p>

# Nvoken

Nvoken is an agent runtime deployed as a service. It is built to be used
by your application, often a SaaS, as its agentic harness. Your app calls
Nvoken, and Nvoken runs the agent loop for you with all the harness features
that are needed to build an excellent agentic AI experience in your app.

No provisioning first. Your application sends the agent specification with
the request: instructions, model preferences, tool schemas, output
contract, budgets. Nvoken resolves or creates the Session, runs the turn
durably, and streams output and tool calls back. Read more at
[docs/product/overview.md](docs/product/overview.md).

The primary operation:

```text
invoke(execution_spec, input, optional_session) -> durable invocation
```

## Why

Every team shipping an agentic product builds a harness, discovers the features
it needs one incident at a time, and ends up with thousands of lines of
plumbing and an AI experience that still trails the leading tools. A few
hosted agent runtimes are available but they lead to vendor lock-in, lack
an open-source focus, and are still slim on capabilities.

Nvoken aims to address these shortcomings. It is open source first,
self-hostable, and built for embedding in a multi-tenant application. It takes
care of the agent loop, sessions, durability, streaming, tool call exchange,
provider portability, cancellation and steering, budgets, usage tracking, and
observability.

Learn more at [docs/product/why.md](docs/product/why.md) and
[docs/product/harness.md](docs/product/harness.md).

Nvoken is carefully designed to avoid becoming a store of too much application
state. In most areas, your application's state resides there, and Nvoken doesn't
know or care about it. The primary type that Nvoken does store is the Session,
which is the message history for each conversation. This is surprisingly hard
to get right while being LLM provider agnostic and while supporting streaming
updates with events. Nvoken helps on those fronts.

## Concepts

- **Session.** A conversation: the ordered sequence of messages, including
  tool call inputs and tool call results, resolvable by a host-provided
  session key.
- **Tool call.** The durable record of what the model requested and what
  actually happened, across builtin, callback, and client execution modes.
- **Agent.** An identity, created automatically the first time an
  Invocation names it. Nvoken tracks which agent each Session and
  Invocation belongs to, for lookup and observability, but stores no agent
  configuration.
- **Agent turn.** One complete unit of agent work: the model is called,
  requests tools, receives results, and is called again, as many rounds as
  the work takes, until it produces a final response.
- **Invocation.** The resource `invoke()` returns: the durable record of
  one agent turn. It carries the input, resolved spec, model and tool
  activity, output, usage, and recovery state, and ends in exactly one
  terminal state. You watch, steer, and cancel a turn through its
  Invocation.
- **Memory.** Keep agent memory entirely on your side, or opt into
  Nvoken-managed memory records.

## Stateless, with two exceptions

Nvoken stores execution state, not product configuration. It is stateless
with exactly two exceptions, the state it must own to do its job:

1. **Sessions.** The conversations: each is the sequence of messages,
   including tool call inputs and results, plus its Invocation history.
2. **The running turn.** Checkpoints, tool calls, and usage for an executing
   Invocation: this is what makes a turn survivable across crashes and restarts.

Everything else is yours: agent definitions, users and tenants,
credentials, orchestration, product data. Agent behavior arrives with each
request as the execution spec; nothing is provisioned or registered first.

## Cross-provider

The execution spec selects the model per Invocation. Multi-provider
support is built on [dive](https://github.com/deepnoodle-ai/dive); no part
of the runtime contract assumes a single vendor.

## Deployment

Nvoken is self-hostable and bring-your-own-key. A complete installation is
one binary plus Postgres.

## Status

Early development. The API contract and internals are actively taking
shape; expect breaking changes.

## License

[Apache-2.0](LICENSE)
