# `single_daemon` backup and restore drill — 2026-07-22

- Event: `restore_verification`
- Profile: `single_daemon`
- Source revision: `c69da17ba9a02ffd60f3eb4d7f25f66c82f14a21`
- Source schema: `000014`
- Recovery point: `sha256:8c1a06b9e899a998a39c3d25a290bf1eebc8d692a512bdaf91fca69c62599180`, completed `2026-07-22T02:32:12.320051Z`
- Restored revision: `c69da17ba9a02ffd60f3eb4d7f25f66c82f14a21`
- Restored schema: `000014`
- Started: `2026-07-22T02:32:12.072772Z`
- Ended: `2026-07-22T02:32:12.476527Z`
- Verification: `success`
- Verification components: `database_schema`, `read_only_transaction`, `required_tables`, `required_constraints`, `nonterminal_unique_index`, `one_nonterminal_invocation_per_session`, `terminal_state_consistency`, `transcript_cursor_bounds`, `checkpoint_cursor_bounds`, `representative_session`, `representative_invocation`, `representative_transcript`, `representative_tool_call`, and `representative_checkpoint` all succeeded.
- Durable readback: 3 Sessions, 3 Invocations, 5 messages, 7 lifecycle states, 1 ToolCall, and 1 checkpoint matched the source; the completed, queued, and waiting Invocation IDs were read successfully.
- Source isolation: `scripts/test_restore.py` used different source and target database names inside disposable Postgres 17. No daemon was started against the full restore; the separate terminal-only fixture passed authenticated daemon startup/readback without model or callback work.
- Cleanup owner: `scripts/test_restore.py`
- Cleanup result: both drill databases and the disposable container were removed by the passing test; no matching container remained after the run.
- Notes: `make test-restore` passed against the recorded revision. Corrupt, dirty, incomplete, and incompatible fixtures also failed with their expected bounded diagnoses.
