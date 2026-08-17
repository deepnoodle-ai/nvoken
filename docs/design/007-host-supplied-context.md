# One home for each host-supplied fact

**Status:** Accepted on 2026-08-17. Rollout step 1 is implemented, as is the
service and contract half of step 2; the SDK facades, conformance fixtures, and
the step 3 items are not.
**Author:** Claude Opus 5 with Curtis Myzie
**Date:** 2026-08-17
**Revised:** 2026-08-17, after a review against the implementation. The review's
findings are folded into the sections below; where it corrected this document,
the correction is marked.
**Applies to:** the service for the contract and runtime behavior; this
repository for the SDK facades, conformance fixtures, and reference
documentation.
**Reading order:** [DIRECTION](DIRECTION.md) is the standing direction and
outranks this document. [Design 002](002-session-options-conflict-scope.md)
decided that `session_options.metadata` merges rather than conflicts; section 1
below changes that field again and explains why that is not a reversal.

## Context

nvoken accepts host-supplied labels in four places, and no two of them agree on
what they are for.

| Where | Lifetime | Enforced by nvoken | Reachable in a callback |
| --- | --- | --- | --- |
| `CreateInvocationRequest.metadata` | per turn, immutable | no | only by reading the Invocation |
| `session_options.metadata` | per Session, merges | no | no |
| `PATCH /v1/sessions` metadata | per Session, merges | no | no |
| `user_key` | per turn, retained on the Session | yes, when memory scope is `user` | no |

Three of those four are spelled or described as descriptive data nvoken never
interprets. One of them is a data partition the runtime enforces. None of them
reaches a callback receiver without a round trip.

That distribution is why a host application whose authorization boundary is
finer than a tenant ends up doing something nvoken never designed for. Its
boards are keyed conversations, one board per user, and a callback delivery has
to be authorized to a board. HMAC verification proves the delivery came from
nvoken. The signed envelope carries `delivery_id`, `tool_call_id`, `tool_name`,
`invocation_id`, `session_id`, `agent_key`, and optionally `tenant_key`. Every
one of those is either minted by nvoken or a resolution key, so the finest
boundary the envelope offers is the tenant.

So the host writes its board key into the Invocation's `metadata` at admission,
and on every callback delivery reads the Invocation back to recover it. The tool
input cannot be trusted for this, because the model wrote it. The rule the host
arrived at is the right one, and it had to derive it unaided: a value in tool
input may only agree with the authoritative binding, never establish it.

Two things are wrong with that. The value is host-supplied and immutable and is
already stored, so it could have travelled inside the signed body instead of
costing a read on the hot path of every delivery. And `metadata` promises none
of the properties an authorization anchor needs. It is documented as "opaque
host correlation data... it exists so a support engineer holding an Invocation
id can get back to the board, ticket, or surface the turn came from." Its
immutability is real but is a consequence of the idempotency fingerprint rather
than a stated guarantee, so a future ergonomics change to idempotency would
silently weaken a security control in someone else's system. Nothing states
whether the model can see it. Nothing states that only admission may write it.

DIRECTION asks what we would build with no installed base. With four homes for
host-supplied facts and no clear rule for choosing between them, the answer is
fewer homes, each with a stated job.

## What changes

### 1. Remove `session_options.metadata`

`SessionOptions` keeps `compaction`, `retention`, and `pinned_revision`, and
gains the field in section 4. Session metadata is written only by
`PATCH /v1/sessions/{session_id}`.

Design 002 asked whether this field should conflict or merge and answered
merge, because a stale title was killing paid turns. It did not ask whether the
field should exist here. Its own tradeoff list records the cost it accepted:
"It gives metadata two write paths, admission and `PATCH`, with the same merge
semantics. Consistent, and one more thing to keep consistent." DIRECTION is
explicit that this is a cost rather than a feature, and that a change adding a
way to do something without retiring one needs a reason in writing.

The reason the second path looked necessary was that authorization-relevant
labels had nowhere else to live, so they had to be written at the moment the
Session came into existence. Section 4 gives them somewhere. What remains in
Session metadata is descriptive: titles and the like, mutable by nature, and
`PATCH` owns mutable.

**Cost.** Labelling a Session at creation now takes a second call. On the
`session_key` path the Session is created by the turn, so the sequence is
admit, then `requireSessionId`, then `PATCH`. For a title that call is already
unavoidable, since a title is written after the first message exists and design
002 exists because a title in `session_options` was fatal. For anything else it
is one extra request against a Session the caller has just created.

**This also settles a question that has been open since 0.21.0.** With one
`metadata` on the turn and one on the Session, reached through different
operations, there is no longer a case for renaming the Invocation's field to
`correlation`. See "What we are not doing".

### 2. Remove the reserved `actor` member from the callback envelope

The signed envelope reserves an `actor` object with `principal_id` and
`principal_type`. The service has never populated it, an integration test
asserts it stays absent, and none of the four SDK callback modules mention it.

**Correction.** This document first said the envelope's shape "is not in
`openapi/nvoken.yaml` at all". It is: the contract defines it as the
`toolCallback` webhook, whose `ToolCallbackContext` sets
`additionalProperties: false` and is code-generated into all four SDKs. That
makes the case for removal stronger rather than weaker — the reservation lives
only in one Go struct, and the contract already forbids the member on the wire.
It also makes rollout step 2 larger than first written; see Rollout.

It was reserved against a future in which admission records an *authenticated*
actor claim, which means nvoken validating something a host forwards. That is a
real feature and a much larger one, and nothing about it has been designed.

An unfilled reservation for an undesigned feature is baggage. Remove it. If
delegated identity is built, it will be designed then, and it should not
inherit a wire shape chosen before the design existed.

Section 4 deliberately does not fill this slot. What it adds is host-asserted
and unverified, and a field named for a principal, arriving inside an
HMAC-verified body, would be read as something nvoken checked.

### 3. `user_key`: say what it is, and pin it to the Session

The contract calls `user_key` "a label rather than a name: it records who a
Session was opened for so you can filter on it later, and it is not a
permission boundary."

That last clause is wrong in a way that matters. `MemoryConfig.scope` takes
`tenant` or `user`, and the user scope "requires user_key on every admitted
Invocation." On such an Agent, `user_key` selects which durable memories the
model can recall. It is not an API permission boundary, which is what the
sentence means, but it is a partition on model-visible content, which is what a
reader will take it to mean it is not.

It is worse than that in combination with the field's lifetime. The contract
says "The Session retains the label from the request that opened it, while
later turns may identify different end users." Memory follows the Invocation.
So today one Session can pull two end users' memory partitions into one
conversation, and nothing objects.

Two changes:

- **Restate what `user_key` is.** A filter, and on Agents with `user` memory
  scope, the memory partition. Security-relevant on those Agents. Delete the
  "not a permission boundary" clause rather than qualifying it.
- **Pin it to the Session and check it.** The first request that opens a
  Session fixes its `user_key`. A later turn presenting a different one is
  refused rather than admitted. This needs its own error code rather than
  `session_options_conflict`, since `user_key` is a top-level member of the
  admission request and not part of `session_options`. A Session whose turns
  disagree about the end user has no correct interpretation, and the incorrect
  one leaks memory.

**The leak is durable, not per turn.** The review confirmed the mechanism: on a
`user`-scope Agent the rendered memory baseline is appended to the transcript
as a persistent Session message. Once one end user's turn injects their
memories, those memories replay on every later turn in that Session, including
another end user's. Admission is therefore the right layer to close it, and a
per-turn check would be too late.

**Pinning is to absent as well.** A Session opened with no label refuses a
later labelled turn rather than adopting the label. Install-if-unset is the
wrong default here: it would let a later writer bind a conversation it did not
open.

**Omitting the label is no assertion.** A later turn that sends no `user_key`
inherits the pin and is recorded under it. Requiring every turn to restate what
the Session already knows is ceremony, and on `user`-scope Agents it was
previously required on every admitted Invocation — which pinning makes
redundant. The rule is: supplied on the opening turn, optional afterwards, must
match the pin when present, taken from the pin when omitted.

**Forking inherits rather than accepts.** A fork copies the transcript,
including any injected memory baseline, so a child opened under a different end
user is this same leak through a side door. The child takes the parent's
`user_key`; the fork request does not carry the field at all, because under
that rule it could only ever restate the parent's value, and a field that can
only agree looks like a choice without being one.

**The database holds the rule too.** The Session identity trigger refuses an
`UPDATE` that changes `user_key`, so no execution path can move a conversation
to another end user by writing the column directly.

**One edge, deliberately left open.** User erasure nulls retained labels on
retained facts. What a pinned Session does after its end user is erased is not
settled here; refusing further turns on `user`-scope Agents is the likely
answer.

**Cost.** A Session cannot attribute individual turns to different speakers
through `user_key`. A host modelling a multi-participant conversation puts the
speaker in the Invocation's `metadata`, which is per turn and is the field for
descriptive per-turn facts. See "Alternatives considered" for the version that
kept per-turn attribution.

### 4. Add `session_options.authorization_context`

A map of host-supplied strings, with the same bounds as `Metadata`, and these
properties stated in the contract rather than implied:

- **Per Session, and checked.** Set by the request that creates the Session,
  whether that is `POST /v1/sessions` or the admission that brings it into
  existence. A later turn presenting a different value is refused with
  `session_options_conflict` and `details.conflicting_paths`.
- **Written at creation only.** There is no patch path and there will not be
  one. A later turn presenting a context on a Session created without one is
  refused, not installed — the same absent-value rule as `user_key`, for the
  same reason.
- **Omitting it is no assertion.** A later turn that sends no context passes.
  The mechanism is creation-time binding plus the signed echo; the check is a
  tripwire for host bugs, not the binding itself.
- **Never interpreted.** nvoken does not read it, route on it, or index it.
- **Never model-visible.** Not rendered into context, not readable by a tool,
  stated as a rule the runtime is held to rather than a fact about the current
  implementation.
- **Carried inside the signed callback envelope**, as a sibling of the `nvoken`
  object rather than a member of it, so authorization needs no read. Everything
  inside `nvoken` is a fact nvoken minted or resolved; keeping a host-asserted
  value outside that object is the structural form of the integrity-not-
  authentication sentence below.
- **Returned on the Session** to a machine audience, so an operator holding a
  `sess_` id can see what a conversation is bound to. It joins the
  browser-omitted set alongside `user_key`: it carries the host's own
  identifiers, and the browser caller is the end user those identifiers are
  about.

What nvoken guarantees is integrity, not authentication: what admission
recorded is what a signed delivery carries, unchanged. It does not verify the
values, and the contract must say so plainly, because a value arriving inside
an HMAC-verified body invites the opposite assumption.

The contract should also carry the usage rule, in these words or close to them:
a value repeated in tool input may only agree with the authorization context,
never establish it. Every callback receiver needs that rule, and today each of
them has to work it out alone. Ship the rule with the tool: expose
`authorization_context` on the verified-callback result in all four facades and
state the rule in the callback-receiver guide and the four SDK READMEs, beside
the existing "dispatch on the signed `tool_name`" rule.

**Forking supplies it rather than inheriting it.** A fork is a creation moment,
and the existing fork rule is that the child inherits neither compaction,
retention, nor metadata. Inheriting an authorization context would silently
authorize the child to the parent's binding, so the fork request carries its
own. `ForkSessionOptions.metadata` goes with section 1's rule that Session
metadata is written only by `PATCH`.

**Concurrency needs no new machinery.** Session creation on the `session_key`
path already inserts on conflict, re-reads, takes the Session row lock, and
reconciles options under it. A losing concurrent first turn with a different
context gets a deterministic `session_options_conflict`; one with the same
value proceeds. This is how `pinned_revision` already behaves.

**Why checked rather than merged, when design 002 was written from exactly that
trap.** 002's organizing principle is "reject only where ignoring would be
worse than rejecting," and it is satisfied here rather than violated. Ignoring
a differing authorization context means a delivery is authorized against a
binding the host did not send, which is the failure the field exists to
prevent. And unlike a title, an authorization context does not drift by
construction: it is fixed for the life of the conversation, so a disagreement
is a host bug that should surface as a refused turn rather than a silent
mismatch. The same reasoning is why `user_key` is pinned in section 3.

**Name.** `authorization_context` says what it is for. The objection is that it
sounds more authoritative than a host-asserted value is, which the guarantee
sentence above has to carry. `scope` was rejected because nvoken already uses
it for credential narrowing. `actor` and `principal` were rejected for section
2's reasons.

## Remaining items from the 0.21.0 review

These are unrelated to host context and are being addressed in the same period.

### 5. `session_options.on_conflict: "join"`

Today a `compaction` or `retention` mismatch against an existing Session is
always `session_options_conflict`. A caller who wants to reach whatever Session
already exists, without asserting anything about how it is configured, has no
way to say so. `on_conflict: "join"` says it. This is the surviving half of a
review item whose other half proposed renaming `session_options` to
`session_seed`; that rename is dropped, because the field is not seed data now
that its descriptive member is gone and its remaining members are assertions.

`join` relaxes `compaction` and `retention` only. It does not suppress the
`authorization_context` or `user_key` checks, and the contract says so: a flag
that turned off an authorization tripwire would be a bypass with a friendly
name.

### 6. `paused` becomes `budget_hold`

`paused` reads as an action someone took. It means a spending limit or
exhausted credits stopped the turn, and that it can continue once the limit is
raised. This is the same class of defect as `stream.end`, recorded as decision
6 in [design 004](004-protocol-end-state.md): a name that asserts something
false, corrected in prose everywhere it appears.

The reach is wider than that list. Beyond the status enum, the
`invocation.paused` webhook event, and `on_budget_exhausted: pause`, it touches
the `invocation_not_paused` error code, the paused-Invocation count on tenant
credits, the resume operation's prose, and roughly forty SQL occurrences
including a CTE named `paused`. With no installed base, rename all of them in
one migration, and rename the option value `pause` to `hold` in the same pass
so the option and the status agree. It ships on its own so the migration is
reviewable by itself.

*Amended 2026-08-17. This section originally chose `funding_hold`, and the
paragraph above still carries the premise that produced it: that the status
"means a spending limit or exhausted credits stopped the turn". That premise is
incomplete.* A turn is held when it opted into `on_budget_exhausted` and hit a
ceiling it could be resumed past, which is four stop reasons, not two:
`max_estimated_cost` and `insufficient_credits`, but also `max_iterations` and
`max_output_tokens`. A turn held after its sixth loop iteration has nothing to
do with funding, and nothing releases it but raising that ceiling.

So `funding_hold` passed this section's own voice test and failed its truth
test, on half the states it covers — it replaced a name that asserts nothing
with one that asserts something false, which is the defect this section cites
design 004 to condemn. The name is corrected to **`budget_hold`**, which agrees
with the option that produces it: after `pause` → `hold`, `on_budget_exhausted:
hold` settles a turn as `budget_hold`. "Budget" is already the word this
contract uses for the whole set of ceilings; "funding" is a word for two of
them.

The status is deliberately *not* split into funding and limit variants.
`stop_reason` is present on a held Invocation and already distinguishes all
four causes, so a second status would duplicate a distinction the payload
carries.

### 7. A per-turn output schema cannot be typed

`overrides.outputSchema` is typed against the handle's inferred output type, so
a schema built at runtime cannot satisfy it and the call requires a cast. The
escape hatch for dynamic schemas is currently a cast, which makes it not an
escape hatch. This is an SDK typing defect, not ergonomics.

## What we are not doing

- **Renaming `CreateInvocationRequest.metadata` to `correlation`.** The review
  asked for this on the grounds that three fields named `metadata` had three
  semantics. Two of the three are now one merge patch on the Session, section 1
  removes the second write path to it, and what remains is one field per
  resource, reached through different operations, in the shape every other
  agent API uses. `metadata` is named first in the contract's "Familiar names"
  section for a reason. The review itself offered "or keep `metadata`".
- **A fifth terminal status, `interrupted`.** It already exists as a stop
  reason on a `completed` settlement. Promoting it would make every
  `isTerminalStatus` caller re-learn the set to read something they can already
  read.
- **`InvocationHandle`'s public nine-argument constructor, and `raw` being both
  a method and a namespace.** Real, cosmetic, not scheduled.
- **`client.text()` and an inline `definition` on `AgentOptions`.** These are
  new API surface rather than fixes, and want their own decision.
- **`leaveWaitingOnMissingHandler` as an enum.** One boolean that reads as a
  sentence while its neighbours `ifActive` and `onBudgetExhausted` are enums.
  Small enough to fold into whichever release touches that call.

## Alternatives considered

### Sign the Invocation's `metadata` into the callback envelope

One wire member, no new field, and the round trip disappears. Rejected because
it keeps one field serving both correlation and authorization, which is the
conflation this document exists to end. It also provides none of the other
three properties: the value stays per turn rather than pinned to the Session,
nothing states it is invisible to the model, and its immutability remains a
side effect of idempotency. Signing it would make a field with no security
promises load-bearing in more places.

### Fold `user_key` into `authorization_context`

Tempting, since one is a subset of the other in shape. Rejected because nvoken
enforces `user_key` and does not interpret `authorization_context`. Collapsing
them would make runtime behavior depend on a field documented as opaque, which
is the same mistake this document is correcting, in the other direction.
`user_key` is also a query filter on sessions, invocations, admissions, and
usage facts, and a usage-aggregation dimension. Those uses are legitimate and
separate.

### Keep `user_key` per turn, and have memory follow the Session's

This preserves per-turn speaker attribution and still closes the memory leak,
by resolving the partition from the Session's retained label rather than the
Invocation's. Rejected because it leaves one field meaning two things depending
on which resource reads it, and a host would have to know that its per-turn
value is attribution while the Session's is the partition. Pinning is the
simpler rule and the review's judgment was that no real Session switches end
users mid-conversation.

### Leave `session_options.metadata` and document the difference

The current state, plus a clearer description. Rejected on DIRECTION's terms:
the redundancy is the cost, and documenting it as intentional turns an accident
into a promise.

## Rollout

Three changes, in this order.

1. **Removals and the `user_key` correction.** Drop
   `session_options.metadata`, `ForkSessionOptions.metadata`, the envelope's
   `actor`, and `ForkSessionRequest.user_key`. Restate `user_key`, pin it to
   the Session, and refuse a turn that presents a different one. The `user_key`
   correction is documentation of behavior that already exists and should not
   wait on the rest: it is wrong today, on a field that partitions
   model-visible content, and the failure it invites lands in a host's system
   rather than ours. The integration test asserting the old permissive
   behaviour is inverted, not deleted.
2. **`authorization_context`.** Contract, service, the contract's webhook
   schema, four generated models, four hand-written facades, the shared signing
   vector consumed by every conformance suite, the fixtures, and the callback
   reference documentation. Two pre-existing fixture defects are worth fixing
   while those files are open: an invocation-webhook fixture uses a
   `stop_reason` that is not in the enum, and a tool-call fixture's envelope
   example is missing `tool_name` — neither is asserted against, which is how
   both went stale. Add an integration test asserting `authorization_context`
   never reaches a generation request, mirroring the existing actor-absence
   assertion.
3. **The remaining review items.** `on_conflict: "join"` and the output-schema
   typing fix together; `budget_hold` (§6, amended) on its own for the migration.

## Open questions

- **Does `authorization_context` belong in the `invocation.ended` webhook and
  the ended-Invocation feed?** A reconciler matching an ended turn to its own
  ledger row wants a host key, and `metadata` already serves that. Adding a
  second field to those payloads for the same purpose would reintroduce the
  choice this document removes. Left out until someone needs it.
- **Should `retention` join `compaction` as a checked member, or merge?**
  Carried over unanswered from design 002. Its argument for strictness is
  weaker, since an idle window is recoverable and never changes what the model
  sees.
- ~~**Is `pinned_revision` conflict-checked?**~~ **Answered: yes.** It has been
  conflict-checked all along, reported at `/session_options/pinned_revision`.
  Only the `SessionOptions` description lagged, naming just `compaction` and
  `retention`; that sentence is corrected in rollout step 1.
