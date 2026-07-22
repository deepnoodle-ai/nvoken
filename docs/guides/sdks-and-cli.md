# SDKs and client CLI

nvoken ships supported workflow facades for Go, TypeScript, Python, and Rust.
They are generated from `openapi/runtime.yaml`, then wrapped with the durable
semantics an ordinary host needs: exact-request admission replay, typed errors,
bounded polling, cursor pagination, resumable Session SSE, client ToolCall
result replay, and callback verification.

| Package | Supported facade | Raw generated client |
| --- | --- | --- |
| Go | `sdk/go` package `nvoken` | `Client.Raw()` |
| TypeScript | `Client` from `@deepnoodle/nvoken` | `raw` export |
| Python | `nvoken.Client` | `nvoken_generated` |
| Rust | `nvoken::Client` | `nvoken::apis` |

Each package directory contains an executable facade-only quickstart. A local
wait timeout or a dropped stream stops only the caller; use explicit
`cancel` to change durable Invocation state. Keep the same idempotency key and
request after an uncertain admission response.

## CLI

The `nvoken` binary is a Runtime client; `nvokend` is the service daemon.
Build and inspect commands with:

```bash
go build ./cmd/nvoken
./nvoken --help
```

Before device login exists, commands require `NVOKEN_API_KEY`. Endpoint
precedence is `--base-url`, `NVOKEN_BASE_URL`, the JSON config file, then
`http://localhost:8080`. The default config is
`$XDG_CONFIG_HOME/nvoken/config.json` (or the operating system equivalent):

```json
{"base_url":"https://runtime.example.com"}
```

Use `--json` before the command for machine-readable output. The CLI covers
durable invoke, Invocation get/list/wait/cancel, Session get/list/messages/
transcript/stream, and ToolCall result submission. It imports the Go SDK and
does not maintain HTTP routes or payload types of its own.

## Development

The pinned generator toolchains and package boundary are recorded in the
[SDK and CLI architecture](../codebase/sdk-and-cli.md). Run:

| Toolchain | Supported development baseline |
| --- | --- |
| Go | 1.26.2 |
| Node.js / TypeScript | Node 24 / TypeScript 5.8.3 |
| Python | 3.10 or newer; CI uses 3.12 |
| Rust | Stable toolchain with `rustfmt` |
| OpenAPI Generator runtime | Java 21 |

```bash
make sdk-generate       # refresh all generated transports
make sdk-generate-check # fail if committed output is stale
make sdk-check          # build and test every SDK and the CLI
make onboarding-check   # prove the packed TypeScript newcomer path (requires disposable Postgres)
```

## TypeScript npm releases

`@deepnoodle/nvoken` is a public package in the existing `@deepnoodle` npm
organization. The first 0.1.0 publish is interactive after the exact merged
`main` artifact passes `make onboarding-check`:

```bash
cd sdk/typescript
npm ci
npm run build
npm test
npm pack --dry-run
npm publish --access public
```

Verify the registry rather than treating the publish command as evidence:

```bash
npm view @deepnoodle/nvoken@0.1.0 name version dist-tags repository --json
```

After the first publish, configure npm trusted publishing for GitHub repository
`deepnoodle-ai/nvoken`, workflow `release-npm.yml`, allowed action
`npm publish`, and no environment unless the workflow gains one. Later releases
must update the package version on `main`, pass the gates, and push the exact
`npm-vX.Y.Z` tag. The workflow uses short-lived OIDC authentication, publishes
with provenance, and verifies the public version.
