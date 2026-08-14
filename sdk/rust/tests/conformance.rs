use std::collections::HashMap;
use std::sync::Mutex;
use std::time::{Duration, UNIX_EPOCH};

use futures_util::StreamExt;
use http::HeaderMap;
use nvoken::models;
use nvoken::{
    ask_user_input_schema, ask_user_tool, ask_user_tool_with, deduplicate_callback_result,
    fetch_tool, preflight_input_blocks, preflight_output_schema, verify_callback, AgentDefinition,
    AgentInvocationOptions, AgentOptions, AskUserInput, AskUserKind, AskUserOutput,
    BudgetExhaustionBehavior, CallbackError, CallbackResultStore, Client, CompactionListOptions,
    ContextCompaction, ContextCompactionTrigger, ContextItem, ContextTier, ErrorCategory,
    IfActivePolicy, InvokeRequest, Limits, ListAgentsOptions, ListInvocationsOptions,
    ListModelsOptions, ListSessionsOptions, McpServer, McpServerHeaders, MessageListOptions, Model,
    NvokenError, ProviderKeySelection, ProviderKeySource, ProviderTool, Reasoning, ReasoningEffort,
    Reducer, RetryPolicy, Sampling, SessionOptions, StreamEvent, StreamPreview, Tool,
    ToolCallListOptions, ToolChoice, ToolMode, ToolResult, WaitCondition, WaitOptions,
    WebSearchLocation, WebSearchTool, WebhookEvent, WebhookTarget, ASK_USER_TOOL_NAME,
};
use serde::Deserialize;
use serde_json::{json, Value};

const AGENT_ID: &str = "agnt_019b0a12-8d51-7f34-aed2-0e07c1bdb320";
const INVOCATION_ID: &str = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const SESSION_ID: &str = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const TOOL_CALL_ID: &str = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325";
const WAIT_ID: &str = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328";
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
    let metadata: HashMap<String, String> = [
        ("board".to_owned(), "brand-2026".to_owned()),
        ("trace_id".to_owned(), "018f-4a".to_owned()),
    ]
    .into_iter()
    .collect();
    assert_eq!(
        serde_json::to_value(SessionOptions::default().metadata(metadata)).unwrap(),
        fixture["session_options"]["metadata_only"]
    );
    let every = SessionOptions::default()
        .compaction(ContextCompaction {
            trigger_tokens: ContextCompactionTrigger::Tokens(32768),
            model: None,
        })
        .retention(3600)
        .metadata(
            [("surface".to_owned(), "web".to_owned())]
                .into_iter()
                .collect(),
        );
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
    let options = AgentOptions::from_definition_id("support", "def_conformance");
    let agent = client.agent(options).unwrap();

    // Durable options apply on a new anonymous Session too, which is where a
    // short retention window matters most.
    let options = AgentInvocationOptions {
        idempotency_key: Some("conformance".to_owned()),
        on_budget_exhausted: Some(BudgetExhaustionBehavior::Pause),
        metadata: Some(
            [
                ("board".to_owned(), "brand-2026".to_owned()),
                ("surface".to_owned(), "web".to_owned()),
            ]
            .into_iter()
            .collect(),
        ),
        session_options: Some(SessionOptions::default().retention(86400)),
        ..AgentInvocationOptions::default()
    };
    let body = client
        .invocation_body(agent.request("hello".to_owned(), &options))
        .unwrap();
    assert_eq!(&serde_json::to_value(body).unwrap(), expected);

    // Existing Session admissions carry options for equal-or-conflict
    // reconciliation instead of rejecting the pairing in the SDK.
    let bound = AgentInvocationOptions {
        session_id: Some(SESSION_ID.to_owned()),
        ..options
    };
    let body = client
        .invocation_body(agent.request("hello".to_owned(), &bound))
        .unwrap();
    assert_eq!(body.session_id.as_deref(), Some(SESSION_ID));
    assert!(body.session_options.is_some());
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
        .invocation_body(InvokeRequest::from_agent_definition(
            "support",
            "hello",
            fixture["agent_definition_id"].as_str().unwrap(),
        ))
        .unwrap();
    assert_eq!(
        body.agent_definition_id.as_deref(),
        fixture["agent_definition_id"].as_str()
    );
    assert!(body.agent_definition.is_none());
}

// Resource creation and inline invocation must render the same writable
// object, or one SDK path could silently drop configuration.
#[test]
fn resource_creation_matches_the_agent_definition_an_invocation_nests() {
    let fixture: Value = serde_json::from_str(include_str!(
        "../../conformance/fixtures/agent-definition-reuse-v1.json"
    ))
    .unwrap();
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let definition = AgentDefinition::new(Model::new("anthropic", "claude-sonnet-5"))
        .instructions("You are a concise billing support agent.");
    let creation = client.agent_definition_body(definition.clone()).unwrap();
    assert_eq!(
        serde_json::to_value(&creation).unwrap(),
        fixture["creation"]["request"]
    );
    let body = client
        .invocation_body(
            InvokeRequest::without_model("support", "hello").agent_definition(definition),
        )
        .unwrap();
    assert_eq!(
        serde_json::to_value(body.agent_definition.unwrap()).unwrap(),
        serde_json::to_value(&creation).unwrap()
    );
}

// A reusable definition is durable configuration, so MCP headers must ride
// alongside it and never within it.
#[test]
fn mcp_secrets_stay_outside_the_agent_definition() {
    let client = Client::new("https://runtime.example.test", "key").unwrap();
    let server = McpServer::new("support", "https://mcp.example.test/rpc").allowed_tool("lookup");
    let headers = HashMap::from([("Authorization".to_owned(), "Bearer secret".to_owned())]);
    let request = InvokeRequest::new(
        "support",
        "hello",
        Model::new("anthropic", "claude-sonnet-5"),
    )
    .mcp_server(server.clone())
    .mcp_server_headers(McpServerHeaders::new("support", headers.clone()));
    let body = client.invocation_body(request).unwrap();
    let definition = serde_json::to_value(body.agent_definition.unwrap()).unwrap();
    assert!(definition["mcp_servers"][0].get("headers").is_none());
    assert_eq!(body.mcp_server_headers.unwrap()[0].headers, headers);

    // A header naming no declared server is a typo the SDK can catch locally.
    let mismatched = InvokeRequest::new(
        "support",
        "hello",
        Model::new("anthropic", "claude-sonnet-5"),
    )
    .mcp_server(server)
    .mcp_server_headers(McpServerHeaders::new("typo", headers));
    assert!(client.invocation_body(mismatched).is_err());
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
        let request = InvokeRequest::new("support", "help", Model::new("openai", "gpt-test"))
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
    let mut input_schema = HashMap::new();
    input_schema.insert("type".to_owned(), json!("object"));
    let mut output_schema = HashMap::new();
    output_schema.insert("type".to_owned(), json!("object"));
    let request = InvokeRequest::new("support", "hello", Model::new("openai", "gpt-test"))
        .instructions("help")
        .limits(Limits {
            max_iterations: Some(4),
            ..Limits::default()
        })
        .tool(Tool::host("lookup", "Look up a value", input_schema))
        .output_schema(output_schema)
        .tenant_key("acme")
        .session_key("ticket-42")
        .idempotency_key("request-key")
        .provider_key(ProviderKeySelection {
            provider: "openai".to_owned(),
            source: ProviderKeySource::AppByok,
        });

    let definition = request.agent_definition.as_ref().unwrap();
    assert_eq!(definition.model.id, "gpt-test");
    assert_eq!(definition.instructions.as_deref(), Some("help"));
    assert_eq!(definition.tools.len(), 1);
    assert_eq!(request.session_key.as_deref(), Some("ticket-42"));
    assert_eq!(request.session_id, None);
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
        .list_agent_identities(ListAgentsOptions {
            agent_key: Some("support".to_owned()),
            ..Default::default()
        })
        .await
        .unwrap();
    assert_eq!(agents.items[0].id, AGENT_ID);
    let agent = client.get_agent_identity(AGENT_ID).await.unwrap();
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
            agent_key: "support".to_owned(),
            tenant_key: None,
            session_id: None,
            session_key: None,
            session_options: None,
            idempotency_key: Some("rust-lost-ack".to_owned()),
            if_active: Some(IfActivePolicy::Supersede),
            on_budget_exhausted: None,
            metadata: None,
            input: "hello".to_owned(),
            input_blocks: Vec::new(),
            agent_definition: Some(AgentDefinition {
                model: Model {
                    provider: "openai".to_owned(),
                    id: "gpt-test".to_owned(),
                },
                instructions: Some("help".to_owned()),
                sampling: Some(Sampling { temperature: 0.0 }),
                reasoning: Some(Reasoning {
                    effort: Some(ReasoningEffort::High),
                    budget_tokens: None,
                }),
                tool_choice: Some(ToolChoice::Required),
                limits: None,
                tools: Vec::new(),
                mcp_servers: vec![server],
                provider_tools: Vec::new(),
                output_schema: None,
            }),
            agent_definition_id: None,
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
    assert_eq!(sessions.items[0].usage.as_ref().unwrap().input_tokens, 9);
    assert_eq!(sessions.items[0].agent_id.as_deref(), Some(AGENT_ID));
    let context = sessions.items[0].context.as_ref().unwrap();
    assert_eq!(context.estimated_tokens, 12);
    assert_eq!(context.context_window_tokens, Some(128000));
    assert_eq!(context.model.provider, "openai");
    assert_eq!(context.model.id, "gpt-test");
    let messages = client
        .list_session_messages(SESSION_ID, MessageListOptions::default())
        .await
        .unwrap();
    assert_eq!(messages.next_cursor.as_deref(), Some("messages-page-2"));
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

    assert_eq!(
        event_types,
        vec![
            "invocation.update",
            "stream.end",
            "invocation.update",
            "invocation.result",
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
    assert_eq!(state.last_event_id, "cursor-1");
    assert_eq!(state.last_statuses, vec!["waiting", "queued", "running"]);
    assert_eq!(state.last_deltas, "false");
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
            nvoken::models::InvocationStatus::Paused,
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
    last_event_id: String,
    last_statuses: Vec<String>,
    last_deltas: String,
}

#[tokio::test]
async fn shared_callback_signing_and_deduplication_vector() {
    let vector: CallbackVector = serde_json::from_str(
        &std::fs::read_to_string("../../docs/design/callback-signing-v1.json").unwrap(),
    )
    .unwrap();
    let headers = header_map(&vector.headers);
    let now = UNIX_EPOCH + Duration::from_secs(vector.now);
    let verified =
        verify_callback(vector.key.as_bytes(), &headers, vector.body.as_bytes(), now).unwrap();
    assert_eq!(verified.tool_call_id, TOOL_CALL_ID);

    let signature_error = verify_callback(
        vector.key.as_bytes(),
        &headers,
        format!("{} ", vector.body).as_bytes(),
        now,
    )
    .unwrap_err();
    assert!(matches!(signature_error, CallbackError::SignatureMismatch));
    for (name, value) in [
        ("x-nvoken-timestamp", "1784635801"),
        ("x-nvoken-delivery-id", "different"),
        ("x-nvoken-signature", "sha256=00"),
    ] {
        let mut tampered = headers.clone();
        tampered.insert(name, value.parse().unwrap());
        assert!(verify_callback(
            vector.key.as_bytes(),
            &tampered,
            vector.body.as_bytes(),
            now,
        )
        .is_err());
    }

    let store = MemoryStore::default();
    let (_, replayed) = deduplicate_callback_result(&store, TOOL_CALL_ID, json!({"ok": true}))
        .await
        .unwrap();
    assert!(!replayed);
    let (stored, replayed) =
        deduplicate_callback_result(&store, TOOL_CALL_ID, json!({"ok": false}))
            .await
            .unwrap();
    assert!(replayed);
    assert_eq!(stored, json!({"ok": true}));
}

#[derive(Deserialize)]
struct CallbackVector {
    key: String,
    now: u64,
    headers: HashMap<String, String>,
    body: String,
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
    value: Mutex<Option<Value>>,
}

#[async_trait::async_trait]
impl CallbackResultStore<Value> for MemoryStore {
    async fn put_if_absent(
        &self,
        _tool_call_id: &str,
        result: Value,
    ) -> Result<(Value, bool), String> {
        let mut stored = self.value.lock().map_err(|error| error.to_string())?;
        if let Some(value) = stored.as_ref() {
            return Ok((value.clone(), false));
        }
        *stored = Some(result.clone());
        Ok((result, true))
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
        .event(WebhookEvent::Paused);
    assert_eq!(target.url, url);
    assert_eq!(target.events.len(), 3);

    let mut generated = models::WebhookTarget::new(url.to_string());
    generated.events = Some(vec![
        models::WebhookEvent::WebhookEventEnded,
        models::WebhookEvent::WebhookEventWaiting,
        models::WebhookEvent::WebhookEventPaused,
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
    let mut request = InvokeRequest::from_agent_definition(
        accepted["agent_key"].as_str().unwrap(),
        accepted["input"].as_str().unwrap(),
        accepted["agent_definition_id"].as_str().unwrap(),
    )
    .session_key(accepted["session_key"].as_str().unwrap())
    .idempotency_key(accepted["idempotency_key"].as_str().unwrap());
    for item in accepted["context"].as_array().unwrap() {
        request = request.context(fixture_context_item(item));
    }
    let body = client.invocation_body(request).unwrap();
    assert_eq!(
        serde_json::to_value(body.context.as_ref().unwrap()).unwrap(),
        accepted["context"]
    );
    assert!(body.agent_definition.is_none());

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
        let mut request = InvokeRequest::from_agent_definition(
            "support",
            "hello",
            accepted["agent_definition_id"].as_str().unwrap(),
        );
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
