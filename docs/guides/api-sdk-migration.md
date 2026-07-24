# Pre-1.0 API and SDK migration

This guide covers the coordinated contract stabilization that follows v0.2.0.
It is intentionally breaking: nvoken is pre-1.0, and these names remove
ambiguity before the surface expands. Regenerate raw clients from
`openapi/runtime.yaml` and update handwritten-facade call sites together.

## Invocation and Session reads

An Invocation is one durable agent turn. Agent `text(input)` runs a turn; the
read-only handle accessor now matches the wire field:

| Language | Before | Now |
| --- | --- | --- |
| TypeScript | `handle.text()` | `handle.outputText()` |
| Python | `handle.text()` | `handle.output_text()` |
| Go | `handle.Text()` | `handle.OutputText()` |
| Rust | `handle.text()` | `handle.output_text()` |

Session-scoped message reads now say Session explicitly:

| Language | Before | Now |
| --- | --- | --- |
| TypeScript | `client.listMessages(id)` | `client.listSessionMessages(id)` |
| Python | `client.list_messages(id)` | `client.list_session_messages(id)` |
| Go | `client.ListMessages(id, options)` | `client.ListSessionMessages(id, options)` |
| Rust | `client.list_messages(id, options)` | `client.list_session_messages(id, options)` |

Invocation-handle `listMessages` / `list_messages` / `ListMessages` keeps its
name because its scope is already established by the handle. TypeScript no
longer exposes the internal `runImmediately` or `replaySafe` helpers; use
`Agent.run()` and the documented Client methods.

## Behavior changes

- SDK error category `authentication` now means HTTP `401` only. HTTP `403`
  maps to `permission`.
- A caller cancellation is never reported as `timeout`. SDKs that return a
  normalized local error use `cancelled`; Python and native task cancellation
  continue to propagate their language-native cancellation.
- `output_text` concatenates text blocks within one assistant message directly
  and joins distinct assistant messages with exactly `"\n\n"`.
- `GET /v1/provider-credentials` now accepts `cursor` and requires
  `{items, has_more, next_cursor}`. Preserve the filters and limit while
  following `next_cursor`.
- `ModelProvider` is one extensible validated string across requests and
  responses. Older clients can decode a future valid provider; the Runtime
  still rejects providers it cannot execute.

## Durable compatibility

No request field in this revision changes admission fingerprint material.
New admissions therefore remain v7, and retries of rows admitted under v1–v7
continue to use the algorithm recorded on each row. The next material request
shape uses v8 and adds a new fixture; do not rewrite retained fingerprints.
