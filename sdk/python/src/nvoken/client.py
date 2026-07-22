from __future__ import annotations

import asyncio
import json
import random
from dataclasses import dataclass, field
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Any, AsyncIterator, Awaitable, Callable, Literal

import httpx

from nvoken_generated.api.invocations_api import InvocationsApi
from nvoken_generated.api.sessions_api import SessionsApi
from nvoken_generated.api_client import ApiClient
from nvoken_generated.configuration import Configuration
from nvoken_generated.exceptions import ApiException
from nvoken_generated.models.callback_target import CallbackTarget
from nvoken_generated.models.callback_tool_spec import CallbackToolSpec
from nvoken_generated.models.client_tool_spec import ClientToolSpec
from nvoken_generated.models.create_invocation_request import CreateInvocationRequest
from nvoken_generated.models.inline_execution_spec import InlineExecutionSpec
from nvoken_generated.models.invocation import Invocation
from nvoken_generated.models.invocation_budget_request import InvocationBudgetRequest
from nvoken_generated.models.invocation_input import InvocationInput
from nvoken_generated.models.invocation_list import InvocationList
from nvoken_generated.models.invocation_result import InvocationResult
from nvoken_generated.models.invocation_status import InvocationStatus
from nvoken_generated.models.model_selection import ModelSelection
from nvoken_generated.models.session import Session
from nvoken_generated.models.session_list import SessionList
from nvoken_generated.models.session_message import SessionMessage
from nvoken_generated.models.session_message_list import SessionMessageList
from nvoken_generated.models.structured_output_spec import StructuredOutputSpec
from nvoken_generated.models.submit_client_tool_results_request import SubmitClientToolResultsRequest
from nvoken_generated.models.submit_client_tool_results_request_results_inner import (
    SubmitClientToolResultsRequestResultsInner,
)
from nvoken_generated.models.submit_client_tool_results_response import SubmitClientToolResultsResponse
from nvoken_generated.models.text_input_block import TextInputBlock
from nvoken_generated.models.tool_spec import ToolSpec as GeneratedToolSpec

from .stream import Reducer, ReducedSnapshot, StreamEvent, stream_session

ErrorCategory = Literal[
    "authentication",
    "validation",
    "not_found",
    "conflict",
    "rate_limit",
    "server",
    "transport",
    "timeout",
    "unexpected_response",
]


class NvokenError(Exception):
    def __init__(
        self,
        category: ErrorCategory,
        message: str,
        *,
        status: int | None = None,
        code: str | None = None,
        request_id: str | None = None,
        retry_after: float | None = None,
        details: dict[str, Any] | None = None,
    ) -> None:
        super().__init__(message)
        self.category = category
        self.status = status
        self.code = code
        self.request_id = request_id
        self.retry_after = retry_after
        self.details = details


@dataclass(frozen=True)
class Model:
    provider: str
    name: str


@dataclass(frozen=True)
class Tool:
    mode: Literal["client", "callback"]
    name: str
    description: str
    input_schema: dict[str, Any]
    callback_url: str | None = None


@dataclass(frozen=True)
class Budgets:
    wall_clock_timeout_seconds: int | None = None
    active_execution_timeout_seconds: int | None = None
    max_output_tokens: int | None = None
    max_estimated_cost_usd: float | None = None
    max_iterations: int | None = None


@dataclass(frozen=True)
class ExecutionSpec:
    instructions: str
    model: Model
    budgets: Budgets | None = None
    tools: tuple[Tool, ...] = ()
    output_schema: dict[str, Any] | None = None


@dataclass(frozen=True)
class InvokeRequest:
    agent_ref: str
    idempotency_key: str
    input: str
    spec: ExecutionSpec
    tenant_ref: str | None = None
    session_id: str | None = None
    session_key: str | None = None


@dataclass(frozen=True)
class ToolResult:
    tool_call_id: str
    content: Any
    is_error: bool = False


@dataclass(frozen=True)
class RetryPolicy:
    maximum_attempts: int = 4
    minimum_delay: float = 0.1
    maximum_delay: float = 2.0


class Client:
    def __init__(
        self,
        base_url: str,
        api_key: str,
        *,
        retry: RetryPolicy = RetryPolicy(),
        transport: httpx.AsyncBaseTransport | None = None,
    ) -> None:
        if not base_url or not api_key:
            raise NvokenError("validation", "base_url and api_key are required")
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.retry = retry
        configuration = Configuration(host=self.base_url, access_token=api_key)
        configuration.discard_unknown_keys = False
        self.api_client = ApiClient(configuration)
        self.invocations = InvocationsApi(self.api_client)
        self.sessions = SessionsApi(self.api_client)
        self.stream_client = httpx.AsyncClient(
            base_url=self.base_url,
            headers={"Authorization": f"Bearer {api_key}", "User-Agent": "nvoken-python/0.1.0"},
            transport=transport,
            timeout=None,
        )

    async def __aenter__(self) -> Client:
        return self

    async def __aexit__(self, *_: object) -> None:
        await self.close()

    async def close(self) -> None:
        await self.api_client.close()
        await self.stream_client.aclose()

    def raw(self) -> tuple[InvocationsApi, SessionsApi]:
        return self.invocations, self.sessions

    async def invoke(self, request: InvokeRequest) -> Handle:
        if not request.agent_ref or not request.idempotency_key or not request.input:
            raise NvokenError("validation", "agent_ref, idempotency_key, and input are required")
        tools: list[GeneratedToolSpec] = []
        for tool in request.spec.tools:
            if tool.mode == "client":
                tools.append(GeneratedToolSpec(ClientToolSpec(
                    mode="client",
                    name=tool.name,
                    description=tool.description,
                    input_schema=tool.input_schema,
                )))
            else:
                if not tool.callback_url:
                    raise NvokenError("validation", f"callback tool {tool.name} requires callback_url")
                tools.append(GeneratedToolSpec(CallbackToolSpec(
                    mode="callback",
                    name=tool.name,
                    description=tool.description,
                    input_schema=tool.input_schema,
                    callback=CallbackTarget(url=tool.callback_url),
                )))
        budgets = request.spec.budgets
        body = CreateInvocationRequest(
            agent_ref=request.agent_ref,
            tenant_ref=request.tenant_ref,
            session_id=request.session_id,
            session_key=request.session_key,
            idempotency_key=request.idempotency_key,
            input=InvocationInput(content=[TextInputBlock(type="text", text=request.input)]),
            spec=InlineExecutionSpec(
                instructions=request.spec.instructions,
                model=ModelSelection(provider=request.spec.model.provider, name=request.spec.model.name),
                budgets=InvocationBudgetRequest(**vars(budgets)) if budgets else None,
                tools=tools or None,
                output=StructuredOutputSpec(schema=request.spec.output_schema) if request.spec.output_schema else None,
            ),
        )
        acknowledgement = await self._replay_safe(lambda: self.invocations.create_invocation(body))
        return Handle(self, acknowledgement.invocation_id, acknowledgement.session_id, acknowledgement.status)

    async def resume(self, invocation_id: str) -> Handle:
        invocation = await self.get(invocation_id)
        return Handle(self, invocation.id, invocation.session_id, invocation.status)

    async def get(self, invocation_id: str) -> Invocation:
        return await self._replay_safe(lambda: self.invocations.get_invocation(invocation_id))

    async def get_result(self, invocation_id: str) -> InvocationResult:
        return await self._replay_safe(
            lambda: self.invocations.get_invocation_result(invocation_id)
        )

    async def cancel(self, invocation_id: str) -> Invocation:
        return await self._replay_safe(lambda: self.invocations.cancel_invocation(invocation_id))

    async def submit_tool_results(
        self,
        invocation_id: str,
        results: list[ToolResult],
    ) -> SubmitClientToolResultsResponse:
        body = SubmitClientToolResultsRequest(results=[
            SubmitClientToolResultsRequestResultsInner(
                tool_call_id=result.tool_call_id,
                content=result.content,
                is_error=result.is_error if result.is_error else None,
            )
            for result in results
        ])
        return await self._replay_safe(
            lambda: self.invocations.submit_client_tool_results(invocation_id, body)
        )

    async def list_invocations(
        self,
        *,
        tenant_ref: str | None = None,
        default_tenant: bool | None = None,
        session_id: str | None = None,
        agent_id: str | None = None,
        status: InvocationStatus | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> InvocationList:
        return await self._replay_safe(lambda: self.invocations.list_invocations(
            tenant_ref=tenant_ref,
            default_tenant=default_tenant,
            session_id=session_id,
            agent_id=agent_id,
            status=status,
            cursor=cursor,
            limit=limit,
        ))

    async def invocation_items(
        self,
        *,
        tenant_ref: str | None = None,
        default_tenant: bool | None = None,
        session_id: str | None = None,
        agent_id: str | None = None,
        status: InvocationStatus | None = None,
        limit: int | None = None,
    ) -> AsyncIterator[Invocation]:
        cursor: str | None = None
        while True:
            page = await self.list_invocations(
                tenant_ref=tenant_ref,
                default_tenant=default_tenant,
                session_id=session_id,
                agent_id=agent_id,
                status=status,
                cursor=cursor,
                limit=limit,
            )
            for item in page.items:
                yield item
            cursor = page.next_cursor
            if not cursor:
                return

    async def list_sessions(
        self,
        *,
        tenant_ref: str | None = None,
        default_tenant: bool | None = None,
        agent_id: str | None = None,
        session_key: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> SessionList:
        return await self._replay_safe(lambda: self.sessions.list_sessions(
            tenant_ref=tenant_ref,
            default_tenant=default_tenant,
            agent_id=agent_id,
            session_key=session_key,
            cursor=cursor,
            limit=limit,
        ))

    async def get_session(self, session_id: str) -> Session:
        return await self._replay_safe(lambda: self.sessions.get_session(session_id))

    async def list_messages(
        self,
        session_id: str,
        *,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> SessionMessageList:
        return await self._replay_safe(
            lambda: self.sessions.list_session_messages(
                session_id,
                cursor=cursor,
                limit=limit,
            )
        )

    async def _replay_safe(self, operation: Callable[[], Awaitable[Any]]) -> Any:
        last_error: NvokenError | None = None
        for attempt in range(1, self.retry.maximum_attempts + 1):
            try:
                return await operation()
            except asyncio.CancelledError:
                raise NvokenError("timeout", "local wait or request was cancelled")
            except (ApiException, httpx.HTTPError) as error:
                last_error = normalize_error(error)
                if attempt == self.retry.maximum_attempts or not retryable(last_error):
                    raise last_error from error
                exponential = min(
                    self.retry.maximum_delay,
                    self.retry.minimum_delay * 2 ** (attempt - 1),
                )
                delay = min(last_error.retry_after, self.retry.maximum_delay) \
                    if last_error.retry_after is not None \
                    else exponential / 2 + random.random() * exponential / 2
                await asyncio.sleep(delay)
        raise last_error or NvokenError("unexpected_response", "request did not run")


@dataclass
class Handle:
    client: Client = field(repr=False)
    invocation_id: str
    session_id: str
    status: InvocationStatus

    async def refresh(self) -> Invocation:
        invocation = await self.client.get(self.invocation_id)
        self.status = invocation.status
        return invocation

    async def wait(self, *, minimum_delay: float = 0.1, maximum_delay: float = 2.0) -> Invocation:
        delay = minimum_delay
        try:
            while True:
                invocation = await self.refresh()
                if invocation.status in {"completed", "failed", "cancelled"}:
                    return invocation
                await asyncio.sleep(delay)
                delay = min(delay * 2, maximum_delay)
        except (asyncio.CancelledError, TimeoutError) as error:
            raise NvokenError("timeout", "local wait timed out or was cancelled") from error

    async def result(self) -> InvocationResult:
        result = await self.client.get_result(self.invocation_id)
        self.status = result.invocation.status
        return result

    async def list_messages(self) -> list[SessionMessage]:
        return (await self.result()).messages

    async def text(self) -> str:
        """Return the completed turn's canonical assistant text.

        Raises ``unexpected_response`` when the wire ``output_text`` is null
        or the empty string: the wire keeps those distinct, but this helper
        deliberately treats both as "no useful answer". Read ``result()``
        directly to observe the distinction.
        """
        result = await self.result()
        if not result.output_text:
            raise NvokenError(
                "unexpected_response",
                f"Invocation {self.invocation_id} has no canonical assistant text",
            )
        return result.output_text

    async def submit_tool_results(self, results: list[ToolResult]) -> SubmitClientToolResultsResponse:
        response = await self.client.submit_tool_results(self.invocation_id, results)
        self.status = response.status
        return response

    async def cancel(self) -> Invocation:
        invocation = await self.client.cancel(self.invocation_id)
        self.status = invocation.status
        return invocation

    async def stream(
        self,
        consume: Callable[[StreamEvent, ReducedSnapshot], Awaitable[None] | None],
    ) -> None:
        await stream_session(self.client, self, Reducer(), consume)


def normalize_error(error: ApiException | httpx.HTTPError) -> NvokenError:
    if isinstance(error, httpx.HTTPError):
        return NvokenError("transport", "nvoken transport failed")
    status = error.status or 0
    body: dict[str, Any] = {}
    if error.data is not None:
        if hasattr(error.data, "model_dump"):
            body = error.data.model_dump()
        elif isinstance(error.data, dict):
            body = error.data
    if not body and error.body:
        try:
            body = json.loads(error.body)
        except json.JSONDecodeError:
            pass
    category: ErrorCategory = (
        "authentication" if status in {401, 403}
        else "validation" if status in {400, 422}
        else "not_found" if status == 404
        else "conflict" if status == 409
        else "rate_limit" if status == 429
        else "server" if status >= 500
        else "unexpected_response"
    )
    headers = error.headers or {}
    return NvokenError(
        category,
        body.get("message") or f"nvoken returned HTTP {status}",
        status=status,
        code=body.get("code"),
        request_id=body.get("request_id") or headers.get("x-request-id"),
        retry_after=parse_retry_after(headers.get("retry-after")),
        details=body.get("details"),
    )


async def normalize_httpx_response(response: httpx.Response) -> NvokenError:
    body: dict[str, Any] = {}
    try:
        body = response.json()
    except (json.JSONDecodeError, UnicodeDecodeError):
        pass
    status = response.status_code
    category: ErrorCategory = (
        "authentication" if status in {401, 403}
        else "validation" if status in {400, 422}
        else "not_found" if status == 404
        else "conflict" if status == 409
        else "rate_limit" if status == 429
        else "server" if status >= 500
        else "unexpected_response"
    )
    return NvokenError(
        category,
        body.get("message") or f"nvoken returned HTTP {status}",
        status=status,
        code=body.get("code"),
        request_id=body.get("request_id") or response.headers.get("x-request-id"),
        retry_after=parse_retry_after(response.headers.get("retry-after")),
        details=body.get("details"),
    )


def retryable(error: NvokenError) -> bool:
    return error.category == "transport" or error.status in {408, 425, 429, 500, 502, 503, 504}


def parse_retry_after(value: str | None) -> float | None:
    if not value:
        return None
    try:
        return max(0.0, float(value))
    except ValueError:
        try:
            when = parsedate_to_datetime(value)
            return max(0.0, (when - datetime.now(timezone.utc)).total_seconds())
        except (TypeError, ValueError):
            return None
