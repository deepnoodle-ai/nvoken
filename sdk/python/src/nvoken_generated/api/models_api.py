"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for so you can filter on it later, and it is not a permission boundary. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource's name, so `session_id` on an Invocation is the Session it belongs to. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken's identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


import warnings
from pydantic import validate_call, Field, StrictFloat, StrictStr, StrictInt
from typing import Any, Dict, List, Optional, Tuple, Union
from typing_extensions import Annotated

from pydantic import Field, StrictBool, field_validator
from typing import Optional
from typing_extensions import Annotated
from nvoken_generated.models.model_descriptor import ModelDescriptor
from nvoken_generated.models.model_list import ModelList

from nvoken_generated.api_client import ApiClient, RequestSerialized
from nvoken_generated.api_response import ApiResponse
from nvoken_generated.rest import RESTResponseType


class ModelsApi:
    """NOTE: This class is auto generated by OpenAPI Generator
    Ref: https://openapi-generator.tech

    Do not edit the class manually.
    """

    def __init__(self, api_client=None) -> None:
        if api_client is None:
            api_client = ApiClient.get_default()
        self.api_client = api_client


    @validate_call
    async def get_model(
        self,
        provider: Annotated[str, Field(strict=True, description="Installed canonical model provider.")],
        model_id: Annotated[str, Field(min_length=1, strict=True, max_length=255, description="Exact model ID used in `model.id`. Encode the complete value as one path segment, including `/`, reserved characters, and Unicode. ")],
        if_none_match: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="ETag from a prior response for this exact representation.")] = None,
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
    ) -> ModelDescriptor:
        """Inspect one exact provider/model selection

        Returns a descriptor for any valid installed provider and exact model ID, including IDs outside the curated catalog. `cataloged` distinguishes maintained metadata from tolerant inspection. Pricing is the standard local USD estimate used by nvoken's estimated-cost guardrail, not provider billing or proof of account access.

        :param provider: Installed canonical model provider. (required)
        :type provider: str
        :param model_id: Exact model ID used in `model.id`. Encode the complete value as one path segment, including `/`, reserved characters, and Unicode.  (required)
        :type model_id: str
        :param if_none_match: ETag from a prior response for this exact representation.
        :type if_none_match: str
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

        _param = self._get_model_serialize(
            provider=provider,
            model_id=model_id,
            if_none_match=if_none_match,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ModelDescriptor",
            '304': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
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
    async def get_model_with_http_info(
        self,
        provider: Annotated[str, Field(strict=True, description="Installed canonical model provider.")],
        model_id: Annotated[str, Field(min_length=1, strict=True, max_length=255, description="Exact model ID used in `model.id`. Encode the complete value as one path segment, including `/`, reserved characters, and Unicode. ")],
        if_none_match: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="ETag from a prior response for this exact representation.")] = None,
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
    ) -> ApiResponse[ModelDescriptor]:
        """Inspect one exact provider/model selection

        Returns a descriptor for any valid installed provider and exact model ID, including IDs outside the curated catalog. `cataloged` distinguishes maintained metadata from tolerant inspection. Pricing is the standard local USD estimate used by nvoken's estimated-cost guardrail, not provider billing or proof of account access.

        :param provider: Installed canonical model provider. (required)
        :type provider: str
        :param model_id: Exact model ID used in `model.id`. Encode the complete value as one path segment, including `/`, reserved characters, and Unicode.  (required)
        :type model_id: str
        :param if_none_match: ETag from a prior response for this exact representation.
        :type if_none_match: str
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

        _param = self._get_model_serialize(
            provider=provider,
            model_id=model_id,
            if_none_match=if_none_match,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ModelDescriptor",
            '304': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
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
    async def get_model_without_preload_content(
        self,
        provider: Annotated[str, Field(strict=True, description="Installed canonical model provider.")],
        model_id: Annotated[str, Field(min_length=1, strict=True, max_length=255, description="Exact model ID used in `model.id`. Encode the complete value as one path segment, including `/`, reserved characters, and Unicode. ")],
        if_none_match: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="ETag from a prior response for this exact representation.")] = None,
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
        """Inspect one exact provider/model selection

        Returns a descriptor for any valid installed provider and exact model ID, including IDs outside the curated catalog. `cataloged` distinguishes maintained metadata from tolerant inspection. Pricing is the standard local USD estimate used by nvoken's estimated-cost guardrail, not provider billing or proof of account access.

        :param provider: Installed canonical model provider. (required)
        :type provider: str
        :param model_id: Exact model ID used in `model.id`. Encode the complete value as one path segment, including `/`, reserved characters, and Unicode.  (required)
        :type model_id: str
        :param if_none_match: ETag from a prior response for this exact representation.
        :type if_none_match: str
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

        _param = self._get_model_serialize(
            provider=provider,
            model_id=model_id,
            if_none_match=if_none_match,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ModelDescriptor",
            '304': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _get_model_serialize(
        self,
        provider,
        model_id,
        if_none_match,
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
        if provider is not None:
            _path_params['provider'] = provider
        if model_id is not None:
            _path_params['model_id'] = model_id
        # process the query parameters
        # process the header parameters
        if if_none_match is not None:
            _header_params['If-None-Match'] = if_none_match
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
            resource_path='/v1/models/{provider}/{model_id}',
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
    async def list_models(
        self,
        provider: Annotated[Optional[Annotated[str, Field(strict=True)]], Field(description="Limit results to one installed canonical provider.")] = None,
        include_deprecated: Annotated[Optional[StrictBool], Field(description="Include catalog entries nvoken marks as deprecated.")] = None,
        if_none_match: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="ETag from a prior response for this exact representation.")] = None,
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
    ) -> ModelList:
        """List nvoken's curated model catalog

        Returns the complete bounded set of text-generation models nvoken intentionally advertises. Catalog membership means nvoken maintains metadata for the exact provider/model selection. It does not prove that the caller's provider account, region, or selected provider key can access the model. Ordering is deterministic but has no semantic meaning.

        :param provider: Limit results to one installed canonical provider.
        :type provider: str
        :param include_deprecated: Include catalog entries nvoken marks as deprecated.
        :type include_deprecated: bool
        :param if_none_match: ETag from a prior response for this exact representation.
        :type if_none_match: str
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

        _param = self._list_models_serialize(
            provider=provider,
            include_deprecated=include_deprecated,
            if_none_match=if_none_match,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ModelList",
            '304': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
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
    async def list_models_with_http_info(
        self,
        provider: Annotated[Optional[Annotated[str, Field(strict=True)]], Field(description="Limit results to one installed canonical provider.")] = None,
        include_deprecated: Annotated[Optional[StrictBool], Field(description="Include catalog entries nvoken marks as deprecated.")] = None,
        if_none_match: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="ETag from a prior response for this exact representation.")] = None,
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
    ) -> ApiResponse[ModelList]:
        """List nvoken's curated model catalog

        Returns the complete bounded set of text-generation models nvoken intentionally advertises. Catalog membership means nvoken maintains metadata for the exact provider/model selection. It does not prove that the caller's provider account, region, or selected provider key can access the model. Ordering is deterministic but has no semantic meaning.

        :param provider: Limit results to one installed canonical provider.
        :type provider: str
        :param include_deprecated: Include catalog entries nvoken marks as deprecated.
        :type include_deprecated: bool
        :param if_none_match: ETag from a prior response for this exact representation.
        :type if_none_match: str
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

        _param = self._list_models_serialize(
            provider=provider,
            include_deprecated=include_deprecated,
            if_none_match=if_none_match,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ModelList",
            '304': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
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
    async def list_models_without_preload_content(
        self,
        provider: Annotated[Optional[Annotated[str, Field(strict=True)]], Field(description="Limit results to one installed canonical provider.")] = None,
        include_deprecated: Annotated[Optional[StrictBool], Field(description="Include catalog entries nvoken marks as deprecated.")] = None,
        if_none_match: Annotated[Optional[Annotated[str, Field(min_length=1, strict=True)]], Field(description="ETag from a prior response for this exact representation.")] = None,
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
        """List nvoken's curated model catalog

        Returns the complete bounded set of text-generation models nvoken intentionally advertises. Catalog membership means nvoken maintains metadata for the exact provider/model selection. It does not prove that the caller's provider account, region, or selected provider key can access the model. Ordering is deterministic but has no semantic meaning.

        :param provider: Limit results to one installed canonical provider.
        :type provider: str
        :param include_deprecated: Include catalog entries nvoken marks as deprecated.
        :type include_deprecated: bool
        :param if_none_match: ETag from a prior response for this exact representation.
        :type if_none_match: str
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

        _param = self._list_models_serialize(
            provider=provider,
            include_deprecated=include_deprecated,
            if_none_match=if_none_match,
            _request_auth=_request_auth,
            _content_type=_content_type,
            _headers=_headers,
            _host_index=_host_index
        )

        _response_types_map: Dict[str, Optional[str]] = {
            '200': "ModelList",
            '304': None,
            '400': "ErrorResponse",
            '401': "ErrorResponse",
            '429': "ErrorResponse",
            '500': "ErrorResponse",
            '503': "ErrorResponse",
        }
        response_data = await self.api_client.call_api(
            *_param,
            _request_timeout=_request_timeout
        )
        return response_data.response


    def _list_models_serialize(
        self,
        provider,
        include_deprecated,
        if_none_match,
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
        if provider is not None:

            _query_params.append(('provider', provider))

        if include_deprecated is not None:

            _query_params.append(('include_deprecated', include_deprecated))

        # process the header parameters
        if if_none_match is not None:
            _header_params['If-None-Match'] = if_none_match
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
            resource_path='/v1/models',
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
