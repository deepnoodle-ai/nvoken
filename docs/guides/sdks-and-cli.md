# SDKs and client CLI

For the fastest TypeScript proof, use the packaged command in
[Run nvoken locally](run-locally.md). This page is the package, CLI, generation,
and release reference for integrators and contributors.

nvoken ships supported workflow facades for Go, TypeScript, Python, and Rust.
They are generated from `openapi/runtime.yaml`, then wrapped with the durable
semantics an ordinary host needs: exact-request admission replay, typed errors,
bounded polling, cursor pagination, resumable Session SSE, host ToolCall
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

All four handwritten facades follow the
[cross-language SDK surface convention](../codebase/sdk-and-cli.md#cross-language-public-convention):
lazy Invocation handles, generated idempotency for ordinary calls, actionable
and terminal waits, direct Invocation event streams, symmetric collection
helpers, typed errors, and an explicit raw generated-client escape hatch.

The [TypeScript SDK guide](../../sdk/typescript/README.md) also covers
actionable host-tool waits, schema-bound tool and structured-output types,
Agent/tenant Session identity, exact host-key recovery, pagination, and
fixed-cut transcript draining. The
[TypeScript invoke showcase](../../examples/typescript-invoke-showcase/README.md)
compiles those advanced flows in the normal SDK gate and can run them against a
local Runtime and real provider.

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

Before device login exists, commands require `NVOKEN_API_KEY`. Endpoint
precedence is `--base-url`, `NVOKEN_BASE_URL`, the JSON config file, then
`http://localhost:8080`. The default config is
`$XDG_CONFIG_HOME/nvoken/config.json` (or the operating system equivalent):

```json
{"base_url":"https://runtime.example.com"}
```

Use `--json` before the command for machine-readable output. The CLI covers
durable invoke, Invocation get/result/list/wait/cancel, Session get/list/
messages/transcript/stream, model-pricing preflight, and ToolCall result
submission. `invocation result` prints the composed result: the Invocation,
its canonical messages, and the assistant text. It
imports the Go SDK and does not maintain HTTP routes or payload types of its
own.

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
