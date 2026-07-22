# `google_cloud` backup and restore drill — 2026-07-22

- Event: `restore_verification`
- Profile: `google_cloud`
- Source revision: `c69da17ba9a02ffd60f3eb4d7f25f66c82f14a21`
- Source schema: `000014`
- Recovery point: Cloud SQL on-demand backup `1784688153543`, completed `2026-07-22T02:44:04.904Z`
- Restored revision: `c69da17ba9a02ffd60f3eb4d7f25f66c82f14a21`
- Restored schema: `000014`
- Started: `2026-07-22T02:32:43.721Z`
- Ended: `2026-07-22T02:57:52Z`
- Verification: `success`
- Verification components: `database_schema`, `read_only_transaction`, `required_tables`, `required_constraints`, `nonterminal_unique_index`, `one_nonterminal_invocation_per_session`, `terminal_state_consistency`, `transcript_cursor_bounds`, `checkpoint_cursor_bounds`, `representative_session`, `representative_invocation`, `representative_transcript`, `representative_tool_call`, and `representative_checkpoint` all succeeded.
- Durable readback: source and target matched on counts and stable-ID digests for 3 Sessions, 3 Invocations, 5 messages, 7 lifecycle states, 1 ToolCall, and 1 checkpoint.
- Source isolation: project `nvoken` had no Cloud Run services or jobs before or after the drill. The source and target were separate Postgres 17 Enterprise instances in `us-central1`; no traffic or Terraform configuration changed, and the source remained `RUNNABLE` with matching durable metadata and schema compatibility declaration after target verification.
- Cleanup owner: `curtis@cmds.dev`
- Cleanup result: backup `1784688153543`, target `nvoken-r20-restore-20260722-0233`, and disposable source `nvoken-r20-src-20260722-0233` were deleted; both Auth Proxies stopped, the temporary password file was removed, and project `nvoken` listed no Cloud SQL instances at `2026-07-22T02:57:52Z`.
- Notes: the successful `RESTORE_VOLUME` operation ran from `2026-07-22T02:44:18.241Z` to `2026-07-22T02:54:34.839Z`. The CLI's initial wait window expired while the operation was still running; following the same operation ID to completion returned `DONE` without a second restore request.
