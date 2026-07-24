<p align="center">
  <img src="assets/header.jpg" alt="nvoken" width="880">
</p>

<div align="center">

**Give your multi-tenant app a great agentic experience without building the harness from scratch.**

<sub>Works with&nbsp;**Anthropic · OpenAI**</sub>

[![license](https://img.shields.io/badge/license-Apache--2.0-2563eb)](LICENSE)
[![status](https://img.shields.io/badge/status-early%20development-b7791f)](#how-to-help)

[Run Locally](docs/guides/run-locally.md) · [Develop](docs/guides/developing-nvoken.md) · [Deploy](deploy/single-daemon/README.md) · [State Ownership](#your-app-owns-the-state) · [Contract](#the-contract) · [Docs](docs/README.md)

</div>

---

nvoken is a lightweight, open-source agent harness-as-a-service and AI gateway.
Your app sends an agent spec and the input; nvoken runs the whole agent turn.
**An Invocation is one durable agent turn.** The lowercase word `turn` is the conceptual unit;
`Invocation` is the durable API resource. The host-owned identity tuple is
`agent_key` (Agent identity), `tenant_key` (optional partition), `session_key`
(conversation identity), and `idempotency_key` (turn retry identity); the
instructions, model, and tools travel inline on each Invocation.
The current Runtime implements durable turns, streaming, checkpoints, durable
waits, host and callback tools, and guarded remote streamable-HTTP MCP tools.
Steering and broader human-in-the-loop workflows remain deferred.

Building a complete agentic experience in your app's UI is non-trivial. nvoken
allows you to focus on the application-specific portions of that problem, rather
than continually filling in gaps in your own agent loop implementation.

And nvoken stays out of your way by being **stateless** with respect to agent
instances and configuration data like system prompts and skills. One of the
reasons the LLM generation APIs are so successful and easy to use is that
they are **stateless**. nvoken aims to retain that approach as much as possible.

**Your app remains the source of truth, not a heavy agent runtime.**

## Run nvoken locally

The first-time path uses the official Homebrew release and public TypeScript
package—no clone, Go build, Python configurator, or manual migration. It assumes
Docker and Node.js 20+ are installed and one provider API key is active in your
shell.

```bash
brew install deepnoodle-ai/tap/nvoken
mkdir -p nvoken-quickstart && cd nvoken-quickstart
export OPENAI_API_KEY='<your-provider-key>'
nvokend quickstart --provider openai --model '<model-you-can-access>'
```

Then the official npm quickstart sends one turn and prints the assistant's
response, proving the installed package can admit, execute, and read durable
work through your local Runtime. Follow
[Run nvoken locally](docs/guides/run-locally.md) for that command and cleanup;
use the [TypeScript SDK guide](sdk/typescript/README.md#multiple-turns) when you
are ready to keep a conversation in one durable Session.
If you intend to change this repository, use [Develop nvoken](docs/guides/developing-nvoken.md)
instead. Production operators should choose the [single-daemon](deploy/single-daemon/README.md)
or [Google Cloud](deploy/google-cloud/README.md) deployment profile only after
the local evaluation path.

## The contract

> **Early implementation.** Durable JSON admission and authoritative
> Invocation/Session reads now work, and the self-contained service executes
> generation-only Anthropic and OpenAI turns. Hosts can list durable work,
> page the canonical transcript, and drain fixed-cut incremental recovery
> snapshots or tail the same state over resumable SSE with ephemeral live
> generation deltas. Durable builtin checkpoints and crash continuation resume
> a lost execution owner from its last committed boundary, and durable host
> signed callback tools can safely continue parked work, and remote MCP tools
> execute through durable discovery snapshots and fenced attempts. Generated Go,
> TypeScript, Python, and Rust clients expose the complete transport surface.
> Handwritten facades add durable handles, MCP discovery, and reliability
> helpers in all four; TypeScript, Python, and Go also provide high-level Agent
> workflows, while Rust deliberately provides the durable-handle floor. The Go
> `nvoken` client CLI uses the same SDK contract.
> A reproducible [Google Cloud Run paved deployment](deploy/google-cloud/README.md)
> packages this slice with private Cloud SQL, Secret Manager, and an explicit
> migration job.
> A separate [single-daemon profile](deploy/single-daemon/README.md) packages one
> combined process with operator-provided Postgres, explicit availability
> limits, smoke/load tooling, and incident guidance.
> Current production-readiness claims and missing evidence are tracked only in
> the [readiness profiles and evidence matrix](docs/testing/production-readiness-profiles.md).
> If the contract looks wrong for your app, please
> [open an issue](https://github.com/deepnoodle-ai/nvoken/issues).

```jsonc
POST /v1/invocations
{
  "agent_key": "support-triage",                 // Account-wide identity only
  "tenant_key": "customer-482",                  // optional Session partition
  "session_key": "thread-8813",                  // yours; resolved or created
  "idempotency_key": "thread-8813:message-7",    // safe retry identity
  "input": "why was I charged twice?",
  "spec": {
    "instructions": "You are a billing support agent…",
    "model": { "provider": "anthropic", "id": "claude-sonnet-5" }
  }
}

202 Accepted
{
  "agent_id": "agnt_…",
  "session_id": "sesn_…",
  "invocation_id": "invk_…",
  "status": "queued",
  "deduplicated": false,
  "deadline_at": "2026-07-23T16:30:00Z"
}
```

The first contract is background execution: acknowledgement follows durable
admission, execution does not belong to the request handler, and clients recover
authoritative state by durable ID or a scope-bound cursor. The answer is one
read away: `GET /v1/invocations/{invocation_id}/result` returns the
authoritative Invocation, the turn's canonical messages, and the assistant text
as one `output_text` string. Text blocks within one assistant message
concatenate directly; distinct assistant messages join with exactly two
newlines. The same `POST` streams one Invocation when the
client sends `Accept: text/event-stream`; `output_text.delta` previews are
id-less, while durable `invocation.update` and `invocation.result` frames carry
resume cursors. A Session SSE stream exposes the same vocabulary across turns.
Disconnecting either stream never affects execution. Hosts can bound or
idempotently cancel accepted work;
Postgres decides the terminal winner. If an execution owner is lost, the same
Invocation is requeued and continues from its last validated checkpoint. A
host tool parks that Invocation without holding compute; the host can recover
the pending call by ID, submit its result idempotently, and let any engine
continue it. A callback tool instead lets nvoken deliver the same durable call
to a public host HTTPS endpoint with a stable ToolCall idempotency key and a
versioned HMAC signature. A remote MCP declaration lets nvoken discover and
durably execute tools from any public streamable-HTTP MCP server without
registering a connector. The exact surface is in
[openapi/runtime.yaml](openapi/runtime.yaml).

Probe an MCP server before writing application code:

```bash
export MCP_HEADERS='{"Authorization":"Bearer ..."}'
nvoken mcp list-tools \
  --name support \
  --url https://mcp.example.com/rpc \
  --header-env MCP_HEADERS
```

The same declaration can travel as `spec.mcp_servers`. Secret headers are
encrypted only for the Invocation, excluded from identity and public reads,
and destroyed at terminal settlement. See
[Remote MCP tools](docs/guides/remote-mcp-tools.md).

A Session runs one turn at a time: at most one nonterminal Invocation. An equal
idempotent replay returns the original durable record; a distinct concurrent
turn receives `session_invocation_active`.

Each Invocation also binds its model provider to one explicit payment and
credential source: caller-ephemeral, reusable Account BYOK, tenant BYOK, or a
platform-funded key; self-hosted installations retain installation BYOK as the
default. The binding is durable for recovery, encrypted when it contains secret
material, rechecked before every provider call, and never falls through to a
different payer when unavailable.

## Your app owns the state

A multi-tenant app cannot treat agents as fixed config. Instructions, tools,
and models vary by tenant, by plan, and by user. If those definitions are
registered into an agent runtime, every variation becomes a record to
provision, and every product change becomes a migration for each agent instance.
For an app with thousands of users, this turns into a big pain.

nvoken avoids this by design. Your app composes the spec from its own database
on every invocation, so tenant customization is just a query. nvoken stores
sessions, running turns, and optional encrypted provider credentials—not agent
definitions. There is nothing to register, sync, or migrate when you update
your app with new agent customizations.

That is the "your app owns the state" test in the comparison below. Only
nvoken passes it. This boundary is the design; see
[docs/product/overview.md](docs/product/overview.md).

## How it compares

Two decisions are hard to reverse once agents are wired into your product:
**where the runtime runs** and **who owns your state**. Compare on those.

| Project                                                                              | Runs on                                   | Fully open source | Your app owns the state |
| ------------------------------------------------------------------------------------ | ----------------------------------------- | :---------------: | :---------------------: |
| **nvoken**                                                                           | anywhere with a binary and a Postgres URL |         ✅         |            ✅            |
| [Claude Managed Agents](https://platform.claude.com/docs/en/managed-agents/overview) | Anthropic's cloud only                    |         ✗         |            ✗            |
| [AWS Bedrock AgentCore](https://aws.amazon.com/bedrock/agentcore/)                   | AWS only                                  |         ✗         |            ✗            |
| [Cloudflare Agents](https://github.com/cloudflare/agents)                            | Cloudflare only                           |         ✗         |            ✗            |
| [Vercel Open Agents](https://github.com/vercel-labs/open-agents)                     | Vercel only                               |         ✗         |            ✗            |
| [kagent](https://github.com/kagent-dev/kagent)                                       | any Kubernetes cluster                    |         ✅         |            ✗            |
| [Google AX](https://github.com/google/ax)                                            | any Kubernetes cluster                    |         ✅         |            ✗            |
| [Letta](https://github.com/letta-ai/letta)                                           | your infra or Letta Cloud                 |         ✅         |            ✗            |

Several well-known "open source" runtimes are open clients to closed
infrastructure. The SDK is MIT, but the part that runs your agent cannot leave
the vendor's cloud. With nvoken, you avoid these limitations. It is bring-your-own-key,
Apache-2.0 end to end, and built for embedding in a multi-tenant app. It runs
**anywhere you can put a binary and a Postgres URL**: a laptop, your cloud
account, or an air-gapped network.

Multi-provider support comes from
[Dive](https://github.com/deepnoodle-ai/dive), so the contract never assumes a
single vendor. More in [docs/product/why.md](docs/product/why.md) and
[docs/product/harness.md](docs/product/harness.md).

The [local Run guide](docs/guides/run-locally.md),
[source-development guide](docs/guides/developing-nvoken.md), and production
deployment profiles deliberately remain separate journeys.

A managed version of nvoken is being considered. An earlier version of nvoken powers
[MobiusOps.ai](https://mobiusops.ai).

## How to help

Early development, and openly so: the API contract and internals are actively
being figured out. That's the good part. The design isn't set, so feedback
right now genuinely changes it.

- ⭐ **Star the repo** if the idea is one you want to exist.
- 💬 **[Open an issue](https://github.com/deepnoodle-ai/nvoken/issues)** with your ideas and feedback.

## License

[Apache-2.0](LICENSE): free to use, modify, and self-host, commercially included.
