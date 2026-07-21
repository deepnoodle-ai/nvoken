# Runtime admission

The self-contained Runtime durably admits, executes, and reads tool-free
Invocations. Admission still returns before model generation begins.

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
`ENGINE_DRAIN_GRACE`. `SHUTDOWN_TIMEOUT` bounds HTTP shutdown and the overall
component join; engine drain grace must be shorter than that total. Defaults are
suitable for local self-contained operation. The Cloud Run paved path overrides
both shutdown values to fit the platform termination window.

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
      "model": {"provider": "anthropic", "name": "claude-sonnet-5"}
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

Read the durable state after any API restart:

```bash
curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  http://localhost:8080/v1/invocations/invk_…

curl --fail-with-body \
  -H "Authorization: Bearer $RUNTIME_API_KEY" \
  http://localhost:8080/v1/sessions/sesn_…
```

The request body is limited to 1 MiB and 64 text blocks. Unknown fields,
unsupported features such as tools, malformed IDs, duplicate JSON member names,
and trailing JSON values are rejected before admission.
