import { verifySignedDelivery } from "./signed-delivery.js";

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
  input: unknown;
}

export interface VerifiedCallback {
  envelope: CallbackEnvelope;
  rawBody: Uint8Array;
  deliveryId: string;
  toolCallId: string;
  toolName: string;
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

export interface CallbackResultStore<T> {
  putIfAbsent(toolCallId: string, result: T): Promise<{ value: T; inserted: boolean }>;
}

export async function deduplicateCallbackResult<T>(
  store: CallbackResultStore<T>,
  toolCallId: string,
  result: T,
): Promise<{ value: T; replayed: boolean }> {
  const stored = await store.putIfAbsent(toolCallId, result);
  return { value: stored.value, replayed: !stored.inserted };
}

