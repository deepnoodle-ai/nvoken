# The browser conversation controller

**Status:** Accepted plan, pre-implementation.
**Author:** Claude Fable 5.1 with Curtis Myzie
**Date:** 2026-09-02
**Applies to:** the service for one contract change; this repository for the
TypeScript SDK, the shared reducer fixture, the Go, Python, and Rust reducers,
and documentation.
**Reading order:** [DIRECTION](DIRECTION.md) outranks this document.
[Design 008](008-typescript-sdk-ergonomics.md) fixed the facade vocabulary
this builds on. The source being ported is branch
`codex/prd-075-conversation-controller` at `95a4d52f`, PR #82, written against
the 0.27 SDK; it does not compile on `main` and is a donor, not a base.

## What this delivers

Someone opens a page that talks to an nvoken Agent. They type, the answer
streams in. Then they switch tabs for ten minutes, their train goes into a
tunnel, they reload, they open a second tab, they come back tomorrow. The
conversation is still there and the page is honest about what it can do right
now.

One headless controller in `@deepnoodle/nvoken/browser`. State and
transitions, no rendering, no framework dependency. Concretely:

- **The conversation survives the page.** Reload resumes the same
  Conversation with recent history and the exact stream position.
- **A visitor needs no account.** Managed anonymous access from an opaque
  visitor token the page stores. No application credential in the bundle.
- **Retry never duplicates a turn.** An ambiguous send failure is a state the
  page can retry with the same idempotency key, never a second Turn.
- **The UI never guesses.** Every action reports enabled, in flight, or
  disabled with a stated reason.
- **Unknown states stay visible.** A Turn status this SDK version does not
  know is surfaced as unknown, never read as finished.
- **Memory is bounded.** 500 messages, 8 previews, 64 KiB per preview, and one
  lifecycle record per Turn.

## Decisions

Each of these was checked against `main` at `977e088e`. The evidence is cited
so an implementer can re-verify rather than trust.

### D0. No common operation needs `raw()`.

Set by Curtis Myzie on 2026-09-02. `raw()` is for administration, reporting,
and transport controls. If a developer building an ordinary chat reaches for
it, the facade is missing something. Every decision below is measured against
this.

For a page, the controller is what satisfies it: a developer holds a
controller and never touches `raw()`. The controller itself may call `raw()`
for operations that are not common outside it. Interrupt is common outside
it, so it moves to the facade (S7).

### D1. The controller calls `raw()` for the transcript read. The facade grows by one method.

`sdk/scripts/check_facade_parity.py` lists `interruptTurn`,
`listConversationMessages`, `getConversationTranscript`, `listTurns`, and
`deleteConversation` in `RAW_ONLY`, with the reason in a comment: Conversation
and Turn administration stays request-shaped under `raw()`. `COVERAGE_BASELINE`
is empty and must stay empty, so promoting any of these to the facade means
writing it in Go, Python, and Rust too.

Applying D0: a stop button is a common operation, so `interruptTurn` leaves
`RAW_ONLY` and becomes `Turn.interrupt()` in all four SDKs. The bounded
transcript read stays under `raw()` for now, because the facade has no
Conversation read handle at all and designing one is outside this series; the
controller is the only common caller and it hides the call. See the follow-up
at the end.

The controller holds a `BrowserClient`, uses `start()` for admission,
`turn(id).interrupt()` to stop, `conversationFrames()` for the stream, and
reaches the transcript snapshot through `client.raw()`.

**Consequence the plan has to handle:** `raw()` is a door with nothing behind
it. Generated APIs throw `ResponseError`, apply no retry, and do not normalize
errors. `Client.request()` does all three but is `@internal` and unreachable
through the `BrowserClient` interface. See S4.

### D2. Bootstrap is one read: a bounded-tail transcript snapshot.

The port plan that preceded this document proposed three reads: a `tail`
page from `listConversationMessages`, `getConversationTranscript` for the
cursor, and `listTurns` for active Turn state. All three are wrong on `main`:

- `getConversationTranscript` takes no `limit` and no `cursor`; its `messages`
  is the whole transcript. Calling it "for the cursor" downloads everything
  the bounded page was meant to avoid.
- `TranscriptSnapshot` embeds `Conversation`, which carries `active_turn_id`
  and `active_turn_status`. `resolveActivity()` in
  `sdk/typescript/src/activity.ts` already consumes exactly those fields.
  `listTurns` is redundant.
- Two separate reads have an ordering hazard: read the tail before the
  snapshot and a message committed between them is in neither, and the stream
  from the snapshot cursor never replays it.

The pre-cut contract had the right shape. PR #80 shipped
`GET /v1/sessions/{id}/transcript?tail=true&limit=N`: the newest `limit`
messages in ascending order, the exact head cursor observed before message
selection, and a page token walking older windows at the same fixed cut.
PR #89 regenerated from the published contract and it was gone, with no line
in the changelog or the PR body saying why. This plan restores it on the
Conversation route. See C1.

### D3. The reducer folds to one current change per Turn.

The donor branch added a `latestChangesOnly` reducer option. The preceding
plan proposed deleting it in favour of filtering on `TurnChange.current`. That
is not equivalent: `current` means "current as of the read that produced this
frame." The reducer keys by `(turnId, revision)` and never clears earlier
entries, so after two frames for one Turn, two stored changes both say
`current: true`. Consumers still fold by revision.

Nothing consumes the log. `TurnHandle.updates()` and `mergeStreamSnapshot()`
in `client.ts` sort by revision and take the highest; `latestChanges()` in
`activity.ts` does the same fold for pages. DIRECTION item I3 records that the
snapshot "advertises state and delivers history." So the reducer folds, always,
in all four SDKs, and the option is never ported. See S2.

### D4. `Turn` exposes its admission. The receipt keeps `conversationId`.

The preceding plan proposed shrinking `ConversationSendReceipt` to `{turnId}`
because the facade `Turn` exposes only `id`. That hides a round trip without
removing it. In anonymous mode `AnonymousTokenResponse.conversation_id` is null
until the first Turn, so after the first send the controller must learn the
Conversation id to open the stream. With a `{turnId}` receipt it calls
`turn.status()` to get it.

`Client.admit()` holds the full admission resource, including
`conversation_id` and `deduplicated`, and keeps only `id`. The fix is a
readonly `admission` on `Turn`, populated by `start()`, absent on `turn(id)`.
The parity script checks request parameters, not response shapes, so this is
TypeScript-only. It also closes a general gap: nobody using
`continue_or_create` can learn which Conversation they landed in without a
second request. See S3.

### D5. Host mode requires a Conversation selection.

The donor takes `sessionId?: string`. On `main`, omitting `conversation` from
`createTurn` admits a standalone Turn with no Conversation. A host page that
omitted the id would get a chat that never persists and never streams, with no
error. Host mode takes `conversation: BrowserConversationSelection`, required.
Anonymous mode omits the selector; the service resumes the visitor's canonical
Conversation (`AnonymousTokenResponse.conversation_id` description).

### D6. `erase()` is not in this series.

`deleteConversation` lost its `force` parameter and refuses a Conversation
with an active Turn. Nothing in the contract says an anonymous grant may
delete its own Conversation. Rather than ship an action that may be refused
for every browser caller, `erase()`, `ConversationReset` for erasure, and the
`erasure` snapshot field are omitted. `startOver()` stays: it replaces the
visitor, not server state.

### D7. `onConnectionChange` lives on `StreamLoopOptions`.

`reconnectingFrames` in `stream.ts` yields `connection.closing`, so a
deliberate close is already visible. A silent drop followed by reconnect is
not, and "reconnecting" is the state a UI needs to stop looking broken. The
callback is called where the loop already knows: after a successful connect,
and before each reconnect delay. It is observational; a throw is swallowed.

### D8. The donor's `browser-client.ts` is deleted, not ported.

`main`'s `browser.ts` has `createBrowserClient`, `issueAnonymousToken`, and
`refuseMachineCredential`, throwing `NvokenError` with codes where the donor
throws bare `Error`. Retarget imports; delete the file; drop its two `files`
entries from `package.json`.

## Contract change

### C1. `getConversationTranscript` gains a bounded tail mode

Service-side. This repository adopts the published `openapi/nvoken.yaml` and
regenerates. Everything below is the proposal the service should implement;
the SDK takes whatever is published.

Query parameters on `GET /v1/conversations/{conversation_id}/transcript`:

| Name | Type | Meaning |
| --- | --- | --- |
| `tail` | boolean, default false | Select the newest bounded window instead of the whole transcript. |
| `limit` | integer 1..200, default 50 | Maximum messages in the window. Only meaningful with `tail` or `page_token`. |
| `page_token` | string | Opaque continuation from `next_page_token`. Fetches the preceding window at the original fixed cut. Mutually exclusive with `tail`. |

Response additions to `TranscriptSnapshot`, present in tail and page modes:

| Field | Type | Meaning |
| --- | --- | --- |
| `has_more` | boolean | Whether an older window exists. |
| `next_page_token` | string or null | Continuation for the next older window. |

Semantics, restored from the pre-cut description of `getSessionTranscript`:

- In tail mode `messages` is at most `limit` newest messages in canonical
  ascending sequence order. `cursor` is the exact committed head observed
  before message selection, so the Conversation stream opened with it delivers
  everything committed afterward with neither overlap nor gap.
- `conversation` is the point-in-time resource, including `active_turn_id`
  and `active_turn_status`. This is how a resumed page learns a Turn is
  mid-flight before the first frame lands.
- Each older page is ascending, carries `has_more` and `next_page_token`, and
  carries the original `cursor`. Older paging never moves the resume position.
- Without `tail` or `page_token` the operation is unchanged.
- `page_token` is deliberately not spelled `cursor`. DIRECTION's one-name rule
  is about the resume position; a paging continuation is a different value
  and giving it the same name is how a client sends one where the other is
  expected.

Not proposed: `tail` or `order` on `listConversationMessages`. One bounded
read is enough, and a second spelling of the same capability is the
redundancy DIRECTION refuses.

Not proposed: the pre-cut tail's per-Turn lifecycle records. The controller
needs only the active Turn, which `conversation` supplies. If a renderer later
needs why a settled Turn stopped, that is a separate proposal.

**This repository, once published:** `make sdk-generate`, then
`make sdk-generate-check`, `make openapi-check`, `make facade-check`. The
operation stays in `RAW_ONLY`, so parity needs no change. `sdk/operations.json`
is regenerated from the contract and tracks IDs, methods, and paths only, so
it does not change.

**If the service change lags:** the controller's bootstrap code path is the
same call without `tail` and `limit`, trimmed client-side to the newest 50.
Correct, not bounded on the wire. Ship it behind the same function so the two
parameters are the only diff, and do not call the feature done until the
bounded form is in.

## SDK changes

### S1. `stream.ts`

Port from the donor's `stream.ts` diff, onto `main`'s rewritten `Reducer`:

- `ReducerOptions { initial?: Partial<ReducedSnapshot>; maxMessages?;
  maxPreviews?; maxPreviewBytes? }`. Constructor seeds from `initial` and
  enforces bounds. `positiveBound()` validation throws `NvokenError`
  `validation`.
- `merge(snapshot: Pick<ReducedSnapshot, "messages" | "turnChanges">)`: fold
  a REST page in without touching `cursor`.
- `appendUTF8()`: truncate a preview at a code-point boundary, never mid
  code point.
- Message eviction removes only whole terminal Turn boundaries, oldest first,
  and stops at a nonterminal boundary. Evicting a Turn's messages also drops
  its folded change.
- Preview eviction drops the oldest preview past `maxPreviews`.
- `StreamLoopOptions.onConnectionChange?: (state: "connected" |
  "reconnecting") => void`, wired in `reconnectingFrames` per D7.
- Do not port `latestChangesOnly`.

### S2. The reducer fold, in four languages

`Reducer.changes` keys by `turnId` and keeps the highest `revision`.
`snapshot().turnChanges` returns one change per Turn. `TurnHandle.updates()`
and `mergeStreamSnapshot()` in `client.ts` keep working unchanged; simplify
their sort-and-take-first to a find once the fold is in.

Apply the same fold to `sdk/go/stream.go`, the Python reducer, and the Rust
reducer, and update `sdk/conformance/fixtures/reducer.json`'s
`expected.turn_revisions` to the folded set. Only TypeScript reads that
fixture today; the other three should gain the same assertion in the same PR
so the fixture cannot drift from them. Note the fold in the streaming
reference where it describes `turn_changes` as a log.

### S3. `Turn.admission`

In `facade-types.ts`:

```ts
export interface TurnAdmission {
  readonly idempotencyKey: string;
  readonly deduplicated: boolean;
  /** Null for a standalone Turn. */
  readonly conversationId: string | null;
}

export interface Turn<TOutput extends object = JsonObject> {
  readonly id: string;
  /** Present on a Turn returned by start(); absent on turn(id). */
  readonly admission?: TurnAdmission;
  // ...unchanged
}
```

`Client.admit()` passes `resource.conversationId` into `TurnHandle` alongside
the existing `idempotencyKey` and `deduplicated`. `TurnResult.admission` keeps
its shape and gains `conversationId` from the same object. `bindTools()`
preserves it. Add a facade test that `start()` with `continue_or_create`
reports the resolved id and `turn(id)` reports `undefined`.

### S4. An internal request seam on the browser client

`BrowserClientHandle` in `browser.ts` owns a private `Client`. Add an
`@internal` module-level function the controller imports from `browser.ts`:

```ts
/** @internal */
export function browserRequest<T>(
  client: BrowserClient,
  operation: () => Promise<T>,
  signal?: AbortSignal,
): Promise<T>
```

It reaches the underlying `Client.request()` through a private symbol or
`WeakMap` keyed by the handle, so the public `BrowserClient` interface does
not change and a caller-supplied fake `BrowserClient` in tests still works
by falling back to `normalizeError` without retry. Every `raw()` call in the
controller goes through it. Do not add `request()` to the public interface.

### S7. `Turn.interrupt()` in four languages

Add to the `Turn` facade in TypeScript, Go, Python, and Rust:

```ts
interrupt(signal?: AbortSignal): Promise<TurnSnapshot<TOutput>>;
```

It calls `interruptTurn` with an empty body through the SDK's retry wrapper
and returns the snapshot of the Turn's state after the request. It never
waits for settlement; the caller follows `updates()` or `result()` for that,
which is what the contract says to do. Interrupting a finished Turn returns
it unchanged and does not throw.

Remove `interruptTurn` from `RAW_ONLY` in `check_facade_parity.py`; it has no
query or body parameters, so parity passes by construction. Add a facade test
in each language and one shared conformance fixture entry if the conformance
server exposes interrupt. Mention it in each SDK's README next to
`result()`.

### S5. `conversation-controller.ts`

New file, exported from `browser.ts` so the public path is
`@deepnoodle/nvoken/browser`. Not exported from `index.ts`; the donor's
removal of `export * from "./browser.js"` in `index.ts` is dropped because
`main` never had that line.

**Constructors**

```ts
export function createConversation(options: {
  client: BrowserClient;
  conversation: BrowserConversationSelection;
}): ConversationController;

export function createAnonymousConversation(options: {
  baseUrl: string;
  appId: string;
  storage?: "local" | "session" | "memory" | ConversationStorageAdapter;
  fetch?: typeof globalThis.fetch;
  clock?: ConversationClock;
}): ConversationController;
```

**Controller interface**, from the donor minus `erase()`:

```ts
getSnapshot(): ConversationSnapshot;
subscribe(listener: () => void): () => void;
send(input: TurnInput): Promise<ConversationSendReceipt>;
retrySend(): Promise<ConversationSendReceipt>;
retryAuthorization(): Promise<void>;
interrupt(): Promise<void>;
reconnect(): Promise<void>;
loadEarlier(): Promise<void>;
startOver(): Promise<void>;
destroy(): void;
```

`ConversationSendReceipt` is `{ turnId: string; conversationId: string;
deduplicated: boolean }`, filled from `turn.admission`.

**Snapshot**, from the donor with these renames and removals: `sessionId` to
`conversationId`; `lifecycles: readonly TurnChange[]`, one per Turn;
`activity` carries `turnId` and `turnStatus`; `interruption` carries `turnId`;
`erasure` removed; `ConversationDisabledReason` loses nothing but
`session_missing` becomes `conversation_missing` and `no_invocation` becomes
`no_turn`. `ConversationRecovery.session_missing` becomes
`conversation_missing`. Everything is frozen and structurally cloned as the
donor does.

**Authority**, ported near-verbatim. `HostAuthority` produces
`BrowserTurnOptions` with the bound `conversation` selection and the
idempotency key. `AnonymousAuthority` produces options with no `conversation`.
Everything else in `AnonymousAuthority` carries over on renames: Web Locks
coordination, in-process flight dedup, versioned opaque continuity, storage
degradation to memory, renewal against an expiry margin, the bounded retry
horizon honouring `Retry-After`, the `exposedSession` guard renamed
`exposedConversation`. `AnonymousTokenResponse.sessionId` is now
`conversationId`.

One ordering fix in `startOver()`: exchange with no visitor token first, and
overwrite stored continuity only after the exchange succeeds. The donor
deletes first, which discards continuity on a transient failure; the contract
says never to discard a stored visitor token because of a network, 429, or
5xx response.

**Bootstrap**, per D2. `bootstrap(conversationId)`:

1. `connection = connecting`.
2. `browserRequest(() => raw().conversations.getConversationTranscript({
   conversationId, tail: true, limit: 50 }))`.
3. Construct `Reducer` with `initial: { messages, cursor }`, and the three
   bounds. Record `nextPageToken` when `hasMore`.
4. Seed activity from `conversation.activeTurnId` and `activeTurnStatus`
   as the "conversation claim" in `resolveActivity()` terms: live until the
   stream reports on that Turn.
5. Mark the anonymous authority's Conversation exposed.
6. Start the stream from `cursor`.

`not_found` on the snapshot resets to no Conversation and sets
`recovery: conversation_missing`. `authentication` sets authorization lost.
`permission` disables reconnect with `not_authorized`. Anything else is
`connection: error` and `recovery: connection_exhausted`.

**Stream.** `client.conversationFrames(conversationId, { cursor, deltas:
true, signal })`, with `onConnectionChange` threaded through `RawStreamOptions`
into `StreamLoopOptions`. The controller owns the reducer, applies every
frame, and publishes. When the stream throws after the reconnect horizon, set
`connection: error`, `recovery: connection_exhausted`, enable `reconnect`.
The `online` listener calls `reconnect()` when it is enabled.

**Activity.** `resolveActivity(reduced.turnChanges, conversationClaim,
admittedClaim)` from `activity.ts`. Do not reimplement the fold. A status that
is nonterminal and not one of `queued`, `running`, `waiting`, `budget_hold`
reports `activity.status = "unknown"` and disables `send` with
`conversation_active`. `isTurnOver` decides terminal, never a local set.

**Send.** `requireEnabled(send)`, then store `pendingSend` with a cloned input
and a fresh idempotency key, then `client.start(input, authority.turnOptions(
idempotencyKey))`. On success the receipt comes from `turn.admission`; if
`admission.conversationId` is null in host mode, throw `unexpected_response`,
because a bound selection cannot produce a standalone Turn. If the controller
had no Conversation, bootstrap it. On failure, classify with `normalizeError`:

- `TurnTimeoutError` from `admit()`, or any `transport`, `timeout`,
  `server`, `rate_limit`, or `authentication` category: `pendingSend.status =
  "uncertain"`, keep the input and key, enable `retrySend`. This builds on
  `admit()`'s existing behaviour rather than duplicating it.
- `conflict` with code `conversation_active`: clear `pendingSend`, then
  re-read the transcript snapshot to refresh the conversation claim, so the
  composer disables for the right reason instead of showing a generic error.
- `permission`: `sendDenied`, disabled `not_authorized`.
- Anything else: clear `pendingSend`, surface as `send.status = "error"`.

`retrySend()` reuses the stored input and key exactly.

**Interrupt.** `client.turn(turnId).interrupt(signal)`. The response is the
Turn's state after the request; the stream is still the source of truth for
settlement, so do not fold the response into the reducer.

**Load earlier.** `getConversationTranscript({ conversationId, pageToken,
limit: 50 })`. Count new sequences; if the window would exceed 500, set
`history_window_full` and do not merge. Otherwise `reducer.merge()` and
advance the page token. The live cursor never moves.

**Start over.** Anonymous only. Stop the stream, replace the visitor per the
ordering fix above, reset the Conversation, bootstrap if the new grant names
one.

**Destroy.** Abort lifetime, stop the stream, destroy the authority, remove
the `online` listener, clear listeners, publish a final snapshot with
`recovery: destroyed`. Silent and final; a second call is a no-op.

### S6. Public surface

- `browser.ts` re-exports the controller: the two constructors, every
  `Conversation*` type, `ConversationStorageAdapter`, `ConversationClock`.
- `package.json` `files` gains `dist/conversation-controller.{js,d.ts}` and
  does not gain `dist/browser-client.*`.
- `public-surface.test.ts` asserts the controller is reachable at `/browser`
  and not at the root.

## Tests

Port the donor's twelve scenarios in
`sdk/typescript/src/test/conversation-controller.test.ts`. They are wire-level
fakes with hand-built SSE frames, so the situations hold; payload shapes and
routes change. Renamed and retargeted:

| Donor scenario | Change |
| --- | --- |
| host controller admits once and resumes the acknowledged Session | Host mode takes `conversation: { id }`; bootstrap hits `/transcript?tail=true&limit=50`; receipt from `turn.admission`. |
| unknown nonterminal lifecycle stays explicit | Unchanged in substance. |
| host authorization loss recovers through the token callback | Unchanged. |
| a missing optional operation disables only that action | `Turn.interrupt()` receiving 403 disables interrupt only. |
| anonymous controllers coordinate one exchange | `conversationId` on the token response. |
| anonymous expiry uses request elapsed time | Unchanged. |
| anonymous renewal honours Retry-After | Unchanged. |
| throwing anonymous storage degrades to memory | Unchanged. |
| anonymous erase retains continuity while start-over replaces the visitor | Erase half removed. Add: start-over on a failed exchange keeps the old visitor token. |
| older history never moves the live cursor and stops at 500 | Pages come from `/transcript?page_token=`. |
| bounded reducer keeps latest lifecycles and evicts only the oldest terminal boundary | "Latest" is now the reducer's only behaviour. Move to `stream.test.ts`. |
| browser APIs are canonical to the browser subpath | Unchanged. |

New tests this plan adds:

- Bootstrap seeds `activity` from `conversation.active_turn_id` before any
  frame arrives, and `interrupt` is enabled in that window.
- A `raw()` call under a browser token sends `Authorization: Bearer` from the
  token resolver and no scope headers. This is the "should hold, deserves a
  test" item from the preceding plan.
- A `raw()` call that returns 503 then 200 succeeds through `browserRequest`.
- `send()` receiving 409 `conversation_active` refreshes the snapshot and
  disables send with `conversation_active`.
- `Turn.admission` on `start()` and `undefined` on `turn(id)`.
- `Turn.interrupt()` posts an empty body, returns the post-request snapshot,
  and does not throw for a Turn that already ended.
- Reducer fold: two revisions for one Turn yield one change; the shared
  fixture's `turn_revisions` matches in Go, Python, and Rust.
- `onConnectionChange` fires `connected` after connect and `reconnecting`
  before a retry delay, and a throwing callback does not break the loop.

Update the browser fixture at
`sdk/typescript/test/browser/conversation-controller.html` for the new
constructor options and routes, or delete it if the Node tests cover the
same surface; do not leave it stale.

## Documentation

- `sdk/typescript/README.md`: replace the donor's "Headless conversation
  controller" section. Host example takes `conversation: { id }`. Receipt
  logs `turnId` and `conversationId`. Remove `erase()`. Say bootstrap is one
  bounded transcript read.
- `CHANGELOG.md` Unreleased: the controller; `Turn.admission`;
  `Turn.interrupt()` in all four SDKs; the reducer fold in all four SDKs;
  `onConnectionChange`; and, once the contract lands, the bounded transcript
  tail. One entry each.
- `docs/reference/streaming-protocol.md`: note the reducer fold where
  `turn_changes` is described as a log, and that resuming a page is one
  transcript read plus a stream from its cursor.
- `docs/README.md`: link this document.
- `examples/typescript-browser-direct/src/page.ts`: replace the `history()`
  helper that lists messages with the bounded transcript read, or add a
  second example that uses the controller. Keep the example compiling.

## Order of work

Each step is one reviewable PR against `main`. Steps 1 through 3 do not
depend on each other and can run in parallel. Step 4 needs all three. The
donor branch is a source to copy from, never a merge base; do not rebase it.

1. **Contract.** Service implements C1 and publishes `openapi/nvoken.yaml`.
   This repository adopts it: `make sdk-generate`, changelog entry, and the
   `page.ts` example update.
2. **Reducer and stream.** S1, S2 in four languages, fixture update, D7. Tests
   for bounds, fold, UTF-8 truncation, and the connection callback.
3. **Facade.** S3 `Turn.admission`, S4 `browserRequest`, and S7
   `Turn.interrupt()` in four languages with the parity script change. Facade
   tests.
4. **Controller.** S5, S6, D8, the ported and new tests, README, changelog.
5. `make check`, then `make sdk-check` against the conformance server.

If step 1 lags, step 4 lands with the unbounded fallback from C1 and a
follow-up PR switches on `tail` and `limit`. The feature is not announced
until that follow-up is in.

## Risks to verify, not assume

- **Anonymous grant permissions.** The contract does not state whether an
  anonymous grant may call `getConversationTranscript` or `interruptTurn`.
  Verify against the conformance server in step 4. If either is refused, the
  controller's per-action `not_authorized` handling covers it, but the README
  must say so.
- **Web Locks in tests.** `navigator.locks` is absent under Node; the donor
  falls back to the in-process flight map. Keep that fallback and test both
  paths.
- **`structuredClone` of `TurnInput`.** Media blocks with `Uint8Array` or
  `Blob` sources must survive `cloneInput()`. Test one.

## Out of scope

- `erase()` and any anonymous self-erase permission.
- `tail` or `order` on `listConversationMessages`.
- `listTurns` in bootstrap.
- `latestChangesOnly` as a reducer option.
- Any new `BrowserClient` method. `Turn.interrupt()` lands on `Turn`, which
  `BrowserClient.turn(id)` already returns.
- A React, Vue, or Svelte binding. Applications bring their own renderer.

## Follow-up raised by D0

Reading a Conversation's recent transcript is common for machine callers too,
and today it needs `raw()`. The facade has `Conversation.start/run/text` and
no read at all. Once C1 lands, a `Conversation.transcript({ tail, limit })`
returning the bounded snapshot, in four languages, would satisfy D0 for that
operation and let the controller drop its last `raw()` call. That is a facade
design decision for a separate document, not a reason to hold this series.
