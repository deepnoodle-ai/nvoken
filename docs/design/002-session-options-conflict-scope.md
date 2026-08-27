# Session options are a creation payload and a precondition at once

**Status:** Historical and superseded. Proposals 1 through 3 landed in the
pre-0.30 service; the 0.30.0 hard cut then replaced Session options with
explicit Conversation selection and removed the public Session and Invocation
nouns. This document remains the record of the production defect and the
reasoning it prompted, not a description of the current contract.
**Author:** Claude Opus 5 with Curtis Myzie
**Date:** 2026-08-13
**Revised:** 2026-08-26 to mark this pre-cut analysis as historical.
**Workflow:** Written from a production defect in a host application
**Applies to:** the service for the contract and runtime behavior; this
repository for the SDK error types

## Context

A host application runs one nvoken turn per user message. Its boards are keyed
conversations: `chat:<boardKey>` and `research:<boardKey>`, admitted with
`POST /v1/invocations` carrying `session_key`, so the host never has to know
whether the Session already exists.

Each admission also carried `session_options.metadata`, holding three entries:
`app:board`, `app:surface`, and `title`. The title followed the contract's own
guidance, which states that a title is a metadata entry rather than a Session
field.

Every board broke on its second message. The turn was rejected before an
Invocation existed, the SSE stream carried

```json
{"type":"error","message":"The supplied Session options conflict with the stored Session."}
```

and the user's message disappeared from the thread. Chat had been unusable past
the first exchange for eleven days.

## The mechanism

A board is created by the host's page loader with the placeholder title
`"Untitled board"`, before any message exists to name it. The first message
admits a turn, so the Session is created storing
`metadata.title = "Untitled board"`. That same request then renames the board to
the message text.

The second message therefore sent a title that disagreed with the stored one,
and `session_options_conflict` rejected the whole turn. The board's title is
descriptive data nvoken never reads, and it took down a paid, user-facing
conversation.

The host code was wrong, and it was wrong in an interesting way. The comment
above the call read:

```ts
// Applied only when this admission is what creates the Session; a later
// turn naming an existing session carries them and they are ignored, so
// `updateSession` owns changes after the first turn.
```

That is the ordinary create-or-get convention, and it is the opposite of what
nvoken does. The contract is not at fault for being unclear: both
`SessionOptions` and `CreateInvocationRequest` describe compare-not-apply
plainly and correctly. An implementer read accurate documentation, wrote a
confidently wrong comment inches from it, and shipped. When a shape reliably
invites the wrong assumption, better documentation is not the remedy.

## Why the shape invites it

`session_options` serves two roles that want opposite values:

- As a **creation payload**, it should carry the caller's *current* values.
  Those are what get stored.
- As a **precondition assertion**, it should carry the *stored* values. Those
  are what get compared.

The moment any member drifts, no single value satisfies both roles. The caller
is asked to send one field that must simultaneously mean "here is my state" and
"here is my belief about your state."

`session_key` compounds this. Its entire purpose is to let a caller admit a turn
without knowing whether the Session exists. `session_options` then requires
exactly that knowledge, because what a caller ought to send depends on the
answer. One request tells the host both that existence is not its concern and
that it had better keep track.

The same request already demonstrates the gentler policy. On
`CreateSessionRequest`, `seed_messages` resolving to an existing keyed Session
"returns it unchanged and never appends these messages" — creation-time input,
silently ignored. `session_options` on that same request is fatal. Two
creation-time payloads, opposite policies, no stated reason for the split.

Finally, the enforcement granularity is uniform where the stakes are not.
`SessionOptions` bundles `compaction`, `retention`, and `metadata`. Silently
reconfiguring a compaction policy costs money and changes what the model sees.
Overwriting an opaque string does neither. The strictest policy justified by the
most consequential member was applied to every member.

## Goals

- Make it impossible for purely descriptive data to fail a turn.
- Keep the protection against two callers silently reconfiguring each other's
  conversation, where that protection is worth a rejection.
- Make the remaining conflicts diagnosable from the error alone.
- Change nothing for callers who send consistent options today.

## Non-goals

- Removing `session_options` from the admission request. Setting options at
  creation without a second round trip is worth keeping.
- Making Session configuration freely mutable per turn. A turn that quietly
  rewrites a stored compaction policy is the failure this rule exists to stop.
- Adding a way to read whether a Session exists before admitting a turn. That
  would restore the round trip `session_key` removes.

## Proposal

The organizing principle: **reject only where ignoring would be worse than
rejecting.**

### 1. Stop conflict-checking `metadata`

On an existing Session, merge supplied metadata the way
`PATCH /v1/sessions/{session_id}` already does — present key replaces, absent
key survives — or ignore it outright. Never reject.

The contract states that nvoken "stores it, returns it verbatim, and never
interprets it." Asserting equality on data the runtime never reads protects
nothing, and its only reachable outcome is a failed turn. Metadata is also the
one member the contract explicitly expects to change over a Session's life,
since it names `title` as the intended way to label a conversation.

Merging is the better of the two. It gives a keyed admission the same effect a
host would get from a follow-up `PATCH`, removes the need for hosts to make that
second call at all, and keeps drift from accumulating.

### 2. Keep the conflict for `compaction` and `retention`

Both change what a turn costs or how long durable state survives. Silently
ignoring a policy a caller asked for is the worse failure, so rejection remains
correct. No change.

### 3. Name the disagreement in the message

Today's message is

> The supplied Session options conflict with the stored Session.

It does not say which path disagreed, so the first diagnostic step is a
guess. `details.conflicting_paths` carries the answer, but a host has to know
the field exists to look — in this incident it was found by reading generated
model source after the cause was already understood.

Put the paths in the message, and the stored and supplied values with them:

> Session options conflict at `/session_options/compaction`: stored
> `trigger_tokens: 40000`, supplied `trigger_tokens: 80000`.

This single change would have reduced that investigation to reading one
error.

### 4. Give the SDKs a typed conflict error

*Deferred. The two claims under this proposal did not survive review; the
reasons are recorded here rather than in a separate document.*

`SessionBusyError` is the precedent: a 409 with its `details` promoted to typed
fields, so callers branch on a class rather than on a string code. Add
`SessionOptionsConflictError` alongside it in all four SDKs, exposing
`conflictingPaths`.

The host now retries admission once without `session_options` when it sees
this code, because correlation metadata is never worth failing a paid turn
over. That
recovery is reasonable and general, and today it requires matching
`error.code === "session_options_conflict"` against a string literal.

**The precedent is one SDK, not four.** `SessionBusyError` exists only in
TypeScript (`sdk/typescript/src/client.ts`). Go returns a single `*nvoken.Error`
carrying `Category`, `Code`, and `Details`; Python raises one `NvokenError`;
Rust returns one `NvokenError` struct. Adding a class to those three would
invent an error hierarchy for one code rather than follow a convention.

**Proposal 1 dissolves the motivating case.** Retrying without
`session_options` was a workaround for metadata failing a turn, and metadata
stops conflicting. What remains is `compaction` and `retention`: real
disagreements a host should fix rather than catch and retry.

If typing is still wanted later, the smaller and more uniform move is to export
the error-code constants from each facade — none of the four expose them today,
so every caller compares a string literal for every code, not just this one.
That is a separate change with a broader reason.

## Alternatives considered

### Make the assertion opt-in

Default `session_options` to create-if-absent, ignore-if-present, and add
`expected_session_options` for callers who genuinely want a precondition. This
is the cleanest separation of the two roles: each field would mean one thing.

Rejected as the primary proposal because it silently weakens an existing
safety property for every current caller, including ones relying on it without
having said so. Proposal 1 fixes the case that can only cause harm and leaves
the rest intact. Worth revisiting if hosts turn out to want assertions on
metadata after all.

### Admit the turn and report what was ignored

Accept the admission, apply nothing, and surface
`ignored_session_options: ["/session_options/compaction"]` on the Invocation.
This fits nvoken's stated principle that anything decided on your behalf is
readable on the resource that used it.

Rejected for `compaction` and `retention`: a host that asked for a cost-bounding
policy and got a readable note instead of an error will discover the difference
on an invoice. Reporting is the right treatment for data with no consequences,
which is what proposal 1 does by merging instead.

### Document it harder

An explicit warning in `SessionOptions`, or a named example of the drift
failure. Rejected as sufficient on its own: the documentation was already
correct and precise, and it did not prevent the defect. Worth doing alongside
whichever proposal lands.

### Leave it and let hosts adapt

Defensible, since the behavior is documented and the fix on the host side is
small once understood. Rejected because the failure mode is severe out of
proportion to the mistake — a stale descriptive string kills a paid turn — and
because every host that hits it will hit it in production, on the second turn of
a conversation, after the first one worked.

## Tradeoffs and consequences

- Merging metadata on admission is a behavior change for any host currently
  relying on the rejection to detect drift. No known host does; the host in
  this incident was broken by the rejection rather than protected by it.
- It gives metadata two write paths, admission and `PATCH`, with the same merge
  semantics. Consistent, and one more thing to keep consistent.
- Richer error messages carry stored values into error responses. For
  `compaction` and `retention` these are host-supplied configuration rather than
  transcript content, so it does not widen what an error discloses. Metadata,
  which could hold anything, stops being a conflict source under proposal 1 and
  never appears in these messages.
- A new SDK error class is additive across four languages and one more type to
  keep in conformance fixtures.
- Hosts that already send consistent options see no change at all.

## Rollout

1. Land the metadata change in the service: merge on admission against an
   existing Session, restrict `conflicting_paths` to `compaction` and
   `retention`, and update the `SessionOptions` description to state which
   members are asserted and which merge.
2. Enrich the conflict message with paths and values.
3. Adopt the published contract here and regenerate, once step 1 has merged. No
   SDK error class ships; see proposal 4.
4. Note the change in `CHANGELOG.md`. Existing callers need no migration; hosts
   working around the old behavior can drop their workarounds at their leisure.

## Open questions

- ~~Does `POST /v1/sessions` with a `session_key` currently conflict on
  `session_options`, or ignore them the way it ignores `seed_messages`?~~
  Answered: it conflicts. Both endpoints call the same
  `reconcileSessionOptions` after resolving the keyed Session, so they already
  agree and the fix covers both in one place. `seed_messages` differs because
  it is skipped in a separate branch guarded on whether this transaction
  inserted the Session.
- Should `retention` merge rather than conflict? Its argument is weaker than
  compaction's, since a longer or shorter idle window is recoverable and never
  changes what the model sees. Left strict here for lack of evidence either way.
- Is there a real caller who wants a precondition on metadata? If one appears,
  `expected_session_options` from the alternatives is the way to serve it
  without restoring the trap.
