from __future__ import annotations

import hashlib
import hmac
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone

#: How far a delivery's signed timestamp may sit from the receiver's clock.
SIGNATURE_TIMESTAMP_WINDOW = timedelta(minutes=5)


@dataclass(frozen=True)
class SignedDelivery:
    """One delivery whose signature has been checked, before its body is read.

    nvoken signs tool callbacks and Invocation webhooks the same way, so
    everything up to and including the HMAC comparison lives here once rather
    than in two copies that have to be kept in step. What differs is only what
    the verified body then means: a callback settles a named ToolCall, a
    webhook reports a transition that already happened.

    ``idempotency_key`` is the ToolCall id on a callback and the delivery id on
    a webhook. In both it is the value a receiver deduplicates on.
    """

    delivery_id: str
    idempotency_key: str
    key_id: str
    key_version: int
    timestamp: datetime


def verify_signed_delivery(
    key: bytes,
    headers: dict[str, str],
    raw_body: bytes,
    *,
    now: datetime | None = None,
) -> SignedDelivery:
    """Check the signature headers and the HMAC over the raw body.

    This reads nothing out of the body, so it is total over both delivery kinds
    and cannot acquire a requirement that belongs to one of them.
    """
    normalized = {name.lower(): value for name, value in headers.items()}
    if len(key) < 32:
        raise ValueError("delivery signing key must be at least 32 bytes")
    if normalized.get("x-nvoken-signature-version") != "v1":
        raise ValueError("unsupported delivery signature version")
    try:
        timestamp_seconds = int(normalized["x-nvoken-timestamp"])
        key_version = int(normalized["x-nvoken-signing-key-version"])
    except (KeyError, ValueError) as error:
        raise ValueError("delivery timestamp or key version is invalid") from error
    timestamp = datetime.fromtimestamp(timestamp_seconds, timezone.utc)
    current = now or datetime.now(timezone.utc)
    if abs(current - timestamp) > SIGNATURE_TIMESTAMP_WINDOW:
        raise ValueError("delivery timestamp is outside the accepted window")
    delivery_id = normalized.get("x-nvoken-delivery-id", "")
    idempotency_key = normalized.get("idempotency-key", "")
    key_id = normalized.get("x-nvoken-signing-key-id", "")
    if not delivery_id or not idempotency_key or not key_id or key_version <= 0:
        raise ValueError("delivery identity headers are invalid")
    provided = normalized.get("x-nvoken-signature", "")
    if not provided.startswith("sha256="):
        raise ValueError("delivery signature must use sha256 prefix")
    canonical = f"v1.{delivery_id}.{timestamp_seconds}.".encode() + raw_body
    expected = "sha256=" + hmac.new(key, canonical, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(provided, expected):
        raise ValueError("delivery signature mismatch")
    return SignedDelivery(
        delivery_id=delivery_id,
        idempotency_key=idempotency_key,
        key_id=key_id,
        key_version=key_version,
        timestamp=timestamp,
    )
