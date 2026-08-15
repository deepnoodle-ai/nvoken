from __future__ import annotations

import asyncio
import json
import os
from dataclasses import asdict, replace
from datetime import datetime, timezone
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

from nvoken import (
    AgentDefinition,
    ASK_USER_TOOL_NAME,
    AskUserInput,
    AskUserOutput,
    Client,
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
    ProviderKeySelection,
    RetryPolicy,
    Reasoning,
    Sampling,
    SessionOptions,
    Reducer,
    StreamEvent,
    ToolResult,
    ToolChoice,
    ask_user_input_schema,
    ask_user_tool,
    SessionRetention,
    WebSearchLocation,
    WebSearchTool,
    web_search_tool,
    deduplicate_callback_result,
    preflight_output_schema,
    verify_callback,
    fetch_tool,
)

AGENT_ID = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320"
INVOCATION_ID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322"
SESSION_ID = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321"
TOOL_CALL_ID = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325"
WAIT_ID = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328"
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
        agent_definition=AgentDefinition(
            model=model,
            provider_tools=(web_search_tool(),),
        ),
    ))
    assert retention["session_options"] == fixture["session_options"]["retention_only"]
    assert retention["metadata"] == fixture["invocation_metadata"]
    # Execution fields belong to the Agent Definition now. Reading them back
    # through that key is what proves the nesting actually happened.
    assert (
        retention["agent_definition"]["provider_tools"]
        == fixture["provider_tools"]["defaults"]
    )

    every = body(InvokeRequest(
        agent_key="support",
        input="hello",
        session_key="conformance",
        session_options=SessionOptions(
            compaction=ContextCompaction(trigger_tokens=32768),
            retention=SessionRetention(ttl_seconds=3600),
            metadata={"surface": "web"},
        ),
        agent_definition=AgentDefinition(
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
    ))
    assert every["session_options"] == fixture["session_options"]["every_member"]
    assert (
        every["agent_definition"]["provider_tools"]
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
            agent_definition=AgentDefinition(
                model=model,
            ),
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
        agent_definition_id="def_conformance",
    ))
    # Durable options apply on a new anonymous Session too, which is where a
    # short retention window matters most.
    options = InvocationOptions(
        idempotency_key="conformance",
        on_budget_exhausted="pause",
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
            InvocationStatus.PAUSED.value,
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
        agent_definition_id=fixture["agent_definition_id"],
    )).to_dict()
    assert body["agent_definition_id"] == fixture["agent_definition_id"]
    assert "agent_definition" not in body


# Registration and invocation must render the same object, or an SDK could
# resource creation and inline invocation must render the same writable object.
def test_resource_creation_matches_the_agent_definition_an_invocation_nests() -> None:
    fixture = json.loads(
        (
            Path(__file__).parents[2]
            / "conformance/fixtures/agent-definition-reuse-v1.json"
        ).read_text()
    )
    client = Client(base_url="https://runtime.example.test", api_key="key")
    definition = AgentDefinition(
        instructions="You are a concise billing support agent.",
        model=Model(provider="anthropic", id="claude-sonnet-5"),
    )
    creation = client._agent_definition_body(definition).to_dict()
    assert creation == fixture["creation"]["request"]
    body = client._invocation_body(InvokeRequest(
        agent_key="support",
        input="hello",
        idempotency_key="agent-definition-inline",
        agent_definition=definition,
    )).to_dict()
    assert body["agent_definition"] == creation


# A reusable definition is durable configuration, so MCP headers must ride
# alongside it and never within it.
def test_mcp_secrets_stay_outside_the_agent_definition() -> None:
    client = Client(base_url="https://runtime.example.test", api_key="key")
    server = MCPServer(
        name="support",
        url="https://mcp.example.test/rpc",
        allowed_tools=("lookup",),
    )
    request = InvokeRequest(
        agent_key="support",
        input="hello",
        idempotency_key="mcp-secret-placement",
        agent_definition=AgentDefinition(
            model=Model(provider="anthropic", id="claude-sonnet-5"),
            mcp_servers=(server,),
        ),
        mcp_server_headers=(
            MCPServerHeaders(name="support", headers={"Authorization": "Bearer secret"}),
        ),
    )
    body = client._invocation_body(request).to_dict()
    assert "headers" not in body["agent_definition"]["mcp_servers"][0]
    assert body["mcp_server_headers"] == [
        {"name": "support", "headers": {"Authorization": "Bearer secret"}}
    ]

    # A header naming no declared server is a typo the SDK can catch locally.
    with pytest.raises(NvokenError):
        client._invocation_body(replace(
            request,
            mcp_server_headers=(
                MCPServerHeaders(name="typo", headers={"Authorization": "Bearer secret"}),
            ),
        ))


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
                    agent_definition=AgentDefinition(
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
        agents = await client.list_agent_identities(agent_key="support")
        assert agents.items[0].id == AGENT_ID
        agent_identity = await client.get_agent_identity(AGENT_ID)
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
            agent_definition=AgentDefinition(
                instructions="help",
                model=Model(provider="openai", id="gpt-test"),
                sampling=Sampling(temperature=0),
                reasoning=Reasoning(effort="high"),
                mcp_servers=(server,),
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
            "stream.end",
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


@pytest.mark.asyncio
async def test_shared_callback_signing_and_deduplication_vector() -> None:
    path = Path(__file__).parents[3] / "docs/design/callback-signing-v1.json"
    vector = json.loads(path.read_text())
    key = vector["key"].encode()
    body = vector["body"].encode()
    now = datetime.fromtimestamp(vector["now"], timezone.utc)
    verified = verify_callback(key, vector["headers"], body, now=now)
    assert verified.tool_call_id == TOOL_CALL_ID

    mutations = []
    mutations.append((dict(vector["headers"]), body + b" "))
    timestamp = dict(vector["headers"])
    timestamp["X-Nvoken-Timestamp"] = "1784635801"
    mutations.append((timestamp, body))
    delivery = dict(vector["headers"])
    delivery["X-Nvoken-Delivery-ID"] = "different"
    mutations.append((delivery, body))
    signature = dict(vector["headers"])
    signature["X-Nvoken-Signature"] = "sha256=00"
    mutations.append((signature, body))
    for headers, candidate in mutations:
        with pytest.raises(ValueError):
            verify_callback(key, headers, candidate, now=now)

    class Store:
        value: dict[str, bool] | None = None

        async def put_if_absent(
            self,
            _identity: str,
            result: dict[str, bool],
        ) -> tuple[dict[str, bool], bool]:
            if self.value is not None:
                return self.value, False
            self.value = result
            return result, True

    store = Store()
    _, replayed = await deduplicate_callback_result(store, TOOL_CALL_ID, {"ok": True})
    assert replayed is False
    stored, replayed = await deduplicate_callback_result(store, TOOL_CALL_ID, {"ok": False})
    assert replayed is True
    assert stored == {"ok": True}


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
    later_invocation_id = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb399"
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
                "event: stream.end\n"
                f"data: {json.dumps({'type': 'stream.end', 'session_id': SESSION_ID, 'reason': 'idle'})}\n\n"
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
                "agent_id": "agnt_test",
                "status": "queued",
                "deduplicated": False,
                "deadline_at": None,
            })()

        client.invocations.create_invocation = create
        base = {
            "agent_key": "support",
            "input": "hello",
            "agent_definition": AgentDefinition(
                model=Model(provider="openai", id="gpt-test"),
            ),
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

        async def archive_agent_definition(agent_definition_id: str) -> None:
            definition_calls.append(("archive", agent_definition_id))

        async def restore_agent_definition(agent_definition_id: str) -> None:
            definition_calls.append(("restore", agent_definition_id))

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
async def test_wait_controls_support_actionable_statuses_and_local_timeout() -> None:
    class StatusClient:
        def __init__(self, statuses: list[str]) -> None:
            self.statuses = statuses

        async def get_invocation(self, _invocation_id: str) -> Any:
            status = self.statuses.pop(0) if len(self.statuses) > 1 else self.statuses[0]
            return SimpleNamespace(
                session_id=SESSION_ID,
                agent_id="agnt_test",
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
        "example_paused_payload",
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
            "invocation_id": "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322",
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
        agent_definition_id=accepted["agent_definition_id"],
        context=tuple(ContextItem(**item) for item in accepted["context"]),
    )).to_dict()
    assert body["context"] == accepted["context"]
    assert "agent_definition" not in body

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
                agent_definition_id=accepted["agent_definition_id"],
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
