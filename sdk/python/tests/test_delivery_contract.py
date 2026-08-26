import base64
import json
from datetime import datetime, timedelta, timezone
from pathlib import Path

from nvoken import (
    ClientTokenClaims,
    ClientTokenConversationAccess,
    ClientTokenMemoryAccess,
    mint_client_token,
    verify_callback,
    verify_webhook,
)


def _delivery_vector() -> dict:
    path = Path(__file__).parents[3] / "docs/design/delivery-signing-v1.json"
    return json.loads(path.read_text())["vectors"]


def _headers(vector: dict) -> dict[str, str]:
    return {name: str(value) for name, value in vector["headers"].items()}


def test_signed_delivery_v2_exposes_turn_facts() -> None:
    vectors = _delivery_vector()
    now = datetime.fromtimestamp(1784635200, timezone.utc)
    callback = vectors["callback"]
    verified_callback = verify_callback(
        b"0123456789abcdef0123456789abcdef",
        _headers(callback),
        callback["body"].encode(),
        now=now,
    )
    assert verified_callback.turn_id.startswith("turn_")
    assert verified_callback.conversation_id.startswith("conv_")
    assert verified_callback.memory_space_id.startswith("mspc_")
    assert verified_callback.behavior_source["kind"] == "agent_revision"
    assert verified_callback.tenant_key == "acme"
    assert verified_callback.user_key == "alice"

    webhook = vectors["webhook"]
    verified_webhook = verify_webhook(
        b"0123456789abcdef0123456789abcdef",
        _headers(webhook),
        webhook["body"].encode(),
        now=now,
    )
    assert verified_webhook.event == "turn.ended"
    assert verified_webhook.turn_id == verified_callback.turn_id
    assert verified_webhook.conversation_id == verified_callback.conversation_id


def test_client_token_matches_published_v2_vector() -> None:
    path = Path(__file__).parents[3] / "docs/design/client-token-v2.json"
    vector = json.loads(path.read_text())
    claims = vector["claims"]
    token = mint_client_token(
        base64.b64decode(vector["signing_key"]["private_key_seed"]),
        ClientTokenClaims(
            app_id=claims["iss"],
            key_id=vector["signing_key"]["key_id"],
            subject=claims["sub"],
            tenant_key=claims["tenant_key"],
            agent_id=claims["agent_id"],
            agent_revision_id=claims["agent_revision_id"],
            memory_access=ClientTokenMemoryAccess.user(
                claims["memory_access"]["namespace"]
            ),
            conversation_access=ClientTokenConversationAccess.exact(
                claims["conversation_access"]["conversation_id"]
            ),
            issued_at=datetime.fromtimestamp(claims["iat"], timezone.utc),
            lifetime=timedelta(seconds=claims["exp"] - claims["iat"]),
        ),
    )
    assert token == vector["token"]
