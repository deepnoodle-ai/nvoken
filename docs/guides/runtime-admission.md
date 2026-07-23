# Runtime admission

> **API behavior reference.** For a first working application, complete
> [Run nvoken locally](run-locally.md) and start from an SDK quickstart. Return
> here when you need durable retry, Session, streaming, cancellation, budget,
> structured-output, or ToolCall semantics.

The self-contained Runtime durably admits, executes, and reads Invocations,
including structured output and host-executed host tools. Admission still
returns before model generation begins.

This guide explains the runtime, not a production-readiness claim. The exact
single-daemon operating boundary and its current proof status live in the
[production-readiness profiles and evidence matrix](../testing/production-readiness-profiles.md).

This reference assumes a Runtime is already serving, the application has a
Runtime credential, and at least one provider key is configured. The local Run
command prepares those inputs automatically. Production operators should use a
[deployment profile](../../deploy/single-daemon/README.md) and the
[credential guide](credentials-and-cli-auth.md), not reconstruct daemon
configuration from API examples in this document.

The self-hosted default is `installation_byok`, so an omitted credential
selection uses the matching `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`. If that
selected key is unavailable, the Invocation fails durably with
`credential_unavailable`; nvoken never falls back to another credential source,
provider, or ambient SDK credential.

To enable caller-ephemeral, Account BYOK, or tenant BYOK, configure an
application-layer AES-256-GCM keyring outside Postgres. The value is a JSON
object mapping nonsecret key IDs to base64-encoded 32-byte keys. Keep old keys
present for decryption while rotating the active ID:

```bash
export PROVIDER_CREDENTIAL_ACTIVE_KEY_ID='v1'
export PROVIDER_CREDENTIAL_ENCRYPTION_KEYS="{\"v1\":\"$(openssl rand -base64 32)\"}"
```

Provider credential lifecycle authority comes from the durable credential
profile described in [Credentials and CLI authentication](credentials-and-cli-auth.md).
An Operator credential can manage Account and tenant credentials. A Runtime
credential can manage tenant credentials only when its `tenant_key` constraint
matches that exact tenant; it cannot manage Account BYOK. A Viewer credential
can list and read secret-free Account and tenant credential metadata but cannot
create, rotate, or revoke credentials. The configured `RUNTIME_API_KEY` is
imported as a Runtime credential; use an Operator user or machine credential for
Account lifecycle requests.
Create and rotate endpoints accept a secret but return metadata only:

```bash
curl --fail-with-body http://localhost:8080/v1/provider-credentials \
  -H "Authorization: Bearer $NVOKEN_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "provider": "openai",
    "scope": "account",
    "credential": {"api_key": "replace-with-an-OpenAI-key"},
    "idempotency_key": "openai-account-v1"
  }'

curl --fail-with-body http://localhost:8080/v1/provider-credentials \
  -H "Authorization: Bearer $NVOKEN_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "provider": "anthropic",
    "scope": "tenant",
    "tenant_key": "customer-482",
    "credential": {"api_key": "replace-with-an-Anthropic-key"},
    "idempotency_key": "customer-482-anthropic-v1"
  }'
```

List, get, rotate, and revoke use `/v1/provider-credentials`; see the OpenAPI
contract for filters and rotation overlap. At most one active credential exists
for each Account-or-tenant/provider tuple. Revocation destroys its live
ciphertext and blocks the next model call for every bound nonterminal
Invocation. Rotation binds new Invocations to the new immutable version; an
explicit overlap can keep already-bound old versions usable for up to one hour.
If the current version expires, its root remains `active` so an operator can
rotate or revoke it, but `version_status=expired` means the credential is
unusable; new admissions and subsequent model calls fail closed until rotation.

`POST /v1/invocations` may include exactly one source selection matching the
spec provider. Reusable selections contain no secret:

```json
"provider_credentials": [{
  "provider": "openai",
  "source": "account_byok"
}]
```

Tenant BYOK uses `tenant_byok`; the effective `tenant_key` must match. A
one-Invocation secret uses `caller_ephemeral` and is encrypted in its durable
binding:

```json
"provider_credentials": [{
  "provider": "anthropic",
  "source": "caller_ephemeral",
  "credential": {"api_key": "one-invocation-key"}
}]
```

Selection is outside the execution spec and transcript. Fingerprint v6 records
literal omission or the explicit nonsecret source, never the raw secret or the
resolved reusable version. Therefore an equal retry with a changed supplied
secret returns the original Invocation and binding rather than replacing it.
Caller ciphertext remains available through the Invocation wall-clock deadline
plus `PROVIDER_CREDENTIAL_CLEANUP_GRACE` (default five minutes), including while
the Invocation is waiting for host tools. Terminal settlement clears live
ciphertext in the same database transaction, and the reaper clears expired
material. Retained backups can still contain encrypted bytes until normal
backup expiration; cleanup does not claim immediate physical erasure there.

`MODEL_CREDENTIAL_DEPLOYMENT_MODE=cloud` disables `installation_byok` and may
use `platform`; `PLATFORM_FUNDING_ENABLED=true` is the deployment-owned funding
allow hook and platform provider keys use `PLATFORM_ANTHROPIC_API_KEY` and
`PLATFORM_OPENAI_API_KEY`. Cloud mode requires the encryption keyring at
startup. Self-hosted mode rejects `platform`. The installation default is set
with `INVOCATION_DEFAULT_CREDENTIAL_SOURCE`; `caller_ephemeral` cannot be a
default because it always needs request material.

Engine capacity and timing are
bounded by `ENGINE_CONCURRENCY`, `ENGINE_POLL_INTERVAL`,
`ENGINE_LEASE_DURATION`, `ENGINE_HEARTBEAT_INTERVAL`,
`ENGINE_REAPER_INTERVAL`, `ENGINE_REAPER_BATCH_LIMIT`, and
`ENGINE_DRAIN_GRACE`. `ENGINE_EXECUTION_SEGMENT_CEILING` bounds one model
segment, and `ENGINE_SETTLEMENT_RESERVE` stops model work early enough to
persist its outcome. `SHUTDOWN_TIMEOUT` bounds HTTP shutdown and the overall
component join; engine drain grace must be shorter than that total. Defaults are
suitable for local self-contained operation. The Cloud Run paved path overrides
both shutdown values to fit the platform termination window.

Live output uses an in-process bounded fan-out by default. Set `REDIS_URL` to a
`redis://` or `rediss://` URL when publishers and stream handlers run in
different processes; `cloud_tasks` execution requires it. `REDIS_PASSWORD`
overrides a URL-embedded password, and `REDIS_CA_CERT` accepts one or more PEM
CAs for server-authenticated TLS. The Google paved path supplies both from
Memorystore and Secret Manager. `LIVE_EVENT_BUFFER`,
`STREAM_POLL_INTERVAL`, `STREAM_KEEPALIVE_INTERVAL`,
`STREAM_MAX_LIFETIME`, and `STREAM_WRITE_TIMEOUT` bound loss, reconciliation,
rotation, and slow clients. Redis carries previews only. A Redis failure cannot
change execution or committed recovery state.

`INVOCATION_DEFAULT_TOTAL_TIMEOUT`,
`INVOCATION_DEFAULT_ACTIVE_TIMEOUT`, and
`INVOCATION_DEFAULT_MAX_ITERATIONS` supply omitted limits. The corresponding
`INVOCATION_MAX_*` settings reject requests above installation policy; output
tokens and estimated cost have no default and remain unlimited unless the host
requests them. `DATABASE_MAX_CONNS` must be at least two because the
cross-process cancellation listener reserves one pool connection.

`max_estimated_cost_usd` is a fail-closed list-price guardrail, not a charge
reservation or billing ledger. It requires known USD pricing for the exact
selected model. When the local pricing registry can determine that price data
is absent, nvoken fails the Invocation before a provider call with
`budget_exceeded` and `details.kind = "estimated_cost_unavailable"`. If an
adapter can determine the gap only from returned usage, the same public reason
is used after the call. Omit the field when trying a newly available model whose
price metadata has not yet shipped.

Before admitting capped work, an authenticated host can inspect the exact
provider/model capability without making a provider call:

```bash
curl --silent --show-error --fail --get \
  http://localhost:8080/v1/model-pricing-capabilities \
  -H "Authorization: Bearer $NVOKEN_API_KEY" \
  --data-urlencode provider=openai \
  --data-urlencode "model=$NVOKEN_MODEL"
```

`priced` means the local registry has standard USD pricing for the exact model;
`unpriced` means it knows that entry is absent; and `unknown` means the adapter
cannot decide before execution. The response also includes the local
`registry_version`. This preflight reports enforcement capability only. It does
not call the provider, verify account access, or guarantee which model and usage
evidence the provider will ultimately serve.

The same check is available through the Go facade and CLI:

```bash
nvoken model pricing --provider openai --model "$NVOKEN_MODEL"
```

A request may add `spec.output.schema` for one validated object result. nvoken
admits a bounded JSON Schema subset, presents it as the reserved
`nvoken_submit_output` builtin, and persists its request/result through the
normal durable ToolCall path. An omitted iteration budget resolves to three (or
the lower installation maximum); an explicit value below two is rejected. The
model must submit a valid object and then finish with a normal assistant
response. Prose or fenced JSON never substitutes for the tool submission.
Patterns are limited to 1,024 UTF-8 bytes. The accepted ToolCall is internal
checkpoint evidence; public `structured_output` remains null until fenced
terminal settlement commits the final object and provenance together.

On its first start, nvoken serializes creation of one installation Account, its
default tenant partition, the local bootstrap Owner membership, and one durable
`Runtime` machine credential derived from `RUNTIME_API_KEY`.
`RUNTIME_TENANT_KEY` optionally confines that imported credential to one tenant
partition. Later starts resolve the import marker rather than configuration, so
changing the environment cannot create a second credential and revocation is
never undone. Keep the configured value only for the documented rollback
window; after explicit cutover a current binary starts without it. The durable
row stores only a nonreversible verifier, not the bearer secret.

Submit one turn:

```bash
curl --fail-with-body http://localhost:8080/v1/invocations \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "agent_key": "support-triage",
    "session_key": "ticket-483",
    "idempotency_key": "ticket-483:first-reply",
    "input": {"content": [{"type": "text", "text": "Why was I charged twice?"}]},
    "spec": {
      "instructions": "You are a concise billing support agent.",
      "model": {"provider": "anthropic", "id": "claude-sonnet-5"},
      "limits": {
        "total_timeout_seconds": 600,
        "active_timeout_seconds": 300,
        "max_output_tokens": 4096,
        "max_iterations": 1
      }
    }
  }'
```

A committed request returns `202` with durable Agent, Session, and Invocation
IDs before generation. The engine reconstructs the turn from Postgres and the
Invocation eventually becomes `completed` or `failed`. If the acknowledgement
is lost, retry the exact request and
`idempotency_key`; nvoken returns the original IDs with `deduplicated: true`.
A changed request using the same scoped key returns
`409 idempotency_conflict`.
Treat `503 unavailable` the same way as any ambiguous acknowledgement: retry
the exact body and key rather than inventing a new key for the same turn.

Canonical messages remain durable evidence even when a later budget or
deadline settlement fails the Invocation. Future provider requests include the
failed Invocation's user input but exclude its assistant and tool messages, so
a rejected answer cannot silently steer a later turn. Hosts can make the same
distinction by joining each message's `invocation_id` to the authoritative
Invocation status.

The database retains checkpoint evidence at each accepted model
iteration: the canonical assistant message, normalized usage/provenance
receipt, any prepared ToolCalls, and a transcript watermark commit together.
Accepted builtin outcomes likewise commit one canonical tool-role result and a
fenced checkpoint. ToolCall rows contain lifecycle and message references, not
a second copy of content.

Declare a host tool inside `spec.tools` with `name`, `description`,
`mode: "host"`, and a bounded object `input_schema`. When the model selects
it, Invocation and Session reads expose the stable pending call after the
Invocation parks in `waiting`; no engine lease or request stays alive. Submit a
result with the returned ID. Tools require at least two model iterations;
omission resolves to three or the lower installation maximum.

```bash
curl --fail-with-body \
  -X POST http://localhost:8080/v1/invocations/invk_…/tool-results \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "results": [{
      "tool_call_id": "tcal_…",
      "content": {"order_id": "order-123", "state": "ready"}
    }]
  }'
```

The command accepts 1-32 results atomically. Equal retries return `202` with
`deduplicated: true`; changed retries return `tool_result_conflict`. Partial
batches remain `waiting`. The transaction that accepts the final pending call
moves the same Invocation to `queued`; embedded mode wakes after commit and
Cloud Tasks mode creates the successor dispatch in that transaction. Use
`is_error: true` to return a model-visible tool failure. Cancellation and the
wall deadline remain first-writer-wins.

Declare a callback tool with the same name, description, and input schema plus
`mode: "callback"` and `callback: {"url": "https://..."}`. Callback admission
is enabled only when the installation has `CALLBACK_SIGNING_KEY`,
`CALLBACK_SIGNING_KEY_ID`, and `CALLBACK_SIGNING_KEY_VERSION`. nvoken commits
the request and a blocked delivery before parking, then a background worker
sends and retries it. Callback calls do not appear in `pending_tool_calls`, and
the public client-result command cannot settle them. See
[Callback receivers](callback-receivers.md) for the signed wire contract and
idempotency requirement.

When an execution lease expires, nvoken accrues the abandoned segment only
through its recorded lease/deadline boundary, moves the same Invocation back to
`queued`, and preserves its checkpoint evidence and open builtin ToolCalls. A
replacement owner validates that evidence before continuing. A committed final
model response is not requested again; model work that completed but did not
commit its checkpoint may run and be billed again. Cancellation and logical
deadlines remain terminal and close prepared calls with a synthetic result.
There is still no public retry-in-place operation for terminal Invocations.

Public reads and streams may observe `running → queued → running` during this
recovery. Use lifecycle revision/cursors to order observations. An internal
recovery failure means the stored transcript/checkpoint evidence was
inconsistent and is exposed only as the bounded public `internal` failure.

The execution-segment ceiling reserves time for a live owner to settle, not a
hard terminal boundary after ownership is gone. A live owner or deadline reaper
observing the cutoff before lease expiry writes the segment-scoped deadline
failure. If no settlement lands and the lease itself later expires, recovery
wins because a replacement cannot distinguish a crashed owner from a delayed
settle. Wall-clock and active-execution limits remain hard limits across that
continuation; operators should keep the reaper cadence well below the lease
duration for prompt outcomes.

In `cloud_tasks` mode, lease recovery deliberately keeps the active published
dispatch so the original task retry is the fastest continuation path. If Cloud
Tasks has exhausted or lost that delivery, the dispatch reconciler settles the
stale publication and creates one successor for the queued Invocation. Keep the
reconcile interval and aged-dispatch alerts tighter than the recovery latency
your product promises; removing the active dispatch during reaping would lose
this original-task retry path.

Read the durable state after any API restart:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  http://localhost:8080/v1/invocations/invk_…

curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  http://localhost:8080/v1/sessions/sesn_…
```

The Invocation read includes terminal `error`, normalized aggregate `usage`,
model `provenance`, resolved `limits`, accrued `active_execution_ms`, and its
wall-clock deadline. It also always includes nullable `structured_output` and
`structured_output_provenance`. A successful schema-bearing Invocation returns
the validated object and its reserved ToolCall ID/schema digest; failed,
cancelled, or schema-free Invocations return null for both. If no valid
submission was accepted, the failure code is `structured_output_unsatisfied`
with a bounded missing, invalid, or oversized reason.

To read the answer itself, use the composed result read. It returns the
authoritative Invocation, this Invocation's canonical messages, and
`output_text`, the assistant text concatenated in transcript order.
`output_text` is non-null only for a completed turn with assistant text:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  http://localhost:8080/v1/invocations/invk_…/result
```

Cancel accepted work idempotently with an empty body:

```bash
curl --fail-with-body -X POST \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  http://localhost:8080/v1/invocations/invk_…/cancel
```

A `200` response means `cancelled` is durable, not that a racing provider call
incurred no cost. Cross-process notification lowers stop latency; renewal and
settlement fencing remain correct if it is lost. Collection reads are bounded,
newest-first, and use the
returned opaque cursor with the exact same filters:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  'http://localhost:8080/v1/invocations?session_id=sesn_…&limit=100'

curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  'http://localhost:8080/v1/sessions?tenant_key=customer-482'
```

Follow one Invocation directly by adding `Accept: text/event-stream` to its
admission, reconnect to an admitted Invocation by ID, or follow every turn in a
Session:

```bash
curl --no-buffer -X POST \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'Accept: text/event-stream' \
  --data-binary @invocation.json \
  http://localhost:8080/v1/invocations

curl --no-buffer \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  -H 'Accept: text/event-stream' \
  http://localhost:8080/v1/invocations/invk_…/stream

curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  'http://localhost:8080/v1/sessions/sesn_…/messages?limit=100'

curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  'http://localhost:8080/v1/sessions/sesn_…/transcript?limit=100'

curl --no-buffer \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  -H 'Accept: text/event-stream' \
  'http://localhost:8080/v1/sessions/sesn_…/transcript/stream'
```

For `/transcript`, continue with `next_page_token` until `has_more` is false,
then retain `resume_cursor` for the next incremental drain. A page token fixes
the original upper cut, so new writes cannot keep an old traversal open.
Message pages always precede lifecycle-change pages; applying the arrays in
response order cannot expose terminal completion before its committed assistant
messages. Cursors and page tokens are scoped to their Account, Session, and
filters and grant no authority of their own.

The minimal Invocation-stream consumer prints `output_text.delta` and finishes
on `invocation.result`. `invocation.accepted` is the normal acknowledgement as
the first admission frame; `invocation.update` carries incremental durable
state. Session streams use the same delta names and `transcript.update` for
durable batches. Every payload carries `type` plus its scope IDs.

Durable update and result frames carry an SSE ID; ephemeral
`output_text.delta` and `thinking.delta` previews never do. Streams accept a
durable cursor as `?cursor=...`, or as `Last-Event-ID` when the query parameter
is absent. On `stream.resync`, discard buffered previews and wait for durable
state. `stream.end` reason `rotate` means reconnect with the last durable ID;
reason `terminal` follows final Postgres reconciliation. Closing or losing a
stream never cancels the Invocation. Use a streaming HTTP client that can set
the bearer header; the browser's bare `EventSource` constructor cannot.

Admission bodies are limited to 1 MiB, 64 JSON nesting levels, and 64 text
blocks. Unknown fields, callback tools, malformed IDs, duplicate JSON member
names, and trailing JSON values are rejected before admission. Client-result
bodies are also limited to 1 MiB; each JSON result is limited to 256 KiB and 32
nesting levels.
