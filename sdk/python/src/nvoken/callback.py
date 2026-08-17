from __future__ import annotations

import json
from collections.abc import Awaitable, Callable, Mapping, Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Literal, Protocol

from .signed_delivery import (
    DeliveryKeyError,
    DeliverySigningKey,
    delivery_signing_keys,
    select_delivery_key,
    verify_signed_delivery,
)


@dataclass(frozen=True)
class VerifiedCallback:
    """One verified callback delivery.

    ``tool_name`` comes from inside the signed body, so a receiver serving
    several tools dispatches on it directly. Any per-tool path or query suffix
    on the endpoint URL is unsigned and belongs in logs, not in a dispatch
    decision.

    ``authorization_context`` is what the Session was bound to at creation, in
    the host's own terms, and is empty when it was created without one. It sits
    beside ``nvoken`` in the envelope rather than inside it, and the placement is
    the rule: everything inside ``nvoken`` is a fact nvoken minted or resolved,
    while this is a value the host asserted and nvoken carried unchanged.
    Signing proves it reached the receiver as recorded, not that it is true.

    Authorize the delivery from it rather than from anything in
    ``envelope["input"]``, and treat a value that appears in both as agreement
    to check rather than as a second source. The model writes the input; it does
    not write this.
    """

    envelope: dict[str, Any]
    raw_body: bytes
    delivery_id: str
    tool_call_id: str
    tool_name: str
    authorization_context: dict[str, str]
    key_id: str
    key_version: int
    timestamp: datetime


def verify_callback(
    key: bytes,
    headers: dict[str, str],
    raw_body: bytes,
    *,
    now: datetime | None = None,
) -> VerifiedCallback:
    """Check one tool-callback delivery and return its signed body.

    The signature scheme is shared with :func:`verify_webhook`; only the checks
    below, which are about what a callback body must say, are particular to it.
    """
    delivery = verify_signed_delivery(key, headers, raw_body, now=now)
    delivery_id = delivery.delivery_id
    tool_call_id = delivery.idempotency_key
    envelope = json.loads(raw_body)
    context = envelope.get("nvoken", {})
    if context.get("schema_version") != 1:
        raise ValueError("unsupported callback schema version")
    if context.get("delivery_id") != delivery_id or context.get("tool_call_id") != tool_call_id:
        raise ValueError("callback identity header does not match signed body")
    # tool_name is required on the wire, so a missing one is a sender that is
    # not nvoken or a body that is not a callback. Failing here keeps the
    # dispatch below it total: no receiver needs a branch for "no name".
    tool_name = context.get("tool_name")
    if not tool_name:
        raise ValueError("callback envelope is missing tool_name")
    return VerifiedCallback(
        envelope=envelope,
        raw_body=bytes(raw_body),
        delivery_id=delivery_id,
        tool_call_id=tool_call_id,
        tool_name=tool_name,
        authorization_context=dict(envelope.get("authorization_context") or {}),
        key_id=delivery.key_id,
        key_version=delivery.key_version,
        timestamp=delivery.timestamp,
    )


@dataclass(frozen=True)
class CallbackReply:
    """The HTTP answer to one callback delivery.

    Rendering it is left to the host's web framework: write ``status``, and
    ``body`` when it is not None.
    """

    status: int
    body: str | None = None


def callback_result(content: Any, is_error: bool = False) -> CallbackReply:
    """Settle the ToolCall inline.

    Content may be any JSON value, encoded to at most 256 KiB and 32 levels of
    nesting. The turn resumes as soon as nvoken records the reply.
    """
    payload: dict[str, Any] = {"content": content}
    if is_error:
        payload["is_error"] = True
    return CallbackReply(status=200, body=json.dumps(payload))


def acknowledge_callback() -> CallbackReply:
    """Accept delivery without settling the ToolCall.

    For work that will outlive this tool's reply deadline — its declared
    ``timeout_seconds``, or the App's default when it declares none. Settle it
    later with :meth:`Client.submit_tool_results`, reusing the delivery's
    ToolCall id.

    This trades away the fail-loud guarantee. nvoken marks an unacknowledged
    delivery failed once its retries are exhausted, so the turn always moves on.
    An acknowledged call instead waits under the host's responsibility, bounded
    only by the Invocation's ``limits.waiting_timeout_seconds``. Acknowledge only
    when something durable will settle the call.
    """
    return CallbackReply(status=202)


class CallbackResultStore(Protocol):
    """Where a receiver records what it already answered, so a redelivery
    returns that answer instead of running the tool again.

    Both operations are needed and they are needed in this order. ``find`` runs
    before the tool does, because a redelivery that re-runs it repeats every
    effect it had. ``put_if_absent`` runs after, because two deliveries of one
    ToolCall can be in flight at once and only one answer may win.
    """

    async def find(self, tool_call_id: str) -> CallbackReply | None: ...

    async def put_if_absent(
        self, tool_call_id: str, reply: CallbackReply
    ) -> tuple[CallbackReply, bool]: ...


#: Runs one tool for one delivery. Return the reply — :func:`callback_result` to
#: settle the call, :func:`acknowledge_callback` to take it away and settle it
#: later.
#:
#: A tool that failed still returns: ``callback_result(reason, True)`` settles
#: the call carrying ``is_error``, which the model can read and correct itself
#: against. Raising means something in the *receiver* failed, not the tool, and
#: answers 503 so nvoken redelivers.
CallbackToolHandler = Callable[["VerifiedCallback"], Awaitable[CallbackReply]]


@dataclass(frozen=True)
class CallbackDelivery:
    """What a receiver did with one delivery, and the reply the host writes.

    ``outcome`` is what the status alone cannot say — a 200 that replayed a
    recorded answer did no work. ``reason`` is a stable token for a log line and
    is never echoed into the reply body, because nvoken is not the audience for
    it and a refused sender should learn nothing.
    """

    reply: CallbackReply
    outcome: Literal["settled", "acknowledged", "replayed", "refused", "failed"]
    reason: str
    #: Set once the signature checked out, whatever happened afterwards.
    delivery: VerifiedCallback | None = None
    #: The exception behind a refused or failed outcome, for the host's logger.
    cause: BaseException | None = None


class CallbackReceiver:
    """Answers a tool-callback endpoint: key selection, signature verification,
    dispatch on the signed tool name, deduplication, and the reply discipline
    nvoken reads.

    That discipline is the part worth having in one place, because every status
    here is a decision about whether nvoken tries again:

    ==========================================  ======  ===============================================
    situation                                   status  why
    ==========================================  ======  ===============================================
    no keys configured                          503     an operator error, still fixable in the window
    signing identity not held                   401     a real identity failure; redelivery repeats it
    signature, timestamp, or envelope invalid   401     the same bytes fail the same way
    no handler for the signed tool name         400     nothing here can ever run it
    handler returned a reply                    200/202 the tool answered, or took the call away
    handler raised                              503     the receiver failed, not the tool
    ==========================================  ======  ===============================================

    A tool that failed is not a receiver that failed. Settle it with
    ``callback_result(reason, True)``: the model can read a tool error and
    correct itself, while a 5xx only has nvoken deliver the same doomed call
    again.

    The endpoint is public because nvoken must reach it, and it is not
    anonymous: nothing below the signature check runs until the HMAC over the
    raw bytes verifies.

    ``store`` may be omitted only when every tool here is safe to run twice:
    without one, a redelivery runs the tool again.
    """

    def __init__(
        self,
        *,
        keys: Sequence[DeliverySigningKey],
        tools: Mapping[str, CallbackToolHandler],
        store: CallbackResultStore | None = None,
        now: Callable[[], datetime] | None = None,
    ) -> None:
        self._keys = delivery_signing_keys(keys)
        self._tools = dict(tools)
        self._store = store
        self._now = now

    async def handle(self, headers: dict[str, str], raw_body: bytes) -> CallbackDelivery:
        """Answer one delivery.

        It never raises: everything that can go wrong is a status nvoken
        understands, and the outcome says which.
        """
        try:
            secret = select_delivery_key(self._keys, headers)
            delivery = verify_callback(
                secret, headers, raw_body, now=self._now() if self._now else None
            )
        except DeliveryKeyError as error:
            status = 503 if error.retryable else 401
            return CallbackDelivery(
                reply=CallbackReply(status=status),
                outcome="failed" if error.retryable else "refused",
                reason=error.reason,
                cause=error,
            )
        except Exception as error:  # noqa: BLE001 - every failure is a status
            return CallbackDelivery(
                reply=CallbackReply(status=401),
                outcome="refused",
                reason="invalid_signature",
                cause=error,
            )

        handler = self._tools.get(delivery.tool_name)
        if handler is None:
            return CallbackDelivery(
                reply=CallbackReply(status=400),
                outcome="refused",
                reason="unknown_tool",
                delivery=delivery,
            )

        try:
            if self._store is not None:
                recorded = await self._store.find(delivery.tool_call_id)
                if recorded is not None:
                    return CallbackDelivery(
                        reply=recorded,
                        outcome="replayed",
                        reason="recorded",
                        delivery=delivery,
                    )
            reply = await handler(delivery)
            if self._store is None:
                return CallbackDelivery(
                    reply=reply,
                    outcome=_settled_or_acknowledged(reply),
                    reason="ran",
                    delivery=delivery,
                )
            stored, inserted = await self._store.put_if_absent(delivery.tool_call_id, reply)
            if inserted:
                return CallbackDelivery(
                    reply=stored,
                    outcome=_settled_or_acknowledged(stored),
                    reason="ran",
                    delivery=delivery,
                )
            # Another delivery of the same ToolCall answered first. Its reply is
            # the one nvoken already has, so returning ours would be a second
            # answer to a call that has one.
            return CallbackDelivery(
                reply=stored, outcome="replayed", reason="raced", delivery=delivery
            )
        except Exception as error:  # noqa: BLE001 - every failure is a status
            return CallbackDelivery(
                reply=CallbackReply(status=503),
                outcome="failed",
                reason="handler_failed",
                delivery=delivery,
                cause=error,
            )


def _settled_or_acknowledged(reply: CallbackReply) -> Literal["settled", "acknowledged"]:
    return "acknowledged" if reply.status == 202 else "settled"
