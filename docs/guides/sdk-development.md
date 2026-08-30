# SDK and contract development

## Source boundaries

The service contract is authored by the nvoken service, which owns it and
publishes `openapi/nvoken.yaml` here. That file is a published copy, not a
second source of truth: do not edit it in this repository.

The generated transports are intentionally committed. A contributor can build,
test, and package every public client with nothing but this checkout — no
service access and no OpenAPI generator. Regeneration requires the pinned
OpenAPI Generator jar, Java, and Go.

## Adopt a published contract change

A contract change arrives as a new `openapi/nvoken.yaml`. Regenerate and verify
it in one reviewed change:

```bash
make sdk-generate
make check
```

`make sdk-generate-check` proves the committed transports match the contract in
this repository, so a contract that landed without regeneration fails CI rather
than shipping.

## Generated and handwritten layers

Do not hand-edit generated transports. The exact generated paths are listed in
`CONTRIBUTING.md` and enforced by `make sdk-generate-check`.

`sdk/scripts/generate.sh` generates from an isolated copy of the snapshot. It
renames the browser `ClientInterface` schema only in the Go generator input,
because oapi-codegen reserves that name for its transport interface. That
isolated input also receives Go-only type annotations preserving omitted, null,
and replacement as distinct states for nullable Conversation policy updates,
without changing every nullable response field. Generation also repairs a small
set of discriminator unions emitted incorrectly by the Rust generator. JSON
names and the committed public contract remain unchanged, and every workaround
fails loudly if the expected generated shape moves.

Handwritten facade code belongs in the language package outside its generated
directory. Preserve these cross-language reliability rules:

- reuse the exact request and idempotency key after ambiguous admission;
- honor `Retry-After` and do not retry semantic conflicts;
- resume SSE from the last durable cursor and reconcile through authoritative
  reads after a resync (see
  [The streaming protocol](../reference/streaming-protocol.md) for the frames,
  their durability, and the shared reducer);
- preserve ToolCall IDs when submitting results;
- treat local wait cancellation and remote Turn cancellation as different
  operations;
- verify callback signatures against the raw body before parsing it.

Shared wire fixtures live in `sdk/conformance/fixtures`. A behavior represented
in more than one SDK should normally gain or update a shared fixture rather than
four unrelated tests.

## Focused commands

```bash
# Go
(cd sdk/go && GOWORK=off go test ./...)

# TypeScript
npm ci --prefix sdk/typescript
npm run build --prefix sdk/typescript
npm test --prefix sdk/typescript

# Python
python3 -m venv sdk/python/.venv
sdk/python/.venv/bin/python -m pip install -e 'sdk/python[test]'
sdk/python/.venv/bin/pytest sdk/python

# Rust
cargo test --manifest-path sdk/rust/Cargo.toml --all-targets
```

Use `make sdk-check` for a shared conformance-server transport smoke through all
four SDKs plus each language's complete local suite.
