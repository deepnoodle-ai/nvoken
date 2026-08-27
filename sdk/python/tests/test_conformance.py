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
        "turn_id": "476dd7be-97a1-78f3-8096-d7032468a80a", "attempt": 1, "message_id": "102002f7-649e-7a77-85c2-7f1695adb24e",
        "content_index": 0, "kind": "text", "delta": "hel",
    }))
    reducer.apply(StreamEvent("message.delta", {
        "turn_id": "476dd7be-97a1-78f3-8096-d7032468a80a", "attempt": 1, "message_id": "102002f7-649e-7a77-85c2-7f1695adb24e",
        "content_index": 0, "kind": "text", "delta": "lo",
    }))
    assert reducer.snapshot().previews[0].delta == "hello"
    reducer.apply(StreamEvent("stream.resync", {"turn_id": "476dd7be-97a1-78f3-8096-d7032468a80a"}))
    assert reducer.snapshot().previews == []


def test_hand_written_facades_forward_only_what_the_generated_client_accepts() -> None:
    """The facade is hand-written and the client under it is regenerated.

    A parameter the contract drops disappears from the generated signature and
    stays in the facade, where nothing notices until a caller gets a TypeError
    from a method that used to work. `default_tenant` outlived its endpoint in
    exactly that way, in three methods at once, with no test that called any of
    them.
    """
    import inspect

    from nvoken import Client

    client = Client("test", base_url="http://localhost")
    forwarding = [
        (client.list_credit_accounts, client.raw.credits.list_credit_accounts),
        (client.list_credit_allocations, client.raw.credits.list_credit_allocations),
        (client.list_models, client.raw.models.list_models),
    ]
    for facade, generated in forwarding:
        accepted = set(inspect.signature(generated).parameters)
        forwarded = set(inspect.signature(facade).parameters) - {"self"}
        assert forwarded <= accepted, (
            f"{facade.__qualname__} takes {sorted(forwarded - accepted)}, "
            f"which {generated.__qualname__} no longer accepts"
        )


def test_credit_allocation_sends_only_fields_the_request_model_declares() -> None:
    """`allocate_credits` builds a request dict rather than forwarding kwargs.

    A removed field survives there silently, because `from_dict` ignores what
    the model does not declare — so the request is built, sent, and refused by
    the service instead of failing here.
    """
    from nvoken_generated.models.allocate_credits_request import (
        AllocateCreditsRequest,
    )

    sent = {
        "tenant_key": "acme",
        "amount": {"amount": "25.000000", "currency": "USD"},
        "reference": None,
        "idempotency_key": "credits-conformance-1",
    }
    declared = set(AllocateCreditsRequest.model_fields)
    assert set(sent) <= declared, sorted(set(sent) - declared)
    assert AllocateCreditsRequest.from_dict(sent).tenant_key == "acme"
