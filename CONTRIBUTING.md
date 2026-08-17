# Contributing

## Ownership

This repository owns SDK ergonomics, generated client artifacts, conformance
fixtures, examples, the `nvoken` CLI, and package releases. The nvoken service
owns server behavior and the authoritative OpenAPI contract, and publishes that
contract to `openapi/nvoken.yaml` here.

`openapi/nvoken.yaml` is therefore not editable in this repository. When a new
contract lands, regenerate all clients and review the complete cross-language
diff in one change:

```bash
make sdk-generate
make check
```

Do not hand-edit these generated paths:

- `sdk/go/generated/`
- `sdk/typescript/src/generated/`
- `sdk/python/src/nvoken_generated/`
- `sdk/rust/src/apis/`
- `sdk/rust/src/models/`
- `sdk/rust/src/routes.rs`
- `sdk/operations.json`

Handwritten facades live beside the generated transports in each SDK. Keep
reliability behavior there instead of customizing generated templates.

## Pull requests

Keep contract, generated output, facade adaptations, tests, examples, and docs
in the same pull request. Run `make check` before requesting review. Focused
language changes may use the native package commands while iterating, but the
complete gate is required before merge.

## Releases

Package versions should normally stay aligned. Release tags are:

- CLI: `vX.Y.Z`
- Go: `sdk/go/vX.Y.Z`
- TypeScript: `npm-vX.Y.Z`
- Python: `pypi-vX.Y.Z`
- Rust: `crates-vX.Y.Z`

Registry trusted publishers and the Homebrew tap token are configured in
GitHub rather than stored in this repository.
The complete release checklist and one-time registry bootstrap steps are in
[`docs/guides/releasing.md`](docs/guides/releasing.md).
