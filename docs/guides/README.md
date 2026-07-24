# Guides

Choose the task you are doing now. You do not need to read these guides in
order.

## Try or contribute

- [Run nvoken locally](run-locally.md) — first success with official releases
- [Develop nvoken](developing-nvoken.md) — change this repository from source
- [Choose a local workflow](local-development.md) — Run versus Develop versus
  production Deploy

## Integrate nvoken into an application

- [TypeScript SDK](../../sdk/typescript/README.md) — first response,
  multi-turn Sessions, host tools, structured output, streaming, and recovery
- [SDKs and client CLI](sdks-and-cli.md) — install a client and choose its
  supported workflow facade
- [Coming from provider APIs](from-provider-apis.md) — map provider messages,
  tool loops, conversations, and structured output onto durable workflows
- [Streaming and recovery](streaming-and-recovery.md) — preview guarantees,
  authoritative settlement, reconnect sequence, and troubleshooting
- [Pre-1.0 API and SDK migration](api-sdk-migration.md) — coordinated breaking
  names and behavior changes after v0.2.0
- [Runtime admission](runtime-admission.md) — wire-level durable Invocation,
  Session, streaming, cancellation, and ToolCall behavior
- [Credentials and CLI authentication](credentials-and-cli-auth.md) — machine
  credentials, device login, scope, and rotation
- [Callback receivers](callback-receivers.md) — safely receive at-least-once
  host tool calls
- [Remote MCP tools](remote-mcp-tools.md) — probe, declare, execute, and
  recover guarded streamable-HTTP tools

## Operate a deployment

Start with a production profile: [single daemon](../../deploy/single-daemon/README.md)
or [Google Cloud](../../deploy/google-cloud/README.md). Use these references for
specific operator tasks:

- [Database migrations](database-migrations.md)
- [Postgres operations](postgres-operations.md)
- [Compatible upgrades](compatible-upgrades.md)
- [Operational signals](operational-signals.md)
- [Backup, restore, and recovery drills](backup-and-restore.md)
- [Data retention and storage growth](data-retention.md)

Production references are intentionally more detailed than the Run guide. They
make database, secret, availability, upgrade, and recovery responsibilities
explicit instead of hiding choices that affect durable data.
