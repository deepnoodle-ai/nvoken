from __future__ import annotations

import asyncio
import json
import random
import re
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
from typing import Any, AsyncIterator, Awaitable, Callable, Literal, Sequence, TypeGuard

import httpx

from nvoken_generated import __version__ as SDK_VERSION
from nvoken_generated.api.admissions_api import AdmissionsApi
from nvoken_generated.api.agents_api import AgentsApi
from nvoken_generated.api.agent_definitions_api import AgentDefinitionsApi
from nvoken_generated.api.apps_api import AppsApi
from nvoken_generated.api.credits_api import CreditsApi
from nvoken_generated.api.identity_api import IdentityApi
from nvoken_generated.api.memories_api import MemoriesApi
from nvoken_generated.api.orgs_api import OrgsApi
from nvoken_generated.api.tenants_api import TenantsApi
from nvoken_generated.api.usage_api import UsageApi
from nvoken_generated.api.invocations_api import InvocationsApi
from nvoken_generated.api.mcp_api import MCPApi
from nvoken_generated.api.models_api import ModelsApi
from nvoken_generated.api.provider_keys_api import ProviderKeysApi
from nvoken_generated.api.sessions_api import SessionsApi
from nvoken_generated.api_client import ApiClient
from nvoken_generated.configuration import Configuration
from nvoken_generated.exceptions import ApiException
from nvoken_generated.models.agent import Agent as AgentResource
from nvoken_generated.models.agent_list import AgentList
from nvoken_generated.models.builtin_tool_declaration import BuiltinToolDeclaration
from nvoken_generated.models.agent_definition_resource import AgentDefinitionResource
from nvoken_generated.models.agent_definition_resource_list import AgentDefinitionResourceList
from nvoken_generated.models.agent_definition_create import AgentDefinitionCreate
from nvoken_generated.models.agent_definition_overrides import (
    AgentDefinitionOverrides as GeneratedAgentDefinitionOverrides,
)
from nvoken_generated.models.agent_definition_write import AgentDefinitionWrite
from nvoken_generated.models.create_agent_request import CreateAgentRequest
from nvoken_generated.models.update_agent_request import UpdateAgentRequest
from nvoken_generated.models.allocate_credits_request import AllocateCreditsRequest
from nvoken_generated.models.allocate_credits_result import AllocateCreditsResult
from nvoken_generated.models.anonymous_token_request import AnonymousTokenRequest
from nvoken_generated.models.anonymous_token_response import AnonymousTokenResponse
from nvoken_generated.models.app import App
from nvoken_generated.models.app_registration import AppRegistration
from nvoken_generated.models.app_signing_key_secret import AppSigningKeySecret
from nvoken_generated.models.credit_account_list import CreditAccountList
from nvoken_generated.models.credit_allocation_list import CreditAllocationList
from nvoken_generated.models.callback_target import CallbackTarget as GeneratedCallbackTarget
from nvoken_generated.models.callback_tool_declaration import CallbackToolDeclaration
from nvoken_generated.models.create_provider_key_request import (
    CreateProviderKeyRequest,
)
from nvoken_generated.models.client_key import ClientKey
from nvoken_generated.models.create_client_key_request import CreateClientKeyRequest
from nvoken_generated.models.create_credential_request import CreateCredentialRequest
from nvoken_generated.models.create_session_request import CreateSessionRequest
from nvoken_generated.models.credential_issuance import CredentialIssuance
from nvoken_generated.models.credential_profile import CredentialProfile
from nvoken_generated.models.compaction_policy import CompactionPolicy
from nvoken_generated.models.compaction_policy_trigger_tokens import (
    CompactionPolicyTriggerTokens,
)
from nvoken_generated.models.session_options import SessionOptions as GeneratedSessionOptions
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
from nvoken_generated.models.mcp_server_headers import MCPServerHeaders as GeneratedMCPServerHeaders
from nvoken_generated.models.invocation import Invocation
from nvoken_generated.models.invocation_trigger import InvocationTrigger
from nvoken_generated.models.invocation_change import InvocationChange
from nvoken_generated.models.invocation_log_list import InvocationLogList
from nvoken_generated.models.invocation_context_item import (
    InvocationContextItem as GeneratedInvocationContextItem,
)
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
from nvoken_generated.models.fork_session_request import ForkSessionRequest
from nvoken_generated.models.mcp_list_tools_request import MCPListToolsRequest
from nvoken_generated.models.mcp_list_tools_response import MCPListToolsResponse
from nvoken_generated.models.mcp_server import MCPServer as GeneratedMCPServer
from nvoken_generated.models.mcp_timeouts import MCPTimeouts as GeneratedMCPTimeouts
from nvoken_generated.models.model import Model as GeneratedModel
from nvoken_generated.models.model_input import ModelInput as GeneratedModelInput
from nvoken_generated.models.model_descriptor import ModelDescriptor
from nvoken_generated.models.model_list import ModelList
from nvoken_generated.models.model_tool_choice_mode import ModelToolChoiceMode
from nvoken_generated.models.memory_config import MemoryConfig as GeneratedMemoryConfig
from nvoken_generated.models.memory_context_config import (
    MemoryContextConfig as GeneratedMemoryContextConfig,
)
from nvoken_generated.models.memory_context_mode import MemoryContextMode as GeneratedMemoryContextMode
from nvoken_generated.models.memory_kind import MemoryKind
from nvoken_generated.models.memory_list import MemoryList
from nvoken_generated.models.memory_search_mode import MemorySearchMode
from nvoken_generated.models.mint_app_signing_key_request import MintAppSigningKeyRequest
from nvoken_generated.models.browser_client_interface import BrowserClientInterface
from nvoken_generated.models.money import Money
from nvoken_generated.models.provider_key import ProviderKey
from nvoken_generated.models.provider_key_list import ProviderKeyList
from nvoken_generated.models.provider_key_scope import ProviderKeyScope
from nvoken_generated.models.provider_key_usage import ProviderKeyUsage
from nvoken_generated.models.provider_static_key import ProviderStaticKey
from nvoken_generated.models.operation import Operation
from nvoken_generated.models.org import Org
from nvoken_generated.models.register_app_request import RegisterAppRequest
from nvoken_generated.models.register_org_request import RegisterOrgRequest
from nvoken_generated.models.rotate_provider_key_request import (
    RotateProviderKeyRequest,
)
from nvoken_generated.models.sampling import Sampling as GeneratedSampling
from nvoken_generated.models.reasoning_effort import ReasoningEffort
from nvoken_generated.models.reasoning import Reasoning as GeneratedReasoning
from nvoken_generated.models.nudge_acknowledgement import NudgeAcknowledgement
from nvoken_generated.models.create_nudge_request import CreateNudgeRequest
from nvoken_generated.models.nudge import Nudge
from nvoken_generated.models.nudge_list import NudgeList
from nvoken_generated.models.nudge_status import NudgeStatus
from nvoken_generated.models.tool_call_list import ToolCallList
from nvoken_generated.models.session import Session
from nvoken_generated.models.session_compaction import SessionCompaction
from nvoken_generated.models.session_compaction_list import SessionCompactionList
from nvoken_generated.models.session_list import SessionList
from nvoken_generated.models.update_session_request import UpdateSessionRequest
from nvoken_generated.models.session_message import SessionMessage
from nvoken_generated.models.session_message_list import SessionMessageList
from nvoken_generated.models.submit_host_tool_results_request import SubmitHostToolResultsRequest
from nvoken_generated.models.submit_host_tool_results_request_results_inner import (
    SubmitHostToolResultsRequestResultsInner,
)
from nvoken_generated.models.submit_host_tool_results_response import SubmitHostToolResultsResponse
from nvoken_generated.models.tool_choice import ToolChoice as GeneratedToolChoice
from nvoken_generated.models.tool_declaration import ToolDeclaration as GeneratedToolDeclaration
from nvoken_generated.models.transcript_snapshot import TranscriptSnapshot
from nvoken_generated.models.update_app_request import UpdateAppRequest
from nvoken_generated.models.update_org_request import UpdateOrgRequest

from .stream import (
    ReducedSnapshot,
    Reducer,
    StreamEvent,
    iter_invocation,
    stream_invocation,
    stream_session,
)
from .invocation_status import TERMINAL_INVOCATION_STATUSES
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
BudgetExhaustionBehavior = Literal["stop", "hold"]

# Sequence order for a message page. A cursor belongs to the direction that
# issued it and is refused by the other, so page one direction to its end
# rather than turning around mid-walk.
ListOrder = Literal["asc", "desc"]

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
        "kind": "output_schema",
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
    Invocation acceptance and completion, so a turn outlasting the window cannot
    expire underneath itself. Automatic expiry never cancels running work.
    """

    # Idle window in seconds, from one hour to thirty days.
    ttl_seconds: int


# What a request asserts about a Session that already exists. ``refuse``, the
# default, compares every option sent. ``join`` reaches whatever Session is
# there without asserting how it is configured, so compaction and retention stop
# conflicting. Join never relaxes the authorization context, the revision pin,
# or the Session's end user: those catch a caller acting on the wrong
# conversation, and a flag that suppressed them would be a way around the check
# rather than a way to express intent.
SessionOptionsConflict = Literal["refuse", "join"]


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
    # Binds the Session to the host's own authorization facts. It is written
    # only by the request that creates the Session, never interpreted by
    # nvoken, never visible to the model, and carried inside the signed
    # callback envelope so a receiver authorizes a delivery without reading the
    # Invocation back. What nvoken guarantees is integrity, not authentication:
    # what creation recorded is what a signed delivery carries.
    authorization_context: dict[str, str] | None = None
    # Fixes the Agent Definition revision for the lifetime of a newly created
    # Session. Omit it to follow the Agent's resolution policy.
    pinned_revision: int | None = None
    # What this request asserts about a Session that already exists.
    on_conflict: SessionOptionsConflict | None = None


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
    """Declares a remote MCP server.

    It carries no secrets: an Agent Definition may be shared
    across turns, so authentication headers travel per Invocation in
    :class:`MCPServerHeaders` instead.
    """

    name: str
    url: str
    transport: Literal["streamable_http"] = "streamable_http"
    allowed_tools: tuple[str, ...] = ()
    timeouts: MCPTimeouts | None = None


@dataclass(frozen=True)
class MCPServerHeaders:
    """Secret headers for one MCP server named by the selected Agent Definition.

    They are encrypted for a single turn, and are never stored in, hashed into,
    or returned with the Agent Definition.
    """

    name: str
    headers: dict[str, str] = field(default_factory=dict, repr=False)


# The recorded context bounds the Runtime enforces at admission.
_MAX_CONTEXT_ITEMS = 8
_MAX_CONTEXT_NAME_LENGTH = 64
_MAX_CONTEXT_CONTENT_BYTES = 8 * 1024
_MAX_CONTEXT_TOTAL_BYTES = 16 * 1024
_CONTEXT_NAME_PATTERN = re.compile(r"[a-z][a-z0-9-]*")


ContextTier = Literal["contextual", "operator"]
"""How a recorded snapshot reaches the model.

``contextual`` is for conversation-adjacent facts, ``operator`` for policy or
other application-authoritative state. The tier stays typed in the transcript;
the provider-native role is chosen when the turn generates.
"""


@dataclass(frozen=True)
class ContextItem:
    """One application-owned state snapshot recorded ahead of a turn's input.

    ``name`` is a stable identity: sending it again supersedes the earlier
    value, and an unchanged resend adds no transcript message, so a stateless
    host may resend its whole snapshot every turn. Omit the reserved ``app-``
    prefix the model sees; nvoken adds it. Context is durable Session history
    rather than an Agent Definition field, so it never changes the admitted
    Agent Definition revision.
    """

    name: str
    tier: ContextTier
    content: str


WebhookEvent = Literal["invocation.waiting", "invocation.budget_hold", "invocation.ended"]


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


MemoryScope = Literal["tenant", "user"]

MemoryContextMode = Literal["index", "full", "false"]
"""``index`` and ``full`` differ in how much memory text a turn receives."""


@dataclass(frozen=True)
class MemoryContextConfig:
    mode: MemoryContextMode | None = None
    # Defaults to 1536 for index and 131072 for full; must be zero for false.
    max_bytes: int | None = None


@dataclass(frozen=True)
class MemoryConfig:
    # User scope requires a user key on every admitted Invocation.
    scope: MemoryScope | None = None
    context: MemoryContextConfig | None = None


@dataclass(frozen=True)
class ClientInterface:
    """One definition-specific browser authorization.

    It grants authorship and settlement only, never selective read visibility:
    every public transcript item in a browser-reachable Session must be treated
    as client-visible. ``None`` on a definition means it is not
    client-token-capable; an empty ``ClientInterface()`` opts in with no
    client-authored context or tools.
    """

    # Recorded-context names a client may append or supersede, contextual tier
    # only.
    context_names: tuple[str, ...] = ()
    # Host-mode tools whose parked calls a client may see and settle.
    tool_names: tuple[str, ...] = ()


@dataclass(frozen=True)
class AgentDefinition:
    """Everything writable on an App-owned Agent Definition.

    It is flat, matching the contract's ``AgentDefinitionWrite``. Reads return
    an ``AgentDefinitionResource``, which is this same flat object plus ``id``,
    ``revision``, and timestamps, so a read-modify-write is a conversion and a
    ``replace``::

        current = await client.get_agent_definition(definition_id)
        definition = AgentDefinition.from_resource(current)
        await client.update_agent_definition(
            definition_id,
            replace(definition, instructions="Be concise and warm."),
            expected_revision=current.revision,
        )
    """

    model: Model
    # Caller-chosen immutable key, unique within the App. Required to create.
    # A replacement cannot move a resource to another key, so it is ignored
    # there and a definition read back from the server may carry one along.
    definition_key: str | None = None
    # Display name. Defaults to definition_key, and because a replacement
    # replaces the whole resource, omitting it on update resets the name to the
    # key rather than keeping the current one.
    name: str | None = None
    instructions: str | None = None
    sampling: Sampling | None = None
    reasoning: Reasoning | None = None
    tool_choice: ToolChoice | None = None
    limits: Limits | None = None
    tools: tuple[Tool | BuiltinTool, ...] = ()
    mcp_servers: tuple[MCPServer, ...] = ()
    provider_tools: tuple[ProviderTool, ...] = ()
    memory: MemoryConfig | None = None
    client_interface: ClientInterface | None = None
    output_schema: dict[str, Any] | None = None

    @classmethod
    def from_resource(cls, resource: AgentDefinitionResource) -> "AgentDefinition":
        """Read a resource back into the definition that produced it.

        A replacement replaces the whole resource, so a field this drops would
        be erased on write. It therefore carries every writable field across by
        name and leaves the read-only ones — ``id``, ``revision``, and the
        timestamps — behind.
        """
        return _agent_definition_from_resource(cls, resource)


DefinitionSyncOutcome = Literal["created", "updated", "unchanged"]
"""What one definition's :meth:`NvokenClient.sync_definitions` call did.

``created`` — the key named nothing and now names this. ``updated`` — a
revision was published over different contents. ``unchanged`` — nvoken already
held exactly this, so nothing was published and the revision did not move.
"""


@dataclass(frozen=True)
class DefinitionSync:
    """One definition's result from :meth:`NvokenClient.sync_definitions`."""

    definition_key: str
    outcome: DefinitionSyncOutcome
    definition: AgentDefinitionResource


@dataclass(frozen=True)
class AgentDefinitionOverrides:
    """Safe per-turn replacements that cannot expand Agent authority."""

    model: Model | None = None
    sampling: Sampling | None = None
    reasoning: Reasoning | None = None
    tool_choice: ToolChoice | None = None
    limits: Limits | None = None
    output_schema: dict[str, Any] | None = None


@dataclass(frozen=True)
class InvokeRequest:
    input: str | tuple[InputBlock, ...]
    """Text shorthand, or ordered blocks mixing text, images, and documents."""
    agent_id: str | None = None
    agent_key: str | None = None
    definition_revision: int | None = None
    overrides: AgentDefinitionOverrides | None = None
    mcp_server_headers: tuple[MCPServerHeaders, ...] = ()
    """Per-turn secret headers, keyed to MCP server names in the definition."""
    idempotency_key: str | None = None
    if_active: IfActivePolicy | None = None
    on_budget_exhausted: BudgetExhaustionBehavior | None = None
    tenant_key: str | None = None
    # Who this turn is for. The first request that opens a Session fixes its
    # user key, including fixing it to absent; every later turn either sends the
    # same one or leaves it out and inherits it. A turn naming a different end
    # user is refused with `session_user_key_conflict`.
    #
    # It is a filter, and on an Agent whose Definition sets `memory.scope: user`
    # it is also the memory partition — it decides whose durable memories the
    # model can recall — so it is required on the turn that opens a Session for
    # such an Agent.
    user_key: str | None = None
    session_id: str | None = None
    session_key: str | None = None
    session_options: SessionOptions | None = None
    triggered_by: InvocationTrigger | None = None
    """Verified parent Invocation and ToolCall that caused this turn."""
    provider_keys: tuple[ProviderKeySelection, ...] = ()
    webhook: WebhookTarget | None = None
    context: tuple[ContextItem, ...] = ()
    """Ordered application state snapshots recorded before this turn's input.

    The list is order-sensitive and material to idempotency: a replay that
    reorders or edits an item conflicts rather than updating it.
    """
    # Opaque host correlation data recorded on this Invocation. It is part of
    # the admitted input, so it is immutable and material to idempotency: a
    # replay carrying different metadata conflicts rather than updating it.
    # Session metadata is separate and mutable — see Client.update_session.
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
class Scope:
    """Narrows every request a Client makes to one tenant, one end user, or both.

    Anything outside it is reported as not found, so an id that arrives from
    the wrong place cannot be acted on — which is what lets one app-wide
    credential serve a whole application without an ownership check written at
    every call site. A scope may only narrow: naming a tenant the credential is
    not bound to is refused rather than silently returning nothing.
    """

    tenant_key: str | None = None
    user_key: str | None = None


@dataclass(frozen=True)
class TranscriptDrain:
    messages: list[SessionMessage]
    invocation_changes: list[InvocationChange]
    cursor: str


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
        scope: Scope | None = None,
    ) -> None:
        if not base_url or not api_key:
            raise NvokenError("validation", "base_url and api_key are required")
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.retry = retry
        self.transport = transport
        self.scope = _narrowing_scope(scope)
        self._session_locks: dict[str, asyncio.Lock] = {}
        self._background_tasks: set[asyncio.Task[Any]] = set()
        configuration = Configuration(host=self.base_url, access_token=api_key)
        configuration.discard_unknown_keys = False
        self.api_client = ApiClient(configuration)
        for name, value in _scope_headers(self.scope).items():
            self.api_client.set_default_header(name, value)
        self.agents = AgentsApi(self.api_client)
        self.credits = CreditsApi(self.api_client)
        self.invocations = InvocationsApi(self.api_client)
        self.agent_definitions = AgentDefinitionsApi(self.api_client)
        self.mcp = MCPApi(self.api_client)
        self.models = ModelsApi(self.api_client)
        self.provider_keys = ProviderKeysApi(self.api_client)
        self.sessions = SessionsApi(self.api_client)
        self.identity = IdentityApi(self.api_client)
        self.memories = MemoriesApi(self.api_client)
        self.usage = UsageApi(self.api_client)
        # Org-scoped control plane. These carry no hand-written wrapper because
        # an App-scoped Runtime credential cannot reach them at all, but a
        # caller holding an org credential should not have to reconstruct a
        # generated client to use one.
        self.apps = AppsApi(self.api_client)
        self.orgs = OrgsApi(self.api_client)
        self.tenants = TenantsApi(self.api_client)
        self.admissions = AdmissionsApi(self.api_client)
        self.stream_client = httpx.AsyncClient(
            base_url=self.base_url,
            headers={
                "Authorization": f"Bearer {api_key}",
                "User-Agent": f"nvoken-python/{SDK_VERSION}",
                **_scope_headers(self.scope),
            },
            transport=transport,
            timeout=None,
        )
        stream_configuration = Configuration(host=self.base_url, access_token=api_key)
        stream_configuration.discard_unknown_keys = False
        self.stream_api_client = ApiClient(stream_configuration)
        for name, value in _scope_headers(self.scope).items():
            self.stream_api_client.set_default_header(name, value)
        self.stream_api_client.rest_client.pool_manager = _StreamingPoolManager(
            self.stream_client
        )
        self.stream_sessions = SessionsApi(self.stream_api_client)

    def scoped(self, scope: Scope) -> Client:
        """Return a Client that stamps this scope on every request it makes.

        The receiver is unchanged, so a scoped client can be handed to the part
        of an application that handles one tenant's or one end user's work
        while the unscoped one keeps doing administrative reads. Closing one
        does not close the other: each holds its own connections.
        """
        if _narrowing_scope(scope) is None:
            raise NvokenError(
                "validation", "a scope requires a tenant_key, a user_key, or both"
            )
        narrowed = Client(
            self.base_url,
            self.api_key,
            retry=self.retry,
            transport=self.transport,
            scope=scope,
        )
        narrowed._session_locks = self._session_locks
        return narrowed

    async def __aenter__(self) -> Client:
        return self

    async def __aexit__(self, *_: object) -> None:
        await self.close()

    async def close(self) -> None:
        await self.api_client.close()
        await self.stream_api_client.close()

    def raw(self) -> tuple[
        InvocationsApi,
        ModelsApi,
        ProviderKeysApi,
        SessionsApi,
        AgentsApi,
        CreditsApi,
        AgentDefinitionsApi,
        MCPApi,
        IdentityApi,
        MemoriesApi,
        UsageApi,
        AppsApi,
        OrgsApi,
        TenantsApi,
        AdmissionsApi,
    ]:
        """Every generated API, so no endpoint is reachable only by hand.

        New entries are appended, so unpacking a prefix of this keeps working.
        """
        return (
            self.invocations,
            self.models,
            self.provider_keys,
            self.sessions,
            self.agents,
            self.credits,
            self.agent_definitions,
            self.mcp,
            self.identity,
            self.memories,
            self.usage,
            self.apps,
            self.orgs,
            self.tenants,
            self.admissions,
        )

    def agent(self, options: AgentOptions) -> Agent:
        from .agent import Agent

        return Agent(self, options)

    async def create_agent(
        self,
        *,
        agent_key: str,
        definition_id: str | None = None,
        definition_key: str | None = None,
        name: str | None = None,
        tenant_key: str | None = None,
        pinned_revision: int | None = None,
    ) -> AgentResource:
        """Create or resolve one tenant's Agent record.

        Name the Definition with ``definition_id`` or
        ``definition_key`` — exactly one. ``name`` defaults to the Agent
        key.
        """
        if bool(definition_id) == bool(definition_key):
            raise NvokenError(
                "validation",
                "supply exactly one of definition_id and definition_key",
            )
        return await self._replay_safe(lambda: self.agents.create_agent(
            CreateAgentRequest(
                tenant_key=tenant_key,
                agent_key=agent_key,
                name=name,
                definition_id=definition_id,
                definition_key=definition_key,
                pinned_revision=pinned_revision,
            )
        ))

    async def get_agent(self, agent_id: str) -> AgentResource:
        return await self._replay_safe(lambda: self.agents.get_agent(agent_id))

    async def list_agents(
        self,
        *,
        tenant_key: str | None = None,
        agent_key: str | None = None,
        definition_id: str | None = None,
        include_archived: bool | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> AgentList:
        return await self._replay_safe(lambda: self.agents.list_agents(
            tenant_key=tenant_key,
            agent_key=agent_key,
            definition_id=definition_id,
            include_archived=include_archived,
            cursor=cursor,
            limit=limit,
        ))

    async def update_agent(
        self,
        agent_id: str,
        request: UpdateAgentRequest,
    ) -> AgentResource:
        return await self._replay_safe(
            lambda: self.agents.update_agent(agent_id, request)
        )

    async def archive_agent(self, agent_id: str) -> None:
        await self._replay_safe(lambda: self.agents.archive_agent(agent_id))

    async def restore_agent(self, agent_id: str) -> None:
        await self._replay_safe(lambda: self.agents.restore_agent(agent_id))

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

    async def list_mcp_tools(
        self,
        server: MCPServer,
        headers: dict[str, str] | None = None,
    ) -> MCPListToolsResponse:
        """Discover the tools a remote MCP server projects.

        Headers are a separate argument because :class:`MCPServer` is part of a
        reusable Agent Definition and therefore carries no secrets;
        these are used for this one discovery request and never stored.
        """
        return await self._replay_safe(lambda: self.mcp.list_mcp_tools(
            MCPListToolsRequest(
                server=_generated_mcp_server(server),
                headers=dict(headers) if headers else None,
            )
        ))

    async def get_model(self, model: Model) -> ModelDescriptor:
        if not model.id:
            raise NvokenError("validation", "model id is required")
        return await self._replay_safe(lambda: self.models.get_model(
            _model_provider(model.provider),
            model.id,
        ))

    async def register_org(
        self,
        display_name: str,
        *,
        external_ref: str | None = None,
    ) -> Org:
        body = RegisterOrgRequest(
            display_name=display_name,
            external_ref=external_ref,
        )
        call = lambda: self.orgs.register_org(body)
        return await (self._call_once(call) if external_ref is None else self._replay_safe(call))

    async def update_org(self, org_id: str, display_name: str) -> Org:
        return await self._replay_safe(
            lambda: self.orgs.update_org(
                org_id,
                UpdateOrgRequest(display_name=display_name),
            )
        )

    async def register_app(self, request: RegisterAppRequest) -> AppRegistration:
        return await self._call_once(lambda: self.apps.register_app(request))

    async def update_app(self, app_id: str, request: UpdateAppRequest) -> App:
        return await self._replay_safe(lambda: self.apps.update_app(app_id, request))

    async def create_app_client_key(
        self,
        app_id: str,
        request: CreateClientKeyRequest,
    ) -> ClientKey:
        return await self._call_once(
            lambda: self.apps.create_app_client_key(app_id, request)
        )

    async def mint_app_signing_key(
        self,
        app_id: str,
        request: MintAppSigningKeyRequest,
    ) -> AppSigningKeySecret:
        """Mint a receiver secret that is returned exactly once."""
        return await self._call_once(
            lambda: self.apps.mint_app_signing_key(app_id, request)
        )

    async def create_credential(
        self,
        *,
        name: str,
        profile: CredentialProfile,
        app_id: str | None = None,
        org_id: str | None = None,
        tenant_key: str | None = None,
        session_id: str | None = None,
        operations: Sequence[Operation] | None = None,
        expires_at: datetime | None = None,
        idempotency_key: str | None = None,
    ) -> CredentialIssuance:
        body = CreateCredentialRequest(
            name=name,
            profile=profile,
            app_id=app_id,
            org_id=org_id,
            tenant_key=tenant_key,
            session_id=session_id,
            operations=list(operations) if operations is not None else None,
            expires_at=expires_at,
        )
        key = idempotency_key or f"nvoken-{uuid.uuid4()}"
        return await self._replay_safe(
            lambda: self.identity.create_credential(key, body)
        )

    async def invoke(self, request: InvokeRequest) -> InvocationHandle:
        body = self._invocation_body(request)
        idempotency_key = body.idempotency_key
        invocation = _machine_projection(
            await self._replay_safe(lambda: self.invocations.create_invocation(body))
        )
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

    def _agent_definition_body(
        self,
        definition: AgentDefinition,
        *,
        include_key: bool,
    ) -> AgentDefinitionWrite | AgentDefinitionCreate:
        """Render one Agent Definition.

        Creation and replacement share every configuration field; only creation
        carries the immutable ``definition_key``. Each field is named here, which
        is what keeps a read-only field off the wire. Only the definition's own
        content is checked; installation state, App signing keys, credits,
        provider keys, and model lifecycle are checked again at turn admission.
        """
        if definition.model is None:
            raise NvokenError("validation", "agent definition requires model")
        if include_key and not definition.definition_key:
            raise NvokenError("validation", "agent definition requires definition_key")
        if definition.tool_choice is not None:
            _preflight_tool_choice(definition.tool_choice)
        if definition.output_schema is not None:
            preflight_output_schema(definition.output_schema)
        tools: list[GeneratedToolDeclaration] = []
        for tool in definition.tools:
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
        body = dict(
            name=definition.name,
            instructions=definition.instructions,
            model=GeneratedModelInput(GeneratedModel(
                provider=definition.model.provider,
                id=definition.model.id,
            )),
            sampling=GeneratedSampling(temperature=definition.sampling.temperature)
            if definition.sampling is not None
            else None,
            reasoning=GeneratedReasoning(
                    effort=ReasoningEffort(definition.reasoning.effort)
                    if definition.reasoning.effort is not None
                    else None,
                    budget_tokens=definition.reasoning.budget_tokens,
                )
                if definition.reasoning is not None
                else None,
            tool_choice=GeneratedToolChoice(
                    mode=ModelToolChoiceMode(definition.tool_choice.mode),
                    name=definition.tool_choice.name,
                )
                if definition.tool_choice is not None
                else None,
            limits=GeneratedLimits(**vars(definition.limits))
            if definition.limits
            else None,
            tools=tools or None,
            mcp_servers=[
                    _generated_mcp_server(server)
                    for server in definition.mcp_servers
                ] or None,
            provider_tools=[
                    _generated_provider_tool(tool)
                    for tool in definition.provider_tools
                ] or None,
            memory=_generated_memory_config(definition.memory),
            client_interface=_generated_client_interface(definition.client_interface),
            output_schema=definition.output_schema,
        )
        if include_key:
            return AgentDefinitionCreate(
                definition_key=definition.definition_key,
                **body,
            )
        # A replacement cannot move a resource to another key, so a definition
        # read back from the server carries one that is dropped here.
        return AgentDefinitionWrite(**body)

    def _mcp_server_headers(
        self,
        request: InvokeRequest,
    ) -> list[GeneratedMCPServerHeaders] | None:
        """Check the per-turn MCP secret headers.

        The selected Agent Definition is server-owned, so server-name
        validation is left to the service.
        """
        seen: set[str] = set()
        entries: list[GeneratedMCPServerHeaders] = []
        for entry in request.mcp_server_headers:
            if not entry.name:
                raise NvokenError("validation", "mcp server headers require a server name")
            if not entry.headers:
                raise NvokenError(
                    "validation",
                    f"mcp server headers for {entry.name} require at least one header",
                )
            if entry.name in seen:
                raise NvokenError(
                    "validation",
                    f"mcp server headers name {entry.name} is repeated",
                )
            seen.add(entry.name)
            entries.append(GeneratedMCPServerHeaders(
                name=entry.name,
                headers=dict(entry.headers),
            ))
        return entries or None

    def _context(
        self,
        request: InvokeRequest,
    ) -> list[GeneratedInvocationContextItem] | None:
        """Check the recorded context against the bounds the Runtime enforces.

        Checked here so a snapshot that cannot be admitted fails before a
        request is spent. The per-Session limit of 16 distinct names is left to
        the service, which is the only side that knows what a Session has
        already recorded.
        """
        if len(request.context) > _MAX_CONTEXT_ITEMS:
            raise NvokenError(
                "validation",
                f"context accepts at most {_MAX_CONTEXT_ITEMS} items",
            )
        seen: set[str] = set()
        total = 0
        items: list[GeneratedInvocationContextItem] = []
        for item in request.context:
            if (
                len(item.name) > _MAX_CONTEXT_NAME_LENGTH
                or not _CONTEXT_NAME_PATTERN.fullmatch(item.name)
            ):
                raise NvokenError(
                    "validation",
                    f"context name {item.name} must match ^[a-z][a-z0-9-]*$ "
                    f"and be at most {_MAX_CONTEXT_NAME_LENGTH} characters",
                )
            if item.name in seen:
                raise NvokenError("validation", f"context name {item.name} is repeated")
            seen.add(item.name)
            if item.tier not in ("contextual", "operator"):
                raise NvokenError(
                    "validation",
                    f"context {item.name} tier must be contextual or operator",
                )
            if not item.content:
                raise NvokenError(
                    "validation",
                    f"context {item.name} content cannot be empty",
                )
            content_bytes = len(item.content.encode("utf-8"))
            if content_bytes > _MAX_CONTEXT_CONTENT_BYTES:
                raise NvokenError(
                    "validation",
                    f"context {item.name} content exceeds "
                    f"{_MAX_CONTEXT_CONTENT_BYTES} bytes",
                )
            total += content_bytes
            if total > _MAX_CONTEXT_TOTAL_BYTES:
                raise NvokenError(
                    "validation",
                    f"context content totals more than {_MAX_CONTEXT_TOTAL_BYTES} bytes",
                )
            items.append(GeneratedInvocationContextItem(
                name=item.name,
                tier=item.tier,
                content=item.content,
            ))
        return items or None

    def _agent_definition_overrides(
        self,
        overrides: AgentDefinitionOverrides | None,
    ) -> GeneratedAgentDefinitionOverrides | None:
        if overrides is None:
            return None
        if not any((
            overrides.model is not None,
            overrides.sampling is not None,
            overrides.reasoning is not None,
            overrides.tool_choice is not None,
            overrides.limits is not None,
            overrides.output_schema is not None,
        )):
            raise NvokenError("validation", "overrides require at least one member")
        if overrides.output_schema is not None:
            preflight_output_schema(overrides.output_schema)
        return GeneratedAgentDefinitionOverrides(
            model=GeneratedModelInput(GeneratedModel(
                provider=overrides.model.provider,
                id=overrides.model.id,
            )) if overrides.model is not None else None,
            sampling=GeneratedSampling(temperature=overrides.sampling.temperature)
            if overrides.sampling is not None
            else None,
            reasoning=GeneratedReasoning(
                effort=ReasoningEffort(overrides.reasoning.effort)
                if overrides.reasoning.effort is not None
                else None,
                budget_tokens=overrides.reasoning.budget_tokens,
            ) if overrides.reasoning is not None else None,
            tool_choice=GeneratedToolChoice(
                mode=ModelToolChoiceMode(overrides.tool_choice.mode),
                name=overrides.tool_choice.name,
            ) if overrides.tool_choice is not None else None,
            limits=GeneratedLimits(**vars(overrides.limits))
            if overrides.limits is not None
            else None,
            output_schema=overrides.output_schema,
        )

    def _invocation_body(self, request: InvokeRequest) -> CreateInvocationRequest:
        if not request.input:
            raise NvokenError("validation", "input is required")
        if bool(request.agent_id) == bool(request.agent_key):
            raise NvokenError(
                "validation",
                "supply exactly one of agent_id and agent_key",
            )
        preflight_input(request.input)
        if request.if_active not in (None, "reject", "supersede", "interrupt"):
            raise NvokenError(
                "validation",
                "if_active must be reject, supersede, or interrupt",
            )
        if request.on_budget_exhausted not in (None, "stop", "hold"):
            raise NvokenError(
                "validation",
                "on_budget_exhausted must be stop or hold",
            )
        idempotency_key = request.idempotency_key or f"nvoken-{uuid.uuid4()}"
        return CreateInvocationRequest(
            agent_id=request.agent_id,
            agent_key=request.agent_key,
            tenant_key=request.tenant_key,
            user_key=request.user_key,
            session_id=request.session_id,
            session_key=request.session_key,
            session_options=_generated_session_options(request.session_options),
            triggered_by=request.triggered_by,
            metadata=dict(request.metadata) if request.metadata else None,
            idempotency_key=idempotency_key,
            if_active=request.if_active,
            on_budget_exhausted=request.on_budget_exhausted,
            input=InvocationInput(
                request.input
                if isinstance(request.input, str)
                else [input_block_wire(block) for block in request.input]
            ),
            definition_revision=request.definition_revision,
            overrides=self._agent_definition_overrides(request.overrides),
            mcp_server_headers=self._mcp_server_headers(request),
            context=self._context(request),
            provider_keys=[
                _provider_key_selection(selection)
                for selection in request.provider_keys
            ] or None,
            webhook=_generated_webhook_target(request.webhook),
        )

    async def create_agent_definition(
        self,
        definition: AgentDefinition,
        *,
        idempotency_key: str | None = None,
    ) -> AgentDefinitionResource:
        """Create one App-owned Agent Definition, or return the one the key names.

        The definition's ``definition_key`` is unique within the App, so this is
        ensure-shaped: restating an existing definition returns it, and a key
        already held by a different definition is a conflict naming the resource
        to update instead. ``name`` defaults to the key. ``idempotency_key`` is
        optional and pins replay to a specific create; the key already scopes
        replay without it.
        """
        resource, _ = await self._ensure_agent_definition(
            definition,
            idempotency_key=idempotency_key,
        )
        return resource

    async def _ensure_agent_definition(
        self,
        definition: AgentDefinition,
        *,
        idempotency_key: str | None = None,
    ) -> tuple[AgentDefinitionResource, bool]:
        """The created-or-resolved resource, and whether this call minted it.

        The status carries that and the body does not: 201 for a create, 200
        for a restatement that resolved to what already existed.
        """
        body = self._agent_definition_body(definition, include_key=True)
        response = await self._replay_safe(
            lambda: self.agent_definitions.create_agent_definition_with_http_info(
                body,
                idempotency_key=idempotency_key,
            )
        )
        return response.data, response.status_code == 201

    async def get_agent_definition_by_key(
        self,
        definition_key: str,
        *,
        include_archived: bool | None = None,
    ) -> AgentDefinitionResource | None:
        """Read the Agent Definition a key names, or ``None`` when it names none.

        The key is unique within the App, so this is a lookup rather than a
        search: nothing to paginate, nothing to filter, no duplicate to detect.
        """
        if not definition_key:
            raise NvokenError("validation", "definition_key is required")
        page = await self.list_agent_definitions(
            definition_key=definition_key,
            include_archived=include_archived,
        )
        return page.items[0] if page.items else None

    async def get_agent_definition(
        self,
        definition_id: str,
    ) -> AgentDefinitionResource:
        return await self._replay_safe(
            lambda: self.agent_definitions.get_agent_definition(definition_id)
        )

    async def get_agent_definition_revision(
        self,
        definition_id: str,
        revision: int,
    ) -> AgentDefinitionResource:
        return await self._replay_safe(
            lambda: self.agent_definitions.get_agent_definition_revision(
                definition_id,
                revision,
            )
        )

    async def list_agent_definitions(
        self,
        *,
        definition_key: str | None = None,
        include_archived: bool | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> AgentDefinitionResourceList:
        return await self._replay_safe(
            lambda: self.agent_definitions.list_agent_definitions(
                definition_key=definition_key,
                include_archived=include_archived,
                cursor=cursor,
                limit=limit,
            )
        )

    async def update_agent_definition(
        self,
        definition_id: str,
        definition: AgentDefinition,
        *,
        expected_revision: int | Literal["*"],
    ) -> AgentDefinitionResource:
        """Replace one Agent Definition, failing when it has moved on.

        This replaces the whole resource, so send back everything you want kept.
        ``AgentDefinition.from_resource`` reads a resource back into the
        definition that produced it, carrying every writable field across.

        Replacement is ensure-shaped: a definition already matching the current
        revision publishes nothing and returns that revision unchanged. So this
        is safe to call with contents you are not sure differ — see
        :meth:`sync_definitions`, which is that call in a loop.

        ``expected_revision`` may be ``"*"``, meaning "I read no revision;
        replace whichever is current" — the honest precondition for a caller
        syncing from its own source of truth, which has nothing to be stale
        against. It is never refused as stale, and it still cannot create: the
        Definition must already exist. A number keeps its own meaning, "I am
        replacing the revision I read", so one that has since moved is refused
        even if the replacement happens to match it.
        """
        resource, _ = await self._replace_agent_definition(
            definition_id,
            definition,
            expected_revision,
        )
        return resource

    async def _replace_agent_definition(
        self,
        definition_id: str,
        definition: AgentDefinition,
        expected_revision: int | Literal["*"],
    ) -> tuple[AgentDefinitionResource, bool]:
        """The replacement, and whether a revision was published.

        The status carries that and the body does not: 201 for a published
        revision, 200 for a request the current revision already satisfied.
        """
        if expected_revision != "*" and expected_revision < 1:
            raise NvokenError("validation", 'expected_revision must be positive, or "*"')
        if_match = "*" if expected_revision == "*" else f'"{expected_revision}"'
        body = self._agent_definition_body(definition, include_key=False)
        response = await self._replay_safe(
            lambda: self.agent_definitions.update_agent_definition_with_http_info(
                if_match,
                definition_id,
                body,
            )
        )
        return response.data, response.status_code == 201

    async def sync_definitions(
        self,
        definitions: Sequence[AgentDefinition],
    ) -> list[DefinitionSync]:
        """Make nvoken hold exactly these definitions, publishing only differences.

        This is a write-only loop: nothing is read back and nothing is compared
        here. Both write paths are ensure-shaped, so nvoken decides what moved —
        which matters because it canonicalizes a definition before comparing it,
        and a caller reproducing that comparison would be maintaining a second
        copy of the rule, in another language, free to disagree the first time
        either side gains a field.

        ::

            for synced in await client.sync_definitions(definitions):
                if synced.outcome != "unchanged":
                    print(f"{synced.definition_key}: {synced.outcome}")

        Each definition costs one call, or two when its contents changed: the
        create conflict names the resource to replace, so nothing has to be
        looked up.

        It is sequential and stops at the first error, which is the useful
        behavior for a deploy step. A key held by an archived Definition is one
        of those errors rather than an outcome: restoring it is a decision, not
        a sync.
        """
        results: list[DefinitionSync] = []
        for definition in definitions:
            if not definition.definition_key:
                raise NvokenError("validation", "definition_key is required")
            try:
                resource, created = await self._ensure_agent_definition(definition)
            except NvokenError as conflict:
                # The conflict names the resource holding the key, so the
                # replacement it points at needs no lookup first.
                definition_id = (
                    (conflict.details or {}).get("definition_id")
                    if conflict.code == "agent_definition_key_conflict"
                    else None
                )
                if not isinstance(definition_id, str) or not definition_id:
                    raise
                # "*", because nothing was read: the conflict proves the
                # resource exists and differs, not which revision it is at.
                resource, published = await self._replace_agent_definition(
                    definition_id,
                    definition,
                    "*",
                )
                results.append(
                    DefinitionSync(
                        definition_key=definition.definition_key,
                        # Not published means someone else published these
                        # contents between the two calls.
                        outcome="updated" if published else "unchanged",
                        definition=resource,
                    )
                )
                continue
            # The create either minted the resource or resolved to one already
            # holding these exact contents. Either way nvoken now holds them.
            results.append(
                DefinitionSync(
                    definition_key=definition.definition_key,
                    outcome="created" if created else "unchanged",
                    definition=resource,
                )
            )
        return results

    async def archive_agent_definition(self, definition_id: str) -> None:
        await self._replay_safe(
            lambda: self.agent_definitions.archive_agent_definition(definition_id)
        )

    async def restore_agent_definition(self, definition_id: str) -> None:
        await self._replay_safe(
            lambda: self.agent_definitions.restore_agent_definition(definition_id)
        )

    def invocation(self, invocation_id: str) -> InvocationHandle:
        return InvocationHandle(self, invocation_id)

    async def get_invocation(self, invocation_id: str) -> Invocation:
        return _machine_projection(
            await self._replay_safe(lambda: self.invocations.get_invocation(invocation_id))
        )

    async def get_invocation_result(self, invocation_id: str) -> InvocationResult:
        return _machine_projection(
            await self._replay_safe(
                lambda: self.invocations.get_invocation_result(invocation_id)
            )
        )

    async def cancel_invocation(self, invocation_id: str) -> Invocation:
        return _machine_projection(
            await self._replay_safe(lambda: self.invocations.cancel_invocation(invocation_id))
        )

    async def interrupt_invocation(self, invocation_id: str) -> Invocation:
        """Stop an Invocation gracefully and keep its work.

        The turn settles ``completed`` with stop reason ``interrupted`` once it
        reaches an execution seam, so the messages it already produced stay in
        the Session. :meth:`cancel_invocation` is the discarding stop.
        """
        return _machine_projection(
            await self._replay_safe(
                lambda: self.invocations.interrupt_invocation(invocation_id)
            )
        )

    async def create_nudge(
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
        marked ``expired`` when the Invocation ends; nvoken never re-homes
        it onto a later turn, so re-sending missed direction as the next
        Invocation's input is the caller's decision.

        Passing ``idempotency_key`` makes a retry safe: the same key with the
        same content returns the original acknowledgement with ``deduplicated``
        set, and the same key with different content is refused.
        """
        body = CreateNudgeRequest(
            content=InvocationInput(content),
            idempotency_key=idempotency_key,
        )
        call = lambda: self.invocations.create_nudge(invocation_id, body)
        if idempotency_key is None:
            # Without a key a retried POST would stage the same direction twice.
            return await call()
        return await self._replay_safe(call)

    async def list_nudges(
        self,
        invocation_id: str,
        *,
        status: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> NudgeList:
        """Read the staged queue in the order the turn will consume it."""
        return await self._replay_safe(
            lambda: self.invocations.list_nudges(
                invocation_id,
                status=NudgeStatus(status) if status is not None else None,
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

    async def cancel_nudge(
        self,
        invocation_id: str,
        nudge_id: str,
    ) -> Nudge:
        """Withdraw staged input the turn has not taken.

        Input the executor already drained is reported as a conflict rather
        than removed from a transcript it is already part of.
        """
        return await self._replay_safe(
            lambda: self.invocations.cancel_nudge(invocation_id, nudge_id)
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

    async def list_invocation_logs(
        self,
        invocation_id: str,
        *,
        cursor: str | None = None,
        limit: int | None = None,
        trace_id: str | None = None,
    ) -> InvocationLogList:
        """Read bounded, content-free operational logs for one Invocation."""
        return await self._replay_safe(
            lambda: self.invocations.list_invocation_logs(
                invocation_id,
                cursor=cursor,
                limit=limit,
                trace_id=trace_id,
            )
        )

    async def list_memories(
        self,
        *,
        agent_id: str,
        tenant_key: str | None = None,
        user_key: str | None = None,
        query: str | None = None,
        search_mode: MemorySearchMode | None = None,
        kind: MemoryKind | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> MemoryList:
        """Browse or search durable memories for one Agent and scope."""
        return await self._replay_safe(
            lambda: self.memories.list_memories(
                agent_id,
                tenant_key=tenant_key,
                user_key=user_key,
                query=query,
                search_mode=search_mode,
                kind=kind,
                cursor=cursor,
                limit=limit,
            )
        )

    async def list_invocations(
        self,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        user_key: str | None = None,
        session_id: str | None = None,
        agent_id: str | None = None,
        agent_key: str | None = None,
        status: InvocationStatus | list[InvocationStatus] | None = None,
        parent_invocation_id: str | None = None,
        ended: bool | None = None,
        ended_since: datetime | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> InvocationList:
        return _machine_projection(
            await self._replay_safe(lambda: self.invocations.list_invocations(
                tenant_key=tenant_key,
                default_tenant=default_tenant,
                user_key=user_key,
                session_id=session_id,
                agent_id=agent_id,
                agent_key=agent_key,
                status=(
                    status
                    if isinstance(status, list)
                    else [status] if status is not None else None
                ),
                parent_invocation_id=parent_invocation_id,
                ended=ended,
                ended_since=ended_since,
                cursor=cursor,
                limit=limit,
            ))
        )

    async def list_ended_invocations(
        self,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        user_key: str | None = None,
        session_id: str | None = None,
        agent_id: str | None = None,
        agent_key: str | None = None,
        status: InvocationStatus | list[InvocationStatus] | None = None,
        parent_invocation_id: str | None = None,
        ended_since: datetime | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> InvocationList:
        """Read one page of the reconciliation feed.

        Turns that ended, oldest first by the moment they ended. Walk it and
        append by ``id``.

        This is the backstop for settlement. ``invocation.ended`` webhooks are
        delivered at least once, so a delivery that never lands leaves a turn
        nobody settles — silently, since nothing errors and the only evidence is
        a ledger row that was never written. Reading this to the end is how you
        find out. :meth:`list_invocations` cannot stand in: it is newest-first
        over current state, so a turn ending mid-page moves under you and a
        terminal status filter gives a set with no position in it.

        ``next_cursor`` is always set here, including on an empty page, so a
        consumer that catches up keeps its place without special-casing. Keep
        calling while ``has_more``; when it is False you are caught up.

        ``complete_through`` is the instant the feed is complete to. Turns that
        ended after it are held back until their settling transactions are
        certainly visible, because a turn appearing behind your cursor is one
        you never see again. It is also the value to alarm on: one that stops
        advancing means settlement has stalled rather than that nothing ended.

        There is deliberately no auto-paging iterator. The cursor is the one
        thing that has to survive the process, and hiding it is how a consumer
        loses its place; store it yourself between pages.

        ``ended_since`` starts a feed that has no cursor yet, and is mutually
        exclusive with ``cursor``.
        """
        return await self.list_invocations(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            session_id=session_id,
            agent_id=agent_id,
            agent_key=agent_key,
            status=status,
            parent_invocation_id=parent_invocation_id,
            ended=True,
            ended_since=ended_since,
            cursor=cursor,
            limit=limit,
        )

    async def invocation_items(
        self,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        session_id: str | None = None,
        agent_id: str | None = None,
        agent_key: str | None = None,
        status: InvocationStatus | list[InvocationStatus] | None = None,
        parent_invocation_id: str | None = None,
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
                parent_invocation_id=parent_invocation_id,
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
        return _machine_projection(
            await self._replay_safe(lambda: self.sessions.list_sessions(
                tenant_key=tenant_key,
                default_tenant=default_tenant,
                agent_id=agent_id,
                agent_key=agent_key,
                session_key=session_key,
                cursor=cursor,
                limit=limit,
            ))
        )

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
        return _machine_projection(
            await self._replay_safe(lambda: self.sessions.get_session(session_id))
        )

    async def create_session(
        self,
        request: CreateSessionRequest | None = None,
    ) -> Session:
        """Create an empty or seeded Session without starting a turn."""
        body = request or CreateSessionRequest()
        call = lambda: self.sessions.create_session(body)
        response = await (
            self._replay_safe(call)
            if body.session_key is not None
            else self._call_once(call)
        )
        return _machine_projection(response)

    async def fork_session(
        self,
        source_session_id: str,
        request: ForkSessionRequest,
    ) -> Session:
        """Copy a Session prefix into a new Session."""
        call = lambda: self.sessions.fork_session(source_session_id, request)
        response = await (
            self._replay_safe(call)
            if request.session_key is not None
            else self._call_once(call)
        )
        return _machine_projection(response)

    async def delete_session(self, session_id: str, *, force: bool = False) -> None:
        """Erase a Session and everything under it.

        Removes its Invocations, transcript, checkpoints, tool calls,
        artifacts, and undelivered webhooks. The erasure is immediate and
        irreversible.

        A Session holding a nonterminal Invocation is refused with
        ``session_invocation_active`` unless ``force``. Erasure skips
        settlement — the Invocation is removed rather than ended, so it records
        no terminal status and emits no ``invocation.ended`` webhook — which is
        why a caller that bills or reconciles on settlement must cancel first
        and wait for the final state.

        ``force`` erases anyway, over a live turn. It is for erasing on an end
        user's behalf, where removing the transcript now outranks keeping a
        settled record: a deletion request has to be honoured, and a refusal
        thrown into that path leaves it unhonoured.

        An unknown or out-of-scope Session is not found, so a retry after a
        lost response can treat that as already-done.

        This is not account erasure by itself: nvoken keeps no account
        tombstone, so a caller honouring a deletion request must stop admitting
        work for the tenant before paginating and deleting.
        """
        # Deletion is idempotent by shape — a repeat is not-found rather than a
        # second erasure — so it is safe to replay.
        await self._replay_safe(
            lambda: self.sessions.delete_session(session_id, force=force or None)
        )

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
        order: ListOrder | None = None,
    ) -> SessionMessageList:
        return _machine_projection(
            await self._replay_safe(
                lambda: self.sessions.list_session_messages(
                    session_id,
                    cursor=cursor,
                    limit=limit,
                    order=order,
                )
            )
        )

    async def session_message_items(
        self,
        session_id: str,
        *,
        limit: int | None = None,
        order: ListOrder | None = None,
    ) -> AsyncIterator[SessionMessage]:
        cursor: str | None = None
        while True:
            page = await self.list_session_messages(
                session_id,
                cursor=cursor,
                limit=limit,
                order=order,
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
        tail: bool | None = None,
    ) -> TranscriptSnapshot:
        return _machine_projection(
            await self._replay_safe(
                lambda: self.sessions.get_session_transcript(
                    session_id,
                    cursor=cursor,
                    page_token=page_token,
                    limit=limit,
                    tail=tail,
                )
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
        # The caller's cursor opens the drain; the pages then report the one
        # this drain ends at. Keeping them in separate names is what stops the
        # first request from losing the position the caller asked to start at.
        drained_cursor: str | None = None
        while True:
            page = await self.get_transcript_page(
                session_id,
                cursor=cursor if page_token is None else None,
                page_token=page_token,
                limit=limit,
            )
            messages.extend(page.messages)
            changes.extend(page.invocation_changes)
            drained_cursor = page.cursor
            page_token = page.next_page_token
            if not page.has_more:
                if not drained_cursor:
                    raise NvokenError(
                        "unexpected_response",
                        "Transcript drain did not return a resume cursor",
                    )
                return TranscriptDrain(
                    messages=messages,
                    invocation_changes=changes,
                    cursor=drained_cursor,
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

    async def allocate_credits(
        self,
        *,
        amount: str,
        tenant_key: str | None = None,
        default_tenant: bool = False,
        reference: str | None = None,
        idempotency_key: str | None = None,
    ) -> AllocateCreditsResult:
        body = AllocateCreditsRequest(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            amount=Money(amount=amount, currency="USD"),
            reference=reference,
            idempotency_key=idempotency_key or f"nvoken-{uuid.uuid4()}",
        )
        return await self._replay_safe(lambda: self.credits.allocate_credits(body))

    async def list_credit_accounts(
        self,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> CreditAccountList:
        return await self._replay_safe(
            lambda: self.credits.list_credit_accounts(
                tenant_key=tenant_key,
                default_tenant=default_tenant,
                cursor=cursor,
                limit=limit,
            )
        )

    async def list_credit_allocations(
        self,
        *,
        tenant_key: str | None = None,
        default_tenant: bool | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> CreditAllocationList:
        return await self._replay_safe(
            lambda: self.credits.list_credit_allocations(
                tenant_key=tenant_key,
                default_tenant=default_tenant,
                cursor=cursor,
                limit=limit,
            )
        )

    async def stream_session(
        self,
        session_id: str,
        reducer: Reducer,
        consume: Callable[[StreamEvent, ReducedSnapshot], Awaitable[None] | None],
        *,
        deltas: bool = True,
    ) -> None:
        await stream_session(self, session_id, reducer, consume, deltas=deltas)

    async def _call_once(self, operation: Callable[[], Awaitable[Any]]) -> Any:
        try:
            return await operation()
        except (ApiException, httpx.HTTPError) as error:
            raise normalize_error(error) from error

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


def _machine_projection(response: Any) -> Any:
    """Unwrap a generated machine-or-browser response union."""
    return getattr(response, "actual_instance", response)


def _agent_definition_from_resource(
    definition_type: type[AgentDefinition],
    resource: AgentDefinitionResource,
) -> AgentDefinition:
    """Read one resource back into the definition that produced it.

    The resource is walked as its wire dictionary rather than field by field,
    so a writable field added to the contract arrives here without this
    function being edited. A replacement replaces the whole resource, so a
    field dropped here would be erased on the next write.
    """
    body = resource.to_dict()
    tools: list[Tool | BuiltinTool] = []
    for declaration in body.get("tools") or ():
        if declaration.get("mode") == "builtin":
            tools.append(BuiltinTool())
            continue
        callback = declaration.get("callback") or {}
        tools.append(Tool(
            mode=declaration["mode"],
            name=declaration["name"],
            description=declaration.get("description", ""),
            input_schema=declaration.get("input_schema") or {},
            callback_url=callback.get("url"),
        ))
    memory = body.get("memory")
    context = (memory or {}).get("context")
    client_interface = body.get("client_interface")
    limits = body.get("limits")
    sampling = body.get("sampling")
    reasoning = body.get("reasoning")
    tool_choice = body.get("tool_choice")
    return definition_type(
        model=Model(provider=body["model"]["provider"], id=body["model"]["id"]),
        definition_key=body.get("definition_key"),
        name=body.get("name"),
        instructions=body.get("instructions"),
        sampling=Sampling(temperature=sampling["temperature"]) if sampling else None,
        reasoning=Reasoning(
            effort=reasoning.get("effort"),
            budget_tokens=reasoning.get("budget_tokens"),
        ) if reasoning else None,
        tool_choice=ToolChoice(
            mode=tool_choice["mode"],
            name=tool_choice.get("name"),
        ) if tool_choice else None,
        limits=Limits(**limits) if limits else None,
        tools=tuple(tools),
        mcp_servers=tuple(
            MCPServer(
                name=server["name"],
                url=server["url"],
                transport=server.get("transport", "streamable_http"),
                allowed_tools=tuple(server.get("allowed_tools") or ()),
                timeouts=MCPTimeouts(**server["timeouts"]) if server.get("timeouts") else None,
            )
            for server in body.get("mcp_servers") or ()
        ),
        provider_tools=tuple(
            _provider_tool_from_wire(tool) for tool in body.get("provider_tools") or ()
        ),
        memory=MemoryConfig(
            scope=memory.get("scope"),
            context=MemoryContextConfig(
                mode=context.get("mode"),
                max_bytes=context.get("max_bytes"),
            ) if context else None,
        ) if memory else None,
        client_interface=ClientInterface(
            context_names=tuple(client_interface.get("context_names") or ()),
            tool_names=tuple(client_interface.get("tool_names") or ()),
        ) if client_interface is not None else None,
        output_schema=body.get("output_schema"),
    )


def _provider_tool_from_wire(tool: dict[str, Any]) -> ProviderTool:
    search = tool.get("web_search") or {}
    location = search.get("user_location")
    return ProviderTool(
        type=tool.get("type", "web_search"),
        web_search=WebSearchTool(
            max_uses=search.get("max_uses"),
            allowed_domains=tuple(search.get("allowed_domains") or ()),
            blocked_domains=tuple(search.get("blocked_domains") or ()),
            user_location=WebSearchLocation(**location) if location else None,
        ),
    )


def _preflight_tool_choice(tool_choice: ToolChoice) -> None:
    """Check that a tool choice names a tool exactly when its mode takes one."""
    if tool_choice.mode == "named":
        if not tool_choice.name:
            raise NvokenError("validation", "tool_choice named requires name")
        return
    if tool_choice.name:
        raise NvokenError(
            "validation",
            f"tool_choice {tool_choice.mode} cannot include name",
        )


def _generated_memory_config(memory: MemoryConfig | None) -> GeneratedMemoryConfig | None:
    if memory is None:
        return None
    context = memory.context
    return GeneratedMemoryConfig(
        scope=memory.scope,
        context=GeneratedMemoryContextConfig(
            mode=GeneratedMemoryContextMode(context.mode) if context.mode else None,
            max_bytes=context.max_bytes,
        ) if context is not None else None,
    )


def _generated_client_interface(
    client_interface: ClientInterface | None,
) -> BrowserClientInterface | None:
    """Render one browser authorization.

    An empty ``ClientInterface()`` is not the same as ``None``: it opts the
    definition into client tokens with no client-authored context or tools, so
    the empty object has to reach the wire.
    """
    if client_interface is None:
        return None
    return BrowserClientInterface(
        context_names=list(client_interface.context_names) or None,
        tool_names=list(client_interface.tool_names) or None,
    )


def _generated_mcp_server(server: MCPServer) -> GeneratedMCPServer:
    timeouts = server.timeouts
    return GeneratedMCPServer(
        name=server.name,
        url=server.url,
        transport=server.transport,
        allowed_tools=list(server.allowed_tools) or None,
        timeouts=GeneratedMCPTimeouts(
            discovery_seconds=timeouts.discovery_seconds,
            call_seconds=timeouts.call_seconds,
        ) if timeouts else None,
    )


def _narrowing_scope(scope: Scope | None) -> Scope | None:
    """Drop a scope that names nobody, so an empty one is not a silent no-op."""
    if scope is None:
        return None
    tenant_key = (scope.tenant_key or "").strip()
    user_key = (scope.user_key or "").strip()
    if not tenant_key and not user_key:
        return None
    return Scope(tenant_key=tenant_key or None, user_key=user_key or None)


def _scope_headers(scope: Scope | None) -> dict[str, str]:
    if scope is None:
        return {}
    headers: dict[str, str] = {}
    if scope.tenant_key:
        headers["X-Nvoken-Tenant-Key"] = scope.tenant_key
    if scope.user_key:
        headers["X-Nvoken-User-Key"] = scope.user_key
    return headers


def _generated_session_options(
    options: SessionOptions | None,
) -> GeneratedSessionOptions | None:
    if options is None:
        return None
    if (
        options.compaction is None
        and options.retention is None
        and not options.authorization_context
        and options.pinned_revision is None
        and options.on_conflict is None
    ):
        raise NvokenError("validation", "session_options requires at least one member")
    if options.on_conflict is not None and options.on_conflict not in ("refuse", "join"):
        raise NvokenError(
            "validation", "session_options on_conflict must be refuse or join"
        )
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
        authorization_context=dict(options.authorization_context)
        if options.authorization_context
        else None,
        pinned_revision=options.pinned_revision,
        on_conflict=options.on_conflict,
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

    async def require_session_id(self) -> str:
        """The Session this turn belongs to, resolving it if the handle lacks it.

        The stream is Session-scoped, so a handle built from a bare Invocation
        ID resolves its Session before the stream opens.
        """
        if self.session_id:
            return self.session_id
        await self.refresh()
        if not self.session_id:
            raise NvokenError("unexpected_response", "the Invocation carried no Session")
        return self.session_id

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
        return await self.client.create_nudge(
            self.invocation_id,
            content,
            idempotency_key=idempotency_key,
        )

    async def list_nudges(
        self,
        *,
        status: str | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> NudgeList:
        return await self.client.list_nudges(
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

    async def cancel_nudge(self, nudge_id: str) -> Nudge:
        return await self.client.cancel_nudge(self.invocation_id, nudge_id)

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


# One encoding, in `invocation_status`. Re-bound here because this module's wait
# helpers read it, and because `incomplete` being terminal is the part callers
# get wrong: a wait that treated only `completed` as an ending would poll a
# budget-stopped turn forever.
TERMINAL_STATUSES = TERMINAL_INVOCATION_STATUSES


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


def is_not_found(error: object) -> TypeGuard[NvokenError]:
    """Whether an SDK call failed because its resource was not found."""
    return isinstance(error, NvokenError) and error.category == "not_found"


async def issue_anonymous_token(
    base_url: str,
    app_id: str,
    origin: str,
    idempotency_key: str,
    *,
    visitor_token: str | None = None,
) -> AnonymousTokenResponse:
    """Mint or renew credential-free browser access for one configured App."""
    if not base_url or not app_id or not origin or not idempotency_key:
        raise NvokenError(
            "validation",
            "base_url, app_id, origin, and idempotency_key are required",
        )
    configuration = Configuration(host=base_url.rstrip("/"))
    configuration.discard_unknown_keys = False
    try:
        async with ApiClient(configuration) as api_client:
            return await AppsApi(api_client).issue_anonymous_token(
                app_id,
                origin,
                idempotency_key,
                AnonymousTokenRequest(visitor_token=visitor_token),
            )
    except ApiException as error:
        raise normalize_error(error) from error


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
