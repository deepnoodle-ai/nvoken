from __future__ import annotations

import hashlib
import hmac
from collections.abc import Sequence
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


@dataclass(frozen=True)
class DeliverySigningKey:
    """One secret a receiver will accept deliveries signed with.

    The key id names the App and the purpose and does not change; the version
    selects the secret within it. Holding two versions is what makes a rotation
    survivable — nvoken mints the next version while still signing with the
    current one, and a signature a receiver cannot verify fails its delivery
    outright rather than retrying, so there is no forgiveness to lean on.

    ``version`` is an integer rather than the string it arrives as in
    configuration on purpose. A version that cannot be read as a positive
    integer makes the receiver refuse to be built, which is loud, instead of
    refusing live deliveries, which is permanent.
    """

    key_id: str
    version: int
    #: At least 32 bytes. A string is read as UTF-8.
    secret: bytes | str


class DeliveryKeyError(Exception):
    """Why a receiver would not accept a delivery's signing identity.

    ``retryable`` is the whole point of the distinction. An unconfigured
    receiver is an operator error this deployment may still fix inside nvoken's
    retry window. A configured receiver that does not know this key version is a
    real signing-identity failure, and asking for redelivery only reproduces it.
    """

    def __init__(self, reason: str, retryable: bool) -> None:
        super().__init__(f"delivery signing key {reason}")
        self.reason = reason
        self.retryable = retryable


def delivery_signing_keys(
    keys: Sequence[DeliverySigningKey],
) -> dict[tuple[str, int], bytes]:
    """Normalize a key table, refusing at build time what would otherwise be
    refused at delivery time.

    Two entries with the same key id and version are a configuration mistake
    rather than a redundancy: which secret wins would decide whether deliveries
    verify, and nothing in the pair says which was meant.
    """
    table: dict[tuple[str, int], bytes] = {}
    for key in keys:
        if not key.key_id:
            raise ValueError("delivery signing key is missing a key id")
        if not isinstance(key.version, int) or isinstance(key.version, bool) or key.version <= 0:
            raise ValueError(f"delivery signing key {key.key_id} has a non-positive version")
        secret = key.secret.encode() if isinstance(key.secret, str) else key.secret
        if len(secret) < 32:
            raise ValueError(
                f"delivery signing secret for {key.key_id} v{key.version} must be at least 32 bytes"
            )
        slot = (key.key_id, key.version)
        if slot in table:
            raise ValueError(f"delivery signing key {key.key_id} v{key.version} is configured twice")
        table[slot] = secret
    return table


def select_delivery_key(
    table: dict[tuple[str, int], bytes],
    headers: dict[str, str],
) -> bytes:
    """Pick the secret a delivery says it was signed with, before anything
    parses the body.

    Selection reads only the two identity headers, so a delivery signed by an
    identity this receiver does not hold is refused without its body ever being
    decoded, logged, or dispatched on.
    """
    if not table:
        raise DeliveryKeyError("not_configured", True)
    normalized = {name.lower(): value for name, value in headers.items()}
    key_id = normalized.get("x-nvoken-signing-key-id", "")
    try:
        version = int(normalized.get("x-nvoken-signing-key-version", ""))
    except ValueError as error:
        raise DeliveryKeyError("unknown_key", False) from error
    secret = table.get((key_id, version))
    if not key_id or version <= 0 or secret is None:
        raise DeliveryKeyError("unknown_key", False)
    return secret


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
