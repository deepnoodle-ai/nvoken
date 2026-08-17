from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from .signed_delivery import verify_signed_delivery

#: The closed set of Invocation webhook events.
WEBHOOK_EVENTS = ("invocation.waiting", "invocation.paused", "invocation.ended")


@dataclass(frozen=True)
class VerifiedWebhook:
    """One Invocation webhook whose signature has been checked.

    ``event`` is read from the signed body. The endpoint URL may carry an
    unsigned per-event suffix; that belongs in logs, not in a dispatch
    decision.

    The envelope's ``invocation`` object is a pointer to the turn, not a
    projection of it: no transcript content, tool arguments, structured output,
    usage, provenance, or failure message. Read
    :meth:`Client.get_invocation` or :meth:`Client.get_invocation_result` for
    anything beyond what is there.
    """

    envelope: dict[str, Any]
    raw_body: bytes
    delivery_id: str
    event: str
    sequence: int
    invocation_id: str
    session_id: str
    key_id: str
    key_version: int
    timestamp: datetime

    def supersedes(self, applied_sequence: int) -> bool:
        """Report whether this delivery is later than what was already applied.

        Delivery is at least once, so the same transition can arrive twice and
        a redelivery can land after a later one. Keep the highest sequence
        applied per Invocation and fold only what supersedes it; a receiver
        that applies whichever arrived last rolls its own state backwards. Pass
        0 for an Invocation nothing has been applied for yet.

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
    """Check one Invocation webhook delivery and return its signed body.

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
    if context.get("schema_version") != 1:
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
    return VerifiedWebhook(
        envelope=envelope,
        raw_body=bytes(raw_body),
        delivery_id=delivery.delivery_id,
        event=event,
        sequence=sequence,
        invocation_id=context.get("invocation_id", ""),
        session_id=context.get("session_id", ""),
        key_id=delivery.key_id,
        key_version=delivery.key_version,
        timestamp=delivery.timestamp,
    )


@dataclass(frozen=True)
class WebhookReply:
    """The HTTP answer to one webhook delivery.

    nvoken ignores the response body, so only the status carries meaning, and
    no answer ever affects the Invocation the webhook describes.
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
    :meth:`Client.list_ended_invocations` is the backstop that finds those.
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
