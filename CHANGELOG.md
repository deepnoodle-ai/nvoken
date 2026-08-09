# Changelog

All notable changes to the nvoken SDKs and CLI are documented here.

The repository uses aligned semantic versions where practical. Each ecosystem
has an independent release tag so a registry-specific failure can be retried
without republishing every artifact.

## Unreleased

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
- Publish the first aligned release from the public repository and move the
  Homebrew formula to CLI-only archives.
