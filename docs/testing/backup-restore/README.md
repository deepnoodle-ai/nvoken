# Backup and restore drill records

Each Markdown file in this directory records one bounded PRD 020 drill. Records
are versioned repository evidence, not a secret store, transcript export, or
replacement for platform backup metadata.

Use `YYYY-MM-DD-PROFILE.md` and include every field below:

```markdown
# PROFILE backup and restore drill — YYYY-MM-DD

- Event: `restore_verification`
- Profile: `single_daemon` or `google_cloud`
- Source revision: immutable Git commit or image digest
- Source schema: six-digit nvoken migration version
- Recovery point: archive checksum plus completion time, Cloud SQL backup ID
  plus completion time, or PITR RFC 3339 timestamp
- Restored revision: immutable verifier Git commit or image digest
- Restored schema: six-digit nvoken migration version
- Started: RFC 3339 timestamp
- Ended: RFC 3339 timestamp
- Verification: `success` or `failed`
- Verification components: bounded `restore_verification` component/outcome
  list or a link to retained scrubbed logs
- Durable readback: counts and stable IDs checked, without content
- Source isolation: evidence that source traffic/configuration stayed unchanged
- Cleanup owner: accountable person or automation identity
- Cleanup result: exact target removed and completion timestamp, or outstanding
  target plus deadline
- Notes: optional bounded operational context
```

Do not include database URLs, passwords, secret names whose disclosure is
sensitive, Terraform state, prompts, transcripts, ToolCall payloads, callback
bodies, provider responses, or unbounded log output. A failed or incompletely
cleaned drill remains useful evidence but cannot mark the readiness row proven.

The operating procedure is
[Backup, restore, and recovery drills](../../guides/backup-and-restore.md).
