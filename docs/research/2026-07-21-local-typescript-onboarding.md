# Local TypeScript onboarding field report

- Date: 2026-07-21
- Repository revision: `1393b5e62092b8d239aaf6c7d2c0c6abd1c9c9d9` (`main`)
- SDK version: `@deepnoodle/nvoken` `0.1.0`

> **Correction (2026-07-22):** The checked package metadata used the nonexistent
> `@deepnoodle-ai` npm scope. The established Deep Noodle organization is
> `@deepnoodle` (for example, `@deepnoodle/mobius`). The corrected
> `@deepnoodle/nvoken` coordinate was also unpublished when rechecked; fixing
> the scope is part of DX-01 through DX-04.

## Bottom line

The implemented path works: I ran `nvokend` locally, used the in-repository
TypeScript SDK from a separate app, completed a two-turn OpenAI chat, and
resumed the same durable Session from a second app process. The core Runtime
and the SDK's `Client`/`Handle` workflow were reliable once configured.

The first-run developer experience is not yet self-contained. A new user has
to assemble a Postgres container, migration command, four installation values,
provider configuration, daemon command, local SDK build, and application setup
from several documents. The TypeScript README's advertised npm package is not
published, and its quickstart hides terminal failure details and never prints
the assistant response. These are onboarding blockers rather than runtime
correctness problems.

## Method

I deliberately began with the root `README.md` and followed its public links.
I did not inspect backend implementation code to discover the initial startup
path. I inspected the SDK facade only after the documented quickstart failed to
show enough information to build a chat loop.

The test environment was macOS on arm64 with Go 1.26.2, Node.js 22.17.1, npm
10.9.2, Docker 29.6.1, and a disposable `postgres:17-alpine` container bound to
localhost. An existing OpenAI key was available to the daemon. The Anthropic
credential present in this environment was rejected by Anthropic and should
not be treated as an nvoken defect.

## What I built and proved

The resulting [TypeScript chat example](../../examples/typescript-chat/README.md)
is a small external-style application with a local `file:` dependency on
`sdk/typescript`. It:

- creates a host-owned `session_key` for the first message;
- uses the returned `session_id` for later messages in the same process;
- gives every new host message a distinct idempotency key;
- waits for each durable Invocation to reach a terminal state;
- treats `failed` and `cancelled` as application errors;
- pages canonical Session messages and extracts assistant text for the exact
  Invocation; and
- can resume a known `session_key` from a new process.

The successful two-turn check was:

```text
you:   Remember that my code word is cedar.
agent: Understood—your code word is cedar.
you:   What is my code word? Reply with only the word.
agent: cedar
```

I then used `NVOKEN_SESSION_KEY=dx-resume-20260721` in two separate Node.js
processes. The first stored `Lisbon`; the second asked for the launch city and
received `Lisbon`. This exercised durable Session resolution rather than only
in-process application memory.

## First-run journey

| Step | Result | New-user observation |
| --- | --- | --- |
| Read the root README | Product boundary was clear | There is no local quickstart or direct path from clone to first response. The two deployment links are production profiles. |
| Open the TypeScript SDK README | Found a three-line example | It assumes an already-running daemon and an API key, does not link to how either is obtained, and does not show the local-checkout install path. |
| Run `npm view @deepnoodle/nvoken version` | Failed with npm `E404 Not Found` | The corrected SDK package was not yet published. |
| Follow Runtime admission and credential guides | Found the required daemon inputs | The usable local recipe is spread across the Runtime guide, credential guide, and single-daemon production profile. |
| Supply Postgres 17 and run migrations | Succeeded | The repository has no disposable local Postgres or Compose path, so the user must invent one. |
| Run `nvokend serve` in combined/embedded mode | Succeeded | Startup logs clearly reported schema compatibility, enabled providers, execution mode, and listening address. This was excellent feedback. |
| Build and run the repository TypeScript quickstart unchanged | Process exited zero after printing `invk_… failed` | The example exposed neither `provider_error` nor useful next steps and did not print a model response. |
| Build a local app with `file:../../sdk/typescript` | Succeeded | The local package works after its `dist` output is built, but this path is undocumented. |
| Add an estimated-cost budget | Model generation succeeded, then the Invocation failed | `maxEstimatedCostUsd` failed closed because pricing was unavailable for the selected model. Only daemon logs exposed `estimated_cost_unavailable`. |
| Remove only the optional cost cap | Succeeded | Two-turn chat and cross-process Session resumption both worked. |

## Findings

### 1. Blocker: the documented npm install target does not exist

`sdk/typescript/README.md` starts with:

```bash
npm install @deepnoodle/nvoken
```

On 2026-07-21, the npm registry returned `E404` for that exact package. A user
outside the monorepo cannot follow the advertised path. A user inside the
checkout can depend on `file:../../sdk/typescript`, but must first build the
SDK because its entrypoint is `dist/index.js` and `dist/` is not committed.

Recommendation: publish `0.1.0`, or label the SDK unreleased and document the
local build plus `file:` installation until publication is verified. The
README should not present a registry command before it is continuously tested.

### 2. Major: there is no paved laptop quickstart

The root README is strong product documentation but does not answer the first
implementation question: "How do I get one response on my laptop?" The
single-daemon guide is intentionally an operating profile, not a development
bootstrap. It begins with immutable builds, checksums, protected environment
files, externally managed Postgres, supervisors, backup, and failure drills.
Those are appropriate production concerns but obscure the minimum local path.

To start the daemon, I had to infer and provide all of the following:

- a supported PostgreSQL 17 instance and database;
- an explicit migration run before service startup;
- a 32-byte-or-longer initial Runtime bearer;
- a separate bootstrap Owner secret;
- an exact 32-byte unpadded-base64url delivery key;
- a public base URL even for localhost; and
- at least one installation provider key.

Recommendation: add one development-only guide linked near the top of the root
README. A small Compose file or equivalent command should create disposable
Postgres, while one checked example environment file and two commands should
migrate and serve. Keep the production profile separate and clearly stricter.

### 3. Major: the TypeScript quickstart is not a useful success or failure test

The repository quickstart hardcodes Anthropic and one model, waits, then prints
only the Invocation ID and terminal status. In my environment it printed:

```text
invk_… failed
```

and exited with status 0. The authoritative Invocation contained
`provider_error`, but even that public error was not printed. The daemon log
contained the only useful classification, `upstream_rejected`. The example
also never reads Session messages, so a successful run would still not show
the assistant answer a prospective user came to see.

Recommendation: make provider and model explicit environment inputs, fail
nonzero for non-completed Invocations, print the public error code/message, and
print canonical assistant text. The added chat example demonstrates that full
path without generated-client calls.

### 4. Major: an unavailable price turns successful generation into a generic budget failure

The TypeScript facade exposes `maxEstimatedCostUsd`, and the Runtime admission
guide includes `max_estimated_cost_usd` in its first request example. With
`gpt-5.4-mini`, the provider successfully returned 90 input tokens and 13
output tokens. nvoken then failed the Invocation as `budget_exceeded` because
its pricing data was unavailable. The provider call had already incurred cost,
and the generated assistant message was present in canonical Session history,
but the Invocation was terminally failed. An application that renders only
completed turns therefore hides text that a later turn can still inherit as
conversation context.

The public failure was only:

```text
budget_exceeded: The execution budget was exceeded.
```

The actionable reason, `estimated_cost_unavailable`, appeared only in daemon
logs. No public guide mentions that a configured cost cap intentionally fails
closed when price metadata is missing.

Recommendation: document this fail-closed contract next to every cost-budget
example, return a safe public detail that distinguishes "limit exceeded" from
"price unavailable," and avoid cost caps in the first-run example unless the
selected model's price is known. Consider validating price availability before
the provider call when possible. Explicitly define whether a response rejected
only because its price is unknown should remain in canonical conversation
history; the current failed-but-context-bearing behavior is surprising for a
chat host.

### 5. Moderate: getting the final assistant text is lower-level than invoking

Admission is pleasant:

```ts
const handle = await client.invoke(request);
const invocation = await handle.wait();
```

There is no corresponding facade method for the result users most commonly
need. The app must call `client.listMessages`, implement cursor traversal,
filter messages by `invocationId` and role, and inspect the open-ended
`SessionContentBlock` shape for text. The package README and quickstart show
none of this.

Recommendation: either add a small facade helper such as
`handle.listMessages()`/`handle.text()` or document a blessed extraction
pattern. The durable transcript should remain authoritative; the improvement
is ergonomic, not a second source of truth.

### 6. Moderate: provider/model selection is hardcoded in the only SDK example

The daemon supports Anthropic and OpenAI, but the quickstart can run only the
hardcoded Anthropic model without source edits. Model availability is
account-specific and changes over time. A first-run example should accept
`NVOKEN_PROVIDER` and `NVOKEN_MODEL`, validate both, and show how they relate to
the provider key configured on the daemon.

## What felt good

- `go run ./cmd/nvokend migrate` was fast and produced explicit progress.
- Daemon startup logs gave a concise, confidence-building capability summary.
- The imported Runtime key authenticated the SDK without additional identity
  setup, which is appropriate for a first local host integration.
- `Client.invoke`, replay-safe admission, `Handle.wait`, and typed camelCase
  request fields were easy to use.
- Stable Session resolution worked both within one process and across process
  restarts.
- Canonical message reads returned exactly the durable chat history needed by
  the host app.
- Node.js 22 worked despite the documented SDK development baseline being Node
  24; the package's declared runtime floor of Node 20 was accurate in this run.

## Complete adjustment backlog

This is the consolidated backlog from the journey above. It is intentionally
explicit so the smaller documentation adjustments do not disappear behind the
larger runtime and SDK changes. Items are proposed unless marked as
demonstrated by the example added during this exercise.

### P0: make distribution truthful

- **DX-01 — Publish or clearly label the package as unreleased.** Publish
  `@deepnoodle/nvoken@0.1.0` to npm, or replace the registry installation
  command with a prominent unreleased notice. Do not leave an `npm install`
  command that returns `E404`.
- **DX-02 — Document local-checkout installation.** Until publication, show
  the exact SDK `npm install`/`npm run build` commands and a consumer dependency
  such as `file:../../sdk/typescript`. Explain that `dist/` is required and is
  not committed.
- **DX-03 — Verify the artifact users actually install.** On release, pack the
  SDK, install that tarball in an empty TypeScript project, compile it, and run
  a facade-only request. After publication, verify the public registry version
  rather than treating a successful repository build as publication evidence.
- **DX-04 — Keep version and availability claims synchronized.** The package
  README, root README, release workflow, and public registry should agree on
  whether the SDK is available and which version is current.

### P1: provide one paved local-development path

- **DX-05 — Add a clone-to-first-response guide.** Link a development-only
  quickstart near the top of the root README and from the TypeScript SDK README.
  A user should not need to infer a path through the production deployment
  profiles.
- **DX-06 — Provide disposable PostgreSQL 17.** Add a small Compose profile or
  an equivalent checked command for a localhost-only Postgres 17 database.
  Document start, readiness, and exact cleanup. Keep it explicitly unsuitable
  for production.
- **DX-07 — Provide a secret-free local environment template.** Include all
  required variable names, comments explaining their roles, and safe commands
  for generating the Runtime bearer, bootstrap Owner secret, and exact
  32-byte unpadded-base64url delivery key. Do not check generated values into
  Git.
- **DX-08 — Make migration and startup two obvious commands.** Show migration,
  daemon startup, health verification, and expected `process_started` fields in
  one place. Explain directly that the daemon's initial `RUNTIME_API_KEY` is the
  application's `NVOKEN_API_KEY` bearer.
- **DX-09 — Show the minimum provider setup.** Require one provider key, not
  both, and pair it with explicit provider and model inputs for the example.
  State that model availability is provider-account-specific and changes over
  time.
- **DX-10 — Preserve the production boundary.** Keep immutable artifacts,
  supervisors, TLS, backups, protected secret stores, failure drills, and the
  exact single-daemon support envelope in the production profile rather than
  making them prerequisites for the laptop tutorial.

### P1: replace the TypeScript quickstart with a real chat proof

- **DX-11 — Make provider and model configurable.** Read and validate
  `NVOKEN_PROVIDER` and `NVOKEN_MODEL`; do not require source edits or hardcode
  one provider's model in the only example.
- **DX-12 — Treat terminal failure as failure.** If an Invocation is `failed`
  or `cancelled`, print its public code, message, Invocation ID, and safe
  diagnostic pointer, then exit nonzero in a one-shot quickstart. Do not print
  only `invk_… failed` and exit zero.
- **DX-13 — Print the assistant's answer.** Page canonical Session messages,
  select the assistant content for the exact Invocation, and display its text.
  A successful quickstart should visibly prove that a model answered.
- **DX-14 — Demonstrate multi-turn Session behavior.** Send at least two turns
  through one Session and show that the second response uses earlier context.
  The added `examples/typescript-chat` app demonstrates this path.
- **DX-15 — Demonstrate restart/resume behavior.** Accept a host-owned
  `session_key`, start a second application process, and prove that it resolves
  the existing durable Session. The added example supports and was tested on
  this path.
- **DX-16 — Teach durable idempotency, not only uniqueness.** Explain that a
  production host should derive the idempotency key from its durable message
  record and reuse the same key and exact request after an uncertain
  acknowledgement. The example's random in-memory key is appropriate only for
  the bounded demo and now says so explicitly.
- **DX-17 — Keep starter budgets safe.** A first-run example may include output
  and iteration limits, but should omit estimated-cost limits unless price
  availability for the selected model is known and the unknown-price behavior
  is clearly explained.
- **DX-18 — Link every prerequisite from the example.** The example README
  should point directly to local daemon startup, credential generation, model
  selection, local SDK build, and cleanup instead of assuming those steps have
  already happened.

### P1: resolve the unknown-price budget semantics

- **DX-19 — Document fail-closed cost enforcement.** State next to the REST
  field, TypeScript field, Runtime admission example, and budget guide that an
  estimated-cost cap requires known price metadata and otherwise fails closed.
- **DX-20 — Return an actionable public reason.** Distinguish actual limit
  exhaustion from `estimated_cost_unavailable` in a safe public detail or
  typed subreason. Keep raw provider responses private, but make the host's
  corrective action discoverable without daemon-log access.
- **DX-21 — Reject known-unpriceable work before the provider call.** When
  price availability can be determined from provider/model metadata before
  execution, fail before incurring cost rather than paying for an answer that
  policy will reject.
- **DX-22 — Define failed-turn transcript behavior.** Choose and document one
  contract for a response generated successfully but rejected during budget
  settlement. Either prevent that assistant content from becoming future model
  context, or make its retained/context-bearing status explicitly observable
  to hosts. The current failed-but-hidden-to-the-UI yet inherited-by-later-turns
  behavior is not safe to leave implicit.
- **DX-23 — Add a regression test for that contract.** The test should assert
  the Invocation status, public failure detail, canonical messages, subsequent
  model context, and provider-call count when price metadata is unavailable.

### P2: improve TypeScript facade ergonomics

- **DX-24 — Add a blessed final-response API or recipe.** Prefer a small helper
  such as `handle.listMessages()`/`handle.text()` that internally pages and
  filters by the exact Invocation. If the facade remains unchanged, put the
  complete extraction recipe in the package README and quickstart.
- **DX-25 — Add text content narrowing.** Expose a facade-level typed text block
  or type guard so ordinary users do not have to inspect an open-ended
  `SessionContentBlock` and cast `block.text` themselves.
- **DX-26 — Keep canonical history authoritative.** Any convenience helper
  should read Session messages rather than copy assistant text onto a second,
  competing result model.
- **DX-27 — Document wait and retry semantics beside the example.** Clarify
  that a local wait timeout or dropped stream does not cancel durable work,
  explicit `cancel` changes server state, and the same durable host message ID
  should be reused after an ambiguous admission.
- **DX-28 — Clarify supported versus development Node versions.** Keep the
  package's Node 20 runtime floor and the repository's Node 24 development
  baseline, but label their different purposes so Node 20/22 users do not read
  the development baseline as a runtime incompatibility.

### P2: continuously test the newcomer journey

- **DX-29 — Add a clean-clone local smoke.** In CI, start disposable Postgres,
  migrate, start the daemon, build the local SDK, install it into the example,
  and exercise a deterministic provider or conformance double. This catches
  missing setup steps without requiring paid live-model calls.
- **DX-30 — Test the success UX.** Assert that the quickstart prints assistant
  text, completes two context-aware turns, exits zero, and can resume the same
  Session from a second process.
- **DX-31 — Test the failure UX.** Assert that invalid credentials and invalid
  models exit nonzero with the safe public category/code, useful identifiers,
  and no secret or raw provider-body leakage.
- **DX-32 — Test packaging separately from source.** Run the example against
  the packed npm artifact as well as the local `file:` link so committed source
  success cannot hide missing `dist` files or package metadata.
- **DX-33 — Check documentation links and commands.** Verify that the root
  quickstart, SDK README, local deployment guide, example, and cleanup commands
  remain mutually consistent.

## Recommended sequence

1. Complete DX-01 through DX-04 so the first command is truthful.
2. Complete DX-05 through DX-10 so one page starts a local Runtime.
3. Complete DX-11 through DX-18 to promote the demonstrated chat/resume path.
4. Resolve DX-19 through DX-23 before recommending estimated-cost caps.
5. Add DX-24 through DX-28 for a smaller ordinary-host integration surface.
6. Lock the path down with DX-29 through DX-33.

## Onboarding acceptance gate

A refined onboarding path passes only when all of the following are true:

- From a clean clone, one linked page produces a healthy local daemon and a
  visible assistant answer without undocumented commands.
- The documented SDK install target exists, and a packed/public artifact works
  in an otherwise empty TypeScript project.
- The example completes two context-aware turns and resumes the same Session
  from a second process.
- A host can obtain assistant text without importing generated transport APIs
  or writing an undocumented content-block cast.
- Invalid credentials and invalid models produce nonzero, actionable, safely
  sanitized failures.
- An estimated-cost cap on an unpriced model fails before paid generation when
  price availability is knowable, and the transcript/context outcome is
  explicit and regression-tested.
- Production hosts are told to persist and reuse message-derived idempotency
  keys after ambiguous admission responses.

## Resolution status (2026-07-22)

The implementation now resolves the full adjustment backlog in source and CI:

| Items | Resolution |
| --- | --- |
| DX-01–DX-04 | Corrected the package to `@deepnoodle/nvoken`, completed public package metadata and a tag-driven release workflow, published 0.1.0 from the exact reviewed `main` artifact, and added packed-artifact plus post-publish registry verification. |
| DX-05–DX-10 | Added a linked laptop guide, localhost-only PostgreSQL 17 Compose project, secret-free template, mode-0600 configurator, migration/start/health flow, one-provider setup, and explicit production boundary. |
| DX-11–DX-18 | Replaced the quickstart with configurable two-turn output and actionable nonzero failures; the chat example now uses the facade helper and documents Session resume, durable idempotency, safe budgets, prerequisites, and cleanup. |
| DX-19–DX-23 | Documented fail-closed estimated-cost semantics, added the public `estimated_cost_unavailable` detail, rejects known-unpriced capped work before the provider call, and keeps failed output canonical but out of later model context with service and Postgres regressions. |
| DX-24–DX-28 | Added `Handle.listMessages()`, `Handle.text()`, and `isTextContentBlock`; all read canonical history, and the README distinguishes durable wait/retry behavior and Node 20 runtime support from the Node 24 development baseline. |
| DX-29–DX-33 | Added a CI newcomer gate that migrates/starts the daemon, verifies startup identity, packs and inspects the SDK, installs it into an empty strict TypeScript project, runs success/resume/failure UX against a deterministic double, tests the local file-linked example, and checks the documented artifacts and cleanup command. |

The gate passes locally against the checked PostgreSQL 17 Compose profile. Version
0.1.0 was published after merge from the exact reviewed `main` revision, and later
versions use the repository's tag-driven npm trusted-publishing workflow.
