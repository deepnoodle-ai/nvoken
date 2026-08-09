from __future__ import annotations

import hashlib
import hmac
import json
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import Any, Protocol, TypeVar, Generic


@dataclass(frozen=True)
class VerifiedCallback:
    envelope: dict[str, Any]
    raw_body: bytes
    delivery_id: str
    tool_call_id: str
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
    normalized = {name.lower(): value for name, value in headers.items()}
    if len(key) < 32:
        raise ValueError("callback signing key must be at least 32 bytes")
    if normalized.get("x-nvoken-signature-version") != "v1":
        raise ValueError("unsupported callback signature version")
    try:
        timestamp_seconds = int(normalized["x-nvoken-timestamp"])
        key_version = int(normalized["x-nvoken-signing-key-version"])
    except (KeyError, ValueError) as error:
        raise ValueError("callback timestamp or key version is invalid") from error
    timestamp = datetime.fromtimestamp(timestamp_seconds, timezone.utc)
    current = now or datetime.now(timezone.utc)
    if abs(current - timestamp) > timedelta(minutes=5):
        raise ValueError("callback timestamp is outside the accepted window")
    delivery_id = normalized.get("x-nvoken-delivery-id", "")
    tool_call_id = normalized.get("idempotency-key", "")
    key_id = normalized.get("x-nvoken-signing-key-id", "")
    if not delivery_id or not tool_call_id or not key_id or key_version <= 0:
        raise ValueError("callback identity headers are invalid")
    provided = normalized.get("x-nvoken-signature", "")
    if not provided.startswith("sha256="):
        raise ValueError("callback signature must use sha256 prefix")
    canonical = f"v1.{delivery_id}.{timestamp_seconds}.".encode() + raw_body
    expected = "sha256=" + hmac.new(key, canonical, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(provided, expected):
        raise ValueError("callback signature mismatch")
    envelope = json.loads(raw_body)
    context = envelope.get("nvoken", {})
    if context.get("schema_version") != 1:
        raise ValueError("unsupported callback schema version")
    if context.get("delivery_id") != delivery_id or context.get("tool_call_id") != tool_call_id:
        raise ValueError("callback identity header does not match signed body")
    return VerifiedCallback(
        envelope=envelope,
        raw_body=bytes(raw_body),
        delivery_id=delivery_id,
        tool_call_id=tool_call_id,
        key_id=key_id,
        key_version=key_version,
        timestamp=timestamp,
    )


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
