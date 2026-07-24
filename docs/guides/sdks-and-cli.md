# SDKs and client CLI

For the fastest TypeScript proof, use the packaged command in
[Run nvoken locally](run-locally.md). This page is the package, CLI, generation,
and release reference for integrators and contributors.

nvoken generates complete Runtime clients for Go, TypeScript, Python, and Rust
from `openapi/runtime.yaml`, then adds handwritten reliability helpers. The
common baseline is exact-request admission replay, durable Invocation handles,
typed errors, bounded polling, Invocation SSE, host ToolCall result replay,
callback verification, model discovery, and a raw generated-client escape
hatch. Agent workflows are implemented in TypeScript, Python, and Go; Rust's
documented floor is transport plus durable handle.

All four handwritten facades accept remote MCP server declarations and expose
stateless tool discovery; TypeScript additionally provides `mcpServer(...)`.
Before integrating, run `nvoken mcp list-tools --url ...` to inspect the exact
execution-time projection. The
[remote MCP guide](remote-mcp-tools.md) covers secret handling, projection, and
crash recovery.

An Invocation is one durable agent turn. The host owns `agent_key`, optional
`tenant_key`, `session_key`, and `idempotency_key`; the spec travels with each
Invocation. Across facades, handle `outputText` reads `output_text`,
Session-scoped reads say `listSessionMessages`, `401` is `authentication`,
`403` is `permission`, cancellation is not a timeout, and each language uses
its native casing.

| Package | Supported handwritten level | Session stream | Raw generated client |
| --- | --- | --- | --- |
| Go | `Agent` + `Client` + `InvocationHandle` | `Client.StreamSession` | `Client.Raw()` |
| TypeScript | `Client` + high-level `Agent` and bound Session | `streamSession` | `client.raw()` |
| Python | async `Agent` + `Client` + `InvocationHandle` | `Client.stream_session` | `nvoken_generated` |
| Rust | `Client` + `InvocationHandle` | Generated operation only | `nvoken::apis` |

Each package directory contains an executable facade-only quickstart. A local
wait timeout or a dropped stream stops only the caller; use explicit
`cancel` to change durable Invocation state. Keep the same idempotency key and
request after an uncertain admission response.

All four handwritten facades follow the baseline parts of the
[cross-language SDK surface convention](../codebase/sdk-and-cli.md#cross-language-public-convention):
lazy Invocation handles, generated idempotency for ordinary calls, actionable
and terminal waits, direct Invocation event streams, typed errors, per-turn
provider-credential selection, and an explicit raw generated-client escape
hatch. TypeScript is the reference Agent facade; Python and Go expose the same
five Agent verbs with language-native cancellation and result shapes. Rust
deliberately remains transport plus durable handle and documents manual
wait-for-action → submit → settle orchestration.

The [TypeScript SDK guide](../../sdk/typescript/README.md) also covers
actionable host-tool waits, schema-bound tool and structured-output types,
Agent/tenant Session identity, exact host-key recovery, pagination, and
fixed-cut transcript draining. The
[TypeScript Agent and host tools](../../examples/typescript-agent-tools/README.md)
is the high-level Agent example. The
[TypeScript invoke showcase](../../examples/typescript-invoke-showcase/README.md)
demonstrates the lower-level handle rung. Both compile in the normal SDK gate
and can run against a local Runtime and real provider. See
[Streaming and recovery](streaming-and-recovery.md) for the shared preview,
cursor, and authoritative-settlement guarantees.

## CLI

The `nvoken` binary is a Runtime client; `nvokend` is the service daemon.
Install the matching official release of both commands with:

```bash
brew install deepnoodle-ai/tap/nvoken
nvoken --version
nvokend --version
```

Contributors can build either command from source through the
[Develop nvoken guide](developing-nvoken.md).

Authenticate interactively with `nvoken auth login`, select a saved profile,
or supply `NVOKEN_API_KEY` for a host or CI process. Endpoint precedence is
`--base-url`, `NVOKEN_BASE_URL`, the saved profile, the JSON config file, then
`http://localhost:8080`. The default config is
`$XDG_CONFIG_HOME/nvoken/config.json` (or the operating system equivalent):

```json
{"base_url":"https://runtime.example.com"}
```

Use `--json` before the command for machine-readable output. The CLI covers
durable invoke, Invocation get/result/list/wait/cancel, Session get/list/
resolve/messages/transcript/stream, model discovery/pricing/access checks, and
ToolCall result submission. Text `invoke` streams and prints one answer;
machine-readable admission acknowledgements retain their stable JSON shape.
`invocation wait --until actionable` stops at waiting or terminal work, and
`session resolve --session-key ...` recovers a durable Session from host keys.
`invocation result` prints the composed result: the Invocation, its canonical
messages, and the assistant text.

For a complete execution spec, put the exact public wire `spec` object in a
JSON file:

```bash
nvoken invoke 'Classify this request' \
  --agent support \
  --session-key ticket-483 \
  --idempotency-key ticket-483-turn-7 \
  --spec-file ./spec.json
```

Reuse that idempotency key only with the unchanged request after an uncertain
acknowledgement; changed fingerprint material returns
`idempotency_conflict`. A Session accepts one nonterminal Invocation at a
time. Local bound Sessions serialize one process, while the Runtime remains
authoritative across processes.

Catalog discovery is not a provider-access check. A small billed canary proves
the configured credential/model path and reports the local pricing evidence:

```bash
nvoken model list --provider openai
nvoken model get --provider openai --model gpt-5.4-mini
nvoken model check openai/gpt-5.4-mini
```

The CLI imports the Go SDK and does not maintain HTTP routes or payload types
of its own. See [Coming from provider APIs](from-provider-apis.md) for the
provider-loop migration.

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

## Binary and Homebrew releases

A `vX.Y.Z` tag on an exact merged commit triggers `.github/workflows/release.yml`.
The workflow tests the Go commands and publishes checksummed archives containing
both `nvoken` and `nvokend` for Darwin and Linux on amd64 and arm64, plus a
Windows amd64 archive. Stable tags update `Formula/nvoken.rb` in
`deepnoodle-ai/homebrew-tap`; prerelease tags publish GitHub assets without
changing the stable formula.

Build and inspect the same assets locally with:

```bash
make release VERSION=X.Y.Z
cat dist/checksums.txt
```

The tag version must match `sdk/typescript/package.json`. Cross-repository
Homebrew publication uses the nvoken repository's `TAP_GITHUB_TOKEN` Actions
secret and fails visibly when that authority is unavailable. A successful tag
push is only the trigger: verify the GitHub Release assets, checksums, Homebrew
formula commit, a clean `brew install`, and both version commands.

## TypeScript npm releases

`@deepnoodle/nvoken` is a public package in the existing `@deepnoodle` npm
organization. Install the published package with:

```bash
npm install @deepnoodle/nvoken
```

An exact `npm-vX.Y.Z` tag triggers `.github/workflows/release-npm.yml`. The tag
must match `sdk/typescript/package.json`; the workflow builds, tests, packs,
publishes with npm trusted publishing, and reads the version back from the
public registry. Verify the registry independently:

```bash
npm view @deepnoodle/nvoken@X.Y.Z name version dist-tags repository --json
```

npm trusted publishing is bound to GitHub repository `deepnoodle-ai/nvoken`,
workflow `release-npm.yml`, the `npm` GitHub environment, and the `npm publish`
action. It uses short-lived OIDC authentication and publishes with provenance.

All four of those must line up or the OIDC exchange is rejected. The publish job
therefore declares `environment: npm`, and that environment exists in the
repository with a deployment policy admitting the `npm-v*` tag pattern. A
mismatch is hard to diagnose from the log: npm does not report a trusted
publishing failure, it silently falls back to anonymous auth, and the registry
answers `404 Not Found - PUT` (see npm/cli#9088). Read a 404 here as "the OIDC
identity did not match", not as a missing package. A successful publish prints
`Signed provenance statement with source and build information from GitHub
Actions`; treat the absence of that line as an authentication failure even if a
later step passes.

Node 24 bundles an npm newer than the 11.5.1 trusted-publishing minimum, so the
workflow does not upgrade the CLI separately.

For one coordinated nvoken release, push `vX.Y.Z` and `npm-vX.Y.Z` from the same
fully checked merged `main` commit. Monitor and verify the binary/Homebrew and
npm workflows independently; one green publication surface cannot prove the
other succeeded.
