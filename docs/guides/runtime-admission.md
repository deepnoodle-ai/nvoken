# Runtime admission

The first working Runtime slice durably admits and reads Invocations. It does
not execute them yet: queued work remains inert until the claim engine ships.

Apply migrations explicitly, then start the service with a Postgres URL and a
random bearer secret of at least 32 bytes:

```bash
DATABASE_URL='postgres://…' go run ./cmd/nvokend migrate

DATABASE_URL='postgres://…' \
RUNTIME_API_KEY='replace-with-a-random-32-byte-or-longer-secret' \
go run ./cmd/nvokend serve
```

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
IDs. If the acknowledgement is lost, retry the exact request and
`idempotency_key`; nvoken returns the original IDs with `deduplicated: true`.
A changed request using the same scoped key returns
`409 idempotency_conflict`.

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
