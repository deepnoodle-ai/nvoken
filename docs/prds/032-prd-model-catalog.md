# Serve a discoverable model catalog from one Models API

**Status:** Implemented
**Sequence:** 032
**Depends on:** `028-prd-per-provider-credential-modes.md` for canonical
provider identity, and the existing `/v1/model-pricing-capabilities` endpoint
this PRD supersedes.

## ELI5

A host can ask nvoken which models it advertises and inspect any exact
provider/model selection before invoking it. Model metadata and nvoken's
standard cost-cap pricing live together under one `/v1/models` API. This does
not claim that the caller's provider account can access a model.

## Why

nvoken currently exposes only `GET /v1/model-pricing-capabilities`, an exact
pricing preflight that cannot enumerate models or grow naturally to describe
context windows and input modalities. Hosts otherwise have to read nvoken
source and provider documentation to build a model selector.

`dive` supplies model-id constants and pricing but no enumerable metadata
catalog. Mobius Cloud proves the useful pattern: curate the advertised model
set locally, seed ids from `dive`, and keep pricing tied to the pinned `dive`
release. nvoken needs that pattern as a small public API rather than another
top-level compound path.

## Outcome

A host can list nvoken's curated models or inspect one exact provider/model
selection through a single Models API family. Catalog membership, descriptive
metadata, and local cost-cap pricing are machine-distinguishable, and adding a
model fact remains an additive schema change.

## Scope

**In:** authenticated `GET /v1/models` and `GET
/v1/models/{provider}/{model_id}`; a flat embedded catalog; exact
provider/model identity; tolerant inspection of uncataloged ids; standard USD
pricing from `dive`; context and output limits, input modalities, display
metadata, recommendation, deprecation, catalog versioning, conditional caching,
SDK and CLI adoption, and retirement of `/v1/model-pricing-capabilities`.

**Out:** `/v1/providers`; provider credential or account-access checks; model
writes; tool, streaming, or reasoning flags; knowledge cutoff, model aliases,
per-model rate limits, image-generation, embedding, or speech pricing; a global
or per-provider default-model pointer; pagination; catalog persistence; and
billing, credits, or checkout.

## Contract

Both endpoints are installation-global reads under the `Models` OpenAPI tag.
`/v1/capabilities` remains the home for installed adapters and protocol
features; `/v1/models` describes model selections.

### List models

`GET /v1/models` returns model resources directly in the existing `items`
envelope, not provider groups:

```json
{
  "items": [
    {
      "provider": "anthropic",
      "id": "claude-opus-4-8",
      "cataloged": true,
      "display_name": "Claude Opus 4.8",
      "description": "Highest-capability Anthropic model.",
      "context_window_tokens": 1000000,
      "max_output_tokens": 64000,
      "input_modalities": ["text"],
      "recommended": false,
      "deprecated": false,
      "pricing": {
        "status": "priced",
        "currency": "USD",
        "unit": "per_million_tokens",
        "input": "5",
        "output": "25",
        "cache_read": "0.5",
        "cache_write": "6.25",
        "updated_at": "2026-05-28",
        "pricing_version": "b3f91e2a"
      }
    }
  ],
  "catalog_version": "opaque-version"
}
```

`provider` filters to one canonical provider. `include_deprecated` is an
optional boolean and defaults to `false`. The response is the complete bounded
catalog with no pagination. Ordering is deterministic but carries no semantic
meaning; consumers use fields rather than position. Every listed item has
`cataloged: true`.

### Inspect a model selection

`GET /v1/models/{provider}/{model_id}` returns a descriptor for any installed
provider and nonempty model id, not only catalog entries. A cataloged selection
returns the full resource above. An uncataloged selection returns `provider`,
`id`, `cataloged: false`, and `pricing`; curated fields are omitted. This keeps
the exact cost-cap preflight without adding another top-level API noun.

The provider is normalized to its canonical value in the response. `model_id`
is the exact 1–255-character value used in `spec.model.id`. Clients must
percent-encode it as one path segment, including `/`; generated SDK and
conformance tests must prove round-trip behavior for reserved and Unicode
characters. Catalog response provider ids are extensible lowercase strings, not
a closed generated enum; request validation still accepts only providers
installed in nvoken's canonical adapter registry.

`cataloged` means nvoken advertises and maintains metadata for the selection. It
does not prove provider-account access, regional availability, credential
availability, or the identity a provider will ultimately report.

### Pricing

`pricing.status` is always `priced`, `unpriced`, or `unknown`, with the same
cost-cap meaning as the superseded endpoint. `priced` requires `currency`,
`unit`, `input`, `output`, `updated_at`, and `pricing_version`; cache fields are
present only when nvoken has a distinct rate for them. `unpriced` and `unknown`
contain only `status` and `pricing_version`.

Rates are non-negative base-10 decimal strings without exponent notation. Their
conversion from nvoken's embedded rate data must be deterministic and tested
against every registered rate. `pricing_version` is an opaque nvoken-owned
identifier that clients compare for equality only. Neither its value nor any
other response field exposes an upstream library or dependency version. Rates
are standard estimated-cost inputs used by nvoken's guardrail, not authoritative
provider billing.

## Requirements

- **R1 — One model-centered API.** The public surface must be exactly the flat
  collection and tolerant descriptor read above. It must not add
  `/v1/providers` or another top-level model-pricing path. The design packet
  must distinguish model selection metadata from installation capabilities.

- **R2 — Curated advertised set.** The list must contain the intentional
  text-generation models nvoken advertises, keyed uniquely by canonical
  `(provider, id)` and seeded from `dive` model constants. It is an advertised
  subset, not a claim to enumerate every id an adapter or pricing registry may
  accept. The catalog is embedded in the binary and needs no database.

- **R3 — Tolerant exact descriptor.** The item read must return `200` for every
  valid installed provider and model id, with catalog membership explicit.
  Invalid providers or malformed ids return the standard `400`; authentication
  and rate-limit failures retain existing conventions. Encoded ids must
  round-trip without normalization.

- **R4 — Pricing equals guardrail evidence.** Pricing status must remain scoped
  to the requested provider and exact id, while rate fields must equal the
  standard `dive` pricing used by Invocation estimated-cost enforcement. Because
  `dive` currently resolves usage cost by model id alone, installed provider
  pricing tables must have no cross-provider id collision; a collision fails
  the build until the enforcement seam becomes provider-aware. The public schema
  and values must use nvoken-owned vocabulary and must not expose `dive`, Go
  module versions, or another dependency identity.

- **R5 — Honest maintained metadata.** Unknown context, output, or modality
  values must be omitted, never guessed. `input_modalities` describes inputs
  nvoken currently accepts for the selection, not every native provider
  capability. Each provider must have exactly one active `recommended` model,
  meaning nvoken's suggested general-purpose starting point, not a hidden
  Invocation default. Deferred facts arrive only as new optional fields.

- **R6 — Complete, cacheable collection.** The unpaginated list must always
  return the complete matching catalog. Both reads must expose a deterministic
  `ETag`; the list also returns an opaque `catalog_version`. Matching
  `If-None-Match` requests return `304`.

- **R7 — Drift and evolution protection.** Tests must reject duplicate keys,
  invalid providers, catalog ids no longer backed by their intended `dive`
  constants, contradictory recommendation/deprecation state, pricing that
  differs from enforcement, and model-id collisions across installed pricing
  tables. A newly priced but unlisted model is review evidence, not an automatic
  catalog addition. Catalog response provider ids must remain decodable by an
  older generated SDK when a new provider is added.

- **R8 — Coordinated pre-freeze replacement.** Remove
  `/v1/model-pricing-capabilities` without a compatibility alias, record the
  change in `docs/design/decisions.md`, and update `docs/design/api.md` and the
  PRD index. Regenerate the four SDKs and conformance server, update handwritten
  facades and `sdk/operations.json`, migrate the `nvoken model pricing` command,
  and replace active guide, onboarding, and test references.

## Acceptance

- [x] **A1 (R1, R2, R5, R6):** An authenticated list returns a flat, complete
  `items` array of active advertised models with unique compound identity,
  maintained metadata, pricing, and one recommendation per provider. Provider
  filtering works; deprecated entries appear only when requested.

- [x] **A2 (R3):** Cataloged, priced-uncataloged, unpriced, and unknown model
  lookups return the required descriptor shapes. Invalid input returns `400`.
  All four SDKs and the conformance server round-trip a model id containing `/`
  and representative reserved and Unicode characters.

- [x] **A3 (R4):** Every priced descriptor exposes the same standard rate and
  update date used to compute Invocation estimated cost. Cross-provider
  misassociation and any duplicate priced model id fail before release.
  Responses contain an opaque `pricing_version` and no upstream library or
  dependency name or version.

- [x] **A4 (R5, R7):** Removing a referenced `dive` constant, duplicating a
  compound key, fabricating unknown metadata, recommending zero or two active
  models for a provider, or creating contradictory deprecated state fails with
  a clear message. Adding an unrelated priced id does not silently advertise it.

- [x] **A5 (R6):** Equal catalog builds produce equal versions and ETags, a
  metadata or pricing change changes them, and matching conditional reads return
  `304` without a JSON body.

- [x] **A6 (R7):** A compatibility fixture containing a future provider id can
  be decoded by SDKs generated from the initial Models contract without losing
  the raw identifier.

- [x] **A7 (R8):** No active source, OpenAPI, SDK facade, CLI, guide, onboarding
  script, or conformance reference uses `model-pricing-capabilities`.
  Governing docs record the Models/capabilities boundary, and `make check`,
  `make sdk-check`, and `make onboarding-check` pass.

## Risks

- Curated capabilities can become stale even when ids and pricing do not. Model
  updates therefore require source review for every maintained metadata field.
- Percent-encoded slash handling varies across proxies. The public deployment
  path must pass the same reserved-character conformance case as the local
  server before the endpoint is called portable.
- Adding a provider remains an adapter capability change. The open response
  identifier prevents list decoding from breaking, but callers still need a
  current SDK or raw string support to select the new provider.
