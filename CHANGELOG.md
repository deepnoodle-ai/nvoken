# Changelog

All notable changes to the nvoken SDKs and CLI are documented here.

The repository uses aligned semantic versions where practical. Each ecosystem
has an independent release tag so a registry-specific failure can be retried
without republishing every artifact.

## Unreleased

- **Sync the streaming protocol contract.** Stream event unions are
  discriminated on `type`, frame schemas accept unknown fields, and
  `stream.resync` and `stream.end` reasons are named enums with
  forward-compatibility guidance. `stream.end` gained `slow_consumer`.
  Lifecycle changes carry `stop_reason`, `credit_block`, `pending_tool_calls`,
  and `tool_calls`. Breaking, in generated type names only: the browser
  projections are renamed from `Client*` to `Browser*`, `TranscriptUpdate` is
  now `TranscriptUpdateEvent`, the resync and end reasons are their own
  `StreamResyncReason` and `StreamEndReason` types rather than inline enums on
  each event (in Go, `generated.Terminal` is now `generated.ReasonTerminal`),
  and `PendingHostToolCall.input` is typed as a JSON object rather than as
  anything. No route, operation, or JSON field name changed, so an older SDK
  keeps working against the updated service.
- **Carry preview identity through the reducers.** `StreamPreview` in all four
  SDKs exposes the `message_id` a delta frame publishes, so a rendered preview
  can be keyed by the identity its saved message will land under.

## 0.15.0 - 2026-08-12

- **Add durable Agent memory across every SDK.** Agent Definitions can select
  tenant or user memory and choose how it enters model context; list, search,
  get, and delete memory records through the generated transports, Go helpers,
  and new CLI memory commands.
- **Enable managed anonymous browser access.** Configure anonymous access on an
  App and exchange an origin-bound visitor token for short-lived client access
  through every SDK or `nvoken app anonymous-token`.
- **Expose Invocation traces and bounded operational logs.** Inspect paginated
  trace summaries, complete span trees, correlated log records, and explicit
  partial or truncated state through the SDKs, Go client, and CLI.

## 0.14.0 - 2026-08-11

- **Replace window Budgets with persistent Credits.** Allocate exact USD funds
  to the default or a named tenant, then read its account and append-only
  allocation history; Session spending ceilings are removed.
- **Make reusable Agent Definitions stable revisioned resources.** Create, get,
  list, update, archive, and restore them by ID; equal content no longer
  implies shared identity.
- Archive and restore Apps and Orgs without destroying usage history, and
  filter container lists by active, archived, or all.
- Invocation requests and all high-level SDKs accept exactly one of an inline
  Agent Definition or `agent_definition_id`. Browser client-token requests
  remain pinned to a reusable ID and revision.

## 0.13.0 - 2026-08-11

- **Add recorded application context.** Send named state snapshots in `context`
  on an invocation and nvoken records them as leading Session messages. Keep
  instructions static and put changing product state here instead, so the prompt
  prefix stays stable enough for provider caching.
- A name is a stable identity. Resending it unchanged adds no transcript
  message, so a stateless host can send its whole snapshot every turn. Use tier
  `contextual` for conversation facts and `operator` for application-authoritative
  policy.
- Context is Session history, not an Agent Definition field, so it never changes
  a content-addressed `agent_definition_id`. The SDKs check the eight-item,
  8 KiB, and 16 KiB bounds before a request leaves the process.
- Add the `reminder` content block and the `system` message role to the
  transcript types. The CLI renders a reminder as its name and content.
- Add `--context` and `--context-operator` to `nvoken invoke`, both taking
  `name=content` and both repeatable.

## 0.12.0 - 2026-08-11

- **Rename Definition to Agent Definition.** `definition_id` on an Invocation is
  now `agent_definition_id`, and the CLI flag `--definition-id` is now
  `--agent-definition-id`.
- **Nest the execution configuration under `agent_definition`.** Instructions,
  model, tools, and the rest move off the top level of an invocation create
  request into a first-class Agent Definition type. Supply exactly one of the
  inline definition and `agent_definition_id`. Agent options keep these fields
  flat, so only low-level invoke callers migrate.
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
