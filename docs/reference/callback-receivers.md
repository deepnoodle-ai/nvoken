# Receiving signed deliveries

**Status:** Descriptive. This is how the four SDKs in this repository receive
tool callbacks and Invocation webhooks today.
**Verified against:** the signing vectors in
[`docs/design/delivery-signing-v1.json`](../design/delivery-signing-v1.json),
the contract in [`openapi/nvoken.yaml`](../../openapi/nvoken.yaml), and the
receiver in each SDK.
**Date:** 2026-08-17
**Authority:** The runtime is the authority. Where this document and the
runtime disagree, the runtime is right and this document is stale.

## Who this is for

People standing up an endpoint nvoken posts to. There are two of them and they
are the same shape:

- a **tool callback**, which delivers one ToolCall and waits for its answer, and
- an **Invocation webhook**, which reports a transition that already happened
  and waits for nothing.

Each SDK ships a receiver — `createCallbackReceiver` in TypeScript,
`NewCallbackReceiver` in Go, `CallbackReceiver` in Python and Rust, and a
`WebhookReceiver` beside each — that owns everything below. Reach for the bare
`verifyCallback` / `verifyWebhook` only when you are building a receiver of your
own, and then read this whole page: most of what a receiver has to get right is
not the signature.

## One signing scheme, two purposes

nvoken signs both kinds identically: HMAC-SHA256 over
`v1.<delivery_id>.<timestamp>.<raw_body>`, sent as
`X-Nvoken-Signature: sha256=<hex>`. Verify over the **raw bytes**, before any
JSON parse — a body re-serialized from a decoded object is not the body that
was signed.

What differs is only what the verified body then means:

|  | tool callback | Invocation webhook |
| --- | --- | --- |
| Signing key purpose | `callback` | `webhook` |
| `Idempotency-Key` | the ToolCall being settled | the delivery id, also in the body |
| Your answer | settles, acknowledges, or fails the ToolCall | affects nothing |

The two purposes hold different secrets. A receiver serving both endpoints
holds two keys and must not try either against the other's deliveries.

## Selecting the key comes first

Every delivery names its signing identity in two headers:

```
X-Nvoken-Signing-Key-Id: key_019b0a12-…
X-Nvoken-Signing-Key-Version: 1
```

The key id names the App and the purpose and does not change. The **version**
selects the secret within it, and holding two versions is what makes a rotation
survivable: nvoken mints the next version while still signing with the current
one, and a signature you cannot verify fails its delivery outright rather than
retrying. There is no forgiveness to lean on.

Select on those headers before anything parses, logs, or dispatches on the
body. A delivery signed by an identity you do not hold should never reach your
JSON decoder.

Configuration is where this goes wrong, so the SDKs take the version as an
integer and validate the whole table when the receiver is **built**. A version
that cannot be read as a positive integer, a secret under 32 bytes, or the same
`(key id, version)` twice fails at startup — loudly, and once — rather than
refusing live deliveries, which is permanent.

## The reply discipline

Your status code is the whole conversation. It decides whether nvoken tries
again, and for a callback it also decides whether the ToolCall settles.

| situation | status | why |
| --- | --- | --- |
| no keys configured | **503** | an operator error this deployment may still fix inside the retry window |
| signing identity not held | **401** | a real identity failure; redelivery reproduces it |
| signature, timestamp, or envelope invalid | **401** | the same bytes fail the same way |
| no handler for the signed name or event | **400** callback / **200** webhook | see below |
| the tool answered | **200** | settles the ToolCall with your content |
| the tool failed | **200** with `is_error` | the model reads it and corrects itself |
| you took the call away | **202** | you will settle it later through tool results |
| *your* code failed | **503** | you failed, not the tool — ask for it again |

Two rows are worth dwelling on.

**A failed tool is not a failed receiver.** Settle it: `200` carrying
`is_error: true`. The model can read a tool error and try something else, while
a 5xx only has nvoken deliver the same doomed call four more times and then
fail it.

**Unconfigured is transient; unknown key is not.** Both look like "cannot
verify" from the inside, and only one of them is the sender's fault. A receiver
that answers 401 because its secret was unreadable has permanently failed a
ToolCall over its own startup race.

nvoken retries any 5xx, plus 408, 425, and 429. Every other non-2xx — 400, 401,
403, 404, 409, 410, 422 among them — is permanent.

## Deduplication is not optional

Delivery is at least once. The same ToolCall can arrive twice, and the second
arrival must not run your tool again — that repeats every effect it had.

The order that works:

1. **Find** a recorded reply for this ToolCall id. If there is one, return it.
   Your tool does not run.
2. Run the tool.
3. **Put if absent.** Two deliveries of one ToolCall can be in flight at once,
   so the write closes the race. Return whichever reply won, not necessarily
   yours.

Skip the store only when every tool on the endpoint is safe to run twice —
a read that touches nothing and writes nothing.

Webhooks deduplicate differently, and it costs you nothing extra. Each carries a
`sequence` counting transitions within one Invocation from 1. Keep the highest
sequence you have applied per Invocation and fold only what exceeds it; a
receiver that applies whichever arrived last rolls its own state backwards. A
repeat carries a sequence you already applied, so ignoring it *is* the dedup.

The SDK receivers deliberately do **not** own that fold. The compare and the
write have to happen in the same transaction as the state they guard, and that
is your transaction. A superseded delivery is still a delivery: record nothing
and answer 200.

An event you subscribed to but did not register a handler for also answers 200.
Retrying it would spend nvoken's bounded attempts reaching the same absent
handler and lose the transition anyway; answer it, and read the receiver's
outcome — `ignored` — in your logs.

## Dispatch on what was signed

The callback envelope carries `tool_name` inside the signed body, and the
webhook envelope carries `event`. Dispatch on those.

A per-tool or per-event suffix on the endpoint URL is unsigned. It is a fine
thing to have in an access log and never a thing to branch on.

## Authorization: what signing does and does not prove

A verified delivery proves it came from nvoken. It does not say what the work
belongs to, and **the tool input cannot say either — the model wrote it.**

The callback envelope answers this directly:

```json
{
  "nvoken": { "tool_name": "open_ticket", "tenant_key": "acme", "…": "…" },
  "authorization_context": { "board": "brd_9f21" },
  "input": { "board": "brd_9f21", "ticket": "A-42" }
}
```

`authorization_context` is what you asserted when the Session was created, in
your own terms — a board, a workspace, a document, whatever your authorization
boundary actually is. It is written at creation only, never interpreted by
nvoken, never model-visible, and carried to you unchanged.

**It sits beside `nvoken`, not inside it, and the placement is the rule.**
Everything under `nvoken` is a fact nvoken minted or resolved. This is a value
you asserted and nvoken carried. Signing guarantees its integrity, not its
truth.

So:

> **A value repeated in tool input may only agree with the authorization
> context, never establish it.**

The example above repeats `board` on purpose. Checking that the two agree is
reasonable — a disagreement is worth refusing. Reading the board out of `input`
when the context is absent is not, because then the model chose it.

This is also what removes a round trip. Without it a receiver has to read the
Invocation back on every delivery to recover which of your objects the work
belongs to. With it, the answer arrives signed.

`user_key` and `authorization_context` are both omitted from browser-audience
responses, for the same reason: they carry your own identifiers, and the browser
caller is the end user those identifiers are about.

## What is not the receiver's job

- **Rendering.** Every SDK returns a status and an optional body; you write it
  with your own framework. The kit never imports one.
- **Routing.** Mount the endpoint yourself, on POST.
- **Reading the turn.** The webhook envelope's `invocation` object is a pointer,
  not a projection: status, stop reason, failure code, waiting ToolCall ids,
  credit block, and nothing else. Read `getInvocation` or `getInvocationResult`
  for anything more.
- **Backstopping.** Retries are bounded, so a receiver that answers 503 forever
  still ends with a transition nobody recorded. `listEndedInvocations` is the
  reconciliation feed that finds those.
