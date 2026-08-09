from __future__ import annotations

import asyncio
import json
import random
import re
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Any, AsyncIterator, Awaitable, Callable, Literal

import httpx

from nvoken_generated.api.agents_api import AgentsApi
from nvoken_generated.api.budgets_api import BudgetsApi
from nvoken_generated.api.invocations_api import InvocationsApi
from nvoken_generated.api.mcp_api import MCPApi
from nvoken_generated.api.models_api import ModelsApi
from nvoken_generated.api.provider_keys_api import ProviderKeysApi
from nvoken_generated.api.sessions_api import SessionsApi
from nvoken_generated.api_client import ApiClient
from nvoken_generated.configuration import Configuration
from nvoken_generated.exceptions import ApiException
from nvoken_generated.models.agent import Agent as AgentIdentity
from nvoken_generated.models.agent_list import AgentList
from nvoken_generated.models.builtin_tool_declaration import BuiltinToolDeclaration
from nvoken_generated.models.budget import Budget
from nvoken_generated.models.budget_scope import BudgetScope
from nvoken_generated.models.callback_target import CallbackTarget as GeneratedCallbackTarget
from nvoken_generated.models.callback_tool_declaration import CallbackToolDeclaration
from nvoken_generated.models.create_provider_key_request import (
    CreateProviderKeyRequest,
)
from nvoken_generated.models.create_budget_request import CreateBudgetRequest
from nvoken_generated.models.compaction_policy import CompactionPolicy
from nvoken_generated.models.compaction_policy_trigger_tokens import (
    CompactionPolicyTriggerTokens,
)
from nvoken_generated.models.session_options import SessionOptions as GeneratedSessionOptions
from nvoken_generated.models.session_budget import SessionBudget as GeneratedSessionBudget
from nvoken_generated.models.retention_policy import (
    RetentionPolicy as GeneratedRetentionPolicy,
)
from nvoken_generated.models.provider_tool import (
    ProviderTool as GeneratedProviderTool,
)
from nvoken_generated.models.web_search_tool import (
    WebSearchTool as GeneratedWebSearchTool,
)
from nvoken_generated.models.web_search_location import (
    WebSearchLocation as GeneratedWebSearchLocation,
)
from nvoken_generated.models.host_tool_declaration import HostToolDeclaration
from nvoken_generated.models.create_invocation_request import CreateInvocationRequest
from nvoken_generated.models.invocation import Invocation
from nvoken_generated.models.invocation_change import InvocationChange
from nvoken_generated.models.limits import Limits as GeneratedLimits
from nvoken_generated.models.invocation_input import InvocationInput
from nvoken_generated.models.invocation_list import InvocationList
from nvoken_generated.models.webhook_target import (
    WebhookTarget as GeneratedWebhookTarget,
)
from nvoken_generated.models.provider_key_selection import (
    ProviderKeySelection as GeneratedProviderKeySelection,
)
from nvoken_generated.models.provider_key_selection_one_of import (
    ProviderKeySelectionOneOf,
)
from nvoken_generated.models.provider_key_selection_one_of1 import (
    ProviderKeySelectionOneOf1,
)
from nvoken_generated.models.invocation_result import InvocationResult
from nvoken_generated.models.invocation_status import InvocationStatus
from nvoken_generated.models.mcp_list_tools_request import MCPListToolsRequest
from nvoken_generated.models.mcp_list_tools_response import MCPListToolsResponse
from nvoken_generated.models.mcp_server import MCPServer as GeneratedMCPServer
from nvoken_generated.models.mcp_timeouts import MCPTimeouts as GeneratedMCPTimeouts
from nvoken_generated.models.model import Model as GeneratedModel
from nvoken_generated.models.model_input import ModelInput as GeneratedModelInput
from nvoken_generated.models.model_descriptor import ModelDescriptor
from nvoken_generated.models.model_list import ModelList
from nvoken_generated.models.model_tool_choice_mode import ModelToolChoiceMode
from nvoken_generated.models.provider_key import ProviderKey
from nvoken_generated.models.provider_key_list import ProviderKeyList
from nvoken_generated.models.provider_key_scope import ProviderKeyScope
from nvoken_generated.models.provider_key_usage import ProviderKeyUsage
from nvoken_generated.models.provider_static_key import ProviderStaticKey
from nvoken_generated.models.rotate_provider_key_request import (
    RotateProviderKeyRequest,
)
from nvoken_generated.models.sampling import Sampling as GeneratedSampling
from nvoken_generated.models.reasoning_effort import ReasoningEffort
from nvoken_generated.models.reasoning import Reasoning as GeneratedReasoning
from nvoken_generated.models.nudge_acknowledgement import NudgeAcknowledgement
from nvoken_generated.models.nudge_invocation_request import NudgeInvocationRequest
from nvoken_generated.models.pending_input import PendingInput
from nvoken_generated.models.pending_input_list import PendingInputList
from nvoken_generated.models.pending_input_status import PendingInputStatus
from nvoken_generated.models.tool_call_list import ToolCallList
from nvoken_generated.models.session import Session
from nvoken_generated.models.session_compaction import SessionCompaction
from nvoken_generated.models.session_compaction_list import SessionCompactionList
from nvoken_generated.models.session_list import SessionList
from nvoken_generated.models.update_session_request import UpdateSessionRequest
from nvoken_generated.models.update_budget_request import UpdateBudgetRequest
from nvoken_generated.models.session_message import SessionMessage
from nvoken_generated.models.session_message_list import SessionMessageList
from nvoken_generated.models.structured_output import StructuredOutput
from nvoken_generated.models.submit_host_tool_results_request import SubmitHostToolResultsRequest
from nvoken_generated.models.submit_host_tool_results_request_results_inner import (
    SubmitHostToolResultsRequestResultsInner,
)
from nvoken_generated.models.submit_host_tool_results_response import SubmitHostToolResultsResponse
from nvoken_generated.models.tool_choice import ToolChoice as GeneratedToolChoice
from nvoken_generated.models.tool_declaration import ToolDeclaration as GeneratedToolDeclaration
from nvoken_generated.models.transcript_snapshot import TranscriptSnapshot

from .stream import (
    ReducedSnapshot,
    Reducer,
    StreamEvent,
    iter_invocation,
    stream_invocation,
    stream_session,
)
from .media_preflight import (
    MEDIA_PREFLIGHT_CODE,
    DocumentBlock,
    DocumentSource,
    ImageBlock,
    ImageSource,
    InputBlock,
    MediaIssue,
    TextBlock,
    input_block_wire,
    media_input_issue,
)
from .schema_preflight import (
    SCHEMA_PREFLIGHT_CODE,
    SchemaIssue,
    output_schema_issue,
)

IfActivePolicy = Literal["reject", "supersede", "interrupt"]
BudgetExhaustionBehavior = Literal["settle", "pause"]

ErrorCategory = Literal[
    "authentication",
    "permission",
    "validation",
    "not_found",
    "conflict",
    "rate_limit",
    "server",
    "transport",
    "cancelled",
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


def preflight_input(value: str | tuple[InputBlock, ...]) -> None:
    """Reject locally checkable input problems with the Runtime's vocabulary."""
    if isinstance(value, str):
        if not value.strip():
            raise _media_error(
                MediaIssue(
                    code="invalid_media",
                    path="input",
                    message="text must not be blank",
                )
            )
        return
    issue = media_input_issue(value)
    if issue is not None:
        raise _media_error(issue)


def _media_error(issue: MediaIssue) -> NvokenError:
    return NvokenError(
        "validation",
        f"input is invalid: {issue.message}",
        code=MEDIA_PREFLIGHT_CODE,
        details={
            "kind": "input_media",
            "code": issue.code,
            "path": issue.path,
        },
    )


def preflight_output_schema(schema: dict[str, Any]) -> None:
    issue = output_schema_issue(schema)
    if issue is None:
        return
    details: dict[str, Any] = {
        "kind": "structured_output_schema",
        "code": issue.code,
        "path": issue.path,
    }
    if issue.keyword is not None:
        details["keyword"] = issue.keyword
    raise NvokenError(
        "validation",
        f"output schema is invalid: {issue.message}",
        code=SCHEMA_PREFLIGHT_CODE,
        details=details,
    )


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
    handler: Callable[[Any], Awaitable[Any] | Any] | None = field(
        default=None,
        compare=False,
        repr=False,
    )


@dataclass(frozen=True)
class BuiltinTool:
    mode: Literal["builtin"] = "builtin"
    name: Literal["nvoken_fetch"] = "nvoken_fetch"


def fetch_tool() -> BuiltinTool:
    return BuiltinTool()


@dataclass(frozen=True)
class Limits:
    total_timeout_seconds: int | None = None
    active_timeout_seconds: int | None = None
    waiting_timeout_seconds: int | None = None
    max_output_tokens: int | None = None
    max_estimated_cost_usd: float | None = None
    max_iterations: int | None = None


@dataclass(frozen=True)
class Sampling:
    temperature: float


@dataclass(frozen=True)
class Reasoning:
    effort: Literal["low", "medium", "high", "xhigh", "max"] | None = None
    budget_tokens: int | None = None


@dataclass(frozen=True)
class ContextCompaction:
    trigger_tokens: int | Literal["auto"]
    model: Model | None = None


@dataclass(frozen=True)
class SessionRetention:
    """Bounds how long an idle Session is retained.

    The window measures idle time rather than lifetime: it restarts on every
    Invocation admission and settlement, so a turn outlasting the window cannot
    expire underneath itself. Automatic expiry never cancels running work.
    """

    # Idle window in seconds, from one hour to thirty days.
    ttl_seconds: int


@dataclass(frozen=True)
class SessionOptions:
    """Durable Session options.

    Every member is optional and at least one must be present. Existing values
    are comparison-only: equal is accepted and different returns
    ``session_options_conflict``.
    """

    # Requires an Invocation because the policy is validated against that
    # turn's model. It may be installed on any Session that has no policy yet,
    # but create_session still cannot set it.
    compaction: ContextCompaction | None = None
    retention: SessionRetention | None = None
    budget: SessionBudget | None = None
    metadata: dict[str, str] | None = None


@dataclass(frozen=True)
class SessionBudget:
    """Mutable Session-wide USD list-price guardrail, not a billing ledger."""

    max_estimated_cost_usd: float


@dataclass(frozen=True)
class WebSearchLocation:
    city: str | None = None
    region: str | None = None
    country: str | None = None
    timezone: str | None = None


@dataclass(frozen=True)
class WebSearchTool:
    """Anthropic web search options, passed through as the provider defines them."""

    # Searches this turn may run, 1 to 20. The only bound nvoken can place on
    # search spend: the provider reports no per-search fee it could meter, so
    # search charges ride the provider's bill outside nvoken's cost estimate.
    max_uses: int | None = None
    # Restrict results to these hosts. Bare hostnames only — a scheme, path, or
    # port is rejected rather than reinterpreted. Mutually exclusive with
    # blocked_domains, which is the provider's rule.
    allowed_domains: tuple[str, ...] = ()
    blocked_domains: tuple[str, ...] = ()
    # Biases results. Every member is optional; the host decides how precise to
    # be about its end user.
    user_location: WebSearchLocation | None = None


@dataclass(frozen=True)
class ProviderTool:
    """Selects one provider server-side tool.

    Web search is Anthropic only for now, and a model that does not declare
    ``controls.tools.web_search`` is refused at admission rather than served a
    search the provider would ignore.
    """

    web_search: WebSearchTool = field(default_factory=WebSearchTool)
    type: Literal["web_search"] = "web_search"


def web_search_tool(options: WebSearchTool | None = None) -> ProviderTool:
    """Anthropic server-side web search with default options."""
    return ProviderTool(web_search=options or WebSearchTool())


@dataclass(frozen=True)
class ToolChoice:
    mode: Literal["auto", "none", "required", "named"]
    name: str | None = None


@dataclass(frozen=True)
class MCPTimeouts:
    discovery_seconds: int | None = None
    call_seconds: int | None = None


@dataclass(frozen=True)
class MCPServer:
    name: str
    url: str
    transport: Literal["streamable_http"] = "streamable_http"
    allowed_tools: tuple[str, ...] = ()
    headers: dict[str, str] = field(default_factory=dict, repr=False)
    timeouts: MCPTimeouts | None = None


WebhookEvent = Literal["invocation.waiting", "invocation.settled"]


@dataclass(frozen=True)
class WebhookTarget:
    """Endpoint nvoken posts a signed webhook to when an Invocation parks
    awaiting host tool results or reaches a terminal status.

    Leaving ``events`` empty selects every event, which is the safe default:
    dropping ``invocation.waiting`` would leave a parked host tool loop with
    nobody listening. The payload carries identifiers and status only, so
    authoritative state is still read through the API.
    """

    url: str
    events: tuple[WebhookEvent, ...] = ()


@dataclass(frozen=True)
class ProviderKeySelection:
    provider: str
    source: Literal["caller_ephemeral", "app_byok", "tenant_byok", "platform"]
    api_key: str | None = None


@dataclass(frozen=True)
class InvokeRequest:
    agent_key: str
    input: str | tuple[InputBlock, ...]
    """Text shorthand, or ordered blocks mixing text, images, and documents."""
    model: Model | None = None
    definition_id: str | None = None
    instructions: str | None = None
    sampling: Sampling | None = None
    reasoning: Reasoning | None = None
    tool_choice: ToolChoice | None = None
    limits: Limits | None = None
    tools: tuple[Tool | BuiltinTool, ...] = ()
    mcp_servers: tuple[MCPServer, ...] = ()
    provider_tools: tuple[ProviderTool, ...] = ()
    output_schema: dict[str, Any] | None = None
    idempotency_key: str | None = None
    if_active: IfActivePolicy | None = None
    on_budget_exhausted: BudgetExhaustionBehavior | None = None
    tenant_key: str | None = None
    session_id: str | None = None
    session_key: str | None = None
    session_options: SessionOptions | None = None
    provider_keys: tuple[ProviderKeySelection, ...] = ()
    webhook: WebhookTarget | None = None
    # Opaque host correlation data recorded on this Invocation. It is part of
    # the admitted input, so it is immutable and material to idempotency: a
    # replay carrying different metadata conflicts rather than updating it.
    # Session metadata is separate and mutable — see SessionOptions.metadata
    # and Client.update_session.
    metadata: dict[str, str] | None = None


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


@dataclass(frozen=True)
class TranscriptDrain:
    messages: list[SessionMessage]
    invocation_changes: list[InvocationChange]
    resume_cursor: str


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
        default_model: Model | None = None,
    ) -> None:
        if not base_url or not api_key:
            raise NvokenError("validation", "base_url and api_key are required")
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.retry = retry
        self.default_model = default_model
        self._session_locks: dict[str, asyncio.Lock] = {}
        self._background_tasks: set[asyncio.Task[Any]] = set()
        configuration = Configuration(host=self.base_url, access_token=api_key)
        configuration.discard_unknown_keys = False
        self.api_client = ApiClient(configuration)
        self.agents = AgentsApi(self.api_client)
        self.budgets = BudgetsApi(self.api_client)
        self.invocations = InvocationsApi(self.api_client)
        self.mcp = MCPApi(self.api_client)
        self.models = ModelsApi(self.api_client)
        self.provider_keys = ProviderKeysApi(self.api_client)
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

    def raw(
        self,
    ) -> tuple[InvocationsApi, ModelsApi, ProviderKeysApi, SessionsApi, AgentsApi, BudgetsApi]:
        return (
            self.invocations,
            self.models,
            self.provider_keys,
            self.sessions,
            self.agents,
            self.budgets,
        )

    def agent(self, options: AgentOptions) -> Agent:
        from .agent import Agent

        return Agent(self, options)

    async def get_agent_identity(self, agent_id: str) -> AgentIdentity:
        return await self._replay_safe(lambda: self.agents.get_agent(agent_id))

    async def list_agent_identities(
        self,
        *,
        agent_key: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> AgentList:
        return await self._replay_safe(lambda: self.agents.list_agents(
            agent_key=agent_key,
            cursor=cursor,
            limit=limit,
        ))

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

    async def list_mcp_tools(self, server: MCPServer) -> MCPListToolsResponse:
        return await self._replay_safe(lambda: self.mcp.list_mcp_tools(
            MCPListToolsRequest(server=_generated_mcp_server(server))
        ))

    async def get_model(self, model: Model) -> ModelDescriptor:
        if not model.id:
            raise NvokenError("validation", "model id is required")
        return await self._replay_safe(lambda: self.models.get_model(
            _model_provider(model.provider),
            model.id,
        ))

    async def invoke(self, request: InvokeRequest) -> InvocationHandle:
        body = self._invocation_body(request)
        idempotency_key = body.idempotency_key
        invocation = await self._replay_safe(lambda: self.invocations.create_invocation(body))
        return InvocationHandle(
            self,
            invocation.id,
            idempotency_key=idempotency_key,
            session_id=invocation.session_id,
            agent_id=invocation.agent_id,
            status=invocation.status,
            deduplicated=bool(invocation.deduplicated),
            deadline_at=invocation.deadline_at,
        )

    def _invocation_body(self, request: InvokeRequest) -> CreateInvocationRequest:
        if not request.agent_key or not request.input:
            raise NvokenError("validation", "agent_key and input are required")
        preflight_input(request.input)
        has_inline_definition = any((
            request.model is not None,
            request.instructions is not None,
            request.sampling is not None,
            request.reasoning is not None,
            request.tool_choice is not None,
            request.limits is not None,
            bool(request.tools),
            bool(request.mcp_servers),
            bool(request.provider_tools),
            request.output_schema is not None,
        ))
        if bool(request.definition_id) == has_inline_definition:
            raise NvokenError(
                "validation",
                "request requires exactly one of definition_id or an inline definition",
            )
        if request.definition_id is None and request.model is None:
            raise NvokenError("validation", "inline definition requires model")
        if request.if_active not in (None, "reject", "supersede", "interrupt"):
            raise NvokenError(
                "validation",
                "if_active must be reject, supersede, or interrupt",
            )
        if request.on_budget_exhausted not in (None, "settle", "pause"):
            raise NvokenError(
                "validation",
                "on_budget_exhausted must be settle or pause",
            )
        if request.output_schema is not None:
            preflight_output_schema(request.output_schema)
        idempotency_key = request.idempotency_key or f"nvoken-{uuid.uuid4()}"
        tools: list[GeneratedToolDeclaration] = []
        for tool in request.tools:
            if isinstance(tool, BuiltinTool):
                tools.append(GeneratedToolDeclaration(BuiltinToolDeclaration(
                    mode="builtin",
                    name="nvoken_fetch",
                )))
                continue
            if tool.mode == "host":
                if tool.callback_url:
                    raise NvokenError(
                        "validation",
                        f"host tool {tool.name} cannot include callback_url",
                    )
                tools.append(GeneratedToolDeclaration(HostToolDeclaration(
                    mode="host",
                    name=tool.name,
                    description=tool.description,
                    input_schema=tool.input_schema,
                )))
            else:
                if not tool.callback_url:
                    raise NvokenError(
                        "validation",
                        f"callback tool {tool.name} requires callback_url",
                    )
                if tool.handler is not None:
                    raise NvokenError(
                        "validation",
                        f"callback tool {tool.name} cannot include a local handler",
                    )
                tools.append(GeneratedToolDeclaration(CallbackToolDeclaration(
                    mode="callback",
                    name=tool.name,
                    description=tool.description,
                    input_schema=tool.input_schema,
                    callback=GeneratedCallbackTarget(url=tool.callback_url),
                )))
        limits = request.limits
        return CreateInvocationRequest(
            agent_key=request.agent_key,
            tenant_key=request.tenant_key,
            session_id=request.session_id,
            session_key=request.session_key,
            session_options=_generated_session_options(request.session_options),
            metadata=dict(request.metadata) if request.metadata else None,
            idempotency_key=idempotency_key,
            if_active=request.if_active,
            on_budget_exhausted=request.on_budget_exhausted,
            input=InvocationInput(
                request.input
                if isinstance(request.input, str)
                else [input_block_wire(block) for block in request.input]
            ),
            definition_id=request.definition_id,
            instructions=request.instructions,
            model=GeneratedModelInput(GeneratedModel(
                provider=request.model.provider,
                id=request.model.id,
            )) if request.model is not None else None,
            sampling=GeneratedSampling(temperature=request.sampling.temperature)
            if request.sampling is not None
            else None,
            reasoning=GeneratedReasoning(
                    effort=ReasoningEffort(request.reasoning.effort)
                    if request.reasoning.effort is not None
                    else None,
                    budget_tokens=request.reasoning.budget_tokens,
                )
                if request.reasoning is not None
                else None,
            tool_choice=GeneratedToolChoice(
                    mode=ModelToolChoiceMode(request.tool_choice.mode),
                    name=request.tool_choice.name,
                )
                if request.tool_choice is not None
                else None,
            limits=GeneratedLimits(**vars(limits)) if limits else None,
            tools=tools or None,
            mcp_servers=[
                    _generated_mcp_server(server)
                    for server in request.mcp_servers
                ] or None,
            provider_tools=[
                    _generated_provider_tool(tool)
                    for tool in request.provider_tools
                ] or None,
            structured_output=StructuredOutput(schema=request.output_schema)
                if request.output_schema is not None
                else None,
            provider_keys=[
                _provider_key_selection(selection)
                for selection in request.provider_keys
            ] or None,
            webhook=_generated_webhook_target(request.webhook),
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

    async def interrupt_invocation(self, invocation_id: str) -> Invocation:
        """Stop an Invocation gracefully and keep its work.

        The turn settles ``completed`` with stop reason ``interrupted`` once it
        reaches an execution seam, so the messages it already produced stay in
        the Session. :meth:`cancel_invocation` is the discarding stop.
        """
        return await self._replay_safe(
            lambda: self.invocations.interrupt_invocation(invocation_id)
        )

    async def nudge_invocation(
        self,
        invocation_id: str,
        content: str,
        *,
        idempotency_key: str | None = None,
    ) -> NudgeAcknowledgement:
        """Append steering to a running Invocation without ending it.

        The turn keeps everything it has already produced — the difference from
        supersession, which rewinds — and the model sees the input at its next
        execution seam rather than immediately. Input the turn never reaches is
        settled ``expired`` when the Invocation settles; nvoken never re-homes
        it onto a later turn, so re-sending missed direction as the next
        Invocation's input is the caller's decision.

        Passing ``idempotency_key`` makes a retry safe: the same key with the
        same content returns the original acknowledgement with ``deduped``
        set, and the same key with different content is refused.
        """
        body = NudgeInvocationRequest(
            content=InvocationInput(content),
            idempotency_key=idempotency_key,
        )
        call = lambda: self.invocations.nudge_invocation(invocation_id, body)
        if idempotency_key is None:
            # Without a key a retried POST would stage the same direction twice.
            return await call()
        return await self._replay_safe(call)

    async def list_pending_inputs(
        self,
        invocation_id: str,
        *,
        status: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> PendingInputList:
        """Read the staged queue in the order the turn will consume it."""
        return await self._replay_safe(
            lambda: self.invocations.list_pending_inputs(
                invocation_id,
                status=PendingInputStatus(status) if status is not None else None,
                cursor=cursor,
                limit=limit,
            )
        )

    async def list_tool_calls(
        self,
        invocation_id: str,
        *,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> ToolCallList:
        """Read durable ToolCall lifecycle records in discovery order."""
        return await self._replay_safe(
            lambda: self.invocations.list_tool_calls(
                invocation_id,
                cursor=cursor,
                limit=limit,
            )
        )

    async def cancel_pending_input(
        self,
        invocation_id: str,
        pending_input_id: str,
    ) -> PendingInput:
        """Withdraw staged input the turn has not taken.

        Input the executor already drained is reported as a conflict rather
        than removed from a transcript it is already part of.
        """
        return await self._replay_safe(
            lambda: self.invocations.cancel_pending_input(invocation_id, pending_input_id)
        )

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
        agent_key: str | None = None,
        status: InvocationStatus | list[InvocationStatus] | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> InvocationList:
        return await self._replay_safe(lambda: self.invocations.list_invocations(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            session_id=session_id,
            agent_id=agent_id,
            agent_key=agent_key,
            status=(
                status
                if isinstance(status, list)
                else [status] if status is not None else None
            ),
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
        agent_key: str | None = None,
        status: InvocationStatus | list[InvocationStatus] | None = None,
        limit: int | None = None,
    ) -> AsyncIterator[Invocation]:
        cursor: str | None = None
        while True:
            page = await self.list_invocations(
                tenant_key=tenant_key,
                default_tenant=default_tenant,
                session_id=session_id,
                agent_id=agent_id,
                agent_key=agent_key,
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
        agent_key: str | None = None,
        session_key: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> SessionList:
        return await self._replay_safe(lambda: self.sessions.list_sessions(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            agent_id=agent_id,
            agent_key=agent_key,
            session_key=session_key,
            cursor=cursor,
            limit=limit,
        ))

    async def session_items(
        self,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        agent_id: str | None = None,
        agent_key: str | None = None,
        session_key: str | None = None,
        limit: int | None = None,
    ) -> AsyncIterator[Session]:
        cursor: str | None = None
        while True:
            page = await self.list_sessions(
                tenant_key=tenant_key,
                default_tenant=default_tenant,
                agent_id=agent_id,
                agent_key=agent_key,
                session_key=session_key,
                cursor=cursor,
                limit=limit,
            )
            for item in page.items:
                yield item
            cursor = page.next_cursor
            if not cursor:
                return

    async def get_session(self, session_id: str) -> Session:
        return await self._replay_safe(lambda: self.sessions.get_session(session_id))

    async def delete_session(self, session_id: str) -> None:
        """Erase a Session and everything under it.

        Removes its Invocations, transcript, checkpoints, tool calls,
        artifacts, and undelivered webhooks. The erasure is immediate and
        irreversible.

        A running Invocation is stopped, and no cancellation is recorded — the
        Invocation is removed rather than settled, so no ``invocation.settled``
        webhook is emitted for it. Cancel first if you need a settled
        record.

        An unknown or out-of-scope Session is not found, so a retry after a
        lost response can treat that as already-done.

        This is not account erasure by itself: nvoken keeps no account
        tombstone, so a caller honouring a deletion request must stop admitting
        work for the tenant before paginating and deleting.
        """
        # Deletion is idempotent by shape — a repeat is not-found rather than a
        # second erasure — so it is safe to replay.
        await self._replay_safe(lambda: self.sessions.delete_session(session_id))

    async def update_session(
        self,
        session_id: str,
        metadata: dict[str, str | None],
    ) -> Session:
        """Merge a metadata patch into a Session.

        A present key replaces, an explicit ``None`` deletes, and an unmentioned
        key survives. Merging rather than replacing is what stops independent
        writers — a title UI and correlation tooling — from silently discarding
        each other's keys.
        """
        body = UpdateSessionRequest(metadata=metadata)
        return await self._replay_safe(
            lambda: self.sessions.update_session(session_id, body)
        )

    async def get_session_by_key(
        self,
        session_key: str,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        agent_id: str | None = None,
        agent_key: str | None = None,
    ) -> Session:
        page = await self.list_sessions(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            agent_id=agent_id,
            agent_key=agent_key,
            session_key=session_key,
            limit=2,
        )
        if not page.items:
            raise NvokenError(
                "not_found",
                f"Session key {session_key!r} was not found",
            )
        if len(page.items) > 1:
            raise NvokenError(
                "conflict",
                f"Session key {session_key!r} matched more than one Session",
            )
        return page.items[0]

    async def list_session_messages(
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

    async def session_message_items(
        self,
        session_id: str,
        *,
        limit: int | None = None,
    ) -> AsyncIterator[SessionMessage]:
        cursor: str | None = None
        while True:
            page = await self.list_session_messages(
                session_id,
                cursor=cursor,
                limit=limit,
            )
            for item in page.items:
                yield item
            cursor = page.next_cursor
            if not cursor:
                return

    async def list_session_compactions(
        self,
        session_id: str,
        *,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> SessionCompactionList:
        """Return newest-first immutable compaction diagnostics."""
        return await self._replay_safe(
            lambda: self.sessions.list_session_compactions(
                session_id,
                cursor=cursor,
                limit=limit,
            )
        )

    async def session_compaction_items(
        self,
        session_id: str,
        *,
        limit: int | None = None,
    ) -> AsyncIterator[SessionCompaction]:
        cursor: str | None = None
        while True:
            page = await self.list_session_compactions(
                session_id,
                cursor=cursor,
                limit=limit,
            )
            for item in page.items:
                yield item
            cursor = page.next_cursor
            if not cursor:
                return

    async def get_transcript_page(
        self,
        session_id: str,
        *,
        cursor: str | None = None,
        page_token: str | None = None,
        limit: int | None = None,
    ) -> TranscriptSnapshot:
        return await self._replay_safe(
            lambda: self.sessions.get_session_transcript(
                session_id,
                cursor=cursor,
                page_token=page_token,
                limit=limit,
            )
        )

    async def drain_transcript(
        self,
        session_id: str,
        *,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> TranscriptDrain:
        messages: list[SessionMessage] = []
        changes: list[InvocationChange] = []
        page_token: str | None = None
        resume_cursor: str | None = None
        while True:
            page = await self.get_transcript_page(
                session_id,
                cursor=cursor if page_token is None else None,
                page_token=page_token,
                limit=limit,
            )
            messages.extend(page.messages)
            changes.extend(page.invocation_changes)
            resume_cursor = page.resume_cursor
            page_token = page.next_page_token
            if not page.has_more:
                if not resume_cursor:
                    raise NvokenError(
                        "unexpected_response",
                        "Transcript drain did not return a resume cursor",
                    )
                return TranscriptDrain(
                    messages=messages,
                    invocation_changes=changes,
                    resume_cursor=resume_cursor,
                )
            if not page_token:
                raise NvokenError(
                    "unexpected_response",
                    "Transcript page has_more without next_page_token",
                )

    async def create_provider_key(
        self,
        *,
        provider: str,
        scope: Literal["app", "tenant"],
        api_key: str,
        tenant_key: str | None = None,
        expires_at: datetime | None = None,
        idempotency_key: str | None = None,
    ) -> ProviderKey:
        body = CreateProviderKeyRequest(
            provider=_model_provider(provider),
            scope=ProviderKeyScope(scope),
            tenant_key=tenant_key,
            key=ProviderStaticKey(api_key=api_key),
            expires_at=expires_at,
            idempotency_key=idempotency_key or f"nvoken-{uuid.uuid4()}",
        )
        return await self._replay_safe(
            lambda: self.provider_keys.create_provider_key(body)
        )

    async def get_provider_key(
        self,
        provider_key_id: str,
    ) -> ProviderKey:
        return await self._replay_safe(
            lambda: self.provider_keys.get_provider_key(
                provider_key_id
            )
        )

    async def get_provider_key_usage(
        self,
        provider_key_id: str,
    ) -> ProviderKeyUsage:
        return await self._replay_safe(
            lambda: self.provider_keys.get_provider_key_usage(
                provider_key_id
            )
        )

    async def list_provider_keys(
        self,
        *,
        provider: str | None = None,
        scope: Literal["app", "tenant"] | None = None,
        status: str | None = None,
        tenant_key: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> ProviderKeyList:
        return await self._replay_safe(
            lambda: self.provider_keys.list_provider_keys(
                provider=_model_provider(provider) if provider is not None else None,
                scope=ProviderKeyScope(scope) if scope is not None else None,
                status=status,
                tenant_key=tenant_key,
                cursor=cursor,
                limit=limit,
            )
        )

    async def provider_key_items(
        self,
        *,
        provider: str | None = None,
        scope: Literal["app", "tenant"] | None = None,
        status: str | None = None,
        tenant_key: str | None = None,
        limit: int | None = None,
    ) -> AsyncIterator[ProviderKey]:
        cursor: str | None = None
        while True:
            page = await self.list_provider_keys(
                provider=provider,
                scope=scope,
                status=status,
                tenant_key=tenant_key,
                cursor=cursor,
                limit=limit,
            )
            for item in page.items:
                yield item
            cursor = page.next_cursor
            if not cursor:
                return

    async def rotate_provider_key(
        self,
        provider_key_id: str,
        *,
        api_key: str,
        expires_at: datetime | None = None,
        overlap_seconds: int = 0,
        idempotency_key: str | None = None,
    ) -> ProviderKey:
        body = RotateProviderKeyRequest(
            key=ProviderStaticKey(api_key=api_key),
            expires_at=expires_at,
            overlap_seconds=overlap_seconds,
            idempotency_key=idempotency_key or f"nvoken-{uuid.uuid4()}",
        )
        return await self._replay_safe(
            lambda: self.provider_keys.rotate_provider_key(
                provider_key_id,
                body,
            )
        )

    async def revoke_provider_key(
        self,
        provider_key_id: str,
    ) -> ProviderKey:
        return await self._replay_safe(
            lambda: self.provider_keys.revoke_provider_key(
                provider_key_id
            )
        )

    async def create_budget(
        self,
        *,
        scope: Literal["app", "customer", "user", "agent", "provider_key", "api_credential"],
        window_start: datetime,
        window_end: datetime,
        max_estimated_cost_usd: float,
        tenant_key: str | None = None,
        default_tenant: bool = False,
        user_key: str | None = None,
        agent_id: str | None = None,
        provider_key_id: str | None = None,
        credential_id: str | None = None,
        idempotency_key: str | None = None,
    ) -> Budget:
        body = CreateBudgetRequest(
            scope=BudgetScope(scope),
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            agent_id=agent_id,
            provider_key_id=provider_key_id,
            credential_id=credential_id,
            window_start=window_start,
            window_end=window_end,
            max_estimated_cost_usd=max_estimated_cost_usd,
            idempotency_key=idempotency_key or f"nvoken-{uuid.uuid4()}",
        )
        return await self._replay_safe(lambda: self.budgets.create_budget(body))

    async def get_budget(self, budget_id: str) -> Budget:
        return await self._replay_safe(lambda: self.budgets.get_budget(budget_id))

    async def update_budget(
        self,
        budget_id: str,
        max_estimated_cost_usd: float,
    ) -> Budget:
        return await self._replay_safe(
            lambda: self.budgets.update_budget(
                budget_id,
                UpdateBudgetRequest(max_estimated_cost_usd=max_estimated_cost_usd),
            )
        )

    async def delete_budget(self, budget_id: str) -> None:
        await self._replay_safe(lambda: self.budgets.delete_budget(budget_id))

    async def stream_session(
        self,
        session_id: str,
        reducer: Reducer,
        consume: Callable[[StreamEvent, ReducedSnapshot], Awaitable[None] | None],
        *,
        deltas: bool = True,
    ) -> None:
        await stream_session(self, session_id, reducer, consume, deltas=deltas)

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


def _generated_mcp_server(server: MCPServer) -> GeneratedMCPServer:
    timeouts = server.timeouts
    return GeneratedMCPServer(
        name=server.name,
        url=server.url,
        transport=server.transport,
        allowed_tools=list(server.allowed_tools) or None,
        headers=dict(server.headers) or None,
        timeouts=GeneratedMCPTimeouts(
            discovery_seconds=timeouts.discovery_seconds,
            call_seconds=timeouts.call_seconds,
        ) if timeouts else None,
    )


def _generated_session_options(
    options: SessionOptions | None,
) -> GeneratedSessionOptions | None:
    if options is None:
        return None
    if (
        options.compaction is None
        and options.retention is None
        and options.budget is None
        and not options.metadata
    ):
        raise NvokenError("validation", "session_options requires at least one member")
    compaction = options.compaction
    return GeneratedSessionOptions(
        compaction=CompactionPolicy(
            trigger_tokens=CompactionPolicyTriggerTokens(compaction.trigger_tokens),
            model=GeneratedModel(
                provider=compaction.model.provider,
                id=compaction.model.id,
            ) if compaction.model is not None else None,
        ) if compaction is not None else None,
        retention=GeneratedRetentionPolicy(ttl_seconds=options.retention.ttl_seconds)
        if options.retention is not None
        else None,
        budget=GeneratedSessionBudget(
            max_estimated_cost_usd=options.budget.max_estimated_cost_usd,
        ) if options.budget is not None else None,
        metadata=dict(options.metadata) if options.metadata else None,
    )


def _generated_provider_tool(tool: ProviderTool) -> GeneratedProviderTool:
    search = tool.web_search
    location = search.user_location
    return GeneratedProviderTool(
        type=tool.type,
        web_search=GeneratedWebSearchTool(
            max_uses=search.max_uses,
            allowed_domains=list(search.allowed_domains) or None,
            blocked_domains=list(search.blocked_domains) or None,
            user_location=GeneratedWebSearchLocation(
                city=location.city,
                region=location.region,
                country=location.country,
                timezone=location.timezone,
            ) if location is not None else None,
        ),
    )


def _model_provider(provider: str) -> str:
    if re.fullmatch(r"[a-z][a-z0-9_]*", provider) is None:
        raise NvokenError(
            "validation",
            "model provider must be a valid canonical identifier",
        )
    return provider


def _generated_webhook_target(
    target: WebhookTarget | None,
) -> GeneratedWebhookTarget | None:
    if target is None:
        return None
    if not target.url:
        raise NvokenError("validation", "webhook.url is required")
    # An empty tuple stays absent on the wire. The Runtime applies the
    # complete-set default, and an empty array is a rejected request, so
    # materializing the default here would change what a replay fingerprints
    # against.
    return GeneratedWebhookTarget(
        url=target.url,
        events=list(target.events) or None,
    )


def _provider_key_selection(
    selection: ProviderKeySelection,
) -> GeneratedProviderKeySelection:
    provider = _model_provider(selection.provider)
    if selection.source == "caller_ephemeral":
        if not selection.api_key:
            raise NvokenError(
                "validation",
                "caller_ephemeral provider keys require api_key",
            )
        return GeneratedProviderKeySelection(
            ProviderKeySelectionOneOf(
                provider=provider,
                source="caller_ephemeral",
                key=ProviderStaticKey(api_key=selection.api_key),
            )
        )
    if selection.api_key is not None:
        raise NvokenError(
            "validation",
            f"{selection.source} provider keys cannot include api_key",
        )
    return GeneratedProviderKeySelection(
        ProviderKeySelectionOneOf1(
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
        until: Literal["terminal", "actionable"] | set[InvocationStatus] = "terminal",
        timeout: float | None = None,
        min_poll_interval: float = 0.1,
        max_poll_interval: float = 2.0,
    ) -> Invocation:
        if min_poll_interval <= 0 or max_poll_interval < min_poll_interval:
            raise NvokenError("validation", "invalid polling interval")
        if timeout is not None and timeout <= 0:
            raise NvokenError("validation", "timeout must be greater than zero")
        delay = min_poll_interval
        started_at = asyncio.get_running_loop().time()
        try:
            while True:
                invocation = await self.refresh()
                if _wait_satisfied(invocation.status, until):
                    return invocation
                if (
                    timeout is not None
                    and asyncio.get_running_loop().time() - started_at >= timeout
                ):
                    raise NvokenError(
                        "timeout",
                        f"Local wait for Invocation {self.invocation_id} timed out",
                    )
                await asyncio.sleep(delay)
                delay = min(delay * 2, max_poll_interval)
        except asyncio.CancelledError:
            raise

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

    async def output_text(self) -> str:
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

    async def interrupt(self) -> Invocation:
        invocation = await self.client.interrupt_invocation(self.invocation_id)
        self.session_id = invocation.session_id
        self.agent_id = invocation.agent_id
        self.status = invocation.status
        return invocation

    async def nudge(
        self,
        content: str,
        *,
        idempotency_key: str | None = None,
    ) -> NudgeAcknowledgement:
        """Append steering to this running turn.

        Not an interrupt: the model sees the input at the next execution seam,
        and nothing in flight is aborted for it.
        """
        return await self.client.nudge_invocation(
            self.invocation_id,
            content,
            idempotency_key=idempotency_key,
        )

    async def list_pending_inputs(
        self,
        *,
        status: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> PendingInputList:
        return await self.client.list_pending_inputs(
            self.invocation_id,
            status=status,
            cursor=cursor,
            limit=limit,
        )

    async def list_tool_calls(
        self,
        *,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> ToolCallList:
        return await self.client.list_tool_calls(
            self.invocation_id,
            cursor=cursor,
            limit=limit,
        )

    async def cancel_pending_input(self, pending_input_id: str) -> PendingInput:
        return await self.client.cancel_pending_input(self.invocation_id, pending_input_id)

    async def wait_for_action(
        self,
        *,
        timeout: float | None = None,
        min_poll_interval: float = 0.1,
        max_poll_interval: float = 2.0,
    ) -> Invocation:
        return await self.wait(
            until="actionable",
            timeout=timeout,
            min_poll_interval=min_poll_interval,
            max_poll_interval=max_poll_interval,
        )

    async def wait_for_result(
        self,
        *,
        timeout: float | None = None,
        min_poll_interval: float = 0.1,
        max_poll_interval: float = 2.0,
    ) -> InvocationResult:
        invocation = await self.wait(
            timeout=timeout,
            min_poll_interval=min_poll_interval,
            max_poll_interval=max_poll_interval,
        )
        if invocation.status != "completed":
            raise NvokenError(
                "conflict",
                ended_message(self.invocation_id, invocation),
                code=invocation.error.code if invocation.error else None,
                details=invocation.error.details if invocation.error else None,
            )
        return await self.result()

    async def stream(
        self,
        consume: Callable[[StreamEvent], Awaitable[None] | None],
        *,
        deltas: bool = True,
    ) -> None:
        await stream_invocation(self.client, self, consume, deltas=deltas)

    def events(self, *, deltas: bool = True) -> AsyncIterator[StreamEvent]:
        return iter_invocation(self.client, self, deltas=deltas)


# `incomplete` is terminal: a turn the runtime cut off at a budget is over, and
# a wait helper that treated only `completed` as an ending would poll it forever.
TERMINAL_STATUSES = frozenset({"completed", "incomplete", "failed", "cancelled"})


def ended_message(invocation_id: str, invocation: Invocation) -> str:
    """Explain an ending that was not the answer asked for.

    An ``incomplete`` turn carries no error, so its stop reason is the only
    thing that names the budget that stopped it.
    """
    status = invocation.status.value
    if invocation.stop_reason is None:
        return f"Invocation {invocation_id} ended with status {status}"
    return f"Invocation {invocation_id} ended with status {status} ({invocation.stop_reason.value})"


def _wait_satisfied(
    status: InvocationStatus,
    until: Literal["terminal", "actionable"] | set[InvocationStatus],
) -> bool:
    if until == "terminal":
        return status in TERMINAL_STATUSES
    if until == "actionable":
        return status in TERMINAL_STATUSES | {"waiting"}
    return status in until


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
        "authentication" if status == 401
        else "permission" if status == 403
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
        "authentication" if status == 401
        else "permission" if status == 403
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
