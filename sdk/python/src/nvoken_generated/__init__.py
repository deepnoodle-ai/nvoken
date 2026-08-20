# coding: utf-8

# flake8: noqa

"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Acting for one tenant or one end user  An app-wide credential can reach every tenant in its App, which means an id that arrives from the wrong place — a stale link, a mixed-up webhook, a tampered form field — is an id it can act on. The usual answer is for the host to re-read the resource and compare its `tenant_key` and `user_key` before every call. Say it once instead:  ``` X-Nvoken-Tenant-Key: acme X-Nvoken-User-Key: user-7c1f ```  Both headers are accepted on every request and narrow that one request to the scope they name. Anything outside it is reported as `not_found`, so a Session or Invocation belonging to another tenant or another end user cannot be read, cancelled, interrupted, forked, answered, or erased — and you learn nothing about whether the id exists. Writes take the same scope: an omitted `tenant_key` or `user_key` in the body inherits it, and one naming somebody else is refused.  Two rules keep them honest. They may only narrow, so a credential already bound to one tenant refuses a header naming another with `forbidden` rather than silently returning nothing. And a record with no `user_key` at all is outside every `X-Nvoken-User-Key` assertion rather than inside all of them, because a Session nobody claimed is not one you can claim by asserting.  Each header takes 1 to 255 characters and may appear once. A blank, oversized, or repeated header is `invalid_request` rather than ignored: an assertion nobody notices dropping is worse than no assertion at all.  A browser token already carries its tenant and its end user, so it neither needs these headers nor is allowed to send them.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for, so you can filter on it later — and on an Agent whose Definition sets `memory.scope: user`, it also selects which memories that Agent can recall. It is fixed by the request that opens a Session and a later turn cannot change it. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource's name, so `session_id` on an Invocation is the Session it belongs to. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken's identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


__version__ = "0.24.0"

# Define package exports
__all__ = [
    "AdmissionsApi",
    "AgentDefinitionsApi",
    "AgentsApi",
    "AppsApi",
    "CreditsApi",
    "IdentityApi",
    "InvocationsApi",
    "MCPApi",
    "MemoriesApi",
    "ModelsApi",
    "OperationsApi",
    "OrgsApi",
    "ProviderKeysApi",
    "SessionsApi",
    "TenantsApi",
    "UsageApi",
    "ApiResponse",
    "ApiClient",
    "Configuration",
    "OpenApiException",
    "ApiTypeError",
    "ApiValueError",
    "ApiKeyError",
    "ApiAttributeError",
    "ApiException",
    "ActivityMetrics",
    "AdmissionAttempt",
    "AdmissionAttemptList",
    "AdmissionOutcome",
    "AdmissionReasonCount",
    "AdmissionSummary",
    "AdmissionTenantCount",
    "Agent",
    "AgentDefinition",
    "AgentDefinitionCreate",
    "AgentDefinitionOverrides",
    "AgentDefinitionResource",
    "AgentDefinitionResourceList",
    "AgentDefinitionWrite",
    "AgentList",
    "AllocateCreditsRequest",
    "AllocateCreditsResult",
    "AnonymousAccess",
    "AnonymousTokenRequest",
    "AnonymousTokenResponse",
    "App",
    "AppDefaultRateLimits",
    "AppList",
    "AppRegistration",
    "AppSigningKey",
    "AppSigningKeyList",
    "AppSigningKeyPurpose",
    "AppSigningKeySecret",
    "AuthenticationMethod",
    "BrowserAccess",
    "BrowserClientInterface",
    "BrowserInvocationWebhook",
    "BrowserRateLimits",
    "BuiltinToolDeclaration",
    "CallbackDeliveryOutcome",
    "CallbackTarget",
    "CallbackToolDeclaration",
    "CharLocationCitation",
    "Citation",
    "ClientKey",
    "ClientKeyList",
    "CompactionPolicy",
    "CompactionPolicyTriggerTokens",
    "ConnectionClosingEvent",
    "ConnectionClosingReason",
    "CostMetrics",
    "CreateAgentRequest",
    "CreateClientKeyRequest",
    "CreateCredentialRequest",
    "CreateInvocationRequest",
    "CreateNudgeRequest",
    "CreateProviderKeyRequest",
    "CreateSessionRequest",
    "Credential",
    "CredentialIssuance",
    "CredentialList",
    "CredentialProfile",
    "CredentialStatus",
    "CreditAccount",
    "CreditAccountList",
    "CreditAllocation",
    "CreditAllocationList",
    "CreditBlock",
    "CreditPolicy",
    "CurrentIdentity",
    "CurrentIdentityAuthentication",
    "DocumentInputBlock",
    "DocumentInputSource",
    "DocumentReferenceBlock",
    "ErrorCode",
    "ErrorResponse",
    "ForkSessionOptions",
    "ForkSessionRequest",
    "ForkSessionRequestFromMessage",
    "HostToolDeclaration",
    "HostToolResultAcceptance",
    "ImageInputBlock",
    "ImageInputSource",
    "ImageReferenceBlock",
    "InputBlock",
    "Invocation",
    "InvocationChange",
    "InvocationChildCounts",
    "InvocationContextItem",
    "InvocationFailure",
    "InvocationInput",
    "InvocationList",
    "InvocationLog",
    "InvocationLogList",
    "InvocationResult",
    "InvocationStatus",
    "InvocationStopReason",
    "InvocationTimeline",
    "InvocationTimelineStep",
    "InvocationTrigger",
    "InvocationWebhookContext",
    "InvocationWebhookRequest",
    "InvocationWebhookSubject",
    "Limits",
    "MCPListToolsRequest",
    "MCPListToolsResponse",
    "MCPProjectedTool",
    "MCPServer",
    "MCPServerHeaders",
    "MCPTimeouts",
    "MCPToolAnnotations",
    "MCPToolExclusion",
    "MachineConcurrencyLimits",
    "Memory",
    "MemoryConfig",
    "MemoryContextConfig",
    "MemoryContextMode",
    "MemoryKind",
    "MemoryList",
    "MemorySearchCoverage",
    "MemorySearchMode",
    "MemorySearchResult",
    "MessageDeltaEvent",
    "MessageDeltaKind",
    "MessagePhase",
    "MintAppSigningKeyRequest",
    "Model",
    "ModelCallFactStatus",
    "ModelCallKind",
    "ModelCallRecord",
    "ModelControlCapabilities",
    "ModelCost",
    "ModelDescriptor",
    "ModelInput",
    "ModelInputCapabilities",
    "ModelList",
    "ModelMediaCapabilities",
    "ModelMediaKindCapabilities",
    "ModelMetrics",
    "ModelPricing",
    "ModelProvenance",
    "ModelReasoningBudgetCapabilities",
    "ModelReasoningCapabilities",
    "ModelReasoningEffortCapabilities",
    "ModelSamplingCapabilities",
    "ModelToolCapabilities",
    "ModelToolChoiceCapabilities",
    "ModelToolChoiceMode",
    "ModelUsage",
    "Money",
    "Nudge",
    "NudgeAcknowledgement",
    "NudgeList",
    "NudgeStatus",
    "ObservationStatus",
    "Operation",
    "Org",
    "OrgList",
    "ProviderKey",
    "ProviderKeyList",
    "ProviderKeyScope",
    "ProviderKeySelection",
    "ProviderKeySelectionOneOf",
    "ProviderKeySelectionOneOf1",
    "ProviderKeySource",
    "ProviderKeyUsage",
    "ProviderStaticKey",
    "ProviderTool",
    "Reasoning",
    "ReasoningEffort",
    "RedactedBlock",
    "RegisterAppRequest",
    "RegisterOrgRequest",
    "ReminderBlock",
    "ResolvedLimits",
    "ResumeInvocationRequest",
    "RetentionPolicy",
    "RotateCredentialRequest",
    "RotateProviderKeyRequest",
    "Sampling",
    "SeedMessage",
    "SeedMessageContent",
    "ServerToolUseBlock",
    "Session",
    "SessionCompaction",
    "SessionCompactionList",
    "SessionCompactionStatus",
    "SessionContentBlock",
    "SessionContext",
    "SessionForkLineage",
    "SessionList",
    "SessionMessage",
    "SessionMessageList",
    "SessionMessageRole",
    "SessionOptions",
    "SessionStreamEvent",
    "StreamResyncEvent",
    "StreamResyncReason",
    "StructuredOutputProvenance",
    "SubmitHostToolResultsRequest",
    "SubmitHostToolResultsRequestResultsInner",
    "SubmitHostToolResultsResponse",
    "Tenant",
    "TenantCredits",
    "TenantList",
    "TextBlock",
    "TextInputBlock",
    "ToolCall",
    "ToolCallDelivery",
    "ToolCallList",
    "ToolCallMode",
    "ToolCallResultOrigin",
    "ToolCallStatus",
    "ToolCallSummary",
    "ToolCallbackContext",
    "ToolCallbackRequest",
    "ToolCallbackResponse",
    "ToolChoice",
    "ToolDeclaration",
    "ToolMetrics",
    "ToolResultBlock",
    "ToolUseBlock",
    "Trace",
    "TraceList",
    "TraceSpan",
    "TraceSummary",
    "TranscriptSnapshot",
    "TranscriptUpdateEvent",
    "URLCitation",
    "UpdateAgentRequest",
    "UpdateAppRequest",
    "UpdateOrgRequest",
    "UpdateSessionRequest",
    "UpdateSessionRequestMetadataValue",
    "UsageBreakdown",
    "UsageBreakdownItem",
    "UsageInterval",
    "UsageMetrics",
    "UsageRecords",
    "UsageTimeseries",
    "UsageTimeseriesBucket",
    "WebSearchLocation",
    "WebSearchResultLocationCitation",
    "WebSearchTool",
    "WebhookEvent",
    "WebhookTarget",
]

# import apis into sdk package
from nvoken_generated.api.admissions_api import AdmissionsApi as AdmissionsApi
from nvoken_generated.api.agent_definitions_api import AgentDefinitionsApi as AgentDefinitionsApi
from nvoken_generated.api.agents_api import AgentsApi as AgentsApi
from nvoken_generated.api.apps_api import AppsApi as AppsApi
from nvoken_generated.api.credits_api import CreditsApi as CreditsApi
from nvoken_generated.api.identity_api import IdentityApi as IdentityApi
from nvoken_generated.api.invocations_api import InvocationsApi as InvocationsApi
from nvoken_generated.api.mcp_api import MCPApi as MCPApi
from nvoken_generated.api.memories_api import MemoriesApi as MemoriesApi
from nvoken_generated.api.models_api import ModelsApi as ModelsApi
from nvoken_generated.api.operations_api import OperationsApi as OperationsApi
from nvoken_generated.api.orgs_api import OrgsApi as OrgsApi
from nvoken_generated.api.provider_keys_api import ProviderKeysApi as ProviderKeysApi
from nvoken_generated.api.sessions_api import SessionsApi as SessionsApi
from nvoken_generated.api.tenants_api import TenantsApi as TenantsApi
from nvoken_generated.api.usage_api import UsageApi as UsageApi

# import ApiClient
from nvoken_generated.api_response import ApiResponse as ApiResponse
from nvoken_generated.api_client import ApiClient as ApiClient
from nvoken_generated.configuration import Configuration as Configuration
from nvoken_generated.exceptions import OpenApiException as OpenApiException
from nvoken_generated.exceptions import ApiTypeError as ApiTypeError
from nvoken_generated.exceptions import ApiValueError as ApiValueError
from nvoken_generated.exceptions import ApiKeyError as ApiKeyError
from nvoken_generated.exceptions import ApiAttributeError as ApiAttributeError
from nvoken_generated.exceptions import ApiException as ApiException

# import models into sdk package
from nvoken_generated.models.activity_metrics import ActivityMetrics as ActivityMetrics
from nvoken_generated.models.admission_attempt import AdmissionAttempt as AdmissionAttempt
from nvoken_generated.models.admission_attempt_list import AdmissionAttemptList as AdmissionAttemptList
from nvoken_generated.models.admission_outcome import AdmissionOutcome as AdmissionOutcome
from nvoken_generated.models.admission_reason_count import AdmissionReasonCount as AdmissionReasonCount
from nvoken_generated.models.admission_summary import AdmissionSummary as AdmissionSummary
from nvoken_generated.models.admission_tenant_count import AdmissionTenantCount as AdmissionTenantCount
from nvoken_generated.models.agent import Agent as Agent
from nvoken_generated.models.agent_definition import AgentDefinition as AgentDefinition
from nvoken_generated.models.agent_definition_create import AgentDefinitionCreate as AgentDefinitionCreate
from nvoken_generated.models.agent_definition_overrides import AgentDefinitionOverrides as AgentDefinitionOverrides
from nvoken_generated.models.agent_definition_resource import AgentDefinitionResource as AgentDefinitionResource
from nvoken_generated.models.agent_definition_resource_list import AgentDefinitionResourceList as AgentDefinitionResourceList
from nvoken_generated.models.agent_definition_write import AgentDefinitionWrite as AgentDefinitionWrite
from nvoken_generated.models.agent_list import AgentList as AgentList
from nvoken_generated.models.allocate_credits_request import AllocateCreditsRequest as AllocateCreditsRequest
from nvoken_generated.models.allocate_credits_result import AllocateCreditsResult as AllocateCreditsResult
from nvoken_generated.models.anonymous_access import AnonymousAccess as AnonymousAccess
from nvoken_generated.models.anonymous_token_request import AnonymousTokenRequest as AnonymousTokenRequest
from nvoken_generated.models.anonymous_token_response import AnonymousTokenResponse as AnonymousTokenResponse
from nvoken_generated.models.app import App as App
from nvoken_generated.models.app_default_rate_limits import AppDefaultRateLimits as AppDefaultRateLimits
from nvoken_generated.models.app_list import AppList as AppList
from nvoken_generated.models.app_registration import AppRegistration as AppRegistration
from nvoken_generated.models.app_signing_key import AppSigningKey as AppSigningKey
from nvoken_generated.models.app_signing_key_list import AppSigningKeyList as AppSigningKeyList
from nvoken_generated.models.app_signing_key_purpose import AppSigningKeyPurpose as AppSigningKeyPurpose
from nvoken_generated.models.app_signing_key_secret import AppSigningKeySecret as AppSigningKeySecret
from nvoken_generated.models.authentication_method import AuthenticationMethod as AuthenticationMethod
from nvoken_generated.models.browser_access import BrowserAccess as BrowserAccess
from nvoken_generated.models.browser_client_interface import BrowserClientInterface as BrowserClientInterface
from nvoken_generated.models.browser_invocation_webhook import BrowserInvocationWebhook as BrowserInvocationWebhook
from nvoken_generated.models.browser_rate_limits import BrowserRateLimits as BrowserRateLimits
from nvoken_generated.models.builtin_tool_declaration import BuiltinToolDeclaration as BuiltinToolDeclaration
from nvoken_generated.models.callback_delivery_outcome import CallbackDeliveryOutcome as CallbackDeliveryOutcome
from nvoken_generated.models.callback_target import CallbackTarget as CallbackTarget
from nvoken_generated.models.callback_tool_declaration import CallbackToolDeclaration as CallbackToolDeclaration
from nvoken_generated.models.char_location_citation import CharLocationCitation as CharLocationCitation
from nvoken_generated.models.citation import Citation as Citation
from nvoken_generated.models.client_key import ClientKey as ClientKey
from nvoken_generated.models.client_key_list import ClientKeyList as ClientKeyList
from nvoken_generated.models.compaction_policy import CompactionPolicy as CompactionPolicy
from nvoken_generated.models.compaction_policy_trigger_tokens import CompactionPolicyTriggerTokens as CompactionPolicyTriggerTokens
from nvoken_generated.models.connection_closing_event import ConnectionClosingEvent as ConnectionClosingEvent
from nvoken_generated.models.connection_closing_reason import ConnectionClosingReason as ConnectionClosingReason
from nvoken_generated.models.cost_metrics import CostMetrics as CostMetrics
from nvoken_generated.models.create_agent_request import CreateAgentRequest as CreateAgentRequest
from nvoken_generated.models.create_client_key_request import CreateClientKeyRequest as CreateClientKeyRequest
from nvoken_generated.models.create_credential_request import CreateCredentialRequest as CreateCredentialRequest
from nvoken_generated.models.create_invocation_request import CreateInvocationRequest as CreateInvocationRequest
from nvoken_generated.models.create_nudge_request import CreateNudgeRequest as CreateNudgeRequest
from nvoken_generated.models.create_provider_key_request import CreateProviderKeyRequest as CreateProviderKeyRequest
from nvoken_generated.models.create_session_request import CreateSessionRequest as CreateSessionRequest
from nvoken_generated.models.credential import Credential as Credential
from nvoken_generated.models.credential_issuance import CredentialIssuance as CredentialIssuance
from nvoken_generated.models.credential_list import CredentialList as CredentialList
from nvoken_generated.models.credential_profile import CredentialProfile as CredentialProfile
from nvoken_generated.models.credential_status import CredentialStatus as CredentialStatus
from nvoken_generated.models.credit_account import CreditAccount as CreditAccount
from nvoken_generated.models.credit_account_list import CreditAccountList as CreditAccountList
from nvoken_generated.models.credit_allocation import CreditAllocation as CreditAllocation
from nvoken_generated.models.credit_allocation_list import CreditAllocationList as CreditAllocationList
from nvoken_generated.models.credit_block import CreditBlock as CreditBlock
from nvoken_generated.models.credit_policy import CreditPolicy as CreditPolicy
from nvoken_generated.models.current_identity import CurrentIdentity as CurrentIdentity
from nvoken_generated.models.current_identity_authentication import CurrentIdentityAuthentication as CurrentIdentityAuthentication
from nvoken_generated.models.document_input_block import DocumentInputBlock as DocumentInputBlock
from nvoken_generated.models.document_input_source import DocumentInputSource as DocumentInputSource
from nvoken_generated.models.document_reference_block import DocumentReferenceBlock as DocumentReferenceBlock
from nvoken_generated.models.error_code import ErrorCode as ErrorCode
from nvoken_generated.models.error_response import ErrorResponse as ErrorResponse
from nvoken_generated.models.fork_session_options import ForkSessionOptions as ForkSessionOptions
from nvoken_generated.models.fork_session_request import ForkSessionRequest as ForkSessionRequest
from nvoken_generated.models.fork_session_request_from_message import ForkSessionRequestFromMessage as ForkSessionRequestFromMessage
from nvoken_generated.models.host_tool_declaration import HostToolDeclaration as HostToolDeclaration
from nvoken_generated.models.host_tool_result_acceptance import HostToolResultAcceptance as HostToolResultAcceptance
from nvoken_generated.models.image_input_block import ImageInputBlock as ImageInputBlock
from nvoken_generated.models.image_input_source import ImageInputSource as ImageInputSource
from nvoken_generated.models.image_reference_block import ImageReferenceBlock as ImageReferenceBlock
from nvoken_generated.models.input_block import InputBlock as InputBlock
from nvoken_generated.models.invocation import Invocation as Invocation
from nvoken_generated.models.invocation_change import InvocationChange as InvocationChange
from nvoken_generated.models.invocation_child_counts import InvocationChildCounts as InvocationChildCounts
from nvoken_generated.models.invocation_context_item import InvocationContextItem as InvocationContextItem
from nvoken_generated.models.invocation_failure import InvocationFailure as InvocationFailure
from nvoken_generated.models.invocation_input import InvocationInput as InvocationInput
from nvoken_generated.models.invocation_list import InvocationList as InvocationList
from nvoken_generated.models.invocation_log import InvocationLog as InvocationLog
from nvoken_generated.models.invocation_log_list import InvocationLogList as InvocationLogList
from nvoken_generated.models.invocation_result import InvocationResult as InvocationResult
from nvoken_generated.models.invocation_status import InvocationStatus as InvocationStatus
from nvoken_generated.models.invocation_stop_reason import InvocationStopReason as InvocationStopReason
from nvoken_generated.models.invocation_timeline import InvocationTimeline as InvocationTimeline
from nvoken_generated.models.invocation_timeline_step import InvocationTimelineStep as InvocationTimelineStep
from nvoken_generated.models.invocation_trigger import InvocationTrigger as InvocationTrigger
from nvoken_generated.models.invocation_webhook_context import InvocationWebhookContext as InvocationWebhookContext
from nvoken_generated.models.invocation_webhook_request import InvocationWebhookRequest as InvocationWebhookRequest
from nvoken_generated.models.invocation_webhook_subject import InvocationWebhookSubject as InvocationWebhookSubject
from nvoken_generated.models.limits import Limits as Limits
from nvoken_generated.models.mcp_list_tools_request import MCPListToolsRequest as MCPListToolsRequest
from nvoken_generated.models.mcp_list_tools_response import MCPListToolsResponse as MCPListToolsResponse
from nvoken_generated.models.mcp_projected_tool import MCPProjectedTool as MCPProjectedTool
from nvoken_generated.models.mcp_server import MCPServer as MCPServer
from nvoken_generated.models.mcp_server_headers import MCPServerHeaders as MCPServerHeaders
from nvoken_generated.models.mcp_timeouts import MCPTimeouts as MCPTimeouts
from nvoken_generated.models.mcp_tool_annotations import MCPToolAnnotations as MCPToolAnnotations
from nvoken_generated.models.mcp_tool_exclusion import MCPToolExclusion as MCPToolExclusion
from nvoken_generated.models.machine_concurrency_limits import MachineConcurrencyLimits as MachineConcurrencyLimits
from nvoken_generated.models.memory import Memory as Memory
from nvoken_generated.models.memory_config import MemoryConfig as MemoryConfig
from nvoken_generated.models.memory_context_config import MemoryContextConfig as MemoryContextConfig
from nvoken_generated.models.memory_context_mode import MemoryContextMode as MemoryContextMode
from nvoken_generated.models.memory_kind import MemoryKind as MemoryKind
from nvoken_generated.models.memory_list import MemoryList as MemoryList
from nvoken_generated.models.memory_search_coverage import MemorySearchCoverage as MemorySearchCoverage
from nvoken_generated.models.memory_search_mode import MemorySearchMode as MemorySearchMode
from nvoken_generated.models.memory_search_result import MemorySearchResult as MemorySearchResult
from nvoken_generated.models.message_delta_event import MessageDeltaEvent as MessageDeltaEvent
from nvoken_generated.models.message_delta_kind import MessageDeltaKind as MessageDeltaKind
from nvoken_generated.models.message_phase import MessagePhase as MessagePhase
from nvoken_generated.models.mint_app_signing_key_request import MintAppSigningKeyRequest as MintAppSigningKeyRequest
from nvoken_generated.models.model import Model as Model
from nvoken_generated.models.model_call_fact_status import ModelCallFactStatus as ModelCallFactStatus
from nvoken_generated.models.model_call_kind import ModelCallKind as ModelCallKind
from nvoken_generated.models.model_call_record import ModelCallRecord as ModelCallRecord
from nvoken_generated.models.model_control_capabilities import ModelControlCapabilities as ModelControlCapabilities
from nvoken_generated.models.model_cost import ModelCost as ModelCost
from nvoken_generated.models.model_descriptor import ModelDescriptor as ModelDescriptor
from nvoken_generated.models.model_input import ModelInput as ModelInput
from nvoken_generated.models.model_input_capabilities import ModelInputCapabilities as ModelInputCapabilities
from nvoken_generated.models.model_list import ModelList as ModelList
from nvoken_generated.models.model_media_capabilities import ModelMediaCapabilities as ModelMediaCapabilities
from nvoken_generated.models.model_media_kind_capabilities import ModelMediaKindCapabilities as ModelMediaKindCapabilities
from nvoken_generated.models.model_metrics import ModelMetrics as ModelMetrics
from nvoken_generated.models.model_pricing import ModelPricing as ModelPricing
from nvoken_generated.models.model_provenance import ModelProvenance as ModelProvenance
from nvoken_generated.models.model_reasoning_budget_capabilities import ModelReasoningBudgetCapabilities as ModelReasoningBudgetCapabilities
from nvoken_generated.models.model_reasoning_capabilities import ModelReasoningCapabilities as ModelReasoningCapabilities
from nvoken_generated.models.model_reasoning_effort_capabilities import ModelReasoningEffortCapabilities as ModelReasoningEffortCapabilities
from nvoken_generated.models.model_sampling_capabilities import ModelSamplingCapabilities as ModelSamplingCapabilities
from nvoken_generated.models.model_tool_capabilities import ModelToolCapabilities as ModelToolCapabilities
from nvoken_generated.models.model_tool_choice_capabilities import ModelToolChoiceCapabilities as ModelToolChoiceCapabilities
from nvoken_generated.models.model_tool_choice_mode import ModelToolChoiceMode as ModelToolChoiceMode
from nvoken_generated.models.model_usage import ModelUsage as ModelUsage
from nvoken_generated.models.money import Money as Money
from nvoken_generated.models.nudge import Nudge as Nudge
from nvoken_generated.models.nudge_acknowledgement import NudgeAcknowledgement as NudgeAcknowledgement
from nvoken_generated.models.nudge_list import NudgeList as NudgeList
from nvoken_generated.models.nudge_status import NudgeStatus as NudgeStatus
from nvoken_generated.models.observation_status import ObservationStatus as ObservationStatus
from nvoken_generated.models.operation import Operation as Operation
from nvoken_generated.models.org import Org as Org
from nvoken_generated.models.org_list import OrgList as OrgList
from nvoken_generated.models.provider_key import ProviderKey as ProviderKey
from nvoken_generated.models.provider_key_list import ProviderKeyList as ProviderKeyList
from nvoken_generated.models.provider_key_scope import ProviderKeyScope as ProviderKeyScope
from nvoken_generated.models.provider_key_selection import ProviderKeySelection as ProviderKeySelection
from nvoken_generated.models.provider_key_selection_one_of import ProviderKeySelectionOneOf as ProviderKeySelectionOneOf
from nvoken_generated.models.provider_key_selection_one_of1 import ProviderKeySelectionOneOf1 as ProviderKeySelectionOneOf1
from nvoken_generated.models.provider_key_source import ProviderKeySource as ProviderKeySource
from nvoken_generated.models.provider_key_usage import ProviderKeyUsage as ProviderKeyUsage
from nvoken_generated.models.provider_static_key import ProviderStaticKey as ProviderStaticKey
from nvoken_generated.models.provider_tool import ProviderTool as ProviderTool
from nvoken_generated.models.reasoning import Reasoning as Reasoning
from nvoken_generated.models.reasoning_effort import ReasoningEffort as ReasoningEffort
from nvoken_generated.models.redacted_block import RedactedBlock as RedactedBlock
from nvoken_generated.models.register_app_request import RegisterAppRequest as RegisterAppRequest
from nvoken_generated.models.register_org_request import RegisterOrgRequest as RegisterOrgRequest
from nvoken_generated.models.reminder_block import ReminderBlock as ReminderBlock
from nvoken_generated.models.resolved_limits import ResolvedLimits as ResolvedLimits
from nvoken_generated.models.resume_invocation_request import ResumeInvocationRequest as ResumeInvocationRequest
from nvoken_generated.models.retention_policy import RetentionPolicy as RetentionPolicy
from nvoken_generated.models.rotate_credential_request import RotateCredentialRequest as RotateCredentialRequest
from nvoken_generated.models.rotate_provider_key_request import RotateProviderKeyRequest as RotateProviderKeyRequest
from nvoken_generated.models.sampling import Sampling as Sampling
from nvoken_generated.models.seed_message import SeedMessage as SeedMessage
from nvoken_generated.models.seed_message_content import SeedMessageContent as SeedMessageContent
from nvoken_generated.models.server_tool_use_block import ServerToolUseBlock as ServerToolUseBlock
from nvoken_generated.models.session import Session as Session
from nvoken_generated.models.session_compaction import SessionCompaction as SessionCompaction
from nvoken_generated.models.session_compaction_list import SessionCompactionList as SessionCompactionList
from nvoken_generated.models.session_compaction_status import SessionCompactionStatus as SessionCompactionStatus
from nvoken_generated.models.session_content_block import SessionContentBlock as SessionContentBlock
from nvoken_generated.models.session_context import SessionContext as SessionContext
from nvoken_generated.models.session_fork_lineage import SessionForkLineage as SessionForkLineage
from nvoken_generated.models.session_list import SessionList as SessionList
from nvoken_generated.models.session_message import SessionMessage as SessionMessage
from nvoken_generated.models.session_message_list import SessionMessageList as SessionMessageList
from nvoken_generated.models.session_message_role import SessionMessageRole as SessionMessageRole
from nvoken_generated.models.session_options import SessionOptions as SessionOptions
from nvoken_generated.models.session_stream_event import SessionStreamEvent as SessionStreamEvent
from nvoken_generated.models.stream_resync_event import StreamResyncEvent as StreamResyncEvent
from nvoken_generated.models.stream_resync_reason import StreamResyncReason as StreamResyncReason
from nvoken_generated.models.structured_output_provenance import StructuredOutputProvenance as StructuredOutputProvenance
from nvoken_generated.models.submit_host_tool_results_request import SubmitHostToolResultsRequest as SubmitHostToolResultsRequest
from nvoken_generated.models.submit_host_tool_results_request_results_inner import SubmitHostToolResultsRequestResultsInner as SubmitHostToolResultsRequestResultsInner
from nvoken_generated.models.submit_host_tool_results_response import SubmitHostToolResultsResponse as SubmitHostToolResultsResponse
from nvoken_generated.models.tenant import Tenant as Tenant
from nvoken_generated.models.tenant_credits import TenantCredits as TenantCredits
from nvoken_generated.models.tenant_list import TenantList as TenantList
from nvoken_generated.models.text_block import TextBlock as TextBlock
from nvoken_generated.models.text_input_block import TextInputBlock as TextInputBlock
from nvoken_generated.models.tool_call import ToolCall as ToolCall
from nvoken_generated.models.tool_call_delivery import ToolCallDelivery as ToolCallDelivery
from nvoken_generated.models.tool_call_list import ToolCallList as ToolCallList
from nvoken_generated.models.tool_call_mode import ToolCallMode as ToolCallMode
from nvoken_generated.models.tool_call_result_origin import ToolCallResultOrigin as ToolCallResultOrigin
from nvoken_generated.models.tool_call_status import ToolCallStatus as ToolCallStatus
from nvoken_generated.models.tool_call_summary import ToolCallSummary as ToolCallSummary
from nvoken_generated.models.tool_callback_context import ToolCallbackContext as ToolCallbackContext
from nvoken_generated.models.tool_callback_request import ToolCallbackRequest as ToolCallbackRequest
from nvoken_generated.models.tool_callback_response import ToolCallbackResponse as ToolCallbackResponse
from nvoken_generated.models.tool_choice import ToolChoice as ToolChoice
from nvoken_generated.models.tool_declaration import ToolDeclaration as ToolDeclaration
from nvoken_generated.models.tool_metrics import ToolMetrics as ToolMetrics
from nvoken_generated.models.tool_result_block import ToolResultBlock as ToolResultBlock
from nvoken_generated.models.tool_use_block import ToolUseBlock as ToolUseBlock
from nvoken_generated.models.trace import Trace as Trace
from nvoken_generated.models.trace_list import TraceList as TraceList
from nvoken_generated.models.trace_span import TraceSpan as TraceSpan
from nvoken_generated.models.trace_summary import TraceSummary as TraceSummary
from nvoken_generated.models.transcript_snapshot import TranscriptSnapshot as TranscriptSnapshot
from nvoken_generated.models.transcript_update_event import TranscriptUpdateEvent as TranscriptUpdateEvent
from nvoken_generated.models.url_citation import URLCitation as URLCitation
from nvoken_generated.models.update_agent_request import UpdateAgentRequest as UpdateAgentRequest
from nvoken_generated.models.update_app_request import UpdateAppRequest as UpdateAppRequest
from nvoken_generated.models.update_org_request import UpdateOrgRequest as UpdateOrgRequest
from nvoken_generated.models.update_session_request import UpdateSessionRequest as UpdateSessionRequest
from nvoken_generated.models.update_session_request_metadata_value import UpdateSessionRequestMetadataValue as UpdateSessionRequestMetadataValue
from nvoken_generated.models.usage_breakdown import UsageBreakdown as UsageBreakdown
from nvoken_generated.models.usage_breakdown_item import UsageBreakdownItem as UsageBreakdownItem
from nvoken_generated.models.usage_interval import UsageInterval as UsageInterval
from nvoken_generated.models.usage_metrics import UsageMetrics as UsageMetrics
from nvoken_generated.models.usage_records import UsageRecords as UsageRecords
from nvoken_generated.models.usage_timeseries import UsageTimeseries as UsageTimeseries
from nvoken_generated.models.usage_timeseries_bucket import UsageTimeseriesBucket as UsageTimeseriesBucket
from nvoken_generated.models.web_search_location import WebSearchLocation as WebSearchLocation
from nvoken_generated.models.web_search_result_location_citation import WebSearchResultLocationCitation as WebSearchResultLocationCitation
from nvoken_generated.models.web_search_tool import WebSearchTool as WebSearchTool
from nvoken_generated.models.webhook_event import WebhookEvent as WebhookEvent
from nvoken_generated.models.webhook_target import WebhookTarget as WebhookTarget
