from __future__ import annotations

import asyncio
import inspect
from dataclasses import dataclass, replace
from typing import Any, AsyncIterator, Callable, Generic, TypeVar

from nvoken_generated.models.invocation import Invocation
from nvoken_generated.models.invocation_result import InvocationResult

from .client import (
    Client,
    ExecutionSpec,
    InvocationHandle,
    InvokeRequest,
    NvokenError,
    ProviderCredentialSelection,
    Tool,
    ToolResult,
)
from .stream import StreamEvent

StructuredT = TypeVar("StructuredT")


class MissingToolHandlerError(NvokenError):
    def __init__(
        self,
        invocation_id: str,
        tool_name: str,
        *,
        invocation_cancelled: bool,
    ) -> None:
        super().__init__(
            "conflict",
            f"Invocation {invocation_id} is waiting for unhandled tool {tool_name!r}",
            code="missing_tool_handler",
            details={
                "invocation_id": invocation_id,
                "tool_name": tool_name,
                "invocation_cancelled": invocation_cancelled,
            },
        )
        self.invocation_id = invocation_id
        self.tool_name = tool_name
        self.invocation_cancelled = invocation_cancelled


class NoOutputTextError(NvokenError):
    def __init__(self, invocation_id: str, result_kind: str) -> None:
        super().__init__(
            "unexpected_response",
            f"Invocation {invocation_id} completed with {result_kind}, not text",
            code="no_output_text",
            details={
                "invocation_id": invocation_id,
                "result_kind": result_kind,
            },
        )
        self.invocation_id = invocation_id
        self.result_kind = result_kind


@dataclass(frozen=True)
class AgentOptions(Generic[StructuredT]):
    agent_key: str
    spec: ExecutionSpec
    tenant_key: str | None = None
    provider_credentials: tuple[ProviderCredentialSelection, ...] = ()
    structured_output_decoder: Callable[[dict[str, Any]], StructuredT] | None = None


@dataclass(frozen=True)
class InvocationOptions:
    idempotency_key: str | None = None
    tenant_key: str | None = None
    session_id: str | None = None
    session_key: str | None = None
    timeout: float | None = None
    leave_waiting_on_missing_handler: bool = False


@dataclass(frozen=True)
class AgentResult(Generic[StructuredT]):
    handle: InvocationHandle
    raw: InvocationResult
    text: str | None
    structured_output: StructuredT | dict[str, Any] | None
    raw_structured_output: dict[str, Any] | None

    @property
    def invocation(self) -> Invocation:
        return self.raw.invocation


@dataclass(frozen=True)
class AgentStreamEvent:
    handle: InvocationHandle
    event: StreamEvent


class Agent(Generic[StructuredT]):
    def __init__(self, client: Client, options: AgentOptions[StructuredT]) -> None:
        if not options.agent_key:
            raise NvokenError("validation", "agent_key is required")
        self.client = client
        self.options = options
        self._host_tools = {
            tool.name: tool
            for tool in options.spec.tools
            if tool.mode == "host"
        }

    async def invoke(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> InvocationHandle:
        call = options or InvocationOptions()
        return await self.client.invoke(self._request(input, call))

    async def run(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> AgentResult[StructuredT]:
        call = options or InvocationOptions()
        handle: InvocationHandle | None = None
        try:
            async for streamed in self.stream(input, options=call):
                handle = streamed.handle
        except asyncio.CancelledError:
            raise
        except NvokenError as error:
            if handle is None or error.category not in {"server", "transport"}:
                raise
        if handle is None:
            raise NvokenError(
                "unexpected_response",
                "Invocation stream ended before admission was acknowledged",
            )
        result = await self._settle_by_read(handle, call)
        return self._result(handle, result)

    async def text(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> str:
        result = await self.run(input, options=options)
        if result.text:
            return result.text
        result_kind = (
            "structured output"
            if result.raw_structured_output is not None
            else "tool-only output"
            if self.options.spec.tools
            else "no assistant output"
        )
        raise NoOutputTextError(result.handle.invocation_id, result_kind)

    async def stream(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> AsyncIterator[AgentStreamEvent]:
        call = options or InvocationOptions()
        handle = await self.invoke(input, options=call)
        submitted: set[str] = set()
        iterator = handle.events().__aiter__()
        deadline = _deadline(call.timeout)
        while True:
            try:
                event = await _next_with_deadline(iterator, deadline, handle.invocation_id)
            except StopAsyncIteration:
                return
            yield AgentStreamEvent(handle=handle, event=event)
            if event.type in {"invocation.update", "stream.end"}:
                invocation = await handle.refresh()
                if invocation.status == "waiting":
                    await self._dispatch_waiting(
                        handle,
                        invocation,
                        submitted,
                        leave_waiting=call.leave_waiting_on_missing_handler,
                    )
            if event.type == "invocation.result":
                return

    def session(
        self,
        *,
        session_id: str | None = None,
        session_key: str | None = None,
        tenant_key: str | None = None,
    ) -> BoundSession[StructuredT]:
        if (session_id is None) == (session_key is None):
            raise NvokenError(
                "validation",
                "exactly one of session_id or session_key is required",
            )
        effective_tenant = tenant_key or self.options.tenant_key
        lock_key = (
            f"id:{session_id}"
            if session_id is not None
            else f"key:{effective_tenant or 'default'}:{session_key}"
        )
        lock = self.client._session_locks.setdefault(lock_key, asyncio.Lock())
        return BoundSession(
            self,
            lock,
            session_id=session_id,
            session_key=session_key,
            tenant_key=effective_tenant,
        )

    def _request(self, input: str, options: InvocationOptions) -> InvokeRequest:
        return InvokeRequest(
            agent_key=self.options.agent_key,
            input=input,
            spec=self.options.spec,
            idempotency_key=options.idempotency_key,
            tenant_key=options.tenant_key or self.options.tenant_key,
            session_id=options.session_id,
            session_key=options.session_key,
            provider_credentials=self.options.provider_credentials,
        )

    async def _settle_by_read(
        self,
        handle: InvocationHandle,
        options: InvocationOptions,
    ) -> InvocationResult:
        submitted: set[str] = set()
        deadline = _deadline(options.timeout)
        while True:
            invocation = await handle.wait_for_action(
                timeout=_remaining(deadline),
            )
            if invocation.status == "waiting":
                dispatched = await self._dispatch_waiting(
                    handle,
                    invocation,
                    submitted,
                    leave_waiting=options.leave_waiting_on_missing_handler,
                )
                if not dispatched:
                    await asyncio.sleep(0.05)
                continue
            if invocation.status != "completed":
                raise NvokenError(
                    "conflict",
                    f"Invocation {handle.invocation_id} ended with status "
                    f"{invocation.status}",
                    code=invocation.error.code if invocation.error else None,
                    details=invocation.error.details if invocation.error else None,
                )
            return await handle.result()

    async def _dispatch_waiting(
        self,
        handle: InvocationHandle,
        invocation: Invocation,
        submitted: set[str],
        *,
        leave_waiting: bool,
    ) -> bool:
        results: list[ToolResult] = []
        for pending in invocation.pending_tool_calls or []:
            if pending.id in submitted:
                continue
            tool = self._host_tools.get(pending.name)
            if tool is None or tool.handler is None:
                cancelled = False
                if not leave_waiting:
                    await handle.cancel()
                    cancelled = True
                raise MissingToolHandlerError(
                    handle.invocation_id,
                    pending.name,
                    invocation_cancelled=cancelled,
                )
            try:
                content = tool.handler(pending.input)
                if inspect.isawaitable(content):
                    content = await content
                results.append(ToolResult(tool_call_id=pending.id, content=content))
            except asyncio.CancelledError:
                raise
            except Exception as error:
                results.append(ToolResult(
                    tool_call_id=pending.id,
                    content={
                        "error": str(error),
                        "type": type(error).__name__,
                    },
                    is_error=True,
                ))
        if not results:
            return False
        await handle.submit_tool_results(results)
        submitted.update(result.tool_call_id for result in results)
        return True

    def _result(
        self,
        handle: InvocationHandle,
        result: InvocationResult,
    ) -> AgentResult[StructuredT]:
        raw_structured = result.invocation.structured_output
        structured: StructuredT | dict[str, Any] | None = raw_structured
        if raw_structured is not None and self.options.structured_output_decoder:
            structured = self.options.structured_output_decoder(raw_structured)
        return AgentResult(
            handle=handle,
            raw=result,
            text=result.output_text,
            structured_output=structured,
            raw_structured_output=raw_structured,
        )


class BoundSession(Generic[StructuredT]):
    def __init__(
        self,
        agent: Agent[StructuredT],
        lock: asyncio.Lock,
        *,
        session_id: str | None,
        session_key: str | None,
        tenant_key: str | None,
    ) -> None:
        self.agent = agent
        self._lock = lock
        self.session_id = session_id
        self.session_key = session_key
        self.tenant_key = tenant_key

    async def invoke(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> InvocationHandle:
        await self._lock.acquire()
        try:
            handle = await self.agent.invoke(input, options=self._options(options))
        except BaseException:
            self._lock.release()
            raise
        task = asyncio.create_task(self._release_when_terminal(handle, options))
        self.agent.client._background_tasks.add(task)
        task.add_done_callback(self.agent.client._background_tasks.discard)
        return handle

    async def run(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> AgentResult[StructuredT]:
        async with self._lock:
            return await self.agent.run(input, options=self._options(options))

    async def text(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> str:
        async with self._lock:
            return await self.agent.text(input, options=self._options(options))

    async def stream(
        self,
        input: str,
        *,
        options: InvocationOptions | None = None,
    ) -> AsyncIterator[AgentStreamEvent]:
        async with self._lock:
            async for event in self.agent.stream(
                input,
                options=self._options(options),
            ):
                yield event

    def _options(self, options: InvocationOptions | None) -> InvocationOptions:
        call = options or InvocationOptions()
        if call.session_id is not None or call.session_key is not None:
            raise NvokenError(
                "validation",
                "bound Session calls cannot override their Session",
            )
        return replace(
            call,
            tenant_key=self.tenant_key,
            session_id=self.session_id,
            session_key=self.session_key,
        )

    async def _release_when_terminal(
        self,
        handle: InvocationHandle,
        options: InvocationOptions | None,
    ) -> None:
        try:
            call = options or InvocationOptions()
            await handle.wait(timeout=call.timeout)
        finally:
            self._lock.release()


def _deadline(timeout: float | None) -> float | None:
    if timeout is None:
        return None
    if timeout <= 0:
        raise NvokenError("validation", "timeout must be greater than zero")
    return asyncio.get_running_loop().time() + timeout


def _remaining(deadline: float | None) -> float | None:
    if deadline is None:
        return None
    remaining = deadline - asyncio.get_running_loop().time()
    if remaining <= 0:
        raise NvokenError("timeout", "Local Agent operation timed out")
    return remaining


async def _next_with_deadline(
    iterator: AsyncIterator[StreamEvent],
    deadline: float | None,
    invocation_id: str,
) -> StreamEvent:
    remaining = _remaining(deadline)
    if remaining is None:
        return await anext(iterator)
    try:
        return await asyncio.wait_for(anext(iterator), timeout=remaining)
    except asyncio.TimeoutError as error:
        raise NvokenError(
            "timeout",
            f"Local stream for Invocation {invocation_id} timed out",
        ) from error
