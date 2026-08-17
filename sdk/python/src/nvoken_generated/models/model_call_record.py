# coding: utf-8

"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `stream.end` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `stream.end` never speaks about turns. It says this connection is closing and nothing more, so a client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `stream.end` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


from __future__ import annotations
import pprint
import re  # noqa: F401
import json

from datetime import datetime
from pydantic import BaseModel, ConfigDict, Field, StrictStr, field_validator
from typing import Any, ClassVar, Dict, Optional
from typing_extensions import Annotated
from nvoken_generated.models.authentication_method import AuthenticationMethod
from nvoken_generated.models.model_call_fact_status import ModelCallFactStatus
from nvoken_generated.models.model_call_kind import ModelCallKind
from nvoken_generated.models.money import Money
from nvoken_generated.models.provider_key_source import ProviderKeySource
from typing import Optional, Set
from typing_extensions import Self
from pydantic_core import to_jsonable_python

class ModelCallRecord(BaseModel):
    """
    ModelCallRecord
    """ # noqa: E501
    id: StrictStr = Field(description="Opaque model-call fact ID with the mcall_ prefix.")
    invocation_id: Optional[StrictStr]
    session_id: Optional[StrictStr]
    app_id: Annotated[str, Field(min_length=1, strict=True)] = Field(description="Opaque identifier with the public `app_` prefix. Treat the body as opaque.")
    tenant_key: Optional[StrictStr]
    user_key: Optional[StrictStr] = Field(description="Null after user erasure.")
    agent_id: Optional[StrictStr]
    credential_family_id: Optional[StrictStr]
    authentication_method: Optional[AuthenticationMethod]
    provider_key_source: ProviderKeySource
    provider_key_id: Optional[StrictStr]
    provider_key_version_id: Optional[StrictStr]
    call_kind: ModelCallKind
    call_ordinal: Annotated[int, Field(strict=True, ge=1)]
    lease_attempt: Annotated[int, Field(strict=True, ge=1)]
    provider_attempt_ordinal: Annotated[int, Field(strict=True, ge=1)]
    requested_provider: StrictStr
    requested_model: StrictStr
    served_provider: Optional[StrictStr]
    served_model: Optional[StrictStr]
    status: ModelCallFactStatus
    outcome: Optional[StrictStr]
    failure_class: Optional[StrictStr]
    input_tokens: Optional[Annotated[int, Field(strict=True, ge=0)]]
    output_tokens: Optional[Annotated[int, Field(strict=True, ge=0)]]
    cache_creation_input_tokens: Optional[Annotated[int, Field(strict=True, ge=0)]]
    cache_read_input_tokens: Optional[Annotated[int, Field(strict=True, ge=0)]]
    reasoning_tokens: Optional[Annotated[int, Field(strict=True, ge=0)]]
    model_cost: Optional[Money]
    cost_coverage: StrictStr
    max_cost_at_risk: Optional[Money]
    pricing_version: Optional[StrictStr]
    created_at: datetime
    started_at: Optional[datetime]
    first_output_at: Optional[datetime]
    settled_at: Optional[datetime]
    __properties: ClassVar[List[str]] = ["id", "invocation_id", "session_id", "app_id", "tenant_key", "user_key", "agent_id", "credential_family_id", "authentication_method", "provider_key_source", "provider_key_id", "provider_key_version_id", "call_kind", "call_ordinal", "lease_attempt", "provider_attempt_ordinal", "requested_provider", "requested_model", "served_provider", "served_model", "status", "outcome", "failure_class", "input_tokens", "output_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "reasoning_tokens", "model_cost", "cost_coverage", "max_cost_at_risk", "pricing_version", "created_at", "started_at", "first_output_at", "settled_at"]

    @field_validator('outcome')
    def outcome_validate_enum(cls, value):
        """Validates the enum"""
        if value is None:
            return value

        if value not in set(['succeeded', 'failed', 'cancelled']):
            raise ValueError("must be one of enum values ('succeeded', 'failed', 'cancelled')")
        return value

    @field_validator('cost_coverage')
    def cost_coverage_validate_enum(cls, value):
        """Validates the enum"""
        if value not in set(['complete', 'none', 'not_applicable']):
            raise ValueError("must be one of enum values ('complete', 'none', 'not_applicable')")
        return value

    model_config = ConfigDict(
        validate_by_name=True,
        validate_by_alias=True,
        validate_assignment=True,
        protected_namespaces=(),
    )


    def to_str(self) -> str:
        """Returns the string representation of the model using alias"""
        return pprint.pformat(self.model_dump(by_alias=True))

    def to_json(self) -> str:
        """Returns the JSON representation of the model using alias"""
        return json.dumps(to_jsonable_python(self.to_dict()))

    @classmethod
    def from_json(cls, json_str: str) -> Optional[Self]:
        """Create an instance of ModelCallRecord from a JSON string"""
        return cls.from_dict(json.loads(json_str))

    def to_dict(self) -> Dict[str, Any]:
        """Return the dictionary representation of the model using alias.

        This has the following differences from calling pydantic's
        `self.model_dump(by_alias=True)`:

        * `None` is only added to the output dict for nullable fields that
          were set at model initialization. Other fields with value `None`
          are ignored.
        """
        excluded_fields: Set[str] = set([
        ])

        _dict = self.model_dump(
            by_alias=True,
            exclude=excluded_fields,
            exclude_none=True,
        )
        # override the default output from pydantic by calling `to_dict()` of model_cost
        if self.model_cost:
            _dict['model_cost'] = self.model_cost.to_dict()
        # override the default output from pydantic by calling `to_dict()` of max_cost_at_risk
        if self.max_cost_at_risk:
            _dict['max_cost_at_risk'] = self.max_cost_at_risk.to_dict()
        # set to None if invocation_id (nullable) is None
        # and model_fields_set contains the field
        if self.invocation_id is None and "invocation_id" in self.model_fields_set:
            _dict['invocation_id'] = None

        # set to None if session_id (nullable) is None
        # and model_fields_set contains the field
        if self.session_id is None and "session_id" in self.model_fields_set:
            _dict['session_id'] = None

        # set to None if tenant_key (nullable) is None
        # and model_fields_set contains the field
        if self.tenant_key is None and "tenant_key" in self.model_fields_set:
            _dict['tenant_key'] = None

        # set to None if user_key (nullable) is None
        # and model_fields_set contains the field
        if self.user_key is None and "user_key" in self.model_fields_set:
            _dict['user_key'] = None

        # set to None if agent_id (nullable) is None
        # and model_fields_set contains the field
        if self.agent_id is None and "agent_id" in self.model_fields_set:
            _dict['agent_id'] = None

        # set to None if credential_family_id (nullable) is None
        # and model_fields_set contains the field
        if self.credential_family_id is None and "credential_family_id" in self.model_fields_set:
            _dict['credential_family_id'] = None

        # set to None if authentication_method (nullable) is None
        # and model_fields_set contains the field
        if self.authentication_method is None and "authentication_method" in self.model_fields_set:
            _dict['authentication_method'] = None

        # set to None if provider_key_id (nullable) is None
        # and model_fields_set contains the field
        if self.provider_key_id is None and "provider_key_id" in self.model_fields_set:
            _dict['provider_key_id'] = None

        # set to None if provider_key_version_id (nullable) is None
        # and model_fields_set contains the field
        if self.provider_key_version_id is None and "provider_key_version_id" in self.model_fields_set:
            _dict['provider_key_version_id'] = None

        # set to None if served_provider (nullable) is None
        # and model_fields_set contains the field
        if self.served_provider is None and "served_provider" in self.model_fields_set:
            _dict['served_provider'] = None

        # set to None if served_model (nullable) is None
        # and model_fields_set contains the field
        if self.served_model is None and "served_model" in self.model_fields_set:
            _dict['served_model'] = None

        # set to None if outcome (nullable) is None
        # and model_fields_set contains the field
        if self.outcome is None and "outcome" in self.model_fields_set:
            _dict['outcome'] = None

        # set to None if failure_class (nullable) is None
        # and model_fields_set contains the field
        if self.failure_class is None and "failure_class" in self.model_fields_set:
            _dict['failure_class'] = None

        # set to None if input_tokens (nullable) is None
        # and model_fields_set contains the field
        if self.input_tokens is None and "input_tokens" in self.model_fields_set:
            _dict['input_tokens'] = None

        # set to None if output_tokens (nullable) is None
        # and model_fields_set contains the field
        if self.output_tokens is None and "output_tokens" in self.model_fields_set:
            _dict['output_tokens'] = None

        # set to None if cache_creation_input_tokens (nullable) is None
        # and model_fields_set contains the field
        if self.cache_creation_input_tokens is None and "cache_creation_input_tokens" in self.model_fields_set:
            _dict['cache_creation_input_tokens'] = None

        # set to None if cache_read_input_tokens (nullable) is None
        # and model_fields_set contains the field
        if self.cache_read_input_tokens is None and "cache_read_input_tokens" in self.model_fields_set:
            _dict['cache_read_input_tokens'] = None

        # set to None if reasoning_tokens (nullable) is None
        # and model_fields_set contains the field
        if self.reasoning_tokens is None and "reasoning_tokens" in self.model_fields_set:
            _dict['reasoning_tokens'] = None

        # set to None if model_cost (nullable) is None
        # and model_fields_set contains the field
        if self.model_cost is None and "model_cost" in self.model_fields_set:
            _dict['model_cost'] = None

        # set to None if max_cost_at_risk (nullable) is None
        # and model_fields_set contains the field
        if self.max_cost_at_risk is None and "max_cost_at_risk" in self.model_fields_set:
            _dict['max_cost_at_risk'] = None

        # set to None if pricing_version (nullable) is None
        # and model_fields_set contains the field
        if self.pricing_version is None and "pricing_version" in self.model_fields_set:
            _dict['pricing_version'] = None

        # set to None if started_at (nullable) is None
        # and model_fields_set contains the field
        if self.started_at is None and "started_at" in self.model_fields_set:
            _dict['started_at'] = None

        # set to None if first_output_at (nullable) is None
        # and model_fields_set contains the field
        if self.first_output_at is None and "first_output_at" in self.model_fields_set:
            _dict['first_output_at'] = None

        # set to None if settled_at (nullable) is None
        # and model_fields_set contains the field
        if self.settled_at is None and "settled_at" in self.model_fields_set:
            _dict['settled_at'] = None

        return _dict

    @classmethod
    def from_dict(cls, obj: Optional[Dict[str, Any]]) -> Optional[Self]:
        """Create an instance of ModelCallRecord from a dict"""
        if obj is None:
            return None

        if not isinstance(obj, dict):
            return cls.model_validate(obj)

        _obj = cls.model_validate({
            "id": obj.get("id"),
            "invocation_id": obj.get("invocation_id"),
            "session_id": obj.get("session_id"),
            "app_id": obj.get("app_id"),
            "tenant_key": obj.get("tenant_key"),
            "user_key": obj.get("user_key"),
            "agent_id": obj.get("agent_id"),
            "credential_family_id": obj.get("credential_family_id"),
            "authentication_method": obj.get("authentication_method"),
            "provider_key_source": obj.get("provider_key_source"),
            "provider_key_id": obj.get("provider_key_id"),
            "provider_key_version_id": obj.get("provider_key_version_id"),
            "call_kind": obj.get("call_kind"),
            "call_ordinal": obj.get("call_ordinal"),
            "lease_attempt": obj.get("lease_attempt"),
            "provider_attempt_ordinal": obj.get("provider_attempt_ordinal"),
            "requested_provider": obj.get("requested_provider"),
            "requested_model": obj.get("requested_model"),
            "served_provider": obj.get("served_provider"),
            "served_model": obj.get("served_model"),
            "status": obj.get("status"),
            "outcome": obj.get("outcome"),
            "failure_class": obj.get("failure_class"),
            "input_tokens": obj.get("input_tokens"),
            "output_tokens": obj.get("output_tokens"),
            "cache_creation_input_tokens": obj.get("cache_creation_input_tokens"),
            "cache_read_input_tokens": obj.get("cache_read_input_tokens"),
            "reasoning_tokens": obj.get("reasoning_tokens"),
            "model_cost": Money.from_dict(obj["model_cost"]) if obj.get("model_cost") is not None else None,
            "cost_coverage": obj.get("cost_coverage"),
            "max_cost_at_risk": Money.from_dict(obj["max_cost_at_risk"]) if obj.get("max_cost_at_risk") is not None else None,
            "pricing_version": obj.get("pricing_version"),
            "created_at": obj.get("created_at"),
            "started_at": obj.get("started_at"),
            "first_output_at": obj.get("first_output_at"),
            "settled_at": obj.get("settled_at")
        })
        return _obj
