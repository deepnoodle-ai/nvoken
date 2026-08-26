from __future__ import annotations

import json
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Literal

from .signed_delivery import (
    DeliveryKeyError,
    DeliverySigningKey,
    delivery_signing_keys,
    select_delivery_key,
    verify_signed_delivery,
)

#: The closed set of Turn webhook events.
WEBHOOK_EVENTS = ("turn.budget_hold", "turn.ended", "turn.waiting")


@dataclass(frozen=True)
class VerifiedWebhook:
    """One Turn webhook whose signature has been checked.

    ``event`` is read from the signed body. The endpoint URL may carry an
    unsigned per-event suffix; that belongs in logs, not in a dispatch
    decision.

    The envelope's ``turn`` object is a pointer to the Turn, not a
    projection of it: no transcript content, tool arguments, structured output,
    usage, provenance, or failure message. Read
    authoritative Turn reads for anything beyond what is there.
    """

    envelope: dict[str, Any]
    raw_body: bytes
    delivery_id: str
    event: str
    sequence: int
    turn_id: str
    conversation_id: str | None
    memory_space_id: str | None
    content_expires_at: str | None
    behavior_source: dict[str, Any]
    tenant_key: str
    user_key: str | None
    key_id: str
    key_version: int
    timestamp: datetime

    def supersedes(self, applied_sequence: int) -> bool:
        """Report whether this delivery is later than what was already applied.

        Delivery is at least once, so the same transition can arrive twice and
        a redelivery can land after a later one. Keep the highest sequence
        applied per Turn and fold only what supersedes it; a receiver
        that applies whichever arrived last rolls its own state backwards. Pass
        0 for a Turn nothing has been applied for yet.

        This is also the dedup: a repeat carries a sequence already applied, so
        nothing further is needed to make handling idempotent. Answer it with
        :func:`accept_webhook` all the same — it was delivered, and asking for
        redelivery of something already handled only produces the same repeat.
        """
        return self.sequence > applied_sequence


def verify_webhook(
    key: bytes,
    headers: dict[str, str],
    raw_body: bytes,
    *,
    now: datetime | None = None,
) -> VerifiedWebhook:
    """Check one Turn webhook delivery and return its signed body.

    It shares its signature scheme with :func:`verify_callback`, so a host that
    receives both implements verification once and dispatches on what the
    verified body says.

    The key is the App's ``webhook``-purpose signing key. Callbacks are signed
    with the ``callback``-purpose key, so a receiver serving both endpoints
    holds two keys and must not try either against the other's deliveries.
    """
    delivery = verify_signed_delivery(key, headers, raw_body, now=now)
    envelope = json.loads(raw_body)
    context = envelope.get("nvoken", {})
    if context.get("schema_version") != 2:
        raise ValueError("unsupported webhook schema version")
    # The idempotency key on a webhook is the delivery id, so both headers pin
    # the same fact and both must agree with the body that was signed.
    if (
        context.get("delivery_id") != delivery.delivery_id
        or delivery.idempotency_key != delivery.delivery_id
    ):
        raise ValueError("webhook identity header does not match signed body")
    # Refusing an unknown event here keeps the dispatch below it total. A new
    # event nvoken adds later reaches a receiver that has no branch for it, and
    # answering it as if it were understood would settle a delivery the host in
    # fact ignored.
    event = context.get("event")
    if event not in WEBHOOK_EVENTS:
        raise ValueError(f"unsupported webhook event {event!r}")
    sequence = context.get("sequence")
    if not isinstance(sequence, int) or isinstance(sequence, bool) or sequence < 1:
        raise ValueError("webhook sequence must be positive")
    turn_id = context.get("turn_id")
    tenant_key = context.get("tenant_key")
    behavior_source = context.get("behavior_source")
    if not turn_id or not tenant_key or not isinstance(behavior_source, dict):
        raise ValueError("webhook envelope is missing Turn attribution")
    return VerifiedWebhook(
        envelope=envelope,
        raw_body=bytes(raw_body),
        delivery_id=delivery.delivery_id,
        event=event,
        sequence=sequence,
        turn_id=turn_id,
        conversation_id=context.get("conversation_id"),
        memory_space_id=context.get("memory_space_id"),
        content_expires_at=context.get("content_expires_at"),
        behavior_source=dict(behavior_source),
        tenant_key=tenant_key,
        user_key=context.get("user_key"),
        key_id=delivery.key_id,
        key_version=delivery.key_version,
        timestamp=delivery.timestamp,
    )


@dataclass(frozen=True)
class WebhookReply:
    """The HTTP answer to one webhook delivery.

    nvoken ignores the response body, so only the status carries meaning, and
    no answer ever affects the Turn the webhook describes.
    """

    status: int


def accept_webhook() -> WebhookReply:
    """Take responsibility for the delivery. nvoken will not send it again."""
    return WebhookReply(status=200)


def retry_webhook() -> WebhookReply:
    """Ask nvoken to deliver again.

    For a receiver that could not record the transition right now — its store
    was unreachable, or it is shedding load. Retries are bounded, so a receiver
    that answers this forever still ends with a transition nobody recorded;
    listing ended Turns through :attr:`Client.raw` is the backstop that finds those.
    """
    return WebhookReply(status=503)


def webhook_status_is_retried(status: int) -> bool:
    """Report whether nvoken redelivers after a receiver answers with this.

    Any 5xx is retried, as are 408, 425, and 429. Every other non-2xx answer —
    400, 401, 403, 404, 409, 410, 422 among them — is permanent, and the
    transition it described is never delivered again. Refusing a body that
    genuinely failed verification with 401 is therefore right: redelivering it
    would fail the same way. Refusing one because the signing key could not be
    read is not, and should answer :func:`retry_webhook` instead, since the two
    are indistinguishable to nvoken and only one of them is the sender's fault.
    """
    return status in (408, 425, 429) or status >= 500


#: Records one transition. Raising asks nvoken to deliver it again, so raise
#: when the receiver could not record it and return when it did.
WebhookEventHandler = Callable[["VerifiedWebhook"], Awaitable[None]]


@dataclass(frozen=True)
class WebhookDelivery:
    """What a receiver did with one webhook delivery, and the reply to write."""

    reply: WebhookReply
    outcome: Literal["handled", "ignored", "refused", "failed"]
    #: A stable token for a log line. Never echoed to nvoken, which ignores
    #: webhook bodies.
    reason: str
    delivery: VerifiedWebhook | None = None
    cause: BaseException | None = None


class WebhookReceiver:
    """Answers a Turn-webhook endpoint.

    It is :class:`~nvoken.callback.CallbackReceiver`'s twin — same key table,
    same reply discipline — because nvoken signs both deliveries the same way.

    It is a separate receiver rather than a mode of one, because the two
    endpoints hold different keys: callbacks are signed with the App's
    ``callback``-purpose key and webhooks with its ``webhook``-purpose key, and
    neither may be tried against the other's deliveries.

    =========================================  ======  ================================================
    situation                                  status  why
    =========================================  ======  ================================================
    no keys configured                         503     an operator error, still fixable in the window
    signing identity not held                  401     a real identity failure; redelivery repeats it
    signature, timestamp, or envelope invalid  401     the same bytes fail the same way
    no handler for the signed event            200     it was delivered; redelivery finds the same gap
    handler returned                           200     the transition is recorded
    handler raised                             503     the receiver could not record it
    =========================================  ======  ================================================

    **Ordering stays yours.** Delivery is at least once and out of order, so the
    highest applied ``sequence`` per Turn has to be read and written in
    the same transaction as the state it guards — which is the host's
    transaction, not one this kit can open. Call
    :meth:`VerifiedWebhook.supersedes` inside it. A superseded delivery is
    still a delivery: record nothing and return, so it answers 200.
    """

    def __init__(
        self,
        *,
        keys: Sequence[DeliverySigningKey],
        events: Mapping[str, WebhookEventHandler],
        now: Callable[[], datetime] | None = None,
    ) -> None:
        self._keys = delivery_signing_keys(keys)
        self._events = dict(events)
        self._now = now

    async def handle(self, headers: dict[str, str], raw_body: bytes) -> WebhookDelivery:
        """Answer one delivery.

        It never raises: everything that can go wrong is a status nvoken
        understands, and the outcome says which.
        """
        try:
            secret = select_delivery_key(self._keys, headers)
            delivery = verify_webhook(
                secret, headers, raw_body, now=self._now() if self._now else None
            )
        except DeliveryKeyError as error:
            return WebhookDelivery(
                reply=retry_webhook() if error.retryable else WebhookReply(status=401),
                outcome="failed" if error.retryable else "refused",
                reason=error.reason,
                cause=error,
            )
        except Exception as error:  # noqa: BLE001 - every failure is a status
            return WebhookDelivery(
                reply=WebhookReply(status=401),
                outcome="refused",
                reason="invalid_signature",
                cause=error,
            )

        handler = self._events.get(delivery.event)
        # A subscribed event with no handler is a gap in this receiver, not a
        # failure of the delivery. Answering 503 would only spend nvoken's
        # bounded retries reaching the same absent handler, and lose it anyway.
        if handler is None:
            return WebhookDelivery(
                reply=accept_webhook(),
                outcome="ignored",
                reason="unhandled_event",
                delivery=delivery,
            )
        try:
            await handler(delivery)
        except Exception as error:  # noqa: BLE001 - every failure is a status
            return WebhookDelivery(
                reply=retry_webhook(),
                outcome="failed",
                reason="handler_failed",
                delivery=delivery,
                cause=error,
            )
        return WebhookDelivery(
            reply=accept_webhook(),
            outcome="handled",
            reason="recorded",
            delivery=delivery,
        )
