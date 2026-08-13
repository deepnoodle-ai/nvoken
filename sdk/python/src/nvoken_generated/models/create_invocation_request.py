# coding: utf-8

"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or   read `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, client-safe projections, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  Two streams carry the same frames. `GET /v1/invocations/{invocation_id}/stream` follows one turn and ends when that turn settles. `GET /v1/sessions/{session_id}/transcript/stream` follows every turn in a Session, and is the surface to use for a conversation. `POST /v1/invocations` with `Accept: text/event-stream` admits and streams one turn inline.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  The Invocation stream's saved frames are `invocation.accepted`, `invocation.update`, and `invocation.result`. The Session stream's only saved frame is `transcript.update`. Every other frame on either stream is live.  ### Resuming and finishing  The resume position has four spellings and one value: the SSE `id` line, `resume_cursor` inside a frame payload, the `cursor` query parameter, and the `Last-Event-ID` header. Send it back as `cursor` or as `Last-Event-ID`; `cursor` wins when a request carries both. Cursors are Session-scoped on both streams, so a position taken from one stream resumes the other.  Reconnecting to a turn that has already settled always yields `invocation.result` followed by `stream.end` with reason `terminal`, at any cursor. Both are valid signals that a turn is over, and a client may exit on either.  `invocation.accepted` is emitted only by the inline `POST` path. The `GET` stream never sends it, so a client that admits separately never sees it. The nvoken SDKs synthesize an equivalent locally so their callers see the same first event either way.  An `invocation.update` never carries a terminal status. Terminal state arrives as `invocation.result` and nowhere else on that stream. The `invocation` it carries is re-read when the frame is written, so it is current state with a resume position attached rather than a snapshot taken at the cursor.  ### Previews  `output_text.delta` and `thinking.delta` preview one model iteration. Their identity is `(invocation_id, attempt, iteration, content_index)`. Accumulate by that tuple, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message lands, and when the turn reaches a terminal status. One model iteration produces exactly one saved assistant message, so previews sharing an `(invocation_id, attempt, iteration)` build one message. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `iteration`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe: a turn that has settled re-yields its result.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


from __future__ import annotations
import pprint
import re  # noqa: F401
import json

from pydantic import BaseModel, ConfigDict, Field, StrictStr, field_validator
from typing import Any, ClassVar, Dict, List, Optional
from typing_extensions import Annotated
from nvoken_generated.models.agent_definition_write import AgentDefinitionWrite
from nvoken_generated.models.invocation_context_item import InvocationContextItem
from nvoken_generated.models.invocation_input import InvocationInput
from nvoken_generated.models.mcp_server_headers import MCPServerHeaders
from nvoken_generated.models.provider_key_selection import ProviderKeySelection
from nvoken_generated.models.session_options import SessionOptions
from nvoken_generated.models.webhook_target import WebhookTarget
from typing import Optional, Set
from typing_extensions import Self
from pydantic_core import to_jsonable_python

class CreateInvocationRequest(BaseModel):
    """
    CreateInvocationRequest
    """ # noqa: E501
    agent_key: Annotated[str, Field(min_length=1, strict=True, max_length=255)] = Field(description="Stable caller-controlled Agent key, unique within the authenticated App. The resulting Agent anchor stores identity only and is shared across that App's tenant partitions. ")
    tenant_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = Field(default=None, description="Optional tenant partition. For Session-key resolution or a new Session, precedence is credential constraint, this explicit value, then the default partition. For Session-ID resolution, an App credential without a tenant constraint may omit it and use the stored partition. ")
    user_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = Field(default=None, description="Optional host-owned end-user label recorded on this Invocation and its input message. The Session retains the label from the request that opened it, while later turns may identify different end users. Filtering only; not an isolation boundary. ")
    session_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = Field(default=None, description="Existing Session to continue. Mutually exclusive with session_key.")
    session_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = Field(default=None, description="Caller key resolved within (effective tenant partition, Agent, session_key). Mutually exclusive with session_id. ")
    session_options: Optional[SessionOptions] = Field(default=None, description="Settings stored on the Session itself, rather than on this turn.  On a new Session these are saved. On a Session that already exists, `compaction` and `retention` are checked rather than applied: matching values are fine, and a different value returns `session_options_conflict` telling you which paths disagreed. This keeps two callers from silently reconfiguring each other's conversation.  `metadata` merges instead, exactly as `PATCH /v1/sessions/{session_id}` does: a present key replaces and an absent key survives. It never conflicts, so a label that drifted since the last turn cannot fail this one.  If no compaction policy is stored yet, this turn can install one, because the policy needs a model to validate against and only a turn supplies that. ")
    metadata: Optional[Dict[str, Annotated[str, Field(strict=True, max_length=512)]]] = Field(default=None, description="Your own data to attach to this turn — a ticket number, a trace ID, whatever helps you tie it back to your system. nvoken stores it and hands it back untouched.  It is fixed once the turn is created and counts as part of the request for idempotency. Retrying with the same `idempotency_key` but different metadata is treated as a different request and returns a conflict rather than updating it. A genuine retry of the same original request carries the same values anyway.  Session metadata is a separate thing and can be changed — see `session_options.metadata` and `PATCH /v1/sessions/{session_id}`. ")
    idempotency_key: Annotated[str, Field(min_length=1, strict=True, max_length=255)] = Field(description="Your key for making retries safe. Send the same unchanged request again after a 5xx, a timeout, a dropped connection, or any case where you never saw the response, and you get the original turn back instead of starting a second one.  Keys are scoped to the tenant and `agent_key`, so the same key under a different tenant is a different request. Deduplication lasts as long as the original turn still exists. ")
    if_active: Optional[StrictStr] = Field(default='reject', description="What to do when the Session already has a turn running. A Session runs one turn at a time.  - `reject` (the default) refuses this request with   `session_invocation_active` and leaves the running turn alone. - `supersede` cancels the running turn and starts this one in its   place. The cancelled turn's work is discarded and does not carry   forward — \"discard and redo\". - `interrupt` asks the running turn to stop cleanly and starts   this one only once it has, so this turn builds on what the   stopped one produced — \"stop and redo\".  Omitting the field and sending `reject` are the same request for idempotency purposes. ")
    on_budget_exhausted: Optional[StrictStr] = Field(default='stop', description="What to do when the turn runs out of one of its spending limits. `stop` ends it as `incomplete`. `pause` leaves it as `paused` so you can raise the limit and continue it.  Covers the iteration, output-token, per-turn cost, and Session cost limits. Deadlines are not covered — a turn that runs out of time always ends and can never be resumed. ")
    context: Optional[Annotated[List[InvocationContextItem], Field(max_length=8)]] = Field(default=None, description="Ordered application-owned state snapshots to record before this turn's input. Send a name again to supersede its prior value. An unchanged latest value is deduplicated from the transcript, while this exact pre-deduplication payload remains part of the Invocation and of idempotency comparison.  A Session may observe at most 16 distinct names over its lifetime. Names are stored and shown to the model with the reserved `app-` prefix, which callers must omit here. Context is not part of the Agent Definition and never advances its revision. ")
    input: InvocationInput
    webhook: Optional[WebhookTarget] = None
    agent_definition_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = Field(default=None, description="Stable resource ID to resolve for this turn. IDs from another App, or IDs that do not exist, return `agent_definition_not_found`. Idempotent replay compares this stable ID and returns the original admitted revision even when the resource has advanced. ")
    agent_definition: Optional[AgentDefinitionWrite] = Field(default=None, description="Full definition for this turn. nvoken records an immutable snapshot and returns its generated `agent_definition_id` and revision on the Invocation. Mutually exclusive with `agent_definition_id`. ")
    mcp_server_headers: Optional[Annotated[List[MCPServerHeaders], Field(max_length=8)]] = Field(default=None, description="Per-Invocation secret headers keyed to MCP server names in the selected Agent Definition. Encrypted for this turn and never stored in, hashed into, or returned with the Agent Definition. ")
    provider_keys: Optional[Annotated[List[ProviderKeySelection], Field(min_length=1, max_length=1)]] = Field(default=None, description="Which key pays for the model on this turn. Names a source; never contains a secret.  Leave it out and nvoken works down its default order: your app's stored key for that provider, then a self-hosted installation's environment key (`config_byok`), then platform funding if the installation allows it.  Whichever source is chosen is fixed when the turn starts. A turn never silently falls through to a different payer partway through, so the bill cannot move once work has begun. ")
    additional_properties: Dict[str, Any] = {}
    __properties: ClassVar[List[str]] = ["agent_key", "tenant_key", "user_key", "session_id", "session_key", "session_options", "metadata", "idempotency_key", "if_active", "on_budget_exhausted", "context", "input", "webhook", "agent_definition_id", "agent_definition", "mcp_server_headers", "provider_keys"]

    @field_validator('if_active')
    def if_active_validate_enum(cls, value):
        """Validates the enum"""
        if value is None:
            return value

        if value not in set(['reject', 'supersede', 'interrupt']):
            raise ValueError("must be one of enum values ('reject', 'supersede', 'interrupt')")
        return value

    @field_validator('on_budget_exhausted')
    def on_budget_exhausted_validate_enum(cls, value):
        """Validates the enum"""
        if value is None:
            return value

        if value not in set(['stop', 'pause']):
            raise ValueError("must be one of enum values ('stop', 'pause')")
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
        """Create an instance of CreateInvocationRequest from a JSON string"""
        return cls.from_dict(json.loads(json_str))

    def to_dict(self) -> Dict[str, Any]:
        """Return the dictionary representation of the model using alias.

        This has the following differences from calling pydantic's
        `self.model_dump(by_alias=True)`:

        * `None` is only added to the output dict for nullable fields that
          were set at model initialization. Other fields with value `None`
          are ignored.
        * Fields in `self.additional_properties` are added to the output dict.
        """
        excluded_fields: Set[str] = set([
            "additional_properties",
        ])

        _dict = self.model_dump(
            by_alias=True,
            exclude=excluded_fields,
            exclude_none=True,
        )
        # override the default output from pydantic by calling `to_dict()` of session_options
        if self.session_options:
            _dict['session_options'] = self.session_options.to_dict()
        # override the default output from pydantic by calling `to_dict()` of each item in context (list)
        _items = []
        if self.context:
            for _item_context in self.context:
                if _item_context:
                    _items.append(_item_context.to_dict())
            _dict['context'] = _items
        # override the default output from pydantic by calling `to_dict()` of input
        if self.input:
            _dict['input'] = self.input.to_dict()
        # override the default output from pydantic by calling `to_dict()` of webhook
        if self.webhook:
            _dict['webhook'] = self.webhook.to_dict()
        # override the default output from pydantic by calling `to_dict()` of agent_definition
        if self.agent_definition:
            _dict['agent_definition'] = self.agent_definition.to_dict()
        # override the default output from pydantic by calling `to_dict()` of each item in mcp_server_headers (list)
        _items = []
        if self.mcp_server_headers:
            for _item_mcp_server_headers in self.mcp_server_headers:
                if _item_mcp_server_headers:
                    _items.append(_item_mcp_server_headers.to_dict())
            _dict['mcp_server_headers'] = _items
        # override the default output from pydantic by calling `to_dict()` of each item in provider_keys (list)
        _items = []
        if self.provider_keys:
            for _item_provider_keys in self.provider_keys:
                if _item_provider_keys:
                    _items.append(_item_provider_keys.to_dict())
            _dict['provider_keys'] = _items
        # puts key-value pairs in additional_properties in the top level
        if self.additional_properties is not None:
            for _key, _value in self.additional_properties.items():
                _dict[_key] = _value

        return _dict

    @classmethod
    def from_dict(cls, obj: Optional[Dict[str, Any]]) -> Optional[Self]:
        """Create an instance of CreateInvocationRequest from a dict"""
        if obj is None:
            return None

        if not isinstance(obj, dict):
            return cls.model_validate(obj)

        _obj = cls.model_validate({
            "agent_key": obj.get("agent_key"),
            "tenant_key": obj.get("tenant_key"),
            "user_key": obj.get("user_key"),
            "session_id": obj.get("session_id"),
            "session_key": obj.get("session_key"),
            "session_options": SessionOptions.from_dict(obj["session_options"]) if obj.get("session_options") is not None else None,
            "metadata": obj.get("metadata"),
            "idempotency_key": obj.get("idempotency_key"),
            "if_active": obj.get("if_active") if obj.get("if_active") is not None else 'reject',
            "on_budget_exhausted": obj.get("on_budget_exhausted") if obj.get("on_budget_exhausted") is not None else 'stop',
            "context": [InvocationContextItem.from_dict(_item) for _item in obj["context"]] if obj.get("context") is not None else None,
            "input": InvocationInput.from_dict(obj["input"]) if obj.get("input") is not None else None,
            "webhook": WebhookTarget.from_dict(obj["webhook"]) if obj.get("webhook") is not None else None,
            "agent_definition_id": obj.get("agent_definition_id"),
            "agent_definition": AgentDefinitionWrite.from_dict(obj["agent_definition"]) if obj.get("agent_definition") is not None else None,
            "mcp_server_headers": [MCPServerHeaders.from_dict(_item) for _item in obj["mcp_server_headers"]] if obj.get("mcp_server_headers") is not None else None,
            "provider_keys": [ProviderKeySelection.from_dict(_item) for _item in obj["provider_keys"]] if obj.get("provider_keys") is not None else None
        })
        # store additional fields in additional_properties
        for _key in obj.keys():
            if _key not in cls.__properties:
                _obj.additional_properties[_key] = obj.get(_key)

        return _obj
