# Changelog

All notable changes to the nvoken SDKs and CLI are documented here.

The repository uses aligned semantic versions where practical. Each ecosystem
has an independent release tag so a registry-specific failure can be retried
without republishing every artifact.

## Unreleased

## 0.11.0 - 2026-08-10

- **Replace the daily usage API.** Add timeseries, breakdown, and record views
  with explicit token, cost, activity, model, and tool metrics.
- Add organization management and app organization ownership across the SDKs
  and CLI.
- Add budget listing and invocation timeline APIs across all four generated
  SDK transports.
- Strengthen Go SDK request validation and retry safety for the expanded API.
- Clean up Budget conformance naming, CLI tenant terminology, and registry
  release guidance after the 0.10.0 migration.
- Track OpenAPI provenance by the commit that last changed the contract, so
  unrelated `nvoken-cloud` commits no longer fail `make openapi-sync-check`
  against a byte-identical snapshot.
- Use `claude-opus-5` in the Python, TypeScript, and Rust reasoning examples.

## 0.10.0 - 2026-08-09

- Generate every SDK and the CLI from the single authoritative
  `openapi/nvoken.yaml` contract, including the Identity API.
- **Adopt the breaking concept-freeze contract.** Replace pending inputs with
  nudges, settled lifecycle names with ended, and nested Session budgets with
  `max_estimated_cost_usd`; expose lowercase credential profiles and one-time
  app signing keys.

## 0.9.1 - 2026-08-09

- Establish this repository as the public home of the Go, Python, TypeScript,
  and Rust SDKs and the Go `nvoken` CLI.
- Port the existing `0.9.0` client implementations from `nvoken-cloud`.
- Add reproducible OpenAPI synchronization, generation, conformance, CI, and
  release workflows.
- Publish the first Python and Rust releases from the public repository and
  move the Homebrew formula to CLI-only archives. npm, Go, and CLI alignment
  followed in 0.10.0.
