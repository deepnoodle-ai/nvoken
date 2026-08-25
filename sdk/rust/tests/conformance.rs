use std::collections::HashMap;
use std::sync::atomic::{AtomicI64, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, UNIX_EPOCH};

use futures_util::StreamExt;
use http::HeaderMap;
use nvoken::models;
use nvoken::{
    accept_webhook, answerable_tool_calls, ask_user_input_schema, ask_user_tool,
    ask_user_tool_with, fetch_tool, host_tool_calls, mint_client_token, preflight_input_blocks,
    preflight_output_schema, retry_webhook, verify_callback, verify_webhook,
    webhook_status_is_retried, AgentDefinition, AgentInvocationOptions, AgentOptions, AskUserInput,
    AskUserKind, AskUserOutput, BudgetExhaustionBehavior, CallbackOutcome, CallbackReceiver,
    CallbackReply, CallbackResultStore, Client, ClientInterface, ClientTokenClaims,
    CompactionListOptions, ContextCompaction, ContextCompactionTrigger, ContextItem, ContextTier,
    DeliveryError, DeliverySigningKey, ErrorCategory, IfActivePolicy, InvocationSession,
    InvokeRequest, Limits, ListAgentsOptions, ListInvocationsOptions, ListModelsOptions,
    ListSessionsOptions, McpServer, McpServerHeaders, MessageListOptions, Model, NvokenError,
    ProviderKeySelection, ProviderKeySource, ProviderTool, Reasoning, ReasoningEffort, Reducer,
    RetryPolicy, SessionOptions, SessionOptionsConflict, SessionRetention, StreamEvent,
    StreamPreview, ToolCallListOptions, ToolChoice, ToolMode, ToolResult, VerifiedCallback,
    VerifiedWebhook, WaitCondition, WaitOptions, WebSearchLocation, WebSearchTool, WebhookEvent,
    WebhookOutcome, WebhookReceiver, WebhookTarget, ASK_USER_TOOL_NAME,
    CLIENT_TOKEN_LIFETIME_LIMIT,
};
use serde::Deserialize;
use serde_json::{json, Value};

const AGENT_ID: &str = "agent_019b0a12-8d51-7f34-aed2-0e07c1bdb320";
const INVOCATION_ID: &str = "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const SESSION_ID: &str = "sess_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const TOOL_CALL_ID: &str = "call_019b0a12-8d51-7f34-aed2-0e07c1bdb325";
const WAIT_ID: &str = "inv_019b0a12-8d51-7f34-aed2-0e07c1bdb328";
const EXACT_MODEL_ID: &str = "experimental/model?variant=雪%#1";

// The ask_user shape is published in four SDKs plus a fixture the runtime's own
// admission test reads. Five hand-written copies drift, and a host that copies
// the guide's schema into an agent nvoken then rejects gets the worst kind of
// bug report, so each copy is pinned to the fixture here and in the three other
// conformance suites.
#[test]
fn published_ask_user_tool_matches_the_shared_fixture() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/ask-user-tool-v1.json"
    ))
    .unwrap();
    let tool = ask_user_tool();
    assert_eq!(tool.name, fixture["name"].as_str().unwrap());
    assert_eq!(tool.name, ASK_USER_TOOL_NAME);
    assert_eq!(tool.description, fixture["description"].as_str().unwrap());
    assert_eq!(
        Value::Object(ask_user_input_schema().into_iter().collect()),
        fixture["input_schema"]
    );
}

// The typed wrapper must produce exactly the wire shape the guide documents,
// including `canceled` on an answered question — a host UI keying off its
// presence should not see it disappear when the user actually answers.
#[tokio::test]
async fn ask_user_handler_encodes_the_documented_result() {
    let tool = ask_user_tool_with(|question: AskUserInput| async move {
        assert_eq!(question.kind, AskUserKind::Select);
        assert_eq!(question.options.len(), 1);
        AskUserOutput::answer(question.options[0].value.clone())
    });
    let handler = tool.handler.as_ref().expect("typed ask_user has a handler");
    let answer = handler(json!({
        "question": "Which name?",
        "type": "select",
        "options": [{"value": "option-b", "label": "Option B"}],
    }))
    .await
    .unwrap();
    assert_eq!(answer, json!({"response": "option-b", "canceled": false}));

    let rejected = handler(json!({"question": "Which name?", "type": "telepathy"}))
        .await
        .unwrap_err();
    assert_eq!(rejected.type_name, "ask_user_input_invalid");
}

// Session options, host metadata, and provider tools are built by four
// independently written request builders. This pins each of them to the same
// fixture, so a field one binding spells differently fails here rather than
// being silently dropped on the way to the Runtime.
#[test]
fn shared_session_lifecycle_fixture_is_expressible() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/session-lifecycle-v1.json"
    ))
    .unwrap();
    assert_eq!(
        serde_json::to_value(SessionOptions::default().retention(86400)).unwrap(),
        fixture["session_options"]["retention_only"]
    );
    let authorization_context: HashMap<String, String> = [
        ("board".to_owned(), "brand-2026".to_owned()),
        ("trace_id".to_owned(), "018f-4a".to_owned()),
    ]
    .into_iter()
    .collect();
    assert_eq!(
        serde_json::to_value(
            SessionOptions::default().authorization_context(authorization_context)
        )
        .unwrap(),
        fixture["session_options"]["authorization_context_only"]
    );
    let every = SessionOptions::default()
        .compaction(ContextCompaction {
            trigger_tokens: ContextCompactionTrigger::Tokens(32768),
            model: None,
        })
        .retention(3600)
        .authorization_context(
            [("surface".to_owned(), "web".to_owned())]
                .into_iter()
                .collect(),
        )
        .pinned_revision(4)
        .on_conflict(SessionOptionsConflict::Join);
    assert_eq!(
        serde_json::to_value(every).unwrap(),
        fixture["session_options"]["every_member"]
    );

    // Provider tools are converted rather than serialized directly, so the
    // The generated declaration has to match because it reaches the Runtime.
    let defaults = ProviderTool::WebSearch(WebSearchTool::default());
    assert_eq!(
        serde_json::to_value([defaults.generated()]).unwrap(),
        fixture["provider_tools"]["defaults"]
    );
    let configured = ProviderTool::WebSearch(
        WebSearchTool::default()
            .max_uses(5)
            .allowed_domains(vec![
                "example.com".to_owned(),
                "docs.example.com".to_owned(),
            ])
            .user_location(WebSearchLocation {
                city: Some("Austin".to_owned()),
                region: Some("Texas".to_owned()),
                country: Some("US".to_owned()),
                timezone: Some("America/Chicago".to_owned()),
            }),
    );
    assert_eq!(
        serde_json::to_value([configured.generated()]).unwrap(),
        fixture["provider_tools"]["configured"]
    );
}

// The Agent binding is where a host actually spends its time, and it is the
// layer that fell behind the contract in every SDK. Pinning the whole Agent-issued
// body means an option the binding cannot forward is a missing key here.
#[test]
fn shared_agent_request_fixture_is_expressible() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/session-lifecycle-v1.json"
    ))
    .unwrap();
    let expected = &fixture["agent_request"]["web_search_metadata_unbound"];
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let options = AgentOptions::new("support");
    let agent = client.agent(options).unwrap();

    // Durable options apply on a new anonymous Session too, which is where a
    // short retention window matters most.
    let options = AgentInvocationOptions {
        idempotency_key: Some("conformance".to_owned()),
        on_budget_exhausted: Some(BudgetExhaustionBehavior::Hold),
        metadata: Some(
            [
                ("board".to_owned(), "brand-2026".to_owned()),
                ("surface".to_owned(), "web".to_owned()),
            ]
            .into_iter()
            .collect(),
        ),
        retention: Some(SessionRetention { ttl_seconds: 86400 }),
        ..AgentInvocationOptions::default()
    };
    let body = client
        .invocation_body(agent.request("hello".to_owned(), &options))
        .unwrap();
    assert_eq!(&serde_json::to_value(body).unwrap(), expected);

    // Existing Session admissions carry options for equal-or-conflict
    // reconciliation instead of rejecting the pairing in the SDK.
    let bound = AgentInvocationOptions {
        session: Some(InvocationSession::continue_by_id(SESSION_ID)),
        ..options
    };
    let body = client
        .invocation_body(agent.request("hello".to_owned(), &bound))
        .unwrap();
    assert_eq!(
        body.session
            .as_ref()
            .and_then(|session| session.id.as_deref()),
        Some(SESSION_ID)
    );
    assert!(body.retention.is_some());
}

#[test]
fn invocation_trigger_reaches_the_admission_body() {
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let mut request = InvokeRequest::new("support", "hello");
    request.triggered_by = Some(models::InvocationTrigger::new(
        models::invocation_trigger::Type::ToolCall,
        INVOCATION_ID.to_owned(),
        TOOL_CALL_ID.to_owned(),
    ));

    let body = client.invocation_body(request).unwrap();
    let trigger = body.triggered_by.unwrap();
    assert_eq!(trigger.parent_invocation_id, INVOCATION_ID);
    assert_eq!(trigger.tool_call_id, TOOL_CALL_ID);
}

#[test]
fn shared_fetch_builtin_fixture_is_expressible() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/fetch-builtin-v1.json"
    ))
    .unwrap();
    let declaration = fetch_tool();
    assert!(matches!(declaration.mode, ToolMode::Builtin));
    assert_eq!(declaration.name, fixture["declaration"]["name"]);
    assert!(declaration.description.is_empty());
    assert!(declaration.input_schema.is_empty());

    let generated = nvoken::models::ToolDeclaration::Builtin(Box::new(
        nvoken::models::BuiltinToolDeclaration::new(
            nvoken::models::builtin_tool_declaration::Name::NameNvokenFetch,
            nvoken::models::builtin_tool_declaration::Mode::ModeBuiltin,
        ),
    ));
    assert_eq!(
        serde_json::to_value(generated).unwrap(),
        fixture["declaration"]
    );
}

#[test]
fn context_window_failure_fixture_preserves_numeric_details() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/invocation-result.json"
    ))
    .unwrap();
    let failure: nvoken::models::InvocationFailure =
        serde_json::from_value(fixture["context_window_failure"].clone()).unwrap();
    assert_eq!(
        failure.code,
        nvoken::models::invocation_failure::Code::ContextWindowExceeded
    );
    let encoded = serde_json::to_value(failure).unwrap();
    assert_eq!(encoded["details"]["input_tokens"], json!(205321));
    assert_eq!(encoded["details"]["context_window_tokens"], json!(200000));
    assert_eq!(encoded["details"]["requested_output_tokens"], json!(4096));
}

#[test]
fn shared_reasoning_control_fixture_is_expressible() {
    #[derive(Deserialize)]
    struct Fixture {
        efforts: Vec<String>,
        budgets: Vec<u32>,
        omitted: Value,
        combination_error: ErrorFixture,
    }
    #[derive(Deserialize)]
    struct ErrorFixture {
        category: String,
        status: u16,
        code: String,
        message: String,
        details: Value,
    }
    let fixture: Fixture = serde_json::from_str(include_str!(
        "../../conformance/fixtures/reasoning-controls-v1.json"
    ))
    .unwrap();
    let efforts: Vec<Value> = fixture
        .efforts
        .iter()
        .map(|effort| {
            let effort = match effort.as_str() {
                "low" => ReasoningEffort::Low,
                "medium" => ReasoningEffort::Medium,
                "high" => ReasoningEffort::High,
                "xhigh" => ReasoningEffort::XHigh,
                "max" => ReasoningEffort::Max,
                other => panic!("unknown reasoning effort {other}"),
            };
            serde_json::to_value(Reasoning {
                effort: Some(effort),
                budget_tokens: None,
            })
            .unwrap()
        })
        .collect();
    assert_eq!(efforts.len(), 5);
    assert_eq!(fixture.budgets, vec![1024, 2048, 63999]);
    assert_eq!(fixture.omitted, json!({}));
    assert_eq!(fixture.combination_error.category, "validation");
    let normalized = NvokenError {
        category: ErrorCategory::Validation,
        message: fixture.combination_error.message,
        status: Some(fixture.combination_error.status),
        code: Some(fixture.combination_error.code),
        request_id: None,
        retry_after: None,
        details: Some(fixture.combination_error.details),
    };
    assert_eq!(normalized.status, Some(400));
    assert_eq!(normalized.code.as_deref(), Some("invalid_request"));
    assert_eq!(
        normalized.details.as_ref().unwrap()["kind"],
        json!("model_control_combination_unsupported")
    );
    assert_eq!(
        normalized.details.as_ref().unwrap()["fields"],
        json!(["reasoning.budget_tokens", "sampling.temperature"])
    );
}

#[test]
fn shared_tool_choice_fixture_is_expressible() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/tool-choice-v1.json"
    ))
    .unwrap();
    let choices = [
        ToolChoice::Auto,
        ToolChoice::None,
        ToolChoice::Required,
        ToolChoice::Named("lookup".to_string()),
    ];
    let encoded: Vec<Value> = choices
        .into_iter()
        .map(|choice| serde_json::to_value(choice).unwrap())
        .collect();
    assert_eq!(Value::Array(encoded), fixture["choices"]);
}

#[test]
fn shared_media_input_fixture_matches_preflight() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/media-input-v1.json"
    ))
    .unwrap();
    assert_eq!(
        fixture["limits"],
        json!({
            "media_blocks": nvoken::media_preflight::MAX_MEDIA_INPUT_BLOCKS,
            "image_bytes": nvoken::media_preflight::MAX_IMAGE_INPUT_BYTES,
            "document_bytes": nvoken::media_preflight::MAX_DOCUMENT_INPUT_BYTES,
            "media_bytes": nvoken::media_preflight::MAX_MEDIA_INPUT_BYTES,
            "title_characters": nvoken::media_preflight::MAX_MEDIA_TITLE_CHARACTERS,
        })
    );
    assert_eq!(
        fixture["media_types"],
        json!({
            "image": nvoken::media_preflight::IMAGE_MEDIA_TYPES,
            "document": nvoken::media_preflight::DOCUMENT_MEDIA_TYPES,
        })
    );
    for accepted in fixture["accepted"].as_array().unwrap() {
        let blocks = fixture_blocks(&accepted["content"]);
        assert!(
            preflight_input_blocks(&blocks).is_ok(),
            "{}",
            accepted["id"]
        );
        assert_eq!(
            serde_json::to_value(&blocks).unwrap(),
            accepted["content"],
            "{}",
            accepted["id"]
        );
    }
    for rejected in fixture["rejected"].as_array().unwrap() {
        // Cases the enums cannot express are guaranteed by typing instead.
        if rejected["unrepresentable_in"]
            .as_array()
            .is_some_and(|values| values.iter().any(|value| value == "rust"))
        {
            continue;
        }
        let blocks = fixture_blocks(&rejected["content"]);
        let error = preflight_input_blocks(&blocks).expect_err("preflight accepted");
        assert_eq!(
            error.code.as_deref(),
            Some("media_preflight_failed"),
            "{}",
            rejected["id"]
        );
        let details = error.details.as_ref().unwrap();
        assert_eq!(details["kind"], json!("input_media"), "{}", rejected["id"]);
        assert_eq!(
            details["code"], rejected["issue"]["code"],
            "{}",
            rejected["id"]
        );
        assert_eq!(
            details["path"], rejected["issue"]["path"],
            "{}",
            rejected["id"]
        );
        assert_eq!(
            error.message,
            format!(
                "input is invalid: {}",
                rejected["issue"]["message"].as_str().unwrap()
            ),
            "{}",
            rejected["id"]
        );
    }
}

#[test]
fn shared_agent_definition_reuse_fixture_is_expressible() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/agent-definition-reuse-v1.json"
    ))
    .unwrap();
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let body = client
        .invocation_body(InvokeRequest::new("support", "hello"))
        .unwrap();
    assert_eq!(body.agent_key.as_deref(), Some("support"));
    assert!(body.agent_id.is_none());
    assert_eq!(fixture["agent"]["agent_key"], "support");
}

// Resource creation must render the complete writable definition.
#[test]
fn resource_creation_matches_the_agent_definition_an_invocation_nests() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/agent-definition-reuse-v1.json"
    ))
    .unwrap();
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let definition = AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
        .definition_key("support")
        .name("Billing support")
        .instructions("You are a concise billing support agent.");
    let creation = client.agent_definition_body(definition).unwrap();
    assert_eq!(
        serde_json::to_value(&creation).unwrap(),
        fixture["creation"]["request"]
    );
}

// A reusable definition is durable configuration, so MCP headers must ride
// alongside it and never within it.
#[test]
fn mcp_secrets_stay_outside_the_agent_definition() {
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let headers = HashMap::from([("Authorization".to_owned(), "Bearer secret".to_owned())]);
    let request = InvokeRequest::new("support", "hello")
        .mcp_server_headers(McpServerHeaders::new("support", headers.clone()));
    let body = client.invocation_body(request).unwrap();
    assert_eq!(body.mcp_server_headers.unwrap()[0].headers, headers);
}

// fixture_blocks decodes fixture wire blocks into generated input blocks.
fn fixture_blocks(content: &Value) -> Vec<nvoken::models::InputBlock> {
    content
        .as_array()
        .unwrap()
        .iter()
        .map(|block| serde_json::from_value(block.clone()).unwrap())
        .collect()
}

#[test]
fn shared_context_compaction_fixture_is_expressible() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/context-compaction-v1.json"
    ))
    .unwrap();
    let auto = SessionOptions::default().compaction(ContextCompaction {
        trigger_tokens: ContextCompactionTrigger::Auto,
        model: None,
    });
    let explicit = SessionOptions::default().compaction(ContextCompaction {
        trigger_tokens: ContextCompactionTrigger::Tokens(32768),
        model: Some(Model {
            provider: "anthropic".to_owned(),
            id: "claude-sonnet-4-6".to_owned(),
        }),
    });
    assert_eq!(serde_json::to_value(auto).unwrap(), fixture["auto"]);
    assert_eq!(serde_json::to_value(explicit).unwrap(), fixture["explicit"]);
    assert_eq!(
        fixture["errors"][1]["fields"],
        json!([
            "model.provider",
            "session_options.compaction.model.provider"
        ])
    );
}

#[test]
fn shared_output_schema_preflight_fixtures() {
    let fixture: OutputSchemaFixture = serde_json::from_str(include_str!(
        "../../conformance/fixtures/structured-output-schema-v1.json"
    ))
    .unwrap();
    for test_case in fixture.accepted {
        let schema = expand_output_schema_fixture(test_case);
        preflight_output_schema(&schema).unwrap();
    }
    for test_case in fixture.rejected {
        let expected = test_case.issue.clone().unwrap();
        let id = test_case.id.clone();
        let error = preflight_output_schema(&expand_output_schema_fixture(test_case)).unwrap_err();
        assert_eq!(
            error.code.as_deref(),
            Some("schema_preflight_failed"),
            "{id}"
        );
        let details = error.details.unwrap();
        assert_eq!(details["kind"], "output_schema", "{id}");
        assert_eq!(details["code"], expected.code, "{id}");
        assert_eq!(details["path"], expected.path, "{id}");
        assert_eq!(
            details.get("keyword").and_then(Value::as_str),
            expected.keyword.as_deref(),
            "{id}"
        );
    }
}

#[tokio::test]
async fn invoke_preflights_output_schema_before_transport() {
    let client = Client::new("http://127.0.0.1:1", "test-key").unwrap();
    let fixture: OutputSchemaFixture = serde_json::from_str(include_str!(
        "../../conformance/fixtures/structured-output-schema-v1.json"
    ))
    .unwrap();
    for test_case in fixture.rejected {
        let id = test_case.id.clone();
        let request = InvokeRequest::new("support", "help")
            .output_schema(expand_output_schema_fixture(test_case));
        let error = match client.invoke(request).await {
            Ok(_) => panic!("{id}: invalid schema was admitted"),
            Err(error) => error,
        };
        assert_eq!(error.category, ErrorCategory::Validation, "{id}");
        assert_eq!(
            error.code.as_deref(),
            Some("schema_preflight_failed"),
            "{id}"
        );
    }
}

#[test]
fn request_builders_cover_core_admission_types() {
    let mut output_schema = HashMap::new();
    output_schema.insert("type".to_owned(), json!("object"));
    let request = InvokeRequest::new("support", "hello")
        .model(Model::new("openai", "gpt-test"))
        .limits(Limits {
            max_iterations: Some(4),
            ..Limits::default()
        })
        .output_schema(output_schema)
        .tenant_key("acme")
        .session(InvocationSession::continue_or_create("ticket-42"))
        .idempotency_key("request-key")
        .provider_key(ProviderKeySelection {
            provider: "openai".to_owned(),
            source: ProviderKeySource::AppByok,
        });

    let overrides = request.overrides.as_ref().unwrap();
    assert_eq!(overrides.model.as_ref().unwrap().id, "gpt-test");
    assert_eq!(overrides.limits.as_ref().unwrap().max_iterations, Some(4));
    assert_eq!(
        request
            .session
            .as_ref()
            .and_then(|session| session.key.as_deref()),
        Some("ticket-42")
    );
    assert_eq!(request.provider_keys.len(), 1);
}

#[derive(Clone, Deserialize)]
struct OutputSchemaFixture {
    accepted: Vec<OutputSchemaFixtureCase>,
    rejected: Vec<OutputSchemaFixtureCase>,
}

#[derive(Clone, Deserialize)]
struct OutputSchemaFixtureCase {
    id: String,
    schema: Option<HashMap<String, Value>>,
    repeat: Option<OutputSchemaRepeat>,
    generate: Option<OutputSchemaGenerate>,
    issue: Option<OutputSchemaIssue>,
}

#[derive(Clone, Deserialize)]
struct OutputSchemaRepeat {
    path: String,
    character: String,
    count: usize,
}

#[derive(Clone, Deserialize)]
struct OutputSchemaGenerate {
    kind: String,
    depth: usize,
}

#[derive(Clone, Deserialize)]
struct OutputSchemaIssue {
    code: String,
    path: String,
    keyword: Option<String>,
}

fn expand_output_schema_fixture(test_case: OutputSchemaFixtureCase) -> HashMap<String, Value> {
    if let Some(generate) = test_case.generate {
        assert_eq!(generate.kind, "nested-object");
        let mut node = json!({"type": "string"});
        for _ in 1..generate.depth {
            node = json!({
                "type": "object",
                "properties": {"child": node},
                "required": ["child"],
            });
        }
        return node.as_object().unwrap().clone().into_iter().collect();
    }
    let mut schema = Value::Object(test_case.schema.unwrap().into_iter().collect());
    if let Some(repeat) = test_case.repeat {
        *schema.pointer_mut(&repeat.path).unwrap() =
            Value::String(repeat.character.repeat(repeat.count));
    }
    schema.as_object().unwrap().clone().into_iter().collect()
}

#[tokio::test]
async fn shared_fault_server_semantics() {
    let Ok(base_url) = std::env::var("NVOKEN_CONFORMANCE_URL") else {
        return;
    };
    reqwest::Client::new()
        .post(format!("{base_url}/__test/reset"))
        .send()
        .await
        .unwrap();
    let client = Client::with_retry_policy(
        &base_url,
        "test-key",
        RetryPolicy {
            max_attempts: 3,
            min_delay: Duration::from_millis(1),
            max_delay: Duration::from_millis(5),
        },
    )
    .unwrap();
    let result_fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/invocation-result.json"
    ))
    .unwrap();
    let tool_call_fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/tool-call-records-v1.json"
    ))
    .unwrap();
    let expected_output_text = result_fixture["message_join"]["expected_output_text"]
        .as_str()
        .unwrap();
    let agents = client
        .list_agents(ListAgentsOptions {
            agent_key: Some("support".to_owned()),
            ..Default::default()
        })
        .await
        .unwrap();
    assert_eq!(agents.items[0].id, AGENT_ID);
    let agent = client.get_agent(AGENT_ID).await.unwrap();
    assert_eq!(agent.agent_key, "support");
    let models = client
        .list_models(ListModelsOptions::default())
        .await
        .unwrap();
    assert_eq!(models.catalog_version, "conformance-catalog-v1");
    assert_eq!(
        models
            .items
            .iter()
            .find(|model| model.id == "future-model")
            .unwrap()
            .provider,
        "future_provider"
    );
    assert!(
        models.items[0]
            .controls
            .as_ref()
            .unwrap()
            .sampling
            .temperature
    );
    assert_eq!(
        models.items[0]
            .controls
            .as_ref()
            .unwrap()
            .reasoning
            .effort
            .values
            .len(),
        5
    );
    let server = McpServer::new("support", "https://mcp.example.test/rpc").allowed_tool("lookup");
    let mcp_secret = HashMap::from([(
        "Authorization".to_owned(),
        "Bearer conformance-mcp-secret".to_owned(),
    )]);
    let mcp_tools = client
        .list_mcp_tools(&server, Some(mcp_secret.clone()))
        .await
        .unwrap();
    assert_eq!(mcp_tools.tools[0].projected_name, "support__lookup");
    let exact_model = client
        .get_model(&Model {
            provider: "openai".to_owned(),
            id: EXACT_MODEL_ID.to_owned(),
        })
        .await
        .unwrap();
    assert_eq!(exact_model.id, EXACT_MODEL_ID);
    assert!(!exact_model.cataloged);
    assert_eq!(
        exact_model.pricing.status,
        nvoken::models::model_pricing::Status::Unpriced
    );
    let credits_fixture: Value =
        serde_json::from_str(include_str!("../../conformance/fixtures/credits-v1.json")).unwrap();
    let mut request = models::AllocateCreditsRequest::new(
        models::Money::new("25.000000".to_owned(), models::money::Currency::Usd),
        "rust-credits-conformance".to_owned(),
    );
    request.default_tenant = Some(true);
    let allocation = client.allocate_credits(request).await.unwrap();
    assert_eq!(
        allocation.allocation.id,
        credits_fixture["allocation"]["id"]
    );
    let accounts = client
        .list_credit_accounts(None, Some(true), None, None)
        .await
        .unwrap();
    assert_eq!(
        accounts.items[0].available.amount,
        credits_fixture["account"]["available"]["amount"]
    );
    let allocations = client
        .list_credit_allocations(None, Some(true), None, None)
        .await
        .unwrap();
    assert_eq!(allocations.items[0].id, credits_fixture["allocation"]["id"]);
    let handle = client
        .invoke(InvokeRequest {
            agent_id: None,
            agent_key: Some("support".to_owned()),
            tenant_key: None,
            user_key: None,
            session: Some(
                InvocationSession::continue_by_id(SESSION_ID).if_active(IfActivePolicy::Supersede),
            ),
            retention: None,
            compaction: None,
            authorization_context: None,
            triggered_by: None,
            idempotency_key: Some("rust-lost-ack".to_owned()),
            on_budget_exhausted: None,
            metadata: None,
            input: "hello".to_owned(),
            input_blocks: Vec::new(),
            definition_revision: None,
            overrides: None,
            mcp_server_headers: vec![McpServerHeaders::new("support", mcp_secret)],
            context: Vec::new(),
            provider_keys: vec![ProviderKeySelection {
                provider: "openai".to_owned(),
                source: ProviderKeySource::CallerEphemeral {
                    api_key: "conformance-secret".to_owned(),
                },
            }],
            webhook: None,
        })
        .await
        .unwrap();
    let tool_calls = handle
        .list_tool_calls(ToolCallListOptions {
            limit: Some(4),
            ..Default::default()
        })
        .await
        .unwrap();
    assert_eq!(
        serde_json::to_value(tool_calls).unwrap(),
        tool_call_fixture["tool_calls"]
    );
    assert_eq!(handle.invocation_id, INVOCATION_ID);
    assert_eq!(handle.session_id.as_deref(), Some(SESSION_ID));

    let mut resumed = client.invocation(INVOCATION_ID);
    resumed.refresh().await.unwrap();
    assert_eq!(
        resumed.status,
        Some(nvoken::models::InvocationStatus::Completed)
    );

    let mut waiting = client.invocation(WAIT_ID);
    let actionable = waiting
        .wait_with_options(WaitOptions {
            until: WaitCondition::Actionable,
            timeout: Some(Duration::from_millis(50)),
            min_poll_interval: Duration::from_millis(1),
            max_poll_interval: Duration::from_millis(2),
        })
        .await
        .unwrap();
    assert_eq!(actionable.status, nvoken::models::InvocationStatus::Waiting);
    let timeout = waiting
        .wait_with_options(WaitOptions {
            timeout: Some(Duration::from_millis(10)),
            min_poll_interval: Duration::from_millis(1),
            max_poll_interval: Duration::from_millis(2),
            ..WaitOptions::default()
        })
        .await
        .unwrap_err();
    assert_eq!(timeout.category, ErrorCategory::Timeout);

    let first_page = client
        .list_invocations(ListInvocationsOptions {
            agent_key: Some("support".to_owned()),
            statuses: vec![
                nvoken::models::InvocationStatus::Queued,
                nvoken::models::InvocationStatus::Running,
                nvoken::models::InvocationStatus::Queued,
            ],
            ..Default::default()
        })
        .await
        .unwrap();
    assert!(first_page.has_more);
    assert_eq!(
        first_page.next_cursor.as_deref(),
        Some("invocations-page-2")
    );
    let second_page = client
        .list_invocations(ListInvocationsOptions {
            agent_key: Some("support".to_owned()),
            statuses: vec![
                nvoken::models::InvocationStatus::Waiting,
                nvoken::models::InvocationStatus::Queued,
                nvoken::models::InvocationStatus::Running,
            ],
            cursor: first_page.next_cursor,
            ..Default::default()
        })
        .await
        .unwrap();
    assert!(!second_page.has_more);
    let sessions = client
        .list_sessions(ListSessionsOptions {
            agent_key: Some("support".to_owned()),
            ..Default::default()
        })
        .await
        .unwrap();
    // These fields are audience-restricted on the one Session schema, so they
    // are optional and nullable: absent for a browser grant, present for this
    // machine credential. Unwrapping both layers is the assertion that a
    // machine caller still receives them.
    let usage = sessions.items[0]
        .usage
        .clone()
        .flatten()
        .expect("machine usage");
    assert_eq!(usage.input_tokens, 9);
    assert_eq!(
        sessions.items[0].agent_id.clone().flatten().as_deref(),
        Some(AGENT_ID)
    );
    let context = sessions.items[0]
        .context
        .clone()
        .flatten()
        .expect("machine context");
    assert_eq!(context.estimated_tokens, 12);
    assert_eq!(context.context_window_tokens, Some(128000));
    assert_eq!(context.model.provider, "openai");
    assert_eq!(context.model.id, "gpt-test");
    let messages = client
        .list_session_messages(SESSION_ID, MessageListOptions::default())
        .await
        .unwrap();
    assert_eq!(messages.next_cursor.as_deref(), Some("messages-page-2"));
    let newest_first = client
        .list_session_messages(
            SESSION_ID,
            MessageListOptions {
                order: Some(nvoken::ListOrder::Descending),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(
        newest_first
            .items
            .iter()
            .map(|message| message.sequence)
            .collect::<Vec<_>>(),
        vec![2]
    );
    assert_eq!(
        newest_first.next_cursor.as_deref(),
        Some("messages-page-2-desc")
    );
    let compactions = client
        .list_session_compactions(SESSION_ID, CompactionListOptions::default())
        .await
        .unwrap();
    assert_eq!(
        compactions.items[0].status,
        nvoken::models::SessionCompactionStatus::Applied
    );
    assert_eq!(
        compactions.items[0].summary.as_deref(),
        Some("The user chose the durable option.")
    );

    let mut result_handle = handle.clone();
    let composed = result_handle.result().await.unwrap();
    assert_eq!(composed.invocation.id, INVOCATION_ID);
    assert_eq!(
        composed.invocation.status,
        nvoken::models::InvocationStatus::Completed
    );
    assert_eq!(composed.messages.len(), 3);
    assert_eq!(
        composed.messages[0].role,
        nvoken::models::SessionMessageRole::User
    );
    assert_eq!(
        composed.messages[1].role,
        nvoken::models::SessionMessageRole::Assistant
    );
    assert_eq!(
        composed.messages[2].role,
        nvoken::models::SessionMessageRole::Assistant
    );
    let structured = composed.invocation.structured_output.as_ref().unwrap();
    assert_eq!(structured.get("answer"), Some(&json!("world")));
    assert!(composed.invocation.structured_output_provenance.is_some());
    assert_eq!(composed.output_text.as_deref(), Some(expected_output_text));
    assert_eq!(
        result_handle.output_text().await.unwrap(),
        composed.output_text.clone().unwrap()
    );
    assert_eq!(result_handle.list_messages().await.unwrap().len(), 3);

    let stream_handle = client.invocation(INVOCATION_ID);
    let stream = stream_handle.stream_with_options(nvoken::StreamOptions {
        deltas: Some(false),
    });
    futures_util::pin_mut!(stream);
    let mut event_types = Vec::new();
    event_types.push(stream.next().await.unwrap().unwrap().event_type);
    let result = stream_handle
        .submit_tool_results(vec![ToolResult {
            tool_call_id: TOOL_CALL_ID.to_owned(),
            content: json!({"ok": true}),
            is_error: false,
        }])
        .await
        .unwrap();
    assert!(result.results[0].deduplicated);
    assert_eq!(
        stream_handle.cancel().await.unwrap().status,
        nvoken::models::InvocationStatus::Cancelled
    );
    let interrupted = stream_handle.interrupt().await.unwrap();
    assert_eq!(
        interrupted.status,
        nvoken::models::InvocationStatus::Completed
    );
    assert_eq!(
        interrupted.stop_reason,
        Some(nvoken::models::InvocationStopReason::Interrupted)
    );
    assert_eq!(interrupted.attempt, 1);
    while let Some(item) = stream.next().await {
        event_types.push(item.unwrap().event_type);
    }

    assert_error(&client, "conflict", ErrorCategory::Conflict, 409).await;
    assert_error(
        &client,
        "unauthenticated",
        ErrorCategory::Authentication,
        401,
    )
    .await;
    assert_error(&client, "forbidden", ErrorCategory::Permission, 403).await;
    assert_eq!(
        client.get_invocation("rate-limit").await.unwrap().status,
        nvoken::models::InvocationStatus::Completed
    );
    assert_error(&client, "rate-limit-always", ErrorCategory::RateLimit, 429).await;
    assert_error(&client, "server-error", ErrorCategory::Server, 503).await;
    let mut failed = client.invocation("failed");
    let local_error = failed.wait_for_result(None).await.unwrap_err();
    assert_eq!(local_error.category, ErrorCategory::Conflict);
    assert_eq!(local_error.status, None);

    // One stream, filtered to one turn: a dropped connection, a rotation, and
    // then the frame carrying the terminal change, which is where the read
    // ends. Nothing announces that the turn is over except the change itself.
    assert_eq!(
        event_types,
        vec![
            "transcript.update",
            "transcript.update",
            "connection.closing",
            "transcript.update",
            "transcript.update",
        ]
    );

    let state = reqwest::get(format!("{base_url}/__test/state"))
        .await
        .unwrap()
        .json::<ServerState>()
        .await
        .unwrap();
    assert_eq!(state.admission_attempts, 2);
    assert_eq!(state.credential_admissions, 2);
    assert_eq!(state.result_attempts, 2);
    assert_eq!(state.cancel_attempts, 1);
    assert_eq!(state.interrupt_attempts, 1);
    assert_eq!(state.stream_attempts, 3);
    assert_eq!(state.last_resume_cursor, "cursor-1");
    assert_eq!(state.last_resume_source, "last_event_id");
    assert_eq!(state.last_statuses, vec!["waiting", "queued", "running"]);
    assert_eq!(state.last_deltas, "false");
    assert_eq!(state.last_invocation_filter, INVOCATION_ID);
}

#[test]
fn shared_settlement_legibility_fixture_pins_the_stop_reasons() {
    #[derive(Deserialize)]
    struct StopReasonFixture {
        values: Vec<nvoken::models::InvocationStopReason>,
        present_only_on_statuses: Vec<nvoken::models::InvocationStatus>,
    }
    #[derive(Deserialize)]
    struct MessagePhaseFixture {
        values: Vec<nvoken::models::MessagePhase>,
    }
    #[derive(Deserialize)]
    struct SettlementFixture {
        terminal_statuses: Vec<nvoken::models::InvocationStatus>,
        stop_reason: StopReasonFixture,
        message_phase: MessagePhaseFixture,
    }

    let fixture: SettlementFixture = serde_json::from_str(
        &std::fs::read_to_string("../conformance/fixtures/settlement-legibility-v1.json").unwrap(),
    )
    .unwrap();
    assert_eq!(
        fixture.stop_reason.values,
        vec![
            nvoken::models::InvocationStopReason::EndTurn,
            nvoken::models::InvocationStopReason::Interrupted,
            nvoken::models::InvocationStopReason::MaxIterations,
            nvoken::models::InvocationStopReason::Deadline,
            nvoken::models::InvocationStopReason::MaxOutputTokens,
            nvoken::models::InvocationStopReason::MaxEstimatedCost,
            nvoken::models::InvocationStopReason::InsufficientCredits,
        ]
    );
    assert_eq!(
        fixture.message_phase.values,
        vec![
            nvoken::models::MessagePhase::Commentary,
            nvoken::models::MessagePhase::FinalAnswer,
        ]
    );
    assert_eq!(
        fixture.stop_reason.present_only_on_statuses,
        vec![
            nvoken::models::InvocationStatus::Completed,
            nvoken::models::InvocationStatus::Incomplete,
            nvoken::models::InvocationStatus::BudgetHold,
        ]
    );
    // The wait helpers stop at exactly these statuses; a terminal the SDK does
    // not recognize is a wait that never returns.
    assert_eq!(
        fixture.terminal_statuses,
        vec![
            nvoken::models::InvocationStatus::Completed,
            nvoken::models::InvocationStatus::Incomplete,
            nvoken::models::InvocationStatus::Failed,
            nvoken::models::InvocationStatus::Cancelled,
        ]
    );
}

#[test]
fn shared_reducer_vector() {
    let fixture: ReducerFixture = serde_json::from_str(
        &std::fs::read_to_string("../conformance/fixtures/reducer.json").unwrap(),
    )
    .unwrap();
    let mut reducer = Reducer::default();
    for event in &fixture.events {
        reducer
            .apply(&StreamEvent {
                id: Some(event.id.clone()),
                event_type: event.event.clone(),
                data: event.data.clone(),
                retry: None,
            })
            .unwrap();
    }
    let snapshot = reducer.snapshot();
    assert_eq!(
        snapshot
            .messages
            .iter()
            .map(|message| message.sequence)
            .collect::<Vec<_>>(),
        fixture.expected.message_sequences
    );
    assert_eq!(
        snapshot
            .invocation_changes
            .iter()
            .map(|change| change.revision)
            .collect::<Vec<_>>(),
        fixture.expected.invocation_revisions
    );
    assert_eq!(
        snapshot.cursor.as_deref(),
        Some(fixture.expected.cursor.as_str())
    );
    assert_eq!(snapshot.previews, fixture.expected.previews);
    for preview_case in fixture.preview_cases {
        let mut preview_reducer = Reducer::default();
        for event in preview_case.events {
            preview_reducer
                .apply(&StreamEvent {
                    id: Some(event.id),
                    event_type: event.event,
                    data: event.data,
                    retry: None,
                })
                .unwrap();
        }
        assert_eq!(
            preview_reducer.snapshot().previews,
            preview_case.expected_previews,
            "{}",
            preview_case.name
        );
    }
}

#[derive(Deserialize)]
struct ReducerFixture {
    events: Vec<ReducerEvent>,
    preview_cases: Vec<ReducerPreviewCase>,
    expected: ReducerExpected,
}

#[derive(Deserialize)]
struct ReducerPreviewCase {
    name: String,
    events: Vec<ReducerEvent>,
    expected_previews: Vec<StreamPreview>,
}

#[derive(Deserialize)]
struct ReducerEvent {
    id: String,
    event: String,
    data: Value,
}

#[derive(Deserialize)]
struct ReducerExpected {
    message_sequences: Vec<u64>,
    invocation_revisions: Vec<u64>,
    cursor: String,
    previews: Vec<StreamPreview>,
}

async fn assert_error(client: &Client, id: &str, category: ErrorCategory, status: u16) {
    let error = client.get_invocation(id).await.unwrap_err();
    assert_eq!(error.category, category);
    assert_eq!(error.status, Some(status));
    assert!(error.request_id.is_some());
    if category == ErrorCategory::RateLimit {
        assert_eq!(error.retry_after, Some(Duration::from_secs(1)));
    }
}

#[derive(Deserialize)]
struct ServerState {
    admission_attempts: u32,
    credential_admissions: u32,
    result_attempts: u32,
    cancel_attempts: u32,
    interrupt_attempts: u32,
    stream_attempts: u32,
    last_resume_cursor: String,
    last_resume_source: String,
    last_statuses: Vec<String>,
    last_deltas: String,
    last_invocation_filter: String,
}

#[tokio::test]
async fn shared_callback_signing_vector() {
    let document = delivery_signing_vectors();
    let vector = &document.vectors.callback;
    let key = document.key.as_bytes();
    let headers = header_map(&vector.headers);
    let now = UNIX_EPOCH + Duration::from_secs(document.now);
    let verified = verify_callback(key, &headers, vector.body.as_bytes(), now).unwrap();
    assert_eq!(verified.tool_call_id, TOOL_CALL_ID);
    // The name is inside the signed body, so a receiver dispatches on it
    // without an authoritative read and without trusting a URL suffix.
    assert_eq!(verified.tool_name, vector.tool_name);
    assert_eq!(verified.envelope.nvoken.tool_name, vector.tool_name);

    let signature_error =
        verify_callback(key, &headers, format!("{} ", vector.body).as_bytes(), now).unwrap_err();
    assert!(matches!(signature_error, DeliveryError::SignatureMismatch));
    for (name, value) in HEADER_TAMPERINGS {
        let mut tampered = headers.clone();
        tampered.insert(name, value.parse().unwrap());
        assert!(verify_callback(key, &tampered, vector.body.as_bytes(), now).is_err());
    }

    // The authorization context is a sibling of `nvoken`, not a member, and the
    // vector is what holds four SDKs to that placement. The input repeats one of
    // its keys on purpose: a receiver may check that the two agree, and may
    // never read the board out of the input alone, because the model wrote it.
    assert!(!vector.authorization_context.is_empty());
    assert_eq!(verified.authorization_context, vector.authorization_context);
    assert_eq!(
        verified.envelope.authorization_context,
        vector.authorization_context
    );
    assert_eq!(
        verified.envelope.input["board"].as_str(),
        verified
            .authorization_context
            .get("board")
            .map(String::as_str)
    );
}

/// Every row of the discipline the receiver owns.
///
/// The receiver's whole job is the answer it gives nvoken, since that answer
/// decides whether a ToolCall settles, retries, or fails for good. Driving it
/// against the published vector rather than a body written for the test is what
/// keeps the two from being separately right.
#[tokio::test]
async fn callback_receiver_reply_discipline() {
    let document = delivery_signing_vectors();
    let vector = &document.vectors.callback;
    let headers = header_map(&vector.headers);
    let body = vector.body.as_bytes();
    let now = UNIX_EPOCH + Duration::from_secs(document.now);
    let key = || {
        DeliverySigningKey::new(
            vector.headers["X-Nvoken-Signing-Key-ID"].clone(),
            1,
            document.key.clone(),
        )
    };

    let ran = Arc::new(AtomicUsize::new(0));
    let counted = Arc::clone(&ran);
    let receiver = CallbackReceiver::builder(vec![key()])
        .tool(
            vector.tool_name.clone(),
            move |delivery: &VerifiedCallback| {
                let counted = Arc::clone(&counted);
                // Authorization comes off the signed sibling, never off the input.
                let board = delivery.authorization_context.get("board").cloned();
                async move {
                    counted.fetch_add(1, Ordering::SeqCst);
                    assert_eq!(board.as_deref(), Some("brd_9f21"));
                    nvoken::callback_result(json!({"ticket": "T-1042"}), false)
                        .map_err(|error| error.to_string())
                }
            },
        )
        .store(MemoryStore::default())
        .build()
        .unwrap();

    let settled = receiver.handle(&headers, body, now).await;
    assert_eq!(settled.outcome, CallbackOutcome::Settled);
    assert_eq!(settled.reply.status, 200);
    assert_eq!(
        settled.delivery.as_ref().map(|d| d.tool_call_id.as_str()),
        Some(TOOL_CALL_ID)
    );

    // A redelivery answers from the store. The tool must not run twice: it would
    // repeat every effect it had, which is the whole reason the store exists.
    let replayed = receiver.handle(&headers, body, now).await;
    assert_eq!(replayed.outcome, CallbackOutcome::Replayed);
    assert_eq!(replayed.reply, settled.reply);
    assert_eq!(ran.load(Ordering::SeqCst), 1);

    // An identity this receiver does not hold is refused permanently, and an
    // unconfigured one is not: only one of the two is the sender's fault, and
    // they are indistinguishable to nvoken unless the status separates them.
    let rotated = CallbackReceiver::builder(vec![DeliverySigningKey::new(
        vector.headers["X-Nvoken-Signing-Key-ID"].clone(),
        2,
        document.key.clone(),
    )])
    .build()
    .unwrap();
    let unknown = rotated.handle(&headers, body, now).await;
    assert_eq!((unknown.reply.status, unknown.reason), (401, "unknown_key"));

    let unconfigured = CallbackReceiver::builder(vec![]).build().unwrap();
    let answered = unconfigured.handle(&headers, body, now).await;
    assert_eq!(
        (answered.reply.status, answered.reason),
        (503, "not_configured")
    );

    let no_tool = CallbackReceiver::builder(vec![key()]).build().unwrap();
    let refused = no_tool.handle(&headers, body, now).await;
    assert_eq!(
        (refused.reply.status, refused.reason),
        (400, "unknown_tool")
    );

    // An `Err` is the receiver failing, not the tool. The tool failing settles
    // the call with is_error, which the model can read and correct itself
    // against.
    let broken = CallbackReceiver::builder(vec![key()])
        .tool(vector.tool_name.clone(), |_: &VerifiedCallback| async {
            Err::<CallbackReply, String>("database unreachable".to_owned())
        })
        .build()
        .unwrap();
    let failed = broken.handle(&headers, body, now).await;
    assert_eq!(
        (failed.reply.status, failed.reason),
        (503, "handler_failed")
    );
    assert!(failed.delivery.is_some());

    let tool_error = CallbackReceiver::builder(vec![key()])
        .tool(vector.tool_name.clone(), |_: &VerifiedCallback| async {
            nvoken::callback_result(json!({"error": "no such ticket"}), true)
                .map_err(|error| error.to_string())
        })
        .build()
        .unwrap();
    let settled_error = tool_error.handle(&headers, body, now).await;
    assert_eq!(settled_error.outcome, CallbackOutcome::Settled);
    assert!(settled_error
        .reply
        .body
        .as_deref()
        .unwrap_or_default()
        .contains("\"is_error\":true"));

    // A version that cannot be read as a positive integer fails the build rather
    // than a live delivery, where the refusal would be permanent.
    assert!(CallbackReceiver::builder(vec![DeliverySigningKey::new(
        vector.headers["X-Nvoken-Signing-Key-ID"].clone(),
        0,
        document.key.clone(),
    )])
    .build()
    .is_err());
    assert!(CallbackReceiver::builder(vec![key(), key()])
        .build()
        .is_err());
    assert!(CallbackReceiver::builder(vec![DeliverySigningKey::new(
        vector.headers["X-Nvoken-Signing-Key-ID"].clone(),
        1,
        "too short",
    )])
    .build()
    .is_err());
}

/// Where the webhook receiver differs from its callback twin: nvoken ignores
/// the body, an unhandled event is not a failure, and ordering stays the host's.
#[tokio::test]
async fn webhook_receiver_reply_discipline() {
    let document = delivery_signing_vectors();
    let vector = &document.vectors.webhook;
    let headers = header_map(&vector.headers);
    let body = vector.body.as_bytes();
    let now = UNIX_EPOCH + Duration::from_secs(document.now);
    let key = || {
        DeliverySigningKey::new(
            vector.headers["X-Nvoken-Signing-Key-ID"].clone(),
            1,
            document.key.clone(),
        )
    };

    let applied = Arc::new(AtomicI64::new(0));
    let folded = Arc::clone(&applied);
    let handled = WebhookReceiver::builder(vec![key()])
        .event(vector.event, move |delivery: &VerifiedWebhook| {
            let folded = Arc::clone(&folded);
            // The fold the host owns: only a delivery that advances the
            // Invocation is applied, and the comparison belongs in its own
            // transaction.
            let sequence = delivery.sequence;
            let supersedes = delivery.supersedes(folded.load(Ordering::SeqCst));
            async move {
                if supersedes {
                    folded.store(sequence, Ordering::SeqCst);
                }
                Ok(())
            }
        })
        .build()
        .unwrap()
        .handle(&headers, body, now)
        .await;
    assert_eq!(handled.outcome, WebhookOutcome::Handled);
    assert_eq!(handled.reply.status, 200);
    assert_eq!(applied.load(Ordering::SeqCst), vector.sequence);

    // An event with no handler is still a delivery. Retrying it would spend
    // nvoken's bounded attempts reaching the same absent handler.
    let ignored = WebhookReceiver::builder(vec![key()])
        .build()
        .unwrap()
        .handle(&headers, body, now)
        .await;
    assert_eq!(ignored.outcome, WebhookOutcome::Ignored);
    assert_eq!(ignored.reason, "unhandled_event");
    assert!(!webhook_status_is_retried(ignored.reply.status));

    let failed = WebhookReceiver::builder(vec![key()])
        .event(vector.event, |_: &VerifiedWebhook| async {
            Err::<(), String>("store unreachable".to_owned())
        })
        .build()
        .unwrap()
        .handle(&headers, body, now)
        .await;
    assert_eq!(failed.outcome, WebhookOutcome::Failed);
    assert_eq!(failed.reason, "handler_failed");
    assert!(webhook_status_is_retried(failed.reply.status));

    // The callback key must not verify a webhook and the reverse, which is what
    // the two purposes are for.
    let crossed = WebhookReceiver::builder(vec![DeliverySigningKey::new(
        document.vectors.callback.headers["X-Nvoken-Signing-Key-ID"].clone(),
        1,
        document.key.clone(),
    )])
    .build()
    .unwrap()
    .handle(&headers, body, now)
    .await;
    assert_eq!((crossed.reply.status, crossed.reason), (401, "unknown_key"));
}

/// The callback vector's twin, and the point of having both: the same key, the
/// same canonical string, the same tampering set, a different verifier. A
/// scheme that drifted apart for one delivery kind would fail here rather than
/// at an integrator who believed the promise that there is only one.
#[tokio::test]
async fn shared_webhook_signing_vector() {
    let document = delivery_signing_vectors();
    let vector = &document.vectors.webhook;
    let key = document.key.as_bytes();
    let headers = header_map(&vector.headers);
    let now = UNIX_EPOCH + Duration::from_secs(document.now);
    let verified = verify_webhook(key, &headers, vector.body.as_bytes(), now).unwrap();
    assert_eq!(verified.event, vector.event);
    assert_eq!(verified.sequence, vector.sequence);
    assert_eq!(verified.invocation_id, INVOCATION_ID);
    assert_eq!(verified.session_id, SESSION_ID);

    let signature_error =
        verify_webhook(key, &headers, format!("{} ", vector.body).as_bytes(), now).unwrap_err();
    assert!(matches!(signature_error, DeliveryError::SignatureMismatch));
    for (name, value) in HEADER_TAMPERINGS {
        let mut tampered = headers.clone();
        tampered.insert(name, value.parse().unwrap());
        assert!(verify_webhook(key, &tampered, vector.body.as_bytes(), now).is_err());
    }

    // A webhook's key is the App's webhook-purpose key and a callback's is its
    // callback key. Nothing in the wire format says which is which, so a
    // receiver that crossed them would verify nothing; each vector must refuse
    // the other's verifier.
    let callback = &document.vectors.callback;
    assert!(verify_webhook(
        key,
        &header_map(&callback.headers),
        callback.body.as_bytes(),
        now,
    )
    .is_err());
    assert!(verify_callback(key, &headers, vector.body.as_bytes(), now).is_err());

    // Delivery is at least once and a redelivery can land after a later
    // transition, so folding is by sequence rather than by arrival.
    assert!(verified.supersedes(vector.sequence - 1));
    assert!(!verified.supersedes(vector.sequence));
    assert!(!webhook_status_is_retried(accept_webhook().status));
    assert!(webhook_status_is_retried(retry_webhook().status));
}

/// The cross-SDK agreement on how nvoken signs a delivery. One file holds both
/// kinds because there is one scheme; a vector each is what makes that testable
/// rather than merely stated.
#[derive(Deserialize)]
struct DeliverySigningVectors {
    key: String,
    now: u64,
    vectors: DeliveryVectors,
}

#[derive(Deserialize)]
struct DeliveryVectors {
    callback: CallbackVector,
    webhook: WebhookVector,
}

#[derive(Deserialize)]
struct CallbackVector {
    tool_name: String,
    authorization_context: HashMap<String, String>,
    headers: HashMap<String, String>,
    body: String,
}

#[derive(Deserialize)]
struct WebhookVector {
    event: models::WebhookEvent,
    sequence: i64,
    headers: HashMap<String, String>,
    body: String,
}

fn delivery_signing_vectors() -> DeliverySigningVectors {
    serde_json::from_str(
        &std::fs::read_to_string("../../docs/design/delivery-signing-v1.json").unwrap(),
    )
    .unwrap()
}

/// The mutations the vector file names, minus the body one, which each test
/// applies itself. Each must be refused by both verifiers, since neither the
/// signature nor its binding to a delivery id and a timestamp is particular to
/// a delivery kind.
const HEADER_TAMPERINGS: [(&str, &str); 3] = [
    ("x-nvoken-timestamp", "1784635801"),
    ("x-nvoken-delivery-id", "different"),
    ("x-nvoken-signature", "sha256=00"),
];

#[derive(Deserialize)]
struct ToolCallModeFixture {
    tool_calls: Vec<models::ToolCallSummary>,
    answerable: Vec<String>,
    host: Vec<String>,
}

/// Answerable is wider than mine once an App declares callback tools: nvoken
/// delivers those itself, yet a machine credential may still settle one that a
/// receiver acknowledged, so it carries arguments like any pending host call.
/// Every SDK draws the same line at the same place, against one fixture.
#[test]
fn shared_tool_call_mode_partition() {
    let fixture: ToolCallModeFixture = serde_json::from_str(
        &std::fs::read_to_string("../conformance/fixtures/tool-call-modes-v1.json").unwrap(),
    )
    .unwrap();
    let ids = |calls: Vec<&models::ToolCallSummary>| {
        calls
            .into_iter()
            .map(|call| call.id.clone())
            .collect::<Vec<_>>()
    };
    assert_eq!(
        ids(answerable_tool_calls(Some(&fixture.tool_calls))),
        fixture.answerable
    );
    assert_eq!(
        ids(host_tool_calls(Some(&fixture.tool_calls))),
        fixture.host
    );
}

fn header_map(values: &HashMap<String, String>) -> HeaderMap {
    values
        .iter()
        .map(|(name, value)| {
            (
                http::HeaderName::from_bytes(name.as_bytes()).unwrap(),
                value.parse().unwrap(),
            )
        })
        .collect()
}

#[derive(Default)]
struct MemoryStore {
    replies: Mutex<HashMap<String, CallbackReply>>,
}

#[async_trait::async_trait]
impl CallbackResultStore for MemoryStore {
    async fn find(&self, tool_call_id: &str) -> Result<Option<CallbackReply>, String> {
        let replies = self.replies.lock().map_err(|error| error.to_string())?;
        Ok(replies.get(tool_call_id).cloned())
    }

    async fn put_if_absent(
        &self,
        tool_call_id: &str,
        reply: CallbackReply,
    ) -> Result<(CallbackReply, bool), String> {
        let mut replies = self.replies.lock().map_err(|error| error.to_string())?;
        if let Some(existing) = replies.get(tool_call_id) {
            return Ok((existing.clone(), false));
        }
        replies.insert(tool_call_id.to_owned(), reply.clone());
        Ok((reply, true))
    }
}

/// The webhook target must be expressible from the Rust facade, and the
/// payload every SDK documents must stay a pointer to authoritative state rather
/// than a copy of it.
#[test]
fn shared_invocation_webhook_fixture_is_expressible_and_stays_a_pointer() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/invocation-webhooks-v1.json"
    ))
    .unwrap();
    let url = fixture["example_request"]["webhook"]["url"]
        .as_str()
        .unwrap();

    let target = WebhookTarget::new(url)
        .event(WebhookEvent::Ended)
        .event(WebhookEvent::Waiting)
        .event(WebhookEvent::BudgetHold);
    assert_eq!(target.url, url);
    assert_eq!(target.events.len(), 3);

    let mut generated = models::WebhookTarget::new(url.to_string());
    generated.events = Some(vec![
        models::WebhookEvent::WebhookEventEnded,
        models::WebhookEvent::WebhookEventWaiting,
        models::WebhookEvent::WebhookEventBudgetHold,
    ]);
    let encoded = serde_json::to_value(&generated).unwrap();
    assert_eq!(encoded["url"], Value::String(url.to_string()));
    let mut encoded_events: Vec<String> = encoded["events"]
        .as_array()
        .unwrap()
        .iter()
        .map(|value| value.as_str().unwrap().to_string())
        .collect();
    encoded_events.sort();
    let mut declared: Vec<String> = fixture["events"]
        .as_array()
        .unwrap()
        .iter()
        .map(|value| value.as_str().unwrap().to_string())
        .collect();
    declared.sort();
    assert_eq!(encoded_events, declared);

    // Omitting events must stay omitted on the wire. The Runtime applies the
    // complete-set default, and an empty array is a rejected request, so
    // materializing the default here would change what a replay fingerprints
    // against.
    let without_events = models::WebhookTarget::new(url.to_string());
    let encoded = serde_json::to_value(&without_events).unwrap();
    assert!(encoded.get("events").is_none());
    let mut defaults: Vec<String> = fixture["default_events_when_omitted"]
        .as_array()
        .unwrap()
        .iter()
        .map(|value| value.as_str().unwrap().to_string())
        .collect();
    defaults.sort();
    assert_eq!(defaults, declared);

    // The payload stays a pointer: nothing the fixture lists as absent may
    // appear in either documented example.
    for name in ["example_ended_payload", "example_waiting_payload"] {
        let payload = fixture[name].as_object().unwrap();
        let mut keys: Vec<&String> = payload.keys().collect();
        keys.sort();
        assert_eq!(keys, vec!["invocation", "nvoken"]);
        for section in ["nvoken", "invocation"] {
            let allowed = fixture["payload_fields"][section].as_array().unwrap();
            for key in payload[section].as_object().unwrap().keys() {
                assert!(
                    allowed.contains(&Value::String(key.clone())),
                    "{name} has unexpected {section} field {key}"
                );
            }
        }
        let serialized = serde_json::to_string(payload).unwrap();
        for absent in fixture["payload_absent_fields"].as_array().unwrap() {
            let absent = absent.as_str().unwrap();
            assert!(!serialized.contains(absent), "{name} leaked {absent}");
        }
    }

    for rejected in fixture["rejected_events"].as_array().unwrap() {
        assert!(!fixture["events"].as_array().unwrap().contains(rejected));
    }
}

/// Provider identity is an open string, so every value in the shared fixture must
/// survive serialization unchanged — including a provider added after this SDK
/// version. A Rust enum here would fail to compile against the fixture rather
/// than fail at runtime, which is the point of asserting it.
#[test]
fn shared_model_provider_fixture_stays_expressible_and_unnormalized() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/model-provider-v1.json"
    ))
    .unwrap();
    let mut transmitted: Vec<String> = fixture["canonical"]
        .as_array()
        .unwrap()
        .iter()
        .map(|value| value.as_str().unwrap().to_string())
        .collect();
    transmitted.extend(
        fixture["aliases_normalized_by_the_runtime_only"]
            .as_object()
            .unwrap()
            .keys()
            .cloned(),
    );
    transmitted.extend(
        fixture["rejected_by_the_runtime"]
            .as_array()
            .unwrap()
            .iter()
            .map(|value| value.as_str().unwrap().to_string()),
    );
    transmitted.push(fixture["forward_compatible"].as_str().unwrap().to_string());
    for provider in transmitted {
        let model = Model::new(provider.clone(), "model-id");
        let encoded = serde_json::to_value(&model).unwrap();
        assert_eq!(encoded["provider"], Value::String(provider));
    }
    let example = Model::new(
        fixture["example_model"]["provider"].as_str().unwrap(),
        fixture["example_model"]["id"].as_str().unwrap(),
    );
    assert_eq!(
        serde_json::to_value(&example).unwrap(),
        fixture["example_model"]
    );
}

/// Mid-turn steering is one contract across four SDKs and the runtime: the
/// status vocabulary a host matches on, the request body it sends, and the
/// acknowledgement fields it reads to know where to watch the transcript.
#[test]
fn shared_invocation_nudge_fixture_pins_the_steering_contract() {
    #[derive(Deserialize)]
    struct RequestFixture {
        content_only: Value,
        with_idempotency_key: Value,
    }
    #[derive(Deserialize)]
    struct AcknowledgementFixture {
        fields: Vec<String>,
    }
    #[derive(Deserialize)]
    struct StatusFixture {
        values: Vec<models::NudgeStatus>,
        consumed_state: models::NudgeStatus,
        drained_carries: String,
    }
    #[derive(Deserialize)]
    struct NudgeFixture {
        request: RequestFixture,
        acknowledgement: AcknowledgementFixture,
        nudge_status: StatusFixture,
    }

    let fixture: NudgeFixture = serde_json::from_str(
        &std::fs::read_to_string("../conformance/fixtures/invocation-nudge-v1.json").unwrap(),
    )
    .unwrap();
    assert_eq!(
        fixture.nudge_status.values,
        vec![
            models::NudgeStatus::Pending,
            models::NudgeStatus::Drained,
            models::NudgeStatus::Expired,
            models::NudgeStatus::Cancelled,
        ]
    );
    assert_eq!(
        fixture.nudge_status.consumed_state,
        models::NudgeStatus::Pending
    );

    let content_only = models::CreateNudgeRequest::new(models::InvocationInput::String(
        "focus on the marine segment".to_string(),
    ));
    assert_eq!(
        serde_json::to_value(&content_only).unwrap(),
        fixture.request.content_only
    );
    let mut with_key = models::CreateNudgeRequest::new(models::InvocationInput::String(
        "focus on the marine segment".to_string(),
    ));
    with_key.idempotency_key = Some("nudge-1".to_string());
    assert_eq!(
        serde_json::to_value(&with_key).unwrap(),
        fixture.request.with_idempotency_key
    );

    let acknowledgement = models::NudgeAcknowledgement::new(
        "nudge_019b0a12-8d51-7f34-aed2-0e07c1bdb330".to_string(),
        models::NudgeStatus::Pending,
        false,
        6,
    );
    let encoded = serde_json::to_value(&acknowledgement).unwrap();
    let mut encoded_fields: Vec<String> = encoded.as_object().unwrap().keys().cloned().collect();
    encoded_fields.sort();
    let mut expected_fields = fixture.acknowledgement.fields.clone();
    expected_fields.sort();
    assert_eq!(encoded_fields, expected_fields);

    // The drained receipt is what tells a host the model actually saw the input.
    let drained: models::Nudge = serde_json::from_value(json!({
        "id": "nudge_019b0a12-8d51-7f34-aed2-0e07c1bdb330",
        "invocation_id": INVOCATION_ID,
        "status": "drained",
        "content": "focus on the marine segment",
        "created_at": "2026-08-02T09:15:00Z",
        "drained_message_sequence": 7,
    }))
    .unwrap();
    let encoded_drained = serde_json::to_value(&drained).unwrap();
    assert_eq!(
        encoded_drained[&fixture.nudge_status.drained_carries],
        json!(7)
    );
}

// Recorded context must reach the wire at the top level rather than inside the
// Agent Definition, and every locally checkable bound must be refused before a
// request is spent.
#[test]
fn shared_recorded_context_fixture_is_expressible() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/recorded-context-v1.json"
    ))
    .unwrap();
    let limits = &fixture["limits"];
    let items_limit = limits["items"].as_u64().unwrap() as usize;
    let name_limit = limits["name_characters"].as_u64().unwrap() as usize;
    let content_limit = limits["content_bytes"].as_u64().unwrap() as usize;
    assert_eq!(fixture["tiers"], json!(["contextual", "operator"]));

    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let accepted = &fixture["accepted"]["request"];
    let mut request = InvokeRequest::new(
        accepted["agent_key"].as_str().unwrap(),
        accepted["input"].as_str().unwrap(),
    )
    .session(InvocationSession::continue_or_create(
        accepted["session_key"].as_str().unwrap(),
    ))
    .idempotency_key(accepted["idempotency_key"].as_str().unwrap());
    for item in accepted["context"].as_array().unwrap() {
        request = request.context(fixture_context_item(item));
    }
    let body = client.invocation_body(request).unwrap();
    assert_eq!(
        serde_json::to_value(body.context.as_ref().unwrap()).unwrap(),
        accepted["context"]
    );
    assert!(body.overrides.is_none());

    // The transcript stores each snapshot as a typed reminder block whose name
    // carries the reserved prefix the request omits.
    for message in fixture["accepted"]["messages"].as_array().unwrap() {
        let block: models::SessionContentBlock =
            serde_json::from_value(message["content"][0].clone()).unwrap();
        let models::SessionContentBlock::Reminder(reminder) = block else {
            panic!("{} content is not a reminder", message["role"]);
        };
        assert!(reminder.name.starts_with("app-"));
        assert!(!reminder.content.is_empty());
    }

    let refused = |context: Vec<ContextItem>| {
        let mut request = InvokeRequest::new("support", "hello");
        request.context = context;
        matches!(
            client.invocation_body(request),
            Err(error) if error.category == ErrorCategory::Validation
        )
    };
    for rejected in fixture["rejected"].as_array().unwrap() {
        let id = rejected["id"].as_str().unwrap();
        if rejected["unrepresentable_in"]
            .as_array()
            .is_some_and(|langs| langs.iter().any(|lang| lang == "rust"))
        {
            continue;
        }
        let context = rejected["context"]
            .as_array()
            .unwrap()
            .iter()
            .map(fixture_context_item)
            .collect();
        assert!(refused(context), "{id}");
    }
    let item =
        |name: String, content: String| ContextItem::new(name, ContextTier::Contextual, content);
    assert!(
        refused(
            (0..=items_limit)
                .map(|index| item(format!("c{index}"), "a".to_owned()))
                .collect()
        ),
        "too-many-items"
    );
    assert!(
        refused(vec![item("a".repeat(name_limit + 1), "x".to_owned())]),
        "oversize-name"
    );
    assert!(
        refused(vec![item(
            "customer".to_owned(),
            "a".repeat(content_limit + 1)
        )]),
        "oversize-content"
    );
    assert!(
        refused(
            (0..3)
                .map(|index| item(format!("c{index}"), "a".repeat(content_limit)))
                .collect()
        ),
        "oversize-total"
    );
}

fn fixture_context_item(item: &Value) -> ContextItem {
    ContextItem::new(
        item["name"].as_str().unwrap(),
        match item["tier"].as_str().unwrap() {
            "operator" => ContextTier::Operator,
            _ => ContextTier::Contextual,
        },
        item["content"].as_str().unwrap(),
    )
}

fn complete_agent_definition_resource() -> models::AgentDefinitionResource {
    serde_json::from_value(json!({
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
        "output_schema": {"type": "object", "properties": {"answer": {"type": "string"}}},
        "tools": [
            {"mode": "builtin", "name": "nvoken_fetch"},
            {
                "mode": "host",
                "name": "lookup_invoice",
                "description": "Look up an invoice.",
                "input_schema": {"type": "object", "properties": {"id": {"type": "string"}}}
            },
            {
                "mode": "callback",
                "name": "refund",
                "description": "Issue a refund.",
                "input_schema": {"type": "object", "properties": {"id": {"type": "string"}}},
                "callback": {"url": "https://tools.example.test/refund"}
            }
        ],
        "mcp_servers": [{
            "name": "billing",
            "url": "https://mcp.example.test/billing",
            "transport": "streamable_http",
            "allowed_tools": ["search"],
            "timeouts": {"discovery_seconds": 5, "call_seconds": 30}
        }],
        "provider_tools": [{
            "type": "web_search",
            "web_search": {"max_uses": 3, "allowed_domains": ["example.test"]}
        }],
        "memory": {"scope": "user", "context": {"mode": "index", "max_bytes": 1536}},
        "client_interface": {"context_names": ["cart"], "tool_names": ["lookup_invoice"]},
        "created_at": "2026-07-21T12:00:00Z",
        "updated_at": "2026-07-21T12:00:00Z",
        "archived_at": null
    }))
    .unwrap()
}

// A replacement replaces the whole resource, so a read-modify-write that drops
// a field is silent data loss rather than a compile error.
#[test]
fn read_modify_write_keeps_every_writable_field() {
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let current = complete_agent_definition_resource();
    let mut definition = AgentDefinition::from_resource(&current).unwrap();
    definition.instructions = Some("Be concise and warm.".to_string());
    let mut written = client.agent_definition_body(definition).unwrap();
    // What `update_agent_definition` drops on the way to a replacement.
    written.definition_key = None;

    let mut expected = serde_json::to_value(&current).unwrap();
    let object = expected.as_object_mut().unwrap();
    for read_only in [
        "id",
        "revision",
        "definition_key",
        "created_at",
        "updated_at",
        "archived_at",
    ] {
        object.remove(read_only);
    }
    object.insert("instructions".to_string(), json!("Be concise and warm."));
    assert_eq!(serde_json::to_value(&written).unwrap(), expected);
}

// Creation sends the flat definition and its key, and the definition key is the
// one field a replacement must not carry.
#[test]
fn creation_sends_the_flat_definition_and_its_key() {
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let definition = AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
        .definition_key("support")
        .name("Billing support")
        .instructions("Be brief.")
        .client_interface(ClientInterface::default().context_name("cart"));
    let body = client.agent_definition_body(definition).unwrap();
    assert_eq!(
        serde_json::to_value(&body).unwrap(),
        json!({
            "definition_key": "support",
            "name": "Billing support",
            "instructions": "Be brief.",
            "model": {"provider": "anthropic", "id": "claude-sonnet-5"},
            "client_interface": {"context_names": ["cart"]}
        })
    );
}

// An empty client interface opts a definition into client tokens with no
// client-authored context or tools, so it must not be mistaken for omission.
#[test]
fn an_empty_client_interface_reaches_the_wire() {
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let body = client
        .agent_definition_body(
            AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
                .client_interface(ClientInterface::default()),
        )
        .unwrap();
    assert_eq!(
        serde_json::to_value(&body).unwrap()["client_interface"],
        json!({})
    );
}

/// The cross-SDK agreement on what a host signs. nvoken publishes it, its own
/// verifier accepts the token in it, and every SDK mints against it.
#[derive(serde::Deserialize)]
struct ClientTokenVector {
    signing_key: ClientTokenSigningKey,
    claims: ClientTokenVectorClaims,
    token: String,
    maximum_lifetime_seconds: u64,
}

#[derive(serde::Deserialize)]
struct ClientTokenSigningKey {
    key_id: String,
    private_key_seed: String,
}

#[derive(serde::Deserialize)]
struct ClientTokenVectorClaims {
    iss: String,
    sub: String,
    iat: u64,
    exp: u64,
    tenant_key: String,
    agent_key: String,
    definition_revision: i64,
    session_id: String,
}

fn client_token_vector() -> ClientTokenVector {
    serde_json::from_str(
        &std::fs::read_to_string("../../docs/design/client-token-v1.json").unwrap(),
    )
    .unwrap()
}

fn client_token_seed(vector: &ClientTokenVector) -> Vec<u8> {
    use base64::Engine as _;
    base64::engine::general_purpose::STANDARD
        .decode(&vector.signing_key.private_key_seed)
        .unwrap()
}

fn client_token_claims(vector: &ClientTokenVector) -> ClientTokenClaims {
    ClientTokenClaims {
        app_id: vector.claims.iss.clone(),
        key_id: vector.signing_key.key_id.clone(),
        subject: vector.claims.sub.clone(),
        tenant_key: Some(vector.claims.tenant_key.clone()),
        agent_id: None,
        agent_key: Some(vector.claims.agent_key.clone()),
        definition_revision: Some(vector.claims.definition_revision),
        session_id: Some(vector.claims.session_id.clone()),
        issued_at: Some(std::time::UNIX_EPOCH + std::time::Duration::from_secs(vector.claims.iat)),
        lifetime: std::time::Duration::from_secs(vector.claims.exp - vector.claims.iat),
    }
}

/// Ed25519 signatures are deterministic, so identical claims produce an
/// identical token in every language. That is what turns the published token
/// from an illustration into a check: nvoken's own verifier accepts this exact
/// string in its test suite, so a token equal to it is a token that works.
#[test]
fn shared_client_token_vector() {
    let vector = client_token_vector();
    let minted = mint_client_token(&client_token_seed(&vector), &client_token_claims(&vector))
        .expect("mint the published claims");
    assert_eq!(minted, vector.token);
}

#[test]
fn client_token_lifetime_matches_the_published_one() {
    let vector = client_token_vector();
    assert_eq!(
        CLIENT_TOKEN_LIFETIME_LIMIT.as_secs(),
        vector.maximum_lifetime_seconds
    );
}

/// nvoken cannot second-guess a signed claim, so every one of these would mint
/// cleanly and then fail in a browser, where the failure reads as "invalid
/// client token" and says nothing about which claim was wrong.
#[test]
fn minting_refuses_what_the_runtime_would_refuse() {
    let vector = client_token_vector();
    let seed = client_token_seed(&vector);
    let mutations: Vec<(&str, fn(&mut ClientTokenClaims))> = vec![
        ("blank subject", |claims| claims.subject = String::new()),
        ("padded subject", |claims| {
            claims.subject = " user ".to_string()
        }),
        ("oversized subject", |claims| {
            claims.subject = "u".repeat(256)
        }),
        ("no agent", |claims| claims.agent_key = None),
        ("both agents", |claims| {
            claims.agent_id = Some("agent_x".to_string())
        }),
        ("malformed app", |claims| claims.app_id = "acme".to_string()),
        ("malformed key", |claims| {
            claims.key_id = "key-1".to_string()
        }),
        ("malformed session", |claims| {
            claims.session_id = Some("session-1".to_string())
        }),
        ("negative revision", |claims| {
            claims.definition_revision = Some(-1)
        }),
        ("zero lifetime", |claims| {
            claims.lifetime = std::time::Duration::ZERO
        }),
        ("excessive lifetime", |claims| {
            claims.lifetime = CLIENT_TOKEN_LIFETIME_LIMIT + std::time::Duration::from_secs(1)
        }),
    ];
    for (name, mutate) in mutations {
        let mut claims = client_token_claims(&vector);
        mutate(&mut claims);
        assert!(
            mint_client_token(&seed, &claims).is_err(),
            "minted a grant the runtime refuses: {name}"
        );
    }
    assert!(mint_client_token(b"short", &client_token_claims(&vector)).is_err());
}
