# Changelog

All notable changes to the nvoken SDKs and CLI are documented here.

The repository uses aligned semantic versions where practical. Each ecosystem
has an independent release tag so a registry-specific failure can be retried
without republishing every artifact.

## Unreleased

## 0.12.0 - 2026-08-11

- **Rename Definition to Agent Definition.** `definition_id` on an Invocation is
  now `agent_definition_id`, and the CLI flag `--definition-id` is now
  `--agent-definition-id`.
- **Nest the execution configuration under `agent_definition`.** Instructions,
  model, tools, and the rest move off the top level of an invocation create
  request into a first-class Agent Definition type. Supply exactly one of the
  inline definition and `agent_definition_id`.
- **Add `POST /v1/agent-definitions`.** Register an Agent Definition without
  starting a turn, then reuse the returned ID. It is idempotent by content, so a
  repeat and a definition an earlier inline turn stored return the same response.
- **Move remote MCP secrets out of the Agent Definition.** Headers now travel
  per Invocation in `mcp_server_headers`, keyed to the server name, so a
  content-addressed definition never hashes a secret.
- Add callback ack-then-settle: reply `202` with an empty body to accept a
  delivery without settling its ToolCall, then settle it later through
  `/tool-results`.
- Record `result_origin` on every ToolCall and add `acknowledged` to the
  callback delivery outcomes.
- Add an optional App callback reply timeout to registration and update.
- Generate the transports from a preprocessed copy of the contract, because no
  generator handles the constraint-only `oneOf` stating Agent Definition
  exclusivity.

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
