# nvoken

Official SDKs and command-line client for the
[nvoken durable agent runtime](https://nvoken.com).

This public repository contains the Go, Python, TypeScript, and Rust clients,
the Go `nvoken` CLI, generated OpenAPI transports, shared conformance fixtures,
and release automation. The nvoken service owns its implementation and the
authoritative OpenAPI contract, and publishes that contract here.

## Install an SDK

```bash
# TypeScript
npm install @deepnoodle/nvoken

# Python
pip install nvoken

# Go
go get github.com/deepnoodle-ai/nvoken/sdk/go@latest

# Rust
cargo add nvoken
```

Each package combines generated low-level API types and transports with an
idiomatic handwritten facade for durable admission, waiting, streaming,
recovery, ToolCalls, callbacks, and common agent workflows.

## TypeScript quickstart

Set the base URL and API key from your nvoken App, then run a turn:

```bash
export NVOKEN_BASE_URL='<your nvoken API base URL>'
export NVOKEN_API_KEY='<your API key>'
```

```ts
import { Client } from "@deepnoodle/nvoken";

const client = new Client();
await client.createAgentDefinition({
  definitionKey: "support",
  name: "Support",
  instructions: "Be concise and helpful.",
  model: "anthropic/claude-sonnet-5",
});
// Declared from its keys; the Agent creates its record on first use.
const agent = client.agent({ agentKey: "support", definitionKey: "support" });

console.log(await agent.text("Summarize the latest customer request."));
```

An Agent is one type in every SDK: the server record and the object that runs
its turns. Declaring it locally costs no round trip, `ensure()` creates or reads
the record without mutating it, and `getAgent()` returns that same type already
hydrated. Both creates are keyed and idempotent, so the snippet is safe to run
twice.

See the [nvoken quickstart](https://nvoken.com/docs/quickstart) and the
language-specific guides in [`sdk/`](sdk/).

## Install the CLI

```bash
go install github.com/deepnoodle-ai/nvoken/cmd/nvoken@latest
```

Released binaries are also available from
[GitHub Releases](https://github.com/deepnoodle-ai/nvoken/releases). The CLI
uses `NVOKEN_BASE_URL` and `NVOKEN_API_KEY`, or named credential profiles:

```bash
nvoken auth login --profile work --default
nvoken model list
nvoken agent-definition create --definition-key support --name Support \
  --instructions "Be concise and helpful." \
  --provider anthropic --model claude-sonnet-5
nvoken agent create --agent-key support --definition-key support
nvoken invoke --agent-key support "Hello"
```

Use `nvoken --help` for the exact command surface.

When one turn is triggered by another turn's ToolCall, preserve that cause and
keep temporary transcript lifetime explicit:

```bash
nvoken invoke --agent-key researcher \
  --parent-invocation-id inv_... --tool-call-id call_... \
  "Investigate this branch"
nvoken invocation list --parent-invocation-id null # top-level only
```

The child still owns a normal Session. Set `session_options.retention` in an
SDK request or a complete CLI request file when that Session should expire.

## Repository layout

```text
cmd/nvoken/           Go CLI
internal/authstore/   CLI-only credential profile storage
sdk/go/               Go SDK module
sdk/python/           Python package
sdk/typescript/       TypeScript package
sdk/rust/             Rust crate
sdk/conformance/      Cross-language fixtures and local test server
openapi/              The nvoken API contract, published here by the service
examples/             Executable SDK examples
docs/                 Design records, guides, protocol reference, research
scripts/              Version, contract, and CLI release tooling
```

The architecture and ownership boundaries are recorded in
[`docs/design/001-public-sdk-repository.md`](docs/design/001-public-sdk-repository.md).

## Develop

The committed generated clients make ordinary builds self-contained. Run the
complete gate with:

```bash
make check
```

Useful focused targets:

```bash
make sdk-check              # all SDKs, conformance, examples, and CLI tests
make sdk-generate           # regenerate from the published OpenAPI contract
make sdk-generate-check     # prove generated transports are current
```

`openapi/nvoken.yaml` is published here by the nvoken service, which owns the
contract. Do not edit it in this repository. When a new contract arrives,
regenerate and verify in one reviewed change:

```bash
make sdk-generate
make check
```

Do not hand-edit generated directories. See
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the ownership and release workflow.

## License

Apache-2.0
