from __future__ import annotations

import asyncio
from dataclasses import dataclass
from types import SimpleNamespace
from typing import Any, AsyncIterator

import pytest

from nvoken import (
    TERMINAL_INVOCATION_STATUSES,
    Agent,
    AgentOptions,
    InvocationOptions,
    MissingToolHandlerError,
    NoOutputTextError,
    NvokenError,
    StreamEvent,
    Tool,
)

INVOCATION_ID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
SESSION_ID = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
TOOL_CALL_ID = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"


class FakeHandle:
    def __init__(
        self,
        *,
        waiting_tool: str | None = None,
        output_text: str | None = "hello",
        structured_output: dict[str, Any] | None = None,
    ) -> None:
        self.invocation_id = INVOCATION_ID
        self.session_id = SESSION_ID
        self.agent_id = "agent_test"
        self.status = "queued"
        self.waiting_tool = waiting_tool
        self.output_text_value = output_text
        self.structured_output = structured_output
        self.submissions: list[Any] = []
        self.cancelled = False

    async def refresh(self) -> Any:
        if self.waiting_tool and not self.submissions and not self.cancelled:
            self.status = "waiting"
            pending = [
                SimpleNamespace(
                    id=TOOL_CALL_ID,
                    name=self.waiting_tool,
                    mode="host",
                    status="pending",
                    arguments={"city": "Paris"},
                )
            ]
        else:
            self.status = "cancelled" if self.cancelled else "completed"
            pending = None
        return SimpleNamespace(
            id=self.invocation_id,
            status=self.status,
            tool_calls=pending,
            error=None,
        )

    async def wait_for_action(self, **_: Any) -> Any:
        return await self.refresh()

    async def result(self) -> Any:
        invocation = SimpleNamespace(
            id=self.invocation_id,
            session_id=self.session_id,
            agent_id=self.agent_id,
            status="completed",
            structured_output=self.structured_output,
        )
        return SimpleNamespace(
            invocation=invocation,
            messages=[],
            output_text=self.output_text_value,
        )

    async def submit_tool_results(self, results: list[Any]) -> Any:
        self.submissions.extend(results)
        return SimpleNamespace(status="queued")

    async def cancel(self) -> Any:
        self.cancelled = True
        self.status = "cancelled"
        return SimpleNamespace(status="cancelled")

    def events(self) -> AsyncIterator[StreamEvent]:
        # Changes are the whole vocabulary a driver needs: one parks the turn on
        # a host tool, one settles it, and the stream ends behind the second.
        def change(revision: int, status: str) -> StreamEvent:
            return StreamEvent(
                type="transcript.update",
                id=f"cursor-{revision}",
                data={
                    "messages": [],
                    "invocation_changes": [{
                        "invocation_id": self.invocation_id,
                        "revision": revision,
                        "status": status,
                        "terminal": status in TERMINAL_INVOCATION_STATUSES,
                    }],
                    "cursor": f"cursor-{revision}",
                },
            )

        async def generate() -> AsyncIterator[StreamEvent]:
            if self.waiting_tool:
                yield change(1, "waiting")
            yield change(2, "completed")

        return generate()

    async def wait(self, **_: Any) -> Any:
        while (await self.refresh()).status not in {
            "completed",
            "failed",
            "cancelled",
        }:
            await asyncio.sleep(0)
        return await self.refresh()


class FakeClient:
    def __init__(self, handles: list[FakeHandle]) -> None:
        self.handles = handles
        self.invocations: list[Any] = []
        self._session_locks: dict[str, asyncio.Lock] = {}
        self._background_tasks: set[asyncio.Task[Any]] = set()

    async def invoke(self, request: Any) -> FakeHandle:
        self.invocations.append(request)
        return self.handles.pop(0)


@dataclass(frozen=True)
class Answer:
    answer: str


def agent_options(*tools: Tool) -> AgentOptions[Answer]:
    return AgentOptions(
        agent_key="support",
        structured_output_decoder=lambda value: Answer(**value),
        tools=tools,
    )


@pytest.mark.asyncio
async def test_agent_five_verbs_dispatch_and_typed_structured_output() -> None:
    handler_calls: list[Any] = []

    async def weather(value: Any) -> Any:
        handler_calls.append(value)
        return {"temperature": 21}

    tool = Tool(
        mode="host",
        name="weather",
        description="Weather lookup",
        input_schema={"type": "object"},
        handler=weather,
    )
    handles = [
        FakeHandle(),
        FakeHandle(waiting_tool="weather"),
        FakeHandle(waiting_tool="weather", structured_output={"answer": "warm"}),
        FakeHandle(waiting_tool="weather"),
        FakeHandle(waiting_tool="weather"),
    ]
    client = FakeClient(handles)
    agent = Agent(client, agent_options(tool))  # type: ignore[arg-type]

    handle = await agent.invoke(
        "invoke",
        options=InvocationOptions(if_active="supersede"),
    )
    assert handle.invocation_id == INVOCATION_ID
    assert client.invocations[0].if_active == "supersede"

    streamed = [
        item.event.type
        async for item in agent.stream("stream")
    ]
    assert streamed == ["transcript.update", "transcript.update"]

    result = await agent.run("run")
    assert result.text == "hello"
    assert result.raw_structured_output == {"answer": "warm"}
    assert result.structured_output == Answer(answer="warm")

    assert await agent.text("text") == "hello"
    bound = agent.session(session_key="customer-123")
    assert await bound.text("bound") == "hello"
    assert client.invocations[-1].session_key == "customer-123"
    assert handler_calls == [{"city": "Paris"}] * 4


@pytest.mark.asyncio
async def test_bound_session_serializes_admission() -> None:
    client = FakeClient([])
    agent = Agent(client, agent_options())  # type: ignore[arg-type]
    active = 0
    maximum = 0
    release = asyncio.Event()

    async def delayed_run(_input: str, *, options: Any = None) -> str:
        nonlocal active, maximum
        active += 1
        maximum = max(maximum, active)
        await release.wait()
        active -= 1
        return "done"

    agent.run = delayed_run  # type: ignore[method-assign]
    bound = agent.session(session_id=SESSION_ID)
    first = asyncio.create_task(bound.run("first"))
    second = asyncio.create_task(bound.run("second"))
    await asyncio.sleep(0)
    assert active == 1
    release.set()
    assert await asyncio.gather(first, second) == ["done", "done"]
    assert maximum == 1


@pytest.mark.asyncio
async def test_missing_handler_cancels_by_default_and_supports_opt_out() -> None:
    missing = Tool(
        mode="host",
        name="weather",
        description="Weather lookup",
        input_schema={"type": "object"},
    )
    cancelled_handle = FakeHandle(waiting_tool="weather")
    agent = Agent(FakeClient([cancelled_handle]), agent_options(missing))  # type: ignore[arg-type]
    with pytest.raises(MissingToolHandlerError) as cancelled:
        await agent.run("hello")
    assert cancelled.value.invocation_cancelled is True
    assert cancelled_handle.cancelled is True

    waiting_handle = FakeHandle(waiting_tool="weather")
    agent = Agent(FakeClient([waiting_handle]), agent_options(missing))  # type: ignore[arg-type]
    with pytest.raises(MissingToolHandlerError) as preserved:
        await agent.run(
            "hello",
            options=InvocationOptions(leave_waiting_on_missing_handler=True),
        )
    assert preserved.value.invocation_cancelled is False
    assert waiting_handle.cancelled is False


@pytest.mark.asyncio
async def test_text_distinguishes_structured_and_tool_only_results() -> None:
    structured = Agent(
        FakeClient([
            FakeHandle(output_text=None, structured_output={"answer": "json"}),
        ]),
        agent_options(),
    )  # type: ignore[arg-type]
    with pytest.raises(NoOutputTextError) as structured_error:
        await structured.text("hello")
    assert structured_error.value.result_kind == "structured output"

    tool = Tool(
        mode="callback",
        name="notify",
        description="Notify",
        input_schema={"type": "object"},
        callback_url="https://example.test/callback",
    )
    tool_only = Agent(
        FakeClient([FakeHandle(output_text=None)]),
        agent_options(tool),
    )  # type: ignore[arg-type]
    with pytest.raises(NoOutputTextError) as tool_error:
        await tool_only.text("hello")
    assert tool_error.value.result_kind == "tool-only output"


@pytest.mark.asyncio
async def test_agent_timeout_is_typed_and_cancellation_stays_native() -> None:
    class BlockingHandle(FakeHandle):
        def events(self) -> AsyncIterator[StreamEvent]:
            async def generate() -> AsyncIterator[StreamEvent]:
                await asyncio.Event().wait()
                yield StreamEvent(type="never", data={})

            return generate()

    agent = Agent(
        FakeClient([BlockingHandle(), BlockingHandle()]),
        agent_options(),
    )  # type: ignore[arg-type]
    with pytest.raises(NvokenError) as timeout:
        await agent.run("hello", options=InvocationOptions(timeout=0.001))
    assert timeout.value.category == "timeout"

    task = asyncio.create_task(agent.run("hello"))
    await asyncio.sleep(0)
    task.cancel()
    with pytest.raises(asyncio.CancelledError):
        await task


class DeclaringClient(FakeClient):
    """A client that records what a declared Agent creates and admits."""

    def __init__(self, handles: list[FakeHandle], *, pinned_revision: int | None = None) -> None:
        super().__init__(handles)
        self.creates: list[dict[str, Any]] = []
        self.pinned_revision = pinned_revision

    async def create_agent(self, **kwargs: Any) -> Any:
        self.creates.append(kwargs)
        return SimpleNamespace(
            id="agent_declared",
            tenant_key=kwargs.get("tenant_key"),
            agent_key=kwargs["agent_key"],
            name=kwargs.get("name") or kwargs["agent_key"],
            definition_id="def_declared",
            pinned_revision=self.pinned_revision,
            archived_at=None,
        )


@pytest.mark.asyncio
async def test_declared_agent_creates_its_record_on_first_use() -> None:
    client = DeclaringClient([FakeHandle(), FakeHandle()])
    agent: Agent[Answer] = Agent(
        client,  # type: ignore[arg-type]
        AgentOptions(
            tenant_key="customer-482",
            agent_key="support",
            definition_key="support",
        ),
    )
    assert agent.id is None and agent.resource is None

    await agent.invoke("first")
    await agent.invoke("second")

    assert len(client.creates) == 1
    assert client.creates[0]["definition_key"] == "support"
    assert client.creates[0]["definition_id"] is None
    assert agent.id == "agent_declared"
    # Admission uses the record's ID once it is known.
    assert [request.agent_id for request in client.invocations] == [
        "agent_declared",
        "agent_declared",
    ]
    assert [request.agent_key for request in client.invocations] == [None, None]


@pytest.mark.asyncio
async def test_declared_agent_refuses_a_contradicted_pin() -> None:
    client = DeclaringClient([], pinned_revision=3)
    contradicted: Agent[Answer] = Agent(
        client,  # type: ignore[arg-type]
        AgentOptions(agent_key="support", definition_key="support", pinned_revision=2),
    )
    with pytest.raises(NvokenError) as conflict:
        await contradicted.ensure()
    assert conflict.value.code == "agent_pin_conflict"

    # Declaring no pin declares nothing about the pin.
    silent: Agent[Answer] = Agent(
        client,  # type: ignore[arg-type]
        AgentOptions(agent_key="support", definition_key="support"),
    )
    assert (await silent.ensure()).pinned_revision == 3


@pytest.mark.asyncio
async def test_declared_agent_without_a_definition_cannot_create_itself() -> None:
    agent: Agent[Answer] = Agent(
        DeclaringClient([]),  # type: ignore[arg-type]
        AgentOptions(agent_key="support"),
    )
    with pytest.raises(NvokenError) as missing:
        await agent.ensure()
    assert "definition_key" in str(missing.value)

    with pytest.raises(NvokenError):
        Agent(
            DeclaringClient([]),  # type: ignore[arg-type]
            AgentOptions(agent_id="agent_declared", definition_key="support"),
        )
