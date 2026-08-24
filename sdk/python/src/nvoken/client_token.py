from __future__ import annotations

import base64
import json
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone

#: The longest a client token may live. nvoken refuses anything longer, so this
#: is a ceiling rather than a suggestion.
#:
#: Short lifetimes are the whole safety story of handing a browser a bearer
#: token: the page refreshes from your backend on the schedule it already
#: refreshes its own session, and a leaked token is worth minutes.
CLIENT_TOKEN_LIFETIME_LIMIT = timedelta(minutes=15)

#: The required ``typ`` header.
#:
#: You sign these with a keypair you own and may sign other things with the
#: same one. Without a type, ``aud`` is the only structural difference between
#: a browser grant and any other EdDSA JWT you mint.
CLIENT_TOKEN_TYPE = "nvoken-client+jwt"

_AUDIENCE = "nvoken"
_MAX_CLAIM = 255

@dataclass
class ClientTokenClaims:
    """What a host asserts when it lets a browser talk to nvoken directly.

    Every field narrows what the browser can do. nvoken cannot second-guess a
    signed claim — it trusts what you assert, exactly as it trusts your API key
    — so the narrowing is yours to do, and :func:`mint_client_token` refuses a
    grant nvoken would refuse rather than handing you one that fails in a
    browser.
    """

    #: The App this token acts inside; becomes ``iss``.
    app_id: str
    #: The registered client key that verifies this token; becomes ``kid``.
    key_id: str
    #: Identifies the end user to nvoken. Opaque: nvoken stores it as the
    #: runtime user constraint and never resolves it to a person, so prefer an
    #: internal id over an email address.
    subject: str
    #: Required, and at most CLIENT_TOKEN_LIFETIME_LIMIT.
    lifetime: timedelta
    #: Scopes the token to one tenant. ``None`` means the App's default tenant.
    tenant_key: str | None = None
    #: Exactly one of ``agent_id`` or ``agent_key`` names the Agent.
    agent_id: str | None = None
    agent_key: str | None = None
    #: Pins the Agent Definition revision this token was minted against, so a
    #: deploy mid-session cannot change what the browser is talking to.
    definition_revision: int | None = None
    #: Confines the token to one Session. Leaving it unset lets the browser
    #: reach every Session belonging to this user and Agent, which is what a
    #: session-list UI needs and a single-conversation UI does not.
    session_id: str | None = None
    #: Defaults to the current time.
    issued_at: datetime | None = None


def mint_client_token(private_key: bytes, claims: ClientTokenClaims) -> str:
    """Sign a browser grant with the App's client key.

    Call it in backend code, never in a browser. The private key is the App's
    browser authority: a page holding it can mint any grant the ceiling allows,
    for any user, which is the failure this whole trust class exists to avoid.

    ``private_key`` is the 32-byte Ed25519 seed, exactly as
    ``nvoken client-key generate`` prints it — base64-decode it and pass the
    bytes.
    """
    sign = _ed25519_signer(private_key)
    _validate(claims)

    issued_at = claims.issued_at or datetime.now(timezone.utc)
    issued = int(issued_at.timestamp())
    header = _ordered_json(
        [("alg", "EdDSA"), ("typ", CLIENT_TOKEN_TYPE), ("kid", claims.key_id)]
    )
    members: list[tuple[str, object]] = [
        ("iss", claims.app_id),
        ("sub", claims.subject),
        ("aud", _AUDIENCE),
        ("iat", issued),
        ("exp", issued + int(claims.lifetime.total_seconds())),
    ]
    if claims.tenant_key is not None:
        members.append(("tenant_key", claims.tenant_key))
    if claims.agent_id is not None:
        members.append(("agent_id", claims.agent_id))
    if claims.agent_key is not None:
        members.append(("agent_key", claims.agent_key))
    if claims.definition_revision:
        members.append(("definition_revision", claims.definition_revision))
    if claims.session_id is not None:
        members.append(("session_id", claims.session_id))
    signing_input = f"{_base64url(header)}.{_base64url(_ordered_json(members))}"
    return f"{signing_input}.{_base64url(sign(signing_input.encode()))}"


def _ed25519_signer(private_key: bytes):
    if len(private_key) != 32:
        raise ValueError("nvoken: client key must be the 32-byte Ed25519 seed")
    try:
        from cryptography.hazmat.primitives.asymmetric.ed25519 import (
            Ed25519PrivateKey,
        )
    except ImportError as error:  # pragma: no cover - depends on the install
        raise ImportError(
            "nvoken: minting a client token needs Ed25519 signing, which the "
            "standard library does not provide. Install nvoken[client-tokens]."
        ) from error
    key = Ed25519PrivateKey.from_private_bytes(private_key)
    return key.sign


def _validate(claims: ClientTokenClaims) -> None:
    if not _valid_stable_id(claims.app_id, "app"):
        raise ValueError(f"nvoken: app_id {claims.app_id!r} is not an App id")
    if not _valid_stable_id(claims.key_id, "ckey"):
        raise ValueError(f"nvoken: key_id {claims.key_id!r} is not a client key id")
    if not _canonical(claims.subject):
        raise ValueError(
            "nvoken: subject is required, and must not be blank, padded, or over "
            "255 characters"
        )
    if claims.tenant_key is not None and not _canonical(claims.tenant_key):
        raise ValueError(
            "nvoken: tenant_key must not be blank, padded, or over 255 characters"
        )
    if (claims.agent_id is None) == (claims.agent_key is None):
        raise ValueError("nvoken: set exactly one of agent_id or agent_key")
    if claims.agent_id is not None and not _valid_stable_id(claims.agent_id, "agent"):
        raise ValueError(f"nvoken: agent_id {claims.agent_id!r} is not an Agent id")
    if claims.agent_key is not None and not _canonical(claims.agent_key):
        raise ValueError(
            "nvoken: agent_key must not be blank, padded, or over 255 characters"
        )
    if claims.definition_revision is not None and claims.definition_revision < 0:
        raise ValueError("nvoken: definition_revision must not be negative")
    if claims.session_id is not None and not _valid_stable_id(claims.session_id, "sess"):
        raise ValueError(f"nvoken: session_id {claims.session_id!r} is not a Session id")
    if (
        claims.lifetime <= timedelta(0)
        or claims.lifetime > CLIENT_TOKEN_LIFETIME_LIMIT
    ):
        raise ValueError(
            f"nvoken: lifetime must be positive and at most {CLIENT_TOKEN_LIFETIME_LIMIT}"
        )


def _canonical(value: str) -> bool:
    return bool(value) and value.strip() == value and len(value) <= _MAX_CLAIM


def _valid_stable_id(value: str, prefix: str) -> bool:
    return _canonical(value) and value.startswith(f"{prefix}_") and len(value) > len(prefix) + 1


def _ordered_json(members: list[tuple[str, object]]) -> bytes:
    """Write members in the order given rather than any order a dict implies.

    The published vector fixes that order so all four SDKs mint the same bytes
    for the same claims; a verifier parses JSON and does not care, but a
    byte-exact vector is only checkable if the order is decided somewhere.
    """
    body = ",".join(
        f"{json.dumps(name)}:{json.dumps(value, separators=(',', ':'))}"
        for name, value in members
    )
    return f"{{{body}}}".encode()


def _base64url(payload: bytes) -> str:
    return base64.urlsafe_b64encode(payload).decode().rstrip("=")
