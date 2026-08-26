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
const agent = await client.agents.create({
  key: "support",
  name: "Support",
  instructions: "Be concise and helpful.",
  model: "anthropic/claude-sonnet-5",
});

console.log(await agent.text(
  "Summarize the latest customer request.",
  { tenant: "acme", user: "alice" },
));
```

An Agent is stored, reusable, versioned behavior and is directly runnable in
every SDK. App ownership is the default, so one Agent can serve every tenant;
each Turn still states its tenant and optional user explicitly. Later code
loads the same Agent with `await client.agent("support")`, while
`client.agents` owns creation, ID lookup, and listing. The returned Agent owns
publication, archive, restore, tool binding, and execution.

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
nvoken agent create --file support-agent.json
nvoken agent lookup support
nvoken turn start --agent-id agent_... --tenant acme "Hello"
```

Use `nvoken --help` for the exact command surface.

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
