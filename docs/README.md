# Repository documentation

**[Design direction](design/DIRECTION.md)** is the standing direction for the
protocol and the contract. It outranks the individual design documents below.
Read it before proposing a change to either.

- [Public SDK repository design](design/001-public-sdk-repository.md)
- [Session options conflict scope](design/002-session-options-conflict-scope.md)
- [Streaming protocol remediation](design/003-streaming-protocol-target.md),
  which is a remediation list and not the target its file name claims
- [The end-state protocol](design/004-protocol-end-state.md), the actual
  target: the protocol designed with no compatibility budget, and the path
  to it. All three steps have landed.
- [CLI contract conformance](design/005-cli-contract-conformance.md)
- [SDK write shape parity](design/006-sdk-write-shape-parity.md)
- [One home for each host-supplied fact](design/007-host-supplied-context.md),
  which retires the second write path to Session metadata, pins `user_key` to
  the Session, and gives callback authorization a field of its own
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
