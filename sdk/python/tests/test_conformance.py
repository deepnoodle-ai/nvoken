import os

import pytest

from nvoken import Client
from nvoken import Reducer, StreamEvent


@pytest.mark.asyncio
async def test_shared_conformance_server_uses_current_model_contract() -> None:
    base_url = os.getenv("NVOKEN_CONFORMANCE_URL")
    if not base_url:
        pytest.skip("NVOKEN_CONFORMANCE_URL is not set")
    models = await Client("conformance", base_url=base_url).list_models(
        provider="future_provider",
    )
    assert len(models.items) == 1
    assert models.items[0].provider == "future_provider"
    assert models.items[0].id == "future-model"


def test_raw_client_exposes_exact_runtime_collections() -> None:
    client = Client("test", base_url="http://localhost")
    assert client.raw.agents
    assert client.raw.conversations
    assert client.raw.memory_spaces
    assert client.raw.turns


def test_removed_runtime_nouns_are_not_exported() -> None:
    import nvoken

    for name in ("AgentDefinition", "Session", "Invocation", "InvocationHandle"):
        assert not hasattr(nvoken, name)


def test_reducer_previews_are_keyed_by_turn_and_resync_scope() -> None:
    reducer = Reducer()
    reducer.apply(StreamEvent("message.delta", {
        "turn_id": "turn_01kc514000e008000000000001", "attempt": 1, "message_id": "msg_01kc514000e008000000000001",
        "content_index": 0, "kind": "text", "delta": "hel",
    }))
    reducer.apply(StreamEvent("message.delta", {
        "turn_id": "turn_01kc514000e008000000000001", "attempt": 1, "message_id": "msg_01kc514000e008000000000001",
        "content_index": 0, "kind": "text", "delta": "lo",
    }))
    assert reducer.snapshot().previews[0].delta == "hello"
    reducer.apply(StreamEvent("stream.resync", {"turn_id": "turn_01kc514000e008000000000001"}))
    assert reducer.snapshot().previews == []
