# nvoken

Official SDKs and command-line client for the
[nvoken durable agent runtime](https://nvoken.com).

This public repository contains the Go, Python, TypeScript, and Rust clients,
the Go `nvoken` CLI, generated OpenAPI transports, shared conformance fixtures,
and release automation. The service implementation and authoritative OpenAPI
contract live in the private `deepnoodle-ai/nvoken-cloud` repository.

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

const agent = new Client().agent({
  agentKey: "support",
  agentDefinition: {
    instructions: "Be concise and helpful.",
    model: { provider: "openai", id: "<model-id>" },
  },
});

console.log(await agent.text("Summarize the latest customer request."));
```

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
nvoken invoke --agent support --provider openai --model <model-id> "Hello"
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
openapi/              Synchronized snapshot of the nvoken-cloud contract
examples/             Executable SDK examples
```

The architecture and ownership boundaries are recorded in
[`docs/design/001-public-sdk-repository.md`](docs/design/001-public-sdk-repository.md).

## Develop

The committed generated clients make ordinary builds independent of the
private backend repository. Run the complete gate with:

```bash
make check
```

Useful focused targets:

```bash
make sdk-check              # all SDKs, conformance, examples, and CLI tests
make sdk-generate           # regenerate from the committed OpenAPI snapshot
make sdk-generate-check     # prove generated transports are current
make openapi-sync-check     # compare the snapshot with ../nvoken-cloud
```

When the authoritative contract changes, synchronize and regenerate it as
one reviewed change:

```bash
make openapi-sync NVOKEN_CLOUD_REPO=../nvoken-cloud
make sdk-generate
make check
```

Do not hand-edit generated directories. See
[`CONTRIBUTING.md`](CONTRIBUTING.md) for the ownership and release workflow.

## License

Apache-2.0
