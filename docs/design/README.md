# Design

**Status:** Active design program
**Started:** 2026-07-20

nvoken is an agent runtime as a service: a durable Responses API with Session
persistence, tool execution, cross-provider model routing, and execution
observability.

This directory is the single entry point for the active runtime design: the
claims register, the vision narrative, the execution architecture, and the
endpoint-level API contract. [docs/product/](../product/) holds the public-facing
distillation of the same material; this packet governs when the two differ in
detail.

The packet was ported from the design program for nvoken's predecessor runtime
(Mobius, which powers [MobiusOps.ai](https://mobiusops.ai)) and adapted for
nvoken. Its supporting research remains in that private repository.

## Start here

Read the active documents in this order:

1. [`vision.md`](vision.md) — the narrative: product thesis, scope, and
   success criteria, introducing the core claims.
2. [`claims.md`](claims.md) — the claims register in two sets: external
   claims (advertised; the website is driven from them) and internal claims
   (binding constraints on architecture and build). One confirmable claim
   per entry.
3. [`architecture.md`](architecture.md) — execution model, ownership boundaries,
   durability, identity, and rollout architecture.
4. [`api.md`](api.md) — complete endpoint-level contract, separated by API
   audience. Exact schemas for the frozen first Runtime slice live in
   [`openapi/runtime.yaml`](../../openapi/runtime.yaml).

[`decisions.md`](decisions.md) is the running list of standing context: the
inputs and decisions that shaped the packet. It is useful background, but it
is not required reading once the four active documents are understood.
Language-neutral compatibility fixtures for the admission idempotency hash
live in [`admission-fingerprint-v1.json`](admission-fingerprint-v1.json) and
[`admission-fingerprint-v2.json`](admission-fingerprint-v2.json). New
structured-output admissions use the v3 vectors in
[`admission-fingerprint-v3.json`](admission-fingerprint-v3.json). New
host-tool admissions use the v4 vectors in
[`admission-fingerprint-v4.json`](admission-fingerprint-v4.json).
New callback-tool admissions use the v5 vectors in
[`admission-fingerprint-v5.json`](admission-fingerprint-v5.json).
Per-provider credential selections use the v6 vectors in
[`admission-fingerprint-v6.json`](admission-fingerprint-v6.json); secret bytes
and materialized defaults are deliberately absent from those fixtures.
The current wire vocabulary, string input normalization, timeout trio, and host
tool mode use the v7 vectors in
[`admission-fingerprint-v7.json`](admission-fingerprint-v7.json). Versions one
through six remain readable only so already-admitted durable rows retain their
original equality semantics. Contract changes that do not alter admission
material continue to use v7; the next material shape uses v8 and must add its
own fixture without rewriting or deleting v1–v7.

## Document authority

| Concern | Governing document |
| --- | --- |
| Product commitments, external and internal | [`claims.md`](claims.md) |
| Product narrative, scope, and explicit cuts | [`vision.md`](vision.md) |
| Runtime semantics and system ownership | [`architecture.md`](architecture.md) |
| Public endpoints and API separation | [`api.md`](api.md) |
| Original questions and decision history | [`decisions.md`](decisions.md) |

`claims.md` is the apex: core commitments live there as confirmable claims,
and the other documents elaborate them with narrative, mechanics, and
process. Below the register, every normative fact has exactly one home: it
is stated in full in its governing document, and other documents reference
it by name (for example, "per the trust boundary in `vision.md` §8") rather
than restating it. When a decision changes, update its home, record the
change and rationale in `decisions.md`'s decision log, and fix any references
in the same change — a confirmed claim changes only through a decision-log
entry. Change-relative language ("no longer", "removed", "superseding")
belongs only in `decisions.md`; the other documents describe the design, not
its history. Do not add a new strategy or specification file beyond these
unless none has a clear home for it.

## Supporting research

Research evidence lives in [docs/research/](../research/), date-stamped per the
repo convention. Research is evidence, not contract: if research conflicts with
an active document, the active document describes the current decision.

## Working rule

All new implementation work should begin from this directory. The predecessor
runtime's code and documents remain useful implementation references, but this
packet governs when the product shape differs.
