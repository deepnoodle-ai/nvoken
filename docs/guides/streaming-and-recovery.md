# Streaming and recovery

nvoken streams useful previews without making the network connection the source
of truth. Use `Agent.stream` for the ordinary create-and-follow workflow, an
Invocation handle to reconnect to known work, and the Session stream when one
consumer needs every turn in a conversation.

## Guarantees the pattern relies on

1. **Durable frames are the cursor carriers.** Resume from the most recent
   durable event ID. A text or thinking delta is provisional and does not move
   the durable transcript cursor.
2. **Delta concatenation is scoped.** Concatenating deltas for one
   `(invocation_id, attempt, iteration, content_index)` produces a prefix of
   that attempt's canonical content. Never concatenate across a changed
   attempt, iteration, or content index.
3. **`stream.resync` invalidates every affected preview.** Discard buffered
   text and thinking for its Invocation, or every preview when the event is
   Session-wide. Wait for a durable `invocation.update`,
   `transcript.update`, or `invocation.result`.
4. **Authoritative reads settle the answer.** A terminal
   `invocation.result`, `GET /v1/invocations/{id}/result`, or the canonical
   Session transcript determines the committed output. Disconnecting,
   timing out, or cancelling a local consumer does not cancel the Invocation.

These guarantees let a UI render low-latency text while keeping retry,
reconnect, and crash recovery safe. The shared SDK reducer implements the same
preview lifecycle.

## Minimal Agent consumer

```ts
for await (const event of agent.stream("Write a short status update.", {
  timeoutMs: 30_000,
})) {
  if (event.type === "output_text.delta") {
    process.stdout.write(event.text);
  }
  if (event.type === "invocation.result") {
    console.log(`\nsettled=${event.result.invocation.status}`);
  }
}
```

The TypeScript, Python, and Go Agent facades dispatch configured host tools
when the stream reports parked work. After a broken or incomplete stream they
reconcile through authoritative reads. Rust deliberately exposes the
transport-plus-handle level: wait until actionable, submit ToolCall results,
then wait for the result.

## Recovery sequence

1. Keep the Invocation ID returned by admission.
2. Reconnect with its lazy handle. SDK streams retain and send the latest
   durable cursor automatically during an uninterrupted consumer call.
3. If the consumer process itself restarted, read the Invocation and composed
   result first. Reopen the stream only while work is nonterminal.
4. On `stream.resync`, clear previews before rendering new deltas.
5. Treat a successful composed-result read as the final answer even when the
   last streaming connection ended unexpectedly.

Reuse the same idempotency key and unchanged request only when the admission
acknowledgement was uncertain. A changed body with the same key returns
`idempotency_conflict`.

## Troubleshooting

| You see | It means | What to do |
| --- | --- | --- |
| Repeated text after reconnect | The application retained previews across a resync or attempt change. | Key previews by Invocation, attempt, iteration, and content index; clear the affected keys on `stream.resync`. |
| The stream ended but the Invocation is still running | The local transport ended or rotated; durable work continues. | Reconnect the handle or poll an authoritative read. Do not submit a duplicate admission. |
| Local timeout or task cancellation | Only the caller stopped waiting. | Reattach later, or call the explicit durable `cancel` operation if work must stop. |
| Invocation is `waiting` | A host ToolCall owns the Session until submitted, cancelled, or timed out. | Dispatch the named handler and submit under its stable ToolCall ID, or cancel deliberately. |
| `MissingToolHandlerError` | The Agent has no local handler for parked work. | By default the Agent cancels before raising. Use the documented leave-waiting opt-out only when another worker owns the call. |
| `session_invocation_active` / `SessionBusyError` | Another nonterminal Invocation already owns the Session. | Wait for or recover that Invocation. Bound Sessions serialize only within one local binding. |
| Final text differs from a preview | Preview output came from an abandoned attempt or incomplete prefix. | Replace it with `output_text` from the composed result or canonical transcript. |

Run `make sdk-check` to exercise stream rotation, reconnect, durable cursor
retention, preview replacement, resync, and authoritative-result fallback at
each SDK's documented level.
