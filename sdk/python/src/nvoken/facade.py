"""Idiomatic high-level facade for Agents, Conversations, and Turns."""

from __future__ import annotations

import asyncio
import inspect
import json
import time
import uuid
from dataclasses import dataclass, field
from datetime import datetime
from typing import (
    TYPE_CHECKING,
    Any,
    AsyncIterator,
    Awaitable,
    Callable,
    Literal,
    Mapping,
    TypeAlias,
    TypeGuard,
)
from httpx import TimeoutException, TransportError

from nvoken_generated.api import (
    AdmissionsApi, AgentsApi, AppsApi, ConsoleIntegrationApi, ConversationsApi,
    CreditsApi, IdentityApi, MCPApi, MemorySpacesApi, ModelsApi, OperationsApi,
    OrgsApi, ProviderKeysApi, TenantsApi, TurnsApi, UsageApi,
)
from nvoken_generated.api_client import ApiClient
from nvoken_generated.configuration import Configuration
from nvoken_generated.exceptions import ApiException
from nvoken_generated.models.agent import Agent as AgentResource
from nvoken_generated.models.agent_owner_kind import AgentOwnerKind
from nvoken_generated.models.behavior_input import BehaviorInput as GeneratedBehaviorInput
from nvoken_generated.models.create_agent_request import CreateAgentRequest
from nvoken_generated.models.create_turn_request import CreateTurnRequest
from nvoken_generated.models.allocate_credits_request import AllocateCreditsRequest
from nvoken_generated.models.anonymous_token_request import AnonymousTokenRequest
from nvoken_generated.models.create_credential_request import CreateCredentialRequest
from nvoken_generated.models.create_provider_key_request import CreateProviderKeyRequest
from nvoken_generated.models.register_org_request import RegisterOrgRequest
from nvoken_generated.models.rotate_credential_request import RotateCredentialRequest
from nvoken_generated.models.rotate_provider_key_request import RotateProviderKeyRequest
from nvoken_generated.models.submit_host_tool_results_request import SubmitHostToolResultsRequest
from nvoken_generated.models.turn_result import TurnResult as TurnResultResource
from nvoken_generated.models.turn_status import TurnStatus
from nvoken_generated.models.conversation_message import ConversationMessage
from nvoken_generated.models.update_org_request import UpdateOrgRequest

if TYPE_CHECKING:
    from .stream import StreamEvent

ErrorCategory = Literal[
    "authentication", "permission", "validation", "not_found", "conflict",
    "rate_limit", "server", "transport", "cancelled", "timeout",
    "unexpected_response",
]
TERMINAL_STATUSES = {"completed", "incomplete", "failed", "cancelled"}


class NvokenError(Exception):
    def __init__(self, category: ErrorCategory, message: str, *, status: int | None = None,
                 code: str | None = None, request_id: str | None = None,
                 details: dict[str, Any] | None = None) -> None:
        super().__init__(message)
        self.category = category
        self.status = status
        self.code = code
        self.request_id = request_id
        self.details = details


class NoOutputTextError(NvokenError):
    def __init__(self) -> None:
        super().__init__("unexpected_response", "This Turn completed without text output")


class TurnExecutionError(NvokenError):
    def __init__(self, result: TurnResult) -> None:
        super().__init__("server", f"Turn {result.turn.id} ended {result.status}")
        self.result = result


class TurnAdmissionError(NvokenError):
    def __init__(
        self,
        category: Literal["transport"],
        idempotency_key: str,
        message: str,
    ) -> None:
        super().__init__(category, message)
        self.idempotency_key = idempotency_key


class TurnTimeoutError(NvokenError):
    def __init__(self, turn: Turn | None, idempotency_key: str | None) -> None:
        suffix = f" {turn.id}" if turn is not None else " admission"
        super().__init__("timeout", f"Timed out waiting for durable Turn{suffix}")
        self.turn = turn
        self.idempotency_key = idempotency_key


def normalize_error(error: ApiException) -> NvokenError:
    status = error.status or 0
    body: dict[str, Any] = {}
    if isinstance(error.data, dict):
        body = error.data
    elif error.data is not None and hasattr(error.data, "model_dump"):
        body = error.data.model_dump()
    elif error.body:
        try:
            body = json.loads(error.body)
        except (TypeError, json.JSONDecodeError):
            pass
    category: ErrorCategory = (
        "authentication" if status == 401 else "permission" if status == 403 else
        "validation" if status in {400, 422} else "not_found" if status in {404, 410} else
        "conflict" if status == 409 else "rate_limit" if status == 429 else
        "server" if status >= 500 else "unexpected_response"
    )
    return NvokenError(category, body.get("message") or f"nvoken returned HTTP {status}",
                       status=status, code=body.get("code"),
                       request_id=body.get("request_id"), details=body.get("details"))


def is_not_found(error: object) -> TypeGuard[NvokenError]:
    return isinstance(error, NvokenError) and error.category == "not_found"


@dataclass(frozen=True)
class OwnedBy:
    """The namespace that owns an Agent. Omit for App ownership."""
    tenant: str
    user: str | None = None


@dataclass(frozen=True)
class Behavior:
    instructions: str
    model: str | dict[str, Any]
    limits: dict[str, Any] | None = None
    output_schema: dict[str, Any] | None = None
    tools: tuple[dict[str, Any], ...] = ()
    memory: dict[str, Any] | None = None

    @classmethod
    def coerce(cls, value: Behavior | Mapping[str, Any]) -> Behavior:
        return value if isinstance(value, cls) else cls(**dict(value))

    def to_wire(self) -> dict[str, Any]:
        data = {
            "instructions": self.instructions, "model": self.model,
            "limits": self.limits,
            "output_schema": self.output_schema,
            "tools": list(self.tools) or None,
            "memory": self.memory,
        }
        return {key: value for key, value in data.items() if value is not None}


@dataclass(frozen=True)
class Memory:
    """Stored-Agent memory selection; its namespace may derive from the Agent ID."""

    scope: Literal["none", "tenant", "user"]
    namespace: str | None = None

    @classmethod
    def none(cls) -> Memory:
        return cls("none")

    @classmethod
    def tenant(cls, namespace: str | None = None) -> Memory:
        return cls("tenant", namespace)

    @classmethod
    def user(cls, namespace: str | None = None) -> Memory:
        return cls("user", namespace)

    def to_wire(self) -> dict[str, Any]:
        result: dict[str, Any] = {"scope": self.scope}
        if self.namespace is not None:
            result["namespace"] = self.namespace
        return result


@dataclass(frozen=True)
class InlineNoMemory:
    scope: Literal["none"] = field(default="none", init=False)

    def to_wire(self) -> dict[str, Any]:
        return {"scope": self.scope}


@dataclass(frozen=True)
class InlineTenantMemory:
    namespace: str
    scope: Literal["tenant"] = field(default="tenant", init=False)

    def __post_init__(self) -> None:
        if not self.namespace:
            raise ValueError("Inline tenant Memory requires an explicit namespace")

    def to_wire(self) -> dict[str, Any]:
        return {"scope": self.scope, "namespace": self.namespace}


@dataclass(frozen=True)
class InlineUserMemory:
    namespace: str
    scope: Literal["user"] = field(default="user", init=False)

    def __post_init__(self) -> None:
        if not self.namespace:
            raise ValueError("Inline user Memory requires an explicit namespace")

    def to_wire(self) -> dict[str, Any]:
        return {"scope": self.scope, "namespace": self.namespace}


InlineMemorySelection: TypeAlias = InlineNoMemory | InlineTenantMemory | InlineUserMemory


class InlineMemory:
    """Closed constructors for memory selected by inline behavior."""

    @staticmethod
    def none() -> InlineNoMemory:
        return InlineNoMemory()

    @staticmethod
    def tenant(namespace: str) -> InlineTenantMemory:
        return InlineTenantMemory(namespace)

    @staticmethod
    def user(namespace: str) -> InlineUserMemory:
        return InlineUserMemory(namespace)


@dataclass(frozen=True)
class ConversationById:
    id: str

    def to_wire(self, _user: str | None) -> dict[str, Any]:
        return {"mode": "continue", "conversation_id": self.id}


@dataclass(frozen=True)
class ConversationByKey:
    key: str
    owner: Literal["tenant", "user"]
    retention: Mapping[str, Any] | None = None
    compaction: Mapping[str, Any] | None = None
    metadata: Mapping[str, Any] | None = None

    def to_wire(self, user: str | None) -> dict[str, Any]:
        owner: dict[str, Any] = {"kind": self.owner}
        if self.owner == "user":
            if user is None:
                raise ValueError("A user-owned Conversation requires Turn user")
            owner["user_key"] = user
        result: dict[str, Any] = {
            "mode": "continue_or_create",
            "conversation_key": self.key,
            "owner": owner,
        }
        if self.retention is not None:
            result["retention"] = dict(self.retention)
        if self.compaction is not None:
            result["compaction"] = dict(self.compaction)
        if self.metadata is not None:
            result["metadata"] = dict(self.metadata)
        return result


ConversationSelection: TypeAlias = ConversationById | ConversationByKey


class ConversationRef:
    """Discriminated constructors for high-level Conversation selection."""

    @staticmethod
    def by_id(conversation_id: str) -> ConversationById:
        return ConversationById(conversation_id)

    @staticmethod
    def by_key(
        key: str,
        *,
        owner: Literal["tenant", "user"],
        retention: Mapping[str, Any] | None = None,
        compaction: Mapping[str, Any] | None = None,
        metadata: Mapping[str, Any] | None = None,
    ) -> ConversationByKey:
        return ConversationByKey(key, owner, retention, compaction, metadata)


@dataclass(frozen=True)
class ToolContext:
    turn_id: str
    tool_call_id: str

    @property
    def cancelled(self) -> bool:
        task = asyncio.current_task()
        return task is not None and task.cancelling() > 0


ToolHandler = Callable[[dict[str, Any], ToolContext], Any | Awaitable[Any]]


@dataclass(frozen=True)
class TurnOptions:
    tenant: str
    user: str | None = None
    memory: Memory | InlineMemorySelection | None = None
    conversation: ConversationSelection | None = None
    limits: Mapping[str, Any] | None = None
    metadata: Mapping[str, str] | None = None
    idempotency_key: str | None = None


@dataclass(frozen=True)
class TurnAdmission:
    idempotency_key: str
    deduplicated: bool


@dataclass(frozen=True)
class TurnSnapshot:
    """Render-safe current state returned by ``Turn.status`` and ``updates``."""

    status: TurnStatus
    messages: tuple[ConversationMessage, ...]
    text: str | None
    structured_output: Mapping[str, Any] | None
    behavior_source: Literal["agent_revision", "inline"]
    agent_id: str | None
    agent_revision_id: str | None
    memory_space_id: str | None
    conversation_id: str | None
    content_expires_at: datetime | None


@dataclass(frozen=True)
class TurnResult(TurnSnapshot):
    turn: Turn
    admission: TurnAdmission | None = None


@dataclass(frozen=True)
class StreamOptions:
    cursor: str | None = None
    deltas: bool = True
    timeout: float | None = None


@dataclass(frozen=True)
class TurnUpdate:
    snapshot: TurnSnapshot
    frame: StreamEvent | None = None
    cursor: str | None = None


def _merge_narrow_limits(
    bound: Mapping[str, Any] | None,
    requested: Mapping[str, Any] | None,
) -> Mapping[str, Any] | None:
    if bound is None:
        return requested
    if requested is None:
        return bound
    merged = dict(bound)
    for name, value in requested.items():
        ceiling = bound.get(name)
        if ceiling is not None and value is not None and value > ceiling:
            raise ValueError(f"Turn limit {name!r} may narrow {ceiling}, not widen it to {value}")
        merged[name] = value
    return merged


@dataclass(frozen=True)
class RawClient:
    admissions: AdmissionsApi
    agents: AgentsApi
    apps: AppsApi
    console_integration: ConsoleIntegrationApi
    conversations: ConversationsApi
    credits: CreditsApi
    identity: IdentityApi
    mcp: MCPApi
    memory_spaces: MemorySpacesApi
    models: ModelsApi
    operations: OperationsApi
    orgs: OrgsApi
    provider_keys: ProviderKeysApi
    tenants: TenantsApi
    turns: TurnsApi
    usage: UsageApi


def _owner_coordinates(owned_by: OwnedBy | None) -> tuple[AgentOwnerKind, str | None, str | None]:
    if owned_by is None:
        return AgentOwnerKind.APP, None, None
    if owned_by.user is None:
        return AgentOwnerKind.TENANT, owned_by.tenant, None
    return AgentOwnerKind.USER, owned_by.tenant, owned_by.user


class AgentCollection:
    def __init__(self, client: Client) -> None:
        self._client = client

    async def create(self, key: str, *, behavior: Behavior | Mapping[str, Any], name: str | None = None,
                     idempotency_key: str | None = None,
                     owned_by: OwnedBy | None = None) -> Agent:
        wire = Behavior.coerce(behavior).to_wire()
        owner_kind, tenant, user = _owner_coordinates(owned_by)
        owner: dict[str, Any] = {"kind": owner_kind.value}
        if tenant is not None:
            owner["tenant_key"] = tenant
        if user is not None:
            owner["user_key"] = user
        request = CreateAgentRequest.from_dict(
            {"agent_key": key, "name": name or key, "owner": owner, **wire}
        )
        try:
            return Agent(
                self._client,
                await self._client.raw.agents.create_agent(idempotency_key or str(uuid.uuid4()), request),
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def get_by_id(self, agent_id: str) -> Agent:
        try:
            return Agent(self._client, await self._client.raw.agents.get_agent(agent_id))
        except ApiException as error:
            raise normalize_error(error) from error

    async def list(self, *, owned_by: OwnedBy | None = None, include_archived: bool = False,
                   cursor: str | None = None) -> AgentPage:
        owner, tenant, user = _owner_coordinates(owned_by)
        try:
            page = await self._client.raw.agents.list_agents(
                owner, tenant_key=tenant, user_key=user, include_archived=include_archived,
                cursor=cursor)
        except ApiException as error:
            raise normalize_error(error) from error
        return AgentPage(
            items=tuple(Agent(self._client, resource) for resource in page.items),
            has_more=page.has_more,
            next_cursor=page.next_cursor,
        )


@dataclass(frozen=True)
class AgentPage:
    items: tuple[Agent, ...]
    has_more: bool
    next_cursor: str | None


class Client:
    def __init__(self, api_key: str, *, base_url: str = "https://api.nvoken.com") -> None:
        configuration = Configuration(host=base_url.rstrip("/"))
        configuration.access_token = api_key
        self._api_client = ApiClient(configuration)
        self._raw = RawClient(
            AdmissionsApi(self._api_client), AgentsApi(self._api_client), AppsApi(self._api_client),
            ConsoleIntegrationApi(self._api_client), ConversationsApi(self._api_client),
            CreditsApi(self._api_client), IdentityApi(self._api_client), MCPApi(self._api_client),
            MemorySpacesApi(self._api_client), ModelsApi(self._api_client), OperationsApi(self._api_client),
            OrgsApi(self._api_client), ProviderKeysApi(self._api_client), TenantsApi(self._api_client),
            TurnsApi(self._api_client), UsageApi(self._api_client))
        self._agents = AgentCollection(self)
        self._conversation_locks: dict[str, asyncio.Lock] = {}

    @property
    def raw(self) -> RawClient:
        return self._raw

    @property
    def agents(self) -> AgentCollection:
        return self._agents

    async def agent(self, key: str, *, owned_by: OwnedBy | None = None) -> Agent:
        owner, tenant, user = _owner_coordinates(owned_by)
        try:
            page = await self.raw.agents.list_agents(
                owner, tenant_key=tenant, user_key=user, agent_key=key, limit=1)
        except ApiException as error:
            raise normalize_error(error) from error
        if not page.items:
            raise NvokenError("not_found", f"Agent {key!r} was not found")
        return Agent(self, page.items[0])

    def inline(self, behavior: Behavior | Mapping[str, Any]) -> InlineAgent:
        return InlineAgent(self, Behavior.coerce(behavior))

    def turn(self, turn_id: str, *, tenant: str, user: str | None = None) -> Turn:
        return Turn(self, turn_id, tenant=tenant, user=user)

    async def allocate_credits(
        self,
        *,
        amount: Mapping[str, Any],
        idempotency_key: str,
        tenant_key: str | None = None,
        default_tenant: bool = False,
        reference: str | None = None,
    ):
        request = AllocateCreditsRequest.from_dict({
            "tenant_key": tenant_key,
            "default_tenant": default_tenant,
            "amount": dict(amount),
            "reference": reference,
            "idempotency_key": idempotency_key,
        })
        try:
            return await self.raw.credits.allocate_credits(request)
        except ApiException as error:
            raise normalize_error(error) from error

    async def create_credential(
        self,
        name: str,
        credential_type: str,
        *,
        idempotency_key: str,
        app_id: str | None = None,
        expires_at: datetime | None = None,
    ):
        request = CreateCredentialRequest.from_dict({
            "name": name, "type": credential_type, "app_id": app_id,
            "expires_at": expires_at,
        })
        try:
            return await self.raw.identity.create_credential(idempotency_key, request)
        except ApiException as error:
            raise normalize_error(error) from error

    async def create_provider_key(
        self,
        provider: str,
        scope: str,
        key: Mapping[str, Any],
        *,
        idempotency_key: str,
        tenant_key: str | None = None,
        expires_at: datetime | None = None,
    ):
        request = CreateProviderKeyRequest.from_dict({
            "provider": provider, "scope": scope, "tenant_key": tenant_key,
            "key": dict(key), "expires_at": expires_at,
            "idempotency_key": idempotency_key,
        })
        try:
            return await self.raw.provider_keys.create_provider_key(request)
        except ApiException as error:
            raise normalize_error(error) from error

    async def issue_anonymous_token(
        self,
        app_id: str,
        origin: str,
        *,
        idempotency_key: str,
        visitor_token: str | None = None,
    ):
        request = AnonymousTokenRequest(visitor_token=visitor_token)
        try:
            return await self.raw.apps.issue_anonymous_token(
                app_id, origin, idempotency_key, request,
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def list_credit_accounts(
        self, *, tenant_key: str | None = None, default_tenant: bool | None = None,
        cursor: str | None = None, limit: int | None = None,
    ):
        try:
            return await self.raw.credits.list_credit_accounts(
                tenant_key=tenant_key, default_tenant=default_tenant,
                cursor=cursor, limit=limit,
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def list_credit_allocations(
        self, *, tenant_key: str | None = None, default_tenant: bool | None = None,
        cursor: str | None = None, limit: int | None = None,
    ):
        try:
            return await self.raw.credits.list_credit_allocations(
                tenant_key=tenant_key, default_tenant=default_tenant,
                cursor=cursor, limit=limit,
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def list_models(
        self, *, provider: str | None = None, include_deprecated: bool | None = None,
        if_none_match: str | None = None,
    ):
        try:
            return await self.raw.models.list_models(
                provider=provider, include_deprecated=include_deprecated,
                if_none_match=if_none_match,
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def list_provider_keys(
        self, *, provider: str | None = None, scope: Any = None,
        status: str | None = None, tenant_key: str | None = None,
        cursor: str | None = None, limit: int | None = None,
    ):
        try:
            return await self.raw.provider_keys.list_provider_keys(
                provider=provider, scope=scope, status=status, tenant_key=tenant_key,
                cursor=cursor, limit=limit,
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def register_org(self, display_name: str, *, external_ref: str | None = None):
        try:
            return await self.raw.orgs.register_org(RegisterOrgRequest(
                display_name=display_name, external_ref=external_ref,
            ))
        except ApiException as error:
            raise normalize_error(error) from error

    async def rotate_credential(
        self, credential_id: str, *, overlap_seconds: int, idempotency_key: str,
    ):
        try:
            return await self.raw.identity.rotate_credential(
                idempotency_key, credential_id,
                RotateCredentialRequest(overlap_seconds=overlap_seconds),
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def rotate_provider_key(
        self,
        provider_key_id: str,
        key: Mapping[str, Any],
        *,
        idempotency_key: str,
        expires_at: datetime | None = None,
        overlap_seconds: int = 0,
    ):
        request = RotateProviderKeyRequest.from_dict({
            "key": dict(key), "expires_at": expires_at,
            "overlap_seconds": overlap_seconds, "idempotency_key": idempotency_key,
        })
        try:
            return await self.raw.provider_keys.rotate_provider_key(provider_key_id, request)
        except ApiException as error:
            raise normalize_error(error) from error

    async def update_org(self, org_id: str, *, display_name: str):
        try:
            return await self.raw.orgs.update_org(
                org_id, UpdateOrgRequest(display_name=display_name),
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def close(self) -> None:
        await self._api_client.close()

    async def __aenter__(self) -> Client:
        return self

    async def __aexit__(self, *_: object) -> None:
        await self.close()


@dataclass(frozen=True)
class _Runnable:
    client: Client
    behavior_wire: Mapping[str, Any]
    tools: Mapping[str, ToolHandler] = field(default_factory=dict)

    def bind_tools(self, handlers: Mapping[str, ToolHandler]) -> _Runnable:
        if isinstance(self, InlineAgent):
            declared = {
                str(tool.get("name"))
                for tool in self.behavior.tools
                if tool.get("mode") == "host" and tool.get("name") is not None
            }
            unknown = sorted(set(handlers) - declared)
            if unknown:
                raise ValueError(
                    "Tool handlers are not declared by the inline behavior: "
                    + ", ".join(unknown)
                )
        bound = dict(self.tools)
        bound.update(handlers)
        if isinstance(self, Agent):
            return Agent(self.client, self.resource, bound)
        if isinstance(self, InlineAgent):
            return InlineAgent(self.client, self.behavior, bound)
        raise TypeError(f"Unsupported runnable {type(self).__name__}")

    def _conversation(
        self,
        ref: ConversationSelection,
        *,
        tenant: str,
        user: str | None = None,
        memory: Memory | InlineMemorySelection | None = None,
        limits: Mapping[str, Any] | None = None,
    ) -> Conversation:
        return Conversation(
            self, ref, tenant=tenant, user=user, memory=memory, limits=limits,
        )

    def _request(self, input: Any, options: TurnOptions) -> CreateTurnRequest:
        if options.memory is not None and options.memory.scope == "user" and options.user is None:
            raise ValueError("User Memory requires a Turn user")
        if self.behavior_wire.get("kind") == "inline" and isinstance(options.memory, Memory):
            if options.memory.scope != "none":
                raise ValueError("Inline behavior requires an InlineMemory selection")
        body: dict[str, Any] = {
            "tenant_key": options.tenant,
            "user_key": options.user,
            "idempotency_key": options.idempotency_key or str(uuid.uuid4()),
            "behavior": dict(self.behavior_wire),
            "input": input,
            "limits": dict(options.limits) if options.limits else None,
            "metadata": dict(options.metadata) if options.metadata else None,
            "memory": options.memory.to_wire() if options.memory else None,
            "conversation": options.conversation.to_wire(options.user) if options.conversation else None,
        }
        return CreateTurnRequest.from_dict({k: v for k, v in body.items() if v is not None})

    async def _start(
        self,
        input: Any,
        *,
        tenant: str,
        user: str | None = None,
        memory: Memory | InlineMemorySelection | None = None,
        conversation: ConversationSelection | None = None,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> Turn:
        admission_key = idempotency_key or str(uuid.uuid4())
        options = TurnOptions(
            tenant=tenant, user=user, memory=memory, conversation=conversation,
            limits=limits, metadata=metadata, idempotency_key=admission_key,
        )
        try:
            request = self._request(input, options)
            if timeout is None:
                admission = self.client.raw.turns.create_turn(request)
            else:
                admission = self.client.raw.turns.create_turn(
                    request,
                    _request_timeout=timeout,
                )
            resource = (
                await asyncio.wait_for(admission, timeout)
                if timeout is not None else await admission
            )
        except ApiException as error:
            raise normalize_error(error) from error
        except (TimeoutError, TimeoutException) as error:
            raise TurnTimeoutError(None, admission_key) from error
        except TransportError as error:
            raise TurnAdmissionError("transport", admission_key, str(error)) from error
        return Turn(
            self.client,
            resource.id,
            tenant=options.tenant,
            user=options.user,
            resource=resource,
            tools=self.tools,
            admission=TurnAdmission(
                idempotency_key=admission_key,
                deduplicated=bool(resource.deduplicated),
            ),
        )

    async def _run(self, input: Any, *, timeout: float | None = None, **options: Any):
        started_at = time.monotonic()
        turn = await self._start(input, timeout=timeout, **options)
        remaining = None if timeout is None else max(0.0, timeout - (time.monotonic() - started_at))
        return await turn.result(timeout=remaining)

    async def _text(self, input: Any, *, timeout: float | None = None, **options: Any) -> str:
        result = await self._run(input, timeout=timeout, **options)
        if result.text is None:
            raise NoOutputTextError()
        return result.text


@dataclass(frozen=True)
class InlineAgent(_Runnable):
    behavior: Behavior = field(default=None)  # type: ignore[assignment]

    def __init__(self, client: Client, behavior: Behavior,
                 tools: Mapping[str, ToolHandler] | None = None) -> None:
        object.__setattr__(self, "client", client)
        object.__setattr__(self, "behavior", behavior)
        object.__setattr__(self, "behavior_wire", {"kind": "inline", "behavior": behavior.to_wire()})
        object.__setattr__(self, "tools", tools or {})

    def conversation(
        self,
        ref: ConversationSelection,
        *,
        tenant: str,
        user: str | None = None,
        memory: InlineMemorySelection | None = None,
        limits: Mapping[str, Any] | None = None,
    ) -> Conversation:
        return self._conversation(
            ref,
            tenant=tenant,
            user=user,
            memory=memory,
            limits=limits,
        )

    async def start(
        self,
        input: Any,
        *,
        tenant: str,
        user: str | None = None,
        memory: InlineMemorySelection | None = None,
        conversation: ConversationSelection | None = None,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> Turn:
        return await self._start(
            input,
            tenant=tenant,
            user=user,
            memory=memory,
            conversation=conversation,
            limits=limits,
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )

    async def run(
        self,
        input: Any,
        *,
        tenant: str,
        user: str | None = None,
        memory: InlineMemorySelection | None = None,
        conversation: ConversationSelection | None = None,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> TurnResult:
        return await self._run(
            input,
            tenant=tenant,
            user=user,
            memory=memory,
            conversation=conversation,
            limits=limits,
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )

    async def text(
        self,
        input: Any,
        *,
        tenant: str,
        user: str | None = None,
        memory: InlineMemorySelection | None = None,
        conversation: ConversationSelection | None = None,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> str:
        return await self._text(
            input,
            tenant=tenant,
            user=user,
            memory=memory,
            conversation=conversation,
            limits=limits,
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )


@dataclass(frozen=True)
class Agent(_Runnable):
    resource: AgentResource = field(default=None)  # type: ignore[assignment]

    def __init__(self, client: Client, resource: AgentResource,
                 tools: Mapping[str, ToolHandler] | None = None) -> None:
        object.__setattr__(self, "client", client)
        object.__setattr__(self, "resource", resource)
        object.__setattr__(self, "behavior_wire", {
            "kind": "agent", "agent": {"agent_id": resource.id, "revision": "current"}})
        object.__setattr__(self, "tools", tools or {})

    @property
    def id(self) -> str:
        return self.resource.id

    @property
    def key(self) -> str:
        return self.resource.agent_key

    @property
    def owner(self):
        return self.resource.owner

    @property
    def current_revision(self) -> int:
        return self.resource.current_revision

    def conversation(
        self,
        ref: ConversationSelection,
        *,
        tenant: str,
        user: str | None = None,
        memory: Memory | None = None,
        limits: Mapping[str, Any] | None = None,
    ) -> Conversation:
        return self._conversation(
            ref,
            tenant=tenant,
            user=user,
            memory=memory,
            limits=limits,
        )

    async def start(
        self,
        input: Any,
        *,
        tenant: str,
        user: str | None = None,
        memory: Memory | None = None,
        conversation: ConversationSelection | None = None,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> Turn:
        return await self._start(
            input,
            tenant=tenant,
            user=user,
            memory=memory,
            conversation=conversation,
            limits=limits,
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )

    async def run(
        self,
        input: Any,
        *,
        tenant: str,
        user: str | None = None,
        memory: Memory | None = None,
        conversation: ConversationSelection | None = None,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> TurnResult:
        return await self._run(
            input,
            tenant=tenant,
            user=user,
            memory=memory,
            conversation=conversation,
            limits=limits,
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )

    async def text(
        self,
        input: Any,
        *,
        tenant: str,
        user: str | None = None,
        memory: Memory | None = None,
        conversation: ConversationSelection | None = None,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> str:
        return await self._text(
            input,
            tenant=tenant,
            user=user,
            memory=memory,
            conversation=conversation,
            limits=limits,
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )

    async def publish(
        self,
        behavior: Behavior | Mapping[str, Any],
        *,
        idempotency_key: str | None = None,
    ):
        request = GeneratedBehaviorInput.from_dict(Behavior.coerce(behavior).to_wire())
        try:
            return await self.client.raw.agents.publish_agent_revision(
                idempotency_key or str(uuid.uuid4()), self.id, request,
            )
        except ApiException as error:
            raise normalize_error(error) from error

    async def archive(self) -> Agent:
        try:
            return Agent(self.client, await self.client.raw.agents.archive_agent(self.id), self.tools)
        except ApiException as error:
            raise normalize_error(error) from error

    async def restore(self) -> Agent:
        try:
            return Agent(self.client, await self.client.raw.agents.restore_agent(self.id), self.tools)
        except ApiException as error:
            raise normalize_error(error) from error


@dataclass(frozen=True)
class Conversation:
    runnable: _Runnable
    ref: ConversationSelection
    tenant: str
    user: str | None = None
    memory: Memory | InlineMemorySelection | None = None
    limits: Mapping[str, Any] | None = None

    def _lock_key(self) -> str:
        if isinstance(self.ref, ConversationById):
            return self.ref.id
        if self.ref.owner == "user":
            return f"{self.tenant}:user:{self.user or ''}:{self.ref.key}"
        return f"{self.tenant}:tenant:{self.ref.key}"

    async def _start_unlocked(
        self,
        input: Any,
        *,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> Turn:
        return await self.runnable.start(
            input,
            tenant=self.tenant,
            user=self.user,
            memory=self.memory,
            conversation=self.ref,
            limits=_merge_narrow_limits(self.limits, limits),
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )

    async def start(
        self,
        input: Any,
        *,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> Turn:
        lock = self.runnable.client._conversation_locks.setdefault(self._lock_key(), asyncio.Lock())
        async with lock:
            return await self._start_unlocked(
                input,
                limits=limits,
                metadata=metadata,
                idempotency_key=idempotency_key,
                timeout=timeout,
            )

    async def run(
        self,
        input: Any,
        *,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> TurnResult:
        lock = self.runnable.client._conversation_locks.setdefault(self._lock_key(), asyncio.Lock())
        async with lock:
            started_at = time.monotonic()
            turn = await self._start_unlocked(
                input,
                limits=limits,
                metadata=metadata,
                idempotency_key=idempotency_key,
                timeout=timeout,
            )
            remaining = (
                None if timeout is None
                else max(0.0, timeout - (time.monotonic() - started_at))
            )
            return await turn.result(timeout=remaining)

    async def text(
        self,
        input: Any,
        *,
        limits: Mapping[str, Any] | None = None,
        metadata: Mapping[str, str] | None = None,
        idempotency_key: str | None = None,
        timeout: float | None = None,
    ) -> str:
        result = await self.run(
            input,
            limits=limits,
            metadata=metadata,
            idempotency_key=idempotency_key,
            timeout=timeout,
        )
        if result.text is None:
            raise NoOutputTextError()
        return result.text


class Turn:
    def __init__(self, client: Client, turn_id: str, *, tenant: str, user: str | None = None,
                 resource: Any = None, tools: Mapping[str, ToolHandler] | None = None,
                 admission: TurnAdmission | None = None,
                 handled_tool_call_ids: set[str] | None = None) -> None:
        self._client = client
        self.id = turn_id
        self.tenant = tenant
        self.user = user
        self._resource = resource
        self._tools = dict(tools or {})
        self._admission = admission
        self._handled_tool_call_ids = (
            handled_tool_call_ids if handled_tool_call_ids is not None else set()
        )

    def bind_tools(self, handlers: Mapping[str, ToolHandler]) -> Turn:
        """Return a new recovery handle with process-local tool handlers."""
        bound = dict(self._tools)
        bound.update(handlers)
        return Turn(
            self._client,
            self.id,
            tenant=self.tenant,
            user=self.user,
            resource=self._resource,
            tools=bound,
            admission=self._admission,
            handled_tool_call_ids=self._handled_tool_call_ids,
        )

    def _access_headers(self) -> dict[str, str]:
        headers = {"X-Nvoken-Tenant-Key": self.tenant}
        if self.user is not None:
            headers["X-Nvoken-User-Key"] = self.user
        return headers

    def _snapshot(
        self,
        result: TurnResultResource,
    ) -> TurnSnapshot:
        self._resource = result.turn
        source = getattr(result.turn, "behavior_source", None)
        actual_source = getattr(source, "actual_instance", source)
        kind = getattr(actual_source, "kind", None)
        if hasattr(kind, "value"):
            kind = kind.value
        if kind not in {"agent_revision", "inline"}:
            raise NvokenError(
                "unexpected_response",
                f"Turn {self.id} did not include a recognized behavior source",
            )
        return TurnSnapshot(
            status=result.turn.status,
            messages=tuple(result.messages),
            text=result.output_text,
            structured_output=result.turn.structured_output,
            behavior_source=kind,
            agent_id=getattr(actual_source, "agent_id", None),
            agent_revision_id=getattr(actual_source, "agent_revision_id", None),
            memory_space_id=result.turn.memory_space_id,
            conversation_id=result.turn.conversation_id,
            content_expires_at=result.turn.content_expires_at,
        )

    async def status(self) -> TurnSnapshot:
        try:
            result = await self._client.raw.turns.get_turn_result(
                self.id, _headers=self._access_headers(),
            )
            return self._snapshot(result)
        except ApiException as error:
            raise normalize_error(error) from error

    async def result(
        self, *, timeout: float | None = None, poll_interval: float = 0.25,
    ) -> TurnResult:
        wait = self._wait_for_result(poll_interval=poll_interval)
        try:
            return await asyncio.wait_for(wait, timeout) if timeout is not None else await wait
        except TimeoutError as error:
            raise TurnTimeoutError(
                self,
                self._admission.idempotency_key if self._admission is not None else None,
            ) from error

    async def _wait_for_result(self, *, poll_interval: float) -> TurnResult:
        while True:
            snapshot = await self.status()
            status = snapshot.status.value if hasattr(snapshot.status, "value") else str(snapshot.status)
            if status == "waiting" and self._resource is not None \
                    and await self._run_host_tools(self._resource):
                continue
            if status in TERMINAL_STATUSES:
                result = TurnResult(
                    status=snapshot.status,
                    messages=snapshot.messages,
                    text=snapshot.text,
                    structured_output=snapshot.structured_output,
                    behavior_source=snapshot.behavior_source,
                    agent_id=snapshot.agent_id,
                    agent_revision_id=snapshot.agent_revision_id,
                    memory_space_id=snapshot.memory_space_id,
                    conversation_id=snapshot.conversation_id,
                    content_expires_at=snapshot.content_expires_at,
                    turn=self,
                    admission=self._admission,
                )
                if status in {"failed", "cancelled"}:
                    raise TurnExecutionError(result)
                return result
            await asyncio.sleep(poll_interval)

    async def _run_host_tools(self, turn: Any) -> bool:
        pending = [call for call in turn.tool_calls
                   if (call.mode.value if hasattr(call.mode, "value") else str(call.mode)) == "host"
                   and call.arguments is not None
                   and call.id not in self._handled_tool_call_ids
                   and call.name in self._tools]
        if not pending:
            return False
        results: list[dict[str, Any]] = []
        attempted: list[str] = []
        for call in pending:
            handler = self._tools[call.name]
            self._handled_tool_call_ids.add(call.id)
            attempted.append(call.id)
            try:
                value = handler(
                    call.arguments,
                    ToolContext(turn_id=self.id, tool_call_id=call.id),
                )
                if inspect.isawaitable(value):
                    value = await value
                results.append({"tool_call_id": call.id, "content": value, "is_error": False})
            except asyncio.CancelledError:
                self._handled_tool_call_ids.difference_update(attempted)
                raise
            except Exception as error:
                results.append({"tool_call_id": call.id, "content": str(error), "is_error": True})
        request = SubmitHostToolResultsRequest.from_dict({"results": results})
        try:
            await self._client.raw.turns.submit_host_tool_results(
                self.id, request, _headers=self._access_headers(),
            )
        except asyncio.CancelledError:
            self._handled_tool_call_ids.difference_update(attempted)
            raise
        except ApiException as error:
            self._handled_tool_call_ids.difference_update(attempted)
            raise normalize_error(error) from error
        return True

    async def updates(
        self,
        options: StreamOptions | None = None,
    ) -> AsyncIterator[TurnUpdate]:
        """Yield reduced, render-safe snapshots from the resumable direct Turn stream."""
        from .stream import Reducer, _read_stream

        options = options or StreamOptions()
        deadline = None if options.timeout is None else time.monotonic() + options.timeout
        reducer = Reducer()
        current = await self.status()
        yield TurnUpdate(snapshot=current, cursor=options.cursor)
        initial_status = (
            current.status.value if hasattr(current.status, "value") else str(current.status)
        )
        if initial_status == "waiting" and self._resource is not None:
            await self._run_host_tools(self._resource)
        if initial_status in TERMINAL_STATUSES:
            return
        try:
            stream = _read_stream(
                self._client,
                conversation_id=None,
                turn_id=self.id,
                reducer=reducer,
                deltas=options.deltas,
                cursor=options.cursor,
                access_headers=self._access_headers(),
            )
            while True:
                try:
                    remaining = (
                        None if deadline is None
                        else max(0.0, deadline - time.monotonic())
                    )
                    event = (
                        await asyncio.wait_for(anext(stream), remaining)
                        if remaining is not None else await anext(stream)
                    )
                except StopAsyncIteration:
                    return
                reduced = reducer.snapshot()
                current = await self.status()
                yield TurnUpdate(snapshot=current, frame=event, cursor=reduced.cursor)
                status = (
                    current.status.value
                    if hasattr(current.status, "value")
                    else str(current.status)
                )
                if status == "waiting" and self._resource is not None:
                    await self._run_host_tools(self._resource)
                if reducer.settled(self.id) or status in TERMINAL_STATUSES:
                    if reducer.settled(self.id) and status not in TERMINAL_STATUSES:
                        current = await self.status()
                        yield TurnUpdate(snapshot=current, cursor=reduced.cursor)
                    return
        except TimeoutError as error:
            raise TurnTimeoutError(
                self,
                self._admission.idempotency_key if self._admission is not None else None,
            ) from error
