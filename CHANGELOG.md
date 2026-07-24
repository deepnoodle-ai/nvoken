# Changelog

All notable changes to nvoken — the runtime (`nvokend` / `nvoken`) and the
SDKs — are documented here. The Go release `vX.Y.Z` and the npm release
`npm-vX.Y.Z` ship the same version.

The format is based on [Keep a Changelog](https://keepachangelog.com/). nvoken
is pre-1.0; pin your version.

## [Unreleased]

## [0.2.0] - 2026-07-23

### Added

- Discoverable model catalog: a `GET /v1/models` endpoint and typed
  `ModelDescriptor` / `ModelList` / `ModelPricing` schemas replace the old
  pricing-only surface, so clients can enumerate available models and their
  pricing and capabilities. TypeScript SDK gains a `models` accessor. (#47)
- TypeScript SDK: agent workflows are first-class — higher-level client helpers
  for invoking agents, following transcripts, and handling host/client tool
  calls, with README coverage. (#44)

### Changed

- **Breaking:** redesigned the invoke and streaming developer experience.
  Client-supplied tool results are now "host" tool results
  (`SubmitClientToolResultsRequest` → `SubmitHostToolResultsRequest` and
  companions), streaming exposes typed `TranscriptUpdate` / `ThinkingDeltaEvent`
  frames, and the TypeScript `stream` and `invocation-error` surfaces were
  reworked. Regenerate/update hand-written clients before upgrading. (#46)

## [0.1.1] - 2026-07-22

First tagged release. Durable agent runtime as a service plus multi-language
SDKs and the `nvoken` CLI.

### Added

- Durable runtime spine: Session ownership and persistence, durable Invocation
  admission and execution ownership, request-bound Cloud Tasks executors,
  durable ToolCall checkpoints, and resume-from-checkpoint recovery.
- Resumable Session streaming with durable transcript reads.
- Validated structured output, durable client and callback tools, and remote
  MCP server tool execution during Invocations.
- Multi-language SDKs (Go, TypeScript, Python, Rust) and the `nvoken` CLI.
- Durable credentials and CLI device auth, with per-provider credential modes.
- Production readiness profiles (single-daemon and Google Cloud), a readiness
  conformance gate, operational signals and diagnostics, retention posture,
  backup/restore verification, and compatible-upgrade / rollback support.
- Paved deployments: Google Cloud Run and single-daemon profiles.
