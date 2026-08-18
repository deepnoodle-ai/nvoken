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

from datetime import datetime
from pydantic import Field, StrictBool, StrictStr, field_validator
from typing import List, Optional
from typing_extensions import Annotated
from nvoken_generated.models.create_invocation_request import CreateInvocationRequest
from nvoken_generated.models.create_nudge_request import CreateNudgeRequest
from nvoken_generated.models.invocation import Invocation
from nvoken_generated.models.invocation_list import InvocationList
from nvoken_generated.models.invocation_log_list import InvocationLogList
from nvoken_generated.models.invocation_result import InvocationResult
from nvoken_generated.models.invocation_status import InvocationStatus
from nvoken_generated.models.invocation_timeline import InvocationTimeline
from nvoken_generated.models.nudge import Nudge
from nvoken_generated.models.nudge_acknowledgement import NudgeAcknowledgement
from nvoken_generated.models.nudge_list import NudgeList
from nvoken_generated.models.nudge_status import NudgeStatus
from nvoken_generated.models.resume_invocation_request import ResumeInvocationRequest
from nvoken_generated.models.submit_host_tool_results_request import SubmitHostToolResultsRequest
from nvoken_generated.models.submit_host_tool_results_response import SubmitHostToolResultsResponse
from nvoken_generated.models.tool_call_list import ToolCallList
from nvoken_generated.models.trace import Trace
from nvoken_generated.models.trace_list import TraceList

from nvoken_generated.api_client import ApiClient, RequestSerialized
from nvoken_generated.api_response import ApiResponse
from nvoken_generated.rest import RESTResponseType


class InvocationsApi:
    """NOTE: This class is auto generated by OpenAPI Generator
    Ref: https://openapi-generator.tech

    Do not edit the class manually.
    """

    def __init__(self, api_client=None) -> None:
        if api_client is None:
            api_client = ApiClient.get_default()
        self.api_client = api_client


    @validate_call
    async def cancel_invocation(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> Invocation:
        """Stop an Invocation and discard its work

        Stops a turn and discards what it produced. The turn ends `cancelled` and its work does not carry into the next turn — use interrupt instead if you want to keep it.  Safe to repeat. Cancelling a turn that already finished returns it unchanged rather than failing. A successful response means the cancellation is recorded and will stick. Work already sent to the model provider stops as soon as it can, so you may still be billed for what had run by then.  Send an empty request body.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._cancel_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def cancel_invocation_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[Invocation]:
        """Stop an Invocation and discard its work

        Stops a turn and discards what it produced. The turn ends `cancelled` and its work does not carry into the next turn — use interrupt instead if you want to keep it.  Safe to repeat. Cancelling a turn that already finished returns it unchanged rather than failing. A successful response means the cancellation is recorded and will stick. Work already sent to the model provider stops as soon as it can, so you may still be billed for what had run by then.  Send an empty request body.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._cancel_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def cancel_invocation_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Stop an Invocation and discard its work

        Stops a turn and discards what it produced. The turn ends `cancelled` and its work does not carry into the next turn — use interrupt instead if you want to keep it.  Safe to repeat. Cancelling a turn that already finished returns it unchanged rather than failing. A successful response means the cancellation is recorded and will stick. Work already sent to the model provider stops as soon as it can, so you may still be billed for what had run by then.  Send an empty request body.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._cancel_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _cancel_invocation_serialize(
        self,
        invocation_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
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
            method='POST',
            resource_path='/v1/invocations/{invocation_id}/cancel',
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
    async def cancel_nudge(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        nudge_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> Nudge:
        """Withdraw a Nudge the Invocation has not taken

        Withdraws direction you sent with `/nudges`, as long as the turn has not picked it up yet. Cancelling something already cancelled returns it unchanged, so retrying is safe.  Cancelling races the turn, and whichever happens first wins outright: you either withdraw it cleanly or the turn uses it. It is never half-applied. If the turn got there first, you get a conflict and the entry stays `drained`.

        :param invocation_id: (required)
        :type invocation_id: str
        :param nudge_id: (required)
        :type nudge_id: str
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

        _param = self._cancel_nudge_serialize(
            invocation_id=invocation_id,
            nudge_id=nudge_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Nudge",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
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
    async def cancel_nudge_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        nudge_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[Nudge]:
        """Withdraw a Nudge the Invocation has not taken

        Withdraws direction you sent with `/nudges`, as long as the turn has not picked it up yet. Cancelling something already cancelled returns it unchanged, so retrying is safe.  Cancelling races the turn, and whichever happens first wins outright: you either withdraw it cleanly or the turn uses it. It is never half-applied. If the turn got there first, you get a conflict and the entry stays `drained`.

        :param invocation_id: (required)
        :type invocation_id: str
        :param nudge_id: (required)
        :type nudge_id: str
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

        _param = self._cancel_nudge_serialize(
            invocation_id=invocation_id,
            nudge_id=nudge_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Nudge",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
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
    async def cancel_nudge_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        nudge_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Withdraw a Nudge the Invocation has not taken

        Withdraws direction you sent with `/nudges`, as long as the turn has not picked it up yet. Cancelling something already cancelled returns it unchanged, so retrying is safe.  Cancelling races the turn, and whichever happens first wins outright: you either withdraw it cleanly or the turn uses it. It is never half-applied. If the turn got there first, you get a conflict and the entry stays `drained`.

        :param invocation_id: (required)
        :type invocation_id: str
        :param nudge_id: (required)
        :type nudge_id: str
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

        _param = self._cancel_nudge_serialize(
            invocation_id=invocation_id,
            nudge_id=nudge_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Nudge",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _cancel_nudge_serialize(
        self,
        invocation_id,
        nudge_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
        if nudge_id is not None:
            _path_params['nudge_id'] = nudge_id
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
            method='POST',
            resource_path='/v1/invocations/{invocation_id}/nudges/{nudge_id}/cancel',
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
    async def create_invocation(
        self,
        create_invocation_request: CreateInvocationRequest,
        x_anthropic_api_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]], Field(description="Caller-supplied Anthropic API key, equivalent to a caller_ephemeral `provider_keys` selection. The header must name the model provider and cannot be combined with the body field. Siblings: X-Openai-Api-Key, X-Gemini-Api-Key, X-Xai-Api-Key. ")] = None,
        x_openai_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
        x_gemini_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
        x_xai_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
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
    ) -> Invocation:
        """Start one Invocation in the background

        Starts one agent turn and returns immediately. In a single database transaction nvoken resolves the deliberately created Agent, selects its Agent Definition revision, finds or creates the Session, appends your input as one message, and queues the turn. Admission never creates an Agent or reusable configuration. You get a response only after that transaction commits, so a `202` means the work is safely recorded and will run even if nvoken restarts. The model does not run on this request — it runs in the background, and you follow it with the stream or by polling.  Pick the Session with either `session_id` or `session_key`, not both. A Session ID must belong to the Agent you named, or to a Session created without an Agent — in which case this turn binds that Agent permanently. An App credential without a tenant constraint may omit `tenant_key` and use whichever tenant the Session already belongs to. A credential locked to one tenant cannot reach another; naming a different one returns `403 forbidden` without revealing whether the resource exists.  ## Retrying safely  Send `idempotency_key` and you can retry this request without risking a second turn. A repeat with the same key returns the original turn and does not add your input again, even if that turn has already finished. Keys are scoped to the tenant and Agent.  A repeat counts as the same request only if the Session selector, the Agent, explicit revision, per-turn overrides, `metadata`, `context`, `webhook`, `on_budget_exhausted`, and input all match. The original admitted revision is returned even if its Definition has advanced. Values are compared as sent, so omitting an override is not the same as supplying one that happens to equal the Definition. Key order inside JSON objects does not matter; array order does. Change anything material and you get `idempotency_conflict` rather than a surprise second turn.  `user_key` is the one exception, because it is the Session's rather than the turn's: omitting it asserts nothing and inherits what the Session already holds, while sending a different one conflicts.  ## When the Session is already busy  A Session runs one turn at a time, and `if_active` decides what happens when you start another. The default, `reject`, returns `session_invocation_active`.  `supersede` cancels the running turn and starts yours in its place, atomically — there is no moment where the Session has no turn or two turns. It requires permission to both create and cancel. Retrying the same request returns your original turn and never cancels newer work that started in the meantime.  `interrupt` needs the same permission but stops the running turn cleanly instead of discarding its work. If that turn can stop immediately, yours starts in the same transaction. If it is mid-step, nvoken records the interrupt and this request waits for it. If it has not stopped by the time the wait is up, you get `session_invocation_active` with `details.interrupt_requested = true` — the interrupt is still in effect, so just send the request again.  ## Retired models  A deprecated model keeps working. On and after its `retires_at` date, new turns are refused with `422 model_retired`, and `details` tells you what to do about it: the `model` you asked for, its `retires_at` date, the exact `replacement` provider and id to switch to, and the request `path`. Retrying an idempotency key from before the retirement still returns that original turn.  ## Size limits  A text-only body may be up to 1 MiB. A body with images or documents may be up to 24 MiB, and within that: at most 8 media blocks, 16 MiB of decoded media in total, 5 MiB per image, and 16 MiB per document. Anything over these is rejected before a turn is created.  URLs are fetched after the idempotency check and before anything is saved, so a retry does not download twice. nvoken accepts public HTTPS only, stops reading at the size limit, and checks what the bytes actually are. It stores them and never fetches the URL again.  ## Streaming  This response is the acknowledgment. Once you hold the returned `id`, follow the turn with `GET /v1/sessions/{session_id}/stream?invocation_id=…`. Admission and streaming are separate requests on purpose: a dropped stream costs you nothing, because the turn already exists and no reconnect can create a second one.

        :param create_invocation_request: (required)
        :type create_invocation_request: CreateInvocationRequest
        :param x_anthropic_api_key: Caller-supplied Anthropic API key, equivalent to a caller_ephemeral `provider_keys` selection. The header must name the model provider and cannot be combined with the body field. Siblings: X-Openai-Api-Key, X-Gemini-Api-Key, X-Xai-Api-Key.
        :type x_anthropic_api_key: str
        :param x_openai_api_key:
        :type x_openai_api_key: str
        :param x_gemini_api_key:
        :type x_gemini_api_key: str
        :param x_xai_api_key:
        :type x_xai_api_key: str
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

        _param = self._create_invocation_serialize(
            create_invocation_request=create_invocation_request,
            x_anthropic_api_key=x_anthropic_api_key,
            x_openai_api_key=x_openai_api_key,
            x_gemini_api_key=x_gemini_api_key,
            x_xai_api_key=x_xai_api_key,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '422': "ErrorResponse",
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
    async def create_invocation_with_http_info(
        self,
        create_invocation_request: CreateInvocationRequest,
        x_anthropic_api_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]], Field(description="Caller-supplied Anthropic API key, equivalent to a caller_ephemeral `provider_keys` selection. The header must name the model provider and cannot be combined with the body field. Siblings: X-Openai-Api-Key, X-Gemini-Api-Key, X-Xai-Api-Key. ")] = None,
        x_openai_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
        x_gemini_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
        x_xai_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
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
    ) -> ApiResponse[Invocation]:
        """Start one Invocation in the background

        Starts one agent turn and returns immediately. In a single database transaction nvoken resolves the deliberately created Agent, selects its Agent Definition revision, finds or creates the Session, appends your input as one message, and queues the turn. Admission never creates an Agent or reusable configuration. You get a response only after that transaction commits, so a `202` means the work is safely recorded and will run even if nvoken restarts. The model does not run on this request — it runs in the background, and you follow it with the stream or by polling.  Pick the Session with either `session_id` or `session_key`, not both. A Session ID must belong to the Agent you named, or to a Session created without an Agent — in which case this turn binds that Agent permanently. An App credential without a tenant constraint may omit `tenant_key` and use whichever tenant the Session already belongs to. A credential locked to one tenant cannot reach another; naming a different one returns `403 forbidden` without revealing whether the resource exists.  ## Retrying safely  Send `idempotency_key` and you can retry this request without risking a second turn. A repeat with the same key returns the original turn and does not add your input again, even if that turn has already finished. Keys are scoped to the tenant and Agent.  A repeat counts as the same request only if the Session selector, the Agent, explicit revision, per-turn overrides, `metadata`, `context`, `webhook`, `on_budget_exhausted`, and input all match. The original admitted revision is returned even if its Definition has advanced. Values are compared as sent, so omitting an override is not the same as supplying one that happens to equal the Definition. Key order inside JSON objects does not matter; array order does. Change anything material and you get `idempotency_conflict` rather than a surprise second turn.  `user_key` is the one exception, because it is the Session's rather than the turn's: omitting it asserts nothing and inherits what the Session already holds, while sending a different one conflicts.  ## When the Session is already busy  A Session runs one turn at a time, and `if_active` decides what happens when you start another. The default, `reject`, returns `session_invocation_active`.  `supersede` cancels the running turn and starts yours in its place, atomically — there is no moment where the Session has no turn or two turns. It requires permission to both create and cancel. Retrying the same request returns your original turn and never cancels newer work that started in the meantime.  `interrupt` needs the same permission but stops the running turn cleanly instead of discarding its work. If that turn can stop immediately, yours starts in the same transaction. If it is mid-step, nvoken records the interrupt and this request waits for it. If it has not stopped by the time the wait is up, you get `session_invocation_active` with `details.interrupt_requested = true` — the interrupt is still in effect, so just send the request again.  ## Retired models  A deprecated model keeps working. On and after its `retires_at` date, new turns are refused with `422 model_retired`, and `details` tells you what to do about it: the `model` you asked for, its `retires_at` date, the exact `replacement` provider and id to switch to, and the request `path`. Retrying an idempotency key from before the retirement still returns that original turn.  ## Size limits  A text-only body may be up to 1 MiB. A body with images or documents may be up to 24 MiB, and within that: at most 8 media blocks, 16 MiB of decoded media in total, 5 MiB per image, and 16 MiB per document. Anything over these is rejected before a turn is created.  URLs are fetched after the idempotency check and before anything is saved, so a retry does not download twice. nvoken accepts public HTTPS only, stops reading at the size limit, and checks what the bytes actually are. It stores them and never fetches the URL again.  ## Streaming  This response is the acknowledgment. Once you hold the returned `id`, follow the turn with `GET /v1/sessions/{session_id}/stream?invocation_id=…`. Admission and streaming are separate requests on purpose: a dropped stream costs you nothing, because the turn already exists and no reconnect can create a second one.

        :param create_invocation_request: (required)
        :type create_invocation_request: CreateInvocationRequest
        :param x_anthropic_api_key: Caller-supplied Anthropic API key, equivalent to a caller_ephemeral `provider_keys` selection. The header must name the model provider and cannot be combined with the body field. Siblings: X-Openai-Api-Key, X-Gemini-Api-Key, X-Xai-Api-Key.
        :type x_anthropic_api_key: str
        :param x_openai_api_key:
        :type x_openai_api_key: str
        :param x_gemini_api_key:
        :type x_gemini_api_key: str
        :param x_xai_api_key:
        :type x_xai_api_key: str
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

        _param = self._create_invocation_serialize(
            create_invocation_request=create_invocation_request,
            x_anthropic_api_key=x_anthropic_api_key,
            x_openai_api_key=x_openai_api_key,
            x_gemini_api_key=x_gemini_api_key,
            x_xai_api_key=x_xai_api_key,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '422': "ErrorResponse",
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
    async def create_invocation_without_preload_content(
        self,
        create_invocation_request: CreateInvocationRequest,
        x_anthropic_api_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]], Field(description="Caller-supplied Anthropic API key, equivalent to a caller_ephemeral `provider_keys` selection. The header must name the model provider and cannot be combined with the body field. Siblings: X-Openai-Api-Key, X-Gemini-Api-Key, X-Xai-Api-Key. ")] = None,
        x_openai_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
        x_gemini_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
        x_xai_api_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=65536)]] = None,
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
        """Start one Invocation in the background

        Starts one agent turn and returns immediately. In a single database transaction nvoken resolves the deliberately created Agent, selects its Agent Definition revision, finds or creates the Session, appends your input as one message, and queues the turn. Admission never creates an Agent or reusable configuration. You get a response only after that transaction commits, so a `202` means the work is safely recorded and will run even if nvoken restarts. The model does not run on this request — it runs in the background, and you follow it with the stream or by polling.  Pick the Session with either `session_id` or `session_key`, not both. A Session ID must belong to the Agent you named, or to a Session created without an Agent — in which case this turn binds that Agent permanently. An App credential without a tenant constraint may omit `tenant_key` and use whichever tenant the Session already belongs to. A credential locked to one tenant cannot reach another; naming a different one returns `403 forbidden` without revealing whether the resource exists.  ## Retrying safely  Send `idempotency_key` and you can retry this request without risking a second turn. A repeat with the same key returns the original turn and does not add your input again, even if that turn has already finished. Keys are scoped to the tenant and Agent.  A repeat counts as the same request only if the Session selector, the Agent, explicit revision, per-turn overrides, `metadata`, `context`, `webhook`, `on_budget_exhausted`, and input all match. The original admitted revision is returned even if its Definition has advanced. Values are compared as sent, so omitting an override is not the same as supplying one that happens to equal the Definition. Key order inside JSON objects does not matter; array order does. Change anything material and you get `idempotency_conflict` rather than a surprise second turn.  `user_key` is the one exception, because it is the Session's rather than the turn's: omitting it asserts nothing and inherits what the Session already holds, while sending a different one conflicts.  ## When the Session is already busy  A Session runs one turn at a time, and `if_active` decides what happens when you start another. The default, `reject`, returns `session_invocation_active`.  `supersede` cancels the running turn and starts yours in its place, atomically — there is no moment where the Session has no turn or two turns. It requires permission to both create and cancel. Retrying the same request returns your original turn and never cancels newer work that started in the meantime.  `interrupt` needs the same permission but stops the running turn cleanly instead of discarding its work. If that turn can stop immediately, yours starts in the same transaction. If it is mid-step, nvoken records the interrupt and this request waits for it. If it has not stopped by the time the wait is up, you get `session_invocation_active` with `details.interrupt_requested = true` — the interrupt is still in effect, so just send the request again.  ## Retired models  A deprecated model keeps working. On and after its `retires_at` date, new turns are refused with `422 model_retired`, and `details` tells you what to do about it: the `model` you asked for, its `retires_at` date, the exact `replacement` provider and id to switch to, and the request `path`. Retrying an idempotency key from before the retirement still returns that original turn.  ## Size limits  A text-only body may be up to 1 MiB. A body with images or documents may be up to 24 MiB, and within that: at most 8 media blocks, 16 MiB of decoded media in total, 5 MiB per image, and 16 MiB per document. Anything over these is rejected before a turn is created.  URLs are fetched after the idempotency check and before anything is saved, so a retry does not download twice. nvoken accepts public HTTPS only, stops reading at the size limit, and checks what the bytes actually are. It stores them and never fetches the URL again.  ## Streaming  This response is the acknowledgment. Once you hold the returned `id`, follow the turn with `GET /v1/sessions/{session_id}/stream?invocation_id=…`. Admission and streaming are separate requests on purpose: a dropped stream costs you nothing, because the turn already exists and no reconnect can create a second one.

        :param create_invocation_request: (required)
        :type create_invocation_request: CreateInvocationRequest
        :param x_anthropic_api_key: Caller-supplied Anthropic API key, equivalent to a caller_ephemeral `provider_keys` selection. The header must name the model provider and cannot be combined with the body field. Siblings: X-Openai-Api-Key, X-Gemini-Api-Key, X-Xai-Api-Key.
        :type x_anthropic_api_key: str
        :param x_openai_api_key:
        :type x_openai_api_key: str
        :param x_gemini_api_key:
        :type x_gemini_api_key: str
        :param x_xai_api_key:
        :type x_xai_api_key: str
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

        _param = self._create_invocation_serialize(
            create_invocation_request=create_invocation_request,
            x_anthropic_api_key=x_anthropic_api_key,
            x_openai_api_key=x_openai_api_key,
            x_gemini_api_key=x_gemini_api_key,
            x_xai_api_key=x_xai_api_key,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '422': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _create_invocation_serialize(
        self,
        create_invocation_request,
        x_anthropic_api_key,
        x_openai_api_key,
        x_gemini_api_key,
        x_xai_api_key,
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
        if x_anthropic_api_key is not None:
            _header_params['X-Anthropic-Api-Key'] = x_anthropic_api_key
        if x_openai_api_key is not None:
            _header_params['X-Openai-Api-Key'] = x_openai_api_key
        if x_gemini_api_key is not None:
            _header_params['X-Gemini-Api-Key'] = x_gemini_api_key
        if x_xai_api_key is not None:
            _header_params['X-Xai-Api-Key'] = x_xai_api_key
        # process the form parameters
        # process the body parameter
        if create_invocation_request is not None:
            _body_params = create_invocation_request


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
            resource_path='/v1/invocations',
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
    async def create_nudge(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        create_nudge_request: CreateNudgeRequest,
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
    ) -> NudgeAcknowledgement:
        """Send extra direction to a running Invocation

        Sends extra direction to a turn that is already running — \"focus on the marine segment\" — without stopping it and without losing the work you are steering. Use this when a long turn is heading the wrong way and you want to correct it in place.  Compare with `if_active: supersede` on a new Invocation, which replaces the running turn and discards what it had produced. Steering a long turn that way throws away exactly the work you were trying to redirect.  **A nudge is not an interrupt, and it is not immediate.** The turn picks it up at its next model-call boundary: before the next model call in a running builtin or MCP tool loop, when a host-tool result resumes the next execution segment, or when a turn that thought it was finished re-enters its loop to answer you. A parked host tool is not woken; its result still has to resume the turn. A model call or tool run already in flight is never aborted to deliver a Nudge. A turn you have interrupted is never given more work — the interrupt wins and the direction you staged expires unused.  Nudges and Invocations never turn into each other. Posting to `/v1/invocations` against a busy Session behaves exactly as its `if_active` setting says; it never quietly becomes a nudge, and a nudge never quietly becomes a new turn.  If the turn ends without ever picking it up, your Nudge is marked `expired` at that moment and has no effect on any later turn. Check `GET .../nudges` to see whether it was used or missed. Whether to re-send missed direction as the next turn's input is your call.  `content` must be text — a string, or an array of text blocks. Images and documents are fine on a turn's own input but are refused here, because a turn resuming in place carries text only, and silently dropping your attachment would be worse than telling you now.  Requires the same permission as cancelling the turn.

        :param invocation_id: (required)
        :type invocation_id: str
        :param create_nudge_request: (required)
        :type create_nudge_request: CreateNudgeRequest
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

        _param = self._create_nudge_serialize(
            invocation_id=invocation_id,
            create_nudge_request=create_nudge_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "NudgeAcknowledgement",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
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
    async def create_nudge_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        create_nudge_request: CreateNudgeRequest,
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
    ) -> ApiResponse[NudgeAcknowledgement]:
        """Send extra direction to a running Invocation

        Sends extra direction to a turn that is already running — \"focus on the marine segment\" — without stopping it and without losing the work you are steering. Use this when a long turn is heading the wrong way and you want to correct it in place.  Compare with `if_active: supersede` on a new Invocation, which replaces the running turn and discards what it had produced. Steering a long turn that way throws away exactly the work you were trying to redirect.  **A nudge is not an interrupt, and it is not immediate.** The turn picks it up at its next model-call boundary: before the next model call in a running builtin or MCP tool loop, when a host-tool result resumes the next execution segment, or when a turn that thought it was finished re-enters its loop to answer you. A parked host tool is not woken; its result still has to resume the turn. A model call or tool run already in flight is never aborted to deliver a Nudge. A turn you have interrupted is never given more work — the interrupt wins and the direction you staged expires unused.  Nudges and Invocations never turn into each other. Posting to `/v1/invocations` against a busy Session behaves exactly as its `if_active` setting says; it never quietly becomes a nudge, and a nudge never quietly becomes a new turn.  If the turn ends without ever picking it up, your Nudge is marked `expired` at that moment and has no effect on any later turn. Check `GET .../nudges` to see whether it was used or missed. Whether to re-send missed direction as the next turn's input is your call.  `content` must be text — a string, or an array of text blocks. Images and documents are fine on a turn's own input but are refused here, because a turn resuming in place carries text only, and silently dropping your attachment would be worse than telling you now.  Requires the same permission as cancelling the turn.

        :param invocation_id: (required)
        :type invocation_id: str
        :param create_nudge_request: (required)
        :type create_nudge_request: CreateNudgeRequest
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

        _param = self._create_nudge_serialize(
            invocation_id=invocation_id,
            create_nudge_request=create_nudge_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "NudgeAcknowledgement",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
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
    async def create_nudge_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        create_nudge_request: CreateNudgeRequest,
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
        """Send extra direction to a running Invocation

        Sends extra direction to a turn that is already running — \"focus on the marine segment\" — without stopping it and without losing the work you are steering. Use this when a long turn is heading the wrong way and you want to correct it in place.  Compare with `if_active: supersede` on a new Invocation, which replaces the running turn and discards what it had produced. Steering a long turn that way throws away exactly the work you were trying to redirect.  **A nudge is not an interrupt, and it is not immediate.** The turn picks it up at its next model-call boundary: before the next model call in a running builtin or MCP tool loop, when a host-tool result resumes the next execution segment, or when a turn that thought it was finished re-enters its loop to answer you. A parked host tool is not woken; its result still has to resume the turn. A model call or tool run already in flight is never aborted to deliver a Nudge. A turn you have interrupted is never given more work — the interrupt wins and the direction you staged expires unused.  Nudges and Invocations never turn into each other. Posting to `/v1/invocations` against a busy Session behaves exactly as its `if_active` setting says; it never quietly becomes a nudge, and a nudge never quietly becomes a new turn.  If the turn ends without ever picking it up, your Nudge is marked `expired` at that moment and has no effect on any later turn. Check `GET .../nudges` to see whether it was used or missed. Whether to re-send missed direction as the next turn's input is your call.  `content` must be text — a string, or an array of text blocks. Images and documents are fine on a turn's own input but are refused here, because a turn resuming in place carries text only, and silently dropping your attachment would be worse than telling you now.  Requires the same permission as cancelling the turn.

        :param invocation_id: (required)
        :type invocation_id: str
        :param create_nudge_request: (required)
        :type create_nudge_request: CreateNudgeRequest
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

        _param = self._create_nudge_serialize(
            invocation_id=invocation_id,
            create_nudge_request=create_nudge_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "NudgeAcknowledgement",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _create_nudge_serialize(
        self,
        invocation_id,
        create_nudge_request,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
        # process the query parameters
        # process the header parameters
        # process the form parameters
        # process the body parameter
        if create_nudge_request is not None:
            _body_params = create_nudge_request


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
            resource_path='/v1/invocations/{invocation_id}/nudges',
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
    async def get_invocation(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> Invocation:
        """Read authoritative Invocation identity and state

        The turn's current state, including anything that went wrong after it started.  A credential that can authenticate but lacks permission for this read gets `forbidden`. A turn belonging to another tenant is reported as `not_found` rather than `forbidden`, so you cannot use this endpoint to discover whether an ID exists outside your scope.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
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
    async def get_invocation_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[Invocation]:
        """Read authoritative Invocation identity and state

        The turn's current state, including anything that went wrong after it started.  A credential that can authenticate but lacks permission for this read gets `forbidden`. A turn belonging to another tenant is reported as `not_found` rather than `forbidden`, so you cannot use this endpoint to discover whether an ID exists outside your scope.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
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
    async def get_invocation_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Read authoritative Invocation identity and state

        The turn's current state, including anything that went wrong after it started.  A credential that can authenticate but lacks permission for this read gets `forbidden`. A turn belonging to another tenant is reported as `not_found` rather than `forbidden`, so you cannot use this endpoint to discover whether an ID exists outside your scope.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
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


    def _get_invocation_serialize(
        self,
        invocation_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
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
            resource_path='/v1/invocations/{invocation_id}',
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
    async def get_invocation_result(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> InvocationResult:
        """Read an Invocation together with its messages

        Returns the turn and the messages it produced, at any status. This is the convenient read for \"what did the agent say?\" — `output_text` gives you the assistant's text already joined into a single string, so you do not have to walk the message blocks yourself.  The turn and its messages are read from one consistent database snapshot, so you will never see a finished turn whose last message is missing.  Authentication, tenant scoping, and the not-found behavior are the same as reading the Invocation on its own.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_result_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationResult",
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
    async def get_invocation_result_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[InvocationResult]:
        """Read an Invocation together with its messages

        Returns the turn and the messages it produced, at any status. This is the convenient read for \"what did the agent say?\" — `output_text` gives you the assistant's text already joined into a single string, so you do not have to walk the message blocks yourself.  The turn and its messages are read from one consistent database snapshot, so you will never see a finished turn whose last message is missing.  Authentication, tenant scoping, and the not-found behavior are the same as reading the Invocation on its own.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_result_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationResult",
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
    async def get_invocation_result_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Read an Invocation together with its messages

        Returns the turn and the messages it produced, at any status. This is the convenient read for \"what did the agent say?\" — `output_text` gives you the assistant's text already joined into a single string, so you do not have to walk the message blocks yourself.  The turn and its messages are read from one consistent database snapshot, so you will never see a finished turn whose last message is missing.  Authentication, tenant scoping, and the not-found behavior are the same as reading the Invocation on its own.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_result_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationResult",
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


    def _get_invocation_result_serialize(
        self,
        invocation_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
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
            resource_path='/v1/invocations/{invocation_id}/result',
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
    async def get_invocation_timeline(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> InvocationTimeline:
        """Read the durable execution waterfall for one Invocation

        Assembles lifecycle waits, model calls, tool calls, nudges, and compactions from one database snapshot. It contains timings and usage, never prompts, responses, tool arguments, results, or error text. After Session erasure it degrades to the retained facts-only skeleton.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_timeline_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationTimeline",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def get_invocation_timeline_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[InvocationTimeline]:
        """Read the durable execution waterfall for one Invocation

        Assembles lifecycle waits, model calls, tool calls, nudges, and compactions from one database snapshot. It contains timings and usage, never prompts, responses, tool arguments, results, or error text. After Session erasure it degrades to the retained facts-only skeleton.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_timeline_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationTimeline",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def get_invocation_timeline_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Read the durable execution waterfall for one Invocation

        Assembles lifecycle waits, model calls, tool calls, nudges, and compactions from one database snapshot. It contains timings and usage, never prompts, responses, tool arguments, results, or error text. After Session erasure it degrades to the retained facts-only skeleton.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._get_invocation_timeline_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationTimeline",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _get_invocation_timeline_serialize(
        self,
        invocation_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
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
            resource_path='/v1/invocations/{invocation_id}/timeline',
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
    async def get_trace(
        self,
        trace_id: Annotated[str, Field(strict=True)],
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
    ) -> Trace:
        """Read one hosted agent trace

        Returns a content-free projection of up to 200 OpenTelemetry spans. Use the pageable Invocation log endpoint with `trace_id` for associated logs. `is_partial` says when the agent root has not arrived or the bounded read omitted spans. nvoken grounds the trace's Invocation attribution in its durable Invocation record before returning it; knowing a W3C trace ID grants no authority.

        :param trace_id: (required)
        :type trace_id: str
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

        _param = self._get_trace_serialize(
            trace_id=trace_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Trace",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def get_trace_with_http_info(
        self,
        trace_id: Annotated[str, Field(strict=True)],
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
    ) -> ApiResponse[Trace]:
        """Read one hosted agent trace

        Returns a content-free projection of up to 200 OpenTelemetry spans. Use the pageable Invocation log endpoint with `trace_id` for associated logs. `is_partial` says when the agent root has not arrived or the bounded read omitted spans. nvoken grounds the trace's Invocation attribution in its durable Invocation record before returning it; knowing a W3C trace ID grants no authority.

        :param trace_id: (required)
        :type trace_id: str
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

        _param = self._get_trace_serialize(
            trace_id=trace_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Trace",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def get_trace_without_preload_content(
        self,
        trace_id: Annotated[str, Field(strict=True)],
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
        """Read one hosted agent trace

        Returns a content-free projection of up to 200 OpenTelemetry spans. Use the pageable Invocation log endpoint with `trace_id` for associated logs. `is_partial` says when the agent root has not arrived or the bounded read omitted spans. nvoken grounds the trace's Invocation attribution in its durable Invocation record before returning it; knowing a W3C trace ID grants no authority.

        :param trace_id: (required)
        :type trace_id: str
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

        _param = self._get_trace_serialize(
            trace_id=trace_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Trace",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _get_trace_serialize(
        self,
        trace_id,
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
        if trace_id is not None:
            _path_params['trace_id'] = trace_id
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
            resource_path='/v1/traces/{trace_id}',
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
    async def interrupt_invocation(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> Invocation:
        """Stop an Invocation but keep what it produced

        Asks a running turn to stop at its next clean stopping point. It ends `completed` with `stop_reason: interrupted`, and everything it produced — the model's replies and any tool results — stays in the conversation for the next turn. That is the whole difference from cancelling, which throws the turn's work away.  The request is recorded and safe to repeat. What happens next depends on what the turn was doing:  - Between steps (`queued`, `waiting`, or `running` with nothing   actively executing) it stops before this call returns. Any tool   calls you still owed results for are closed out, so submitting one   afterwards returns `409`. - Mid-step, nvoken records the request and returns the turn still   `running`. It stops at the next checkpoint, at worst one model call   later. Watch the stream or re-read the turn to see it end.  Interrupting a turn that has already finished changes nothing and returns it as-is. A turn that was asked for structured output but never produced a valid object ends `failed` with `structured_output_unsatisfied` rather than `completed`. Either way usage is reported in full and billed, because the work was kept.  Send an empty request body.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._interrupt_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def interrupt_invocation_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[Invocation]:
        """Stop an Invocation but keep what it produced

        Asks a running turn to stop at its next clean stopping point. It ends `completed` with `stop_reason: interrupted`, and everything it produced — the model's replies and any tool results — stays in the conversation for the next turn. That is the whole difference from cancelling, which throws the turn's work away.  The request is recorded and safe to repeat. What happens next depends on what the turn was doing:  - Between steps (`queued`, `waiting`, or `running` with nothing   actively executing) it stops before this call returns. Any tool   calls you still owed results for are closed out, so submitting one   afterwards returns `409`. - Mid-step, nvoken records the request and returns the turn still   `running`. It stops at the next checkpoint, at worst one model call   later. Watch the stream or re-read the turn to see it end.  Interrupting a turn that has already finished changes nothing and returns it as-is. A turn that was asked for structured output but never produced a valid object ends `failed` with `structured_output_unsatisfied` rather than `completed`. Either way usage is reported in full and billed, because the work was kept.  Send an empty request body.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._interrupt_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def interrupt_invocation_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Stop an Invocation but keep what it produced

        Asks a running turn to stop at its next clean stopping point. It ends `completed` with `stop_reason: interrupted`, and everything it produced — the model's replies and any tool results — stays in the conversation for the next turn. That is the whole difference from cancelling, which throws the turn's work away.  The request is recorded and safe to repeat. What happens next depends on what the turn was doing:  - Between steps (`queued`, `waiting`, or `running` with nothing   actively executing) it stops before this call returns. Any tool   calls you still owed results for are closed out, so submitting one   afterwards returns `409`. - Mid-step, nvoken records the request and returns the turn still   `running`. It stops at the next checkpoint, at worst one model call   later. Watch the stream or re-read the turn to see it end.  Interrupting a turn that has already finished changes nothing and returns it as-is. A turn that was asked for structured output but never produced a valid object ends `failed` with `structured_output_unsatisfied` rather than `completed`. Either way usage is reported in full and billed, because the work was kept.  Send an empty request body.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._interrupt_invocation_serialize(
            invocation_id=invocation_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "Invocation",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _interrupt_invocation_serialize(
        self,
        invocation_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
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
            method='POST',
            resource_path='/v1/invocations/{invocation_id}/interrupt',
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
    async def list_invocation_logs(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
        trace_id: Annotated[Optional[Annotated[str, Field(strict=True)]], Field(description="Return only logs correlated to this W3C trace ID.")] = None,
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
    ) -> InvocationLogList:
        """Page through hosted structured logs for one Invocation

        Returns the content-free structured lifecycle logs associated by the Invocation ID. Arbitrary attributes and raw error values are omitted. `status` is `disabled` when this installation has no configured observation store.

        :param invocation_id: (required)
        :type invocation_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
        :type limit: int
        :param trace_id: Return only logs correlated to this W3C trace ID.
        :type trace_id: str
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

        _param = self._list_invocation_logs_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            trace_id=trace_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationLogList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def list_invocation_logs_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
        trace_id: Annotated[Optional[Annotated[str, Field(strict=True)]], Field(description="Return only logs correlated to this W3C trace ID.")] = None,
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
    ) -> ApiResponse[InvocationLogList]:
        """Page through hosted structured logs for one Invocation

        Returns the content-free structured lifecycle logs associated by the Invocation ID. Arbitrary attributes and raw error values are omitted. `status` is `disabled` when this installation has no configured observation store.

        :param invocation_id: (required)
        :type invocation_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
        :type limit: int
        :param trace_id: Return only logs correlated to this W3C trace ID.
        :type trace_id: str
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

        _param = self._list_invocation_logs_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            trace_id=trace_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationLogList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def list_invocation_logs_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=200, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 100.")] = None,
        trace_id: Annotated[Optional[Annotated[str, Field(strict=True)]], Field(description="Return only logs correlated to this W3C trace ID.")] = None,
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
        """Page through hosted structured logs for one Invocation

        Returns the content-free structured lifecycle logs associated by the Invocation ID. Arbitrary attributes and raw error values are omitted. `status` is `disabled` when this installation has no configured observation store.

        :param invocation_id: (required)
        :type invocation_id: str
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 100.
        :type limit: int
        :param trace_id: Return only logs correlated to this W3C trace ID.
        :type trace_id: str
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

        _param = self._list_invocation_logs_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            trace_id=trace_id,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationLogList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_invocation_logs_serialize(
        self,
        invocation_id,
        cursor,
        limit,
        trace_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
        # process the query parameters
        if cursor is not None:

            _query_params.append(('cursor', cursor))

        if limit is not None:

            _query_params.append(('limit', limit))

        if trace_id is not None:

            _query_params.append(('trace_id', trace_id))

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
            resource_path='/v1/invocations/{invocation_id}/logs',
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
    async def list_invocation_traces(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> TraceList:
        """Page through hosted agent traces for one Invocation

        Returns newest-first, content-free summaries exported from Dive through OpenTelemetry. A child-only trace is returned as `is_partial: true` while its agent root is still open or if the process exits before that root is exported. Traces remain diagnostic and best-effort; the durable Invocation timeline is the execution authority. `status` is `disabled` when this installation has no configured observation store.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._list_invocation_traces_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "TraceList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def list_invocation_traces_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[TraceList]:
        """Page through hosted agent traces for one Invocation

        Returns newest-first, content-free summaries exported from Dive through OpenTelemetry. A child-only trace is returned as `is_partial: true` while its agent root is still open or if the process exits before that root is exported. Traces remain diagnostic and best-effort; the durable Invocation timeline is the execution authority. `status` is `disabled` when this installation has no configured observation store.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._list_invocation_traces_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "TraceList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def list_invocation_traces_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Page through hosted agent traces for one Invocation

        Returns newest-first, content-free summaries exported from Dive through OpenTelemetry. A child-only trace is returned as `is_partial: true` while its agent root is still open or if the process exits before that root is exported. Traces remain diagnostic and best-effort; the durable Invocation timeline is the execution authority. `status` is `disabled` when this installation has no configured observation store.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._list_invocation_traces_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "TraceList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_invocation_traces_serialize(
        self,
        invocation_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
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
            resource_path='/v1/invocations/{invocation_id}/traces',
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
    async def list_invocations(
        self,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        default_tenant: Annotated[Optional[StrictBool], Field(description="Select only the default tenant partition. Mutually exclusive with tenant_key.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        session_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        agent_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Mutually exclusive with agent_key.")] = None,
        agent_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id. ")] = None,
        status: Annotated[Optional[Annotated[List[InvocationStatus], Field(min_length=1)]], Field(description="Repeat to select a union of statuses. Order and duplicates are normalized before cursor binding. ")] = None,
        parent_invocation_id: Annotated[Optional[StrictStr], Field(description="Select direct children of one Invocation. Send the literal `null` to select top-level Invocations; omit the parameter to retain the authoritative unfiltered collection. This filter is part of the opaque cursor's collection identity. ")] = None,
        ended: Annotated[Optional[StrictBool], Field(description="Walk the turns that ended, oldest first, instead of listing current state newest first. See the description above. ")] = None,
        ended_since: Annotated[Optional[datetime], Field(description="Inclusive RFC 3339 lower bound on `ended_at`, for starting a feed that has no cursor yet. Requires `ended=true`, and is mutually exclusive with `cursor`, which already carries a position. ")] = None,
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
    ) -> InvocationList:
        """List authoritative Invocations, or walk the ones that ended

        Returns newest-first durable Invocation state. Exact filters combine with AND. An App credential without a tenant constraint may list all tenant partitions in that App, one named partition with `tenant_key`, or the default partition with `default_tenant=true`. A tenant-constrained credential is always scoped to its partition. The opaque cursor is bound to the normalized filter set and credential tenant scope. `agent_id` and `agent_key` are mutually exclusive; both normalize to the resolved Agent ID for cursor binding, so an equivalent cursor may resume under either spelling.  ## `ended=true` makes this a reconciliation feed  Set it and the same operation reverses into a feed: every Invocation that reached a terminal status, **oldest first by the moment it ended**, each appearing exactly once. Walk it and append by `id`.  This is the backstop for settlement. `invocation.ended` webhooks are delivered at least once, which narrows the window but does not close it: a delivery that never lands leaves a turn nobody settles, and that failure is silent — no error, just a ledger row that was never written. Reading the feed to the end is how you find out.  The default listing cannot do that job. Newest-first over current state means a turn that ends while you page moves under you, and filtering by terminal status gives you a set with no position in it. Ending order is the only order you can resume.  Start with `ended_since`, or with no position at all to begin at the oldest retained turn. Then send back `next_cursor`, which in this mode is present on every response including an empty page, so a consumer that catches up keeps its position without special-casing. Keep going while `has_more` is true; when it is false you are caught up and can wait before asking again.  `complete_through` is returned in this mode and is the instant the feed is complete to. Turns that ended after it are held back until their settling transactions are certainly visible, because a turn that appeared behind your cursor would be one you never see again. It trails the present by a bounded interval, so a consumer is always slightly behind and never wrong; it is also the number to alarm on, since a `complete_through` that stops advancing means settlement has stalled rather than that nothing ended.  A cursor carries its mode, so one from the default listing cannot resume the feed and the reverse is also refused. Erased Sessions take their Invocations with them, so a turn deleted before you read it never appears; reconcile before you erase.

        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param default_tenant: Select only the default tenant partition. Mutually exclusive with tenant_key.
        :type default_tenant: bool
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param session_id:
        :type session_id: str
        :param agent_id: Mutually exclusive with agent_key.
        :type agent_id: str
        :param agent_key: Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id.
        :type agent_key: str
        :param status: Repeat to select a union of statuses. Order and duplicates are normalized before cursor binding.
        :type status: List[InvocationStatus]
        :param parent_invocation_id: Select direct children of one Invocation. Send the literal `null` to select top-level Invocations; omit the parameter to retain the authoritative unfiltered collection. This filter is part of the opaque cursor's collection identity.
        :type parent_invocation_id: str
        :param ended: Walk the turns that ended, oldest first, instead of listing current state newest first. See the description above.
        :type ended: bool
        :param ended_since: Inclusive RFC 3339 lower bound on `ended_at`, for starting a feed that has no cursor yet. Requires `ended=true`, and is mutually exclusive with `cursor`, which already carries a position.
        :type ended_since: datetime
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

        _param = self._list_invocations_serialize(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            session_id=session_id,
            agent_id=agent_id,
            agent_key=agent_key,
            status=status,
            parent_invocation_id=parent_invocation_id,
            ended=ended,
            ended_since=ended_since,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationList",
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
    async def list_invocations_with_http_info(
        self,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        default_tenant: Annotated[Optional[StrictBool], Field(description="Select only the default tenant partition. Mutually exclusive with tenant_key.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        session_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        agent_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Mutually exclusive with agent_key.")] = None,
        agent_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id. ")] = None,
        status: Annotated[Optional[Annotated[List[InvocationStatus], Field(min_length=1)]], Field(description="Repeat to select a union of statuses. Order and duplicates are normalized before cursor binding. ")] = None,
        parent_invocation_id: Annotated[Optional[StrictStr], Field(description="Select direct children of one Invocation. Send the literal `null` to select top-level Invocations; omit the parameter to retain the authoritative unfiltered collection. This filter is part of the opaque cursor's collection identity. ")] = None,
        ended: Annotated[Optional[StrictBool], Field(description="Walk the turns that ended, oldest first, instead of listing current state newest first. See the description above. ")] = None,
        ended_since: Annotated[Optional[datetime], Field(description="Inclusive RFC 3339 lower bound on `ended_at`, for starting a feed that has no cursor yet. Requires `ended=true`, and is mutually exclusive with `cursor`, which already carries a position. ")] = None,
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
    ) -> ApiResponse[InvocationList]:
        """List authoritative Invocations, or walk the ones that ended

        Returns newest-first durable Invocation state. Exact filters combine with AND. An App credential without a tenant constraint may list all tenant partitions in that App, one named partition with `tenant_key`, or the default partition with `default_tenant=true`. A tenant-constrained credential is always scoped to its partition. The opaque cursor is bound to the normalized filter set and credential tenant scope. `agent_id` and `agent_key` are mutually exclusive; both normalize to the resolved Agent ID for cursor binding, so an equivalent cursor may resume under either spelling.  ## `ended=true` makes this a reconciliation feed  Set it and the same operation reverses into a feed: every Invocation that reached a terminal status, **oldest first by the moment it ended**, each appearing exactly once. Walk it and append by `id`.  This is the backstop for settlement. `invocation.ended` webhooks are delivered at least once, which narrows the window but does not close it: a delivery that never lands leaves a turn nobody settles, and that failure is silent — no error, just a ledger row that was never written. Reading the feed to the end is how you find out.  The default listing cannot do that job. Newest-first over current state means a turn that ends while you page moves under you, and filtering by terminal status gives you a set with no position in it. Ending order is the only order you can resume.  Start with `ended_since`, or with no position at all to begin at the oldest retained turn. Then send back `next_cursor`, which in this mode is present on every response including an empty page, so a consumer that catches up keeps its position without special-casing. Keep going while `has_more` is true; when it is false you are caught up and can wait before asking again.  `complete_through` is returned in this mode and is the instant the feed is complete to. Turns that ended after it are held back until their settling transactions are certainly visible, because a turn that appeared behind your cursor would be one you never see again. It trails the present by a bounded interval, so a consumer is always slightly behind and never wrong; it is also the number to alarm on, since a `complete_through` that stops advancing means settlement has stalled rather than that nothing ended.  A cursor carries its mode, so one from the default listing cannot resume the feed and the reverse is also refused. Erased Sessions take their Invocations with them, so a turn deleted before you read it never appears; reconcile before you erase.

        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param default_tenant: Select only the default tenant partition. Mutually exclusive with tenant_key.
        :type default_tenant: bool
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param session_id:
        :type session_id: str
        :param agent_id: Mutually exclusive with agent_key.
        :type agent_id: str
        :param agent_key: Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id.
        :type agent_key: str
        :param status: Repeat to select a union of statuses. Order and duplicates are normalized before cursor binding.
        :type status: List[InvocationStatus]
        :param parent_invocation_id: Select direct children of one Invocation. Send the literal `null` to select top-level Invocations; omit the parameter to retain the authoritative unfiltered collection. This filter is part of the opaque cursor's collection identity.
        :type parent_invocation_id: str
        :param ended: Walk the turns that ended, oldest first, instead of listing current state newest first. See the description above.
        :type ended: bool
        :param ended_since: Inclusive RFC 3339 lower bound on `ended_at`, for starting a feed that has no cursor yet. Requires `ended=true`, and is mutually exclusive with `cursor`, which already carries a position.
        :type ended_since: datetime
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

        _param = self._list_invocations_serialize(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            session_id=session_id,
            agent_id=agent_id,
            agent_key=agent_key,
            status=status,
            parent_invocation_id=parent_invocation_id,
            ended=ended,
            ended_since=ended_since,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationList",
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
    async def list_invocations_without_preload_content(
        self,
        tenant_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact non-default tenant partition reference.")] = None,
        default_tenant: Annotated[Optional[StrictBool], Field(description="Select only the default tenant partition. Mutually exclusive with tenant_key.")] = None,
        user_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned end-user reference. Filters to rows whose Session carries this label. ")] = None,
        session_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = None,
        agent_id: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Mutually exclusive with agent_key.")] = None,
        agent_key: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]], Field(description="Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id. ")] = None,
        status: Annotated[Optional[Annotated[List[InvocationStatus], Field(min_length=1)]], Field(description="Repeat to select a union of statuses. Order and duplicates are normalized before cursor binding. ")] = None,
        parent_invocation_id: Annotated[Optional[StrictStr], Field(description="Select direct children of one Invocation. Send the literal `null` to select top-level Invocations; omit the parameter to retain the authoritative unfiltered collection. This filter is part of the opaque cursor's collection identity. ")] = None,
        ended: Annotated[Optional[StrictBool], Field(description="Walk the turns that ended, oldest first, instead of listing current state newest first. See the description above. ")] = None,
        ended_since: Annotated[Optional[datetime], Field(description="Inclusive RFC 3339 lower bound on `ended_at`, for starting a feed that has no cursor yet. Requires `ended=true`, and is mutually exclusive with `cursor`, which already carries a position. ")] = None,
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
        """List authoritative Invocations, or walk the ones that ended

        Returns newest-first durable Invocation state. Exact filters combine with AND. An App credential without a tenant constraint may list all tenant partitions in that App, one named partition with `tenant_key`, or the default partition with `default_tenant=true`. A tenant-constrained credential is always scoped to its partition. The opaque cursor is bound to the normalized filter set and credential tenant scope. `agent_id` and `agent_key` are mutually exclusive; both normalize to the resolved Agent ID for cursor binding, so an equivalent cursor may resume under either spelling.  ## `ended=true` makes this a reconciliation feed  Set it and the same operation reverses into a feed: every Invocation that reached a terminal status, **oldest first by the moment it ended**, each appearing exactly once. Walk it and append by `id`.  This is the backstop for settlement. `invocation.ended` webhooks are delivered at least once, which narrows the window but does not close it: a delivery that never lands leaves a turn nobody settles, and that failure is silent — no error, just a ledger row that was never written. Reading the feed to the end is how you find out.  The default listing cannot do that job. Newest-first over current state means a turn that ends while you page moves under you, and filtering by terminal status gives you a set with no position in it. Ending order is the only order you can resume.  Start with `ended_since`, or with no position at all to begin at the oldest retained turn. Then send back `next_cursor`, which in this mode is present on every response including an empty page, so a consumer that catches up keeps its position without special-casing. Keep going while `has_more` is true; when it is false you are caught up and can wait before asking again.  `complete_through` is returned in this mode and is the instant the feed is complete to. Turns that ended after it are held back until their settling transactions are certainly visible, because a turn that appeared behind your cursor would be one you never see again. It trails the present by a bounded interval, so a consumer is always slightly behind and never wrong; it is also the number to alarm on, since a `complete_through` that stops advancing means settlement has stalled rather than that nothing ended.  A cursor carries its mode, so one from the default listing cannot resume the feed and the reverse is also refused. Erased Sessions take their Invocations with them, so a turn deleted before you read it never appears; reconcile before you erase.

        :param tenant_key: Exact non-default tenant partition reference.
        :type tenant_key: str
        :param default_tenant: Select only the default tenant partition. Mutually exclusive with tenant_key.
        :type default_tenant: bool
        :param user_key: Exact host-owned end-user reference. Filters to rows whose Session carries this label.
        :type user_key: str
        :param session_id:
        :type session_id: str
        :param agent_id: Mutually exclusive with agent_key.
        :type agent_id: str
        :param agent_key: Exact host-owned Agent key. On Session and Invocation lists this is mutually exclusive with agent_id.
        :type agent_key: str
        :param status: Repeat to select a union of statuses. Order and duplicates are normalized before cursor binding.
        :type status: List[InvocationStatus]
        :param parent_invocation_id: Select direct children of one Invocation. Send the literal `null` to select top-level Invocations; omit the parameter to retain the authoritative unfiltered collection. This filter is part of the opaque cursor's collection identity.
        :type parent_invocation_id: str
        :param ended: Walk the turns that ended, oldest first, instead of listing current state newest first. See the description above.
        :type ended: bool
        :param ended_since: Inclusive RFC 3339 lower bound on `ended_at`, for starting a feed that has no cursor yet. Requires `ended=true`, and is mutually exclusive with `cursor`, which already carries a position.
        :type ended_since: datetime
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

        _param = self._list_invocations_serialize(
            tenant_key=tenant_key,
            default_tenant=default_tenant,
            user_key=user_key,
            session_id=session_id,
            agent_id=agent_id,
            agent_key=agent_key,
            status=status,
            parent_invocation_id=parent_invocation_id,
            ended=ended,
            ended_since=ended_since,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "InvocationList",
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


    def _list_invocations_serialize(
        self,
        tenant_key,
        default_tenant,
        user_key,
        session_id,
        agent_id,
        agent_key,
        status,
        parent_invocation_id,
        ended,
        ended_since,
        cursor,
        limit,
        _request_auth,
        _content_type,
        _headers,
        _host_index,
    ) -> RequestSerialized:

        _host = None

        _collection_formats: Dict[str, str] = {
            'status': 'multi',
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

        if session_id is not None:

            _query_params.append(('session_id', session_id))

        if agent_id is not None:

            _query_params.append(('agent_id', agent_id))

        if agent_key is not None:

            _query_params.append(('agent_key', agent_key))

        if status is not None:

            _query_params.append(('status', status))

        if parent_invocation_id is not None:

            _query_params.append(('parent_invocation_id', parent_invocation_id))

        if ended is not None:

            _query_params.append(('ended', ended))

        if ended_since is not None:
            if isinstance(ended_since, datetime):
                _query_params.append(
                    (
                        'ended_since',
                        ended_since.strftime(
                            self.api_client.configuration.datetime_format
                        )
                    )
                )
            else:
                _query_params.append(('ended_since', ended_since))

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
            resource_path='/v1/invocations',
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
    async def list_nudges(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        status: Annotated[Optional[NudgeStatus], Field(description="Restrict to one status.")] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=100, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 20.")] = None,
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
    ) -> NudgeList:
        """List Nudges for an Invocation

        Lists the direction you have sent to this turn with `/nudges`, in the order the turn will pick it up. Entries stay listed after they are used or missed, so you can answer \"what did the user say, and did the model ever see it?\"  Check `status` on each entry: `drained` means the turn used it, `expired` means the turn ended first, `cancelled` means you withdrew it.

        :param invocation_id: (required)
        :type invocation_id: str
        :param status: Restrict to one status.
        :type status: NudgeStatus
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 20.
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

        _param = self._list_nudges_serialize(
            invocation_id=invocation_id,
            status=status,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "NudgeList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def list_nudges_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        status: Annotated[Optional[NudgeStatus], Field(description="Restrict to one status.")] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=100, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 20.")] = None,
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
    ) -> ApiResponse[NudgeList]:
        """List Nudges for an Invocation

        Lists the direction you have sent to this turn with `/nudges`, in the order the turn will pick it up. Entries stay listed after they are used or missed, so you can answer \"what did the user say, and did the model ever see it?\"  Check `status` on each entry: `drained` means the turn used it, `expired` means the turn ended first, `cancelled` means you withdrew it.

        :param invocation_id: (required)
        :type invocation_id: str
        :param status: Restrict to one status.
        :type status: NudgeStatus
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 20.
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

        _param = self._list_nudges_serialize(
            invocation_id=invocation_id,
            status=status,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "NudgeList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
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
    async def list_nudges_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        status: Annotated[Optional[NudgeStatus], Field(description="Restrict to one status.")] = None,
        cursor: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="Opaque cursor returned by the same operation and filter set.")] = None,
        limit: Annotated[Optional[Annotated[int, Field(le=100, strict=True, ge=1)]], Field(description="Maximum items in this page. Defaults to 20.")] = None,
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
        """List Nudges for an Invocation

        Lists the direction you have sent to this turn with `/nudges`, in the order the turn will pick it up. Entries stay listed after they are used or missed, so you can answer \"what did the user say, and did the model ever see it?\"  Check `status` on each entry: `drained` means the turn used it, `expired` means the turn ended first, `cancelled` means you withdrew it.

        :param invocation_id: (required)
        :type invocation_id: str
        :param status: Restrict to one status.
        :type status: NudgeStatus
        :param cursor: Opaque cursor returned by the same operation and filter set.
        :type cursor: str
        :param limit: Maximum items in this page. Defaults to 20.
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

        _param = self._list_nudges_serialize(
            invocation_id=invocation_id,
            status=status,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "NudgeList",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_nudges_serialize(
        self,
        invocation_id,
        status,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
        # process the query parameters
        if status is not None:

            _query_params.append(('status', status.value))

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
            resource_path='/v1/invocations/{invocation_id}/nudges',
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
    async def list_tool_calls(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ToolCallList:
        """Page through durable ToolCall execution records

        Returns ToolCalls in execution discovery order. Every execution mode is included. The records contain lifecycle and timing facts only. Tool inputs and results remain in the canonical Session transcript.  Callback records include a delivery object. Its terminal outcome, attempt count, and last HTTP status remain available after the bounded delivery transport row is pruned. These records use the same authentication, tenant scope, Session constraint, and nondisclosing not_found behavior as the Invocation read. Deleting the Session deletes these records with the rest of its subtree.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._list_tool_calls_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ToolCallList",
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
    async def list_tool_calls_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
    ) -> ApiResponse[ToolCallList]:
        """Page through durable ToolCall execution records

        Returns ToolCalls in execution discovery order. Every execution mode is included. The records contain lifecycle and timing facts only. Tool inputs and results remain in the canonical Session transcript.  Callback records include a delivery object. Its terminal outcome, attempt count, and last HTTP status remain available after the bounded delivery transport row is pruned. These records use the same authentication, tenant scope, Session constraint, and nondisclosing not_found behavior as the Invocation read. Deleting the Session deletes these records with the rest of its subtree.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._list_tool_calls_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ToolCallList",
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
    async def list_tool_calls_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
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
        """Page through durable ToolCall execution records

        Returns ToolCalls in execution discovery order. Every execution mode is included. The records contain lifecycle and timing facts only. Tool inputs and results remain in the canonical Session transcript.  Callback records include a delivery object. Its terminal outcome, attempt count, and last HTTP status remain available after the bounded delivery transport row is pruned. These records use the same authentication, tenant scope, Session constraint, and nondisclosing not_found behavior as the Invocation read. Deleting the Session deletes these records with the rest of its subtree.

        :param invocation_id: (required)
        :type invocation_id: str
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

        _param = self._list_tool_calls_serialize(
            invocation_id=invocation_id,
            cursor=cursor,
            limit=limit,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ToolCallList",
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


    def _list_tool_calls_serialize(
        self,
        invocation_id,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
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
            resource_path='/v1/invocations/{invocation_id}/tool-calls',
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
    async def resume_invocation(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        resume_invocation_request: ResumeInvocationRequest,
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
    ) -> Invocation:
        """Raise a held Invocation's limit and continue it

        Continues a turn on `budget_hold` because one of its own consumption limits ran out. Send `limits` containing only the limit that ran out, raised above both its old value and what the turn has already used, and still within what your installation allows.  If the turn is held because the tenant ran out of credits rather than on a limit of its own, allocate credits to that account instead — this endpoint refuses it, and funding the account continues the turn on its own. Deadlines never put a turn on budget hold, so they never bring you here.

        :param invocation_id: (required)
        :type invocation_id: str
        :param resume_invocation_request: (required)
        :type resume_invocation_request: ResumeInvocationRequest
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

        _param = self._resume_invocation_serialize(
            invocation_id=invocation_id,
            resume_invocation_request=resume_invocation_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "Invocation",
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
    async def resume_invocation_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        resume_invocation_request: ResumeInvocationRequest,
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
    ) -> ApiResponse[Invocation]:
        """Raise a held Invocation's limit and continue it

        Continues a turn on `budget_hold` because one of its own consumption limits ran out. Send `limits` containing only the limit that ran out, raised above both its old value and what the turn has already used, and still within what your installation allows.  If the turn is held because the tenant ran out of credits rather than on a limit of its own, allocate credits to that account instead — this endpoint refuses it, and funding the account continues the turn on its own. Deadlines never put a turn on budget hold, so they never bring you here.

        :param invocation_id: (required)
        :type invocation_id: str
        :param resume_invocation_request: (required)
        :type resume_invocation_request: ResumeInvocationRequest
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

        _param = self._resume_invocation_serialize(
            invocation_id=invocation_id,
            resume_invocation_request=resume_invocation_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "Invocation",
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
    async def resume_invocation_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        resume_invocation_request: ResumeInvocationRequest,
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
        """Raise a held Invocation's limit and continue it

        Continues a turn on `budget_hold` because one of its own consumption limits ran out. Send `limits` containing only the limit that ran out, raised above both its old value and what the turn has already used, and still within what your installation allows.  If the turn is held because the tenant ran out of credits rather than on a limit of its own, allocate credits to that account instead — this endpoint refuses it, and funding the account continues the turn on its own. Deadlines never put a turn on budget hold, so they never bring you here.

        :param invocation_id: (required)
        :type invocation_id: str
        :param resume_invocation_request: (required)
        :type resume_invocation_request: ResumeInvocationRequest
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

        _param = self._resume_invocation_serialize(
            invocation_id=invocation_id,
            resume_invocation_request=resume_invocation_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "Invocation",
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


    def _resume_invocation_serialize(
        self,
        invocation_id,
        resume_invocation_request,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
        # process the query parameters
        # process the header parameters
        # process the form parameters
        # process the body parameter
        if resume_invocation_request is not None:
            _body_params = resume_invocation_request


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
            resource_path='/v1/invocations/{invocation_id}/resume',
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
    async def submit_host_tool_results(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        submit_host_tool_results_request: SubmitHostToolResultsRequest,
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
    ) -> SubmitHostToolResultsResponse:
        """Submit durable results for pending host and callback ToolCalls

        Atomically accepts one bounded batch for a waiting Invocation. The first committed result for each ToolCall wins. An equal replay is acknowledged as deduplicated; a changed replay conflicts. Partial batches leave the Invocation waiting. Closing the final pending call queues the same Invocation and its successor dispatch before returning `202`.  This command accepts only host- or callback-mode calls owned by the path Invocation and authenticated tenant scope. It is not a generic Session append endpoint. The body is limited to 1 MiB; each result content value is valid JSON limited to 256 KiB and 32 nesting levels.  `content` accepts any JSON value and the stored transcript retains it verbatim. Before a result reaches the model, a string or an array of content blocks passes through unchanged; any other value is serialized to its compact JSON text and sent as a string, so the model sees the same bytes a host that pre-stringifies would send.

        :param invocation_id: (required)
        :type invocation_id: str
        :param submit_host_tool_results_request: (required)
        :type submit_host_tool_results_request: SubmitHostToolResultsRequest
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

        _param = self._submit_host_tool_results_serialize(
            invocation_id=invocation_id,
            submit_host_tool_results_request=submit_host_tool_results_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "SubmitHostToolResultsResponse",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
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
    async def submit_host_tool_results_with_http_info(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        submit_host_tool_results_request: SubmitHostToolResultsRequest,
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
    ) -> ApiResponse[SubmitHostToolResultsResponse]:
        """Submit durable results for pending host and callback ToolCalls

        Atomically accepts one bounded batch for a waiting Invocation. The first committed result for each ToolCall wins. An equal replay is acknowledged as deduplicated; a changed replay conflicts. Partial batches leave the Invocation waiting. Closing the final pending call queues the same Invocation and its successor dispatch before returning `202`.  This command accepts only host- or callback-mode calls owned by the path Invocation and authenticated tenant scope. It is not a generic Session append endpoint. The body is limited to 1 MiB; each result content value is valid JSON limited to 256 KiB and 32 nesting levels.  `content` accepts any JSON value and the stored transcript retains it verbatim. Before a result reaches the model, a string or an array of content blocks passes through unchanged; any other value is serialized to its compact JSON text and sent as a string, so the model sees the same bytes a host that pre-stringifies would send.

        :param invocation_id: (required)
        :type invocation_id: str
        :param submit_host_tool_results_request: (required)
        :type submit_host_tool_results_request: SubmitHostToolResultsRequest
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

        _param = self._submit_host_tool_results_serialize(
            invocation_id=invocation_id,
            submit_host_tool_results_request=submit_host_tool_results_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "SubmitHostToolResultsResponse",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
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
    async def submit_host_tool_results_without_preload_content(
        self,
        invocation_id: Annotated[str, Field(min_length=1, strict=True)],
        submit_host_tool_results_request: SubmitHostToolResultsRequest,
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
        """Submit durable results for pending host and callback ToolCalls

        Atomically accepts one bounded batch for a waiting Invocation. The first committed result for each ToolCall wins. An equal replay is acknowledged as deduplicated; a changed replay conflicts. Partial batches leave the Invocation waiting. Closing the final pending call queues the same Invocation and its successor dispatch before returning `202`.  This command accepts only host- or callback-mode calls owned by the path Invocation and authenticated tenant scope. It is not a generic Session append endpoint. The body is limited to 1 MiB; each result content value is valid JSON limited to 256 KiB and 32 nesting levels.  `content` accepts any JSON value and the stored transcript retains it verbatim. Before a result reaches the model, a string or an array of content blocks passes through unchanged; any other value is serialized to its compact JSON text and sent as a string, so the model sees the same bytes a host that pre-stringifies would send.

        :param invocation_id: (required)
        :type invocation_id: str
        :param submit_host_tool_results_request: (required)
        :type submit_host_tool_results_request: SubmitHostToolResultsRequest
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

        _param = self._submit_host_tool_results_serialize(
            invocation_id=invocation_id,
            submit_host_tool_results_request=submit_host_tool_results_request,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '202': "SubmitHostToolResultsResponse",
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '403': "ErrorResponse",
            '404': "ErrorResponse",
            '409': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _submit_host_tool_results_serialize(
        self,
        invocation_id,
        submit_host_tool_results_request,
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
        if invocation_id is not None:
            _path_params['invocation_id'] = invocation_id
        # process the query parameters
        # process the header parameters
        # process the form parameters
        # process the body parameter
        if submit_host_tool_results_request is not None:
            _body_params = submit_host_tool_results_request


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
            resource_path='/v1/invocations/{invocation_id}/tool-results',
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
