# `google_cloud` backup and restore

## Record

| Field | Value |
| --- | --- |
| Profile | `google_cloud` |
| Tested revision | `c69da17ba9a02ffd60f3eb4d7f25f66c82f14a21` |
| Dimensions | `Backup/restore` |
| Result | `pass` |

- Operator: `curtis@cmds.dev`
- Started: `2026-07-22T02:32:43.721Z`
- Finished: `2026-07-22T02:57:52Z`
- Immutable image: N/A; verifier built from the tested Git revision
- Binary schema expectation: `000014`
- PostgreSQL: Cloud SQL for PostgreSQL 17, two isolated Enterprise `db-f1-micro` instances in `us-central1`
- Configuration overrides: disposable public-IP instances reached only through loopback Cloud SQL Auth Proxies; no Runtime or executor service used either instance

## Scenarios

| Scenario | Durable IDs or safe correlation reference | Observed outcome | Result |
| --- | --- | --- | --- |
| On-demand backup | Backup `1784688153543` | Backup completed successfully at `2026-07-22T02:44:04.904Z`. | pass |
| Isolated restore | Restore operation `2fdfa50b-ed67-4df9-92f6-135c00000032` | The existing operation completed `DONE`; the target passed every schema, catalog, invariant, and representative-read check. | pass |
| Durable comparison | Bounded count and stable-ID digests | Source and target matched at schema `000014`, including the `14`/`14` compatibility declaration, 3 Sessions, 3 Invocations, 5 messages, 7 lifecycle states, 1 ToolCall, and 1 checkpoint. | pass |

The full PRD 020 record is
[2026-07-22 Google Cloud backup and restore](../../../backup-restore/2026-07-22-google_cloud.md).

## Measurements

The selected backup ran from `2026-07-22T02:42:33.550Z` to
`2026-07-22T02:44:04.904Z`. The restore ran from
`2026-07-22T02:44:18.241Z` to `2026-07-22T02:54:34.839Z`.

## Caveats

This proves one isolated backup/restore and durable readback path. It does not
set backup scheduling, RPO/RTO, cross-region recovery, promotion, or failover
policy.

## Cleanup

Backup `1784688153543`, target `nvoken-r20-restore-20260722-0233`, and source
`nvoken-r20-src-20260722-0233` were deleted. Both Auth Proxies stopped, the
temporary password file was removed, and project `nvoken` listed no Cloud SQL
instances at `2026-07-22T02:57:52Z`.
