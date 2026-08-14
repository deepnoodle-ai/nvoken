from __future__ import annotations

import asyncio
import inspect
from dataclasses import dataclass, replace
from typing import Any, AsyncIterator, Awaitable, Callable, Generic, TypeVar

from nvoken_generated.models.invocation import Invocation
from nvoken_generated.models.invocation_result import InvocationResult
from nvoken_generated.models.tool_call_summary import ToolCallSummary

from .client import (
    AgentDefinition,
    BuiltinTool,
    BudgetExhaustionBehavior,
    Client,
    ContextItem,
    IfActivePolicy,
    InvocationHandle,
    InvokeRequest,
    Limits,
    MCPServer,
    MCPServerHeaders,
    Model,
    WebhookTarget,
    NvokenError,
    ProviderKeySelection,
    ProviderTool,
    Reasoning,
    Sampling,
    SessionOptions,
    Tool,
    ToolChoice,
    ToolResult,
    ended_message,
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
    """One identity and the Agent Definition every turn from this Agent runs with.

    Set ``agent_definition_id`` for a reusable resource, or leave it unset and
    provide the flat inline definition fields. ``tools`` registers local host
    handlers in either form. Inline ``model`` may be omitted when the Client
    carries a default.
    """

    agent_key: str
    agent_definition_id: str | None = None
    model: Model | None = None
    instructions: str | None = None
    sampling: Sampling | None = None
    reasoning: Reasoning | None = None
    tool_choice: ToolChoice | None = None
    limits: Limits | None = None
    tools: tuple[Tool | BuiltinTool, ...] = ()
    mcp_servers: tuple[MCPServer, ...] = ()
    provider_tools: tuple[ProviderTool, ...] = ()
    output_schema: dict[str, Any] | None = None
    mcp_server_headers: tuple[MCPServerHeaders, ...] = ()
    """Per-turn secret headers for the MCP servers this Agent declares.

    They stay outside the Agent Definition so no reusable revision contains a
    secret.
    """
    tenant_key: str | None = None
    provider_keys: tuple[ProviderKeySelection, ...] = ()
    webhook: WebhookTarget | None = None
    on_budget_exhausted: BudgetExhaustionBehavior | None = None
    structured_output_decoder: Callable[[dict[str, Any]], StructuredT] | None = None


@dataclass(frozen=True)
class InvocationOptions:
    idempotency_key: str | None = None
    if_active: IfActivePolicy | None = None
    on_budget_exhausted: BudgetExhaustionBehavior | None = None
    tenant_key: str | None = None
    session_id: str | None = None
    session_key: str | None = None
    session_options: SessionOptions | None = None
    webhook: WebhookTarget | None = None
    # Application state snapshots to record ahead of this turn's input.
    # Per-call rather than per-Agent, because a snapshot is what changes between
    # turns while the Agent Definition stays fixed.
    context: tuple[ContextItem, ...] = ()
    # Opaque host correlation data recorded on this Invocation. Per-call rather
    # than per-Agent: it is immutable and material to idempotency, so an
    # Agent-level default would make two otherwise distinct calls conflict.
    metadata: dict[str, str] | None = None
    timeout: float | None = None
    leave_waiting_on_missing_handler: bool = False


@dataclass(frozen=True)
class AnswerPendingToolCallsOptions:
    """Options for :meth:`Agent.answer_pending_tool_calls`."""

    # Runs before each tool. Returning False skips that call — use it to take an
    # execution lease keyed by the ToolCall id, so a streaming reader and this
    # worker cannot both start the same non-idempotent tool.
    claim: Callable[[ToolCallSummary], bool | Awaitable[bool]] | None = None
    # Raise rather than skipping a call this Agent has no handler for. The
    # default skips, because an unattended worker is often one of several
    # answering different tools.
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
        has_inline_fields = any((
            options.model is not None,
            options.instructions is not None,
            options.sampling is not None,
            options.reasoning is not None,
            options.tool_choice is not None,
            options.limits is not None,
            bool(options.mcp_servers),
            bool(options.provider_tools),
            options.output_schema is not None,
        ))
        if options.agent_definition_id is not None and has_inline_fields:
            raise NvokenError(
                "validation",
                "supply agent_definition_id or inline definition fields, not both",
            )
        definition: AgentDefinition | None = None
        if options.agent_definition_id is None:
            model = options.model or client.default_model
            if model is None:
                raise NvokenError("validation", "model is required for inline definition")
            definition = AgentDefinition(
                model=model,
                instructions=options.instructions,
                sampling=options.sampling,
                reasoning=options.reasoning,
                tool_choice=options.tool_choice,
                limits=options.limits,
                tools=options.tools,
                mcp_servers=options.mcp_servers,
                provider_tools=options.provider_tools,
                output_schema=options.output_schema,
            )
        self.client = client
        self.options = options
        self.definition = definition
        self._host_tools = {
            tool.name: tool
            for tool in options.tools
            if not isinstance(tool, BuiltinTool) and tool.mode == "host"
        }
        # Tools nvoken delivers over HTTPS. They can appear in a waiting
        # Invocation's pending calls once an endpoint has acknowledged a
        # delivery without settling it, and are answered by whatever accepted
        # that acknowledgement rather than from local handlers.
        self._callback_tools = {
            tool.name
            for tool in options.tools
            if not isinstance(tool, BuiltinTool) and tool.mode == "callback"
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
            if self.options.tools
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

    async def answer_pending_tool_calls(
        self,
        invocation_id: str,
        *,
        options: AnswerPendingToolCallsOptions | None = None,
    ) -> int:
        """Answer the host tool calls a parked Invocation is waiting on.

        This is the unattended path. An Invocation's ``webhook`` target receives
        a signed ``invocation.waiting`` post when the turn parks, and a worker
        calls this to finish it, so a turn makes progress with nobody watching.
        The same handlers still serve an attached reader — the first accepted
        result per call wins, so the two coexist rather than being a choice
        made per deployment.

        Acknowledge the webhook before calling this. Webhook delivery uses
        a 10 second request timeout while a host tool budget is minutes, so a
        receiver that executes tools inline has its delivery marked failed and
        retried while the work is still running. Verify the signature, enqueue,
        return 2xx, and call this from the worker.

        Fence side effects with ``claim``. First-accepted-result deduplicates
        the transcript; it does not stop two processes from both *beginning* a
        call. An attached reader and this worker can race, and webhooks
        are at-least-once, so two deliveries can race each other.

        Returns how many results were submitted. Zero means the Invocation was
        no longer waiting or every call was claimed elsewhere — both ordinary
        outcomes rather than errors.
        """
        call_options = options or AnswerPendingToolCallsOptions()
        invocation = await self.client.get_invocation(invocation_id)
        if invocation.status != "waiting":
            return 0
        handle = self.client.invocation(invocation_id)
        results: list[ToolResult] = []
        for pending in answerable_tool_calls(invocation):
            if pending.name in self._callback_tools:
                continue
            tool = self._host_tools.get(pending.name)
            if tool is None or tool.handler is None:
                if call_options.leave_waiting_on_missing_handler:
                    raise MissingToolHandlerError(
                        invocation_id,
                        pending.name,
                        invocation_cancelled=False,
                    )
                continue
            if call_options.claim is not None:
                claimed = call_options.claim(pending)
                if inspect.isawaitable(claimed):
                    claimed = await claimed
                if not claimed:
                    continue
            try:
                content = tool.handler(pending.arguments or {})
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
            return 0
        await handle.submit_tool_results(results)
        return len(results)

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
            agent_definition=self.definition,
            agent_definition_id=self.options.agent_definition_id,
            mcp_server_headers=self.options.mcp_server_headers,
            idempotency_key=options.idempotency_key,
            if_active=options.if_active,
            on_budget_exhausted=(
                options.on_budget_exhausted or self.options.on_budget_exhausted
            ),
            tenant_key=options.tenant_key or self.options.tenant_key,
            session_id=options.session_id,
            session_key=options.session_key,
            session_options=options.session_options,
            provider_keys=self.options.provider_keys,
            # A per-call target overrides the agent default so one Agent can
            # webhook different endpoints without a second Agent.
            webhook=options.webhook or self.options.webhook,
            context=options.context,
            metadata=options.metadata,
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
                    ended_message(handle.invocation_id, invocation),
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
        for pending in answerable_tool_calls(invocation):
            if pending.id in submitted:
                continue
            if pending.name in self._callback_tools:
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
                content = tool.handler(pending.arguments or {})
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


def answerable_tool_calls(invocation: Any) -> list[ToolCallSummary]:
    """The tool calls this caller is expected to run.

    There is one tool-call collection. A call you have to answer is the one
    carrying the arguments to answer it with; builtin and MCP calls nvoken runs
    itself, and calls that have already settled, carry none. Filtering on that
    is what replaced the separate pending list.
    """
    return [call for call in (invocation.tool_calls or []) if call.arguments is not None]
