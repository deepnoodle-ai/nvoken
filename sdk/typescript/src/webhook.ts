import {
  DeliveryKeyError,
  deliverySigningKeys,
  selectDeliveryKey,
  verifySignedDelivery,
  type DeliverySigningKey,
} from "./signed-delivery.js";
import { WebhookEvent } from "./generated/models/WebhookEvent.js";
import type { CreditBlock } from "./generated/models/CreditBlock.js";
import type { InvocationStatus } from "./generated/models/InvocationStatus.js";
import type { InvocationStopReason } from "./generated/models/InvocationStopReason.js";

// Derived from the generated enum rather than restated, so an event the
// contract adds cannot be one this verifier silently keeps refusing.
const WEBHOOK_EVENTS: readonly string[] = Object.values(WebhookEvent);

/**
 * The signed body of one Invocation webhook.
 *
 * It mirrors `CallbackEnvelope`: everything nvoken asserts sits under `nvoken`,
 * and the subject of the delivery sits beside it.
 */
export interface WebhookEnvelope {
  nvoken: {
    schema_version: number;
    delivery_id: string;
    event: WebhookEvent;
    /**
     * Counts transitions within one Invocation, from 1. See
     * `webhookSupersedes` for what a receiver does with it.
     */
    sequence: number;
    invocation_id: string;
    session_id: string;
    agent_key: string;
    tenant_key?: string;
  };
  /**
   * A pointer to the turn, not a projection of it. It carries no transcript
   * content, tool arguments, structured output, usage, provenance, or failure
   * message: read `getInvocation` or `getInvocationResult` for anything beyond
   * what is here.
   */
  invocation: {
    status: InvocationStatus;
    stop_reason?: InvocationStopReason;
    failure_code?: string;
    /**
     * The host tools the turn is parked on, on `invocation.waiting` only.
     * Tools nvoken delivers itself are absent: they are not work the host has
     * been handed.
     */
    waiting_tool_call_ids?: string[];
    /**
     * Names the account that could not fund the next attempt, when a spending
     * limit stopped the turn.
     */
    credit_block?: CreditBlock;
  };
}

/** One Invocation webhook whose signature has been checked. */
export interface VerifiedWebhook {
  envelope: WebhookEnvelope;
  rawBody: Uint8Array;
  deliveryId: string;
  /**
   * Read from the signed body. The endpoint URL may carry an unsigned
   * per-event suffix; that belongs in logs, not in a dispatch decision.
   */
  event: WebhookEvent;
  sequence: number;
  invocationId: string;
  sessionId: string;
  keyId: string;
  keyVersion: number;
  timestamp: Date;
}

/**
 * Checks one Invocation webhook delivery and returns its signed body. It
 * shares its signature scheme with `verifyCallback`, so a host that receives
 * both implements verification once and dispatches on what the verified body
 * says.
 *
 * The key is the App's `webhook`-purpose signing key. Callbacks are signed
 * with the `callback`-purpose key, so a receiver serving both endpoints holds
 * two keys and must not try either against the other's deliveries.
 */
export async function verifyWebhook(
  key: Uint8Array,
  headers: Headers,
  rawBody: Uint8Array,
  now = new Date(),
): Promise<VerifiedWebhook> {
  const { deliveryId, idempotencyKey, keyId, keyVersion, timestamp } =
    await verifySignedDelivery(key, headers, rawBody, now);
  const envelope = JSON.parse(new TextDecoder().decode(rawBody)) as WebhookEnvelope;
  if (envelope.nvoken?.schema_version !== 1) throw new Error("unsupported webhook schema version");
  // The idempotency key on a webhook is the delivery id, so both headers pin
  // the same fact and both must agree with the body that was signed.
  if (envelope.nvoken.delivery_id !== deliveryId || idempotencyKey !== deliveryId) {
    throw new Error("webhook identity header does not match signed body");
  }
  // Refusing an unknown event here keeps the dispatch below it total. A new
  // event nvoken adds later reaches a receiver that has no branch for it, and
  // answering it as if it were understood would settle a delivery the host in
  // fact ignored.
  if (!WEBHOOK_EVENTS.includes(envelope.nvoken.event)) {
    throw new Error(`unsupported webhook event ${envelope.nvoken.event}`);
  }
  if (!Number.isSafeInteger(envelope.nvoken.sequence) || envelope.nvoken.sequence < 1) {
    throw new Error("webhook sequence must be positive");
  }
  return {
    envelope,
    rawBody: rawBody.slice(),
    deliveryId,
    event: envelope.nvoken.event,
    sequence: envelope.nvoken.sequence,
    invocationId: envelope.nvoken.invocation_id,
    sessionId: envelope.nvoken.session_id,
    keyId,
    keyVersion,
    timestamp,
  };
}

/**
 * Reports whether a delivery describes a later transition of its Invocation
 * than the one already applied.
 *
 * Delivery is at least once, so the same transition can arrive twice and a
 * redelivery can land after a later one. Keep the highest sequence applied per
 * Invocation and fold only what supersedes it; a receiver that applies
 * whichever arrived last rolls its own state backwards. Pass 0 for an
 * Invocation nothing has been applied for yet.
 *
 * This is also the dedup: a repeat carries a sequence already applied, so
 * nothing further is needed to make handling idempotent. Answer it with
 * `acceptWebhook` all the same — it was delivered, and asking for redelivery
 * of something already handled only produces the same repeat.
 */
export function webhookSupersedes(delivery: VerifiedWebhook, appliedSequence: number): boolean {
  return delivery.sequence > appliedSequence;
}

/**
 * The HTTP answer to one webhook delivery. nvoken ignores the response body,
 * so only the status carries meaning, and no answer ever affects the
 * Invocation the webhook describes.
 */
export interface WebhookReply {
  status: number;
}

/** Takes responsibility for the delivery. nvoken will not send it again. */
export function acceptWebhook(): WebhookReply {
  return { status: 200 };
}

/**
 * Asks nvoken to deliver again, for a receiver that could not record the
 * transition right now — its store was unreachable, or it is shedding load.
 * Retries are bounded, so a receiver that answers this forever still ends with
 * a transition nobody recorded; `listEndedInvocations` is the backstop that
 * finds those.
 */
export function retryWebhook(): WebhookReply {
  return { status: 503 };
}

/**
 * Reports whether nvoken redelivers after a receiver answers with this status.
 *
 * Any 5xx is retried, as are 408, 425, and 429. Every other non-2xx answer —
 * 400, 401, 403, 404, 409, 410, 422 among them — is permanent, and the
 * transition it described is never delivered again. Refusing a body that
 * genuinely failed verification with 401 is therefore right: redelivering it
 * would fail the same way. Refusing one because the signing key could not be
 * read is not, and should answer `retryWebhook` instead, since the two are
 * indistinguishable to nvoken and only one of them is the sender's fault.
 */
export function webhookStatusIsRetried(status: number): boolean {
  return status === 408 || status === 425 || status === 429 || status >= 500;
}

/**
 * Records one transition. Throwing asks nvoken to deliver it again, so throw
 * when the receiver could not record it and return when it did.
 */
export type WebhookEventHandler = (delivery: VerifiedWebhook) => Promise<void> | void;

/** What a receiver did with one webhook delivery, and the reply the host writes. */
export interface WebhookDeliveryOutcome {
  reply: WebhookReply;
  outcome: "handled" | "ignored" | "refused" | "failed";
  /** A stable token for a log line. Never echoed to nvoken, which ignores bodies. */
  reason: string;
  delivery?: VerifiedWebhook;
  cause?: unknown;
}

export interface WebhookReceiverOptions {
  /** Every secret this endpoint accepts. Two entries span a key rotation. */
  keys: readonly DeliverySigningKey[];
  /** Handlers by the event nvoken signs into the body. */
  events: Readonly<Partial<Record<WebhookEvent, WebhookEventHandler>>>;
  now?: () => Date;
}

export interface WebhookReceiver {
  /**
   * Answers one delivery. It never throws: everything that can go wrong is a
   * status nvoken understands, and the outcome says which.
   */
  handle(headers: Headers, rawBody: Uint8Array): Promise<WebhookDeliveryOutcome>;
}

/**
 * Builds the receiver for an Invocation-webhook endpoint. It is the callback
 * receiver's twin — same key table, same reply discipline — because nvoken
 * signs both deliveries the same way.
 *
 * It is a separate receiver rather than a mode of one, because the two
 * endpoints hold different keys: callbacks are signed with the App's
 * `callback`-purpose key and webhooks with its `webhook`-purpose key, and
 * neither may be tried against the other's deliveries.
 *
 * | situation | status | why |
 * | --- | --- | --- |
 * | no keys configured | 503 | an operator error, still fixable inside the retry window |
 * | signing identity not held | 401 | a real identity failure; redelivery reproduces it |
 * | signature, timestamp, or envelope invalid | 401 | the same bytes fail the same way |
 * | no handler for the signed event | 200 | it was delivered; redelivering it finds the same absent handler |
 * | handler returned | 200 | the transition is recorded |
 * | handler threw | 503 | the receiver could not record it, so ask for it again |
 *
 * **Ordering stays yours.** Delivery is at least once and out of order, so the
 * highest applied `sequence` per Invocation has to be read and written in the
 * same transaction as the state it guards — which is the host's transaction,
 * not one this kit can open. Call `webhookSupersedes` inside it. A superseded
 * delivery is still a delivery: record nothing and return, so it answers 200.
 */
export function createWebhookReceiver(options: WebhookReceiverOptions): WebhookReceiver {
  const table = deliverySigningKeys(options.keys);
  const events = { ...options.events };
  const now = options.now ?? (() => new Date());

  return {
    async handle(headers, rawBody) {
      let delivery: VerifiedWebhook;
      try {
        const secret = selectDeliveryKey(table, headers);
        delivery = await verifyWebhook(secret, headers, rawBody, now());
      } catch (error) {
        const retryable = error instanceof DeliveryKeyError && error.retryable;
        return {
          reply: { status: retryable ? 503 : 401 },
          outcome: retryable ? "failed" : "refused",
          reason: error instanceof DeliveryKeyError ? error.reason : "invalid_signature",
          cause: error,
        };
      }

      const handler = events[delivery.event];
      // A subscribed event with no handler is a gap in this receiver, not a
      // failure of the delivery. Answering 503 would only spend nvoken's
      // bounded retries reaching the same absent handler, and lose it anyway.
      if (!handler) return { reply: acceptWebhook(), outcome: "ignored", reason: "unhandled_event", delivery };

      try {
        await handler(delivery);
        return { reply: acceptWebhook(), outcome: "handled", reason: "recorded", delivery };
      } catch (error) {
        return { reply: retryWebhook(), outcome: "failed", reason: "handler_failed", delivery, cause: error };
      }
    },
  };
}
