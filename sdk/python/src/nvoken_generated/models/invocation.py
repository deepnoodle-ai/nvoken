# coding: utf-8

"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `stream.end` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  `stream.end` never speaks about turns. It says this connection is closing and nothing more, so a client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `stream.end` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


from __future__ import annotations
import pprint
import re  # noqa: F401
import json

from datetime import datetime
from pydantic import BaseModel, ConfigDict, Field, StrictBool, StrictStr
from typing import Any, ClassVar, Dict, List, Optional
from typing_extensions import Annotated
from nvoken_generated.models.agent_definition import AgentDefinition
from nvoken_generated.models.credit_block import CreditBlock
from nvoken_generated.models.invocation_context_item import InvocationContextItem
from nvoken_generated.models.invocation_failure import InvocationFailure
from nvoken_generated.models.invocation_status import InvocationStatus
from nvoken_generated.models.invocation_stop_reason import InvocationStopReason
from nvoken_generated.models.model_provenance import ModelProvenance
from nvoken_generated.models.model_usage import ModelUsage
from nvoken_generated.models.resolved_limits import ResolvedLimits
from nvoken_generated.models.structured_output_provenance import StructuredOutputProvenance
from nvoken_generated.models.tool_call_summary import ToolCallSummary
from typing import Optional, Set
from typing_extensions import Self
from pydantic_core import to_jsonable_python

class Invocation(BaseModel):
    """
    One turn.  Some fields are audience-restricted: they are present for a machine credential and omitted for a browser grant, which is why they are not required. Omission is the whole mechanism, so one schema decodes every response and nothing has to be guessed from the payload. The omitted set here is `agent_id`, `user_key`, `agent_definition`, `context`, `credit_block`, `usage`, `provenance`, `structured_output_provenance`, `metadata`, and `limits`.
    """ # noqa: E501
    id: Annotated[str, Field(min_length=1, strict=True)] = Field(description="Opaque identifier with the public `inv_` prefix. Treat the body as opaque.")
    agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = Field(default=None, description="Opaque identifier with the public `agent_` prefix. Treat the body as opaque.")
    session_id: Annotated[str, Field(min_length=1, strict=True)] = Field(description="Opaque identifier with the public `sess_` prefix. Treat the body as opaque.")
    user_key: Optional[StrictStr] = Field(default=None, description="Your own label for the end user this turn belongs to. Useful for filtering lists. It is not a security boundary — no request is ever refused because of it, so do not rely on it to keep one user's data away from another. ")
    agent_definition_id: Annotated[str, Field(min_length=1, strict=True)] = Field(description="Stable App-owned Agent Definition identifier with the public `def_` prefix. Treat the body as opaque.")
    agent_definition_revision: Annotated[int, Field(strict=True, ge=1)] = Field(description="Immutable Agent Definition revision admitted for this turn.")
    agent_definition: Optional[AgentDefinition] = Field(default=None, description="The agent definition this turn actually ran with, stored when the turn started and returned exactly as it was. Request headers for remote MCP servers are never stored and never appear here.  Present on `GET /v1/invocations/{id}` and on the result. Null in list items, where `agent_definition_id` and `agent_definition_revision` identify it instead. ")
    context: Optional[Annotated[List[InvocationContextItem], Field(max_length=8)]] = Field(default=None, description="The ordered context payload accepted with this turn, before transcript deduplication. Null when omitted and in Invocation list items. Present on admission, point reads, results, and stream Invocation projections. Context is immutable and order-sensitive for idempotency. ")
    deduplicated: Optional[StrictBool] = Field(default=None, description="Only present on the `POST /v1/invocations` response. False when this call created a new turn, true when your idempotency key matched one that already existed and you got that one back. ")
    status: InvocationStatus
    stop_reason: Optional[InvocationStopReason] = Field(description="Why the turn stopped or paused. Present on `completed`, `incomplete`, and `paused`; null on every other status — a failure keeps `error` as the authority. Treat an unrecognized value as an ordinary end. ")
    credit_block: Optional[CreditBlock] = Field(default=None, description="Tenant credit account for an insufficient-credits stop, otherwise null.")
    attempt: Annotated[int, Field(strict=True, ge=0)] = Field(description="Execution attempts this Invocation has been claimed for. It increases on every claim, so an attempt increase across a `running → queued → running` transition is the retry signal that status alone cannot give, and it is the durable anchor for discarding provisional output from an earlier attempt. Zero before the first claim. ")
    error: Optional[InvocationFailure]
    usage: Optional[ModelUsage] = Field(default=None, description="One normalized terminal aggregate, not a billing ledger.")
    provenance: Optional[ModelProvenance] = None
    structured_output: Optional[Dict[str, Any]] = Field(description="The object the model produced, already checked against the schema you asked for. Null until the turn finishes successfully, and always null if you did not ask for structured output. ")
    structured_output_provenance: Optional[StructuredOutputProvenance] = None
    metadata: Optional[Dict[str, Annotated[str, Field(strict=True, max_length=512)]]] = Field(default=None, description="Your own data, stored when the turn was created and returned exactly as you sent it. ")
    limits: Optional[ResolvedLimits] = None
    active_execution_ms: Annotated[int, Field(strict=True, ge=0)]
    deadline_at: Optional[datetime] = Field(description="The deadline currently enforced by the runtime. Null while the Invocation is waiting without an explicit waiting timeout; the explicit waiting deadline while bounded; otherwise the total-time deadline for queued, running, and terminal Invocations. ")
    created_at: datetime
    updated_at: datetime
    ended_at: Optional[datetime]
    tool_calls: Optional[List[ToolCallSummary]] = Field(default=None, description="Every tool call this turn has made, with its current status. Omitted when the turn has made none. ")
    __properties: ClassVar[List[str]] = ["id", "agent_id", "session_id", "user_key", "agent_definition_id", "agent_definition_revision", "agent_definition", "context", "deduplicated", "status", "stop_reason", "credit_block", "attempt", "error", "usage", "provenance", "structured_output", "structured_output_provenance", "metadata", "limits", "active_execution_ms", "deadline_at", "created_at", "updated_at", "ended_at", "tool_calls"]

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
        """Create an instance of Invocation from a JSON string"""
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
        # override the default output from pydantic by calling `to_dict()` of agent_definition
        if self.agent_definition:
            _dict['agent_definition'] = self.agent_definition.to_dict()
        # override the default output from pydantic by calling `to_dict()` of each item in context (list)
        _items = []
        if self.context:
            for _item_context in self.context:
                if _item_context:
                    _items.append(_item_context.to_dict())
            _dict['context'] = _items
        # override the default output from pydantic by calling `to_dict()` of credit_block
        if self.credit_block:
            _dict['credit_block'] = self.credit_block.to_dict()
        # override the default output from pydantic by calling `to_dict()` of error
        if self.error:
            _dict['error'] = self.error.to_dict()
        # override the default output from pydantic by calling `to_dict()` of usage
        if self.usage:
            _dict['usage'] = self.usage.to_dict()
        # override the default output from pydantic by calling `to_dict()` of provenance
        if self.provenance:
            _dict['provenance'] = self.provenance.to_dict()
        # override the default output from pydantic by calling `to_dict()` of structured_output_provenance
        if self.structured_output_provenance:
            _dict['structured_output_provenance'] = self.structured_output_provenance.to_dict()
        # override the default output from pydantic by calling `to_dict()` of limits
        if self.limits:
            _dict['limits'] = self.limits.to_dict()
        # override the default output from pydantic by calling `to_dict()` of each item in tool_calls (list)
        _items = []
        if self.tool_calls:
            for _item_tool_calls in self.tool_calls:
                if _item_tool_calls:
                    _items.append(_item_tool_calls.to_dict())
            _dict['tool_calls'] = _items
        # set to None if user_key (nullable) is None
        # and model_fields_set contains the field
        if self.user_key is None and "user_key" in self.model_fields_set:
            _dict['user_key'] = None

        # set to None if agent_definition (nullable) is None
        # and model_fields_set contains the field
        if self.agent_definition is None and "agent_definition" in self.model_fields_set:
            _dict['agent_definition'] = None

        # set to None if context (nullable) is None
        # and model_fields_set contains the field
        if self.context is None and "context" in self.model_fields_set:
            _dict['context'] = None

        # set to None if stop_reason (nullable) is None
        # and model_fields_set contains the field
        if self.stop_reason is None and "stop_reason" in self.model_fields_set:
            _dict['stop_reason'] = None

        # set to None if credit_block (nullable) is None
        # and model_fields_set contains the field
        if self.credit_block is None and "credit_block" in self.model_fields_set:
            _dict['credit_block'] = None

        # set to None if error (nullable) is None
        # and model_fields_set contains the field
        if self.error is None and "error" in self.model_fields_set:
            _dict['error'] = None

        # set to None if usage (nullable) is None
        # and model_fields_set contains the field
        if self.usage is None and "usage" in self.model_fields_set:
            _dict['usage'] = None

        # set to None if provenance (nullable) is None
        # and model_fields_set contains the field
        if self.provenance is None and "provenance" in self.model_fields_set:
            _dict['provenance'] = None

        # set to None if structured_output (nullable) is None
        # and model_fields_set contains the field
        if self.structured_output is None and "structured_output" in self.model_fields_set:
            _dict['structured_output'] = None

        # set to None if structured_output_provenance (nullable) is None
        # and model_fields_set contains the field
        if self.structured_output_provenance is None and "structured_output_provenance" in self.model_fields_set:
            _dict['structured_output_provenance'] = None

        # set to None if metadata (nullable) is None
        # and model_fields_set contains the field
        if self.metadata is None and "metadata" in self.model_fields_set:
            _dict['metadata'] = None

        # set to None if deadline_at (nullable) is None
        # and model_fields_set contains the field
        if self.deadline_at is None and "deadline_at" in self.model_fields_set:
            _dict['deadline_at'] = None

        # set to None if ended_at (nullable) is None
        # and model_fields_set contains the field
        if self.ended_at is None and "ended_at" in self.model_fields_set:
            _dict['ended_at'] = None

        return _dict

    @classmethod
    def from_dict(cls, obj: Optional[Dict[str, Any]]) -> Optional[Self]:
        """Create an instance of Invocation from a dict"""
        if obj is None:
            return None

        if not isinstance(obj, dict):
            return cls.model_validate(obj)

        _obj = cls.model_validate({
            "id": obj.get("id"),
            "agent_id": obj.get("agent_id"),
            "session_id": obj.get("session_id"),
            "user_key": obj.get("user_key"),
            "agent_definition_id": obj.get("agent_definition_id"),
            "agent_definition_revision": obj.get("agent_definition_revision"),
            "agent_definition": AgentDefinition.from_dict(obj["agent_definition"]) if obj.get("agent_definition") is not None else None,
            "context": [InvocationContextItem.from_dict(_item) for _item in obj["context"]] if obj.get("context") is not None else None,
            "deduplicated": obj.get("deduplicated"),
            "status": obj.get("status"),
            "stop_reason": obj.get("stop_reason"),
            "credit_block": CreditBlock.from_dict(obj["credit_block"]) if obj.get("credit_block") is not None else None,
            "attempt": obj.get("attempt"),
            "error": InvocationFailure.from_dict(obj["error"]) if obj.get("error") is not None else None,
            "usage": ModelUsage.from_dict(obj["usage"]) if obj.get("usage") is not None else None,
            "provenance": ModelProvenance.from_dict(obj["provenance"]) if obj.get("provenance") is not None else None,
            "structured_output": obj.get("structured_output"),
            "structured_output_provenance": StructuredOutputProvenance.from_dict(obj["structured_output_provenance"]) if obj.get("structured_output_provenance") is not None else None,
            "metadata": obj.get("metadata"),
            "limits": ResolvedLimits.from_dict(obj["limits"]) if obj.get("limits") is not None else None,
            "active_execution_ms": obj.get("active_execution_ms"),
            "deadline_at": obj.get("deadline_at"),
            "created_at": obj.get("created_at"),
            "updated_at": obj.get("updated_at"),
            "ended_at": obj.get("ended_at"),
            "tool_calls": [ToolCallSummary.from_dict(_item) for _item in obj["tool_calls"]] if obj.get("tool_calls") is not None else None
        })
        return _obj
