# Runtime admission

The self-contained Runtime durably admits, executes, and reads public
tool-free Invocations. Admission still returns before model generation begins.

Apply migrations explicitly, then start the service with a Postgres URL and a
random bearer secret of at least 32 bytes. Supply the installation key for each
provider your admitted specs may select:

```bash
DATABASE_URL='postgres://…' go run ./cmd/nvokend migrate

DATABASE_URL='postgres://…' \
RUNTIME_API_KEY='replace-with-a-random-32-byte-or-longer-secret' \
ANTHROPIC_API_KEY='replace-with-an-Anthropic-key' \
OPENAI_API_KEY='' \
go run ./cmd/nvokend serve
```

Provider keys are optional at startup, but an Invocation selecting a provider
without its matching key fails durably with `provider_error`; nvoken never falls
back to another provider or ambient credentials. Engine capacity and timing are
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

`INVOCATION_DEFAULT_WALL_CLOCK_TIMEOUT`,
`INVOCATION_DEFAULT_ACTIVE_EXECUTION_TIMEOUT`, and
`INVOCATION_DEFAULT_MAX_ITERATIONS` supply omitted limits. The corresponding
`INVOCATION_MAX_*` settings reject requests above installation policy; output
tokens and estimated cost have no default and remain unlimited unless the host
requests them. `DATABASE_MAX_CONNS` must be at least two because the
cross-process cancellation listener reserves one pool connection.

A request may add `spec.output.schema` for one validated object result. nvoken
admits a bounded JSON Schema subset, presents it as the reserved
`nvoken_submit_output` builtin, and persists its request/result through the
normal durable ToolCall path. An omitted iteration budget resolves to three (or
the lower installation maximum); an explicit value below two is rejected. The
model must submit a valid object and then finish with a normal assistant
response. Prose or fenced JSON never substitutes for the tool submission.
Patterns are limited to 1,024 UTF-8 bytes. The accepted ToolCall is internal
checkpoint evidence; public `output` remains null until fenced terminal
settlement commits the final object and provenance together.

On its first start, the static self-hosted authenticator serializes creation of
one installation Account and its default tenant partition. Later starts resolve
that same Account and fail closed if the database contains more than one.
`RUNTIME_TENANT_REF` optionally confines the installation credential to one
tenant partition. The bearer secret remains installation configuration and is
never stored or logged.

Submit one turn:

```bash
curl --fail-with-body http://localhost:8080/v1/invocations \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  -H 'Content-Type: application/json' \
  --data '{
    "agent_ref": "support-triage",
    "session_key": "ticket-483",
    "idempotency_key": "ticket-483:first-reply",
    "input": {"content": [{"type": "text", "text": "Why was I charged twice?"}]},
    "spec": {
      "instructions": "You are a concise billing support agent.",
      "model": {"provider": "anthropic", "name": "claude-sonnet-5"},
      "budgets": {
        "wall_clock_timeout_seconds": 600,
        "active_execution_timeout_seconds": 300,
        "max_output_tokens": 4096,
        "max_estimated_cost_usd": 0.25,
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

The database now retains internal checkpoint evidence at each accepted model
iteration: the canonical assistant message, normalized usage/provenance
receipt, any prepared ToolCalls, and a transcript watermark commit together.
Accepted builtin outcomes likewise commit one canonical tool-role result and a
fenced checkpoint. ToolCall rows contain lifecycle and message references, not
a second copy of content. These records are intentionally not exposed as a
public tool surface yet.

Checkpoint evidence does not currently turn process loss into continuation.
An expired engine claim still becomes the durable `execution_lost` failure;
cancellation, deadlines, and reaping close any prepared calls with a synthetic
tool-result message so transcript replay remains structurally complete. Do not
retry a terminal Invocation in place. Create a new Invocation when the host
wants another attempt.

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
model `provenance`, resolved `budgets`, accrued `active_execution_ms`, and its
wall-clock deadline. It also always includes nullable `output` and
`output_provenance`. A successful schema-bearing Invocation returns the
validated object and its reserved ToolCall ID/schema digest; failed, cancelled,
or schema-free Invocations return null for both. If no valid submission was
accepted, the failure code is `structured_output_unsatisfied` with a bounded
missing, invalid, or oversized reason. Cancel accepted work idempotently with
an empty body:

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
  'http://localhost:8080/v1/sessions?tenant_ref=customer-482'
```

Read the sole durable transcript directly, use the incremental JSON snapshot,
or tail that same snapshot model with ephemeral live deltas:

```bash
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

The stream accepts that `resume_cursor` as `?cursor=...`, or as
`Last-Event-ID` when the query parameter is absent. Only
`transcript.snapshot` frames carry an SSE ID. `generation.delta` frames are
live-only text or thinking previews, and `stream.resync` means discard the
preview and wait for canonical state. `stream.end` reason `rotate` requests a
normal reconnect with the last ID; reason `terminal` is emitted only after the
final Postgres reconciliation. Closing or losing the stream never cancels the
Invocation. Use a streaming HTTP client that can set the bearer header; the
browser's bare `EventSource` constructor cannot.

The request body is limited to 1 MiB, 64 JSON nesting levels, and 64 text blocks. Unknown fields,
unsupported features such as tools, malformed IDs, duplicate JSON member names,
and trailing JSON values are rejected before admission.
