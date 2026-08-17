from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from typing import Any, Protocol, TypeVar, Generic

from .signed_delivery import verify_signed_delivery


@dataclass(frozen=True)
class VerifiedCallback:
    """One verified callback delivery.

    ``tool_name`` comes from inside the signed body, so a receiver serving
    several tools dispatches on it directly. Any per-tool path or query suffix
    on the endpoint URL is unsigned and belongs in logs, not in a dispatch
    decision.
    """

    envelope: dict[str, Any]
    raw_body: bytes
    delivery_id: str
    tool_call_id: str
    tool_name: str
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


T = TypeVar("T")


class CallbackResultStore(Protocol, Generic[T]):
    async def put_if_absent(self, tool_call_id: str, result: T) -> tuple[T, bool]: ...


async def deduplicate_callback_result(
    store: CallbackResultStore[T],
    tool_call_id: str,
    result: T,
) -> tuple[T, bool]:
    stored, inserted = await store.put_if_absent(tool_call_id, result)
    return stored, not inserted
