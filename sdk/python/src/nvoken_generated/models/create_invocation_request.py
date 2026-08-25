# coding: utf-8

"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - Full App keys can read and mutate every resource owned by their App. - Read-only App keys can read the same non-secret App and runtime data but   cannot mutate anything, including their own key lineage. - Installation-admin keys manage Orgs, Apps, and App keys but resolve no   App data. Short-lived console presentations provide fixed Org or admin   control-plane and reporting access.  Tenant and user assertion headers narrow individual requests. Durable API keys carry no tenant, Session, profile, or operation constraints.  ## Acting for one tenant or one end user  An app-wide credential can reach every tenant in its App, which means an id that arrives from the wrong place — a stale link, a mixed-up webhook, a tampered form field — is an id it can act on. The usual answer is for the host to re-read the resource and compare its `tenant_key` and `user_key` before every call. Say it once instead:  ``` X-Nvoken-Tenant-Key: acme X-Nvoken-User-Key: user-7c1f ```  Both headers are accepted on every request and narrow that one request to the scope they name. Anything outside it is reported as `not_found`, so a Session or Invocation belonging to another tenant or another end user cannot be read, cancelled, interrupted, forked, answered, or erased — and you learn nothing about whether the id exists. Writes take the same scope: an omitted `tenant_key` or `user_key` in the body inherits it, and one naming somebody else is refused.  Two rules keep them honest. They may only narrow, so a credential already bound to one tenant refuses a header naming another with `forbidden` rather than silently returning nothing. And a record with no `user_key` at all is outside every `X-Nvoken-User-Key` assertion rather than inside all of them, because a Session nobody claimed is not one you can claim by asserting.  Each header takes 1 to 255 characters and may appear once. A blank, oversized, or repeated header is `invalid_request` rather than ignored: an assertion nobody notices dropping is worse than no assertion at all.  A browser token already carries its tenant and its end user, so it neither needs these headers nor is allowed to send them.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that reusable resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for, so you can filter on it later — and on an Agent whose Definition sets `memory.scope: user`, it also selects which memories that Agent can recall. It is fixed by the request that opens a Session and a later turn cannot change it. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource's name. `session_id` on an Invocation is the reusable conversation it belongs to, or null for a standalone turn. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken's identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  `GET /v1/invocations/{invocation_id}/stream` follows exactly one turn and closes after its terminal change is delivered. For standalone work its cursor is scoped to that Invocation and exposes no carrier ID. For a conversation-bound turn it uses the Session cursor scope, so the same cursor can resume the aggregate Session stream.  `GET /v1/sessions/{session_id}/stream` is the durable conversation subscription: it carries every turn in the Session and stays open while the conversation is idle. A standalone Invocation cursor cannot be used on this route because standalone work has no public Session.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

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
from nvoken_generated.models.agent_definition_overrides import AgentDefinitionOverrides
from nvoken_generated.models.compaction_policy import CompactionPolicy
from nvoken_generated.models.invocation_context_item import InvocationContextItem
from nvoken_generated.models.invocation_input import InvocationInput
from nvoken_generated.models.invocation_session import InvocationSession
from nvoken_generated.models.invocation_trigger import InvocationTrigger
from nvoken_generated.models.mcp_server_headers import MCPServerHeaders
from nvoken_generated.models.provider_key_selection import ProviderKeySelection
from nvoken_generated.models.retention_policy import RetentionPolicy
from nvoken_generated.models.webhook_target import WebhookTarget
from typing import Optional, Set
from typing_extensions import Self
from pydantic_core import to_jsonable_python

class CreateInvocationRequest(BaseModel):
    """
    CreateInvocationRequest
    """ # noqa: E501
    agent_id: Optional[Annotated[str, Field(min_length=1, strict=True)]] = Field(default=None, description="Opaque identifier with the public `agent_` prefix. Treat the body as opaque.")
    agent_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = Field(default=None, description="Stable caller-controlled Agent key, unique within the effective tenant. Mutually exclusive with `agent_id`. ")
    tenant_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = Field(default=None, description="Optional tenant partition. For Session-key resolution or a new Session, precedence is credential constraint, this explicit value, then the default partition. For Session-ID resolution, an App credential without a tenant constraint may omit it and use the stored partition. ")
    user_key: Optional[Annotated[str, Field(min_length=1, strict=True, max_length=255)]] = Field(default=None, description="Who this turn is for. The first request that opens a Session fixes its `user_key`, including fixing it to absent; every later turn either sends the same one or leaves it out and inherits it. A turn naming a different end user is refused with `session_user_key_conflict`.  It is a filter, and on an Agent whose Definition sets `memory.scope: user` it is also the memory partition — it decides whose durable memories the model can recall — so it is required on the turn that opens a Session for such an Agent. ")
    triggered_by: Optional[InvocationTrigger] = Field(default=None, description="The exact ToolCall and parent Invocation that caused this turn. nvoken verifies the pair, inherits and enforces its tenant and user scope, and keeps it as immutable idempotency evidence. Accepted only from machine credentials. One ToolCall may trigger multiple children with different idempotency keys. ")
    session: Optional[InvocationSession] = Field(default=None, description="Conversation intent. Omit this object for a standalone Invocation; nvoken still uses an expiring internal carrier, but it is not a public Session and every response reports `session_id: null`. ")
    retention: Optional[RetentionPolicy] = Field(default=None, description="Retention for a newly created conversation or a standalone turn. Standalone turns default to one hour. Invalid when continuing an existing Session. ")
    compaction: Optional[CompactionPolicy] = Field(default=None, description="Conversation compaction policy. Invalid for standalone turns.")
    authorization_context: Optional[Dict[str, Annotated[str, Field(strict=True, max_length=512)]]] = Field(default=None, description="Host authorization binding recorded on a newly created conversation.")
    metadata: Optional[Dict[str, Annotated[str, Field(strict=True, max_length=512)]]] = Field(default=None, description="Your own data to attach to this turn — a ticket number, a trace ID, whatever helps you tie it back to your system. nvoken stores it and hands it back untouched.  It is fixed once the turn is created and counts as part of the request for idempotency. Retrying with the same `idempotency_key` but different metadata is treated as a different request and returns a conflict rather than updating it. A genuine retry of the same original request carries the same values anyway.  Session metadata is a separate thing and can be changed — see `PATCH /v1/sessions/{session_id}`. ")
    idempotency_key: Annotated[str, Field(min_length=1, strict=True, max_length=255)] = Field(description="Your key for making retries safe. Send the same unchanged request again after a 5xx, a timeout, a dropped connection, or any case where you never saw the response, and you get the original turn back instead of starting a second one.  Keys are scoped to the tenant and resolved Agent, so the same key under a different tenant or Agent is a different request. Deduplication survives private-content erasure. An equal retry of an erased turn returns `410 invocation_erased` with the original Invocation ID; a changed retry still conflicts. ")
    on_budget_exhausted: Optional[StrictStr] = Field(default='stop', description="What to do when the turn runs out of one of its consumption limits. `stop` ends it as `incomplete`. `hold` leaves it as `budget_hold` so you can raise the limit and continue it.  Covers the iteration, output-token, and per-turn estimated-cost limits, and exhausted tenant credits. Deadlines are not covered — a turn that runs out of time always ends and can never be resumed. ")
    context: Optional[Annotated[List[InvocationContextItem], Field(max_length=8)]] = Field(default=None, description="Ordered application-owned state snapshots to record before this turn's input. Send a name again to supersede its prior value. An unchanged latest value is deduplicated from the transcript, while this exact pre-deduplication payload remains part of the Invocation and of idempotency comparison.  A Session may observe at most 16 distinct names over its lifetime. Names are stored and shown to the model with the reserved `app-` prefix, which callers must omit here. Context is not part of the Agent Definition and never advances its revision. ")
    input: InvocationInput
    webhook: Optional[WebhookTarget] = None
    definition_revision: Optional[Annotated[int, Field(strict=True, ge=1)]] = Field(default=None, description="Optional one-turn revision pin, ahead of Session and Agent pins.")
    overrides: Optional[AgentDefinitionOverrides] = None
    mcp_server_headers: Optional[Annotated[List[MCPServerHeaders], Field(max_length=8)]] = Field(default=None, description="Per-Invocation secret headers keyed to MCP server names in the selected Agent Definition. Encrypted for this turn and never stored in, hashed into, or returned with the Agent Definition. ")
    provider_keys: Optional[Annotated[List[ProviderKeySelection], Field(min_length=1, max_length=1)]] = Field(default=None, description="Which key pays for the model on this turn. Names a source; never contains a secret.  Leave it out and nvoken works down its default order: your app's stored key for that provider, then a self-hosted installation's environment key (`config_byok`), then platform funding if the installation allows it.  Whichever source is chosen is fixed when the turn starts. A turn never silently falls through to a different payer partway through, so the bill cannot move once work has begun. ")
    additional_properties: Dict[str, Any] = {}
    __properties: ClassVar[List[str]] = ["agent_id", "agent_key", "tenant_key", "user_key", "triggered_by", "session", "retention", "compaction", "authorization_context", "metadata", "idempotency_key", "on_budget_exhausted", "context", "input", "webhook", "definition_revision", "overrides", "mcp_server_headers", "provider_keys"]

    @field_validator('on_budget_exhausted')
    def on_budget_exhausted_validate_enum(cls, value):
        """Validates the enum"""
        if value is None:
            return value

        if value not in set(['stop', 'hold']):
            raise ValueError("must be one of enum values ('stop', 'hold')")
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
        # override the default output from pydantic by calling `to_dict()` of triggered_by
        if self.triggered_by:
            _dict['triggered_by'] = self.triggered_by.to_dict()
        # override the default output from pydantic by calling `to_dict()` of session
        if self.session:
            _dict['session'] = self.session.to_dict()
        # override the default output from pydantic by calling `to_dict()` of retention
        if self.retention:
            _dict['retention'] = self.retention.to_dict()
        # override the default output from pydantic by calling `to_dict()` of compaction
        if self.compaction:
            _dict['compaction'] = self.compaction.to_dict()
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
        # override the default output from pydantic by calling `to_dict()` of overrides
        if self.overrides:
            _dict['overrides'] = self.overrides.to_dict()
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
            "agent_id": obj.get("agent_id"),
            "agent_key": obj.get("agent_key"),
            "tenant_key": obj.get("tenant_key"),
            "user_key": obj.get("user_key"),
            "triggered_by": InvocationTrigger.from_dict(obj["triggered_by"]) if obj.get("triggered_by") is not None else None,
            "session": InvocationSession.from_dict(obj["session"]) if obj.get("session") is not None else None,
            "retention": RetentionPolicy.from_dict(obj["retention"]) if obj.get("retention") is not None else None,
            "compaction": CompactionPolicy.from_dict(obj["compaction"]) if obj.get("compaction") is not None else None,
            "authorization_context": obj.get("authorization_context"),
            "metadata": obj.get("metadata"),
            "idempotency_key": obj.get("idempotency_key"),
            "on_budget_exhausted": obj.get("on_budget_exhausted") if obj.get("on_budget_exhausted") is not None else 'stop',
            "context": [InvocationContextItem.from_dict(_item) for _item in obj["context"]] if obj.get("context") is not None else None,
            "input": InvocationInput.from_dict(obj["input"]) if obj.get("input") is not None else None,
            "webhook": WebhookTarget.from_dict(obj["webhook"]) if obj.get("webhook") is not None else None,
            "definition_revision": obj.get("definition_revision"),
            "overrides": AgentDefinitionOverrides.from_dict(obj["overrides"]) if obj.get("overrides") is not None else None,
            "mcp_server_headers": [MCPServerHeaders.from_dict(_item) for _item in obj["mcp_server_headers"]] if obj.get("mcp_server_headers") is not None else None,
            "provider_keys": [ProviderKeySelection.from_dict(_item) for _item in obj["provider_keys"]] if obj.get("provider_keys") is not None else None
        })
        # store additional fields in additional_properties
        for _key in obj.keys():
            if _key not in cls.__properties:
                _obj.additional_properties[_key] = obj.get(_key)

        return _obj
