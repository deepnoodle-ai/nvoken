from __future__ import annotations

import asyncio
import json
import os
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import httpx
import pytest

from nvoken import (
    Client,
    ExecutionSpec,
    InvocationHandle,
    InvokeRequest,
    Model,
    NvokenError,
    ProviderCredentialSelection,
    RetryPolicy,
    Reducer,
    StreamEvent,
    ToolResult,
    deduplicate_callback_result,
    verify_callback,
)

INVOCATION_ID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
SESSION_ID = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
TOOL_CALL_ID = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
WAIT_ID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328"
EXACT_MODEL_ID = "experimental/model?variant=雪%#1"


@pytest.mark.asyncio
async def test_shared_fault_server_semantics() -> None:
    base_url = os.getenv("NVOKEN_CONFORMANCE_URL")
    if not base_url:
        pytest.skip("NVOKEN_CONFORMANCE_URL is not set")
    result_fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/invocation-result.json").read_text()
    )
    expected_output_text = result_fixture["message_join"]["expected_output_text"]
    async with httpx.AsyncClient() as setup:
        await setup.post(f"{base_url}/__test/reset")

    async with Client(
        base_url,
        "test-key",
        retry=RetryPolicy(max_attempts=3, min_delay=0.001, max_delay=0.005),
    ) as client:
        models = await client.list_models()
        assert models.catalog_version == "conformance-catalog-v1"
        assert next(model for model in models.items if model.id == "future-model").provider == \
            "future_provider"
        exact_model = await client.get_model(Model(provider="openai", id=EXACT_MODEL_ID))
        assert exact_model.id == EXACT_MODEL_ID
        assert exact_model.cataloged is False
        assert exact_model.pricing.status == "unpriced"

        handle = await client.invoke(InvokeRequest(
            agent_key="support",
            idempotency_key="python-lost-ack",
            input="hello",
            spec=ExecutionSpec(
                instructions="help",
                model=Model(provider="openai", id="gpt-test"),
            ),
            provider_credentials=(
                ProviderCredentialSelection(
                    provider="openai",
                    source="caller_ephemeral",
                    api_key="conformance-secret",
                ),
            ),
        ))
        assert handle.invocation_id == INVOCATION_ID
        assert handle.session_id == SESSION_ID

        resumed = client.invocation(INVOCATION_ID)
        await resumed.refresh()
        assert resumed.status == "completed"

        waiting = client.invocation(WAIT_ID)
        with pytest.raises(TimeoutError) as timeout:
            await asyncio.wait_for(
                waiting.wait(min_poll_interval=0.001, max_poll_interval=0.002),
                timeout=0.01,
            )
        assert not isinstance(timeout.value, NvokenError)

        first_page = await client.list_invocations()
        assert first_page.has_more is True
        assert first_page.next_cursor == "invocations-page-2"
        second_page = await client.list_invocations(cursor=first_page.next_cursor)
        assert second_page.has_more is False
        messages = await client.list_session_messages(SESSION_ID)
        assert messages.next_cursor == "messages-page-2"

        composed = await handle.result()
        assert composed.invocation.id == INVOCATION_ID
        assert composed.invocation.status == "completed"
        assert composed.invocation.structured_output == {"answer": "world"}
        assert composed.invocation.structured_output_provenance.source == "tool_call"
        assert [message.role for message in composed.messages] == [
            "user",
            "assistant",
            "assistant",
        ]
        assert composed.output_text == expected_output_text
        assert await handle.output_text() == composed.output_text
        assert len(await handle.list_messages()) == 3

        accepted = await handle.submit_tool_results([
            ToolResult(tool_call_id=TOOL_CALL_ID, content={"ok": True}),
        ])
        assert accepted.results[0].deduplicated is True
        assert (await handle.cancel()).status == "cancelled"

        with pytest.raises(NvokenError) as conflict:
            await client.get_invocation("conflict")
        assert conflict.value.category == "conflict"
        assert conflict.value.status == 409
        assert conflict.value.request_id
        with pytest.raises(NvokenError) as unauthenticated:
            await client.get_invocation("unauthenticated")
        assert unauthenticated.value.category == "authentication"
        assert unauthenticated.value.status == 401
        with pytest.raises(NvokenError) as forbidden:
            await client.get_invocation("forbidden")
        assert forbidden.value.category == "permission"
        assert forbidden.value.status == 403
        assert (await client.get_invocation("rate-limit")).status == "completed"
        with pytest.raises(NvokenError) as rate_limited:
            await client.get_invocation("rate-limit-always")
        assert rate_limited.value.category == "rate_limit"
        assert rate_limited.value.status == 429
        assert rate_limited.value.retry_after == 1
        with pytest.raises(NvokenError) as unavailable:
            await client.get_invocation("server-error")
        assert unavailable.value.category == "server"
        assert unavailable.value.status == 503

        event_types: list[str] = []

        async def consume(event: StreamEvent) -> None:
            event_types.append(event.type)

        await client.invocation(INVOCATION_ID).stream(consume)
        assert event_types == [
            "invocation.update",
            "stream.end",
            "invocation.update",
            "invocation.result",
        ]

    async with httpx.AsyncClient() as inspect:
        state = (await inspect.get(f"{base_url}/__test/state")).json()
    assert state == {
        "admission_attempts": 2,
        "credential_admissions": 2,
        "result_attempts": 2,
        "cancel_attempts": 1,
        "stream_attempts": 3,
        "last_event_id": "cursor-1",
    }


@pytest.mark.asyncio
async def test_shared_callback_signing_and_deduplication_vector() -> None:
    path = Path(__file__).parents[3] / "docs/design/callback-signing-v1.json"
    vector = json.loads(path.read_text())
    key = vector["key"].encode()
    body = vector["body"].encode()
    now = datetime.fromtimestamp(vector["now"], timezone.utc)
    verified = verify_callback(key, vector["headers"], body, now=now)
    assert verified.tool_call_id == TOOL_CALL_ID

    mutations = []
    mutations.append((dict(vector["headers"]), body + b" "))
    timestamp = dict(vector["headers"])
    timestamp["X-Nvoken-Timestamp"] = "1784635801"
    mutations.append((timestamp, body))
    delivery = dict(vector["headers"])
    delivery["X-Nvoken-Delivery-ID"] = "different"
    mutations.append((delivery, body))
    signature = dict(vector["headers"])
    signature["X-Nvoken-Signature"] = "sha256=00"
    mutations.append((signature, body))
    for headers, candidate in mutations:
        with pytest.raises(ValueError):
            verify_callback(key, headers, candidate, now=now)

    class Store:
        value: dict[str, bool] | None = None

        async def put_if_absent(
            self,
            _identity: str,
            result: dict[str, bool],
        ) -> tuple[dict[str, bool], bool]:
            if self.value is not None:
                return self.value, False
            self.value = result
            return result, True

    store = Store()
    _, replayed = await deduplicate_callback_result(store, TOOL_CALL_ID, {"ok": True})
    assert replayed is False
    stored, replayed = await deduplicate_callback_result(store, TOOL_CALL_ID, {"ok": False})
    assert replayed is True
    assert stored == {"ok": True}


def test_shared_reducer_vector() -> None:
    path = Path(__file__).parents[2] / "conformance/fixtures/reducer.json"
    fixture = json.loads(path.read_text())
    reducer = Reducer()
    for event in fixture["events"]:
        reducer.apply(StreamEvent(
            id=event["id"],
            type=event["event"],
            data=event["data"],
        ))
    snapshot = reducer.snapshot()
    assert [message.sequence for message in snapshot.messages] == fixture["expected"]["message_sequences"]
    assert [change.revision for change in snapshot.invocation_changes] == fixture["expected"]["invocation_revisions"]
    assert snapshot.resume_cursor == fixture["expected"]["resume_cursor"]
    assert snapshot.previews == fixture["expected"]["previews"]
    for preview_case in fixture["preview_cases"]:
        preview_reducer = Reducer()
        for event in preview_case["events"]:
            preview_reducer.apply(StreamEvent(
                id=event["id"],
                type=event["event"],
                data=event["data"],
            ))
        assert [
            {
                "invocation_id": preview.invocation_id,
                "attempt": preview.attempt,
                "iteration": preview.iteration,
                "content_index": preview.content_index,
                "output_text": preview.output_text,
                "thinking": preview.thinking,
            }
            for preview in preview_reducer.snapshot().previews
        ] == preview_case["expected_previews"], preview_case["name"]


@pytest.mark.asyncio
async def test_cancellation_propagates_through_replay_and_waits() -> None:
    async def assert_cancelled(awaitable: Any) -> None:
        task = asyncio.create_task(awaitable)
        await asyncio.sleep(0)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task

    async with Client("http://nvoken.test", "test-key") as client:
        blocked = asyncio.Event()
        await assert_cancelled(client._replay_safe(blocked.wait))

    class BlockingClient:
        async def get_invocation(self, _invocation_id: str) -> Any:
            await asyncio.Event().wait()

    await assert_cancelled(
        InvocationHandle(BlockingClient(), INVOCATION_ID).wait()
    )
    await assert_cancelled(
        InvocationHandle(BlockingClient(), INVOCATION_ID).wait_for_action()
    )


@pytest.mark.asyncio
async def test_session_stream_uses_public_operation_and_follows_later_turns() -> None:
    path = Path(__file__).parents[2] / "conformance/fixtures/reducer.json"
    events = json.loads(path.read_text())["events"]
    later_invocation_id = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb399"
    later_event = json.loads(json.dumps(events[1]))
    for message in later_event["data"]["messages"]:
        message["invocation_id"] = later_invocation_id
    for change in later_event["data"]["invocation_changes"]:
        change["invocation_id"] = later_invocation_id

    def sse(event: dict[str, Any], *, terminal: bool = False) -> str:
        frame = (
            "retry: 1\n"
            f"id: {event['id']}\n"
            f"event: {event['event']}\n"
            f"data: {json.dumps(event['data'])}\n\n"
        )
        if terminal:
            frame += (
                "event: stream.end\n"
                f"data: {json.dumps({'type': 'stream.end', 'session_id': SESSION_ID, 'invocation_id': None, 'reason': 'terminal', 'resume_cursor': event['id']})}\n\n"
            )
        return frame

    class StreamOperations:
        def __init__(self) -> None:
            self.calls: list[tuple[str, str | None]] = []
            self.responses = [
                httpx.Response(200, text=sse(events[0], terminal=True)),
                httpx.Response(200, text=sse(later_event)),
            ]

        async def stream_session_transcript_without_preload_content(
            self,
            session_id: str,
            *,
            cursor: str | None,
            last_event_id: str | None,
        ) -> httpx.Response:
            assert cursor is None
            self.calls.append((session_id, last_event_id))
            return self.responses.pop(0)

    operations = StreamOperations()

    class StreamClient:
        stream_sessions = operations

    seen_updates = 0

    async def consume(event: StreamEvent, _snapshot: Any) -> None:
        nonlocal seen_updates
        if event.type == "transcript.update":
            seen_updates += 1
        if seen_updates == 2:
            raise asyncio.CancelledError

    reducer = Reducer()
    with pytest.raises(asyncio.CancelledError):
        from nvoken import stream_session
        await stream_session(StreamClient(), SESSION_ID, reducer, consume)

    assert operations.calls == [
        (SESSION_ID, None),
        (SESSION_ID, "cursor-1"),
    ]
    assert reducer.snapshot().resume_cursor == "cursor-2"
    assert later_invocation_id in {
        change.invocation_id for change in reducer.snapshot().invocation_changes
    }


@pytest.mark.asyncio
async def test_invoke_maps_ephemeral_and_stored_provider_credentials() -> None:
    async with Client("http://nvoken.test", "test-key") as client:
        captured: list[Any] = []

        async def create(body: Any) -> Any:
            captured.append(body)
            return type("Ack", (), {
                "invocation_id": INVOCATION_ID,
                "session_id": SESSION_ID,
                "agent_id": "agnt_test",
                "status": "queued",
                "deduplicated": False,
                "deadline_at": None,
            })()

        client.invocations.create_invocation = create
        base = {
            "agent_key": "support",
            "input": "hello",
            "spec": ExecutionSpec(model=Model(provider="openai", id="gpt-test")),
        }
        await client.invoke(InvokeRequest(
            **base,
            provider_credentials=(
                ProviderCredentialSelection(
                    provider="openai",
                    source="caller_ephemeral",
                    api_key="secret",
                ),
            ),
        ))
        await client.invoke(InvokeRequest(
            **base,
            provider_credentials=(
                ProviderCredentialSelection(
                    provider="openai",
                    source="account_byok",
                ),
            ),
        ))

    assert captured[0].provider_credentials[0].to_dict() == {
        "provider": "openai",
        "source": "caller_ephemeral",
        "credential": {"api_key": "secret"},
    }
    assert captured[1].provider_credentials[0].to_dict() == {
        "provider": "openai",
        "source": "account_byok",
    }
