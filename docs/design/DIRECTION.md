# Design direction: converge on one protocol

**Status:** Standing direction. Set by Curtis Myzie on 2026-08-13. It governs
every later change to the streaming protocol and the contract, and it outranks
any individual design document in this directory.
**Applies to:** `openapi/nvoken.yaml` and the runtime behind it; the SDKs,
conformance fixtures, and reference documentation in this repository.

## The direction

Eventually we need the final design, the one that carries no pollution from the
legacy protocol. We need to be aiming for that.

That is the target. Not a protocol whose rough edges have been sanded one at a
time, but the protocol we would design today if there were no installed base.
Every change from here is measured against it.

The test any proposal has to pass: if we were starting now, with nothing to
stay compatible with, would we write it this way?

## What this changes about how we work

A change that adds a way to do something without retiring a way needs a reason
in writing. Redundancy is a cost, not a feature. Two spellings of one value,
two signals for one event, two schemas for one resource: each is something
every future client author has to learn, and something every future change has
to preserve.

Writing a redundancy down as intentional is worse than leaving it undocumented.
It turns an accident into a promise.

## Design 003 was not written with this in mind

[Design 003](003-streaming-protocol-target.md) is called a target and is not
one. It is a remediation list against the protocol we happen to have. It fixed
real defects and it is worth keeping. It never asked what the protocol should
be.

In two places it made the pollution permanent. It states that all four
spellings of the resume cursor are interchangeable, and that either terminal
signal is a valid exit. Before 003 those were accidents. After it they are
contract, and harder to remove than they were.

It also added a fourth way to learn about a tool call and retired none of the
three.

Read 003 as what it is: a set of fixes that landed, and the record of why. Do
not read it as the end state. Its own name is misleading and this note is the
correction.

## Known pollution

Where the protocol says one thing twice. This is the starting point for the
end-state design, not the design itself.

1. **Two schema families for one resource.** Machine callers and browser
   callers get different projections of the same Invocation, Session, and
   transcript. Which arm of the response union you receive is decided by your
   credential, not by anything in the payload, so the union cannot be decoded
   from the contract alone. `sdk/scripts/generate.sh` carries a
   post-generation patch that works around this on every build. This one
   breaks design 003's own first property, that a stranger holding only the
   contract can write a correct client.

2. **Three stream entry points that are one stream.** Cursors are
   Session-scoped on both `GET` routes and a position taken from one resumes
   the other, so the Invocation stream is a filtered view of the Session
   stream that we ship as a separate endpoint. The inline `POST` form is a
   third, and our own deployment target buffers it until the turn settles.

3. **Two frame vocabularies for the same facts.** `invocation.accepted`,
   `invocation.update`, and `invocation.result` on one route;
   `transcript.update` on the other.

4. **Two tool-call collections.** `pending_tool_calls` is `tool_calls`
   filtered to host mode and pending status, with three more fields.

5. **Two terminal signals.** `stream.end` with reason `terminal` describes a
   connection closing. `invocation.result` describes a turn ending. A client
   has to handle both because we conflated them.

6. **Two protocol spellings of the resume position.** `resume_cursor` in the
   frame body and the `cursor` query parameter: one value, two names. The SSE
   `id` line and the `Last-Event-ID` header carry the same value again, but
   they are the SSE binding's own mechanics and belong to any faithful SSE
   binding, so the pollution is the two protocol names, not four spellings.
   This entry originally counted all four; the correction is recorded in
   [design 004](004-protocol-end-state.md).

## What comes next

An end-state design that specifies the protocol with no compatibility budget,
in its own section, standing alone as the thing later changes are measured
against. How to get there from here belongs in a separate section of the same
document, and is the smaller problem.

That document now exists:
[design 004, the end-state protocol](004-protocol-end-state.md). Its Part 1
is the measuring stick this section asked for; its Part 2 is the path.

**There are no external users.** Curtis is the only consumer of `/v1`,
confirmed on 2026-08-13. So nothing needs a deprecation window and none of
that machinery gets built: no `deprecated` markers, no `Deprecation` or
`Sunset` headers, no SDK warnings. There is nobody to signal. Every collapse
removes the old way in the same change that adds the new one, and we are
switching now rather than staging it, because this is the cheapest it will
ever be.
