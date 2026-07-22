# Operational signals and diagnostics

nvoken emits the same bounded JSON event vocabulary in the single-daemon and
Google Cloud profiles. Use `event` as the primary selector, then correlate with
`request_id`, `invocation_id`, `dispatch_id`, `delivery_id`, `tool_call_id`, or
`session_id`. IDs are intentionally high-cardinality log fields; suitable
metric dimensions are the bounded fields `outcome`, `outcome_class`, `status`,
`process_role`, `execution_mode`, `component`, `operation`, `reason_code`, and
`handler_outcome`.

Logs never intentionally contain credentials, signing material, prompts,
transcript content, tool inputs or results, callback or provider response
bodies, or remote URLs. Error evidence uses bounded `error_class`,
`outcome_class`, or `reason_code` values. Treat any new free-form field as
sensitive until its shape and bounds have a test.

## Startup and liveness

Every successful daemon logs `process_started` with `build_version`, the
embedded `schema_version`, `process_role`, `execution_mode`, provider and
callback capability booleans, Cloud Tasks enablement, and `live_event_mode`.
Local builds report `devel`. The release container injects its immutable image
tag at build time.

`process_start_failed` identifies the failed `check`, such as
`configuration`, `database_connectivity`, or `database_schema`, without
printing configuration values. `process_failed` means the selected command or
a running component returned unsuccessfully.

`GET /health` is unauthenticated process liveness only. It does not query
Postgres, Redis, Cloud Tasks, callback hosts, or model providers. Keep liveness
probes on this endpoint so a dependency incident does not cause a restart loop.

## One-shot diagnosis

Run the same configuration as `serve`, but select the read-only diagnostic:

```bash
DATABASE_URL='postgres://…' nvokend diagnose
```

For local source runs, use `go run ./cmd/nvokend diagnose`. The total check is
bounded by `DIAGNOSTIC_TIMEOUT`, which defaults to `15s`. The command exits zero
only when every configured component passes. It never migrates or writes the
database, creates Cloud Tasks, publishes Redis events, calls a model, or sends
a callback.

Every result uses `event=diagnostic_check`:

| `component` | Pass condition | Failure class and first check |
| --- | --- | --- |
| `configuration` | The exact serve configuration is valid for the selected role and execution mode. | `invalid_configuration`: compare required environment variables with the deployment guide; do not print their values. |
| `database_connectivity` | A bounded Postgres connection and ping succeed. | `timeout`, `transport`, or `internal`: check network reachability, credentials, and Postgres availability. |
| `database_schema` | One clean migration row exactly matches the binary's embedded schema version. | `empty`, `dirty`, `behind`, `ahead`, or `invalid`: stop serving changes and follow the migration guide. Until PRD 019 defines a compatibility window, every ahead schema is unsafe. |
| `live_event_redis` | The configured Redis endpoint answers `PING`. | `timeout`, `transport`, or `internal`: check Memorystore/Redis reachability and TLS configuration. Durable reads remain authoritative. |
| `cloud_tasks_queue` | The configured queue is readable through Cloud Tasks without creating a task. | `timeout`, `transport`, or `internal`: check queue existence, API availability, and control-service IAM. Do not delete uncertain tasks. |

`outcome=skipped` with `error_class=dependency_failed` means an earlier check,
such as database connectivity, made the dependent verdict impossible.

## Event catalog

| Event | Meaning and useful correlation | First operator check |
| --- | --- | --- |
| `http_request_completed` | One public request completed; use `request_id`, bounded route, method, status, outcome, and latency. | For `server_error`, find `http_request_failed` with the same request ID, then check Postgres and component events. |
| `http_request_failed` | A handler returned an internal failure without logging its unsafe error text. | Check `error_class`, database diagnosis, then nearby Invocation events. |
| `invocation_claimed` / `invocation_claim_failed` | An engine acquired work, or its queue/store claim failed. | Correlate `invocation_id` and `lease_attempt`; for failure, check Postgres connectivity. |
| `invocation_recovered` / `invocation_recovery_loaded` | An expired owner was reclaimed and its committed checkpoint prefix was loaded. | Confirm the lease attempt increased and later reaches `invocation_settled`; repeated recovery points to engine or provider instability. |
| `invocation_execution_failed` / `invocation_lease_lost` | Execution could not produce a valid result, requested durable retry, or lost its fence. | Check bounded `error_class` values such as `invalid_spec`, `invalid_spec_scope`, `invalid_output_contract`, `invalid_transcript`, `recovery_invalid`, `invalid_generation_input`, `invalid_response`, `retryable`, or the generic process classes; then check the lease attempt and whether a replacement owner recovers the Invocation. Never force a stale writer. |
| `invocation_maintenance_failed` | Lease renewal, reaping, or settlement maintenance failed. | Use `operation`; check Postgres latency/connectivity and the lease expiry window. |
| `invocation_settled` | A fenced owner or reaper wrote the terminal/waiting state. | Read the authoritative Invocation and transcript; use status and bounded terminal failure fields. |
| `provider_generation` | A provider call or provider configuration check succeeded, failed, or was canceled. | Use `outcome_class`: `configuration`, `throttled`, `upstream_rejected`, `upstream_unavailable`, `timeout_or_transport`, `invalid_response`, `unknown`, `canceled`, or `success`; then check provider status and selected credential source without exposing the key. |
| `callback_delivery_claimed` / `callback_delivery_retry` / `callback_delivery_settled` / `callback_delivery_abandoned` / `callback_delivery_stale` | Durable callback delivery advanced, retried, settled, was abandoned, or lost its fence. | Correlate `delivery_id` and `tool_call_id`; inspect bounded `reason_code`, attempt, and delivery status. Preserve receiver idempotency by ToolCall ID. |
| `callback_claim_failed` / `callback_process_failed` / `callback_recovery_failed` / `callback_prune_failed` | A callback worker or maintenance operation failed. | Check `error_class`, Postgres, then the corresponding delivery lifecycle events. |
| `callback_lease_recovered` / `callback_pruned` | Expired delivery leases recovered or terminal transport diagnostics were pruned. | Recovery should be followed by a new claim; pruning affects no authoritative transcript content. |
| `dispatch_publish_failure` / `dispatch_claim_failed` | The outbox could not claim or publish delivery work. | Check `dispatch_id`, attempt count, Cloud Tasks diagnosis, IAM, and queue state; leave the Postgres row intact. |
| `dispatch_aged_pending` / `dispatch_stale_published` | Runnable delivery work exceeded its configured age. | Check the oldest dispatch, queue depth, executor health, and reconciliation; do not delete the task or row. |
| `dispatch_repair_failed` / `dispatch_reconcile_failed` / `dispatch_prune_failed` | Dispatch maintenance failed. | Use `operation` and check Postgres plus Cloud Tasks read access. |
| `dispatch_reconciled` / `dispatch_pruned` | Missing delivery converged or terminal transport diagnostics were pruned. | Verify counts and authoritative Invocation state; no manual action is normally required. |
| `dispatch_attempt_retry` / `dispatch_attempt_decided` / `dispatch_attempt_settled` | A private executor delivery needs transport retry, converged to a durable decision, or settled its Invocation. | Correlate `dispatch_id` and `invocation_id`; check `retry_reason`, executor IAM, and the Invocation fence. |
| `live_event_publish_failed` / `live_event_subscribe_failed` / `live_event_decode_failed` | Ephemeral Redis fan-out degraded. | Check `error_class`, Redis diagnosis, and reconnect clients from a durable cursor; do not infer lost durable output. |
| `live_event_stream_resync` / `live_event_stream_closed` | An SSE client must resync or the bounded stream ended. | Use `reason`, fetch the transcript snapshot with the resume cursor, then reconnect. |
| `client_tool_result_partial` / `client_tool_resume_queued` / `client_tool_result_deduplicated` | Host-submitted client tool results were accepted, resumed work, or converged as duplicates. | Correlate `invocation_id`; read pending ToolCalls and authoritative Invocation status. |

Google Cloud log queries, alert policies, and alert-specific actions remain in
the [Google Cloud runbooks](../../deploy/google-cloud/runbooks.md). The event
meaning and recovery invariants above are portable and govern both profiles.
