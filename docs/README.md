# Repository documentation

**[Design direction](design/DIRECTION.md)** is the standing direction for the
protocol and the contract. It outranks the individual design documents below.
Read it before proposing a change to either.

- [Public SDK repository design](design/001-public-sdk-repository.md)
- [Session options conflict scope](design/002-session-options-conflict-scope.md),
  a historical pre-0.30 production-defect analysis
- [Streaming protocol remediation](design/003-streaming-protocol-target.md),
  which is a remediation list and not the target its file name claims
- [The end-state protocol](design/004-protocol-end-state.md), the actual
  target: the protocol designed with no compatibility budget, and the path
  to it. All three steps have landed.
- [CLI contract conformance](design/005-cli-contract-conformance.md), the
  historical 0.19 implementation record
- [SDK write shape parity](design/006-sdk-write-shape-parity.md), the historical
  Agent Definition write-shape record and its resolved follow-ups
- [One home for each host-supplied fact](design/007-host-supplied-context.md),
  the historical context-authority analysis that preceded the hard cut to
  per-Turn actor attribution and signed resolved facts
- [TypeScript SDK ergonomics and explicit facade surface](design/008-typescript-sdk-ergonomics.md),
  the accepted facade: awaited Agent lookup, direct `start`/`run`/`text`,
  independent behavior, memory, Conversation, and actor coordinates, and an
  exact raw-transport escape hatch
- [The browser conversation controller](design/009-browser-conversation-controller.md),
  the plan for a resumable headless conversation in a page: one bounded
  transcript read to resume, a reducer that folds to current state rather than
  logging revisions, `Turn.admission`, and the rule that no common developer
  operation may require `raw()`
- [SDK and contract development](guides/sdk-development.md)
- [CLI development and release](guides/cli.md)
- [Receiving signed deliveries](reference/callback-receivers.md) — the key
  table, the reply discipline, deduplication, and what a signature does and
  does not prove
- [The streaming protocol](reference/streaming-protocol.md)
- [Streaming protocol assessment](reference/streaming-protocol-assessment.md),
  spent, and kept as the record of the argument
- [Research on agent protocols and standards](research/README.md)

Product documentation, guides, and the HTTP API reference are published at
[nvoken.com/docs](https://nvoken.com/docs).
