# Runtime schema migrations

Migrations are ordered, embedded, and forward-only. golang-migrate records the
current version and dirty state in `nvoken_schema_migrations`; nvoken fails on a
dirty state or a version the binary does not know. An applied file must never be
edited. Correct a released migration with a new `.up.sql` migration.

sqlc parses this directory in lexical order as the schema source, so migration
filenames stay zero-padded and their numeric and lexical ordering must agree.

Runtime business history is preserved by default. Foreign keys use
`ON DELETE RESTRICT`, and Session messages and Invocation states are
append-only. `execution_dispatches` are transport diagnostics rather than
business outcomes, so terminal rows alone have an explicit bounded retention
operation; their authoritative synthetic work rows are not pruned with them.
Terminal `callback_deliveries` have the same bounded diagnostic posture; their
owning ToolCalls, attempts, results, checkpoints, Invocations, and transcript
messages remain authoritative. Any broader retention design requires an
explicit, ordered migration and operation rather than cascades. The operator
policy, settings, and storage queries live in
[`docs/guides/data-retention.md`](../../../../docs/guides/data-retention.md).

Migration `000007` extends the outbox to scoped Invocation work. The generic
`work_id` remains intentionally free of a foreign key because the table carries
multiple kinds; kind-specific checks and service transactions enforce shape.

Migration `000008` adds the durable ToolCall/checkpoint spine. Tool request and
result content remains canonical only in append-only `session_messages`; the
new rows retain immutable identity, transcript references, attempts, normalized
usage receipts, and replay watermarks. These records are business evidence and
have no independent pruning path.

Migration `000009` adds immutable output-schema identity plus the terminal
structured-output value/provenance projection. The projection may be written
only with successful settlement and remains bound to the accepted transcript
ToolCall by service equality checks and database shape constraints.

Migration `000010` records whether a terminal ToolCall result came from its
trusted builtin, the host client, or nvoken's own terminal-settlement path.
That immutable origin lets retries distinguish an accepted host result from a
synthetic cancellation/deadline result without comparing payloads.

Migration `000011` adds one durable callback delivery per callback ToolCall,
including blocked-before-park activation, delivery leases and attempts,
terminal retention, and callback result provenance. Request and result content
remain canonical only in `session_messages`.

Migration `000012` replaces configuration-only Runtime authentication with
durable machine and user credentials. It adds installation operator subjects
and memberships, bounded encrypted issuance delivery, RFC 8628 device grants,
bootstrap browser sessions, rotation lineage, and the one-time legacy Runtime
key import marker.
