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
    InvokeRequest,
    Model,
    NvokenError,
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


@pytest.mark.asyncio
async def test_shared_fault_server_semantics() -> None:
    base_url = os.getenv("NVOKEN_CONFORMANCE_URL")
    if not base_url:
        pytest.skip("NVOKEN_CONFORMANCE_URL is not set")
    async with httpx.AsyncClient() as setup:
        await setup.post(f"{base_url}/__test/reset")

    async with Client(
        base_url,
        "test-key",
        retry=RetryPolicy(maximum_attempts=3, minimum_delay=0.001, maximum_delay=0.005),
    ) as client:
        handle = await client.invoke(InvokeRequest(
            agent_ref="support",
            idempotency_key="python-lost-ack",
            input="hello",
            spec=ExecutionSpec(
                instructions="help",
                model=Model(provider="openai", name="gpt-test"),
            ),
        ))
        assert handle.invocation_id == INVOCATION_ID
        assert handle.session_id == SESSION_ID

        resumed = await client.resume(INVOCATION_ID)
        assert resumed.status == "completed"

        waiting = await client.resume(WAIT_ID)
        with pytest.raises(NvokenError) as timeout:
            await asyncio.wait_for(
                waiting.wait(minimum_delay=0.001, maximum_delay=0.002),
                timeout=0.01,
            )
        assert timeout.value.category == "timeout"

        first_page = await client.list_invocations()
        assert first_page.has_more is True
        assert first_page.next_cursor == "invocations-page-2"
        second_page = await client.list_invocations(cursor=first_page.next_cursor)
        assert second_page.has_more is False
        messages = await client.list_messages(SESSION_ID)
        assert messages.next_cursor == "messages-page-2"

        composed = await handle.result()
        assert composed.invocation.id == INVOCATION_ID
        assert composed.invocation.status == "completed"
        assert composed.invocation.structured_output == {"answer": "world"}
        assert composed.invocation.structured_output_provenance.source == "tool_call"
        assert [message.role for message in composed.messages] == ["user", "assistant"]
        assert composed.output_text == "world"
        assert await handle.text() == composed.output_text
        assert len(await handle.list_messages()) == 2

        accepted = await handle.submit_tool_results([
            ToolResult(tool_call_id=TOOL_CALL_ID, content={"ok": True}),
        ])
        assert accepted.results[0].deduplicated is True
        assert (await handle.cancel()).status == "cancelled"

        with pytest.raises(NvokenError) as conflict:
            await client.get("conflict")
        assert conflict.value.category == "conflict"
        assert conflict.value.status == 409
        assert conflict.value.request_id
        assert (await client.get("rate-limit")).status == "completed"
        with pytest.raises(NvokenError) as rate_limited:
            await client.get("rate-limit-always")
        assert rate_limited.value.category == "rate_limit"
        assert rate_limited.value.status == 429
        assert rate_limited.value.retry_after == 1
        with pytest.raises(NvokenError) as unavailable:
            await client.get("server-error")
        assert unavailable.value.category == "server"
        assert unavailable.value.status == 503

        reduced: Any = None

        async def consume(_event: Any, snapshot: Any) -> None:
            nonlocal reduced
            reduced = snapshot

        await (await client.resume(INVOCATION_ID)).stream(consume)
        assert len(reduced.messages) == 2
        assert len(reduced.invocation_changes) == 2
        assert reduced.resume_cursor == "cursor-2"

    async with httpx.AsyncClient() as inspect:
        state = (await inspect.get(f"{base_url}/__test/state")).json()
    assert state == {
        "admission_attempts": 2,
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
