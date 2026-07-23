# Local TypeScript onboarding second-pass report

- Date: 2026-07-22
- Repository revision: `c10e5e21ac5db1da106cfb75bcf5ed67f3bc8525` (`main`)
- Published SDK: `@deepnoodle/nvoken` `0.1.0`
- Previous report: [Local TypeScript onboarding field report](2026-07-21-local-typescript-onboarding.md)

## Bottom line

The newcomer path is substantially improved and the core workflow now works from a
fresh clone without reverse-engineering the daemon. I used the linked local guide to
start disposable PostgreSQL, generate secrets, migrate and serve nvoken, build the
repository SDK and chat example, complete a live two-turn OpenAI conversation, and
resolve the same durable Session from a second process. I also installed the public
0.1.0 package into an empty strict TypeScript project and completed a live Invocation.

The original feedback is not completely resolved. The public SDK README advertises a
quickstart file that is deliberately absent from the npm package, so the first command
after `npm install` fails. The source quickstart also cannot append a turn when its
documented `NVOKEN_SESSION_KEY` option is reused. Local provider selection can be
silently changed by unrelated keys already exported in the shell, and cost-capped
hosts still have no way to discover pricing capability before risking a paid call.

My overall assessment is: **the supported chat path is ready for an early developer,
but the published-package quickstart and a few configuration contracts need another
small release before onboarding can be called closed.**

## Method

I started from a new GitHub clone at the revision above and followed the root Local
Quickstart link. I used the documented commands rather than an existing development
database or daemon. The live path used OpenAI and a provider model available to this
account. I separately installed the registry artifact into a new consumer directory;
that test did not use the SDK checkout.

The host was macOS 26.5.1 on arm64 with Go 1.26.2, Node.js 22.17.1/25.9.0, npm
10.9.2/11.12.1, Docker 29.6.1 with Compose 5.3.0, and Python 3.14.4. I also imported
the published SDK under Node.js 20.20.2 in a clean container to verify the declared
runtime floor.

I exercised:

- fresh-clone configuration, migration, startup, health, and exact cleanup;
- the source-linked TypeScript chat and SDK quickstart against a live provider;
- two turns and cross-process Session resolution;
- the public npm artifact in an otherwise empty strict TypeScript project;
- invalid Runtime credentials and an invalid model;
- known-unpriceable and post-response unknown-price cost-cap failures;
- canonical failed-turn retention and exclusion from later model context; and
- `make check`, `make sdk-check`, and the PostgreSQL-backed
  `make onboarding-check`.

No provider credentials, generated Runtime bearer, or raw provider response is
included in this report.

## What now works

| Journey | Result | Observation |
| --- | --- | --- |
| Find local setup from the root README | Passed | The Local Quickstart is prominent and clearly labeled development-only. |
| Start disposable PostgreSQL 17 | Passed | Compose waited for health and bound Postgres only to localhost. |
| Generate `.env` | Passed | The configurator copied the selected key, generated independent secrets, wrote mode 0600, printed no secret, and refused an accidental overwrite. |
| Migrate and start the combined daemon | Passed | Startup logs exposed schema compatibility, execution mode, process role, listening address, and enabled providers. |
| Health check | Passed | `GET /health` returned `ok`, with the guide correctly explaining its scope. |
| Build the source SDK and chat app | Passed | The exact `npm ci` and `npm run build` commands worked. |
| Run two live turns | Passed | The model remembered `juniper` and returned it on the second turn. |
| Resume from another process | Passed in `examples/typescript-chat` | A second process resolved the same host-owned Session key and recovered the prior code word. |
| Read final assistant text | Passed | `Handle.text()` returned canonical assistant output without generated API imports or casts. |
| Install public package | Passed | `npm install @deepnoodle/nvoken@0.1.0` compiled and ran in an empty strict TypeScript app. |
| Run under the declared Node floor | Passed | The published ESM entry point and `Client` export loaded under Node.js 20.20.2. |
| Invalid Runtime credential | Passed | The app exited nonzero with a sanitized `authentication` category and request ID. |
| Invalid model | Passed with copy polish noted below | The app exited nonzero with Invocation ID, `provider_error`, a safe public message, and no raw provider body. |
| Known-unpriceable cost-capped model | Passed | nvoken returned `budget_exceeded` with `details.kind=estimated_cost_unavailable` before provider generation. |
| Failed-output context semantics | Passed | The failed assistant checkpoint remained readable canonically but was excluded from the next model request. |
| Exact cleanup command | Passed | It removed the local Compose container, network, and database volume; `.env` remained as documented. |

## Prior backlog disposition

| Original items | Second-pass status | Evidence and qualification |
| --- | --- | --- |
| DX-01–DX-04: distribution | **Partially resolved** | The corrected public package exists and works as a library. The README embedded in that package tells users to run a file the artifact does not contain, so artifact/documentation truth is still incomplete. |
| DX-05–DX-10: local development | **Partially resolved** | Compose, secrets, migration, startup, health, cleanup, and production boundaries work. Python is used but absent from the page's prerequisites, model selection has no discovery path, and ambient provider keys can override the selected local configuration. |
| DX-11–DX-18: examples and resume | **Partially resolved** | Configurable success/failure output, multi-turn chat, safe starter limits, and the chat app's resume behavior work. The SDK quickstart's own advertised Session resume does not append new work. |
| DX-19–DX-23: cost and transcript semantics | **Resolved against the original acceptance criteria; follow-up recommended** | Public failure details, early rejection when absence is knowable, canonical evidence retention, and future-context exclusion all work. Unknown pricing can still fail only after paid generation by design, but there is no preflight capability for hosts. |
| DX-24–DX-28: TypeScript facade | **Resolved** | Canonical message/text helpers, content narrowing, wait/retry documentation, and Node 20 runtime support all worked. One handwritten cost-budget comment overstates pre-generation enforcement. |
| DX-29–DX-33: newcomer CI | **Partially resolved** | The automated gate passes and covers the library artifact, daemon, source examples, chat resume, and failures. Its current assertions cannot catch the public quickstart, quickstart-resume, or ambient-provider problems found here. |

## New and remaining findings

### F2-01 — Blocker: the public npm quickstart cannot run

The README shown by npm places this command immediately after the registry install
path:

```bash
node dist/examples/quickstart.js
```

The 0.1.0 artifact contains the facade and generated transport under `dist/`, but no
`dist/examples/quickstart.js`. Running the advertised command in a package consumer
produces `MODULE_NOT_FOUND`. The repository packaging test explicitly rejects any
`/dist/examples/` entry, so the package contents and its public instructions currently
contradict each other.

Suggested adjustments:

1. Prefer a complete, copyable consumer snippet in the SDK README that imports
   `Client` from `@deepnoodle/nvoken`; keep the package minimal.
2. If the executable is intended to be public, include and export it deliberately
   instead of excluding all examples from the artifact.
3. Label source-checkout-only commands explicitly and run them from the repository
   root with an unambiguous path.
4. Publish the corrected README in 0.1.1; changing `main` alone does not repair the
   README already embedded in 0.1.0.
5. Make packaging CI execute every public post-install command or compile every
   public snippet against the packed tarball.

Acceptance: a developer can start with an empty directory, install the registry
package, follow the adjacent quickstart literally, and see assistant text without a
repository checkout.

### F2-02 — Major: selected local provider configuration is not authoritative

I selected OpenAI in `deploy/local/configure.py`. The resulting `.env` correctly had
an OpenAI key and an empty Anthropic key, but the parent shell already exported an
Anthropic key. Because nvoken intentionally does not override exported values with
dotenv values, daemon startup reported both providers enabled.

The guide says to export exactly one key, but it does not neutralize keys inherited
from shell startup files or another project. This is especially confusing because
the configurator's `--provider openai` option reads like a provider selection.

Suggested adjustments:

1. After generating `.env`, tell users to unset both provider variables before
   migration/startup so the generated file becomes the authority.
2. Have the configurator warn, without printing values, when a non-selected provider
   variable is already exported.
3. Consider a small local launcher that supplies only the generated environment.
4. Add an onboarding regression that seeds a non-selected ambient provider key and
   asserts the exact documented startup enables only the selected provider.

Acceptance: `--provider openai` followed by the documented commands enables only
OpenAI, even when the user's normal shell has an Anthropic key.

### F2-03 — Major: hosts cannot preflight estimated-cost capability

The original semantic fixes work. For a model that the pricing registry knew was
unpriceable, nvoken rejected before generation. For live `gpt-5.4-mini`, however,
the provider completed generation and returned usage, after which nvoken failed the
Invocation with:

```json
{
  "code": "budget_exceeded",
  "message": "Estimated cost is unavailable for the requested model.",
  "details": { "kind": "estimated_cost_unavailable" }
}
```

That fallback is documented and preserves durability, but an application cannot ask
nvoken whether the exact provider/model has known USD pricing before choosing to set
`maxEstimatedCostUsd`. The current handwritten TypeScript JSDoc is also too strong:
it says missing pricing "fails closed before generation," which is not true when
absence is discovered only from returned evidence.

Suggested adjustments:

1. Correct the facade JSDoc to match the OpenAPI contract: pre-generation rejection
   happens only when pricing absence is knowable before execution.
2. Expose an authenticated pricing-capability preflight for an exact provider/model,
   returning at least `priced`, `unpriced`, or `unknown` plus the local registry
   version. It need not claim provider-account availability.
3. Add a facade helper for that preflight so hosts can intentionally omit the cap,
   reject the request themselves, or accept the documented post-response risk.
4. Keep the existing public error and failed-output context regressions.

Acceptance: before admitting a cost-capped turn, a host can determine whether nvoken
can enforce the requested USD guardrail without relying on a paid provider response.

### F2-04 — Moderate: the SDK quickstart's Session resume claim is false

The source quickstart derives two static idempotency keys solely from the Session key:
`<session-key>:message-1` and `<session-key>:message-2`. Re-running it with the same
`NVOKEN_SESSION_KEY` and model silently resolves the original two Invocations and
prints their old output. Changing the model produces `idempotency_conflict` because
the same keys now identify different request bodies. It never demonstrates a new
turn in the existing Session.

Suggested adjustments:

1. Either remove the quickstart's resume claim and point to the working chat example,
   or accept a distinct durable host message/run key for each new turn.
2. If resume remains in the quickstart, make the second process ask a new question
   about context created by the first process.
3. Explain that the new key must be persisted and reused only for retrying that exact
   request; a random key generated after an ambiguous response is unsafe.
4. Run the quickstart twice in CI with one Session key and distinct durable message
   identities, then assert that the second process appends rather than replays.

Acceptance: the documented explicit-Session flow creates a new Invocation that uses
context written by an earlier process, while exact retries still deduplicate.

### F2-05 — Moderate: the local guide omits a required Python prerequisite

The prerequisites list Go, Node.js, npm, Docker Compose, and a provider key. Step 2
then requires `python3 deploy/local/configure.py`. The repository SDK guide mentions
Python elsewhere, but a newcomer following the dedicated local page is not linked to
that requirement.

Suggested adjustment: list a supported Python version on the local page (the script's
syntax requires Python 3.9 or newer), or replace the configurator with a tool already
in the stated prerequisite set. Exercise the documented minimum in CI.

### F2-06 — Moderate: `<available-model>` has no discovery path

Both quickstarts require `NVOKEN_MODEL='<available-model>'`, but neither gives a
tested example, a provider-specific discovery link, nor an nvoken command that helps
the developer choose one. The placeholder avoids publishing a stale universal model
claim, but it moves a required decision outside the paved path.

Suggested adjustments:

1. Link to the official provider model-listing instructions and explain that account
   access is authoritative.
2. Provide a dated model used by the smoke test as an example, clearly not a
   guarantee, or add a provider-aware diagnostic/model-list command.
3. When model admission fails, include a safe next-step pointer to the same guidance.

Acceptance: a developer who has a provider key but does not already know a model ID
can complete the local guide without searching the repository or guessing.

### F2-07 — Minor: terminal failure formatting adds duplicate punctuation

The SDK quickstart formats the provider's already-punctuated public message as
`...execution.. Inspect structured daemon logs...`. Build the sentence from
punctuation-neutral fields or add punctuation only when the message lacks it. Add the
exact rendered failure to the existing failure-UX assertion.

### F2-08 — Minor: the health command adds avoidable curl noise

`curl --fail` succeeds but prints a transfer progress meter around the one-word health
response in an interactive terminal. `curl --silent --show-error --fail` keeps real
failures visible while making the expected `ok` proof easier to recognize.

## CI adjustments

The current checks are valuable and all pass, but the second pass found three blind
spots caused by testing related components separately:

- `pack_sdk` asserts that examples are excluded, while `check_examples` runs the
  source-tree example. It never verifies that the packed README's advertised command
  is satisfiable.
- The quickstart runs only once without an explicit Session key. Cross-process resume
  is tested only in the separate chat example.
- Daemon startup is tested with the non-selected provider variable forcibly empty,
  so normal inherited-shell behavior cannot surface.

In addition to the finding-specific regressions above, run the packed import or a
small consumer test on Node 20 as well as the Node 24 development baseline. The
manual Node 20 check passed; automating it will keep the declared engine floor true.

## Recommended sequence

1. Fix F2-01, correct the public quickstart, and publish 0.1.1.
2. Fix F2-04 in the same documentation/example pass so resume claims are executable.
3. Make local provider selection deterministic (F2-02) and close its CI gap.
4. Correct the cost JSDoc immediately, then design the pricing preflight in F2-03.
5. Add Python and model discovery to the paved guide (F2-05 and F2-06).
6. Apply the small output-polish fixes and expand the newcomer assertions.

## Refreshed acceptance gate

The onboarding work is complete when:

- the public package's adjacent quickstart runs from an empty consumer directory;
- every README command is checked against the same artifact and working directory a
  reader will use;
- an explicitly selected local provider is not changed by unrelated ambient keys;
- both the chat example and any SDK quickstart resume claim append a new durable turn;
- Python and model selection are discoverable before their first use;
- the TypeScript cost contract distinguishes pre-generation rejection from
  post-response fail-closed settlement;
- hosts can preflight whether a USD cost cap is enforceable for an exact model; and
- source, packed-package, minimum-Node, failure, resume, and environment-precedence
  checks all pass.

## Resolution status (2026-07-22)

All eight findings have corresponding source, documentation, and regression-test
changes. The package is bumped to 0.1.1, but that version remains intentionally
unpublished until this work merges and the exact merged `main` artifact passes the
release gates.

| Finding | Resolution |
| --- | --- |
| F2-01 | Replaced the nonexistent packaged example command with a complete consumer snippet, labeled source-only commands, and made onboarding extract and execute the README snippet from the packed tarball. |
| F2-02 | The configurator emits a value-free warning for an ambient non-selected provider key; the local guide unsets both provider variables before startup; the daemon regression proves only the selected provider is enabled. |
| F2-03 | Corrected cost-cap JSDoc and added authenticated `GET /v1/model-pricing-capabilities`, generated clients, `Client.pricingCapability()`, registry-version reporting, and priced/unpriced/unknown regressions. |
| F2-04 | Added a distinct required durable run key for resumed quickstart work, made the second process append a context-aware turn, documented exact retry semantics, and exercised the two-process flow. |
| F2-05 | Added Python 3.9 or newer to local prerequisites and a Python 3.9 CI job for the configurator. |
| F2-06 | Linked official provider model catalogs, clarified account access as authoritative, and added the same safe pointer to invalid-model starter failures. |
| F2-07 | Made terminal punctuation conditional and asserted that invalid-model output contains no duplicate punctuation. |
| F2-08 | Changed the health proof to `curl --silent --show-error --fail`. |

The CI workflow additionally runs the full packed-package onboarding gate under
Node 20, while the standard repository job retains the Node 24 development
baseline.

## Third-pass correction (2026-07-22)

A live third pass found that the initial pricing preflight validated the provider
but queried Dive's global model-name pricing registry. It therefore reported
cross-provider pairs such as `openai` plus a Claude model as `priced`. The corrected
adapter queries the selected provider's standard USD pricing table, with regressions
for both valid pairs and both cross-provider directions. The capability also reports
the version of the provider-specific pricing module, which matters because the
OpenAI registry can advance independently from the core Dive module.

The same pass found that the new packed README snippet and source quickstart still
printed internal Node.js stacks for validation or authentication failures. Both
entrypoints now use one-line public error rendering, and the packed-artifact gate
exercises invalid credentials and invalid models in addition to success. Version
0.1.1 remains deliberately unpublished until these corrections merge and pass from
the exact `main` revision.

PR review then hardened the correction without changing its release scope. Supported
providers now fail tests if their registry version degrades to `unknown`, and a
synthetic dependency graph proves Anthropic and OpenAI use their respective module
versions when those versions diverge. Public error rendering moved into exported SDK
helpers shared by the packed README, source quickstart, and chat example. The
end-to-end checks now assert diagnostic meaning rather than the exact serialized
shape of safe detail objects, so additive fields do not break onboarding.
