"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Acting for one tenant or one end user  An app-wide credential can reach every tenant in its App, which means an id that arrives from the wrong place — a stale link, a mixed-up webhook, a tampered form field — is an id it can act on. The usual answer is for the host to re-read the resource and compare its `tenant_key` and `user_key` before every call. Say it once instead:  ``` X-Nvoken-Tenant-Key: acme X-Nvoken-User-Key: user-7c1f ```  Both headers are accepted on every request and narrow that one request to the scope they name. Anything outside it is reported as `not_found`, so a Session or Invocation belonging to another tenant or another end user cannot be read, cancelled, interrupted, forked, answered, or erased — and you learn nothing about whether the id exists. Writes take the same scope: an omitted `tenant_key` or `user_key` in the body inherits it, and one naming somebody else is refused.  Two rules keep them honest. They may only narrow, so a credential already bound to one tenant refuses a header naming another with `forbidden` rather than silently returning nothing. And a record with no `user_key` at all is outside every `X-Nvoken-User-Key` assertion rather than inside all of them, because a Session nobody claimed is not one you can claim by asserting.  Each header takes 1 to 255 characters and may appear once. A blank, oversized, or repeated header is `invalid_request` rather than ignored: an assertion nobody notices dropping is worse than no assertion at all.  A browser token already carries its tenant and its end user, so it neither needs these headers nor is allowed to send them.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for, so you can filter on it later — and on an Agent whose Definition sets `memory.scope: user`, it also selects which memories that Agent can recall. It is fixed by the request that opens a Session and a later turn cannot change it. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource's name, so `session_id` on an Invocation is the Session it belongs to. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken's identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


import warnings
from pydantic import validate_call, Field, StrictFloat, StrictStr, StrictInt
from typing import Any, Dict, List, Optional, Tuple, Union
from typing_extensions import Annotated

from pydantic import Field, StrictBool, StrictStr, field_validator
from typing import Optional
from typing_extensions import Annotated
from nvoken_generated.models.create_session_request import CreateSessionRequest
from nvoken_generated.models.fork_session_request import ForkSessionRequest
from nvoken_generated.models.session import Session
from nvoken_generated.models.session_compaction_list import SessionCompactionList
from nvoken_generated.models.session_list import SessionList
from nvoken_generated.models.session_message_list import SessionMessageList
from nvoken_generated.models.session_stream_event import SessionStreamEvent
from nvoken_generated.models.transcript_snapshot import TranscriptSnapshot
from nvoken_generated.models.update_session_request import UpdateSessionRequest

from nvoken_generated.api_client import ApiClient, RequestSerialized
from nvoken_generated.api_response import ApiResponse
from nvoken_generated.rest import RESTResponseType


class SessionsApi:
    """NOTE: This class is auto generated by OpenAPI Generator
    Ref: https://openapi-generator.tech

    Do not edit the class manually.
    """

    def __init__(self, api_client=None) -> None:
        if api_client is None:
            api_client = ApiClient.get_default()
        self.api_client = api_client


    @validate_call
    async def create_session(
        self,
        create_session_request: CreateSessionRequest,
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
    ) -> Session:
        """Create or seed a Session without creating an Invocation

        Creates an empty Session, optionally seeded with history you already have. Use this when you want a conversation to exist before the first turn runs — to show it in a UI, or to import messages from elsewhere.  Every field is optional. Leave out both `agent_id` and `agent_key` and the Session starts unbound: `agent_id` stays null until the first turn binds it permanently. A supplied Agent must already exist in the selected tenant.

        :param create_session_request: (required)
        :type create_session_request: CreateSessionRequest
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

        _param = self._create_session_serialize(
            create_session_request=create_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '201': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def create_session_with_http_info(
        self,
        create_session_request: CreateSessionRequest,
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
    ) -> ApiResponse[Session]:
        """Create or seed a Session without creating an Invocation

        Creates an empty Session, optionally seeded with history you already have. Use this when you want a conversation to exist before the first turn runs — to show it in a UI, or to import messages from elsewhere.  Every field is optional. Leave out both `agent_id` and `agent_key` and the Session starts unbound: `agent_id` stays null until the first turn binds it permanently. A supplied Agent must already exist in the selected tenant.

        :param create_session_request: (required)
        :type create_session_request: CreateSessionRequest
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

        _param = self._create_session_serialize(
            create_session_request=create_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '201': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def create_session_without_preload_content(
        self,
        create_session_request: CreateSessionRequest,
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
        """Create or seed a Session without creating an Invocation

        Creates an empty Session, optionally seeded with history you already have. Use this when you want a conversation to exist before the first turn runs — to show it in a UI, or to import messages from elsewhere.  Every field is optional. Leave out both `agent_id` and `agent_key` and the Session starts unbound: `agent_id` stays null until the first turn binds it permanently. A supplied Agent must already exist in the selected tenant.

        :param create_session_request: (required)
        :type create_session_request: CreateSessionRequest
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

        _param = self._create_session_serialize(
            create_session_request=create_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '201': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _create_session_serialize(
        self,
        create_session_request,
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
        # process the header parameters
        # process the form parameters
        # process the body parameter
        if create_session_request is not None:
            _body_params = create_session_request


        # set the HTTP header `Accept`
        if 'Accept' not in _header_params:
            _header_params['Accept'] = self.api_client.select_header_accept(
                [
                    'application/json'
                ]
            )

        # set the HTTP header `Content-Type`
        if _content_type:
            _header_params['Content-Type'] = _content_type
        else:
            _default_content_type = (
                self.api_client.select_header_content_type(
                    [
                        'application/json'
                    ]
                )
            )
            if _default_content_type is not None:
                _header_params['Content-Type'] = _default_content_type

        # authentication setting
        _auth_settings: List[str] = [
            'bearerAuth'
        ]

        return self.api_client.param_serialize(
            method='POST',
            resource_path='/v1/sessions',
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
    async def delete_session(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        force: Annotated[Optional[StrictBool], Field(description="Erase even when the Session holds a nonterminal Invocation, discarding that turn's settlement. Without it, live work is refused with `session_invocation_active`. ")] = None,
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
    ) -> None:
        """Erase a Session and everything under it

        Removes the Session, its Invocations, transcript messages, checkpoints, tool calls, provider artifacts, compactions, provider-key and MCP bindings, and undelivered webhooks. The erasure is immediate and irreversible; a subsequent read is `not_found`.  A Session holding a nonterminal Invocation is refused with `session_invocation_active` unless you pass `force=true`. Erasure skips settlement: a turn still running is stopped, but no cancellation is recorded — there is nothing left to record it against — and no `invocation.ended` webhook fires for it. So if you bill or reconcile on settlement, cancel the turn and wait for its final state first, then delete. `force=true` is for erasing on an end user's behalf, where removing the transcript now outranks keeping a settled record of the turn.  An unknown `session_id`, or one outside your scope, returns `not_found`. So if you lose the response and retry, you can safely treat `404` as \"already deleted\". Deleting requires the Runtime or Operator profile; a Viewer credential cannot erase a transcript. A managed anonymous token may erase only its own fully constrained Session. Host-minted browser tokens remain unable to call this route. A still-live anonymous visitor token can subsequently start one empty replacement Session; conversation erasure is not credential revocation.  **Deleting Sessions is not the same as deleting a user's account.** nvoken has no record that an account was deleted, so to honour a deletion request you must first stop starting new turns for that tenant, then page through `GET /v1/sessions` and delete until the list comes back empty. Otherwise a request arriving mid-sweep creates a new Session behind you.  Two consequences to plan for. Content-free Invocation, model-call, and tool-call facts remain for usage reporting, with the Invocation marked erased; prompts, responses, and tool payloads do not. The deleted turns' idempotency keys become reusable, since deduplication only holds while the original turn still exists.

        :param session_id: (required)
        :type session_id: str
        :param force: Erase even when the Session holds a nonterminal Invocation, discarding that turn's settlement. Without it, live work is refused with `session_invocation_active`.
        :type force: bool
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

        _param = self._delete_session_serialize(
            session_id=session_id,
            force=force,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '204': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def delete_session_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        force: Annotated[Optional[StrictBool], Field(description="Erase even when the Session holds a nonterminal Invocation, discarding that turn's settlement. Without it, live work is refused with `session_invocation_active`. ")] = None,
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
    ) -> ApiResponse[None]:
        """Erase a Session and everything under it

        Removes the Session, its Invocations, transcript messages, checkpoints, tool calls, provider artifacts, compactions, provider-key and MCP bindings, and undelivered webhooks. The erasure is immediate and irreversible; a subsequent read is `not_found`.  A Session holding a nonterminal Invocation is refused with `session_invocation_active` unless you pass `force=true`. Erasure skips settlement: a turn still running is stopped, but no cancellation is recorded — there is nothing left to record it against — and no `invocation.ended` webhook fires for it. So if you bill or reconcile on settlement, cancel the turn and wait for its final state first, then delete. `force=true` is for erasing on an end user's behalf, where removing the transcript now outranks keeping a settled record of the turn.  An unknown `session_id`, or one outside your scope, returns `not_found`. So if you lose the response and retry, you can safely treat `404` as \"already deleted\". Deleting requires the Runtime or Operator profile; a Viewer credential cannot erase a transcript. A managed anonymous token may erase only its own fully constrained Session. Host-minted browser tokens remain unable to call this route. A still-live anonymous visitor token can subsequently start one empty replacement Session; conversation erasure is not credential revocation.  **Deleting Sessions is not the same as deleting a user's account.** nvoken has no record that an account was deleted, so to honour a deletion request you must first stop starting new turns for that tenant, then page through `GET /v1/sessions` and delete until the list comes back empty. Otherwise a request arriving mid-sweep creates a new Session behind you.  Two consequences to plan for. Content-free Invocation, model-call, and tool-call facts remain for usage reporting, with the Invocation marked erased; prompts, responses, and tool payloads do not. The deleted turns' idempotency keys become reusable, since deduplication only holds while the original turn still exists.

        :param session_id: (required)
        :type session_id: str
        :param force: Erase even when the Session holds a nonterminal Invocation, discarding that turn's settlement. Without it, live work is refused with `session_invocation_active`.
        :type force: bool
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

        _param = self._delete_session_serialize(
            session_id=session_id,
            force=force,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '204': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def delete_session_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        force: Annotated[Optional[StrictBool], Field(description="Erase even when the Session holds a nonterminal Invocation, discarding that turn's settlement. Without it, live work is refused with `session_invocation_active`. ")] = None,
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
        """Erase a Session and everything under it

        Removes the Session, its Invocations, transcript messages, checkpoints, tool calls, provider artifacts, compactions, provider-key and MCP bindings, and undelivered webhooks. The erasure is immediate and irreversible; a subsequent read is `not_found`.  A Session holding a nonterminal Invocation is refused with `session_invocation_active` unless you pass `force=true`. Erasure skips settlement: a turn still running is stopped, but no cancellation is recorded — there is nothing left to record it against — and no `invocation.ended` webhook fires for it. So if you bill or reconcile on settlement, cancel the turn and wait for its final state first, then delete. `force=true` is for erasing on an end user's behalf, where removing the transcript now outranks keeping a settled record of the turn.  An unknown `session_id`, or one outside your scope, returns `not_found`. So if you lose the response and retry, you can safely treat `404` as \"already deleted\". Deleting requires the Runtime or Operator profile; a Viewer credential cannot erase a transcript. A managed anonymous token may erase only its own fully constrained Session. Host-minted browser tokens remain unable to call this route. A still-live anonymous visitor token can subsequently start one empty replacement Session; conversation erasure is not credential revocation.  **Deleting Sessions is not the same as deleting a user's account.** nvoken has no record that an account was deleted, so to honour a deletion request you must first stop starting new turns for that tenant, then page through `GET /v1/sessions` and delete until the list comes back empty. Otherwise a request arriving mid-sweep creates a new Session behind you.  Two consequences to plan for. Content-free Invocation, model-call, and tool-call facts remain for usage reporting, with the Invocation marked erased; prompts, responses, and tool payloads do not. The deleted turns' idempotency keys become reusable, since deduplication only holds while the original turn still exists.

        :param session_id: (required)
        :type session_id: str
        :param force: Erase even when the Session holds a nonterminal Invocation, discarding that turn's settlement. Without it, live work is refused with `session_invocation_active`.
        :type force: bool
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

        _param = self._delete_session_serialize(
            session_id=session_id,
            force=force,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '204': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _delete_session_serialize(
        self,
        session_id,
        force,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
        if force is not None:

            _query_params.append(('force', force))

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
            method='DELETE',
            resource_path='/v1/sessions/{session_id}',
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
    async def fork_session(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        fork_session_request: ForkSessionRequest,
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
    ) -> Session:
        """Copy a Session prefix into a new Session

        Creates a new Session in the source Session's tenant and Agent scope, copying every canonical message through `from_message` inclusively. The source is untouched. The child stores durable Session and message lineage, but copied messages no longer belong to the source Invocations. Their `origin`, per-turn `user_key`, and resolved message phase are preserved.  Usage and compaction summaries are not copied. Child usage starts at zero and the child starts uncompacted. Retention, the revision pin, and the authorization context come only from `session_options` on this request; no Session option is inherited, because a child's policy is the forker's choice. `user_key` is the exception and is inherited from the source, because who the conversation is about is not a policy. A `session_key` has the same tenant/Agent-scoped upsert behavior as Session creation.

        :param session_id: (required)
        :type session_id: str
        :param fork_session_request: (required)
        :type fork_session_request: ForkSessionRequest
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

        _param = self._fork_session_serialize(
            session_id=session_id,
            fork_session_request=fork_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '201': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def fork_session_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        fork_session_request: ForkSessionRequest,
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
    ) -> ApiResponse[Session]:
        """Copy a Session prefix into a new Session

        Creates a new Session in the source Session's tenant and Agent scope, copying every canonical message through `from_message` inclusively. The source is untouched. The child stores durable Session and message lineage, but copied messages no longer belong to the source Invocations. Their `origin`, per-turn `user_key`, and resolved message phase are preserved.  Usage and compaction summaries are not copied. Child usage starts at zero and the child starts uncompacted. Retention, the revision pin, and the authorization context come only from `session_options` on this request; no Session option is inherited, because a child's policy is the forker's choice. `user_key` is the exception and is inherited from the source, because who the conversation is about is not a policy. A `session_key` has the same tenant/Agent-scoped upsert behavior as Session creation.

        :param session_id: (required)
        :type session_id: str
        :param fork_session_request: (required)
        :type fork_session_request: ForkSessionRequest
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

        _param = self._fork_session_serialize(
            session_id=session_id,
            fork_session_request=fork_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '201': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def fork_session_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        fork_session_request: ForkSessionRequest,
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
        """Copy a Session prefix into a new Session

        Creates a new Session in the source Session's tenant and Agent scope, copying every canonical message through `from_message` inclusively. The source is untouched. The child stores durable Session and message lineage, but copied messages no longer belong to the source Invocations. Their `origin`, per-turn `user_key`, and resolved message phase are preserved.  Usage and compaction summaries are not copied. Child usage starts at zero and the child starts uncompacted. Retention, the revision pin, and the authorization context come only from `session_options` on this request; no Session option is inherited, because a child's policy is the forker's choice. `user_key` is the exception and is inherited from the source, because who the conversation is about is not a policy. A `session_key` has the same tenant/Agent-scoped upsert behavior as Session creation.

        :param session_id: (required)
        :type session_id: str
        :param fork_session_request: (required)
        :type fork_session_request: ForkSessionRequest
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

        _param = self._fork_session_serialize(
            session_id=session_id,
            fork_session_request=fork_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '201': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _fork_session_serialize(
        self,
        session_id,
        fork_session_request,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
        # process the header parameters
        # process the form parameters
        # process the body parameter
        if fork_session_request is not None:
            _body_params = fork_session_request


        # set the HTTP header `Accept`
        if 'Accept' not in _header_params:
            _header_params['Accept'] = self.api_client.select_header_accept(
                [
                    'application/json'
                ]
            )

        # set the HTTP header `Content-Type`
        if _content_type:
            _header_params['Content-Type'] = _content_type
        else:
            _default_content_type = (
                self.api_client.select_header_content_type(
                    [
                        'application/json'
                    ]
                )
            )
            if _default_content_type is not None:
                _header_params['Content-Type'] = _default_content_type

        # authentication setting
        _auth_settings: List[str] = [
            'bearerAuth'
        ]

        return self.api_client.param_serialize(
            method='POST',
            resource_path='/v1/sessions/{session_id}/fork',
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
    async def get_session(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> Session:
        """Read authoritative Session identity and current state

        An App credential without a tenant constraint may resolve a Session in any tenant partition in that App. A tenant-constrained credential resolves only Sessions in its partition. Missing, incompatible, and undisclosable resources use `not_found`; a credential denied the read operation itself receives `forbidden`.

        :param session_id: (required)
        :type session_id: str
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

        _param = self._get_session_serialize(
            session_id=session_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def get_session_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[Session]:
        """Read authoritative Session identity and current state

        An App credential without a tenant constraint may resolve a Session in any tenant partition in that App. A tenant-constrained credential resolves only Sessions in its partition. Missing, incompatible, and undisclosable resources use `not_found`; a credential denied the read operation itself receives `forbidden`.

        :param session_id: (required)
        :type session_id: str
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

        _param = self._get_session_serialize(
            session_id=session_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def get_session_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Read authoritative Session identity and current state

        An App credential without a tenant constraint may resolve a Session in any tenant partition in that App. A tenant-constrained credential resolves only Sessions in its partition. Missing, incompatible, and undisclosable resources use `not_found`; a credential denied the read operation itself receives `forbidden`.

        :param session_id: (required)
        :type session_id: str
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

        _param = self._get_session_serialize(
            session_id=session_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _get_session_serialize(
        self,
        session_id,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
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
            resource_path='/v1/sessions/{session_id}',
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
    async def get_session_transcript(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        page_token: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque fixed-cut continuation from `next_page_token`.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
    ) -> TranscriptSnapshot:
        """Drain a fixed-cut incremental transcript snapshot

        Returns the Session's stored messages plus a running log of turn state changes.  To catch up rather than re-read everything, pass a `cursor` you received earlier as `cursor` and you get only what is new since then. Within one read, keep passing `page_token` until `has_more` is false — all pages come from the same consistent snapshot, so the transcript cannot shift under you mid-read.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param page_token: Opaque fixed-cut continuation from `next_page_token`.
        :type page_token: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._get_session_transcript_serialize(
            session_id=session_id,
            cursor=cursor,
            page_token=page_token,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "TranscriptSnapshot",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def get_session_transcript_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        page_token: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque fixed-cut continuation from `next_page_token`.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
    ) -> ApiResponse[TranscriptSnapshot]:
        """Drain a fixed-cut incremental transcript snapshot

        Returns the Session's stored messages plus a running log of turn state changes.  To catch up rather than re-read everything, pass a `cursor` you received earlier as `cursor` and you get only what is new since then. Within one read, keep passing `page_token` until `has_more` is false — all pages come from the same consistent snapshot, so the transcript cannot shift under you mid-read.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param page_token: Opaque fixed-cut continuation from `next_page_token`.
        :type page_token: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._get_session_transcript_serialize(
            session_id=session_id,
            cursor=cursor,
            page_token=page_token,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "TranscriptSnapshot",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def get_session_transcript_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        page_token: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque fixed-cut continuation from `next_page_token`.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
        """Drain a fixed-cut incremental transcript snapshot

        Returns the Session's stored messages plus a running log of turn state changes.  To catch up rather than re-read everything, pass a `cursor` you received earlier as `cursor` and you get only what is new since then. Within one read, keep passing `page_token` until `has_more` is false — all pages come from the same consistent snapshot, so the transcript cannot shift under you mid-read.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param page_token: Opaque fixed-cut continuation from `next_page_token`.
        :type page_token: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._get_session_transcript_serialize(
            session_id=session_id,
            cursor=cursor,
            page_token=page_token,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "TranscriptSnapshot",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _get_session_transcript_serialize(
        self,
        session_id,
        cursor,
        page_token,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
        if cursor is not None:

            _query_params.append(('cursor', cursor))

        if page_token is not None:

            _query_params.append(('page_token', page_token))

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
            resource_path='/v1/sessions/{session_id}/transcript',
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
    async def list_session_compactions(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
    ) -> SessionCompactionList:
        """Page through immutable Session compaction records

        Lists every attempt nvoken made to summarize this Session's history, newest first. Use it to understand why the model's context looks the way it does.  An `applied` record includes the summary that took effect and what the summarizing call cost. A `fell_through` record tells you why the attempt was not usable, and includes usage when a model call happened before it failed.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._list_session_compactions_serialize(
            session_id=session_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionCompactionList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def list_session_compactions_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
    ) -> ApiResponse[SessionCompactionList]:
        """Page through immutable Session compaction records

        Lists every attempt nvoken made to summarize this Session's history, newest first. Use it to understand why the model's context looks the way it does.  An `applied` record includes the summary that took effect and what the summarizing call cost. A `fell_through` record tells you why the attempt was not usable, and includes usage when a model call happened before it failed.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._list_session_compactions_serialize(
            session_id=session_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionCompactionList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def list_session_compactions_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
        """Page through immutable Session compaction records

        Lists every attempt nvoken made to summarize this Session's history, newest first. Use it to understand why the model's context looks the way it does.  An `applied` record includes the summary that took effect and what the summarizing call cost. A `fell_through` record tells you why the attempt was not usable, and includes usage when a model call happened before it failed.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._list_session_compactions_serialize(
            session_id=session_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionCompactionList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_session_compactions_serialize(
        self,
        session_id,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
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
            resource_path='/v1/sessions/{session_id}/compactions',
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
    async def list_session_messages(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
        order: Annotated[Optional[StrictStr], Field(description="Sequence order for this page. `asc` (the default) reads oldest first; `desc` reads newest first.  A cursor belongs to the direction that issued it and is refused by the other, because the position it encodes means opposite things in each. Page one direction to its end rather than turning around mid-walk. ")] = None,
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
    ) -> SessionMessageList:
        """Page through the canonical Session transcript

        Returns persisted SessionMessage rows in sequence order, ascending by default. The opaque cursor is bound to the authenticated caller, the Session, and the direction it was issued for. This history endpoint contains no lifecycle or live-preview copies.  Use `order=desc` to read the newest messages first. A conversation's interesting end is its recent end, and reaching it ascending costs a walk through every older message: the tail of a three thousand message Session is one page descending and fifteen round trips ascending.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
        :type limit: int
        :param order: Sequence order for this page. `asc` (the default) reads oldest first; `desc` reads newest first.  A cursor belongs to the direction that issued it and is refused by the other, because the position it encodes means opposite things in each. Page one direction to its end rather than turning around mid-walk.
        :type order: str
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

        _param = self._list_session_messages_serialize(
            session_id=session_id,
            cursor=cursor,
            limit=limit,
            order=order,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionMessageList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def list_session_messages_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
        order: Annotated[Optional[StrictStr], Field(description="Sequence order for this page. `asc` (the default) reads oldest first; `desc` reads newest first.  A cursor belongs to the direction that issued it and is refused by the other, because the position it encodes means opposite things in each. Page one direction to its end rather than turning around mid-walk. ")] = None,
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
    ) -> ApiResponse[SessionMessageList]:
        """Page through the canonical Session transcript

        Returns persisted SessionMessage rows in sequence order, ascending by default. The opaque cursor is bound to the authenticated caller, the Session, and the direction it was issued for. This history endpoint contains no lifecycle or live-preview copies.  Use `order=desc` to read the newest messages first. A conversation's interesting end is its recent end, and reaching it ascending costs a walk through every older message: the tail of a three thousand message Session is one page descending and fifteen round trips ascending.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
        :type limit: int
        :param order: Sequence order for this page. `asc` (the default) reads oldest first; `desc` reads newest first.  A cursor belongs to the direction that issued it and is refused by the other, because the position it encodes means opposite things in each. Page one direction to its end rather than turning around mid-walk.
        :type order: str
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

        _param = self._list_session_messages_serialize(
            session_id=session_id,
            cursor=cursor,
            limit=limit,
            order=order,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionMessageList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def list_session_messages_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
        order: Annotated[Optional[StrictStr], Field(description="Sequence order for this page. `asc` (the default) reads oldest first; `desc` reads newest first.  A cursor belongs to the direction that issued it and is refused by the other, because the position it encodes means opposite things in each. Page one direction to its end rather than turning around mid-walk. ")] = None,
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
        """Page through the canonical Session transcript

        Returns persisted SessionMessage rows in sequence order, ascending by default. The opaque cursor is bound to the authenticated caller, the Session, and the direction it was issued for. This history endpoint contains no lifecycle or live-preview copies.  Use `order=desc` to read the newest messages first. A conversation's interesting end is its recent end, and reaching it ascending costs a walk through every older message: the tail of a three thousand message Session is one page descending and fifteen round trips ascending.

        :param session_id: (required)
        :type session_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
        :type limit: int
        :param order: Sequence order for this page. `asc` (the default) reads oldest first; `desc` reads newest first.  A cursor belongs to the direction that issued it and is refused by the other, because the position it encodes means opposite things in each. Page one direction to its end rather than turning around mid-walk.
        :type order: str
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

        _param = self._list_session_messages_serialize(
            session_id=session_id,
            cursor=cursor,
            limit=limit,
            order=order,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionMessageList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_session_messages_serialize(
        self,
        session_id,
        cursor,
        limit,
        order,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
        if cursor is not None:

            _query_params.append(('cursor', cursor))

        if limit is not None:

            _query_params.append(('limit', limit))

        if order is not None:

            _query_params.append(('order', order))

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
            resource_path='/v1/sessions/{session_id}/messages',
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
    async def list_sessions(
        self,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        default_tenant: Annotated[Optional[StrictBool], Field(description="Select only the default tenant partition. Mutually exclusive with tenant_key.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Mutually exclusive with agent_key.")] = None,
        agent_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id. ")] = None,
        session_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
    ) -> SessionList:
        """List authoritative Sessions

        Lists Sessions, newest first, each with the state of its currently running turn if it has one. Filters combine with AND. Tenant filtering and cursors work the same as on the Invocation list. `agent_id` and `agent_key` are mutually exclusive.

        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param default_tenant: Select only the default tenant partition. Mutually exclusive with tenant_key.
        :type default_tenant: bool
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id: Mutually exclusive with agent_key.
        :type agent_id: str
        :param agent_key: Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id.
        :type agent_key: str
        :param session_key:
        :type session_key: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._list_sessions_serialize(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            agent_id=agent_id,
            agent_key=agent_key,
            session_key=session_key,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def list_sessions_with_http_info(
        self,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        default_tenant: Annotated[Optional[StrictBool], Field(description="Select only the default tenant partition. Mutually exclusive with tenant_key.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Mutually exclusive with agent_key.")] = None,
        agent_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id. ")] = None,
        session_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
    ) -> ApiResponse[SessionList]:
        """List authoritative Sessions

        Lists Sessions, newest first, each with the state of its currently running turn if it has one. Filters combine with AND. Tenant filtering and cursors work the same as on the Invocation list. `agent_id` and `agent_key` are mutually exclusive.

        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param default_tenant: Select only the default tenant partition. Mutually exclusive with tenant_key.
        :type default_tenant: bool
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id: Mutually exclusive with agent_key.
        :type agent_id: str
        :param agent_key: Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id.
        :type agent_key: str
        :param session_key:
        :type session_key: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._list_sessions_serialize(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            agent_id=agent_id,
            agent_key=agent_key,
            session_key=session_key,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def list_sessions_without_preload_content(
        self,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        default_tenant: Annotated[Optional[StrictBool], Field(description="Select only the default tenant partition. Mutually exclusive with tenant_key.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        agent_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Mutually exclusive with agent_key.")] = None,
        agent_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id. ")] = None,
        session_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
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
        """List authoritative Sessions

        Lists Sessions, newest first, each with the state of its currently running turn if it has one. Filters combine with AND. Tenant filtering and cursors work the same as on the Invocation list. `agent_id` and `agent_key` are mutually exclusive.

        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param default_tenant: Select only the default tenant partition. Mutually exclusive with tenant_key.
        :type default_tenant: bool
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param agent_id: Mutually exclusive with agent_key.
        :type agent_id: str
        :param agent_key: Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id.
        :type agent_key: str
        :param session_key:
        :type session_key: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
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

        _param = self._list_sessions_serialize(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            agent_id=agent_id,
            agent_key=agent_key,
            session_key=session_key,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_sessions_serialize(
        self,
        tenant_key,
        default_tenant,
        user_key,
        agent_id,
        agent_key,
        session_key,
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
        if tenant_key is not None:

            _query_params.append(('tenant_key', tenant_key))

        if default_tenant is not None:

            _query_params.append(('default_tenant', default_tenant))

        if user_key is not None:

            _query_params.append(('user_key', user_key))

        if agent_id is not None:

            _query_params.append(('agent_id', agent_id))

        if agent_key is not None:

            _query_params.append(('agent_key', agent_key))

        if session_key is not None:

            _query_params.append(('session_key', session_key))

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
            resource_path='/v1/sessions',
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
    async def stream_session(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        invocation_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Narrow every frame to one turn, and close once it settles.")] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        deltas: Annotated[Optional[StrictBool], Field(description="Include id-less preview frames. Defaults to true.")] = None,
        last_event_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque `cursor` from the last durable update frame; ignored when the `cursor` parameter is supplied.")] = None,
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
    ) -> SessionStreamEvent:
        """Follow a Session over Server-Sent Events

        The one stream. It carries the Session's messages and the lifecycle changes of every turn in it, live, and can be resumed after a dropped connection. It covers the same records as the JSON transcript endpoint.  Every non-empty `transcript.update` frame carries `id: <cursor>`. That opaque ID is your resume position and the only value you need to store — reconnect with it and you continue exactly where you left off. `message.delta`, `stream.resync`, and `connection.closing` never carry an `id`, because they are live previews and control frames rather than saved records.  Previews can be lost. If you receive `stream.resync`, discard the preview text you have accumulated and wait for the saved messages to arrive. Set `deltas=false` to skip previews entirely; nothing about replay, resumption, or how the stream ends changes.  ## Following one turn  Pass `invocation_id` and every frame is narrowed to that turn: messages it produced, its lifecycle changes, its previews. The connection closes once that turn's terminal change has been delivered. Cursors are Session-scoped either way, so a position taken from a filtered read resumes an unfiltered one and the other way round.  Without `invocation_id` this is a subscription. It stays open while the Session is idle and a turn started later by anyone appears on it, so there is nothing to poll.  ## Knowing a turn is over  A turn is over when an `invocation_changes` entry for it carries a terminal status. That is the signal, and there is no other. It is saved, so it replays at any cursor. Read `GET /v1/invocations/{invocation_id}` for the composed result.  `connection.closing` is about this connection. Reason `rotate` means the server is cycling the connection, so reconnect now with your last `cursor`. Reason `idle` means it is reclaiming an idle connection, so reconnect when you next need to read; nothing is lost while you are away. Reason `slow_consumer` means this connection could not keep up. A connection that just drops carries no meaning: reconnect and resume. Disconnecting never cancels a running turn.  ## Mechanics  The `cursor` query parameter wins over the `Last-Event-ID` header. Because this endpoint uses bearer authentication, you need an SSE client that can set the `Authorization` header — the browser's built-in `EventSource` cannot. The server suggests a 1000 ms reconnect delay.  This stream is strictly forward: a message past your cursor is never sent again. A message's `phase` is worked out when it is read, so this stream is not the place to learn which message was the answer. Derive that instead from facts you already hold: a turn has a final answer only once it settled `completed` with stop reason `end_turn`, and that answer is the turn's last assistant message.  Browser and machine callers receive the same frame types, including `thinking` previews. A browser payload carries fewer fields on the same schema.

        :param session_id: (required)
        :type session_id: str
        :param invocation_id: Narrow every frame to one turn, and close once it settles.
        :type invocation_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param deltas: Include id-less preview frames. Defaults to true.
        :type deltas: bool
        :param last_event_id: Opaque `cursor` from the last durable update frame; ignored when the `cursor` parameter is supplied.
        :type last_event_id: str
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

        _param = self._stream_session_serialize(
            session_id=session_id,
            invocation_id=invocation_id,
            cursor=cursor,
            deltas=deltas,
            last_event_id=last_event_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionStreamEvent",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def stream_session_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        invocation_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Narrow every frame to one turn, and close once it settles.")] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        deltas: Annotated[Optional[StrictBool], Field(description="Include id-less preview frames. Defaults to true.")] = None,
        last_event_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque `cursor` from the last durable update frame; ignored when the `cursor` parameter is supplied.")] = None,
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
    ) -> ApiResponse[SessionStreamEvent]:
        """Follow a Session over Server-Sent Events

        The one stream. It carries the Session's messages and the lifecycle changes of every turn in it, live, and can be resumed after a dropped connection. It covers the same records as the JSON transcript endpoint.  Every non-empty `transcript.update` frame carries `id: <cursor>`. That opaque ID is your resume position and the only value you need to store — reconnect with it and you continue exactly where you left off. `message.delta`, `stream.resync`, and `connection.closing` never carry an `id`, because they are live previews and control frames rather than saved records.  Previews can be lost. If you receive `stream.resync`, discard the preview text you have accumulated and wait for the saved messages to arrive. Set `deltas=false` to skip previews entirely; nothing about replay, resumption, or how the stream ends changes.  ## Following one turn  Pass `invocation_id` and every frame is narrowed to that turn: messages it produced, its lifecycle changes, its previews. The connection closes once that turn's terminal change has been delivered. Cursors are Session-scoped either way, so a position taken from a filtered read resumes an unfiltered one and the other way round.  Without `invocation_id` this is a subscription. It stays open while the Session is idle and a turn started later by anyone appears on it, so there is nothing to poll.  ## Knowing a turn is over  A turn is over when an `invocation_changes` entry for it carries a terminal status. That is the signal, and there is no other. It is saved, so it replays at any cursor. Read `GET /v1/invocations/{invocation_id}` for the composed result.  `connection.closing` is about this connection. Reason `rotate` means the server is cycling the connection, so reconnect now with your last `cursor`. Reason `idle` means it is reclaiming an idle connection, so reconnect when you next need to read; nothing is lost while you are away. Reason `slow_consumer` means this connection could not keep up. A connection that just drops carries no meaning: reconnect and resume. Disconnecting never cancels a running turn.  ## Mechanics  The `cursor` query parameter wins over the `Last-Event-ID` header. Because this endpoint uses bearer authentication, you need an SSE client that can set the `Authorization` header — the browser's built-in `EventSource` cannot. The server suggests a 1000 ms reconnect delay.  This stream is strictly forward: a message past your cursor is never sent again. A message's `phase` is worked out when it is read, so this stream is not the place to learn which message was the answer. Derive that instead from facts you already hold: a turn has a final answer only once it settled `completed` with stop reason `end_turn`, and that answer is the turn's last assistant message.  Browser and machine callers receive the same frame types, including `thinking` previews. A browser payload carries fewer fields on the same schema.

        :param session_id: (required)
        :type session_id: str
        :param invocation_id: Narrow every frame to one turn, and close once it settles.
        :type invocation_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param deltas: Include id-less preview frames. Defaults to true.
        :type deltas: bool
        :param last_event_id: Opaque `cursor` from the last durable update frame; ignored when the `cursor` parameter is supplied.
        :type last_event_id: str
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

        _param = self._stream_session_serialize(
            session_id=session_id,
            invocation_id=invocation_id,
            cursor=cursor,
            deltas=deltas,
            last_event_id=last_event_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionStreamEvent",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def stream_session_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        invocation_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Narrow every frame to one turn, and close once it settles.")] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        deltas: Annotated[Optional[StrictBool], Field(description="Include id-less preview frames. Defaults to true.")] = None,
        last_event_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque `cursor` from the last durable update frame; ignored when the `cursor` parameter is supplied.")] = None,
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
        """Follow a Session over Server-Sent Events

        The one stream. It carries the Session's messages and the lifecycle changes of every turn in it, live, and can be resumed after a dropped connection. It covers the same records as the JSON transcript endpoint.  Every non-empty `transcript.update` frame carries `id: <cursor>`. That opaque ID is your resume position and the only value you need to store — reconnect with it and you continue exactly where you left off. `message.delta`, `stream.resync`, and `connection.closing` never carry an `id`, because they are live previews and control frames rather than saved records.  Previews can be lost. If you receive `stream.resync`, discard the preview text you have accumulated and wait for the saved messages to arrive. Set `deltas=false` to skip previews entirely; nothing about replay, resumption, or how the stream ends changes.  ## Following one turn  Pass `invocation_id` and every frame is narrowed to that turn: messages it produced, its lifecycle changes, its previews. The connection closes once that turn's terminal change has been delivered. Cursors are Session-scoped either way, so a position taken from a filtered read resumes an unfiltered one and the other way round.  Without `invocation_id` this is a subscription. It stays open while the Session is idle and a turn started later by anyone appears on it, so there is nothing to poll.  ## Knowing a turn is over  A turn is over when an `invocation_changes` entry for it carries a terminal status. That is the signal, and there is no other. It is saved, so it replays at any cursor. Read `GET /v1/invocations/{invocation_id}` for the composed result.  `connection.closing` is about this connection. Reason `rotate` means the server is cycling the connection, so reconnect now with your last `cursor`. Reason `idle` means it is reclaiming an idle connection, so reconnect when you next need to read; nothing is lost while you are away. Reason `slow_consumer` means this connection could not keep up. A connection that just drops carries no meaning: reconnect and resume. Disconnecting never cancels a running turn.  ## Mechanics  The `cursor` query parameter wins over the `Last-Event-ID` header. Because this endpoint uses bearer authentication, you need an SSE client that can set the `Authorization` header — the browser's built-in `EventSource` cannot. The server suggests a 1000 ms reconnect delay.  This stream is strictly forward: a message past your cursor is never sent again. A message's `phase` is worked out when it is read, so this stream is not the place to learn which message was the answer. Derive that instead from facts you already hold: a turn has a final answer only once it settled `completed` with stop reason `end_turn`, and that answer is the turn's last assistant message.  Browser and machine callers receive the same frame types, including `thinking` previews. A browser payload carries fewer fields on the same schema.

        :param session_id: (required)
        :type session_id: str
        :param invocation_id: Narrow every frame to one turn, and close once it settles.
        :type invocation_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param deltas: Include id-less preview frames. Defaults to true.
        :type deltas: bool
        :param last_event_id: Opaque `cursor` from the last durable update frame; ignored when the `cursor` parameter is supplied.
        :type last_event_id: str
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

        _param = self._stream_session_serialize(
            session_id=session_id,
            invocation_id=invocation_id,
            cursor=cursor,
            deltas=deltas,
            last_event_id=last_event_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "SessionStreamEvent",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _stream_session_serialize(
        self,
        session_id,
        invocation_id,
        cursor,
        deltas,
        last_event_id,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
        if invocation_id is not None:

            _query_params.append(('invocation_id', invocation_id))

        if cursor is not None:

            _query_params.append(('cursor', cursor))

        if deltas is not None:

            _query_params.append(('deltas', deltas))

        # process the header parameters
        if last_event_id is not None:
            _header_params['Last-Event-ID'] = last_event_id
        # process the form parameters
        # process the body parameter


        # set the HTTP header `Accept`
        if 'Accept' not in _header_params:
            _header_params['Accept'] = self.api_client.select_header_accept(
                [
                    'text/event-stream',
                    'application/json'
                ]
            )


        # authentication setting
        _auth_settings: List[str] = [
            'bearerAuth'
        ]

        return self.api_client.param_serialize(
            method='GET',
            resource_path='/v1/sessions/{session_id}/stream',
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
    async def update_session(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        update_session_request: UpdateSessionRequest,
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
    ) -> Session:
        """Update a Session

        Merges host metadata into the Session. A present key replaces its value, an explicit `null` deletes that key, and a key the patch does not mention survives.  Merge rather than replace, because independent writers share this map — a conversation UI writing a title, correlation tooling writing a trace id — and a full replacement would make each silently discard the other's keys. The merge happens under the Session lock, so two concurrent patches compose instead of one overwriting the other's read.  `\"metadata\": null` is refused rather than guessed at: it could mean \"clear everything\" or \"leave it alone\", and either reading is destructive or silent. Delete keys one at a time.  Bounds apply to the merged result, not to the patch, so a patch that deletes as many keys as it adds is not refused for a count it never produces. Requires the `update_session` operation.

        :param session_id: (required)
        :type session_id: str
        :param update_session_request: (required)
        :type update_session_request: UpdateSessionRequest
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

        _param = self._update_session_serialize(
            session_id=session_id,
            update_session_request=update_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def update_session_with_http_info(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        update_session_request: UpdateSessionRequest,
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
    ) -> ApiResponse[Session]:
        """Update a Session

        Merges host metadata into the Session. A present key replaces its value, an explicit `null` deletes that key, and a key the patch does not mention survives.  Merge rather than replace, because independent writers share this map — a conversation UI writing a title, correlation tooling writing a trace id — and a full replacement would make each silently discard the other's keys. The merge happens under the Session lock, so two concurrent patches compose instead of one overwriting the other's read.  `\"metadata\": null` is refused rather than guessed at: it could mean \"clear everything\" or \"leave it alone\", and either reading is destructive or silent. Delete keys one at a time.  Bounds apply to the merged result, not to the patch, so a patch that deletes as many keys as it adds is not refused for a count it never produces. Requires the `update_session` operation.

        :param session_id: (required)
        :type session_id: str
        :param update_session_request: (required)
        :type update_session_request: UpdateSessionRequest
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

        _param = self._update_session_serialize(
            session_id=session_id,
            update_session_request=update_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
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
    async def update_session_without_preload_content(
        self,
        session_id: Annotated[str, Field(min_length=1, strict=True)],
        update_session_request: UpdateSessionRequest,
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
        """Update a Session

        Merges host metadata into the Session. A present key replaces its value, an explicit `null` deletes that key, and a key the patch does not mention survives.  Merge rather than replace, because independent writers share this map — a conversation UI writing a title, correlation tooling writing a trace id — and a full replacement would make each silently discard the other's keys. The merge happens under the Session lock, so two concurrent patches compose instead of one overwriting the other's read.  `\"metadata\": null` is refused rather than guessed at: it could mean \"clear everything\" or \"leave it alone\", and either reading is destructive or silent. Delete keys one at a time.  Bounds apply to the merged result, not to the patch, so a patch that deletes as many keys as it adds is not refused for a count it never produces. Requires the `update_session` operation.

        :param session_id: (required)
        :type session_id: str
        :param update_session_request: (required)
        :type update_session_request: UpdateSessionRequest
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

        _param = self._update_session_serialize(
            session_id=session_id,
            update_session_request=update_session_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Session",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _update_session_serialize(
        self,
        session_id,
        update_session_request,
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
        if session_id is not None:
            _path_params['session_id'] = session_id
        # process the query parameters
        # process the header parameters
        # process the form parameters
        # process the body parameter
        if update_session_request is not None:
            _body_params = update_session_request


        # set the HTTP header `Accept`
        if 'Accept' not in _header_params:
            _header_params['Accept'] = self.api_client.select_header_accept(
                [
                    'application/json'
                ]
            )

        # set the HTTP header `Content-Type`
        if _content_type:
            _header_params['Content-Type'] = _content_type
        else:
            _default_content_type = (
                self.api_client.select_header_content_type(
                    [
                        'application/json'
                    ]
                )
            )
            if _default_content_type is not None:
                _header_params['Content-Type'] = _default_content_type

        # authentication setting
        _auth_settings: List[str] = [
            'bearerAuth'
        ]

        return self.api_client.param_serialize(
            method='PATCH',
            resource_path='/v1/sessions/{session_id}',
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
