use std::collections::HashMap;
use std::sync::Mutex;
use std::time::{Duration, UNIX_EPOCH};

use futures_util::StreamExt;
use http::HeaderMap;
use nvoken::{
    deduplicate_callback_result, verify_callback, CallbackError, CallbackResultStore, Client,
    ErrorCategory, ExecutionSpec, InvokeRequest, Limits, ListInvocationsOptions, ListModelsOptions,
    MessageListOptions, Model, ProviderCredentialSelection, ProviderCredentialSource, Reducer,
    RetryPolicy, StreamEvent, StreamPreview, Tool, ToolResult, WaitCondition, WaitOptions,
};
use serde::Deserialize;
use serde_json::{json, Value};

const INVOCATION_ID: &str = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb322";
const SESSION_ID: &str = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const TOOL_CALL_ID: &str = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325";
const WAIT_ID: &str = "invk_019b0a12-8d51-7f34-aed2-0e07c1bdb328";
const EXACT_MODEL_ID: &str = "experimental/model?variant=雪%#1";

#[test]
fn request_and_spec_builders_cover_core_admission_types() {
    let mut input_schema = HashMap::new();
    input_schema.insert("type".to_owned(), json!("object"));
    let mut output_schema = HashMap::new();
    output_schema.insert("type".to_owned(), json!("object"));
    let spec = ExecutionSpec::new(Model::new("openai", "gpt-test"))
        .instructions("help")
        .limits(Limits {
            max_iterations: Some(4),
            ..Limits::default()
        })
        .tool(Tool::host("lookup", "Look up a value", input_schema))
        .output_schema(output_schema);
    let request = InvokeRequest::new("support", "hello", Model::new("openai", "replaced-by-spec"))
        .tenant_key("acme")
        .session_key("ticket-42")
        .idempotency_key("request-key")
        .spec(spec)
        .provider_credential(ProviderCredentialSelection {
            provider: "openai".to_owned(),
            source: ProviderCredentialSource::AccountByok,
        });

    assert_eq!(request.spec.model.id, "gpt-test");
    assert_eq!(request.spec.instructions.as_deref(), Some("help"));
    assert_eq!(request.spec.tools.len(), 1);
    assert_eq!(request.session_key.as_deref(), Some("ticket-42"));
    assert_eq!(request.session_id, None);
    assert_eq!(request.provider_credentials.len(), 1);
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
    let expected_output_text = result_fixture["message_join"]["expected_output_text"]
        .as_str()
        .unwrap();
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
    let handle = client
        .invoke(InvokeRequest {
            agent_key: "support".to_owned(),
            tenant_key: None,
            session_id: None,
            session_key: None,
            idempotency_key: Some("rust-lost-ack".to_owned()),
            input: "hello".to_owned(),
            spec: ExecutionSpec {
                instructions: Some("help".to_owned()),
                model: Model {
                    provider: "openai".to_owned(),
                    id: "gpt-test".to_owned(),
                },
                limits: None,
                tools: Vec::new(),
                output_schema: None,
            },
            provider_credentials: vec![ProviderCredentialSelection {
                provider: "openai".to_owned(),
                source: ProviderCredentialSource::CallerEphemeral {
                    api_key: "conformance-secret".to_owned(),
                },
            }],
        })
        .await
        .unwrap();
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
        .list_invocations(ListInvocationsOptions::default())
        .await
        .unwrap();
    assert!(first_page.has_more);
    assert_eq!(
        first_page.next_cursor.as_deref(),
        Some("invocations-page-2")
    );
    let second_page = client
        .list_invocations(ListInvocationsOptions {
            cursor: first_page.next_cursor,
            ..Default::default()
        })
        .await
        .unwrap();
    assert!(!second_page.has_more);
    let messages = client
        .list_session_messages(SESSION_ID, MessageListOptions::default())
        .await
        .unwrap();
    assert_eq!(messages.next_cursor.as_deref(), Some("messages-page-2"));

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
    let stream = stream_handle.stream();
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
    assert_eq!(state.stream_attempts, 3);
    assert_eq!(state.last_event_id, "cursor-1");
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
        snapshot.resume_cursor.as_deref(),
        Some(fixture.expected.resume_cursor.as_str())
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
    resume_cursor: String,
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
    stream_attempts: u32,
    last_event_id: String,
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
