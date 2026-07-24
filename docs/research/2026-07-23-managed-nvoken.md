# Managed nvoken: public cloud, tenancy, credentials, pricing, and website

**Date:** 2026-07-23  
**Status:** Recommendation  
**Scope:** A deliberately small public managed offering for nvoken, with a path to
paid shared infrastructure and isolated deployments.

## Executive recommendation

Launch **nvoken Cloud** as a shared, multi-customer service, while preserving the
same runtime API and SDKs as self-hosted nvoken.

- Make the shared service the default for Free and Pro.
- Offer a manually provisioned **Dedicated** data plane at a high minimum
  commitment.
- Defer customer-cloud **BYOC** until repeated qualified demand justifies its
  support surface.
- Support both BYOK and nvoken-funded model access. BYOK should be available on
  every plan. Platform-funded models should be prepaid, capped, and billed
  separately from runtime usage.
- Keep the public website documentation-first and the authenticated console to
  five jobs: quickstart, runtime credentials, model credentials, usage/funding,
  and account/data settings.
- Keep hosted commerce and human-account management out of the open-source
  runtime. Build a thin private control plane around the existing data plane.

The important tenancy distinction is:

1. **Application tenancy:** a customer's application can have many end users or
   workspaces, represented by nvoken `tenant_key` values.
2. **Deployment tenancy:** multiple nvoken customers can share a managed data
   plane, or one customer can receive an isolated data plane.

nvoken already addresses the first. The public cloud must add the second.
Calling both of these merely "multi-tenant" obscures the largest remaining
engineering boundary.

The proposed product line is:

| Product | Deployment | Model credentials | Buyer |
| --- | --- | --- | --- |
| Open-source nvoken | Customer-operated | Installation BYOK | Teams that want full control |
| nvoken Cloud Free | Shared | BYOK; small guarded platform trial | Evaluation and small projects |
| nvoken Cloud Pro | Shared | BYOK or prepaid platform funding | Production developer teams |
| nvoken Dedicated | Isolated nvoken data plane | BYOK or prepaid/invoiced platform funding | Compliance, isolation, or predictable-capacity buyers |
| nvoken BYOC, later | Isolated in customer cloud | Usually BYOK | Enterprise buyers with cloud-boundary requirements |

Do not launch a free single-tenant deployment per customer. The fixed database,
Redis, queue, deployment, upgrade, backup, and incident surface makes that an
economically poor free product.

## Why a managed version is worth offering

The open-source runtime has a useful product boundary: host applications retain
their users, agent definitions, orchestration, product data, and tool effects;
nvoken owns durable Sessions and Invocation execution. A managed version removes
the least differentiated but most operationally sensitive work from those host
teams:

- provisioning and upgrading Postgres, Redis, queues, and executors;
- keeping Invocations alive through disconnects and deploys;
- encrypting and resolving provider credentials;
- applying capacity limits without violating durable execution semantics;
- operating backups, restoration, observability, and incident response.

That is a narrower promise than an "agent platform." It does not require an
agent builder, prompt registry, integration catalog, evaluation suite, or
general-purpose workflow UI.

The qualitative developer evidence supports this narrow boundary:

> "How can one deploy LangGraph as an API (with production like features)?"
>
> — `mendeza`, [Hacker News discussion](https://news.ycombinator.com/item?id=43468435)

> "none of them are a unified runtime, which is why everyone ends up composing."
>
> — `FragrantBox4293`, [r/AI_Agents discussion](https://www.reddit.com/r/AI_Agents/comments/1ribz4g/is_there_an_opensource_runtime_for_production_ai/)

> "Less Heroku, more the fuse box."
>
> — `No-Conflict4823`, [r/AI_Agents discussion](https://www.reddit.com/r/AI_Agents/comments/1v2d1lj/do_production_agents_need_their_own_kind_of_paas/)

These are directional signals, not market-size evidence. They suggest that
durable production operation is a recognizable pain and that developers are
wary of frameworks that take ownership of too much application logic.

## What nvoken has today

The current repository is materially closer to a hosted data plane than to a
hosted product.

### Already useful for a managed offering

- Account-scoped domain rows and runtime authorization.
- `tenant_key` isolation within an Account.
- Durable Sessions, Invocations, tool calls, event replay, and terminal status.
- Explicit credential-source selection with no fallback:
  - caller-ephemeral credentials;
  - Account BYOK;
  - tenant BYOK;
  - platform-funded credentials;
  - installation BYOK for self-hosting.
- Application-layer encryption for stored provider credentials.
- Source-aware, durable model-usage receipts.
- Normalized provider usage including input, output, cache, and reasoning tokens.
- List-price estimates from the model catalog.
- A split Google Cloud deployment with a public runtime, private executor, Cloud
  Tasks, Postgres, and Redis.
- Infrastructure defaults for backup and point-in-time recovery.
- A CLI and TypeScript SDK that can target the same API locally or remotely.

Relevant design and product sources:

- [Product overview](../product/overview.md)
- [Architecture](../design/architecture.md)
- [Per-provider credential modes](../prds/028-prd-per-provider-credential-modes.md)
- [Data retention](../guides/data-retention.md)
- [Google Cloud deployment](../../deploy/google-cloud/README.md)

### Not yet a public managed service

The largest gap is not the runtime loop. It is the customer control plane and
the public-service safety boundary.

1. **The daemon bootstrap still assumes one installation Account.** The schema
   and most runtime services are Account-scoped, but startup resolves a fixed
   Account and refuses an installation containing more than one. A shared cloud
   needs managed Account provisioning and dynamic Account identity, not a
   bootstrap-secret flow.
2. **There is no human cloud identity or membership model.** Runtime credentials
   authenticate applications, not the humans who need to create Accounts,
   rotate credentials, fund usage, or delete data.
3. **There is no complete quota and fair-scheduling layer.** Device
   authorization has local limits, but the runtime does not yet enforce
   plan-level Account concurrency, monthly usage, provider spend, or abusive
   request patterns.
4. **Platform funding is a hook, not a commercial system.** There is no wallet,
   payment collection, idempotent billing event ledger, or authoritative
   reconciliation.
5. **Authoritative runtime data is retained indefinitely.** There is no
   tenant/session deletion or automatic compaction. This is a public-cloud
   launch blocker, especially for a free tier.
6. **Some cloud readiness proof remains pending.** Existing PRDs mark major
   mechanics implemented, but staging drills, restore evidence, and parts of
   Google Cloud readiness are still explicit follow-up work.
7. **There is no customer-facing operational contract.** Status communication,
   support boundaries, abuse handling, incident procedures, and eventually an
   SLA must be defined.

The conclusion is not to build another runtime. It is to finish the small set
of boundaries that turn this runtime into a safe public data plane.

## Market pattern

Current hosted durable-execution and agent-runtime products generally combine a
small fixed plan with usage. Their isolation options are usually progressive:
shared self-serve first, then dedicated or BYOC at enterprise pricing.

Prices below are public prices observed on 2026-07-23 and will change.

| Product | Public packaging | Relevant lesson for nvoken |
| --- | --- | --- |
| [LangSmith](https://www.langchain.com/pricing) | Paid developer plan plus metered serverless/dedicated runtime, memory, and database resources | Customer-facing workloads push buyers toward always-on/dedicated capacity; raw infrastructure meters become difficult to predict |
| [Letta](https://docs.letta.com/pricing) | Base API plan plus active-agent, server-tool, and model usage; BYOK is published as an option | Separate runtime value from model spend and avoid charging only a token markup |
| [AWS Bedrock AgentCore](https://aws.amazon.com/bedrock/agentcore/pricing/) | Per-second active CPU and memory, with model inference charged separately | Separating runtime and model usage is credible; charging customers directly for several low-level meters is complex |
| [Trigger.dev](https://trigger.dev/pricing) | Free/Hobby/Pro plans with included credits, run concurrency, and per-second compute; waiting is not charged | Concurrency and included usage are understandable plan levers; durable waiting should not look expensive |
| [Restate Cloud](https://www.restate.dev/cloud) | Shared multi-tenant cloud with a free allowance and paid tiers; enterprise BYOC separates control and data planes | This is the closest deployment pattern: shared self-serve first, isolated customer cloud later |
| [Hatchet](https://hatchet.run/pricing) | Free developer usage, paid team/scale plans, enterprise self-hosting/BYOC | Runs, retention, tenants, throughput, and support can differentiate tiers without inventing product features |
| [Agentuity](https://agentuity.com/pricing) | Free starting credits and usage-based agent compute/storage | A small trial balance can make first use immediate, but needs hard abuse and spend controls |
| [Cloudflare Agents](https://developers.cloudflare.com/agents/) | Agent durability built from Workers and Durable Objects meters | Infrastructure primitives are an alternative, so nvoken must sell the complete runtime contract rather than generic compute |

Three patterns are consistent:

1. **A constrained free experience is normal.** It usually limits included
   usage and concurrency, not the basic API.
2. **Runtime and model inference are separate economic units.** This matters
   whether the vendor exposes one invoice or two line items.
3. **Dedicated/BYOC is an upgrade, not the default architecture for every
   account.** Isolation carries a meaningful price because capacity and
   operations are reserved.

## Tenancy recommendation

### Default: shared multi-customer cloud

Use a shared nvoken data plane for Free and Pro. In managed-cloud mode:

- one cloud customer organization maps to one nvoken `Account`;
- the customer's application maps its own users/workspaces to `tenant_key`;
- the customer's backend authenticates with Account-scoped runtime
  credentials;
- every authoritative query remains Account-scoped, with `tenant_key` added
  where the resource contract requires it;
- Account and tenant scope come from authenticated context, never from a
  client-asserted Account ID.

This is the only practical basis for an open free tier. It also concentrates
operational learning in one deployment rather than multiplying immature
installations.

Shared should mean shared services with logical isolation, not shared secrets or
ambiguous identity. Provider credentials remain encrypted and keyed by Account
and optional tenant scope. Usage, limits, and billing events must also carry the
Account identity.

### Premium: dedicated data plane

Offer a dedicated deployment when a customer needs:

- an isolated Postgres database, Redis instance, queue, and runtime/executor;
- a chosen region;
- predictable reserved capacity;
- custom retention or backup terms;
- private networking;
- a stronger support or availability commitment.

The shared control plane can still own login, plan, billing, deployment
inventory, and support access. The isolated data plane should run the same
nvoken release, API, schema, and migrations as shared Cloud.

Initially provision Dedicated manually through the existing infrastructure
path. Do not build a self-service deployment orchestrator before there are
several paying customers asking for it.

### Later: BYOC

BYOC means nvoken operates or coordinates a data plane in the customer's cloud
account while retaining a managed control plane. It is not the same as
self-hosting:

- self-hosted customers own upgrades and operations;
- BYOC customers pay nvoken to operate the release and service contract inside
  their boundary.

BYOC creates a large matrix: cloud providers, IAM, networking, observability,
upgrades, rollback, support access, and incident ownership. The existing Google
Cloud path is a good foundation, but one supported cloud and region should be
the maximum initial scope. Add BYOC only after qualified buyers repeatedly
identify cloud-boundary control as a purchase blocker.

### Why not single-tenant only

Single-tenant-only would simplify one kind of isolation while worsening the
business:

- every free evaluator would create infrastructure and upgrade burden;
- low-volume deployments would have poor utilization;
- migrations, incidents, backups, and capacity changes would multiply;
- onboarding could not remain instant;
- the minimum sustainable price would exclude the intended developer entry
  point.

Dedicated should therefore be an isolation option, not the basic unit of the
product.

## Proposed hosted architecture

Keep the open-source runtime as the data plane. Add a small, separate hosted
control plane.

```text
Browser
  |
  v
nvoken Cloud control plane
  - human login and Account membership
  - plans, funding, and Stripe
  - deployment registry
  - credential-management UI
  - quotas and support administration
  |
  +---------------------------+
  |                           |
  v                           v
Shared nvoken data plane      Dedicated nvoken data plane
  - Runtime API                 - same API and release
  - executor                    - isolated Postgres
  - Postgres                    - isolated Redis/queue
  - Redis/queue                 - optional private network
  - BYOK secret store
  - canonical usage receipts
```

### Ownership boundary

| Concern | Open-source nvoken data plane | Hosted control plane |
| --- | --- | --- |
| Sessions, Invocations, tool calls, runtime events | Owns | References and summarizes only |
| Runtime machine credentials | Issues and validates, or accepts control-plane provisioning through a narrow internal port | Presents lifecycle to human owners |
| BYOK provider credentials | Encrypts, resolves, and audits use | Presents write-only management UI |
| Platform provider credentials | Resolves a platform key selected by funding policy | Owns funding eligibility, wallet, spend caps, and commercial policy |
| Canonical model/runtime usage | Emits durable, idempotent facts | Rates facts, debits funds, invoices, and reconciles |
| Human users and Account membership | Does not own | Owns |
| Plans, checkout, tax, invoices | Does not own | Owns |
| Agent definitions, tool effects, host product data | Does not own | Does not own |
| Deployment inventory and lifecycle | Reports version/health | Owns |

The hosted control plane is a good candidate for a separate private repository.
That keeps Clerk/identity, Stripe, commercial policy, promotional credits, and
support tooling out of the Apache-licensed runtime. The open-source repository
should contain only the general interfaces and mechanics needed to operate a
data plane in managed mode.

### Narrow runtime changes

1. **Cloud Account provisioner**
   - create and suspend Accounts idempotently;
   - provision initial machine credentials;
   - replace the fixed installation Account in managed mode;
   - preserve today's one-Account bootstrap path for self-hosting.
2. **Dynamic cloud authentication**
   - resolve every runtime request to an Account;
   - do not make browser sessions valid runtime credentials;
   - keep human/control-plane and machine/runtime credentials distinct.
3. **Admission and funding port**
   - check Account status, concurrency, monthly allowance, and platform-funding
     balance before accepting work;
   - reserve conservatively before platform-funded model calls;
   - fail closed and never silently switch credential sources.
4. **Durable usage outbox**
   - emit an idempotent event only from canonical committed runtime facts;
   - do not bill from logs, retries, or HTTP attempts;
   - let the control plane acknowledge and reconcile consumption.
5. **Deletion and retention**
   - Account, tenant, Session, and Invocation deletion contracts;
   - documented cascade and tombstone semantics;
   - backup-expiry disclosure;
   - automatic retention by plan where appropriate.
6. **Fair scheduling and limits**
   - per-Account active Invocation caps;
   - queue depth and admission limits;
   - provider-source and model spend caps;
   - global emergency stops and Account suspension.
7. **Operational identity**
   - separate, audited support/admin access;
   - no reuse of customer credentials for support;
   - nondisclosing authorization failures for scoped resources.

### Billing event semantics

Copy Mobius's durable-accounting principle, not its entire billing product.

- Write one idempotent usage event per committed billable fact.
- Maintain an append-only money/credit ledger.
- Make the wallet balance a derived or transactionally maintained value, not
  the only record.
- Use integer currency units. If customer-visible credits exist, make
  `1 credit = $0.01`; do not create a mysterious exchange rate.
- Reserve platform-model funds before the call and settle against actual
  receipt usage.
- Reconcile provider usage and Stripe state independently.
- Treat webhook events as notifications, then read canonical state from Stripe
  where practical.

This is enough for a trustworthy prepaid service without reproducing Mobius's
monthly allowances, purchase-lot complexity, broad RBAC, and product-specific
billing flows on day one.

## BYOK versus platform keys

### Recommendation: offer both

BYOK and platform keys solve different adoption problems:

- **BYOK** gives cost transparency, provider relationship, rate-limit ownership,
  data-policy control, and a safer nvoken balance sheet.
- **Platform keys** remove the largest quickstart interruption and let a new
  developer see a successful durable Invocation before opening provider
  consoles.

Making either one exclusive weakens the offering. BYOK-only makes managed
nvoken feel like infrastructure assembly. Platform-only forces a model reseller
relationship on customers who do not want it.

### Rules for BYOK

- Available on Free, Pro, and Dedicated.
- Preserve explicit per-Invocation source selection.
- Support Account BYOK first; retain tenant BYOK for host applications that let
  their own customers fund models.
- Store encrypted secrets and show only metadata after write.
- Let customers rotate and revoke without changing nvoken runtime credentials.
- Continue charging for nvoken runtime usage. BYOK removes model cost, not
  nvoken's durability, execution, storage, and operations value.

### Rules for platform-funded access

- Keep provider keys entirely server-side; never expose or transfer them.
- Present the customer purchase as managed nvoken runtime/model usage, not the
  resale of a provider key or raw provider account access.
- Use prepaid funds and hard Account/model/day limits initially.
- Give at most a small one-time promotional balance.
- Disable automatic fallback from BYOK to platform funding.
- Show the exact credential source and estimated/settled model cost on every
  usage receipt.
- Allow an Account owner to disable platform funding completely.
- Maintain provider-level kill switches and loss limits.

A reasonable quickstart grant is **$2-$5 once**, with low concurrency and
model/day caps. If abuse controls are not ready, require a verified payment
method before promotional platform spend is usable. Free BYOK can remain
cardless.

### Provider-contract caution

This is product analysis, not legal advice.

[OpenAI's Services Agreement](https://openai.com/policies/services-agreement/)
permits using its API in customer applications made available to end users, but
prohibits reselling or transferring account access and API keys.
[Anthropic's Commercial Terms](https://www.anthropic.com/legal/commercial-terms)
similarly permit powering products offered to a customer's users while
restricting resale except where expressly approved. Anthropic also advises that
third-party tools store uploaded keys as encrypted secrets in its
[API key guidance](https://support.claude.com/en/articles/9767949-api-key-best-practices-keeping-your-keys-safe-and-secure).

Before public platform funding:

1. describe the exact integrated nvoken service and funding flow to each model
   provider;
2. obtain provider confirmation or legal review that the intended use is
   permitted;
3. reflect downstream usage-policy, region, suspension, and user-notice
   obligations in nvoken's terms;
4. never market or implement transferable access to nvoken's provider account.

BYOK should be the operational fallback if a provider does not approve platform
funding—not an automatic per-Invocation credential fallback.

## What to charge for

### Keep two economic lines

1. **Runtime usage:** durability, execution, events, tool-call coordination,
   storage, and service operation.
2. **Model usage:** charged only when the customer chooses a platform-funded
   credential.

This makes BYOK legible and prevents model markup from becoming the sole
business model.

### Runtime meter options

| Meter | Advantage | Problem | Recommendation |
| --- | --- | --- | --- |
| Invocation accepted | Extremely simple | A one-step call and a 100-step agent cost the same | Do not use alone |
| Model tokens | Already captured; familiar | Charges runtime according to provider economics and disadvantages BYOK | Use for model settlement, not runtime |
| Wall-clock/CPU time | Tracks infrastructure cost | Provider latency and durable waiting make bills hard to predict | Keep as internal COGS telemetry |
| Tool calls | Value-adjacent | Host-executed tools vary and some agents use none | Do not use alone |
| Durable runtime steps | Retry-safe, understandable, roughly proportional to work | Requires a crisp public definition | Recommended |

Define one **runtime step** as one model response durably committed to an
Invocation. Retries that do not commit do not count. Time spent durably waiting
for tool results does not count. Tool result submission itself is included.

This definition fits nvoken's existing execution checkpoints and normalized
usage receipts. Keep recording tokens, duration, events, storage, and queue
behavior internally so the meter can be validated against actual COGS.

### Pricing shapes considered

#### Option A: entirely usage-based

- $0 monthly commitment.
- Small free runtime-step allowance.
- Per-step runtime usage plus platform model cost.

This is easy to try but produces weak revenue predictability and makes support
entitlement unclear.

#### Option B: plans with included runtime steps

- Free with strict allowance and concurrency.
- Pro monthly base with included steps and published overage.
- Dedicated minimum commitment plus usage.

This is the best launch shape. It is familiar, gives nvoken recurring revenue,
and keeps a visible marginal price.

#### Option C: model markup funds the product

- Runtime is nominally free.
- Platform model spend carries the margin.
- BYOK has a separate processing fee.

Do not choose this. It makes nvoken's economics depend on customers surrendering
their provider relationship and makes BYOK feel punitive.

## Recommended launch pricing

Treat these numbers as a pricing hypothesis to test, not a permanent rate card.
Before committing publicly, run the existing load tooling against
representative one-step, tool-heavy, long-output, and high-concurrency workloads
and compare runtime steps to Cloud Run, Postgres, Redis, queue, network, support,
and observability costs.

| | Free | Pro | Dedicated |
| --- | --- | --- | --- |
| Price | $0 | **$29/month** | **From $1,000/month** |
| Deployment | Shared | Shared | Isolated data plane |
| Included runtime steps | 2,500/month | 25,000/month | Capacity- and contract-based |
| Runtime overage | Hard stop | $1 per 1,000 steps | Contracted |
| Active Invocations | 1 | 10 | Sized deployment |
| Provider credentials | BYOK; guarded one-time platform trial | BYOK or prepaid platform funding | BYOK or prepaid/invoiced platform funding |
| Runtime credentials | 2 | 10 | Contracted |
| Data retention | Short, fixed, clearly disclosed | Longer plus user deletion/export controls | Custom within supported policy |
| Support | Community | Email, best effort | Named operational channel |
| SLA | None | None initially | Contracted only after operational evidence |

The Free limits should be hard limits, not surprise charges. Pro may continue
only with explicit overage consent or a prepaid runtime balance.

### Platform model price

For platform-funded calls:

```text
customer charge = provider model cost + platform funding margin
```

Start with **provider list price plus 15%**, separately from runtime steps.
Mobius currently uses a thinner model-cost markup, but a new public runtime has
fraud, payment, support, price-drift, and reconciliation exposure that model
list price does not cover. Reprice from actual loss and payment data; do not
promise perpetual pass-through pricing.

The dashboard should display dollar amounts and token details even if the
prepaid ledger uses cents-denominated credits internally.

### Why $29

The current market spans low-cost developer plans, $50 workflow plans, and
$75-plus durable infrastructure tiers. $29:

- is low enough for a production-minded individual developer;
- is high enough to establish a paid support and reliability relationship;
- does not require hiding the runtime price inside model markup;
- leaves room for a later Team tier if membership, audit, and higher
  concurrency become real demand.

Do not launch Pro, Team, Business, Enterprise, and several credit packs at once.
Free, Pro, and a contact-based Dedicated offer are sufficient to learn.

## Minimal website and console

### Positioning

Use the mature category and state the difference:

> Durable agent turns for multi-tenant applications.

Supporting copy:

> Send a spec and input. nvoken keeps the Session running through disconnects,
> tool calls, retries, and deploys—without taking ownership of your agents or
> product data.

Primary actions:

- **Get an API key**
- **Run it yourself**

The website should repeatedly clarify that the open-source and managed products
use the same API and SDKs.

### Public site

A static or documentation-backed site needs only:

1. **Home**
   - one-sentence contract;
   - a short TypeScript quickstart;
   - durability, tenant isolation, and model-routing proof points;
   - Cloud versus self-hosted choice;
   - pricing summary and calls to action.
2. **Docs**
   - existing concepts, API, SDKs, self-hosting, and Cloud quickstart;
   - one canonical newcomer path.
3. **Pricing**
   - the three offers;
   - exact runtime-step definition;
   - model funding examples;
   - limits and retention.
4. **Status**
   - hosted externally or kept operationally independent from the main cloud.
5. **Sign in**
   - enters the small authenticated control plane.

A separate blog, template gallery, integration marketplace, customer community,
or content CMS is not required to launch.

### Authenticated console

The first console should do only five jobs:

1. **Quickstart**
   - cloud endpoint;
   - one-time display of a newly created runtime credential;
   - copyable TypeScript and curl invocation;
   - visible success status.
2. **Runtime credentials**
   - create, name, last-used metadata, rotate, revoke.
3. **Model credentials**
   - add/rotate/revoke Account BYOK;
   - show tenant-BYOK API guidance rather than building an end-user secret UI;
   - choose whether platform funding is allowed.
4. **Usage and funding**
   - runtime steps, active Invocation limit, provider tokens/cost, prepaid
     balance, spend caps, and invoices.
5. **Account and data**
   - owner identity;
   - plan;
   - retention disclosure;
   - export/delete controls;
   - support link.

A small read-only list of recent failed Invocations may be worth adding because
it reduces support cost. It should link to runtime/API detail rather than
becoming an observability product.

### Explicit non-goals

Do not build these for the initial managed offering:

- agent definitions or an agent editor;
- prompt/version registry;
- tool or integration marketplace;
- workflow/loop builder;
- playground;
- project hierarchy;
- full team RBAC;
- evaluation suite;
- broad analytics;
- general-purpose product database;
- custom model gateway abstractions beyond nvoken's current credential/model
  contract.

The customer should arrive with an application and leave with an endpoint and a
credential.

### Signup flow

The ideal first session is under five minutes:

1. Sign in with email or GitHub.
2. Create one nvoken Account automatically.
3. Issue one runtime credential and reveal it once.
4. Show a copyable SDK example using a small guarded platform trial, if
   eligible.
5. Complete a durable Invocation.
6. Prompt the developer to add BYOK or fund a prepaid balance.
7. Show the runtime-step and model-cost receipts for that Invocation.

Use an external identity provider if it shortens secure delivery; Mobius already
provides operational familiarity with Clerk. Do not copy Mobius's organization,
project, invitation, and role surface until the managed nvoken customer model
requires it. One owner and one Account are enough for the first release.

## What to learn from Mobius Cloud

The current Mobius Cloud codebase is useful evidence that the team can operate a
multi-tenant Go/React application on Cloud Run, Postgres, and Redis. The most
portable lessons are below.

### Copy these principles

1. **Authenticated scope is immutable.** Mobius carries organization and project
   scope on tenant data and derives it from authentication. Managed nvoken
   should do the same with Account and, where required, tenant scope.
2. **Human and machine identity are separate.** Human sign-in manages the
   account; application credentials invoke the runtime.
3. **Provisioning is transactional and idempotent.** Signup must not create a
   half-usable Account.
4. **Billing is durable accounting.** Usage-event identity, append-only ledger
   entries, canonical payment reads, and reconciliation matter more than a
   polished billing screen.
5. **Funding is checked before expensive work.** A negative balance discovered
   after a provider call is not a useful control.
6. **Infrastructure configuration is one deployable path.** Mobius's history
   includes production functionality being disabled by a skipped manual secret
   wiring step. Managed nvoken should make provider, billing, and deployment
   configuration reproducible and fail closed.
7. **Support administration is separate and auditable.** Internal access is not
   tenant RBAC.

### Do not copy this product surface

Mobius has accumulated appropriate complexity for its product: organizations,
projects, workflows, loops, integrations, seats, roles, invitations, broad
navigation, monthly allowances, wallets, credit purchases, reconciliation, and
many product-specific resources.

nvoken Cloud should copy the hard-won isolation, accounting, and deployment
semantics without copying that surface area. Its promise is a runtime endpoint,
not another application operating system.

## Launch sequence

### Phase 0: operator-run private cloud

Goal: prove the multi-Account data plane and operational loop before public
signup.

- Add managed Account provisioning and dynamic runtime identity.
- Operate one shared staging/prod deployment.
- Provision a small number of Accounts manually.
- Begin with BYOK and manually granted, tightly capped platform funding.
- Validate backup/restore, deploy-during-execution, queue recovery, provider
  failure, Account suspension, and usage reconciliation.
- Measure per-step COGS across representative workloads.

Exit evidence:

- at least three independent customer Accounts;
- automated cross-Account isolation tests and live probes;
- one restore drill;
- one deploy-under-load drill;
- billing facts reconcile to canonical runtime usage;
- an Account can be deleted through the documented lifecycle.

### Phase 1: public Free

Goal: make first successful use self-serve without taking open-ended financial
or data risk.

- Minimal public site and console.
- Email/GitHub login and automatic Account provisioning.
- BYOK, hard runtime limits, fixed retention, deletion, and abuse controls.
- Guarded promotional platform balance only if provider and fraud boundaries are
  ready.
- Public status page and support policy.
- No SLA.

### Phase 2: Pro

Goal: establish repeatable revenue from shared production usage.

- Stripe subscription and prepaid funding.
- Durable ledger, idempotent usage events, spend caps, and reconciliation.
- Published overage behavior.
- Higher concurrency and retention.
- Email support.
- Price and included steps revised from actual Phase 0/1 COGS and conversion.

### Phase 3: Dedicated

Goal: sell isolation without forking the product.

- Manually provision isolated data planes from one paved infrastructure path.
- Shared release train and control plane.
- Contracted capacity, region, retention, support, and availability.
- Minimum price high enough to cover the whole deployment and human operations.

### Phase 4: BYOC, only on demand

Goal: unblock enterprise cloud-boundary requirements.

- One cloud provider and one paved topology.
- Explicit control-plane/data-plane connectivity contract.
- Automated upgrades, rollback, support access, and drift detection.
- Price as an enterprise operated service, not a cheap variation of
  self-hosting.

## Public-launch gates

Do not open unbounded public signup until all critical gates below are true.

### Critical

- [ ] Shared managed mode supports multiple Accounts without the fixed
      installation-Account assumption.
- [ ] Cross-Account authorization and storage isolation are continuously tested.
- [ ] Account, tenant, Session, and Invocation deletion/retention semantics are
      implemented and documented.
- [ ] Per-Account concurrency, queue, usage, and platform-spend limits fail
      closed.
- [ ] Platform model credentials have provider approval/legal review, encrypted
      storage, rotation, and emergency revocation.
- [ ] Runtime usage events are idempotent and reconcile to canonical receipts.
- [ ] Prepaid funds cannot go materially negative under concurrent calls.
- [ ] Backup restoration and deploy-during-execution drills have current
      evidence.
- [ ] Signup/provisioning is idempotent and cannot leave an unusable Account.
- [ ] Abuse suspension does not corrupt already durable state.

### Important

- [ ] Terms, privacy policy, acceptable-use policy, retention, and subprocessors
      describe the actual service.
- [ ] A public status page and incident communication path exist.
- [ ] Provider price-catalog drift is monitored.
- [ ] Internal support access is separate, least-privileged, and audited.
- [ ] Cost dashboards attribute shared infrastructure and platform model spend
      by Account.
- [ ] Free-tier hard-stop behavior is visible before work is rejected.

### Defer

- [ ] Team invitations and RBAC.
- [ ] Annual contracts.
- [ ] Formal Pro SLA.
- [ ] Multiple regions.
- [ ] Multiple dedicated topologies.
- [ ] BYOC.
- [ ] Agent/product UI.

## Decisions and open questions

### Recommended decisions now

1. Build shared Cloud first.
2. Keep Dedicated as a manually provisioned premium option.
3. Keep BYOC off the initial roadmap.
4. Support BYOK on every plan.
5. Offer platform funding only as capped/prepaid integrated usage.
6. Meter runtime by committed durable model responses.
7. Launch with Free, $29 Pro, and $1,000-plus Dedicated as hypotheses.
8. Use a thin private hosted control plane; keep agent/product concerns out.
9. Block public launch on multi-Account bootstrap, quotas, and deletion.

### Questions to answer with evidence

- What is the distribution of model-call duration, output size, tool waits, and
  steps per Invocation in realistic workloads?
- Does one runtime step remain acceptably correlated with infrastructure cost at
  high concurrency and long streaming responses?
- How many developers complete first use with BYOK-only versus a guarded
  platform trial?
- Do qualified buyers ask for dedicated infrastructure, BYOC, private
  networking, or merely stronger data terms?
- Which retention period preserves the Session value proposition while keeping
  a safe free tier?
- Is $29 a meaningful production commitment or should the first paid plan be
  $49 with higher included usage?
- Can provider platform funding be offered under standard terms, or does either
  provider require explicit approval?

The first three months should optimize for answers to these questions, not for
maximizing plan count or console features.

## Bottom line

Managed nvoken should be the shortest path from an application backend to a
durable agent runtime:

```text
sign in -> get endpoint and credential -> invoke -> see durable result
```

The defensible product is not a large agent-development suite. It is the
operated version of nvoken's existing contract: durable turns, Sessions,
tool-call coordination, tenant isolation, and model routing while the host
application remains the source of truth.

A shared multi-customer cloud is the right default. Dedicated data planes are a
valuable paid isolation option. BYOC is a later enterprise operation. BYOK and
prepaid platform funding should coexist, with separate runtime and model
economics. The website and console should expose only what is necessary to
obtain, fund, operate, and delete that runtime.

## Sources

### Local product and implementation

- [nvoken README](../../README.md)
- [Product overview](../product/overview.md)
- [Architecture](../design/architecture.md)
- [Data retention](../guides/data-retention.md)
- [Per-provider credential modes PRD](../prds/028-prd-per-provider-credential-modes.md)
- [Google Cloud deployment](../../deploy/google-cloud/README.md)
- Current `../mobius-cloud` implementation and its tenancy, authentication,
  authorization, billing, and deployment documentation, inspected 2026-07-23.

### Official external sources

- [LangSmith pricing](https://www.langchain.com/pricing)
- [Letta pricing](https://docs.letta.com/pricing)
- [AWS Bedrock AgentCore pricing](https://aws.amazon.com/bedrock/agentcore/pricing/)
- [Trigger.dev pricing](https://trigger.dev/pricing)
- [Restate Cloud](https://www.restate.dev/cloud)
- [Hatchet pricing](https://hatchet.run/pricing)
- [Agentuity pricing](https://agentuity.com/pricing)
- [Cloudflare Agents documentation](https://developers.cloudflare.com/agents/)
- [OpenAI Services Agreement](https://openai.com/policies/services-agreement/)
- [Anthropic Commercial Terms](https://www.anthropic.com/legal/commercial-terms)
- [Anthropic API key guidance](https://support.claude.com/en/articles/9767949-api-key-best-practices-keeping-your-keys-safe-and-secure)

### Qualitative community sources

- [Hacker News: "We chose LangGraph to build our coding agent"](https://news.ycombinator.com/item?id=43468435)
- [Hacker News: "Do you roll your own agent framework or use an existing framework?"](https://news.ycombinator.com/item?id=45502646)
- [Reddit: "Is there an open-source runtime for production AI agents?"](https://www.reddit.com/r/AI_Agents/comments/1ribz4g/is_there_an_opensource_runtime_for_production_ai/)
- [Reddit: "Open-source Agent Protocol implementation"](https://www.reddit.com/r/LangChain/comments/1mgoa6o/opensource_agent_protocol_implementation/)
- [Reddit: "Do production agents need their own kind of PaaS?"](https://www.reddit.com/r/AI_Agents/comments/1v2d1lj/do_production_agents_need_their_own_kind_of_paas/)
