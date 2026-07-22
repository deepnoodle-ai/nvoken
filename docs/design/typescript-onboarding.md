# Local TypeScript onboarding

**Status:** Implemented; `@deepnoodle/nvoken` 0.1.0 published
**Author:** OpenAI Codex  
**Date:** 2026-07-22  
**Workflow:** Spec and build in parallel; the field report supplies the acceptance criteria.

## Context

The [local TypeScript onboarding field report](../research/2026-07-21-local-typescript-onboarding.md)
proved that the Runtime and TypeScript facade worked, but also found 33 distribution,
bootstrap, failure-semantics, ergonomics, and CI gaps. The implementation closed those
gaps and the reviewed 0.1.0 artifact is now public at `@deepnoodle/nvoken`. It also
sharpened estimated-cost enforcement now that durable model checkpoints can retain a
provider response before terminal budget settlement.

## Goals

- Make every documented install command truthful and ship a public, packed-artifact
  test for `@deepnoodle/nvoken` 0.1.0.
- Provide one clone-to-first-response path with disposable PostgreSQL 17, generated
  local secrets, migration, health verification, TypeScript chat, resume, and exact
  cleanup commands.
- Make the supported TypeScript quickstart configurable, visibly successful, and
  nonzero with safe actionable details on failure.
- Let a host obtain the exact Invocation's canonical messages and assistant text
  through the facade without generated-client imports or casts.
- Fail capped, known-unpriceable work before a provider call and prevent output from
  a failed or cancelled Invocation from influencing a later model turn.
- Continuously test source and packed-package onboarding, including success,
  failure, multi-turn context, and cross-process Session resolution.

## Non-goals

- Make the laptop topology a production profile or weaken the existing
  single-daemon and Google Cloud operating requirements.
- Turn estimated list-price evidence into preauthorization, billing, or a credit
  ledger.
- Copy assistant text onto the Invocation as a second result authority.
- Add framework-specific TypeScript adapters or change the Runtime's provider set.
- Publish other language SDKs as part of the TypeScript 0.1.0 release.

## Proposal

### Distribution and release

The package is corrected from the nonexistent `@deepnoodle-ai` scope to the
existing `@deepnoodle/nvoken` coordinate and has complete public package metadata,
explicit exports, public access configuration, and build/test gates before packing.
Version 0.1.0 was published interactively from the exact reviewed `main` revision. A
dedicated GitHub Actions workflow publishes a later exact `npm-vX.Y.Z` tag only when
the tag and `package.json` versions match; it uses npm trusted publishing for
`deepnoodle-ai/nvoken` and the exact workflow filename.

CI packs the package, installs the tarball into an otherwise empty TypeScript
project, compiles a facade-only consumer, and runs it against the deterministic SDK
conformance server. Registry verification remains a post-publication check; a green
repository build is never reported as public availability.

### Local development path

`deploy/local` owns a development-only Compose file, a secret-free environment
template, and a small Python configurator. Compose binds PostgreSQL 17 to localhost
and uses a profile-specific named volume. The configurator generates independent
Runtime, bootstrap Owner, and 32-byte delivery secrets without printing them, copies
exactly one selected provider key from the caller's environment, writes the ignored
root `.env` with mode 0600, and refuses to overwrite an existing file by default.

A single guide connects these artifacts to `nvokend migrate`, `nvokend serve`,
`GET /health`, the TypeScript chat example, Session-key resume, and volume cleanup.
It labels the topology disposable and keeps TLS, supervisors, backups, immutable
artifacts, failure drills, and availability claims in the production profiles.

### TypeScript facade and examples

The facade adds:

```ts
interface TextContentBlock { type: "text"; text: string }
isTextContentBlock(block): block is TextContentBlock
handle.listMessages(): Promise<SessionMessage[]>
handle.text(): Promise<string>
```

Both handle methods page canonical Session messages and filter by the exact
Invocation ID. `text()` joins assistant text blocks and reports an actionable client
error when a completed Invocation has no assistant text. It does not cache or copy a
result.

The quickstart requires `NVOKEN_API_KEY`, `NVOKEN_PROVIDER`, and `NVOKEN_MODEL`, uses
bounded output/iteration limits without an estimated-cost cap, performs two turns,
and prints each assistant response. Failed or cancelled work prints status,
Invocation ID, public error code/message, safe detail, and a structured-log pointer,
then exits nonzero. The chat example uses the same handle helper, accepts a durable
Session key, and documents message-derived idempotency and local wait semantics.

Node 20 remains the supported package runtime floor. Node 24 remains only the pinned
repository development and CI baseline.

### Estimated-cost and transcript semantics

The production Dive adapter exposes whether its provider/model registry has standard
USD pricing. When a request has `max_estimated_cost_usd` and pricing is known to be
absent, execution settles `failed` before credential resolution or a provider call:

```json
{
  "code": "budget_exceeded",
  "message": "Estimated cost is unavailable for the requested model.",
  "details": { "kind": "estimated_cost_unavailable" }
}
```

When pricing is available, the existing post-response cost calculation remains a
guardrail rather than a reservation. Unknown or non-USD cost evidence still fails
closed if an adapter could not decide before the call.

Model checkpoints remain canonical durability evidence, including checkpoints that
precede a later terminal failure. Public Session reads therefore remain lossless.
Generation input uses a narrower repository query: user messages remain eligible,
but assistant and tool messages belonging to a failed or cancelled Invocation do not
enter a later provider request. The message's `invocation_id` and the authoritative
Invocation status make this relationship observable without adding a competing
message state field.

### Continuous newcomer check

One Python orchestration test performs the non-trivial lifecycle work:

1. migrate a disposable PostgreSQL 17 database;
2. start the daemon from the documented local environment, verify `/health`, and
   assert the expected `process_started` capability fields;
3. pack and inspect the npm artifact;
4. install it into an empty TypeScript project and compile a facade-only consumer;
5. run the quickstart and chat example against the deterministic conformance double;
6. prove two-turn context, cross-process Session-key resume, invalid-credential and
   invalid-model failures, useful identifiers, and absence of raw provider content;
7. verify the linked docs and cleanup command remain present.

The normal SDK gate continues to exercise shared transport/retry behavior. This
new check focuses on what a newcomer installs and sees.

## Alternatives considered

**Document message extraction without a helper.** This avoids facade code, but every
consumer would still reimplement pagination, Invocation filtering, and open-ended
content narrowing. A canonical-reading helper is smaller and more reliable.

**Discard a model checkpoint when later budget settlement fails.** This makes the
public transcript look simpler but loses durable evidence after a paid provider call
and can cause a crash retry to repeat the external call. Retaining evidence while
excluding failed output from future context preserves both durability and semantic
safety.

**Publish from a long-lived npm token.** It would bootstrap automation quickly, but
npm trusted publishing removes a reusable write credential and automatically adds
provenance. Only the first package creation remains interactive.

**Put local setup in the production single-daemon profile.** That would conflate a
disposable localhost database with a supported operating boundary. A separate local
profile keeps both paths honest.

## Tradeoffs and consequences

- The generation-context query now depends on Invocation status as well as message
  order. It is more semantically correct but must be regression-tested with failed
  checkpoints and recovery.
- Pricing metadata is a versioned local snapshot. A newly available model may reject
  capped work until Dive updates even when the provider can serve it. Uncapped work
  remains available, and the public reason tells the host how to proceed.
- The onboarding check adds npm and process lifecycle time to CI. It runs as a
  named step after the core Go and SDK gates so failures remain attributable.
- npm package administration and trusted-publisher changes still require an npm
  account with permission in the `@deepnoodle` organization; code cannot create
  that authority.

## Rollout

The implementation and published-availability README are merged. Version 0.1.0 was
packed, inspected, tested, and published interactively from the exact merged `main`
revision. npm trusted publishing is connected to repository `deepnoodle-ai/nvoken`,
workflow `release-npm.yml`, and the `npm publish` action. Later releases update the
version on `main`, pass the gates, and push the exact `npm-vX.Y.Z` tag.

## Open questions

There are no unresolved design questions. The public package coordinate is
`@deepnoodle/nvoken`, and later releases use the tag-driven trusted-publishing
workflow.
