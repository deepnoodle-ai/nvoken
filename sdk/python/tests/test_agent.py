from __future__ import annotations

from types import SimpleNamespace
from dataclasses import fields
import asyncio
import inspect
import httpx

import pytest

from nvoken import (
    Behavior,
    Client,
    ConversationById,
    ConversationByKey,
    ConversationRef,
    InlineMemory,
    Memory,
    OwnedBy,
    TurnAdmission,
    TurnAdmissionError,
    TurnExecutionError,
    TurnSnapshot,
    TurnTimeoutError,
    TurnUpdate,
)
from nvoken.facade import (
    AgentCollection,
    Conversation,
    InlineAgent,
    Turn,
    TurnOptions,
    _merge_narrow_limits,
)


@pytest.mark.asyncio
async def test_agent_lookup_is_explicit_and_awaited() -> None:
    resource = SimpleNamespace(id="agent_1", agent_key="analyst")

    class Agents:
        async def list_agents(self, owner, **kwargs):
            assert owner.value == "user"
            assert kwargs == {"tenant_key": "acme", "user_key": "alice", "agent_key": "analyst", "limit": 1}
            return SimpleNamespace(items=[resource])

    client = object.__new__(Client)
    client._raw = SimpleNamespace(agents=Agents())
    client._agents = AgentCollection(client)
    agent = await client.agent("analyst", owned_by=OwnedBy("acme", "alice"))
    assert agent.id == "agent_1"
    assert agent.key == "analyst"


@pytest.mark.asyncio
async def test_agent_create_generates_idempotency_key_when_omitted() -> None:
    resource = SimpleNamespace(id="agent_1", agent_key="analyst")

    class Agents:
        async def create_agent(self, idempotency_key, request):
            assert idempotency_key
            assert request.agent_key == "analyst"
            return resource

    client = object.__new__(Client)
    client._raw = SimpleNamespace(agents=Agents())
    client._agents = AgentCollection(client)
    created = await client.agents.create(
        "analyst", behavior=Behavior("Analyze", "openai/gpt-5"),
    )
    assert created.id == "agent_1"


def test_inline_request_keeps_actor_memory_conversation_and_behavior_independent() -> None:
    client = object.__new__(Client)
    client._conversation_locks = {}
    inline = InlineAgent(client, Behavior("Analyze listings", "openai/gpt-5"))
    request = inline._request(
        "Compare these homes",
        TurnOptions(
            tenant="acme",
            user="alice",
            memory=InlineMemory.tenant("portfolio"),
            conversation=ConversationRef.by_key("deal-42", owner="user"),
            limits={"max_iterations": 6},
            idempotency_key="turn-request-1",
        ),
    ).to_dict()
    assert request["behavior"]["kind"] == "inline"
    assert request["tenant_key"] == "acme"
    assert request["user_key"] == "alice"
    assert request["memory"] == {"scope": "tenant", "namespace": "portfolio"}
    assert request["conversation"]["owner"] == {"kind": "user", "user_key": "alice"}
    assert request["limits"]["max_iterations"] == 6


def test_behavior_and_agent_list_hide_raw_only_controls() -> None:
    assert {field.name for field in fields(Behavior)} == {
        "instructions", "model", "tools", "limits", "output_schema", "memory",
    }
    assert "limit" not in inspect.signature(AgentCollection.list).parameters


def test_bind_tools_is_an_immutable_handler_map() -> None:
    client = object.__new__(Client)
    client._conversation_locks = {}
    inline = InlineAgent(client, Behavior(
        "Help",
        "openai/gpt-5",
        tools=({
            "mode": "host", "name": "lookup", "description": "Find a record",
            "input_schema": {"type": "object"},
        },),
    ))
    bound = inline.bind_tools({"lookup": lambda value, context: value})
    assert not inline.tools
    assert set(bound.tools) == {"lookup"}
    request = bound._request("Find it", TurnOptions(tenant="acme")).to_dict()
    assert request["behavior"]["behavior"]["tools"][0]["mode"] == "host"
    with pytest.raises(ValueError, match="not declared"):
        inline.bind_tools({"other": lambda value, context: value})


def test_turn_recovery_is_local() -> None:
    client = object.__new__(Client)
    handle = client.turn("turn_123", tenant="acme", user="alice")
    assert (handle.id, handle.tenant, handle.user) == ("turn_123", "acme", "alice")


def test_inline_memory_requires_namespace_and_user_actor() -> None:
    client = object.__new__(Client)
    inline = InlineAgent(client, Behavior("Help", "openai/gpt-5"))
    with pytest.raises(ValueError, match="explicit namespace"):
        InlineMemory.tenant("")
    with pytest.raises(ValueError, match="Turn user"):
        inline._request(
            "Help", TurnOptions(tenant="acme", memory=InlineMemory.user("personal")),
        )


def test_conversation_selection_is_discriminated_and_keeps_create_options() -> None:
    by_id = ConversationRef.by_id("conv_123")
    assert isinstance(by_id, ConversationById)
    assert by_id.to_wire(None) == {"mode": "continue", "conversation_id": "conv_123"}

    by_key = ConversationRef.by_key(
        "case-42",
        owner="tenant",
        retention={"ttl_seconds": 3600},
        compaction={"trigger_tokens": {"kind": "fixed", "tokens": 4000}},
        metadata={"title": "Home search", "rank": 2},
    )
    assert isinstance(by_key, ConversationByKey)
    assert by_key.to_wire(None) == {
        "mode": "continue_or_create",
        "conversation_key": "case-42",
        "owner": {"kind": "tenant"},
        "retention": {"ttl_seconds": 3600},
        "compaction": {"trigger_tokens": {"kind": "fixed", "tokens": 4000}},
        "metadata": {"title": "Home search", "rank": 2},
    }
    assert "if_active" not in by_key.to_wire(None)


def test_conversation_limits_only_narrow_and_tenant_lock_ignores_actor() -> None:
    assert _merge_narrow_limits(
        {"max_iterations": 6, "max_output_tokens": 1000},
        {"max_iterations": 3},
    ) == {"max_iterations": 3, "max_output_tokens": 1000}
    with pytest.raises(ValueError, match="not widen"):
        _merge_narrow_limits({"max_iterations": 6}, {"max_iterations": 7})

    client = object.__new__(Client)
    client._conversation_locks = {}
    inline = InlineAgent(client, Behavior("Help", "openai/gpt-5"))
    tenant_ref = ConversationRef.by_key("shared", owner="tenant")
    alice = Conversation(inline, tenant_ref, "acme", user="alice")
    bob = Conversation(inline, tenant_ref, "acme", user="bob")
    assert alice.ref == bob.ref
    assert alice._lock_key() == bob._lock_key()


@pytest.mark.asyncio
async def test_turn_status_uses_result_snapshot_and_actor_assertions() -> None:
    source = SimpleNamespace(
        actual_instance=SimpleNamespace(
            kind="agent_revision", agent_id="agent_1", agent_revision_id="arev_1",
        ),
    )
    resource = SimpleNamespace(
        status="running", structured_output=None, behavior_source=source,
        conversation_id=None, memory_space_id=None, content_expires_at=None,
    )

    class Turns:
        async def get_turn_result(self, turn_id, **kwargs):
            assert turn_id == "turn_123"
            assert kwargs["_headers"] == {
                "X-Nvoken-Tenant-Key": "acme",
                "X-Nvoken-User-Key": "alice",
            }
            return SimpleNamespace(turn=resource, messages=["working"], output_text=None)

    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=Turns())
    snapshot = await Turn(client, "turn_123", tenant="acme", user="alice").status()
    assert snapshot.status == "running"
    assert snapshot.messages == ("working",)
    assert snapshot.text is None
    assert snapshot.agent_id == "agent_1"
    assert not hasattr(snapshot, "resource")


@pytest.mark.asyncio
async def test_missing_tool_handler_leaves_waiting_turn_unmodified() -> None:
    call = SimpleNamespace(id="call_1", name="lookup", mode="host", arguments={"id": 1})
    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=SimpleNamespace())
    turn = Turn(client, "turn_123", tenant="acme")
    assert await turn._run_host_tools(SimpleNamespace(tool_calls=[call])) is False


@pytest.mark.asyncio
async def test_tool_replay_dedupes_known_calls_and_passes_context() -> None:
    submissions = []
    contexts = []

    class Turns:
        async def submit_host_tool_results(self, turn_id, request, **kwargs):
            submissions.append(request.to_dict())

    async def lookup(arguments, context):
        contexts.append((arguments, context.turn_id, context.tool_call_id))
        return {"ok": True}

    known = SimpleNamespace(id="call_1", name="lookup", mode="host", arguments={"id": 1})
    unknown = SimpleNamespace(id="call_2", name="other", mode="host", arguments={"id": 2})
    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=Turns())
    turn = Turn(client, "turn_123", tenant="acme", tools={"lookup": lookup})
    resource = SimpleNamespace(tool_calls=[known, unknown])
    assert await turn._run_host_tools(resource) is True
    assert await turn._run_host_tools(resource) is False
    assert contexts == [({"id": 1}, "turn_123", "call_1")]
    assert len(submissions) == 1
    assert len(submissions[0]["results"]) == 1


@pytest.mark.asyncio
async def test_failed_tool_submission_clears_local_replay_guard() -> None:
    calls = 0

    class Turns:
        attempts = 0

        async def submit_host_tool_results(self, turn_id, request, **kwargs):
            self.attempts += 1
            if self.attempts == 1:
                from nvoken_generated.exceptions import ApiException
                raise ApiException(status=503, reason="try again")

    async def lookup(arguments, context):
        nonlocal calls
        calls += 1
        return {"ok": True}

    call = SimpleNamespace(id="call_1", name="lookup", mode="host", arguments={})
    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=Turns())
    turn = Turn(client, "turn_123", tenant="acme", tools={"lookup": lookup})
    with pytest.raises(Exception):
        await turn._run_host_tools(SimpleNamespace(tool_calls=[call]))
    assert await turn._run_host_tools(SimpleNamespace(tool_calls=[call])) is True
    assert calls == 2


@pytest.mark.asyncio
async def test_cancelled_tool_handler_clears_local_replay_guard() -> None:
    entered = asyncio.Event()

    async def lookup(arguments, context):
        assert context.cancelled is False
        entered.set()
        await asyncio.Event().wait()

    call = SimpleNamespace(id="call_1", name="lookup", mode="host", arguments={})
    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=SimpleNamespace())
    turn = Turn(client, "turn_123", tenant="acme", tools={"lookup": lookup})
    task = asyncio.create_task(turn._run_host_tools(SimpleNamespace(tool_calls=[call])))
    await entered.wait()
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task
    assert "call_1" not in turn._handled_tool_call_ids


@pytest.mark.asyncio
async def test_uncertain_admission_error_keeps_generated_idempotency_key() -> None:
    class Turns:
        async def create_turn(self, request):
            raise httpx.ConnectError("connection dropped", request=httpx.Request("POST", "http://x"))

    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=Turns())
    inline = InlineAgent(client, Behavior("Help", "openai/gpt-5"))
    with pytest.raises(TurnAdmissionError) as error:
        await inline.start("hello", tenant="acme")
    assert error.value.category == "transport"
    assert error.value.idempotency_key


@pytest.mark.asyncio
async def test_admission_timeout_keeps_generated_idempotency_key() -> None:
    class Turns:
        async def create_turn(self, request, **kwargs):
            await asyncio.Event().wait()

    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=Turns())
    inline = InlineAgent(client, Behavior("Help", "openai/gpt-5"))
    with pytest.raises(TurnTimeoutError) as error:
        await inline.start("hello", tenant="acme", timeout=0.001)
    assert error.value.turn is None
    assert error.value.idempotency_key


@pytest.mark.asyncio
async def test_turn_stream_carries_recovery_assertions() -> None:
    from nvoken.stream import Reducer, _read_stream

    class Response:
        is_error = False
        status = 200

        async def aiter_lines(self):
            yield "event: message.delta"
            yield 'data: {"turn_id":"turn_123","attempt":1,"message_id":"msg_1","content_index":0,"kind":"text","delta":"hi"}'
            yield ""

        async def aclose(self):
            pass

    class Turns:
        async def stream_turn_without_preload_content(self, turn_id, **kwargs):
            assert turn_id == "turn_123"
            assert kwargs["_headers"] == {
                "X-Nvoken-Tenant-Key": "acme",
                "X-Nvoken-User-Key": "alice",
            }
            return Response()

    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=Turns())
    stream = _read_stream(
        client,
        conversation_id=None,
        turn_id="turn_123",
        reducer=Reducer(),
        deltas=True,
        access_headers={
            "X-Nvoken-Tenant-Key": "acme",
            "X-Nvoken-User-Key": "alice",
        },
    )
    event = await anext(stream)
    assert event.data["turn_id"] == "turn_123"
    await stream.aclose()


@pytest.mark.asyncio
async def test_turn_stream_reconnects_after_transport_failure(monkeypatch) -> None:
    from nvoken import stream as stream_module
    from nvoken.stream import Reducer, _read_stream

    async def no_sleep(_seconds):
        return None

    monkeypatch.setattr(stream_module.asyncio, "sleep", no_sleep)

    class Response:
        is_error = False
        status = 200

        async def aiter_lines(self):
            yield "id: cursor-1"
            yield "event: message.delta"
            yield 'data: {"turn_id":"turn_123","attempt":1,"message_id":"msg_1","content_index":0,"kind":"text","delta":"hi"}'
            yield ""

        async def aclose(self):
            pass

    class Turns:
        calls = 0

        async def stream_turn_without_preload_content(self, turn_id, **kwargs):
            self.calls += 1
            if self.calls == 1:
                raise httpx.ReadError(
                    "connection dropped",
                    request=httpx.Request("GET", "http://x"),
                )
            return Response()

    turns = Turns()
    client = object.__new__(Client)
    client._raw = SimpleNamespace(turns=turns)
    stream = _read_stream(
        client,
        conversation_id=None,
        turn_id="turn_123",
        reducer=Reducer(),
        deltas=True,
    )
    event = await anext(stream)
    assert event.id == "cursor-1"
    assert turns.calls == 2
    await stream.aclose()


def test_turn_update_exposes_snapshot_frame_and_cursor_without_raw_resource() -> None:
    snapshot = TurnSnapshot(
        status="running", messages=(), text=None, structured_output=None,
        behavior_source="inline", agent_id=None, agent_revision_id=None,
        memory_space_id=None, conversation_id=None, content_expires_at=None,
    )
    update = TurnUpdate(snapshot=snapshot, frame={"type": "message.delta"}, cursor="cursor-1")
    assert update.snapshot is snapshot
    assert update.cursor == "cursor-1"
    assert not hasattr(update.snapshot, "resource")


@pytest.mark.asyncio
async def test_conversation_run_holds_lock_through_terminal_result() -> None:
    client = object.__new__(Client)
    client._conversation_locks = {}
    release = asyncio.Event()
    starts = 0

    class FakeTurn:
        async def result(self, **kwargs):
            await release.wait()
            return SimpleNamespace(text="done")

    class Runnable:
        def __init__(self):
            self.client = client

        async def start(self, *args, **kwargs):
            nonlocal starts
            starts += 1
            return FakeTurn()

    conversation = Conversation(
        Runnable(), ConversationRef.by_key("shared", owner="tenant"), "acme",
    )
    first = asyncio.create_task(conversation.run("first"))
    await asyncio.sleep(0)
    second = asyncio.create_task(conversation.run("second"))
    await asyncio.sleep(0)
    assert starts == 1
    release.set()
    await asyncio.gather(first, second)
    assert starts == 2


@pytest.mark.asyncio
async def test_turn_result_raises_typed_execution_and_timeout_errors() -> None:
    client = object.__new__(Client)
    failed_resource = SimpleNamespace(
        status="failed", structured_output=None, conversation_id=None,
        memory_space_id=None, tool_calls=[],
    )
    turn = Turn(
        client,
        "turn_failed",
        tenant="acme",
        admission=TurnAdmission("idem-1", False),
    )

    async def failed_status():
        return TurnSnapshot(
            status="failed", messages=(), text=None, structured_output=None,
            behavior_source="inline", agent_id=None, agent_revision_id=None,
            memory_space_id=None, conversation_id=None, content_expires_at=None,
        )

    turn.status = failed_status
    with pytest.raises(TurnExecutionError) as execution:
        await turn.result()
    assert execution.value.result.turn is turn
    assert execution.value.result.admission.idempotency_key == "idem-1"

    running_resource = SimpleNamespace(
        status="running", structured_output=None, conversation_id=None,
        memory_space_id=None, tool_calls=[],
    )
    waiting = Turn(
        client,
        "turn_running",
        tenant="acme",
        admission=TurnAdmission("idem-2", False),
    )

    async def running_status():
        await asyncio.Event().wait()

    waiting.status = running_status
    with pytest.raises(TurnTimeoutError) as timeout:
        await waiting.result(timeout=0.001)
    assert timeout.value.turn is waiting
    assert timeout.value.idempotency_key == "idem-2"
