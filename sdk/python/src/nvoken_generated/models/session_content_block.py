# coding: utf-8

"""
    nvoken API

    nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or   read `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, client-safe projections, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  Two streams carry the same frames. `GET /v1/invocations/{invocation_id}/stream` follows one turn and ends when that turn settles. `GET /v1/sessions/{session_id}/transcript/stream` follows every turn in a Session, and is the surface to use for a conversation. `POST /v1/invocations` with `Accept: text/event-stream` admits and streams one turn inline.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  The Invocation stream's saved frames are `invocation.accepted`, `invocation.update`, and `invocation.result`. The Session stream's only saved frame is `transcript.update`. Every other frame on either stream is live.  ### Resuming and finishing  The resume position has four spellings and one value: the SSE `id` line, `resume_cursor` inside a frame payload, the `cursor` query parameter, and the `Last-Event-ID` header. Send it back as `cursor` or as `Last-Event-ID`; `cursor` wins when a request carries both. Cursors are Session-scoped on both streams, so a position taken from one stream resumes the other.  Reconnecting to a turn that has already settled always yields `invocation.result` followed by `stream.end` with reason `terminal`, at any cursor. Both are valid signals that a turn is over, and a client may exit on either.  `invocation.accepted` is emitted only by the inline `POST` path. The `GET` stream never sends it, so a client that admits separately never sees it. The nvoken SDKs synthesize an equivalent locally so their callers see the same first event either way.  An `invocation.update` never carries a terminal status. Terminal state arrives as `invocation.result` and nowhere else on that stream. The `invocation` it carries is re-read when the frame is written, so it is current state with a resume position attached rather than a snapshot taken at the cursor.  ### Previews  `output_text.delta` and `thinking.delta` preview one model iteration. Their identity is `(invocation_id, attempt, iteration, content_index)`. Accumulate by that tuple, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message lands, and when the turn reaches a terminal status. One model iteration produces exactly one saved assistant message, so previews sharing an `(invocation_id, attempt, iteration)` build one message. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `iteration`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe: a turn that has settled re-yields its result.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.

    The version of the OpenAPI document: 0.1.0
    Generated by OpenAPI Generator (https://openapi-generator.tech)

    Do not edit the class manually.
"""  # noqa: E501


from __future__ import annotations
import json
import pprint
from pydantic import BaseModel, ConfigDict, Field, StrictStr, ValidationError, field_validator
from typing import Any, List, Optional
from nvoken_generated.models.document_reference_block import DocumentReferenceBlock
from nvoken_generated.models.image_reference_block import ImageReferenceBlock
from nvoken_generated.models.redacted_block import RedactedBlock
from nvoken_generated.models.reminder_block import ReminderBlock
from nvoken_generated.models.server_tool_use_block import ServerToolUseBlock
from nvoken_generated.models.text_block import TextBlock
from nvoken_generated.models.tool_result_block import ToolResultBlock
from nvoken_generated.models.tool_use_block import ToolUseBlock
from pydantic import StrictStr, Field
from typing import Union, List, Set, Optional, Dict
from typing_extensions import Literal, Self

SESSIONCONTENTBLOCK_ONE_OF_SCHEMAS = ["DocumentReferenceBlock", "ImageReferenceBlock", "RedactedBlock", "ReminderBlock", "ServerToolUseBlock", "TextBlock", "ToolResultBlock", "ToolUseBlock"]

class SessionContentBlock(BaseModel):
    """
    One block of a stored message, in nvoken's own shape rather than any provider's. Images and documents come back described rather than inlined — type, media type, optional title, size in `bytes`, and a `sha256:` checksum — so reading a transcript never ships the original bytes back to you.  New block types will appear as features ship. Render the ones you know and skip the rest.
    """
    # data type: TextBlock
    oneof_schema_1_validator: Optional[TextBlock] = None
    # data type: ImageReferenceBlock
    oneof_schema_2_validator: Optional[ImageReferenceBlock] = None
    # data type: DocumentReferenceBlock
    oneof_schema_3_validator: Optional[DocumentReferenceBlock] = None
    # data type: ToolUseBlock
    oneof_schema_4_validator: Optional[ToolUseBlock] = None
    # data type: ServerToolUseBlock
    oneof_schema_5_validator: Optional[ServerToolUseBlock] = None
    # data type: ToolResultBlock
    oneof_schema_6_validator: Optional[ToolResultBlock] = None
    # data type: ReminderBlock
    oneof_schema_7_validator: Optional[ReminderBlock] = None
    # data type: RedactedBlock
    oneof_schema_8_validator: Optional[RedactedBlock] = None
    actual_instance: Optional[Union[DocumentReferenceBlock, ImageReferenceBlock, RedactedBlock, ReminderBlock, ServerToolUseBlock, TextBlock, ToolResultBlock, ToolUseBlock]] = None
    one_of_schemas: Set[str] = { "DocumentReferenceBlock", "ImageReferenceBlock", "RedactedBlock", "ReminderBlock", "ServerToolUseBlock", "TextBlock", "ToolResultBlock", "ToolUseBlock" }

    model_config = ConfigDict(
        validate_assignment=True,
        protected_namespaces=(),
    )


    discriminator_value_class_map: Dict[str, str] = {
    }

    def __init__(self, *args, **kwargs) -> None:
        if args:
            if len(args) > 1:
                raise ValueError("If a position argument is used, only 1 is allowed to set `actual_instance`")
            if kwargs:
                raise ValueError("If a position argument is used, keyword arguments cannot be used.")
            super().__init__(actual_instance=args[0])
        else:
            super().__init__(**kwargs)

    @field_validator('actual_instance')
    def actual_instance_must_validate_oneof(cls, v):
        instance = SessionContentBlock.model_construct()
        error_messages = []
        match = 0
        # validate data type: TextBlock
        if not isinstance(v, TextBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `TextBlock`")
        else:
            match += 1
        # validate data type: ImageReferenceBlock
        if not isinstance(v, ImageReferenceBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `ImageReferenceBlock`")
        else:
            match += 1
        # validate data type: DocumentReferenceBlock
        if not isinstance(v, DocumentReferenceBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `DocumentReferenceBlock`")
        else:
            match += 1
        # validate data type: ToolUseBlock
        if not isinstance(v, ToolUseBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `ToolUseBlock`")
        else:
            match += 1
        # validate data type: ServerToolUseBlock
        if not isinstance(v, ServerToolUseBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `ServerToolUseBlock`")
        else:
            match += 1
        # validate data type: ToolResultBlock
        if not isinstance(v, ToolResultBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `ToolResultBlock`")
        else:
            match += 1
        # validate data type: ReminderBlock
        if not isinstance(v, ReminderBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `ReminderBlock`")
        else:
            match += 1
        # validate data type: RedactedBlock
        if not isinstance(v, RedactedBlock):
            error_messages.append(f"Error! Input type `{type(v)}` is not `RedactedBlock`")
        else:
            match += 1
        if match > 1:
            # more than 1 match
            raise ValueError("Multiple matches found when setting `actual_instance` in SessionContentBlock with oneOf schemas: DocumentReferenceBlock, ImageReferenceBlock, RedactedBlock, ReminderBlock, ServerToolUseBlock, TextBlock, ToolResultBlock, ToolUseBlock. Details: " + ", ".join(error_messages))
        elif match == 0:
            # no match
            raise ValueError("No match found when setting `actual_instance` in SessionContentBlock with oneOf schemas: DocumentReferenceBlock, ImageReferenceBlock, RedactedBlock, ReminderBlock, ServerToolUseBlock, TextBlock, ToolResultBlock, ToolUseBlock. Details: " + ", ".join(error_messages))
        else:
            return v

    @classmethod
    def from_dict(cls, obj: Union[str, Dict[str, Any]]) -> Self:
        return cls.from_json(json.dumps(obj))

    @classmethod
    def from_json(cls, json_str: str) -> Self:
        """Returns the object represented by the json string"""
        instance = cls.model_construct()
        error_messages = []
        match = 0

        # deserialize data into TextBlock
        try:
            instance.actual_instance = TextBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))
        # deserialize data into ImageReferenceBlock
        try:
            instance.actual_instance = ImageReferenceBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))
        # deserialize data into DocumentReferenceBlock
        try:
            instance.actual_instance = DocumentReferenceBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))
        # deserialize data into ToolUseBlock
        try:
            instance.actual_instance = ToolUseBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))
        # deserialize data into ServerToolUseBlock
        try:
            instance.actual_instance = ServerToolUseBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))
        # deserialize data into ToolResultBlock
        try:
            instance.actual_instance = ToolResultBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))
        # deserialize data into ReminderBlock
        try:
            instance.actual_instance = ReminderBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))
        # deserialize data into RedactedBlock
        try:
            instance.actual_instance = RedactedBlock.from_json(json_str)
            match += 1
        except (ValidationError, ValueError) as e:
            error_messages.append(str(e))

        if match > 1:
            # more than 1 match
            raise ValueError("Multiple matches found when deserializing the JSON string into SessionContentBlock with oneOf schemas: DocumentReferenceBlock, ImageReferenceBlock, RedactedBlock, ReminderBlock, ServerToolUseBlock, TextBlock, ToolResultBlock, ToolUseBlock. Details: " + ", ".join(error_messages))
        elif match == 0:
            # no match
            raise ValueError("No match found when deserializing the JSON string into SessionContentBlock with oneOf schemas: DocumentReferenceBlock, ImageReferenceBlock, RedactedBlock, ReminderBlock, ServerToolUseBlock, TextBlock, ToolResultBlock, ToolUseBlock. Details: " + ", ".join(error_messages))
        else:
            return instance

    def to_json(self) -> str:
        """Returns the JSON representation of the actual instance"""
        if self.actual_instance is None:
            return "null"

        if hasattr(self.actual_instance, "to_json") and callable(self.actual_instance.to_json):
            return self.actual_instance.to_json()
        else:
            return json.dumps(self.actual_instance)

    def to_dict(self) -> Optional[Union[Dict[str, Any], DocumentReferenceBlock, ImageReferenceBlock, RedactedBlock, ReminderBlock, ServerToolUseBlock, TextBlock, ToolResultBlock, ToolUseBlock]]:
        """Returns the dict representation of the actual instance"""
        if self.actual_instance is None:
            return None

        if hasattr(self.actual_instance, "to_dict") and callable(self.actual_instance.to_dict):
            return self.actual_instance.to_dict()
        else:
            # primitive type
            return self.actual_instance

    def to_str(self) -> str:
        """Returns the string representation of the actual instance"""
        return pprint.pformat(self.model_dump())
