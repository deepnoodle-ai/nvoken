# Changelog

All notable changes to the nvoken SDKs and CLI are documented here.

The repository uses aligned semantic versions where practical. Each ecosystem
has an independent release tag so a registry-specific failure can be retried
without republishing every artifact.

## Unreleased

- **Breaking: the `stream.end` frame is now `connection.closing`.** It never
  said anything about a turn, but its name said the stream was over, and enough
  readers believed the name that the contract carried a correction in four
  places and every SDK repeated it again in its own doc comments. A name that
  needs a footnote everywhere it appears is the wrong name. The generated types
  are `ConnectionClosingEvent` and `ConnectionClosingReason`; the reasons
  `rotate`, `idle`, and `slow_consumer` are unchanged, and so is everything the
  frame does. If you switch on the frame type, change the one case. Nothing
  else in a read loop moves, because reconnecting was always the answer to any
  connection ending.

- **One word for a turn in the contract.** `Invocation` is the name in every
  path, field, schema, and error, and the endpoint summaries say it too, so the
  generated doc comments no longer alternate between the two. The introduction
  now defines the pair, and states the rule that a `*_key` is a name you choose
  while an `id` is one nvoken mints.

- **`listEndedInvocations` walks every turn that ended, oldest first.** The
  reconciliation feed, in all four SDKs. `invocation.ended` webhooks are
  delivered at least once, so a delivery that never lands leaves a turn nobody
  settles — silently, because the only evidence is a ledger row that was never
  written. `listInvocations` cannot stand in: newest-first over current state
  has no position to resume from. `nextCursor` is set on every page including
  an empty one, and `completeThrough` reports the instant the feed is complete
  to. There is deliberately no auto-paging helper: the cursor is the one thing
  that has to outlive the process.

- **The TypeScript `deleteSession` doc described behavior it no longer had.**
  It still said a live turn is stopped and silently discarded, from before the
  server started refusing that. The four facades now say the same thing:
  refused with `session_invocation_active` unless `force`, which exists for
  erasing on an end user's behalf.

- **`@deepnoodle/nvoken/status` exports the status values, not just the type.**
  `listInvocations({status})` filters server-side and takes values, so a caller
  who could only reach the type had to hand-write the list — the exact fork the
  subpath existed to prevent. `ACTIVE_INVOCATION_STATUSES` is new in all four
  SDKs, derived from the enum rather than written out, alongside
  `ALL_INVOCATION_STATUSES` in Go, Python, and Rust. The subpath stays free of
  the runtime client.

- **`make facade-check` asserts every facade exposes every parameter its
  operation has.** The hand-written facades are what integrators call and the
  only part of the SDK not derived from the contract; nothing checked them.
  `deleteSession` shipped without `force` in one language out of four because
  of it. Comments are stripped before the check, since the facade in question
  documented the parameter it never forwarded.

- **`make scripts-check` holds the Python tooling to the standard library.**
  These checks run on whatever Python is already on the machine, and nothing
  installs packages, so a third-party import does not fail as a missing
  dependency: it fails as the check, and the thing being guarded goes
  unchecked. `check_facade_parity.py` shipped importing PyYAML and did exactly
  that. It now reads the contract with `re`, the way `check_go_frame_keys.py`
  already did, and `check_script_imports.py` makes the rule enforceable rather
  than remembered.

- **Breaking: Agent Definition writes are flat, in every SDK.** The first
  argument is the definition itself, matching `AgentDefinitionWrite` on the
  wire, and `idempotencyKey` / `expectedRevision` move to a second transport
  argument. A read gives back the same flat shape plus `id`, `revision`, and
  timestamps, so a read-modify-write is now a spread and a write:
  `updateAgentDefinition(id, {...current, instructions}, {expectedRevision:
  current.revision})`. The read-only fields are stripped on the way out. Go
  gets `AgentDefinitionFromResource`, Python `AgentDefinition.from_resource`,
  Rust `AgentDefinition::from_resource`; TypeScript spreads the resource
  directly. An update replaces the whole resource, so this is the difference
  between keeping a field and silently erasing it, and each SDK now has a
  round-trip test that fails if any writable field is dropped.

- **Breaking: `name` and `idempotencyKey` are optional everywhere.** `name`
  defaults to the definition key, and the contract makes the Agent Definition
  idempotency key optional — the definition key already scopes replay. Go no
  longer rejects an empty `Name` or `IdempotencyKey`, Rust no longer takes four
  positional arguments, and `nvoken agent-definition create` no longer requires
  `--name` or `--idempotency-key`.

- **`memory` and `client_interface` are writable from every SDK.** TypeScript
  and Rust had neither and Go had no `ClientInterface`, so a definition using
  durable memory or browser client tokens could not be created or, worse, could
  be read and written back with those settings dropped.

- **Breaking: TypeScript `ToolChoice` is flat.** `{mode, name?}` matching the
  wire and the other three SDKs, in place of the discriminated union — a
  resource read back could not be spread into a write while the union stood.
  The mode-and-name agreement is now checked at the call boundary instead.

- **Fixed: Rust could not decode tool declarations or stream frames.** The
  generator emits these unions as internally tagged while keeping the required
  discriminator on each branch, so every read of an Agent Definition with tools
  failed with ``missing field `mode` `` and every write sent the discriminator
  twice. Both are `untagged` now, like the transcript blocks already patched
  for the same defect.

- **Fixed: Go dropped an Agent Definition restate.** Creation is ensure-shaped
  and answers `200` when it restates an existing definition — the ordinary
  deploy-time path — and only the `201` body was read, so the successful call
  returned a nil definition.

- **Fixed: TypeScript `deleteSession` never forwarded `force`.** The wire and
  the generated client both had it; the wrapper did not, so a caller following
  the 0.21 notes landed their boolean in the abort-signal slot and got
  `signal?.addEventListener is not a function`.

- **Breaking: options objects for the Session writes that needed room to
  grow.** TypeScript `updateSession(id, {metadata})` and `deleteSession(id,
  {force})`, Rust `update_session(id, UpdateSessionOptions)` and
  `delete_session(id, DeleteSessionOptions)`. Go and Python keep their
  signatures, which already accept a new setting without breaking callers.
  TypeScript also exports `MetadataPatch` for the `string | null` patch value
  the generated type cannot express.

- **Every generated API is reachable.** `memories`, `usage`, `apps`, `orgs`,
  `tenants`, and `admissions` join the nine already on the TypeScript `Client`
  and in `raw()`. Python was missing the same six plus `identity`, and its
  `raw()` tuple also omitted `agent_definitions` and `mcp`; all fifteen are
  there now, appended so unpacking a prefix of it still works.

- **Go generates a missing idempotency key wherever the contract requires
  one.** Credentials, credit allocation, and provider key create and rotate
  behaved differently from `Invoke` and from the other SDKs, refusing the call
  instead. Nothing is invented where the contract makes the key optional.

## 0.21.0 - 2026-08-17

- **Breaking: one `Agent` type, declared from its keys.** `Agent` is now the
  server record and the object that runs its turns; the wire shape is
  `AgentResource` (the `AgentIdentity` alias is gone). `client.agent({
  tenantKey, agentKey, definitionKey, tools })` declares one locally and it
  creates its record on first use, or on an explicit `ensure()`, which never
  mutates and refuses a declared `pinnedRevision` the record does not follow.
  `getAgent()` returns the same type, hydrated, for `withTools()` to complete.
  Same shape in Go (`AgentOptions.DefinitionKey`, `Ensure`, `Resource`),
  Python (`definition_key`, `ensure`, `with_tools`), and Rust
  (`AgentOptions::declared`, `ensure`, `resource`).

- **Breaking: one name for the Agent Definition a resource points at.** Every
  field referencing it is `definitionId`, `definitionKey`, `definitionRevision`,
  or `definition` (`definition_id`, … on the wire; `DefinitionID` in Go). That
  covers `Agent.definitionId`, `Invocation.definitionId` / `definitionRevision`
  / `definition`, the `listAgents({definitionId})` filter, and the one-turn pin
  on invoke — previously `agentRevision`, a name for a revision Agents do not
  have. `nvoken` CLI flags follow: `--definition-id`, `--definition-key`,
  `--definition-revision`. Resource names, error codes, and the
  `/v1/agent-definitions/{id}` routes keep the qualified spelling.

- **An Agent names its Definition by key.** `POST /v1/agents` accepts
  `definition_key` in place of `definition_id`, so declaring an Agent needs no
  lookup first. `nvoken agent create` takes `--definition-key`, and `--name`
  is optional now that it defaults to the Agent key.

- **Fixed: the default `fetch` is bound, so streaming works on workerd.** It
  was stored unbound and invoked as a method, which throws `Illegal
  invocation` on Cloudflare Workers — on the stream path only, so every REST
  call kept working while a turn parked on a host tool was never answered. The
  reconnect loop that hid it is now bounded: a stream that cannot connect for
  `streamReconnectTimeoutMs` (five minutes by default) throws instead of
  retrying forever, and the clock resets on any successful connection.

- **Breaking: `run()` and `waitForResult()` return an `incomplete` turn
  instead of throwing.** An `incomplete` turn stopped at a budget with its work
  retained — including a validated structured output, since an unsatisfied
  schema settles `failed` — so it is paid-for work and now arrives as a result
  to branch on. `InvocationError` is reserved for `failed` and `cancelled`.
  `EndedInvocationHandle.status` widens to `"completed" | "incomplete"`;
  branch on it rather than assuming `completed`.

- **Breaking: the client-level default model is removed** from every SDK. It
  was accepted and documented but never read in TypeScript, Go, and Python,
  and in Rust it filled an Agent Definition's model — which makes durable,
  versioned, App-owned configuration depend on whichever process published it.
  Name the model in the definition, or override it per turn.

- **A model is nameable as `"provider/id"` everywhere it appears.** The wire
  and the contract always allowed the string form; the handwritten types
  required the object. `normalizeModel` is exported for callers that need the
  object form themselves.

- **Agent Definitions are readable and creatable by key.**
  `getAgentDefinitionByKey` (`get_agent_definition_by_key`) replaces
  paginate-and-filter, and creation is ensure-shaped: restating an existing
  definition returns it, so `Idempotency-Key` is optional and the
  caller-invented key disappears. `name` defaults to the key on both creates.

- **`deleteSession` takes `force`.** Erasing a Session with a live turn skips
  its settlement, so it is now refused unless asked for explicitly.

- **`handle.streamReduced()` yields folded snapshots.** The four
  preview-discard rules and the messages-before-changes ordering live in the
  SDK's `Reducer` rather than in each consumer.

- **Dependency-free subpath exports.** `@deepnoodle/nvoken/status` and
  `@deepnoodle/nvoken/callback` reach the protocol predicates and the callback
  verifier without pulling in the runtime client, so a browser bundle or a
  separate callback process no longer forks them.

- **A transport hook.** `onResponse` reports method, URL, status, and duration
  for every round trip, including requests that failed before a response
  existed.

- **The contract arrives here; this repository no longer fetches it.** The
  nvoken service publishes `openapi/nvoken.yaml`, so `make openapi-sync`,
  `make openapi-sync-check`, `scripts/sync_openapi.py`, and
  `openapi/SOURCE.json` are gone. Regenerate with `make sdk-generate` when a
  new contract lands; `make sdk-generate-check` still proves the transports
  match it.

## 0.20.0 - 2026-08-16

- **The CLI covers the complete public outbound API.** Every operation now has
  a documented command, every command path has useful help, and pageable and
  mutating commands consistently expose cursors and stable text/JSON receipts.

- **Complex requests keep their full shape.** Invocation, App, and Session
  commands accept JSON request files for nested payloads and explicit nulls;
  tool results can be submitted in batches, usage records can be emitted as
  CSV, provider identifiers stay extensible, and the remaining API filters are
  available as flags.

- **Streams can resume and narrow to one turn.** The Go SDK and CLI accept an
  initial cursor and Invocation filter for Session streams, while filtered
  streams stop after that Invocation settles.

## 0.19.0 - 2026-08-16

- **Agents are tenant-scoped instances of versioned Agent Definitions.** Create
  a Definition and Agent before invoking by Agent ID or key; all SDKs and the
  CLI expose Agent lifecycle, revision pins, and authority-safe turn overrides.

- **`nvoken auth login` now authorizes through the console by default.** The
  browser-approved 90-day Org/operator credential is saved with Org and device
  metadata; explicit `--api-key` and `NVOKEN_API_KEY` keep the non-interactive
  CI path.

## 0.18.0 - 2026-08-15

- **A callback delivery names its tool inside the signed body.** `tool_name` on
  the envelope, surfaced as `VerifiedCallback.ToolName` / `.toolName` /
  `.tool_name` in all four SDKs. One endpoint can now serve several tools
  without trusting a URL suffix nothing signs. It is required, so verification
  rejects an envelope without it.

- **Every tool-call summary carries `mode`.** `hostToolCalls` /
  `HostToolCalls` / `host_tool_calls` sits beside the answerable filter and
  returns the narrower set: answerable **and** `mode` `host`. An acknowledged
  callback delivery is answerable to a machine credential but is nvoken's to
  deliver, so filtering on `arguments` alone runs it twice. `Agent` dispatch now
  partitions on the mode instead of on its own declared tool names, which never
  knew about a server-owned Agent Definition's callback tools.

- **A callback tool can declare its own reply deadline.** `timeout_seconds`, 1
  to 300, on the callback target; absent, the App's default applies. The App
  ceiling stays 60.

- **Signing keys rotate without a failed verification.** `signing-key
  list|mint|activate|retire` in the CLI and
  `ListAppSigningKeys`/`MintAppSigningKey`/`ActivateAppSigningKey`/`RetireAppSigningKey`
  on the Go client. Mint returns plaintext once and leaves the old version
  signing; activate moves signing after your receiver holds both; retire deletes
  the old one. Minting with `activate` collapses the three into one call, for
  recovering a lost secret.

## 0.17.0 - 2026-08-15

- **Message pages can start at the newest message.** `order: "desc"` on
  `listSessionMessages` / `MessageListOptions` / `list_session_messages`, which
  reaches nvoken's new `order` query parameter. Reading the tail of a long
  Session took a forward walk of every page; it is now one request. A cursor
  belongs to the direction that issued it and is refused by the other.

- **The TypeScript and Go readers refuse a malformed frame.** Both decoded a
  stream payload missing a field the contract requires and carried on: the
  generated TypeScript decoder left it `undefined`, and `encoding/json` left the
  Go field at its zero value. For a required bool that is a confident wrong
  answer — a change with no `terminal` read as "not the end of the turn" and was
  indistinguishable from one that genuinely was not. Rust and Python already
  rejected it, through serde and pydantic, so this is the other two catching up
  rather than a new rule.

  TypeScript wires in the generated `instanceOf` guards, which encode the
  required set from the contract already. Go has no generated validator, so
  `sdk/go/frame_validation.go` carries a table that `scripts/check_go_frame_keys.py`
  holds against `openapi/nvoken.yaml` on every `make check`. Both validate the
  entries of a `transcript.update`, not just the frame around them, and both
  still ignore fields they do not recognize.

- **Every SDK exports the terminal predicate.** `isTerminalStatus` /
  `isTurnOver` in TypeScript, `IsTerminalStatus` / `IsTurnOver` in Go,
  `is_terminal_status` / `is_turn_over` in Python and Rust, alongside
  `TERMINAL_INVOCATION_STATUSES`. The set had been spelled out privately in
  three of the four SDKs — twice in Python — and nowhere a caller could reach
  it, so every application kept its own copy and each one had to be corrected
  by hand when `paused` arrived. `isTurnOver` reads the new `terminal` field on
  an InvocationChange and falls back to classifying the status, so it is
  correct against a server that predates the field.

- **A change says whether it ends the turn.** `InvocationChange` gains a
  required `terminal`, and every SDK reducer now folds on it rather than on a
  status set of its own. It describes the change and not the turn, so a
  replayed `running` change stays false and messages still fold before changes.

- **`answerable_tool_calls` is importable from Python.** It was defined in
  `nvoken.agent` and never re-exported, so `from nvoken import
  answerable_tool_calls` failed — the one SDK where 0.16.0's promise that each
  exports the filter was not actually true.

- **The runtime `InvocationStatus` values are exported from TypeScript.** The
  generated enum object was reachable only through `raw`, so a host that wanted
  to enumerate the statuses had to reach past the public surface or hard-code
  them.

## 0.16.0 - 2026-08-14

This release collapses the protocol onto its end state, as
[design 004](docs/design/004-protocol-end-state.md) specifies. There is one
stream, one frame vocabulary, one schema family, one tool-call collection, and
one cursor name. All of it is breaking and none of it gets a deprecation
window, because `/v1` has no external users and this is the cheapest the change
will ever be.

- **One streaming route.** `GET /v1/sessions/{session_id}/stream` is the only
  stream, and `invocation_id` narrows it to one turn. Cursors were always
  Session-scoped, so the Invocation stream was a filtered view of this one that
  we shipped as a separate endpoint; it is gone, and so is the inline
  `POST /v1/invocations` streaming form. Admission is a plain JSON POST whose
  response is the acknowledgment, so you hold the turn's ID before you open
  anything.

- **One terminal signal.** A turn is over when a change for it carries a
  terminal status. That is the signal, and there is no other. The change is
  saved, so it replays at any cursor: a turn that settled while you were away
  is still settled when you return. Read `GET /v1/invocations/{invocation_id}`
  for the composed result. `invocation.accepted`, `invocation.update`, and
  `invocation.result` are removed, and `stream.end` loses reason `terminal`.
  `stream.end` speaks only about the connection now, carries no cursor because
  you already track your own, and gained `idle` and `slow_consumer`.

- **One preview frame.** `output_text.delta` and `thinking.delta` become
  `message.delta`, carrying a `kind` (`text`, `thinking`, `tool_arguments`) and
  one `delta` field for every kind. Each reducer has one accumulator instead of
  parallel fields where one is always empty. `StreamPreview` follows: `kind`
  and `delta` replace `output_text` and `thinking`, `message_id` is required
  and is the key, `iteration` is gone, and tool argument previews carry
  `tool_call_id` and `name` on every fragment. Accumulate by
  `(message_id, content_index)`.

- **The Session stream is a subscription.** It stays open while the Session is
  idle, and a turn started later by anyone appears on it, so it no longer
  returns on its own. Leave it by breaking out of the iterator (TypeScript,
  Rust), returning `ErrStopStream` (Go), or cancelling the task (Python). A
  filtered stream still closes once it has delivered its turn's terminal
  change. `nvoken session stream` tails until the server reports the Session
  idle; `nvoken invocation stream` ends on the turn's terminal change.

- **One schema family.** The `Client*` browser projections and the ten
  `*Response` wrapper unions are removed. There is one generated type per
  resource, and a browser grant receives that same shape with the fields it may
  not see omitted, which is why those fields are now optional: `agent_id`,
  `user_key`, `usage`, `provenance`, `metadata`, `context`, `limits`,
  `credit_block`, and the Session policy fields. A stranger holding only the
  contract can now decode any payload without knowing which credential fetched
  it. Generation runs with no post-processing: the `sdk/scripts/generate.sh`
  patch block that hard-selected the machine arm in TypeScript and reordered
  the Python decoder is deleted, because the union it worked around no longer
  exists. `nvoken auth whoami` prints the authentication method first and only
  the fields that caller actually has.

- **One tool-call collection.** `pending_tool_calls` and `PendingHostToolCall`
  are removed. `tool_calls` is the only collection, and its entries gained
  `name`, `arguments`, and `deadline_at`, so a call you have to answer is the
  one carrying the arguments to answer it with. Each SDK exports the filter
  rather than making every caller rediscover it: `AnswerableToolCalls` in Go,
  `answerableToolCalls` in TypeScript and Rust, `answerable_tool_calls` in
  Python.

  The unattended answering method loses the same word. `AnswerPendingToolCalls`
  is now `AnswerToolCalls` in Go, `answerToolCalls` in TypeScript, and
  `answer_tool_calls` in Python and Rust, with the options types renamed to
  match. It never selected on status `pending`, and the collection it was named
  for is gone.

- **One cursor name.** `resume_cursor` in the frame body becomes `cursor`, the
  same name as the query parameter that resumes a stream. Server-Sent Events
  still mirrors the value onto the `id:` line and accepts `Last-Event-ID` in
  the parameter's place, because a faithful SSE binding must, but those are the
  binding's mechanics rather than two more names for one value.

- **Smaller contract corrections.** Stream event unions are discriminated on
  `type`, and frame schemas accept unknown fields, so an SDK that meets a frame
  type or enum value it does not recognize skips it instead of failing the
  stream. The resync and end reasons are their own `StreamResyncReason` and
  `StreamEndReason` types rather than inline enums on each event.
  `TranscriptUpdate` is now `TranscriptUpdateEvent`. Lifecycle changes carry
  `stop_reason`, `credit_block`, and `tool_calls`.

The reference documentation is rewritten against the protocol that now exists:
see [the streaming protocol](docs/reference/streaming-protocol.md).

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
  unrelated upstream commits no longer fail the snapshot check against
  byte-identical content.
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
- Port the existing `0.9.0` client implementations into this repository.
- Add reproducible OpenAPI synchronization, generation, conformance, CI, and
  release workflows.
- Publish the first Python and Rust releases from the public repository and
  move the Homebrew formula to CLI-only archives. npm, Go, and CLI alignment
  followed in 0.10.0.
