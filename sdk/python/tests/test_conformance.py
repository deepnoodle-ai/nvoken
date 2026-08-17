from __future__ import annotations

import asyncio
import base64
import json
import os
from dataclasses import asdict, replace
from datetime import datetime, timedelta, timezone
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import httpx
import pytest
from nvoken_generated import (
    InvocationFailure,
    InvocationStatus,
    InvocationStopReason,
    MessagePhase,
)
from nvoken_generated.models.invocation_input import InvocationInput
from nvoken_generated.models.nudge_acknowledgement import NudgeAcknowledgement
from nvoken_generated.models.create_nudge_request import CreateNudgeRequest
from nvoken_generated.models.nudge import Nudge
from nvoken_generated.models.nudge_status import NudgeStatus
from nvoken_generated.models.builtin_tool_declaration import BuiltinToolDeclaration
from nvoken_generated.models.tool_declaration import ToolDeclaration as GeneratedToolDeclaration
from nvoken_generated.models.tool_call_summary import ToolCallSummary
from nvoken_generated.exceptions import ApiException
from nvoken_generated.models.agent_definition_resource import AgentDefinitionResource

from nvoken_generated.models.reminder_block import ReminderBlock
from nvoken_generated.models.session_content_block import SessionContentBlock

from nvoken.client import (
    TERMINAL_STATUSES,
    _MAX_CONTEXT_CONTENT_BYTES,
    _MAX_CONTEXT_ITEMS,
    _MAX_CONTEXT_NAME_LENGTH,
    _MAX_CONTEXT_TOTAL_BYTES,
    _generated_webhook_target,
)
from nvoken.media_preflight import (
    DOCUMENT_MEDIA_TYPES,
    IMAGE_MEDIA_TYPES,
    MAX_DOCUMENT_INPUT_BYTES,
    MAX_IMAGE_INPUT_BYTES,
    MAX_MEDIA_INPUT_BLOCKS,
    MAX_MEDIA_INPUT_BYTES,
    MAX_MEDIA_TITLE_CHARACTERS,
    DocumentBlock,
    DocumentSource,
    ImageBlock,
    ImageSource,
    InputBlock,
    TextBlock,
    input_block_wire,
    media_input_issue,
)

from nvoken.client_token import (
    CLIENT_TOKEN_LIFETIME_LIMIT,
    ClientTokenClaims,
    all_browser_operations,
    mint_client_token,
)

from nvoken import (
    AgentDefinition,
    AgentDefinitionOverrides,
    ASK_USER_TOOL_NAME,
    AskUserInput,
    AskUserOutput,
    AppSigningKeyPurpose,
    Client,
    CreateClientKeyRequest,
    CreateSessionRequest,
    CredentialProfile,
    ClientInterface,
    ContextCompaction,
    ContextItem,
    InvocationHandle,
    InvokeRequest,
    MCPServer,
    MCPServerHeaders,
    Model,
    WebhookTarget,
    NvokenError,
    AgentOptions,
    InvocationOptions,
    ForkSessionRequest,
    MemoryKind,
    MemorySearchMode,
    MintAppSigningKeyRequest,
    Operation,
    ProviderKeySelection,
    RetryPolicy,
    RegisterAppRequest,
    Reasoning,
    Sampling,
    Scope,
    SessionOptions,
    UpdateAppRequest,
    Reducer,
    StreamEvent,
    ToolResult,
    ToolChoice,
    answerable_tool_calls,
    ask_user_input_schema,
    ask_user_tool,
    host_tool_calls,
    is_not_found,
    issue_anonymous_token,
    SessionRetention,
    WebSearchLocation,
    WebSearchTool,
    web_search_tool,
    CallbackReceiver,
    CallbackReply,
    DeliverySigningKey,
    VerifiedCallback,
    VerifiedWebhook,
    WebhookReceiver,
    callback_result,
    preflight_output_schema,
    verify_callback,
    verify_webhook,
    webhook_status_is_retried,
    accept_webhook,
    retry_webhook,
    fetch_tool,
)

AGENT_ID = "agent_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
INVOCATION_ID = "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
SESSION_ID = "sess_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
TOOL_CALL_ID = "call_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
WAIT_ID = "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb328"
EXACT_MODEL_ID = "experimental/model?variant=雪%#1"


# The ask_user shape is published in four SDKs plus a fixture the runtime's own
# admission test reads. Five hand-written copies drift, and a host that copies
# the guide's schema into an agent nvoken then rejects gets the worst kind of
# bug report, so each copy is pinned to the fixture here and in the three other
# conformance suites.
def test_published_ask_user_tool_matches_the_shared_fixture() -> None:
    fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/ask-user-tool-v1.json").read_text()
    )
    tool = ask_user_tool(lambda _: AskUserOutput(canceled=True))
    assert tool.name == fixture["name"] == ASK_USER_TOOL_NAME
    assert tool.description == fixture["description"]
    assert tool.input_schema == fixture["input_schema"]
    assert ask_user_input_schema() == fixture["input_schema"]


# The typed wrapper must produce exactly the wire shape the guide documents,
# including `canceled` on an answered question — a host UI keying off its
# presence should not see it disappear when the user actually answers.
def test_ask_user_handler_encodes_the_documented_result() -> None:
    def answer(question: AskUserInput) -> AskUserOutput:
        assert question.type == "select"
        return AskUserOutput(response=question.options[0].value)

    tool = ask_user_tool(answer)
    content = asyncio.run(tool.handler({
        "question": "Which name?",
        "type": "select",
        "options": [{"value": "option-b", "label": "Option B"}],
    }))
    assert content == {"canceled": False, "response": "option-b"}


# Session options, host metadata, and provider tools are built by four
# independently written request builders. This pins each of them to the same
# fixture, so a field one binding spells differently fails here rather than
# being silently dropped on the way to the Runtime.
def test_shared_session_lifecycle_fixture() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2] / "conformance/fixtures/session-lifecycle-v1.json"
        ).read_text()
    )
    client = Client(base_url="https://runtime.example.test", api_key="key")
    model = Model(provider="anthropic", id="claude-sonnet-4-6")

    def body(request: InvokeRequest) -> dict[str, Any]:
        return client._invocation_body(request).to_dict()

    retention = body(InvokeRequest(
        agent_key="support",
        input="hello",
        session_key="conformance",
        session_options=SessionOptions(retention=SessionRetention(ttl_seconds=86400)),
        metadata=fixture["invocation_metadata"],
    ))
    assert retention["session_options"] == fixture["session_options"]["retention_only"]
    assert retention["metadata"] == fixture["invocation_metadata"]
    assert (
        client._agent_definition_body(
            AgentDefinition(
                name="Support",
                model=model,
                provider_tools=(web_search_tool(),),
            ),
            include_key=False,
        ).to_dict()["provider_tools"]
        == fixture["provider_tools"]["defaults"]
    )

    every = body(InvokeRequest(
        agent_key="support",
        input="hello",
        session_key="conformance",
        session_options=SessionOptions(
            compaction=ContextCompaction(trigger_tokens=32768),
            retention=SessionRetention(ttl_seconds=3600),
            authorization_context={"surface": "web"},
            pinned_revision=4,
            on_conflict="join",
        ),
    ))
    assert every["session_options"] == fixture["session_options"]["every_member"]
    configured_definition = client._agent_definition_body(
        AgentDefinition(
            name="Support",
            model=model,
            provider_tools=(web_search_tool(WebSearchTool(
                max_uses=5,
                allowed_domains=("example.com", "docs.example.com"),
                user_location=WebSearchLocation(
                    city="Austin",
                    region="Texas",
                    country="US",
                    timezone="America/Chicago",
                ),
            )),),
        ),
        include_key=False,
    ).to_dict()
    assert (
        configured_definition["provider_tools"]
        == fixture["provider_tools"]["configured"]
    )

    # Session options with no members would serialize to `{}`, which the Runtime
    # rejects for minProperties — catching it locally names the field.
    with pytest.raises(NvokenError):
        body(InvokeRequest(
            agent_key="support",
            input="hello",
            session_key="conformance",
            session_options=SessionOptions(),
        ))


# The Agent binding is where a host actually spends its time, and it is the layer
# that fell behind the contract in every SDK. Pinning the whole Agent-issued body
# means an option the binding cannot forward is a missing key here.
def test_shared_agent_request_fixture() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2] / "conformance/fixtures/session-lifecycle-v1.json"
        ).read_text()
    )
    expected = fixture["agent_request"]["web_search_metadata_unbound"]
    client = Client(base_url="https://runtime.example.test", api_key="key")
    agent = client.agent(AgentOptions(
        agent_key="support",
    ))
    # Durable options apply on a new anonymous Session too, which is where a
    # short retention window matters most.
    options = InvocationOptions(
        idempotency_key="conformance",
        on_budget_exhausted="hold",
        metadata={"board": "brand-2026", "surface": "web"},
        session_options=SessionOptions(
            retention=SessionRetention(ttl_seconds=86400),
        ),
    )
    assert client._invocation_body(agent._request("hello", options)).to_dict() == expected

    # Existing Session admissions carry options for equal-or-conflict
    # reconciliation instead of rejecting the pairing in the SDK.
    body = client._invocation_body(agent._request("hello", InvocationOptions(
        idempotency_key="conformance",
        session_id=SESSION_ID,
        session_options=SessionOptions(retention=SessionRetention(ttl_seconds=86400)),
    )))
    assert body.session_id == SESSION_ID
    assert body.session_options.retention.ttl_seconds == 86400


def test_shared_fetch_builtin_fixture_is_expressible() -> None:
    fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/fetch-builtin-v1.json").read_text()
    )
    declaration = fetch_tool()
    assert asdict(declaration) == fixture["declaration"]
    generated = GeneratedToolDeclaration(BuiltinToolDeclaration(**fixture["declaration"]))
    assert generated.to_dict() == fixture["declaration"]


def test_shared_settlement_legibility_fixture_pins_the_stop_reasons() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2] / "conformance/fixtures/settlement-legibility-v1.json"
        ).read_text()
    )
    assert sorted(fixture["stop_reason"]["values"]) == sorted(
        member.value for member in InvocationStopReason
    )
    assert sorted(fixture["message_phase"]["values"]) == sorted(
        member.value for member in MessagePhase
    )
    assert sorted(fixture["stop_reason"]["present_only_on_statuses"]) == sorted(
        [
            InvocationStatus.COMPLETED.value,
            InvocationStatus.INCOMPLETE.value,
            InvocationStatus.BUDGET_HOLD.value,
        ]
    )
    # The wait helpers stop at exactly these statuses; a terminal the SDK does
    # not recognize is a wait that never returns.
    assert sorted(fixture["terminal_statuses"]) == sorted(TERMINAL_STATUSES)


def test_context_window_failure_fixture_preserves_numeric_details() -> None:
    fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/invocation-result.json").read_text()
    )
    failure = InvocationFailure.from_dict(fixture["context_window_failure"])
    assert failure.code == "context_window_exceeded"
    assert failure.details is not None
    assert failure.details["input_tokens"] == 205321
    assert isinstance(failure.details["context_window_tokens"], int)
    encoded = failure.to_dict()
    assert encoded["details"]["requested_output_tokens"] == 4096
    assert isinstance(encoded["details"]["requested_output_tokens"], int)


def test_shared_reasoning_control_fixture_is_expressible() -> None:
    fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/reasoning-controls-v1.json").read_text()
    )
    assert [Reasoning(effort=value).effort for value in fixture["efforts"]] == \
        ["low", "medium", "high", "xhigh", "max"]
    assert [Reasoning(budget_tokens=value).budget_tokens for value in fixture["budgets"]] == \
        [1024, 2048, 63999]
    assert Reasoning() == Reasoning(**fixture["omitted"])
    error = fixture["combination_error"]
    normalized = NvokenError(
        error["category"],
        error["message"],
        status=error["status"],
        code=error["code"],
        details=error["details"],
    )
    assert normalized.category == "validation"
    assert normalized.status == 400
    assert normalized.code == "invalid_request"
    assert normalized.details == {
        "kind": "model_control_combination_unsupported",
        "fields": [
            "reasoning.budget_tokens",
            "sampling.temperature",
        ],
    }


def test_shared_tool_choice_fixture_is_expressible() -> None:
    fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/tool-choice-v1.json").read_text()
    )
    choices = [
        ToolChoice(mode="auto"),
        ToolChoice(mode="none"),
        ToolChoice(mode="required"),
        ToolChoice(mode="named", name="lookup"),
    ]
    assert [
        {
            key: value
            for key, value in asdict(choice).items()
            if value is not None
        }
        for choice in choices
    ] == fixture["choices"]


def _fixture_block(block: dict[str, Any]) -> InputBlock:
    if block["type"] == "text":
        return TextBlock(text=block["text"])
    if block["type"] == "image":
        return ImageBlock(
            source=ImageSource(
                media_type=block["source"].get("media_type"),
                data=block["source"].get("data"),
                url=block["source"].get("url"),
            )
        )
    return DocumentBlock(
        source=DocumentSource(
            media_type=block["source"].get("media_type"),
            data=block["source"].get("data"),
            url=block["source"].get("url"),
        ),
        title=block.get("title"),
    )


def test_shared_media_input_fixture_matches_preflight() -> None:
    fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/media-input-v1.json").read_text()
    )
    assert fixture["limits"] == {
        "media_blocks": MAX_MEDIA_INPUT_BLOCKS,
        "image_bytes": MAX_IMAGE_INPUT_BYTES,
        "document_bytes": MAX_DOCUMENT_INPUT_BYTES,
        "media_bytes": MAX_MEDIA_INPUT_BYTES,
        "title_characters": MAX_MEDIA_TITLE_CHARACTERS,
    }
    assert fixture["media_types"]["image"] == list(IMAGE_MEDIA_TYPES)
    assert fixture["media_types"]["document"] == list(DOCUMENT_MEDIA_TYPES)
    for accepted in fixture["accepted"]:
        blocks = tuple(_fixture_block(block) for block in accepted["content"])
        assert media_input_issue(blocks) is None, accepted["id"]
        assert [input_block_wire(block) for block in blocks] == accepted["content"]
    for rejected in fixture["rejected"]:
        # Cases the dataclasses cannot express are guaranteed by typing instead.
        if "python" in rejected.get("unrepresentable_in", []):
            continue
        blocks = tuple(_fixture_block(block) for block in rejected["content"])
        issue = media_input_issue(blocks)
        assert issue is not None, rejected["id"]
        assert asdict(issue) == rejected["issue"], rejected["id"]


def test_shared_agent_definition_reuse_fixture_is_expressible() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2]
            / "conformance/fixtures/agent-definition-reuse-v1.json"
        ).read_text()
    )
    client = Client(base_url="https://runtime.example.test", api_key="key")
    body = client._invocation_body(InvokeRequest(
        agent_key="support",
        input="hello",
        idempotency_key="agent-definition-reference",
    )).to_dict()
    assert body["agent_key"] == "support"
    assert "definition" not in body


# Resource creation must render the complete named and keyed definition.
def test_resource_creation_renders_the_named_definition() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2]
            / "conformance/fixtures/agent-definition-reuse-v1.json"
        ).read_text()
    )
    client = Client(base_url="https://runtime.example.test", api_key="key")
    definition = AgentDefinition(
        definition_key="support",
        name="Billing support",
        instructions="You are a concise billing support agent.",
        model=Model(provider="anthropic", id="claude-sonnet-5"),
    )
    creation = client._agent_definition_body(definition, include_key=True).to_dict()
    assert creation == fixture["creation"]["request"]


# A reusable definition is durable configuration, so MCP headers must ride
# alongside it and never within it.
def test_mcp_secrets_stay_outside_the_agent_definition() -> None:
    client = Client(base_url="https://runtime.example.test", api_key="key")
    request = InvokeRequest(
        agent_key="support",
        input="hello",
        idempotency_key="mcp-secret-placement",
        mcp_server_headers=(
            MCPServerHeaders(name="support", headers={"Authorization": "Bearer secret"}),
        ),
    )
    body = client._invocation_body(request).to_dict()
    assert body["mcp_server_headers"] == [
        {"name": "support", "headers": {"Authorization": "Bearer secret"}}
    ]



def test_shared_context_compaction_fixture_is_expressible() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2]
            / "conformance/fixtures/context-compaction-v1.json"
        ).read_text()
    )
    auto = SessionOptions(
        compaction=ContextCompaction(
            trigger_tokens=fixture["auto"]["compaction"]["trigger_tokens"],
        ),
    )
    explicit = SessionOptions(
        compaction=ContextCompaction(
            trigger_tokens=fixture["explicit"]["compaction"]["trigger_tokens"],
            model=Model(**fixture["explicit"]["compaction"]["model"]),
        ),
    )
    assert auto.compaction.trigger_tokens == "auto"
    assert explicit.compaction.trigger_tokens == 32768
    assert explicit.compaction.model is not None
    assert explicit.compaction.model.id == "claude-sonnet-4-6"
    assert fixture["errors"][1]["fields"] == [
        "model.provider",
        "session_options.compaction.model.provider",
    ]


def expand_output_schema_fixture(test_case: dict[str, Any]) -> dict[str, Any]:
    generated = test_case.get("generate")
    if generated:
        assert generated["kind"] == "nested-object"
        node: dict[str, Any] = {"type": "string"}
        for _ in range(1, generated["depth"]):
            node = {
                "type": "object",
                "properties": {"child": node},
                "required": ["child"],
            }
        return node
    schema = json.loads(json.dumps(test_case["schema"]))
    repeated = test_case.get("repeat")
    if repeated:
        parts = [
            part.replace("~1", "/").replace("~0", "~")
            for part in repeated["path"].removeprefix("/").split("/")
        ]
        current = schema
        for part in parts[:-1]:
            current = current[part]
        current[parts[-1]] = repeated["character"] * repeated["count"]
    return schema


def test_shared_output_schema_preflight_fixtures() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2]
            / "conformance/fixtures/structured-output-schema-v1.json"
        ).read_text()
    )
    for test_case in fixture["accepted"]:
        preflight_output_schema(expand_output_schema_fixture(test_case))
    for test_case in fixture["rejected"]:
        with pytest.raises(NvokenError) as failure:
            preflight_output_schema(expand_output_schema_fixture(test_case))
        error = failure.value
        assert error.category == "validation", test_case["id"]
        assert error.code == "schema_preflight_failed", test_case["id"]
        assert error.details["kind"] == "output_schema", test_case["id"]
        assert error.details["code"] == test_case["issue"]["code"], test_case["id"]
        assert error.details["path"] == test_case["issue"]["path"], test_case["id"]
        assert error.details.get("keyword") == test_case["issue"].get("keyword"), \
            test_case["id"]


@pytest.mark.asyncio
async def test_invoke_preflights_output_schema_before_transport() -> None:
    async with Client("http://nvoken.test", "test-key") as client:
        attempts = 0

        async def create(_: Any) -> Any:
            nonlocal attempts
            attempts += 1
            raise AssertionError("transport must not be called")

        client.invocations.create_invocation = create
        fixture = json.loads(
            (
                Path(__file__).parents[2]
                / "conformance/fixtures/structured-output-schema-v1.json"
            ).read_text()
        )
        for test_case in fixture["rejected"]:
            with pytest.raises(NvokenError) as failure:
                await client.invoke(InvokeRequest(
                    agent_key="support",
                    input="help",
                    overrides=AgentDefinitionOverrides(
                        model=Model(provider="anthropic", id="test-model"),
                        output_schema=expand_output_schema_fixture(test_case),
                    ),
                ))
            assert failure.value.code == "schema_preflight_failed", test_case["id"]
        assert attempts == 0


@pytest.mark.asyncio
async def test_shared_fault_server_semantics() -> None:
    base_url = os.getenv("NVOKEN_CONFORMANCE_URL")
    if not base_url:
        pytest.skip("NVOKEN_CONFORMANCE_URL is not set")
    result_fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/invocation-result.json").read_text()
    )
    tool_call_fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/tool-call-records-v1.json").read_text()
    )
    expected_output_text = result_fixture["message_join"]["expected_output_text"]
    async with httpx.AsyncClient() as setup:
        await setup.post(f"{base_url}/__test/reset")

    async with Client(
        base_url,
        "test-key",
        retry=RetryPolicy(max_attempts=3, min_delay=0.001, max_delay=0.005),
    ) as client:
        agents = await client.list_agents(agent_key="support")
        assert agents.items[0].id == AGENT_ID
        agent_identity = await client.get_agent(AGENT_ID)
        assert agent_identity.agent_key == "support"
        models = await client.list_models()
        assert models.catalog_version == "conformance-catalog-v1"
        assert next(model for model in models.items if model.id == "future-model").provider == \
            "future_provider"
        assert models.items[0].controls.sampling.temperature is True
        assert models.items[0].controls.reasoning.effort.values == [
            "low", "medium", "high", "xhigh", "max"
        ]
        server = MCPServer(
            name="support",
            url="https://mcp.example.test/rpc",
            allowed_tools=("lookup",),
        )
        mcp_tools = await client.list_mcp_tools(
            server,
            {"Authorization": "Bearer conformance-mcp-secret"},
        )
        assert mcp_tools.tools[0].projected_name == "support__lookup"
        exact_model = await client.get_model(Model(provider="openai", id=EXACT_MODEL_ID))
        assert exact_model.id == EXACT_MODEL_ID
        assert exact_model.cataloged is False
        assert exact_model.pricing.status == "unpriced"

        fixture = json.loads(
            (Path(__file__).parents[2] / "conformance/fixtures/credits-v1.json").read_text()
        )
        credits = await client.allocate_credits(
            amount="25.000000",
            default_tenant=True,
            idempotency_key="python-credits-conformance",
        )
        assert credits.allocation.id == fixture["allocation"]["id"]
        assert credits.account.available.amount == "20.250000"
        accounts = await client.list_credit_accounts(default_tenant=True)
        assert accounts.items[0].available.amount == "20.250000"
        allocations = await client.list_credit_allocations(default_tenant=True)
        assert allocations.items[0].amount.amount == "25.000000"

        handle = await client.invoke(InvokeRequest(
            agent_key="support",
            idempotency_key="python-lost-ack",
            if_active="supersede",
            input="hello",
            provider_keys=(
                ProviderKeySelection(
                    provider="openai",
                    source="caller_ephemeral",
                    api_key="conformance-secret",
                ),
            ),
            mcp_server_headers=(
                MCPServerHeaders(
                    name="support",
                    headers={"Authorization": "Bearer conformance-mcp-secret"},
                ),
            ),
        ))
        assert handle.invocation_id == INVOCATION_ID
        assert handle.session_id == SESSION_ID
        tool_calls = await handle.list_tool_calls(limit=4)
        assert json.loads(tool_calls.to_json()) == tool_call_fixture["tool_calls"]

        resumed = client.invocation(INVOCATION_ID)
        await resumed.refresh()
        assert resumed.status == "completed"

        waiting = client.invocation(WAIT_ID)
        with pytest.raises(TimeoutError) as timeout:
            await asyncio.wait_for(
                waiting.wait(min_poll_interval=0.001, max_poll_interval=0.002),
                timeout=0.01,
            )
        assert not isinstance(timeout.value, NvokenError)

        first_page = await client.list_invocations(
            agent_key="support",
            status=["queued", "running", "queued"],
        )
        assert first_page.has_more is True
        assert first_page.next_cursor == "invocations-page-2"
        second_page = await client.list_invocations(
            agent_key="support",
            status=["waiting", "queued", "running"],
            cursor=first_page.next_cursor,
        )
        assert second_page.has_more is False
        sessions = await client.list_sessions(agent_key="support")
        assert sessions.items[0].agent_id == AGENT_ID
        assert sessions.items[0].usage.input_tokens == 9
        assert sessions.items[0].context.estimated_tokens == 12
        assert sessions.items[0].context.context_window_tokens == 128000
        assert sessions.items[0].context.model.provider == "openai"
        assert sessions.items[0].context.model.id == "gpt-test"
        messages = await client.list_session_messages(SESSION_ID)
        assert messages.next_cursor == "messages-page-2"
        newest_first = await client.list_session_messages(SESSION_ID, order="desc")
        assert [message.sequence for message in newest_first.items] == [2]
        assert newest_first.next_cursor == "messages-page-2-desc"
        compactions = await client.list_session_compactions(SESSION_ID)
        assert compactions.items[0].status == "applied"
        assert compactions.items[0].summary == "The user chose the durable option."

        composed = await handle.result()
        assert composed.invocation.id == INVOCATION_ID
        assert composed.invocation.status == "completed"
        assert composed.invocation.structured_output == {"answer": "world"}
        assert composed.invocation.structured_output_provenance.source == "tool_call"
        assert [message.role for message in composed.messages] == [
            "user",
            "assistant",
            "assistant",
        ]
        assert composed.output_text == expected_output_text
        assert await handle.output_text() == composed.output_text
        assert len(await handle.list_messages()) == 3

        accepted = await handle.submit_tool_results([
            ToolResult(tool_call_id=TOOL_CALL_ID, content={"ok": True}),
        ])
        assert accepted.results[0].deduplicated is True
        assert (await handle.cancel()).status == "cancelled"
        interrupted = await handle.interrupt()
        assert interrupted.status == "completed"
        assert interrupted.stop_reason == "interrupted"
        assert interrupted.attempt == 1

        with pytest.raises(NvokenError) as conflict:
            await client.get_invocation("conflict")
        assert conflict.value.category == "conflict"
        assert conflict.value.status == 409
        assert conflict.value.request_id
        with pytest.raises(NvokenError) as unauthenticated:
            await client.get_invocation("unauthenticated")
        assert unauthenticated.value.category == "authentication"
        assert unauthenticated.value.status == 401
        with pytest.raises(NvokenError) as forbidden:
            await client.get_invocation("forbidden")
        assert forbidden.value.category == "permission"
        assert forbidden.value.status == 403
        assert (await client.get_invocation("rate-limit")).status == "completed"
        with pytest.raises(NvokenError) as rate_limited:
            await client.get_invocation("rate-limit-always")
        assert rate_limited.value.category == "rate_limit"
        assert rate_limited.value.status == 429
        assert rate_limited.value.retry_after == 1
        with pytest.raises(NvokenError) as unavailable:
            await client.get_invocation("server-error")
        assert unavailable.value.category == "server"
        assert unavailable.value.status == 503

        event_types: list[str] = []

        async def consume(event: StreamEvent) -> None:
            event_types.append(event.type)

        # One stream, filtered to one turn: a dropped connection, a rotation,
        # and then the frame carrying the terminal change, which is where the
        # read ends. Nothing announces that the turn is over except the change.
        await client.invocation(INVOCATION_ID).stream(consume, deltas=False)
        assert event_types == [
            "transcript.update",
            "transcript.update",
            "connection.closing",
            "transcript.update",
            "transcript.update",
        ]

    async with httpx.AsyncClient() as inspect:
        state = (await inspect.get(f"{base_url}/__test/state")).json()
    assert state == {
        "admission_attempts": 2,
        "credential_admissions": 2,
        "result_attempts": 2,
        "cancel_attempts": 1,
        "interrupt_attempts": 1,
        "stream_attempts": 3,
        "last_event_id": "cursor-1",
        "last_statuses": ["waiting", "queued", "running"],
        "last_deltas": "false",
        "last_invocation_filter": INVOCATION_ID,
    }


def test_shared_tool_call_mode_partition() -> None:
    """Answerable is wider than mine once an App declares callback tools.

    nvoken delivers those itself, yet a machine credential may still settle one
    that a receiver acknowledged, so it carries arguments like any pending host
    call. Every SDK draws the same line at the same place.
    """
    path = Path(__file__).parents[2] / "conformance/fixtures/tool-call-modes-v1.json"
    fixture = json.loads(path.read_text())
    invocation = SimpleNamespace(
        tool_calls=[ToolCallSummary.from_dict(call) for call in fixture["tool_calls"]],
    )
    assert [
        call.id for call in answerable_tool_calls(invocation)
    ] == fixture["answerable"]
    assert [call.id for call in host_tool_calls(invocation)] == fixture["host"]


def delivery_signing_vectors() -> dict:
    """The cross-SDK agreement on how nvoken signs a delivery.

    One file holds both kinds because there is one scheme; a vector each is
    what makes that testable rather than merely stated.
    """
    path = Path(__file__).parents[3] / "docs/design/delivery-signing-v1.json"
    return json.loads(path.read_text())


def tamperings(headers: dict[str, str], body: bytes) -> list[tuple[dict[str, str], bytes]]:
    """The mutations the vector file names.

    Each must be refused by both verifiers, since neither the signature nor its
    binding to a delivery id and a timestamp is particular to a delivery kind.
    """
    timestamp = dict(headers)
    timestamp["X-Nvoken-Timestamp"] = "1784635801"
    delivery = dict(headers)
    delivery["X-Nvoken-Delivery-ID"] = "different"
    signature = dict(headers)
    signature["X-Nvoken-Signature"] = "sha256=00"
    return [
        (dict(headers), body + b" "),
        (timestamp, body),
        (delivery, body),
        (signature, body),
    ]


def test_shared_callback_signing_vector() -> None:
    document = delivery_signing_vectors()
    vector = document["vectors"]["callback"]
    key = document["key"].encode()
    body = vector["body"].encode()
    now = datetime.fromtimestamp(document["now"], timezone.utc)
    verified = verify_callback(key, vector["headers"], body, now=now)
    assert verified.tool_call_id == TOOL_CALL_ID
    # The name is inside the signed body, so a receiver dispatches on it without
    # an authoritative read and without trusting a URL suffix.
    assert verified.tool_name == vector["tool_name"]
    assert verified.envelope["nvoken"]["tool_name"] == vector["tool_name"]

    # The authorization context is a sibling of `nvoken`, not a member, and the
    # vector is what holds four SDKs to that placement. The input repeats one of
    # its keys on purpose: a receiver may check that the two agree, and may
    # never read the board out of the input alone, because the model wrote it.
    assert verified.authorization_context == vector["authorization_context"]
    assert verified.envelope["authorization_context"] == vector["authorization_context"]
    assert verified.envelope["input"]["board"] == verified.authorization_context["board"]

    for headers, candidate in tamperings(vector["headers"], body):
        with pytest.raises(ValueError):
            verify_callback(key, headers, candidate, now=now)


class _ReplyStore:
    def __init__(self) -> None:
        self.replies: dict[str, CallbackReply] = {}

    async def find(self, tool_call_id: str) -> CallbackReply | None:
        return self.replies.get(tool_call_id)

    async def put_if_absent(
        self, tool_call_id: str, reply: CallbackReply
    ) -> tuple[CallbackReply, bool]:
        existing = self.replies.get(tool_call_id)
        if existing is not None:
            return existing, False
        self.replies[tool_call_id] = reply
        return reply, True


@pytest.mark.asyncio
async def test_callback_receiver_reply_discipline() -> None:
    """Every row of the discipline the receiver owns.

    The receiver's whole job is the answer it gives nvoken, since that answer
    decides whether a ToolCall settles, retries, or fails for good. Driving it
    against the published vector rather than a body written for the test is what
    keeps the two from being separately right.
    """
    document = delivery_signing_vectors()
    vector = document["vectors"]["callback"]
    body = vector["body"].encode()
    headers = vector["headers"]
    now = lambda: datetime.fromtimestamp(document["now"], timezone.utc)  # noqa: E731
    keys = [
        DeliverySigningKey(
            key_id=headers["X-Nvoken-Signing-Key-ID"],
            version=int(headers["X-Nvoken-Signing-Key-Version"]),
            secret=document["key"],
        )
    ]

    ran = 0

    async def open_ticket(delivery: VerifiedCallback) -> CallbackReply:
        nonlocal ran
        ran += 1
        # Authorization comes off the signed sibling, never off the input.
        assert delivery.authorization_context["board"] == "brd_9f21"
        return callback_result({"ticket": "T-1042"})

    store = _ReplyStore()
    receiver = CallbackReceiver(
        keys=keys, tools={vector["tool_name"]: open_ticket}, store=store, now=now
    )

    settled = await receiver.handle(headers, body)
    assert settled.outcome == "settled"
    assert settled.reply.status == 200
    assert settled.delivery is not None and settled.delivery.tool_call_id == TOOL_CALL_ID

    # A redelivery answers from the store. The tool must not run twice: it would
    # repeat every effect it had, which is the whole reason the store exists.
    replayed = await receiver.handle(headers, body)
    assert replayed.outcome == "replayed"
    assert replayed.reply == settled.reply
    assert ran == 1

    # An identity this receiver does not hold is refused permanently, and an
    # unconfigured one is not: only one of the two is the sender's fault, and
    # they are indistinguishable to nvoken unless the status separates them.
    rotated = CallbackReceiver(
        keys=[DeliverySigningKey(keys[0].key_id, keys[0].version + 1, keys[0].secret)],
        tools={},
        now=now,
    )
    unknown_key = await rotated.handle(headers, body)
    assert (unknown_key.reply.status, unknown_key.reason) == (401, "unknown_key")

    unconfigured = await CallbackReceiver(keys=[], tools={}, now=now).handle(headers, body)
    assert (unconfigured.reply.status, unconfigured.reason) == (503, "not_configured")

    no_tool = await CallbackReceiver(keys=keys, tools={}, now=now).handle(headers, body)
    assert (no_tool.reply.status, no_tool.reason) == (400, "unknown_tool")

    # A raise is the receiver failing, not the tool. The tool failing settles the
    # call with is_error, which the model can read and correct itself against.
    async def broken(_delivery: VerifiedCallback) -> CallbackReply:
        raise RuntimeError("database unreachable")

    failed = await CallbackReceiver(
        keys=keys, tools={vector["tool_name"]: broken}, now=now
    ).handle(headers, body)
    assert (failed.reply.status, failed.reason) == (503, "handler_failed")
    assert failed.delivery is not None

    async def tool_error(_delivery: VerifiedCallback) -> CallbackReply:
        return callback_result({"error": "no such ticket"}, True)

    settled_error = await CallbackReceiver(
        keys=keys, tools={vector["tool_name"]: tool_error}, now=now
    ).handle(headers, body)
    assert settled_error.outcome == "settled"
    assert '"is_error": true' in (settled_error.reply.body or "")

    # A version that cannot be read as a positive integer fails the build rather
    # than a live delivery, where the refusal would be permanent.
    for broken_keys in (
        [DeliverySigningKey(keys[0].key_id, 0, keys[0].secret)],
        [keys[0], keys[0]],
        [DeliverySigningKey(keys[0].key_id, 1, "too short")],
    ):
        with pytest.raises(ValueError):
            CallbackReceiver(keys=broken_keys, tools={})


@pytest.mark.asyncio
async def test_webhook_receiver_reply_discipline() -> None:
    """Where the webhook receiver differs from its callback twin.

    nvoken ignores the body, an unhandled event is not a failure, and ordering
    stays the host's.
    """
    document = delivery_signing_vectors()
    vector = document["vectors"]["webhook"]
    body = vector["body"].encode()
    headers = vector["headers"]
    now = lambda: datetime.fromtimestamp(document["now"], timezone.utc)  # noqa: E731
    keys = [
        DeliverySigningKey(
            key_id=headers["X-Nvoken-Signing-Key-ID"],
            version=int(headers["X-Nvoken-Signing-Key-Version"]),
            secret=document["key"],
        )
    ]

    applied = 0

    async def record(delivery: VerifiedWebhook) -> None:
        # The fold the host owns: only a delivery that advances the Invocation is
        # applied, and the comparison belongs in its own transaction.
        nonlocal applied
        if delivery.supersedes(applied):
            applied = delivery.sequence

    handled = await WebhookReceiver(
        keys=keys, events={vector["event"]: record}, now=now
    ).handle(headers, body)
    assert handled.outcome == "handled"
    assert handled.reply.status == 200
    assert applied == vector["sequence"]

    # An event with no handler is still a delivery. Retrying it would spend
    # nvoken's bounded attempts reaching the same absent handler.
    ignored = await WebhookReceiver(keys=keys, events={}, now=now).handle(headers, body)
    assert (ignored.outcome, ignored.reason) == ("ignored", "unhandled_event")
    assert webhook_status_is_retried(ignored.reply.status) is False

    async def unreachable(_delivery: VerifiedWebhook) -> None:
        raise RuntimeError("store unreachable")

    failed = await WebhookReceiver(
        keys=keys, events={vector["event"]: unreachable}, now=now
    ).handle(headers, body)
    assert (failed.outcome, failed.reason) == ("failed", "handler_failed")
    assert webhook_status_is_retried(failed.reply.status) is True

    # The callback key must not verify a webhook and the reverse, which is what
    # the two purposes are for.
    callback_headers = document["vectors"]["callback"]["headers"]
    crossed = await WebhookReceiver(
        keys=[
            DeliverySigningKey(
                key_id=callback_headers["X-Nvoken-Signing-Key-ID"],
                version=int(callback_headers["X-Nvoken-Signing-Key-Version"]),
                secret=document["key"],
            )
        ],
        events={},
        now=now,
    ).handle(headers, body)
    assert (crossed.reply.status, crossed.reason) == (401, "unknown_key")


def test_shared_webhook_signing_vector() -> None:
    """The callback vector's twin, and the point of having both.

    The same key, the same canonical string, the same tampering set, a
    different verifier. A scheme that drifted apart for one delivery kind would
    fail here rather than at an integrator who believed the promise that there
    is only one.
    """
    document = delivery_signing_vectors()
    vector = document["vectors"]["webhook"]
    key = document["key"].encode()
    body = vector["body"].encode()
    now = datetime.fromtimestamp(document["now"], timezone.utc)
    verified = verify_webhook(key, vector["headers"], body, now=now)
    assert verified.event == vector["event"]
    assert verified.sequence == vector["sequence"]
    assert verified.invocation_id == INVOCATION_ID
    assert verified.session_id == SESSION_ID

    for headers, candidate in tamperings(vector["headers"], body):
        with pytest.raises(ValueError):
            verify_webhook(key, headers, candidate, now=now)

    # A webhook's key is the App's webhook-purpose key and a callback's is its
    # callback key. Nothing in the wire format says which is which, so a
    # receiver that crossed them would verify nothing; each vector must refuse
    # the other's verifier.
    callback = document["vectors"]["callback"]
    with pytest.raises(ValueError):
        verify_webhook(key, callback["headers"], callback["body"].encode(), now=now)
    with pytest.raises(ValueError):
        verify_callback(key, vector["headers"], body, now=now)

    # Delivery is at least once and a redelivery can land after a later
    # transition, so folding is by sequence rather than by arrival.
    assert verified.supersedes(vector["sequence"] - 1) is True
    assert verified.supersedes(vector["sequence"]) is False
    assert webhook_status_is_retried(accept_webhook().status) is False
    assert webhook_status_is_retried(retry_webhook().status) is True


def test_shared_reducer_vector() -> None:
    path = Path(__file__).parents[2] / "conformance/fixtures/reducer.json"
    fixture = json.loads(path.read_text())
    reducer = Reducer()
    for event in fixture["events"]:
        reducer.apply(StreamEvent(
            id=event["id"],
            type=event["event"],
            data=event["data"],
        ))
    snapshot = reducer.snapshot()
    assert [message.sequence for message in snapshot.messages] == fixture["expected"]["message_sequences"]
    assert [change.revision for change in snapshot.invocation_changes] == fixture["expected"]["invocation_revisions"]
    assert snapshot.cursor == fixture["expected"]["cursor"]
    assert snapshot.previews == fixture["expected"]["previews"]
    for preview_case in fixture["preview_cases"]:
        preview_reducer = Reducer()
        for event in preview_case["events"]:
            preview_reducer.apply(StreamEvent(
                id=event["id"],
                type=event["event"],
                data=event["data"],
            ))
        assert [
            {
                "invocation_id": preview.invocation_id,
                "attempt": preview.attempt,
                "message_id": preview.message_id,
                "content_index": preview.content_index,
                "kind": preview.kind,
                "delta": preview.delta,
                **({} if preview.tool_call_id is None else {"tool_call_id": preview.tool_call_id}),
                **({} if preview.name is None else {"name": preview.name}),
            }
            for preview in preview_reducer.snapshot().previews
        ] == preview_case["expected_previews"], preview_case["name"]


@pytest.mark.asyncio
async def test_cancellation_propagates_through_replay_and_waits() -> None:
    async def assert_cancelled(awaitable: Any) -> None:
        task = asyncio.create_task(awaitable)
        await asyncio.sleep(0)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task

    async with Client("http://nvoken.test", "test-key") as client:
        blocked = asyncio.Event()
        await assert_cancelled(client._replay_safe(blocked.wait))

    class BlockingClient:
        async def get_invocation(self, _invocation_id: str) -> Any:
            await asyncio.Event().wait()

    await assert_cancelled(
        InvocationHandle(BlockingClient(), INVOCATION_ID).wait()
    )
    await assert_cancelled(
        InvocationHandle(BlockingClient(), INVOCATION_ID).wait_for_action()
    )


@pytest.mark.asyncio
async def test_session_stream_uses_public_operation_and_follows_later_turns() -> None:
    path = Path(__file__).parents[2] / "conformance/fixtures/reducer.json"
    events = json.loads(path.read_text())["events"]
    later_invocation_id = "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb399"
    later_event = json.loads(json.dumps(events[1]))
    for message in later_event["data"]["messages"]:
        message["invocation_id"] = later_invocation_id
    for change in later_event["data"]["invocation_changes"]:
        change["invocation_id"] = later_invocation_id

    def sse(event: dict[str, Any], *, idle: bool = False) -> str:
        frame = (
            "retry: 1\n"
            f"id: {event['id']}\n"
            f"event: {event['event']}\n"
            f"data: {json.dumps(event['data'])}\n\n"
        )
        if idle:
            # Nothing is running, so the server reclaims the connection. That
            # says nothing about any turn, and the subscription reconnects.
            frame += (
                "event: connection.closing\n"
                f"data: {json.dumps({'type': 'connection.closing', 'session_id': SESSION_ID, 'reason': 'idle'})}\n\n"
            )
        return frame

    class StreamOperations:
        def __init__(self) -> None:
            self.calls: list[tuple[str, str | None]] = []
            self.responses = [
                httpx.Response(200, text=sse(events[0], idle=True)),
                httpx.Response(200, text=sse(later_event)),
            ]

        async def stream_session_without_preload_content(
            self,
            session_id: str,
            *,
            invocation_id: str | None,
            cursor: str | None,
            deltas: bool,
            last_event_id: str | None,
        ) -> httpx.Response:
            assert cursor is None
            assert deltas is True
            assert invocation_id is None
            self.calls.append((session_id, last_event_id))
            return self.responses.pop(0)

    operations = StreamOperations()

    class StreamClient:
        stream_sessions = operations

    seen_updates = 0

    async def consume(event: StreamEvent, _snapshot: Any) -> None:
        nonlocal seen_updates
        if event.type == "transcript.update":
            seen_updates += 1
        if seen_updates == 2:
            raise asyncio.CancelledError

    reducer = Reducer()
    with pytest.raises(asyncio.CancelledError):
        from nvoken import stream_session
        await stream_session(StreamClient(), SESSION_ID, reducer, consume)

    assert operations.calls == [
        (SESSION_ID, None),
        (SESSION_ID, "cursor-1"),
    ]
    assert reducer.snapshot().cursor == "cursor-2"
    assert later_invocation_id in {
        change.invocation_id for change in reducer.snapshot().invocation_changes
    }


@pytest.mark.asyncio
async def test_invoke_maps_ephemeral_and_stored_provider_keys() -> None:
    async with Client("http://nvoken.test", "test-key") as client:
        captured: list[Any] = []

        async def create(body: Any) -> Any:
            captured.append(body)
            return type("Invocation", (), {
                "id": INVOCATION_ID,
                "session_id": SESSION_ID,
                "agent_id": "agent_test",
                "status": "queued",
                "deduplicated": False,
                "deadline_at": None,
            })()

        client.invocations.create_invocation = create
        base = {
            "agent_key": "support",
            "input": "hello",
        }
        await client.invoke(InvokeRequest(
            **base,
            provider_keys=(
                ProviderKeySelection(
                    provider="openai",
                    source="caller_ephemeral",
                    api_key="secret",
                ),
            ),
        ))
        await client.invoke(InvokeRequest(
            **base,
            provider_keys=(
                ProviderKeySelection(
                    provider="openai",
                    source="app_byok",
                ),
            ),
        ))

    assert captured[0].provider_keys[0].to_dict() == {
        "provider": "openai",
        "source": "caller_ephemeral",
        "key": {"api_key": "secret"},
    }
    assert captured[1].provider_keys[0].to_dict() == {
        "provider": "openai",
        "source": "app_byok",
    }


@pytest.mark.asyncio
async def test_collection_transcript_and_provider_key_operations() -> None:
    async with Client("http://nvoken.test", "test-key") as client:
        session_calls: list[str | None] = []

        async def list_sessions(**kwargs: Any) -> Any:
            cursor = kwargs.get("cursor")
            session_calls.append(cursor)
            if cursor is None:
                return SimpleNamespace(
                    items=["session-1"],
                    next_cursor="sessions-2",
                )
            return SimpleNamespace(items=["session-2"], next_cursor=None)

        message_calls: list[str | None] = []

        async def list_messages(
            _session_id: str,
            **kwargs: Any,
        ) -> Any:
            cursor = kwargs.get("cursor")
            message_calls.append(cursor)
            if cursor is None:
                return SimpleNamespace(
                    items=["message-1"],
                    next_cursor="messages-2",
                )
            return SimpleNamespace(items=["message-2"], next_cursor=None)

        transcript_calls: list[tuple[str | None, str | None]] = []

        async def transcript(
            _session_id: str,
            **kwargs: Any,
        ) -> Any:
            cursor = kwargs.get("cursor")
            page_token = kwargs.get("page_token")
            transcript_calls.append((cursor, page_token))
            if page_token is None:
                return SimpleNamespace(
                    messages=["message-1"],
                    invocation_changes=[],
                    has_more=True,
                    cursor="resume-1",
                    next_page_token="transcript-2",
                )
            return SimpleNamespace(
                messages=["message-2"],
                invocation_changes=["change-1"],
                has_more=False,
                cursor="resume-2",
                next_page_token=None,
            )

        client.sessions.list_sessions = list_sessions
        client.sessions.list_session_messages = list_messages
        client.sessions.get_session_transcript = transcript

        assert [item async for item in client.session_items()] == [
            "session-1",
            "session-2",
        ]
        assert [item async for item in client.session_message_items(SESSION_ID)] == [
            "message-1",
            "message-2",
        ]
        drained = await client.drain_transcript(SESSION_ID, cursor="resume-0")
        assert drained.messages == ["message-1", "message-2"]
        assert drained.invocation_changes == ["change-1"]
        assert drained.cursor == "resume-2"
        assert session_calls == [None, "sessions-2"]
        assert message_calls == [None, "messages-2"]
        assert transcript_calls == [
            ("resume-0", None),
            (None, "transcript-2"),
        ]

        definition_calls: list[tuple[str, Any]] = []

        async def list_agent_definitions(**kwargs: Any) -> Any:
            definition_calls.append(("list", kwargs))
            return SimpleNamespace(items=[SimpleNamespace(id="def_test")])

        async def archive_agent_definition(definition_id: str) -> None:
            definition_calls.append(("archive", definition_id))

        async def restore_agent_definition(definition_id: str) -> None:
            definition_calls.append(("restore", definition_id))

        client.agent_definitions.list_agent_definitions = list_agent_definitions
        client.agent_definitions.archive_agent_definition = archive_agent_definition
        client.agent_definitions.restore_agent_definition = restore_agent_definition

        definitions = await client.list_agent_definitions(
            include_archived=True,
            cursor="definitions-2",
            limit=10,
        )
        assert definitions.items[0].id == "def_test"
        await client.archive_agent_definition("def_test")
        await client.restore_agent_definition("def_test")
        assert definition_calls == [
            ("list", {
                "definition_key": None,
                "include_archived": True,
                "cursor": "definitions-2",
                "limit": 10,
            }),
            ("archive", "def_test"),
            ("restore", "def_test"),
        ]

        credential_calls: list[tuple[str, Any]] = []

        async def create_credential(body: Any) -> Any:
            credential_calls.append(("create", body))
            return "created"

        async def get_credential(credential_id: str) -> Any:
            credential_calls.append(("get", credential_id))
            return "read"

        async def list_credentials(**kwargs: Any) -> Any:
            credential_calls.append(("list", kwargs))
            cursor = kwargs.get("cursor")
            return SimpleNamespace(
                items=["credential-1"] if cursor is None else ["credential-2"],
                next_cursor="credentials-2" if cursor is None else None,
            )

        async def rotate_credential(credential_id: str, body: Any) -> Any:
            credential_calls.append(("rotate", (credential_id, body)))
            return "rotated"

        async def revoke_credential(credential_id: str) -> Any:
            credential_calls.append(("revoke", credential_id))
            return "revoked"

        client.provider_keys.create_provider_key = create_credential
        client.provider_keys.get_provider_key = get_credential
        client.provider_keys.list_provider_keys = list_credentials
        client.provider_keys.rotate_provider_key = rotate_credential
        client.provider_keys.revoke_provider_key = revoke_credential

        assert await client.create_provider_key(
            provider="openai",
            scope="tenant",
            tenant_key="tenant-1",
            api_key="secret",
            idempotency_key="create-key",
        ) == "created"
        assert await client.get_provider_key("pkey_test") == "read"
        assert [
            item
            async for item in client.provider_key_items(provider="openai")
        ] == ["credential-1", "credential-2"]
        assert await client.rotate_provider_key(
            "pkey_test",
            api_key="rotated-secret",
            idempotency_key="rotate-key",
        ) == "rotated"
        assert await client.revoke_provider_key("pkey_test") == "revoked"

    create_body = credential_calls[0][1]
    assert create_body.provider == "openai"
    assert create_body.scope == "tenant"
    assert create_body.tenant_key == "tenant-1"
    assert create_body.key.api_key == "secret"
    assert create_body.idempotency_key == "create-key"
    rotate_body = next(
        value[1]
        for operation, value in credential_calls
        if operation == "rotate"
    )
    assert rotate_body.key.api_key == "rotated-secret"
    assert rotate_body.idempotency_key == "rotate-key"


@pytest.mark.asyncio
async def test_management_session_observation_and_memory_facades_forward_inputs() -> None:
    async with Client("http://nvoken.test", "test-key") as client:
        calls: dict[str, Any] = {}

        async def register_org(body: Any) -> Any:
            calls["register_org"] = body
            return "registered-org"

        async def update_org(org_id: str, body: Any) -> Any:
            calls["update_org"] = (org_id, body)
            return "updated-org"

        async def register_app(body: Any) -> Any:
            calls["register_app"] = body
            return "registered-app"

        async def update_app(app_id: str, body: Any) -> Any:
            calls["update_app"] = (app_id, body)
            return "updated-app"

        async def create_client_key(app_id: str, body: Any) -> Any:
            calls["create_client_key"] = (app_id, body)
            return "client-key"

        async def mint_signing_key(app_id: str, body: Any) -> Any:
            calls["mint_signing_key"] = (app_id, body)
            return "signing-key"

        async def create_credential(key: str, body: Any) -> Any:
            calls["create_credential"] = (key, body)
            return "credential"

        async def list_logs(invocation_id: str, **kwargs: Any) -> Any:
            calls["list_logs"] = (invocation_id, kwargs)
            return "logs"

        async def list_memories(agent_id: str, **kwargs: Any) -> Any:
            calls["list_memories"] = (agent_id, kwargs)
            return "memories"

        async def create_session(body: Any) -> Any:
            calls["create_session"] = body
            return SimpleNamespace(id="sess_created")

        async def fork_session(session_id: str, body: Any) -> Any:
            calls["fork_session"] = (session_id, body)
            return SimpleNamespace(id="sess_forked")

        async def delete_session(session_id: str, **kwargs: Any) -> None:
            calls["delete_session"] = (session_id, kwargs)

        client.orgs.register_org = register_org
        client.orgs.update_org = update_org
        client.apps.register_app = register_app
        client.apps.update_app = update_app
        client.apps.create_app_client_key = create_client_key
        client.apps.mint_app_signing_key = mint_signing_key
        client.identity.create_credential = create_credential
        client.invocations.list_invocation_logs = list_logs
        client.memories.list_memories = list_memories
        client.sessions.create_session = create_session
        client.sessions.fork_session = fork_session
        client.sessions.delete_session = delete_session

        register_app_request = RegisterAppRequest(
            name="support",
            display_name="Support",
            callback_timeout_seconds=20,
        )
        update_app_request = UpdateAppRequest(anonymous_access=None)
        client_key_request = CreateClientKeyRequest(
            name="browser",
            public_key="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
        )
        signing_key_request = MintAppSigningKeyRequest(
            purpose=AppSigningKeyPurpose.CALLBACK,
            activate=True,
        )
        session_request = CreateSessionRequest(
            agent_key="support",
            tenant_key="acme",
            user_key="user-1",
            session_key="case-1",
        )
        fork_request = ForkSessionRequest.from_dict(
            {"from_message": 1, "session_key": "case-1-alt"}
        )
        assert fork_request is not None

        assert await client.register_org(
            "Acme", external_ref="org-acme"
        ) == "registered-org"
        assert await client.update_org("org_1", "Acme, Inc.") == "updated-org"
        assert await client.register_app(register_app_request) == "registered-app"
        assert await client.update_app("app_1", update_app_request) == "updated-app"
        assert await client.create_app_client_key(
            "app_1", client_key_request
        ) == "client-key"
        assert await client.mint_app_signing_key(
            "app_1", signing_key_request
        ) == "signing-key"
        assert await client.create_credential(
            name="deployer",
            profile=CredentialProfile.OPERATOR,
            app_id="app_1",
            org_id=None,
            tenant_key="acme",
            session_id=SESSION_ID,
            operations=[Operation.GET_APP],
            expires_at=datetime(2026, 9, 1, tzinfo=timezone.utc),
            idempotency_key="credential-1",
        ) == "credential"
        assert await client.list_invocation_logs(
            INVOCATION_ID,
            cursor="logs-2",
            limit=20,
            trace_id="0123456789abcdef0123456789abcdef",
        ) == "logs"
        assert await client.list_memories(
            agent_id=AGENT_ID,
            tenant_key="acme",
            user_key="user-1",
            query="refund policy",
            search_mode=MemorySearchMode.HYBRID,
            kind=MemoryKind.FACT,
            cursor="memories-2",
            limit=10,
        ) == "memories"
        assert (await client.create_session(session_request)).id == "sess_created"
        assert (await client.fork_session(SESSION_ID, fork_request)).id == "sess_forked"
        await client.delete_session(SESSION_ID, force=True)

    assert calls["register_org"].to_dict() == {
        "display_name": "Acme",
        "external_ref": "org-acme",
    }
    assert calls["update_org"][0] == "org_1"
    assert calls["update_org"][1].display_name == "Acme, Inc."
    assert calls["register_app"] is register_app_request
    assert calls["update_app"] == ("app_1", update_app_request)
    assert calls["create_client_key"] == ("app_1", client_key_request)
    assert calls["mint_signing_key"] == ("app_1", signing_key_request)
    credential_key, credential_body = calls["create_credential"]
    assert credential_key == "credential-1"
    assert credential_body.org_id is None
    assert credential_body.profile is CredentialProfile.OPERATOR
    assert credential_body.operations == [Operation.GET_APP]
    assert calls["list_logs"] == (
        INVOCATION_ID,
        {
            "cursor": "logs-2",
            "limit": 20,
            "trace_id": "0123456789abcdef0123456789abcdef",
        },
    )
    assert calls["list_memories"] == (
        AGENT_ID,
        {
            "tenant_key": "acme",
            "user_key": "user-1",
            "query": "refund policy",
            "search_mode": MemorySearchMode.HYBRID,
            "kind": MemoryKind.FACT,
            "cursor": "memories-2",
            "limit": 10,
        },
    )
    assert calls["create_session"] is session_request
    assert calls["fork_session"] == (SESSION_ID, fork_request)
    assert calls["delete_session"] == (SESSION_ID, {"force": True})


@pytest.mark.asyncio
async def test_anonymous_access_has_a_credential_free_facade(monkeypatch: Any) -> None:
    import nvoken.client as client_module

    calls: list[tuple[str, str, Any]] = []

    class FakeApiClient:
        def __init__(self, _configuration: Any) -> None:
            pass

        async def __aenter__(self) -> FakeApiClient:
            return self

        async def __aexit__(self, *_args: object) -> None:
            pass

    class FakeAppsApi:
        def __init__(self, _api_client: Any) -> None:
            pass

        async def issue_anonymous_token(
            self,
            app_id: str,
            origin: str,
            request: Any,
        ) -> Any:
            calls.append((app_id, origin, request))
            return SimpleNamespace(visitor_token="visitor-2")

    monkeypatch.setattr(client_module, "ApiClient", FakeApiClient)
    monkeypatch.setattr(client_module, "AppsApi", FakeAppsApi)
    token = await issue_anonymous_token(
        "https://runtime.example.test/",
        "app_1",
        "https://app.example.test",
        visitor_token="visitor-1",
    )
    assert token.visitor_token == "visitor-2"
    assert calls[0][0:2] == ("app_1", "https://app.example.test")
    assert calls[0][2].visitor_token == "visitor-1"


def test_is_not_found_uses_the_authoritative_error_category() -> None:
    assert is_not_found(NvokenError("not_found", "missing", status=404)) is True
    assert is_not_found(SimpleNamespace(status=404, code="not_found")) is False


@pytest.mark.asyncio
async def test_wait_controls_support_actionable_statuses_and_local_timeout() -> None:
    class StatusClient:
        def __init__(self, statuses: list[str]) -> None:
            self.statuses = statuses

        async def get_invocation(self, _invocation_id: str) -> Any:
            status = self.statuses.pop(0) if len(self.statuses) > 1 else self.statuses[0]
            return SimpleNamespace(
                session_id=SESSION_ID,
                agent_id="agent_test",
                status=status,
                deadline_at=None,
            )

    actionable = InvocationHandle(
        StatusClient(["queued", "waiting"]),  # type: ignore[arg-type]
        INVOCATION_ID,
    )
    assert (
        await actionable.wait(
            until="actionable",
            min_poll_interval=0.001,
            max_poll_interval=0.001,
        )
    ).status == "waiting"

    blocked = InvocationHandle(
        StatusClient(["queued"]),  # type: ignore[arg-type]
        INVOCATION_ID,
    )
    with pytest.raises(NvokenError) as timeout:
        await blocked.wait(
            timeout=0.001,
            min_poll_interval=0.001,
            max_poll_interval=0.001,
        )
    assert timeout.value.category == "timeout"


def test_shared_invocation_webhook_fixture_is_expressible_and_stays_a_pointer() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2]
            / "conformance/fixtures/invocation-webhooks-v1.json"
        ).read_text()
    )
    target = _generated_webhook_target(
        WebhookTarget(
            url=fixture["example_request"]["webhook"]["url"],
            events=tuple(fixture["example_request"]["webhook"]["events"]),
        )
    )
    assert target is not None
    assert target.url == fixture["example_request"]["webhook"]["url"]
    assert sorted(target.events or []) == sorted(fixture["events"])

    # Omitting events must stay omitted on the wire. The Runtime applies the
    # complete-set default, and an empty array is a rejected request, so
    # materializing the default here would change what a replay fingerprints
    # against.
    without_events = _generated_webhook_target(
        WebhookTarget(url=fixture["example_request"]["webhook"]["url"])
    )
    assert without_events is not None
    assert without_events.events is None
    assert sorted(fixture["default_events_when_omitted"]) == sorted(fixture["events"])

    with pytest.raises(NvokenError):
        _generated_webhook_target(WebhookTarget(url=""))
    assert _generated_webhook_target(None) is None

    # The payload stays a pointer: nothing the fixture lists as absent may appear
    # in either documented example.
    for name in (
        "example_ended_payload",
        "example_waiting_payload",
        "example_budget_hold_payload",
    ):
        payload = fixture[name]
        assert sorted(payload) == ["invocation", "nvoken"]
        for key in payload["nvoken"]:
            assert key in fixture["payload_fields"]["nvoken"], f"{name} field {key}"
        for key in payload["invocation"]:
            assert key in fixture["payload_fields"]["invocation"], f"{name} field {key}"
        serialized = json.dumps(payload)
        for absent in fixture["payload_absent_fields"]:
            assert absent not in serialized, f"{name} leaked {absent}"

    for rejected in fixture["rejected_events"]:
        assert rejected not in fixture["events"]


def test_shared_model_provider_fixture_stays_expressible_and_unnormalized() -> None:
    fixture = json.loads(
        (Path(__file__).parents[2] / "conformance/fixtures/model-provider-v1.json").read_text()
    )
    transmitted = [
        *fixture["canonical"],
        *fixture["aliases_normalized_by_the_runtime_only"],
        *fixture["rejected_by_the_runtime"],
        fixture["forward_compatible"],
    ]
    # Provider is an open string in the wire contract, so every value survives
    # unchanged, including one this SDK version predates. A Literal or Enum here
    # would fail this assertion rather than fail in production.
    for provider in transmitted:
        model = Model(provider=provider, id="model-id")
        assert asdict(model) == {"provider": provider, "id": "model-id"}
    assert asdict(Model(**fixture["example_model"])) == fixture["example_model"]


def test_shared_invocation_nudge_fixture_pins_the_steering_contract() -> None:
    """Mid-turn steering is one contract across four SDKs and the runtime: the
    status vocabulary a host switches on, the request body it sends, and the
    acknowledgement fields it reads to know where to watch the transcript."""
    fixture = json.loads(
        (
            Path(__file__).parents[2] / "conformance/fixtures/invocation-nudge-v1.json"
        ).read_text()
    )
    assert sorted(fixture["nudge_status"]["values"]) == sorted(
        member.value for member in NudgeStatus
    )
    assert (
        fixture["nudge_status"]["consumed_state"] == NudgeStatus.PENDING.value
    )

    content_only = CreateNudgeRequest(
        content=InvocationInput("focus on the marine segment"),
    )
    assert content_only.to_dict() == fixture["request"]["content_only"]
    with_key = CreateNudgeRequest(
        content=InvocationInput("focus on the marine segment"),
        idempotency_key="nudge-1",
    )
    assert with_key.to_dict() == fixture["request"]["with_idempotency_key"]

    acknowledgement = NudgeAcknowledgement(
        nudge_id="nudge_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
        status=NudgeStatus.PENDING,
        deduplicated=False,
        after_sequence=6,
    )
    assert sorted(acknowledgement.to_dict()) == sorted(fixture["acknowledgement"]["fields"])

    # The drained receipt is what tells a host the model actually saw the input.
    drained = Nudge.from_dict(
        {
            "id": "nudge_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
            "invocation_id": "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
            "status": NudgeStatus.DRAINED.value,
            "content": "focus on the marine segment",
            "created_at": "2026-08-02T09:15:00Z",
            "drained_message_sequence": 7,
        }
    )
    assert drained is not None
    assert getattr(drained, fixture["nudge_status"]["drained_carries"]) == 7


# Recorded context must reach the wire at the top level rather than inside the
# Agent Definition, and every locally checkable bound must be refused before a
# request is spent.
def test_shared_recorded_context_fixture_is_expressible() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2]
            / "conformance/fixtures/recorded-context-v1.json"
        ).read_text()
    )
    limits = fixture["limits"]
    assert limits["items"] == _MAX_CONTEXT_ITEMS
    assert limits["name_characters"] == _MAX_CONTEXT_NAME_LENGTH
    assert limits["content_bytes"] == _MAX_CONTEXT_CONTENT_BYTES
    assert limits["total_content_bytes"] == _MAX_CONTEXT_TOTAL_BYTES
    assert fixture["tiers"] == ["contextual", "operator"]

    client = Client(base_url="https://runtime.example.test", api_key="key")
    accepted = fixture["accepted"]["request"]
    body = client._invocation_body(InvokeRequest(
        agent_key=accepted["agent_key"],
        session_key=accepted["session_key"],
        idempotency_key=accepted["idempotency_key"],
        input=accepted["input"],
        context=tuple(ContextItem(**item) for item in accepted["context"]),
    )).to_dict()
    assert body["context"] == accepted["context"]
    assert "definition" not in body

    # The transcript stores each snapshot as a typed reminder block whose name
    # carries the reserved prefix the request omits.
    for message in fixture["accepted"]["messages"]:
        block = SessionContentBlock.from_dict(message["content"][0])
        reminder = block.actual_instance
        assert isinstance(reminder, ReminderBlock), message["role"]
        assert reminder.name.startswith("app-")
        assert reminder.content

    def refused(items: tuple[ContextItem, ...]) -> bool:
        try:
            client._invocation_body(InvokeRequest(
                agent_key="support",
                input="hello",
                context=items,
            ))
        except NvokenError as error:
            return error.category == "validation"
        return False

    for rejected in fixture["rejected"]:
        if "python" in rejected.get("unrepresentable_in", []):
            continue
        assert refused(
            tuple(ContextItem(**item) for item in rejected["context"])
        ), rejected["id"]

    def item(name: str, content: str) -> ContextItem:
        return ContextItem(name=name, tier="contextual", content=content)

    assert refused(tuple(
        item(f"c{index}", "a") for index in range(limits["items"] + 1)
    )), "too-many-items"
    assert refused(
        (item("a" * (limits["name_characters"] + 1), "x"),)
    ), "oversize-name"
    assert refused(
        (item("customer", "a" * (limits["content_bytes"] + 1)),)
    ), "oversize-content"
    assert refused(tuple(
        item(f"c{index}", "a" * limits["content_bytes"]) for index in range(3)
    )), "oversize-total"


def complete_agent_definition_resource() -> AgentDefinitionResource:
    """One Agent Definition with every writable field populated."""
    return AgentDefinitionResource.from_dict({
        "id": "def_1",
        "definition_key": "support",
        "name": "Billing support",
        "revision": 4,
        "instructions": "Be brief.",
        "model": {"provider": "anthropic", "id": "claude-sonnet-5"},
        "sampling": {"temperature": 0.4},
        "reasoning": {"effort": "high", "budget_tokens": 2048},
        "tool_choice": {"mode": "named", "name": "lookup_invoice"},
        "limits": {"max_iterations": 6, "max_output_tokens": 1024},
        "output_schema": {
            "type": "object",
            "properties": {"answer": {"type": "string"}},
        },
        "tools": [
            {"mode": "builtin", "name": "nvoken_fetch"},
            {
                "mode": "host",
                "name": "lookup_invoice",
                "description": "Look up an invoice.",
                "input_schema": {
                    "type": "object",
                    "properties": {"id": {"type": "string"}},
                },
            },
            {
                "mode": "callback",
                "name": "refund",
                "description": "Issue a refund.",
                "input_schema": {
                    "type": "object",
                    "properties": {"id": {"type": "string"}},
                },
                "callback": {"url": "https://tools.example.test/refund"},
            },
        ],
        "mcp_servers": [{
            "name": "billing",
            "url": "https://mcp.example.test/billing",
            "transport": "streamable_http",
            "allowed_tools": ["search"],
            "timeouts": {"discovery_seconds": 5, "call_seconds": 30},
        }],
        "provider_tools": [{
            "type": "web_search",
            "web_search": {"max_uses": 3, "allowed_domains": ["example.test"]},
        }],
        "memory": {"scope": "user", "context": {"mode": "index", "max_bytes": 1536}},
        "client_interface": {
            "context_names": ["cart"],
            "tool_names": ["lookup_invoice"],
        },
        "created_at": "2026-07-21T12:00:00Z",
        "updated_at": "2026-07-21T12:00:00Z",
        "archived_at": None,
    })


# A replacement replaces the whole resource, so a read-modify-write that drops a
# field is silent data loss rather than a compile error.
def test_read_modify_write_keeps_every_writable_field() -> None:
    client = Client(base_url="https://runtime.example.test", api_key="key")
    current = complete_agent_definition_resource()
    definition = AgentDefinition.from_resource(current)
    written = client._agent_definition_body(
        replace(definition, instructions="Be concise and warm."),
        include_key=False,
    ).to_dict()
    expected = current.to_dict()
    for read_only in ("id", "revision", "definition_key",
                      "created_at", "updated_at", "archived_at"):
        expected.pop(read_only, None)
    expected["instructions"] = "Be concise and warm."
    assert written == expected


# Creation sends the flat definition and its key, and invents no idempotency key:
# one this SDK made up would be new on every attempt and deduplicate nothing.
@pytest.mark.asyncio
async def test_create_agent_definition_sends_the_flat_definition() -> None:
    async with Client("https://runtime.example.test", "key") as client:
        seen: dict[str, Any] = {}

        async def create(body: Any, idempotency_key: str | None = None) -> Any:
            seen["body"] = body.to_dict()
            seen["idempotency_key"] = idempotency_key
            return SimpleNamespace(status_code=201, data=complete_agent_definition_resource())

        client.agent_definitions.create_agent_definition_with_http_info = create
        await client.create_agent_definition(AgentDefinition(
            definition_key="support",
            name="Billing support",
            instructions="Be brief.",
            model=Model(provider="anthropic", id="claude-sonnet-5"),
            client_interface=ClientInterface(context_names=("cart",)),
        ))
        assert seen["idempotency_key"] is None
        assert seen["body"] == {
            "definition_key": "support",
            "name": "Billing support",
            "instructions": "Be brief.",
            "model": {"provider": "anthropic", "id": "claude-sonnet-5"},
            "client_interface": {"context_names": ["cart"]},
        }
        with pytest.raises(NvokenError):
            await client.create_agent_definition(AgentDefinition(
                model=Model(provider="anthropic", id="claude-sonnet-5"),
            ))


def _synced_definition(definition_key: str, revision: int) -> AgentDefinitionResource:
    return AgentDefinitionResource.from_dict({
        "id": f"def_{definition_key}",
        "definition_key": definition_key,
        "name": definition_key,
        "revision": revision,
        "model": {"provider": "anthropic", "id": "claude-sonnet-5"},
        "created_at": "2026-08-17T12:00:00Z",
        "updated_at": "2026-08-17T12:00:00Z",
        "archived_at": None,
    })


def _definition_conflict(code: str, definition_id: str) -> ApiException:
    return ApiException(status=409, body=json.dumps({
        "code": code,
        "message": "definition_key is held by another definition",
        "details": {"definition_id": definition_id},
    }))


# A sync writes and never reads: nvoken decides what moved, because it
# canonicalizes a definition before comparing it and a second copy of that rule
# in the SDK would be free to disagree.
@pytest.mark.asyncio
async def test_sync_definitions_writes_without_reading() -> None:
    async with Client("https://runtime.example.test", "key") as client:
        calls: list[tuple[str, Any]] = []

        async def create(body: Any, idempotency_key: str | None = None) -> Any:
            calls.append(("POST", body.to_dict()))
            # A key the App has never used, then a restatement of one it holds,
            # then one whose contents differ.
            if body.definition_key == "new":
                return SimpleNamespace(status_code=201, data=_synced_definition("new", 1))
            if body.definition_key == "same":
                return SimpleNamespace(status_code=200, data=_synced_definition("same", 3))
            raise _definition_conflict("agent_definition_key_conflict", "def_changed")

        async def update(if_match: str, definition_id: str, body: Any) -> Any:
            calls.append((f"PUT {definition_id} If-Match={if_match}", body.to_dict()))
            return SimpleNamespace(status_code=201, data=_synced_definition("changed", 7))

        client.agent_definitions.create_agent_definition_with_http_info = create
        client.agent_definitions.update_agent_definition_with_http_info = update

        model = Model(provider="anthropic", id="claude-sonnet-5")
        synced = await client.sync_definitions([
            AgentDefinition(definition_key="new", model=model),
            AgentDefinition(definition_key="same", model=model),
            AgentDefinition(definition_key="changed", model=model, instructions="Be warm."),
        ])

        assert [(one.definition_key, one.outcome) for one in synced] == [
            ("new", "created"),
            ("same", "unchanged"),
            ("changed", "updated"),
        ]
        assert synced[2].definition.revision == 7
        # Nothing was read: three creates, and one replacement the conflict
        # addressed with "*", because it proves the resource exists and differs,
        # not which revision it is at.
        assert [call for call, _ in calls] == [
            "POST",
            "POST",
            "POST",
            "PUT def_changed If-Match=*",
        ]
        # A replacement cannot move a resource to another key.
        assert "definition_key" not in calls[3][1]
        assert calls[3][1]["instructions"] == "Be warm."


# Someone else publishing the same contents between the two calls leaves the
# replacement with nothing to publish, which is not an update.
@pytest.mark.asyncio
async def test_sync_definitions_reports_a_raced_replacement_as_unchanged() -> None:
    async with Client("https://runtime.example.test", "key") as client:
        async def create(body: Any, idempotency_key: str | None = None) -> Any:
            raise _definition_conflict("agent_definition_key_conflict", "def_raced")

        async def update(if_match: str, definition_id: str, body: Any) -> Any:
            return SimpleNamespace(status_code=200, data=_synced_definition("support", 2))

        client.agent_definitions.create_agent_definition_with_http_info = create
        client.agent_definitions.update_agent_definition_with_http_info = update

        synced = await client.sync_definitions([
            AgentDefinition(
                definition_key="support",
                model=Model(provider="anthropic", id="claude-sonnet-5"),
            ),
        ])
        assert [one.outcome for one in synced] == ["unchanged"]


# Restoring an archived key is a decision, not a sync step, so it stops the loop
# rather than being skipped past.
@pytest.mark.asyncio
async def test_sync_definitions_stops_at_the_first_error() -> None:
    async with Client("https://runtime.example.test", "key") as client:
        posts = 0

        async def create(body: Any, idempotency_key: str | None = None) -> Any:
            nonlocal posts
            posts += 1
            raise _definition_conflict("agent_definition_archived", "def_archived")

        client.agent_definitions.create_agent_definition_with_http_info = create

        model = Model(provider="anthropic", id="claude-sonnet-5")
        with pytest.raises(NvokenError) as raised:
            await client.sync_definitions([
                AgentDefinition(definition_key="gone", model=model),
                AgentDefinition(definition_key="next", model=model),
            ])
        assert raised.value.code == "agent_definition_archived"
        assert posts == 1


# The mode and the name have to agree, which is the check the flat shape gives up
# in the type system and takes back at the boundary.
def test_tool_choice_names_a_tool_only_in_named_mode() -> None:
    client = Client(base_url="https://runtime.example.test", api_key="key")
    model = Model(provider="anthropic", id="claude-sonnet-5")

    def render(tool_choice: ToolChoice) -> dict[str, Any]:
        return client._agent_definition_body(
            AgentDefinition(model=model, tool_choice=tool_choice),
            include_key=False,
        ).to_dict()

    assert render(ToolChoice(mode="named", name="lookup_invoice"))["tool_choice"] == {
        "mode": "named",
        "name": "lookup_invoice",
    }
    assert render(ToolChoice(mode="auto"))["tool_choice"] == {"mode": "auto"}
    with pytest.raises(NvokenError):
        render(ToolChoice(mode="named"))
    with pytest.raises(NvokenError):
        render(ToolChoice(mode="auto", name="lookup_invoice"))


# A scope is worth nothing if it is only remembered locally, so this asserts the
# headers that actually leave the process — on the pooled client and on the
# streaming one, which is a separate connection with its own header set.
def test_a_scoped_client_stamps_every_request_and_leaves_its_parent_alone() -> None:
    client = Client(base_url="https://runtime.example.test", api_key="key")
    assert "X-Nvoken-Tenant-Key" not in client.api_client.default_headers
    assert "X-Nvoken-User-Key" not in client.api_client.default_headers

    scoped = client.scoped(Scope(tenant_key="acme", user_key="user-7c1f"))
    assert scoped.api_client.default_headers["X-Nvoken-Tenant-Key"] == "acme"
    assert scoped.api_client.default_headers["X-Nvoken-User-Key"] == "user-7c1f"
    assert scoped.stream_api_client.default_headers["X-Nvoken-Tenant-Key"] == "acme"
    assert scoped.stream_client.headers["X-Nvoken-User-Key"] == "user-7c1f"
    assert scoped.scope == Scope(tenant_key="acme", user_key="user-7c1f")

    # The receiver keeps its own scope, so handing a scoped client to one part
    # of an application cannot narrow the administrative one it came from.
    assert client.scope is None
    assert "X-Nvoken-Tenant-Key" not in client.api_client.default_headers

    tenant_only = Client(
        base_url="https://runtime.example.test",
        api_key="key",
        scope=Scope(tenant_key="acme"),
    )
    assert tenant_only.api_client.default_headers["X-Nvoken-Tenant-Key"] == "acme"
    assert "X-Nvoken-User-Key" not in tenant_only.api_client.default_headers

    # An empty scope would stamp nothing while reading as a narrowing, which is
    # the one failure mode a scope cannot have.
    with pytest.raises(NvokenError):
        client.scoped(Scope())
    with pytest.raises(NvokenError):
        client.scoped(Scope(tenant_key="   "))


def client_token_vector() -> dict[str, Any]:
    """The cross-SDK agreement on what a host signs.

    nvoken publishes it, its own verifier accepts the token in it, and every
    SDK mints against it.
    """
    path = Path(__file__).parents[3] / "docs/design/client-token-v1.json"
    return json.loads(path.read_text())


def vector_claims(vector: dict[str, Any]) -> ClientTokenClaims:
    claims = vector["claims"]
    return ClientTokenClaims(
        app_id=claims["iss"],
        key_id=vector["signing_key"]["key_id"],
        subject=claims["sub"],
        tenant_key=claims["tenant_key"],
        agent_key=claims["agent_key"],
        definition_revision=claims["definition_revision"],
        session_id=claims["session_id"],
        operations=list(claims["ops"]),
        issued_at=datetime.fromtimestamp(claims["iat"], timezone.utc),
        lifetime=timedelta(seconds=claims["exp"] - claims["iat"]),
    )


def test_shared_client_token_vector() -> None:
    """Ed25519 signatures are deterministic, so identical claims produce an
    identical token in every language. That is what turns the published token
    from an illustration into a check: nvoken's own verifier accepts this exact
    string in its test suite, so a token equal to it is a token that works.
    """
    vector = client_token_vector()
    seed = base64.b64decode(vector["signing_key"]["private_key_seed"])
    assert mint_client_token(seed, vector_claims(vector)) == vector["token"]


def test_client_token_ceiling_matches_the_published_one() -> None:
    """The vector's list is derived on the server from its route table, so this
    is the one place this SDK's idea of what a browser may do meets the routes
    the runtime actually opens to one.
    """
    vector = client_token_vector()
    assert sorted(all_browser_operations()) == sorted(vector["browser_operation_ceiling"])
    assert CLIENT_TOKEN_LIFETIME_LIMIT.total_seconds() == vector["maximum_lifetime_seconds"]


def test_minting_refuses_what_the_runtime_would_refuse() -> None:
    """nvoken cannot second-guess a signed claim, so every one of these would
    mint cleanly and then fail in a browser, where the failure reads as
    "invalid client token" and says nothing about which claim was wrong.
    """
    vector = client_token_vector()
    seed = base64.b64decode(vector["signing_key"]["private_key_seed"])
    mutations: dict[str, dict[str, Any]] = {
        "blank subject": {"subject": ""},
        "padded subject": {"subject": " user "},
        "oversized subject": {"subject": "u" * 256},
        "no agent": {"agent_key": None},
        "both agents": {"agent_id": "agent_x"},
        "malformed app": {"app_id": "acme"},
        "malformed key": {"key_id": "key-1"},
        "malformed session": {"session_id": "session-1"},
        "negative revision": {"definition_revision": -1},
        "zero lifetime": {"lifetime": timedelta(0)},
        "excessive lifetime": {"lifetime": CLIENT_TOKEN_LIFETIME_LIMIT + timedelta(seconds=1)},
        "unreachable op": {"operations": ["delete_session"]},
        "duplicate op": {"operations": ["get_session", "get_session"]},
        "unscoped operations": {"operations": []},
    }
    for name, changes in mutations.items():
        claims = replace(vector_claims(vector), **changes)
        with pytest.raises(ValueError):
            mint_client_token(seed, claims)
    with pytest.raises(ValueError):
        mint_client_token(b"short", vector_claims(vector))


def test_minting_makes_breadth_deliberate() -> None:
    """nvoken reads an absent ``ops`` as the whole ceiling, which means the most
    permissive token is also the one you get by not thinking about it. Here the
    two are spelled differently, so breadth is something a host chose.
    """
    vector = client_token_vector()
    seed = base64.b64decode(vector["signing_key"]["private_key_seed"])
    with pytest.raises(ValueError, match="all_browser_operations"):
        mint_client_token(seed, replace(vector_claims(vector), operations=[]))
    assert mint_client_token(
        seed, replace(vector_claims(vector), operations=all_browser_operations())
    )
