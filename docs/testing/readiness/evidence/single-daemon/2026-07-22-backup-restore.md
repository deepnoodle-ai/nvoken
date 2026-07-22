# `single_daemon` backup and restore

## Record

| Field | Value |
| --- | --- |
| Profile | `single_daemon` |
| Tested revision | `7a39ec91f411a9f48dc711008b311584a9bcfda8` |
| Dimensions | `Backup/restore` |
| Result | `pass` |

- Operator: `scripts/test_restore.py`
- Started: `2026-07-22T02:32:12.072772Z`
- Finished: `2026-07-22T02:32:12.476527Z`
- Immutable image: N/A; verifier built from the tested Git revision
- Binary schema expectation: `000014`
- PostgreSQL: disposable `postgres:17` container with separate source and target databases
- Host: macOS development host; resource shape not used for a capacity claim
- Configuration overrides: test-only database URL and explicit logical-restore drill gate

## Scenarios

| Scenario | Durable IDs or safe correlation reference | Observed outcome | Result |
| --- | --- | --- | --- |
| Logical backup and isolated restore | `sha256:8c1a06b9e899a998a39c3d25a290bf1eebc8d692a512bdaf91fca69c62599180` | The target passed every schema, catalog, invariant, and representative-read check. | pass |
| Durable comparison | Bounded counts and three stable Invocation IDs | Source and target matched at schema `000014`: 3 Sessions, 3 Invocations, 5 messages, 7 lifecycle states, 1 ToolCall, and 1 checkpoint. | pass |
| Terminal-only startup/readback | Separate terminal fixture in the full Python restore suite | Authenticated daemon readback passed without model or callback work. | pass |

The full PRD 020 record is
[2026-07-22 single-daemon backup and restore](../../../backup-restore/2026-07-22-single_daemon.md).

## Measurements

The selected dump completed at `2026-07-22T02:32:12.320051Z`; the isolated
restore and verification completed at `2026-07-22T02:32:12.476527Z`.

## Caveats

This proves logical backup/restore mechanics and durable readback. Backup
scheduling, storage, retention, and recovery objectives remain operator-owned.
The drill originally ran at `c69da17ba9a02ffd60f3eb4d7f25f66c82f14a21`;
the recorded tested revision is its content-equivalent commit after rebasing on
`main`, with no verifier or migration changes in that history rewrite.

## Cleanup

Both drill databases and the disposable container were removed by the passing
Python runner. No matching container remained after the run.
