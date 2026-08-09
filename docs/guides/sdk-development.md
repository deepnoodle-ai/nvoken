# SDK and contract development

## Source boundaries

The service contract is authored in the private `nvoken-cloud` repository. The
`openapi/nvoken.yaml` file in this repository is a synchronized snapshot, not a
second source of truth. Its exact upstream commit is recorded in
`openapi/SOURCE.json`.

The generated transports are intentionally committed. A contributor can build,
test, and package every public client without access to `nvoken-cloud` or an
OpenAPI generator. Regeneration requires the pinned OpenAPI Generator jar,
Java, and Go.

## Synchronize a contract change

Start from a clean, committed `nvoken-cloud` checkout:

```bash
make openapi-sync NVOKEN_CLOUD_REPO=../nvoken-cloud
make sdk-generate
make check
```

`make openapi-sync-check` compares the snapshot and its provenance
with the neighboring cloud checkout. The default sync refuses uncommitted
contract changes. `scripts/sync_openapi.py --allow-dirty` exists for local
cross-repository iteration, but dirty provenance should not be merged.

## Generated and handwritten layers

Do not hand-edit generated transports. The exact generated paths are listed in
`CONTRIBUTING.md` and enforced by `make sdk-generate-check`.

Handwritten facade code belongs in the language package outside its generated
directory. Preserve these cross-language reliability rules:

- reuse the exact request and idempotency key after ambiguous admission;
- honor `Retry-After` and do not retry semantic conflicts;
- resume SSE from the last durable cursor and reconcile through authoritative
  reads after a resync;
- preserve ToolCall IDs when submitting results;
- treat local wait cancellation and remote Invocation cancellation as different
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

Use `make sdk-check` for the shared conformance server and complete
cross-language gate.
