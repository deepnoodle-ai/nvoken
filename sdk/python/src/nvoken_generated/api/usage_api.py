"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - Full App keys can read and mutate every resource owned by their App. - Read-only App keys can read the same non-secret App and runtime data but   cannot mutate anything, including their own key lineage. - Installation-admin keys manage Orgs, Apps, and App keys but resolve no   App data. Short-lived console presentations provide fixed Org or admin   control-plane and reporting access.  Tenant and user assertion headers narrow individual requests. Durable API keys carry no tenant, Session, profile, or operation constraints.  ## Acting for one tenant or one end user  An app-wide credential can reach every tenant in its App, which means an id that arrives from the wrong place — a stale link, a mixed-up webhook, a tampered form field — is an id it can act on. The usual answer is for the host to re-read the resource and compare its `tenant_key` and `user_key` before every call. Say it once instead:  ``` X-Nvoken-Tenant-Key: acme X-Nvoken-User-Key: user-7c1f ```  Both headers are accepted on every request and narrow that one request to the scope they name. Anything outside it is reported as `not_found`, so a Session or Invocation belonging to another tenant or another end user cannot be read, cancelled, interrupted, forked, answered, or erased — and you learn nothing about whether the id exists. Writes take the same scope: an omitted `tenant_key` or `user_key` in the body inherits it, and one naming somebody else is refused.  Two rules keep them honest. They may only narrow, so a credential already bound to one tenant refuses a header naming another with `forbidden` rather than silently returning nothing. And a record with no `user_key` at all is outside every `X-Nvoken-User-Key` assertion rather than inside all of them, because a Session nobody claimed is not one you can claim by asserting.  Each header takes 1 to 255 characters and may appear once. A blank, oversized, or repeated header is `invalid_request` rather than ignored: an assertion nobody notices dropping is worse than no assertion at all.  A browser token already carries its tenant and its end user, so it neither needs these headers nor is allowed to send them.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that reusable resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for, so you can filter on it later — and on an Agent whose Definition sets `memory.scope: user`, it also selects which memories that Agent can recall. It is fixed by the request that opens a Session and a later turn cannot change it. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource's name. `session_id` on an Invocation is the reusable conversation it belongs to, or null for a standalone turn. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken's identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  `GET /v1/invocations/{invocation_id}/stream` follows exactly one turn and closes after its terminal change is delivered. For standalone work its cursor is scoped to that Invocation and exposes no carrier ID. For a conversation-bound turn it uses the Session cursor scope, so the same cursor can resume the aggregate Session stream.  `GET /v1/sessions/{session_id}/stream` is the durable conversation subscription: it carries every turn in the Session and stays open while the conversation is idle. A standalone Invocation cursor cannot be used on this route because standalone work has no public Session.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


import warnings
from pydantic import validate_call, Field, StrictFloat, StrictStr, StrictInt
from typing import Any, Dict, List, Optional, Tuple, Union
from typing_extensions import Annotated

from datetime import datetime
from pydantic import Field, StrictStr, field_validator
from typing import Optional
from typing_extensions import Annotated
from nvoken_generated.models.authentication_method import AuthenticationMethod
from nvoken_generated.models.model_call_kind import ModelCallKind
from nvoken_generated.models.provider_key_source import ProviderKeySource
from nvoken_generated.models.tool_call_mode import ToolCallMode
from nvoken_generated.models.usage_breakdown import UsageBreakdown
from nvoken_generated.models.usage_interval import UsageInterval
from nvoken_generated.models.usage_records import UsageRecords
from nvoken_generated.models.usage_timeseries import UsageTimeseries

from nvoken_generated.api_client import ApiClient, RequestSerialized
from nvoken_generated.api_response import ApiResponse
from nvoken_generated.rest import RESTResponseType


class UsageApi:
    """NOTE: This class is auto generated by OpenAPI Generator
    Ref: https://openapi-generator.tech

    Do not edit the class manually.
    """

    def __init__(self, api_client=None) -> None:
        if api_client is None:
            api_client = ApiClient.get_default()
        self.api_client = api_client


    @validate_call
    async def get_usage_breakdown(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        group_by: StrictStr,
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        sort: Optional[StrictStr] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Optional[Annotated[int, Field(le=100, strict=True, ge=1)]] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> UsageBreakdown:
        """Rank usage by one dimension


        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param group_by: (required)
        :type group_by: str
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param sort:
        :type sort: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit:
        :type limit: int
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._get_usage_breakdown_serialize(
            start_at=start_at,
            end_at=end_at,
            group_by=group_by,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            sort=sort,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageBreakdown",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        await response_data.read()
        return self.api_client.response_deserialize(
            response_data=response_data,
            response_types_map=_response_types_map,
        ).data


    @validate_call
    async def get_usage_breakdown_with_http_info(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        group_by: StrictStr,
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        sort: Optional[StrictStr] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Optional[Annotated[int, Field(le=100, strict=True, ge=1)]] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> ApiResponse[UsageBreakdown]:
        """Rank usage by one dimension


        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param group_by: (required)
        :type group_by: str
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param sort:
        :type sort: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit:
        :type limit: int
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._get_usage_breakdown_serialize(
            start_at=start_at,
            end_at=end_at,
            group_by=group_by,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            sort=sort,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageBreakdown",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        await response_data.read()
        return self.api_client.response_deserialize(
            response_data=response_data,
            response_types_map=_response_types_map,
        )


    @validate_call
    async def get_usage_breakdown_without_preload_content(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        group_by: StrictStr,
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        sort: Optional[StrictStr] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Optional[Annotated[int, Field(le=100, strict=True, ge=1)]] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> RESTResponseType:
        """Rank usage by one dimension


        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param group_by: (required)
        :type group_by: str
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param sort:
        :type sort: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit:
        :type limit: int
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._get_usage_breakdown_serialize(
            start_at=start_at,
            end_at=end_at,
            group_by=group_by,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            sort=sort,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageBreakdown",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _get_usage_breakdown_serialize(
        self,
        start_at,
        end_at,
        group_by,
        app_id,
        tenant_key,
        user_key,
        agent_id,
        provider,
        model,
        provider_key_source,
        provider_key_id,
        credential_family_id,
        authentication_method,
        call_kind,
        tool_name,
        tool_mode,
        sort,
        cursor,
        limit,
        _request_auth,
        _content_type,
        _headers,
        _host_index,
    ) -> RequestSerialized:

        _host = None

        _collection_formats: Dict[str, str] = {
        }

        _path_params: Dict[str, str] = {}
        _query_params: List[Tuple[str, str]] = []
        _header_params: Dict[str, Optional[str]] = _headers or {}
        _form_params: List[Tuple[str, str]] = []
        _files: Dict[
            str, Union[str, bytes, List[str], List[bytes], List[Tuple[str, bytes]]]
        ] = {}
        _body_params: Optional[bytes] = None

        # process the path parameters
        # process the query parameters
        if start_at is not None:
            if isinstance(start_at, datetime):
                _query_params.append(
                    (
                        'start_at',
                        start_at.strftime(
                            self.api_client.configuration.datetime_format
                        )
                    )
                )
            else:
                _query_params.append(('start_at', start_at))

        if end_at is not None:
            if isinstance(end_at, datetime):
                _query_params.append(
                    (
                        'end_at',
                        end_at.strftime(
                            self.api_client.configuration.datetime_format
                        )
                    )
                )
            else:
                _query_params.append(('end_at', end_at))

        if app_id is not None:

            _query_params.append(('app_id', app_id))

        if tenant_key is not None:

            _query_params.append(('tenant_key', tenant_key))

        if user_key is not None:

            _query_params.append(('user_key', user_key))

        if agent_id is not None:

            _query_params.append(('agent_id', agent_id))

        if provider is not None:

            _query_params.append(('provider', provider))

        if model is not None:

            _query_params.append(('model', model))

        if provider_key_source is not None:

            _query_params.append(('provider_key_source', provider_key_source.value))

        if provider_key_id is not None:

            _query_params.append(('provider_key_id', provider_key_id))

        if credential_family_id is not None:

            _query_params.append(('credential_family_id', credential_family_id))

        if authentication_method is not None:

            _query_params.append(('authentication_method', authentication_method.value))

        if call_kind is not None:

            _query_params.append(('call_kind', call_kind.value))

        if tool_name is not None:

            _query_params.append(('tool_name', tool_name))

        if tool_mode is not None:

            _query_params.append(('tool_mode', tool_mode.value))

        if group_by is not None:

            _query_params.append(('group_by', group_by))

        if sort is not None:

            _query_params.append(('sort', sort))

        if cursor is not None:

            _query_params.append(('cursor', cursor))

        if limit is not None:

            _query_params.append(('limit', limit))

        # process the header parameters
        # process the form parameters
        # process the body parameter


        # set the HTTP header `Accept`
        if 'Accept' not in _header_params:
            _header_params['Accept'] = self.api_client.select_header_accept(
                [
                    'application/json'
                ]
            )


        # authentication setting
        _auth_settings: List[str] = [
            'bearerAuth'
        ]

        return self.api_client.param_serialize(
            method='GET',
            resource_path='/v1/usage/breakdown',
            path_params=_path_params,
            query_params=_query_params,
            header_params=_header_params,
            body=_body_params,
            post_params=_form_params,
            files=_files,
            auth_settings=_auth_settings,
            collection_formats=_collection_formats,
            _host=_host,
            _request_auth=_request_auth
        )




    @validate_call
    async def get_usage_timeseries(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        interval: UsageInterval,
        timezone: Annotated[Optional[StrictStr], Field(description="IANA timezone used for bucket boundaries.")] = None,
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        group_by: Optional[StrictStr] = None,
        top: Optional[Annotated[int, Field(le=10, strict=True, ge=1)]] = None,
        keys: Annotated[Optional[StrictStr], Field(description="Comma-separated explicit series keys, maximum ten; mutually exclusive with top.")] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> UsageTimeseries:
        """Read usage totals and sparse time buckets

        Returns activity, model, tool, and model-cost metrics from retained, content-free facts. The half-open window totals use exact distinct counts and are not sums of bucket distincts. Grouping is bounded to ten selected series plus `other`. Session deletion does not rewrite history. An App credential is forced to its App, an Org console presentation to Apps currently owned by its Org, and only an installation-scoped admin issuer token can span every App. Durable installation keys cannot read reporting data.

        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param interval: (required)
        :type interval: UsageInterval
        :param timezone: IANA timezone used for bucket boundaries.
        :type timezone: str
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param group_by:
        :type group_by: str
        :param top:
        :type top: int
        :param keys: Comma-separated explicit series keys, maximum ten; mutually exclusive with top.
        :type keys: str
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._get_usage_timeseries_serialize(
            start_at=start_at,
            end_at=end_at,
            interval=interval,
            timezone=timezone,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            group_by=group_by,
            top=top,
            keys=keys,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageTimeseries",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        await response_data.read()
        return self.api_client.response_deserialize(
            response_data=response_data,
            response_types_map=_response_types_map,
        ).data


    @validate_call
    async def get_usage_timeseries_with_http_info(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        interval: UsageInterval,
        timezone: Annotated[Optional[StrictStr], Field(description="IANA timezone used for bucket boundaries.")] = None,
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        group_by: Optional[StrictStr] = None,
        top: Optional[Annotated[int, Field(le=10, strict=True, ge=1)]] = None,
        keys: Annotated[Optional[StrictStr], Field(description="Comma-separated explicit series keys, maximum ten; mutually exclusive with top.")] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> ApiResponse[UsageTimeseries]:
        """Read usage totals and sparse time buckets

        Returns activity, model, tool, and model-cost metrics from retained, content-free facts. The half-open window totals use exact distinct counts and are not sums of bucket distincts. Grouping is bounded to ten selected series plus `other`. Session deletion does not rewrite history. An App credential is forced to its App, an Org console presentation to Apps currently owned by its Org, and only an installation-scoped admin issuer token can span every App. Durable installation keys cannot read reporting data.

        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param interval: (required)
        :type interval: UsageInterval
        :param timezone: IANA timezone used for bucket boundaries.
        :type timezone: str
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param group_by:
        :type group_by: str
        :param top:
        :type top: int
        :param keys: Comma-separated explicit series keys, maximum ten; mutually exclusive with top.
        :type keys: str
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._get_usage_timeseries_serialize(
            start_at=start_at,
            end_at=end_at,
            interval=interval,
            timezone=timezone,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            group_by=group_by,
            top=top,
            keys=keys,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageTimeseries",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        await response_data.read()
        return self.api_client.response_deserialize(
            response_data=response_data,
            response_types_map=_response_types_map,
        )


    @validate_call
    async def get_usage_timeseries_without_preload_content(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        interval: UsageInterval,
        timezone: Annotated[Optional[StrictStr], Field(description="IANA timezone used for bucket boundaries.")] = None,
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        group_by: Optional[StrictStr] = None,
        top: Optional[Annotated[int, Field(le=10, strict=True, ge=1)]] = None,
        keys: Annotated[Optional[StrictStr], Field(description="Comma-separated explicit series keys, maximum ten; mutually exclusive with top.")] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> RESTResponseType:
        """Read usage totals and sparse time buckets

        Returns activity, model, tool, and model-cost metrics from retained, content-free facts. The half-open window totals use exact distinct counts and are not sums of bucket distincts. Grouping is bounded to ten selected series plus `other`. Session deletion does not rewrite history. An App credential is forced to its App, an Org console presentation to Apps currently owned by its Org, and only an installation-scoped admin issuer token can span every App. Durable installation keys cannot read reporting data.

        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param interval: (required)
        :type interval: UsageInterval
        :param timezone: IANA timezone used for bucket boundaries.
        :type timezone: str
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param group_by:
        :type group_by: str
        :param top:
        :type top: int
        :param keys: Comma-separated explicit series keys, maximum ten; mutually exclusive with top.
        :type keys: str
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._get_usage_timeseries_serialize(
            start_at=start_at,
            end_at=end_at,
            interval=interval,
            timezone=timezone,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            group_by=group_by,
            top=top,
            keys=keys,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageTimeseries",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _get_usage_timeseries_serialize(
        self,
        start_at,
        end_at,
        interval,
        timezone,
        app_id,
        tenant_key,
        user_key,
        agent_id,
        provider,
        model,
        provider_key_source,
        provider_key_id,
        credential_family_id,
        authentication_method,
        call_kind,
        tool_name,
        tool_mode,
        group_by,
        top,
        keys,
        _request_auth,
        _content_type,
        _headers,
        _host_index,
    ) -> RequestSerialized:

        _host = None

        _collection_formats: Dict[str, str] = {
        }

        _path_params: Dict[str, str] = {}
        _query_params: List[Tuple[str, str]] = []
        _header_params: Dict[str, Optional[str]] = _headers or {}
        _form_params: List[Tuple[str, str]] = []
        _files: Dict[
            str, Union[str, bytes, List[str], List[bytes], List[Tuple[str, bytes]]]
        ] = {}
        _body_params: Optional[bytes] = None

        # process the path parameters
        # process the query parameters
        if start_at is not None:
            if isinstance(start_at, datetime):
                _query_params.append(
                    (
                        'start_at',
                        start_at.strftime(
                            self.api_client.configuration.datetime_format
                        )
                    )
                )
            else:
                _query_params.append(('start_at', start_at))

        if end_at is not None:
            if isinstance(end_at, datetime):
                _query_params.append(
                    (
                        'end_at',
                        end_at.strftime(
                            self.api_client.configuration.datetime_format
                        )
                    )
                )
            else:
                _query_params.append(('end_at', end_at))

        if interval is not None:

            _query_params.append(('interval', interval.value))

        if timezone is not None:

            _query_params.append(('timezone', timezone))

        if app_id is not None:

            _query_params.append(('app_id', app_id))

        if tenant_key is not None:

            _query_params.append(('tenant_key', tenant_key))

        if user_key is not None:

            _query_params.append(('user_key', user_key))

        if agent_id is not None:

            _query_params.append(('agent_id', agent_id))

        if provider is not None:

            _query_params.append(('provider', provider))

        if model is not None:

            _query_params.append(('model', model))

        if provider_key_source is not None:

            _query_params.append(('provider_key_source', provider_key_source.value))

        if provider_key_id is not None:

            _query_params.append(('provider_key_id', provider_key_id))

        if credential_family_id is not None:

            _query_params.append(('credential_family_id', credential_family_id))

        if authentication_method is not None:

            _query_params.append(('authentication_method', authentication_method.value))

        if call_kind is not None:

            _query_params.append(('call_kind', call_kind.value))

        if tool_name is not None:

            _query_params.append(('tool_name', tool_name))

        if tool_mode is not None:

            _query_params.append(('tool_mode', tool_mode.value))

        if group_by is not None:

            _query_params.append(('group_by', group_by))

        if top is not None:

            _query_params.append(('top', top))

        if keys is not None:

            _query_params.append(('keys', keys))

        # process the header parameters
        # process the form parameters
        # process the body parameter


        # set the HTTP header `Accept`
        if 'Accept' not in _header_params:
            _header_params['Accept'] = self.api_client.select_header_accept(
                [
                    'application/json'
                ]
            )


        # authentication setting
        _auth_settings: List[str] = [
            'bearerAuth'
        ]

        return self.api_client.param_serialize(
            method='GET',
            resource_path='/v1/usage/timeseries',
            path_params=_path_params,
            query_params=_query_params,
            header_params=_header_params,
            body=_body_params,
            post_params=_form_params,
            files=_files,
            auth_settings=_auth_settings,
            collection_formats=_collection_formats,
            _host=_host,
            _request_auth=_request_auth
        )




    @validate_call
    async def list_usage_records(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Optional[Annotated[int, Field(le=1000, strict=True, ge=1)]] = None,
        format: Optional[StrictStr] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> UsageRecords:
        """Export itemized model-call facts

        Stable ascending `(started_at, id)` order; JSON and CSV contain the same logical columns and never content.

        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit:
        :type limit: int
        :param format:
        :type format: str
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._list_usage_records_serialize(
            start_at=start_at,
            end_at=end_at,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            cursor=cursor,
            limit=limit,
            format=format,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageRecords",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        await response_data.read()
        return self.api_client.response_deserialize(
            response_data=response_data,
            response_types_map=_response_types_map,
        ).data


    @validate_call
    async def list_usage_records_with_http_info(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Optional[Annotated[int, Field(le=1000, strict=True, ge=1)]] = None,
        format: Optional[StrictStr] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> ApiResponse[UsageRecords]:
        """Export itemized model-call facts

        Stable ascending `(started_at, id)` order; JSON and CSV contain the same logical columns and never content.

        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit:
        :type limit: int
        :param format:
        :type format: str
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._list_usage_records_serialize(
            start_at=start_at,
            end_at=end_at,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            cursor=cursor,
            limit=limit,
            format=format,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageRecords",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        await response_data.read()
        return self.api_client.response_deserialize(
            response_data=response_data,
            response_types_map=_response_types_map,
        )


    @validate_call
    async def list_usage_records_without_preload_content(
        self,
        start_at: Annotated[datetime, Field(description="Inclusive RFC 3339 start of the half-open reporting window.")],
        end_at: Annotated[datetime, Field(description="Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at.")],
        app_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Exact App within the caller's App, Org, or installation reporting scope.")] = None,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        provider: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        model: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        provider_key_source: Optional[ProviderKeySource] = None,
        provider_key_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        credential_family_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        authentication_method: Optional[AuthenticationMethod] = None,
        call_kind: Optional[ModelCallKind] = None,
        tool_name: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        tool_mode: Optional[ToolCallMode] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Optional[Annotated[int, Field(le=1000, strict=True, ge=1)]] = None,
        format: Optional[StrictStr] = None,
        _request_timeout: Union[
            None,
            Annotated[StrictFloat, Field(gt=0)],
            Tuple[
                Annotated[StrictFloat, Field(gt=0)],
                Annotated[StrictFloat, Field(gt=0)]
            ]
        ] = None,
        _request_auth: Optional[Dict[StrictStr, Any]] = None,
        _content_type: Optional[StrictStr] = None,
        _headers: Optional[Dict[StrictStr, Any]] = None,
        _host_index: Annotated[StrictInt, Field(ge=0, le=0)] = 0,
    ) -> RESTResponseType:
        """Export itemized model-call facts

        Stable ascending `(started_at, id)` order; JSON and CSV contain the same logical columns and never content.

        :param start_at: Inclusive RFC 3339 start of the half-open reporting window. (required)
        :type start_at: datetime
        :param end_at: Exclusive RFC 3339 end of the half-open reporting window; at most 400 days after start_at. (required)
        :type end_at: datetime
        :param app_id: Exact App within the caller's App, Org, or installation reporting scope.
        :type app_id: str
        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id:
        :type agent_id: str
        :param provider:
        :type provider: str
        :param model:
        :type model: str
        :param provider_key_source:
        :type provider_key_source: ProviderKeySource
        :param provider_key_id:
        :type provider_key_id: str
        :param credential_family_id:
        :type credential_family_id: str
        :param authentication_method:
        :type authentication_method: AuthenticationMethod
        :param call_kind:
        :type call_kind: ModelCallKind
        :param tool_name:
        :type tool_name: str
        :param tool_mode:
        :type tool_mode: ToolCallMode
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit:
        :type limit: int
        :param format:
        :type format: str
        :param _request_timeout: timeout setting for this request. If one
                                 number provided, it will be total request
                                 timeout. It can also be a pair (tuple) of
                                 (connection, read) timeouts.
        :type _request_timeout: int, tuple(int, int), optional
        :param _request_auth: set to override the auth_settings for an a single
                              request; this effectively ignores the
                              authentication in the spec for a single request.
        :type _request_auth: dict, optional
        :param _content_type: force content-type for the request.
        :type _content_type: str, Optional
        :param _headers: set to override the headers for a single
                         request; this effectively ignores the headers
                         in the spec for a single request.
        :type _headers: dict, optional
        :param _host_index: set to override the host_index for a single
                            request; this effectively ignores the host_index
                            in the spec for a single request.
        :type _host_index: int, optional
        :return: Returns the result object.
        """ # noqa: E501

        _param = self._list_usage_records_serialize(
            start_at=start_at,
            end_at=end_at,
            app_id=app_id,
            tenant_key=tenant_key,
            user_key=user_key,
            agent_id=agent_id,
            provider=provider,
            model=model,
            provider_key_source=provider_key_source,
            provider_key_id=provider_key_id,
            credential_family_id=credential_family_id,
            authentication_method=authentication_method,
            call_kind=call_kind,
            tool_name=tool_name,
            tool_mode=tool_mode,
            cursor=cursor,
            limit=limit,
            format=format,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "UsageRecords",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_usage_records_serialize(
        self,
        start_at,
        end_at,
        app_id,
        tenant_key,
        user_key,
        agent_id,
        provider,
        model,
        provider_key_source,
        provider_key_id,
        credential_family_id,
        authentication_method,
        call_kind,
        tool_name,
        tool_mode,
        cursor,
        limit,
        format,
        _request_auth,
        _content_type,
        _headers,
        _host_index,
    ) -> RequestSerialized:

        _host = None

        _collection_formats: Dict[str, str] = {
        }

        _path_params: Dict[str, str] = {}
        _query_params: List[Tuple[str, str]] = []
        _header_params: Dict[str, Optional[str]] = _headers or {}
        _form_params: List[Tuple[str, str]] = []
        _files: Dict[
            str, Union[str, bytes, List[str], List[bytes], List[Tuple[str, bytes]]]
        ] = {}
        _body_params: Optional[bytes] = None

        # process the path parameters
        # process the query parameters
        if start_at is not None:
            if isinstance(start_at, datetime):
                _query_params.append(
                    (
                        'start_at',
                        start_at.strftime(
                            self.api_client.configuration.datetime_format
                        )
                    )
                )
            else:
                _query_params.append(('start_at', start_at))

        if end_at is not None:
            if isinstance(end_at, datetime):
                _query_params.append(
                    (
                        'end_at',
                        end_at.strftime(
                            self.api_client.configuration.datetime_format
                        )
                    )
                )
            else:
                _query_params.append(('end_at', end_at))

        if app_id is not None:

            _query_params.append(('app_id', app_id))

        if tenant_key is not None:

            _query_params.append(('tenant_key', tenant_key))

        if user_key is not None:

            _query_params.append(('user_key', user_key))

        if agent_id is not None:

            _query_params.append(('agent_id', agent_id))

        if provider is not None:

            _query_params.append(('provider', provider))

        if model is not None:

            _query_params.append(('model', model))

        if provider_key_source is not None:

            _query_params.append(('provider_key_source', provider_key_source.value))

        if provider_key_id is not None:

            _query_params.append(('provider_key_id', provider_key_id))

        if credential_family_id is not None:

            _query_params.append(('credential_family_id', credential_family_id))

        if authentication_method is not None:

            _query_params.append(('authentication_method', authentication_method.value))

        if call_kind is not None:

            _query_params.append(('call_kind', call_kind.value))

        if tool_name is not None:

            _query_params.append(('tool_name', tool_name))

        if tool_mode is not None:

            _query_params.append(('tool_mode', tool_mode.value))

        if cursor is not None:

            _query_params.append(('cursor', cursor))

        if limit is not None:

            _query_params.append(('limit', limit))

        if format is not None:

            _query_params.append(('format', format))

        # process the header parameters
        # process the form parameters
        # process the body parameter


        # set the HTTP header `Accept`
        if 'Accept' not in _header_params:
            _header_params['Accept'] = self.api_client.select_header_accept(
                [
                    'application/json',
                    'text/csv'
                ]
            )


        # authentication setting
        _auth_settings: List[str] = [
            'bearerAuth'
        ]

        return self.api_client.param_serialize(
            method='GET',
            resource_path='/v1/usage/records',
            path_params=_path_params,
            query_params=_query_params,
            header_params=_header_params,
            body=_body_params,
            post_params=_form_params,
            files=_files,
            auth_settings=_auth_settings,
            collection_formats=_collection_formats,
            _host=_host,
            _request_auth=_request_auth
        )
