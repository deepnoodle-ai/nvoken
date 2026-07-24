from __future__ import annotations

import asyncio
import json
import random
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Any, AsyncIterator, Awaitable, Callable, Literal

import httpx

from nvoken_generated.api.invocations_api import InvocationsApi
from nvoken_generated.api.models_api import ModelsApi
from nvoken_generated.api.sessions_api import SessionsApi
from nvoken_generated.api_client import ApiClient
from nvoken_generated.configuration import Configuration
from nvoken_generated.exceptions import ApiException
from nvoken_generated.models.callback_target import CallbackTarget
from nvoken_generated.models.callback_tool_spec import CallbackToolSpec
from nvoken_generated.models.host_tool_spec import HostToolSpec
from nvoken_generated.models.create_invocation_request import CreateInvocationRequest
from nvoken_generated.models.inline_execution_spec import InlineExecutionSpec
from nvoken_generated.models.invocation import Invocation
from nvoken_generated.models.invocation_limit_request import InvocationLimitRequest
from nvoken_generated.models.invocation_input import InvocationInput
from nvoken_generated.models.invocation_list import InvocationList
from nvoken_generated.models.invocation_provider_credential_selection import (
    InvocationProviderCredentialSelection,
)
from nvoken_generated.models.invocation_provider_credential_selection_one_of import (
    InvocationProviderCredentialSelectionOneOf,
)
from nvoken_generated.models.invocation_provider_credential_selection_one_of1 import (
    InvocationProviderCredentialSelectionOneOf1,
)
from nvoken_generated.models.invocation_result import InvocationResult
from nvoken_generated.models.invocation_status import InvocationStatus
from nvoken_generated.models.model_selection import ModelSelection
from nvoken_generated.models.model_descriptor import ModelDescriptor
from nvoken_generated.models.model_list import ModelList
from nvoken_generated.models.model_provider import ModelProvider
from nvoken_generated.models.provider_static_credential import ProviderStaticCredential
from nvoken_generated.models.session import Session
from nvoken_generated.models.session_list import SessionList
from nvoken_generated.models.session_message import SessionMessage
from nvoken_generated.models.session_message_list import SessionMessageList
from nvoken_generated.models.structured_output_spec import StructuredOutputSpec
from nvoken_generated.models.submit_host_tool_results_request import SubmitHostToolResultsRequest
from nvoken_generated.models.submit_host_tool_results_request_results_inner import (
    SubmitHostToolResultsRequestResultsInner,
)
from nvoken_generated.models.submit_host_tool_results_response import SubmitHostToolResultsResponse
from nvoken_generated.models.tool_spec import ToolSpec as GeneratedToolSpec

from .stream import ReducedSnapshot, Reducer, StreamEvent, stream_invocation, stream_session

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
    id: str


@dataclass(frozen=True)
class Tool:
    mode: Literal["host", "callback"]
    name: str
    description: str
    input_schema: dict[str, Any]
    callback_url: str | None = None


@dataclass(frozen=True)
class Limits:
    total_timeout_seconds: int | None = None
    active_timeout_seconds: int | None = None
    waiting_timeout_seconds: int | None = None
    max_output_tokens: int | None = None
    max_estimated_cost_usd: float | None = None
    max_iterations: int | None = None


@dataclass(frozen=True)
class ExecutionSpec:
    model: Model
    instructions: str | None = None
    limits: Limits | None = None
    tools: tuple[Tool, ...] = ()
    output_schema: dict[str, Any] | None = None


@dataclass(frozen=True)
class ProviderCredentialSelection:
    provider: str
    source: Literal["caller_ephemeral", "account_byok", "tenant_byok", "platform"]
    api_key: str | None = None


@dataclass(frozen=True)
class InvokeRequest:
    agent_key: str
    input: str
    spec: ExecutionSpec
    idempotency_key: str | None = None
    tenant_key: str | None = None
    session_id: str | None = None
    session_key: str | None = None
    provider_credentials: tuple[ProviderCredentialSelection, ...] = ()


@dataclass(frozen=True)
class ToolResult:
    tool_call_id: str
    content: Any
    is_error: bool = False


@dataclass(frozen=True)
class RetryPolicy:
    max_attempts: int = 4
    min_delay: float = 0.1
    max_delay: float = 2.0


class _StreamingPoolManager:
    def __init__(self, client: httpx.AsyncClient) -> None:
        self.client = client

    async def request(self, **kwargs: Any) -> httpx.Response:
        request = self.client.build_request(**kwargs)
        return await self.client.send(request, stream=True)

    async def aclose(self) -> None:
        await self.client.aclose()


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
        self.models = ModelsApi(self.api_client)
        self.sessions = SessionsApi(self.api_client)
        self.stream_client = httpx.AsyncClient(
            base_url=self.base_url,
            headers={"Authorization": f"Bearer {api_key}", "User-Agent": "nvoken-python/0.1.0"},
            transport=transport,
            timeout=None,
        )
        stream_configuration = Configuration(host=self.base_url, access_token=api_key)
        stream_configuration.discard_unknown_keys = False
        self.stream_api_client = ApiClient(stream_configuration)
        self.stream_api_client.rest_client.pool_manager = _StreamingPoolManager(
            self.stream_client
        )
        self.stream_invocations = InvocationsApi(self.stream_api_client)
        self.stream_sessions = SessionsApi(self.stream_api_client)

    async def __aenter__(self) -> Client:
        return self

    async def __aexit__(self, *_: object) -> None:
        await self.close()

    async def close(self) -> None:
        await self.api_client.close()
        await self.stream_api_client.close()

    def raw(self) -> tuple[InvocationsApi, ModelsApi, SessionsApi]:
        return self.invocations, self.models, self.sessions

    async def list_models(
        self,
        *,
        provider: str | None = None,
        include_deprecated: bool = False,
    ) -> ModelList:
        generated_provider = _model_provider(provider) if provider is not None else None
        return await self._replay_safe(lambda: self.models.list_models(
            provider=generated_provider,
            include_deprecated=include_deprecated,
        ))

    async def get_model(self, model: Model) -> ModelDescriptor:
        if not model.id:
            raise NvokenError("validation", "model id is required")
        return await self._replay_safe(lambda: self.models.get_model(
            _model_provider(model.provider),
            model.id,
        ))

    async def invoke(self, request: InvokeRequest) -> InvocationHandle:
        if not request.agent_key or not request.input:
            raise NvokenError("validation", "agent_key and input are required")
        idempotency_key = request.idempotency_key or f"nvoken-{uuid.uuid4()}"
        tools: list[GeneratedToolSpec] = []
        for tool in request.spec.tools:
            if tool.mode == "host":
                tools.append(GeneratedToolSpec(HostToolSpec(
                    mode="host",
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
        limits = request.spec.limits
        body = CreateInvocationRequest(
            agent_key=request.agent_key,
            tenant_key=request.tenant_key,
            session_id=request.session_id,
            session_key=request.session_key,
            idempotency_key=idempotency_key,
            input=InvocationInput(request.input),
            spec=InlineExecutionSpec(
                instructions=request.spec.instructions,
                model=ModelSelection(provider=request.spec.model.provider, id=request.spec.model.id),
                limits=InvocationLimitRequest(**vars(limits)) if limits else None,
                tools=tools or None,
                output=StructuredOutputSpec(schema=request.spec.output_schema) if request.spec.output_schema else None,
            ),
            provider_credentials=[
                _provider_credential_selection(selection)
                for selection in request.provider_credentials
            ] or None,
        )
        acknowledgement = await self._replay_safe(lambda: self.invocations.create_invocation(body))
        return InvocationHandle(
            self,
            acknowledgement.invocation_id,
            idempotency_key=idempotency_key,
            session_id=acknowledgement.session_id,
            agent_id=acknowledgement.agent_id,
            status=acknowledgement.status,
            deduplicated=acknowledgement.deduplicated,
            deadline_at=acknowledgement.deadline_at,
        )

    def invocation(self, invocation_id: str) -> InvocationHandle:
        return InvocationHandle(self, invocation_id)

    async def get_invocation(self, invocation_id: str) -> Invocation:
        return await self._replay_safe(lambda: self.invocations.get_invocation(invocation_id))

    async def get_invocation_result(self, invocation_id: str) -> InvocationResult:
        return await self._replay_safe(
            lambda: self.invocations.get_invocation_result(invocation_id)
        )

    async def cancel_invocation(self, invocation_id: str) -> Invocation:
        return await self._replay_safe(lambda: self.invocations.cancel_invocation(invocation_id))

    async def submit_tool_results(
        self,
        invocation_id: str,
        results: list[ToolResult],
    ) -> SubmitHostToolResultsResponse:
        body = SubmitHostToolResultsRequest(results=[
            SubmitHostToolResultsRequestResultsInner(
                tool_call_id=result.tool_call_id,
                content=result.content,
                is_error=result.is_error if result.is_error else None,
            )
            for result in results
        ])
        return await self._replay_safe(
            lambda: self.invocations.submit_host_tool_results(invocation_id, body)
        )

    async def list_invocations(
        self,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        session_id: str | None = None,
        agent_id: str | None = None,
        status: InvocationStatus | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> InvocationList:
        return await self._replay_safe(lambda: self.invocations.list_invocations(
            tenant_key=tenant_key,
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
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        session_id: str | None = None,
        agent_id: str | None = None,
        status: InvocationStatus | None = None,
        limit: int | None = None,
    ) -> AsyncIterator[Invocation]:
        cursor: str | None = None
        while True:
            page = await self.list_invocations(
                tenant_key=tenant_key,
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
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        agent_id: str | None = None,
        session_key: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> SessionList:
        return await self._replay_safe(lambda: self.sessions.list_sessions(
            tenant_key=tenant_key,
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

    async def stream_session(
        self,
        session_id: str,
        reducer: Reducer,
        consume: Callable[[StreamEvent, ReducedSnapshot], Awaitable[None] | None],
    ) -> None:
        await stream_session(self, session_id, reducer, consume)

    async def _replay_safe(self, operation: Callable[[], Awaitable[Any]]) -> Any:
        last_error: NvokenError | None = None
        for attempt in range(1, self.retry.max_attempts + 1):
            try:
                return await operation()
            except asyncio.CancelledError:
                raise
            except (ApiException, httpx.HTTPError) as error:
                last_error = normalize_error(error)
                if attempt == self.retry.max_attempts or not retryable(last_error):
                    raise last_error from error
                exponential = min(
                    self.retry.max_delay,
                    self.retry.min_delay * 2 ** (attempt - 1),
                )
                delay = min(last_error.retry_after, self.retry.max_delay) \
                    if last_error.retry_after is not None \
                    else exponential / 2 + random.random() * exponential / 2
                await asyncio.sleep(delay)
        raise last_error or NvokenError("unexpected_response", "request did not run")


def _model_provider(provider: str) -> ModelProvider:
    try:
        return ModelProvider(provider)
    except ValueError as error:
        raise NvokenError(
            "validation",
            "model provider must be anthropic or openai",
        ) from error


def _provider_credential_selection(
    selection: ProviderCredentialSelection,
) -> InvocationProviderCredentialSelection:
    provider = _model_provider(selection.provider)
    if selection.source == "caller_ephemeral":
        if not selection.api_key:
            raise NvokenError(
                "validation",
                "caller_ephemeral provider credentials require api_key",
            )
        return InvocationProviderCredentialSelection(
            InvocationProviderCredentialSelectionOneOf(
                provider=provider,
                source="caller_ephemeral",
                credential=ProviderStaticCredential(api_key=selection.api_key),
            )
        )
    if selection.api_key is not None:
        raise NvokenError(
            "validation",
            f"{selection.source} provider credentials cannot include api_key",
        )
    return InvocationProviderCredentialSelection(
        InvocationProviderCredentialSelectionOneOf1(
            provider=provider,
            source=selection.source,
        )
    )


@dataclass
class InvocationHandle:
    client: Client = field(repr=False)
    invocation_id: str
    idempotency_key: str | None = None
    session_id: str | None = None
    agent_id: str | None = None
    status: InvocationStatus | None = None
    deduplicated: bool | None = None
    deadline_at: datetime | None = None

    async def refresh(self) -> Invocation:
        invocation = await self.client.get_invocation(self.invocation_id)
        self.session_id = invocation.session_id
        self.agent_id = invocation.agent_id
        self.status = invocation.status
        self.deadline_at = invocation.deadline_at
        return invocation

    async def wait(
        self,
        *,
        min_poll_interval: float = 0.1,
        max_poll_interval: float = 2.0,
    ) -> Invocation:
        delay = min_poll_interval
        try:
            while True:
                invocation = await self.refresh()
                if invocation.status in {"completed", "failed", "cancelled"}:
                    return invocation
                await asyncio.sleep(delay)
                delay = min(delay * 2, max_poll_interval)
        except asyncio.CancelledError:
            raise
        except TimeoutError as error:
            raise NvokenError("timeout", "local wait timed out") from error

    async def result(self) -> InvocationResult:
        """Read the composed InvocationResult at any status: the
        authoritative Invocation, this Invocation's canonical messages, and
        the ``output_text`` projection.
        """
        result = await self.client.get_invocation_result(self.invocation_id)
        self.session_id = result.invocation.session_id
        self.agent_id = result.invocation.agent_id
        self.status = result.invocation.status
        return result

    async def list_messages(self) -> list[SessionMessage]:
        """Return this Invocation's canonical messages from the composed
        result read.
        """
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

    async def submit_tool_results(self, results: list[ToolResult]) -> SubmitHostToolResultsResponse:
        response = await self.client.submit_tool_results(self.invocation_id, results)
        self.status = response.status
        return response

    async def cancel(self) -> Invocation:
        invocation = await self.client.cancel_invocation(self.invocation_id)
        self.session_id = invocation.session_id
        self.agent_id = invocation.agent_id
        self.status = invocation.status
        return invocation

    async def wait_for_action(
        self,
        *,
        min_poll_interval: float = 0.1,
        max_poll_interval: float = 2.0,
    ) -> Invocation:
        delay = min_poll_interval
        try:
            while True:
                invocation = await self.refresh()
                if invocation.status == "waiting" or invocation.status in {
                    "completed", "failed", "cancelled",
                }:
                    return invocation
                await asyncio.sleep(delay)
                delay = min(delay * 2, max_poll_interval)
        except asyncio.CancelledError:
            raise
        except TimeoutError as error:
            raise NvokenError("timeout", "local wait timed out") from error

    async def wait_for_result(
        self,
        *,
        min_poll_interval: float = 0.1,
        max_poll_interval: float = 2.0,
    ) -> InvocationResult:
        invocation = await self.wait(
            min_poll_interval=min_poll_interval,
            max_poll_interval=max_poll_interval,
        )
        if invocation.status != "completed":
            raise NvokenError(
                "conflict",
                f"Invocation {self.invocation_id} ended with status {invocation.status}",
                code=invocation.error.code if invocation.error else None,
                details=invocation.error.details if invocation.error else None,
            )
        return await self.result()

    async def stream(
        self,
        consume: Callable[[StreamEvent], Awaitable[None] | None],
    ) -> None:
        await stream_invocation(self.client, self, consume)


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
        await response.aread()
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
