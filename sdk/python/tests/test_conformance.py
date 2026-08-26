from nvoken import Client
from nvoken import Reducer, StreamEvent


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
        "turn_id": "turn_1", "attempt": 1, "message_id": "msg_1",
        "content_index": 0, "kind": "text", "delta": "hel",
    }))
    reducer.apply(StreamEvent("message.delta", {
        "turn_id": "turn_1", "attempt": 1, "message_id": "msg_1",
        "content_index": 0, "kind": "text", "delta": "lo",
    }))
    assert reducer.snapshot().previews[0].delta == "hello"
    reducer.apply(StreamEvent("stream.resync", {"turn_id": "turn_1"}))
    assert reducer.snapshot().previews == []
