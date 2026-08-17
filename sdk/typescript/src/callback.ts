import {
  DeliveryKeyError,
  deliverySigningKeys,
  selectDeliveryKey,
  verifySignedDelivery,
  type DeliverySigningKey,
} from "./signed-delivery.js";

export interface CallbackEnvelope {
  nvoken: {
    schema_version: number;
    delivery_id: string;
    tool_call_id: string;
    /**
     * The tool this delivery is for. It is inside the signed body, so a
     * receiver serving several tools dispatches on it directly. Any per-tool
     * path or query suffix on the endpoint URL is unsigned and belongs in
     * logs, not in a dispatch decision.
     */
    tool_name: string;
    invocation_id: string;
    session_id: string;
    agent_key: string;
    tenant_key?: string;
  };
  /**
   * What the Session was bound to at creation, in the host's own terms, absent
   * when it was created without one.
   *
   * It is a sibling of `nvoken` rather than a member of it, and the placement
   * is the rule: everything inside `nvoken` is a fact nvoken minted or
   * resolved, while this is a value the host asserted and nvoken carried
   * unchanged. Signing proves it reached the receiver as recorded, not that it
   * is true.
   *
   * **A value repeated in tool input may only agree with this, never establish
   * it.** The model writes the input; it does not write this.
   */
  authorization_context?: Record<string, string>;
  input: unknown;
}

export interface VerifiedCallback {
  envelope: CallbackEnvelope;
  rawBody: Uint8Array;
  deliveryId: string;
  toolCallId: string;
  toolName: string;
  /**
   * The Session's authorization context, read off the signed body. Authorize
   * the delivery from this rather than from anything in `envelope.input`, and
   * treat a value that appears in both as agreement to check rather than as a
   * second source.
   */
  authorizationContext?: Record<string, string>;
  keyId: string;
  keyVersion: number;
  timestamp: Date;
}

/**
 * Checks one tool-callback delivery and returns its signed body. The signature
 * scheme is shared with `verifyWebhook`; only the checks below, which are about
 * what a callback body must say, are particular to it.
 */
export async function verifyCallback(
  key: Uint8Array,
  headers: Headers,
  rawBody: Uint8Array,
  now = new Date(),
): Promise<VerifiedCallback> {
  const { deliveryId, idempotencyKey: toolCallId, keyId, keyVersion, timestamp } =
    await verifySignedDelivery(key, headers, rawBody, now);
  const envelope = JSON.parse(new TextDecoder().decode(rawBody)) as CallbackEnvelope;
  if (envelope.nvoken.schema_version !== 1) throw new Error("unsupported callback schema version");
  if (envelope.nvoken.delivery_id !== deliveryId || envelope.nvoken.tool_call_id !== toolCallId) throw new Error("callback identity header does not match signed body");
  // tool_name is required on the wire, so a missing one is a sender that is not
  // nvoken or a body that is not a callback. Failing here keeps the dispatch
  // below it total: no receiver needs a branch for "no name".
  if (!envelope.nvoken.tool_name) throw new Error("callback envelope is missing tool_name");
  return {
    envelope,
    rawBody: rawBody.slice(),
    deliveryId,
    toolCallId,
    toolName: envelope.nvoken.tool_name,
    authorizationContext: envelope.authorization_context,
    keyId,
    keyVersion,
    timestamp,
  };
}

/**
 * The HTTP answer to one callback delivery. Rendering it is left to the host's
 * web framework: write `status`, and `body` when it is not undefined.
 */
export interface CallbackReply {
  status: number;
  body?: string;
}

/**
 * Settles the ToolCall inline. Content may be any JSON value, encoded to at
 * most 256 KiB and 32 levels of nesting. The turn resumes as soon as nvoken
 * records the reply.
 */
export function callbackResult(content: unknown, isError = false): CallbackReply {
  return {
    status: 200,
    body: JSON.stringify(isError ? { content, is_error: true } : { content }),
  };
}

/**
 * Accepts delivery without settling the ToolCall, for work that will outlive
 * this tool's reply deadline — its declared `timeout_seconds`, or the App's
 * default when it declares none. Settle it later with
 * `client.submitToolResults`, reusing the delivery's ToolCall ID.
 *
 * This trades away the fail-loud guarantee. nvoken marks an unacknowledged
 * delivery failed once its retries are exhausted, so the turn always moves on.
 * An acknowledged call instead waits under the host's responsibility, bounded
 * only by the Invocation's `limits.waitingTimeoutSeconds`. Acknowledge only
 * when something durable will settle the call.
 */
export function acknowledgeCallback(): CallbackReply {
  return { status: 202 };
}

/**
 * Where a receiver records what it already answered, so a redelivery returns
 * that answer instead of running the tool again.
 *
 * Both operations are needed and they are needed in this order. `find` runs
 * before the tool does, because a redelivery that re-runs it repeats every
 * effect it had. `putIfAbsent` runs after, because two deliveries of one
 * ToolCall can be in flight at once and only one answer may win.
 */
export interface CallbackResultStore<T> {
  find(toolCallId: string): Promise<T | undefined>;
  putIfAbsent(toolCallId: string, result: T): Promise<{ value: T; inserted: boolean }>;
}

/**
 * Runs one tool for one delivery. Return the reply — `callbackResult` to settle
 * the call, `acknowledgeCallback` to take it away and settle it later.
 *
 * A tool that failed still returns: `callbackResult(reason, true)` settles the
 * call carrying `is_error`, which the model can read and correct itself
 * against. Throwing means something in the *receiver* failed, not the tool, and
 * answers 503 so nvoken redelivers.
 */
export type CallbackToolHandler = (
  delivery: VerifiedCallback,
) => Promise<CallbackReply> | CallbackReply;

/**
 * What a receiver did with one delivery, and the reply the host writes.
 *
 * `outcome` is what the status alone cannot say — a 200 that replayed a
 * recorded answer did no work. `reason` is a stable token for a log line and is
 * never echoed into the reply body, because nvoken is not the audience for it
 * and a refused sender should learn nothing.
 */
export interface CallbackDeliveryOutcome {
  reply: CallbackReply;
  outcome: "settled" | "acknowledged" | "replayed" | "refused" | "failed";
  reason: string;
  /** Present once the signature checked out, whatever happened afterwards. */
  delivery?: VerifiedCallback;
  /** The throw behind a `refused` or `failed` outcome, for the host's logger. */
  cause?: unknown;
}

export interface CallbackReceiverOptions {
  /** Every secret this endpoint accepts. Two entries span a key rotation. */
  keys: readonly DeliverySigningKey[];
  /** Handlers by the tool name nvoken signs into the body. */
  tools: Readonly<Record<string, CallbackToolHandler>>;
  /**
   * Where answered ToolCalls are recorded. Omit it only when every tool here is
   * safe to run twice: without a store, a redelivery runs the tool again.
   */
  store?: CallbackResultStore<CallbackReply>;
  now?: () => Date;
}

export interface CallbackReceiver {
  /**
   * Answers one delivery. It never throws: everything that can go wrong is a
   * status nvoken understands, and the outcome says which.
   */
  handle(headers: Headers, rawBody: Uint8Array): Promise<CallbackDeliveryOutcome>;
}

/**
 * Builds the receiver for a tool-callback endpoint: key selection, signature
 * verification, dispatch on the signed tool name, deduplication, and the reply
 * discipline nvoken reads.
 *
 * That discipline is the part worth having in one place, because every status
 * here is a decision about whether nvoken tries again:
 *
 * | situation | status | why |
 * | --- | --- | --- |
 * | no keys configured | 503 | an operator error, still fixable inside the retry window |
 * | signing identity not held | 401 | a real identity failure; redelivery reproduces it |
 * | signature, timestamp, or envelope invalid | 401 | the same bytes fail the same way |
 * | no handler for the signed tool name | 400 | nothing here can ever run it |
 * | handler returned a reply | 200 or 202 | the tool answered, or took the call away |
 * | handler threw | 503 | the receiver failed, not the tool — and the store makes retrying safe |
 *
 * A tool that failed is not a receiver that failed. Settle it with
 * `callbackResult(reason, true)`: the model can read a tool error and correct
 * itself, while a 5xx only has nvoken deliver the same doomed call again.
 *
 * The endpoint is public because nvoken must reach it, and it is not anonymous:
 * nothing below the signature check runs until the HMAC over the raw bytes
 * verifies.
 */
export function createCallbackReceiver(options: CallbackReceiverOptions): CallbackReceiver {
  const table = deliverySigningKeys(options.keys);
  const tools = { ...options.tools };
  const store = options.store;
  const now = options.now ?? (() => new Date());

  return {
    async handle(headers, rawBody) {
      let delivery: VerifiedCallback;
      try {
        const secret = selectDeliveryKey(table, headers);
        delivery = await verifyCallback(secret, headers, rawBody, now());
      } catch (error) {
        const retryable = error instanceof DeliveryKeyError && error.retryable;
        return {
          reply: { status: retryable ? 503 : 401 },
          outcome: retryable ? "failed" : "refused",
          reason: error instanceof DeliveryKeyError ? error.reason : "invalid_signature",
          cause: error,
        };
      }

      const handler = tools[delivery.toolName];
      if (!handler) {
        return { reply: { status: 400 }, outcome: "refused", reason: "unknown_tool", delivery };
      }

      try {
        const recorded = await store?.find(delivery.toolCallId);
        if (recorded) return { reply: recorded, outcome: "replayed", reason: "recorded", delivery };

        const reply = await handler(delivery);
        if (!store) return { reply, outcome: settledOrAcknowledged(reply), reason: "ran", delivery };

        const winner = await store.putIfAbsent(delivery.toolCallId, reply);
        return winner.inserted
          ? { reply: winner.value, outcome: settledOrAcknowledged(winner.value), reason: "ran", delivery }
          : // Another delivery of the same ToolCall answered first. Its reply is
            // the one nvoken already has, so returning ours would be a second
            // answer to a call that has one.
            { reply: winner.value, outcome: "replayed", reason: "raced", delivery };
      } catch (error) {
        return { reply: { status: 503 }, outcome: "failed", reason: "handler_failed", delivery, cause: error };
      }
    },
  };
}

function settledOrAcknowledged(reply: CallbackReply): "settled" | "acknowledged" {
  return reply.status === 202 ? "acknowledged" : "settled";
}

