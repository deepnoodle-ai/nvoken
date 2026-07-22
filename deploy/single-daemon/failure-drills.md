# Single-daemon failure and recovery drills

Run these drills only in a disposable single-daemon installation with a
dedicated PostgreSQL database, bounded provider spend, and no production host
traffic. Record the exact Git revision or image digest, schema version, process
configuration, operator, start/end time, durable IDs, safe log references, and
cleanup outcome using the
[evidence template](../../docs/testing/readiness/evidence/single-daemon/README.md).

Before each drill, run `nvokend diagnose`, confirm the `combined`/`embedded`/
`in_process` startup identity, and make one normal smoke pass. Use the exact
supervisor unit or PID; never a broad process-name kill. Keep prompts,
transcripts, callback bodies, credentials, and database URLs out of evidence.

## Required boundaries

| Boundary | Controlled action | Required durable outcome |
| --- | --- | --- |
| Termination during execution | After `invocation_claimed`, force-stop only the disposable daemon, then restart after the stored lease can expire. | The same Invocation remains authoritative, requeues from its committed checkpoint under a newer fence, and settles at most once. Work completed outside Postgres in the uncertainty window may repeat. |
| Queued work across restart | With `ENGINE_CONCURRENCY` lower than a bounded batch, admit enough independent Sessions to leave queued work; gracefully stop and restart. | Every acknowledgement remains queryable. Queued work is later claimed or remains durably queued; none disappears. |
| Waiting client ToolCall | Admit a client-tool smoke until `waiting`, stop and restart before submitting its result, then submit the same result twice. | The call remains parked without an owner, the first result is accepted, the equal replay deduplicates, and the Invocation continues once. |
| Postgres unavailable | After one acknowledgement, temporarily stop or isolate the disposable Postgres service without changing data. | `/health` retains its liveness meaning, `diagnose` and dependent requests fail safely, no false settlement appears, and the same acknowledgement is readable after recovery. |
| Provider failure | Select a deliberately invalid model in one bounded disposable Invocation or use an operator-controlled provider failure account. | The Invocation settles durably as `failed` with `provider_error` and a bounded outcome class; no alternate provider or credential source is selected. |
| Callback failure | Use an idempotent receiver that records stable IDs and returns a retryable failure through the configured maximum. | Retries preserve delivery and ToolCall IDs; exhaustion becomes one durable model-visible result, with no duplicated accepted receiver effect. |
| Graceful shutdown | Send `SIGTERM` while bounded work is active and wait through `SHUTDOWN_TIMEOUT`. | The process joins or records an attributable timeout; no claim is orphaned permanently, and restart recovery is fenced. |

## Procedure

1. Create a new evidence record from the template and write the exact initial
   environment descriptions and revision before mutation.
2. Admit only the work needed for one boundary. Save the returned Invocation and
   Session IDs; never copy their content.
3. Observe the precondition through the public read and the stable event catalog
   before inducing the failure. Timing alone is not proof that work was running
   or waiting.
4. Apply one bounded mutation. Record its start/end time and the exact service,
   PID, dependency, or receiver behavior changed.
5. Restore the dependency or process. Run `diagnose`, then use authoritative
   reads and correlation events to record the outcome.
6. Check for one terminal state, stable ToolCall/delivery identity, and absence
   of stale-owner settlement. For a queued result, record queue age and the
   reason the bounded observation ended.
7. Restore the original configuration and rerun the normal smoke. Record
   cleanup responsibility for the disposable database, state file, callback
   receiver data, and logs.

The waiting-work drill may use the `client-tool-admit` and
`client-tool-resume` actions in `smoke.py`, with a dedicated `--state-file`.
The termination drill should use a provider/model or controlled test condition
long enough to observe `invocation_claimed`; do not infer the boundary from a
sleep.

## Passing evidence

A drill passes only when its authoritative final state and correlation events
are recorded. A process exit code, provider error page, callback request count,
or supervisor restart by itself is supporting evidence, not the outcome.
Skipped or ambiguous boundaries stay visible and keep the readiness row
pending.

After review, commit the secret-free record and link it from the matching
`single_daemon` process/dependency-failure row in the readiness matrix. Do not
maintain a second readiness status here.
