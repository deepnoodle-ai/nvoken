# Local TypeScript onboarding

**Status:** Newcomer surface implemented in source; 0.2.0 release pending
**Author:** OpenAI Codex  
**Date:** 2026-07-22  
**Workflow:** Spec and build in parallel; the field report supplies the acceptance criteria.

## Context

The [local TypeScript onboarding field report](../research/2026-07-21-local-typescript-onboarding.md)
proved that the Runtime and TypeScript facade worked, but also found 33 distribution,
bootstrap, failure-semantics, ergonomics, and CI gaps. The implementation closed those
gaps and the reviewed 0.1.0 artifact is now public at `@deepnoodle/nvoken`. It also
sharpened estimated-cost enforcement now that durable model checkpoints can retain a
provider response before terminal cost-limit settlement.

A [second-pass validation](../research/2026-07-22-local-typescript-onboarding-second-pass.md)
found eight remaining issues. The follow-up makes the packed README executable,
separates Session identity from durable message identity, makes selected-provider
startup deterministic, adds an authenticated pricing-capability preflight, and
expands minimum-runtime and newcomer regressions. Those corrections shipped in
`@deepnoodle/nvoken` 0.1.1.

A third-pass review caught that the initial pricing preflight looked up a globally
registered model ID after validating—but not applying—the provider. The corrected
adapter resolves the provider-specific pricing table, and the packaged and
source-checkout quickstart commands render validation and transport failures
without an internal Node.js stack.

A fourth pass treated the package as a new product with no compatibility burden.
It reduced the ordinary path to `new Client().agent(...).text(...)`, made string
input and generated idempotency the defaults, separated the one-response
quickstart from the multi-turn chat example, and organized advanced behavior
around five verbs: `text`, `run`, `invoke`, `stream`, and `session`.

## Goals

- Make every documented install command truthful and ship a public, packed-artifact
  test for `@deepnoodle/nvoken` 0.1.0.
- Provide one install-to-first-response path with disposable PostgreSQL 17,
  generated local secrets, migration, health verification, one TypeScript turn,
  and exact cleanup commands.
- Make the supported TypeScript quickstart configurable, visibly successful, and
  nonzero with safe actionable details on failure.
- Let a host obtain the exact Invocation's canonical messages and assistant text
  through the facade without generated-client imports or casts.
- Fail capped, known-unpriceable work before a provider call and prevent output from
  a failed or cancelled Invocation from influencing a later model turn.
- Continuously test source and packed-package onboarding, including success,
  failure, an anonymous first response, multi-turn context, and cross-process
  Session resolution.

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
conformance server. It also extracts and executes the public README quickstart from
the installed tarball under Node 20. Registry verification remains a
post-publication check; a green repository build is never reported as public
availability.

### Local development path

`nvokend quickstart` owns the disposable local lifecycle. It starts one
explicitly labeled PostgreSQL 17 container on localhost, generates independent
Runtime, bootstrap Owner, and delivery secrets without printing them, copies
exactly one selected provider key from the caller's environment, writes a
marked ignored `.env` with mode 0600, applies migrations, and runs the daemon.
Re-running it reuses only the marked resources; `nvokend quickstart cleanup`
removes only the owned container.

The Run guide pairs the official binary with the packaged one-response command.
The Develop guide uses the same daemon automation from source and the SDK build
from the checkout. `deploy/local` remains lower-level test and debugging
infrastructure, not the first-time path. Both guides label the topology
disposable and keep TLS, supervisors, backups, immutable artifacts, failure
drills, and availability claims in the production profiles.

### TypeScript facade and examples

The newcomer surface is:

```ts
import { Client } from "@deepnoodle/nvoken";

const agent = new Client().agent({
  agentKey: "support",
  instructions: "Be concise and helpful.",
});

console.log(await agent.text("Why was I charged twice?"));
```

`Client` resolves explicit options first, then `NVOKEN_*`, then only the marked
`.env` created by `nvokend quickstart`. It never loads an arbitrary dotenv file
or mutates `process.env`. `agent.text()` returns text, `agent.run()` returns the
typed composed result, `agent.invoke()` returns a lazy durable handle,
`agent.stream()` exposes one-turn events, and `agent.session()` binds and
serializes a multi-turn Session. The SDK generates an idempotency key before
admission; hosts supply one only when they need to reproduce the same logical
turn across processes.

The handle exposes acknowledgement identity, `refresh()`, actionable and
terminal waits, result/text/message reads, host-tool submission, cancellation,
and Invocation-scoped streaming. Failed or cancelled terminal work raises an
actionable `InvocationError` carrying the handle and authoritative Invocation.
Schema helpers bind TypeScript input/output types to host-tool and structured
output schemas without pretending to perform runtime validation. Symmetric
async iterators cover Session, Invocation, message, and transcript pagination;
`drainTranscript()` preserves the fixed-cut cursor contract.

All composed result helpers are served by
`GET /v1/invocations/{invocation_id}/result`, which returns the authoritative
Invocation, this Invocation's canonical messages, and the wire `output_text`
projection. Nothing is cached or copied; the projection is computed from
canonical rows at read time.

The packaged quickstart performs one anonymous turn and prints the assistant
response. Anonymous one-shot work uses a real durable Session internally but
does not expose a Session binding in the newcomer code. The separate chat
example demonstrates durable Session identity, SDK-generated idempotency, and
cross-process recovery.

Node 20 remains the supported package runtime floor and now runs the complete
onboarding gate in CI. Node 24 remains the pinned repository development baseline.

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

Authenticated hosts can call `GET /v1/models` to discover nvoken's curated
selections and `GET /v1/models/{provider}/{model_id}` to inspect an exact
selection before admission. The nested pricing object's `priced`, `unpriced`,
or `unknown` result and opaque `pricing_version` describe only whether nvoken
can enforce the USD cap without relying on a paid provider response; they do
not claim provider-account access or served-model identity. The TypeScript
facade exposes the same operations through `Client.listModels()` and
`Client.getModel()`.

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
6. prove the anonymous first response, two-turn context, cross-process Session-key
   resume, invalid-credential and invalid-model failures, useful identifiers, and
   absence of raw provider content;
7. verify the linked docs and cleanup command remain present.

The normal SDK gate continues to exercise shared transport/retry behavior. This
new check focuses on what a newcomer installs and sees.

## Alternatives considered

**Document message extraction without a helper.** This avoids facade code, but every
consumer would still reimplement pagination, Invocation filtering, and open-ended
content narrowing. A canonical-reading helper is smaller and more reliable.

**Discard a model checkpoint when later cost-limit settlement fails.** This
makes the public transcript look simpler but loses durable evidence after a paid
provider call and can cause a crash retry to repeat the external call. Retaining
evidence while excluding failed output from future context preserves both
durability and semantic safety.

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

Version 0.1.0 was packed, inspected, tested, and published interactively from the
exact merged `main` revision, and the second- and third-pass corrections shipped
in 0.1.1. The redesigned surface is versioned 0.2.0. Publish `v0.2.0` and
`npm-v0.2.0` only from the same exact merged `main` commit after its full
repository, SDK, and onboarding gates pass. npm trusted publishing is connected
to repository `deepnoodle-ai/nvoken`, workflow `release-npm.yml`, and the
`npm publish` action.

## Open questions

There are no unresolved design questions. The public package coordinate is
`@deepnoodle/nvoken`; 0.2.0 publication remains deliberately separate from a
green source branch.
