from __future__ import annotations

import asyncio
from dataclasses import dataclass
from types import SimpleNamespace
from typing import Any, AsyncIterator

import pytest

from nvoken import (
    Agent,
    AgentOptions,
    ExecutionSpec,
    InvocationOptions,
    MissingToolHandlerError,
    Model,
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
        self.agent_id = "agnt_test"
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
                    input={"city": "Paris"},
                )
            ]
        else:
            self.status = "cancelled" if self.cancelled else "completed"
            pending = None
        return SimpleNamespace(
            id=self.invocation_id,
            status=self.status,
            pending_tool_calls=pending,
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
        async def generate() -> AsyncIterator[StreamEvent]:
            if self.waiting_tool:
                yield StreamEvent(type="invocation.update", data={"status": "waiting"})
            yield StreamEvent(type="invocation.result", data={})

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
        spec=ExecutionSpec(
            model=Model(provider="openai", id="gpt-test"),
            tools=tools,
            output_schema={"type": "object"},
        ),
        structured_output_decoder=lambda value: Answer(**value),
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

    handle = await agent.invoke("invoke")
    assert handle.invocation_id == INVOCATION_ID

    streamed = [
        item.event.type
        async for item in agent.stream("stream")
    ]
    assert streamed == ["invocation.update", "invocation.result"]

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
