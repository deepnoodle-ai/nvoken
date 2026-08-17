//! Exercises the high-level Agent binding (D13) against a minimal
//! hand-rolled HTTP/1.1 mock server, mirroring the Go SDK's
//! `agent_test.go` runtime and behavioral contract: the five verbs,
//! host-tool dispatch, structured-output decoding, bound-Session
//! serialization, and missing-handler/no-output-text error kinds.

use std::collections::HashMap;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use futures_util::StreamExt;
use nvoken::{
    AgentInvocationOptions, AgentOptions, Client, ErrorCategory, IfActivePolicy, NvokenError,
    SessionBinding, Tool, ToolHandlerError,
};
use serde_json::{json, Value};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};
use tokio::sync::watch;

/// `terminal` is required on a change, so a fixture has to carry it. Derived
/// from the status rather than written per call site, so a fixture cannot claim
/// an ending its own status disagrees with.
const TERMINAL_STATUS_NAMES: [&str; 4] = ["completed", "incomplete", "failed", "cancelled"];

const AGENT_ID: &str = "agent_019b0a12-8d51-7f34-aed2-0e07c1bdb320";
const SESSION_ID: &str = "sesn_019b0a12-8d51-7f34-aed2-0e07c1bdb321";
const DEFINITION_ID: &str = "def_019b0a12-8d51-7f34-aed2-0e07c1bdb330";
const TOOL_CALL_ID: &str = "tcal_019b0a12-8d51-7f34-aed2-0e07c1bdb325";

#[derive(Clone, Default)]
struct InvocationState {
    input: String,
    session_key: String,
    if_active: String,
    submitted: bool,
    cancelled: bool,
}

#[derive(Default)]
struct RuntimeState {
    next_id: u64,
    invocations: HashMap<String, InvocationState>,
    submissions: u64,
    cancel_count: u64,
    agent_creates: Vec<Value>,
    admitted_identities: Vec<(Option<String>, Option<String>)>,
}

struct TestRuntime {
    state: Mutex<RuntimeState>,
    slow_tx: watch::Sender<bool>,
}

impl TestRuntime {
    fn new() -> Arc<Self> {
        let (slow_tx, _) = watch::channel(false);
        Arc::new(Self {
            state: Mutex::new(RuntimeState::default()),
            slow_tx,
        })
    }

    /// Blocks until `release_slow` is called. Subscribing and checking the
    /// current value first, rather than only waiting on a one-shot
    /// webhook, means a release that already happened before this call
    /// is still observed instead of hanging forever.
    async fn wait_for_slow_release(&self) {
        let mut receiver = self.slow_tx.subscribe();
        while !*receiver.borrow() {
            if receiver.changed().await.is_err() {
                return;
            }
        }
    }

    fn admissions(&self) -> u64 {
        self.state.lock().unwrap().next_id
    }

    fn tool_submissions(&self) -> u64 {
        self.state.lock().unwrap().submissions
    }

    fn cancellations(&self) -> u64 {
        self.state.lock().unwrap().cancel_count
    }

    fn last_session_key(&self) -> String {
        self.state
            .lock()
            .unwrap()
            .invocations
            .values()
            .filter(|state| !state.session_key.is_empty())
            .map(|state| state.session_key.clone())
            .last()
            .unwrap_or_default()
    }

    fn last_if_active(&self) -> String {
        self.state
            .lock()
            .unwrap()
            .invocations
            .values()
            .find(|state| !state.if_active.is_empty())
            .map(|state| state.if_active.clone())
            .unwrap_or_default()
    }

    async fn wait_for_admissions(&self, count: u64) {
        let deadline = tokio::time::Instant::now() + Duration::from_secs(1);
        while self.admissions() < count {
            if tokio::time::Instant::now() > deadline {
                panic!("admissions = {}, want at least {count}", self.admissions());
            }
            tokio::time::sleep(Duration::from_millis(1)).await;
        }
    }

    fn release_slow(&self) {
        let _ = self.slow_tx.send(true);
    }

    fn state_of(&self, id: &str) -> InvocationState {
        self.state
            .lock()
            .unwrap()
            .invocations
            .get(id)
            .cloned()
            .unwrap_or_default()
    }
}

fn needs_tool(input: &str) -> bool {
    input == "tool structured" || input.contains("missing")
}

fn invocation_payload(id: &str, status: &str) -> Value {
    let ended_at = if status == "completed" || status == "cancelled" {
        json!("2026-07-21T12:00:03Z")
    } else {
        Value::Null
    };
    let mut value = json!({
        "id": id,
        "agent_id": AGENT_ID,
        "agent_key": "support",
        "session_id": SESSION_ID,
        "user_key": null,
        "agent_definition_id": DEFINITION_ID,
        "agent_definition_revision": 1,
        "agent_definition": null,
        "context": null,
        "status": status,
        "stop_reason": if status == "completed" { json!("end_turn") } else { Value::Null },
        "credit_block": null,
        "attempt": 1,
        "error": null,
        "usage": null,
        "provenance": null,
        "structured_output": null,
        "structured_output_provenance": null,
        "metadata": null,
        "limits": {
            "total_timeout_seconds": 300,
            "active_timeout_seconds": 120,
            "waiting_timeout_seconds": 180,
            "max_iterations": 16,
        },
        "active_execution_ms": 250,
        "deadline_at": "2026-07-21T12:05:00Z",
        "created_at": "2026-07-21T12:00:00Z",
        "updated_at": "2026-07-21T12:00:03Z",
        "ended_at": ended_at,
    });
    if status == "waiting" {
        value["tool_calls"] = json!([{
            "id": TOOL_CALL_ID,
            "name": "weather",
            "mode": "host",
            "status": "pending",
            "arguments": {"city": "Paris"},
            "deadline_at": "2026-07-21T12:05:00Z",
            "updated_at": "2026-07-21T12:00:03Z",
        }]);
    }
    value
}

struct HttpRequest {
    method: String,
    path: String,
    body: Vec<u8>,
}

async fn read_request(stream: &mut TcpStream) -> Option<HttpRequest> {
    let mut buffer = Vec::new();
    let mut chunk = [0u8; 4096];
    let header_end = loop {
        if let Some(index) = find_subslice(&buffer, b"\r\n\r\n") {
            break index;
        }
        let read = stream.read(&mut chunk).await.ok()?;
        if read == 0 {
            return None;
        }
        buffer.extend_from_slice(&chunk[..read]);
    };
    let header_text = String::from_utf8_lossy(&buffer[..header_end]).into_owned();
    let mut lines = header_text.split("\r\n");
    let request_line = lines.next()?;
    let mut parts = request_line.split_whitespace();
    let method = parts.next()?.to_owned();
    let path = parts.next()?.to_owned();
    let mut content_length = 0usize;
    for line in lines {
        if let Some((name, value)) = line.split_once(':') {
            if name.trim().eq_ignore_ascii_case("content-length") {
                content_length = value.trim().parse().unwrap_or(0);
            }
        }
    }
    let mut body = buffer[header_end + 4..].to_vec();
    while body.len() < content_length {
        let read = stream.read(&mut chunk).await.ok()?;
        if read == 0 {
            break;
        }
        body.extend_from_slice(&chunk[..read]);
    }
    Some(HttpRequest { method, path, body })
}

async fn write_json(stream: &mut TcpStream, status: u16, body: &Value) {
    let text = status_text(status);
    let payload = serde_json::to_vec(body).unwrap();
    let head = format!(
        "HTTP/1.1 {status} {text}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        payload.len()
    );
    let _ = stream.write_all(head.as_bytes()).await;
    let _ = stream.write_all(&payload).await;
    let _ = stream.shutdown().await;
}

fn status_text(status: u16) -> &'static str {
    match status {
        200 => "OK",
        202 => "Accepted",
        404 => "Not Found",
        _ => "Error",
    }
}

async fn handle_connection(mut stream: TcpStream, runtime: Arc<TestRuntime>) {
    let Some(request) = read_request(&mut stream).await else {
        return;
    };
    if request.path == "/v1/agents" && request.method == "POST" {
        let body: Value = serde_json::from_slice(&request.body).unwrap_or(Value::Null);
        // One key stands for a record somebody else already pinned, so a
        // declaration can be tested against a record that disagrees with it.
        let pinned_revision = if body["agent_key"] == json!("pinned-elsewhere") {
            json!(3)
        } else {
            body["pinned_revision"].clone()
        };
        runtime
            .state
            .lock()
            .unwrap()
            .agent_creates
            .push(body.clone());
        let record = json!({
            "id": AGENT_ID,
            "tenant_key": body["tenant_key"].clone(),
            "agent_key": body["agent_key"].clone(),
            "name": body["agent_key"].clone(),
            "agent_definition_id": DEFINITION_ID,
            "pinned_revision": if pinned_revision.is_null() { json!(null) } else { pinned_revision },
            "created_at": "2026-07-21T12:00:00Z",
            "updated_at": "2026-07-21T12:00:00Z",
            "archived_at": null,
        });
        write_json(&mut stream, 201, &record).await;
        return;
    }
    if request.path == "/v1/invocations" && request.method == "POST" {
        let body: Value = serde_json::from_slice(&request.body).unwrap_or(Value::Null);
        runtime.state.lock().unwrap().admitted_identities.push((
            body["agent_id"].as_str().map(str::to_owned),
            body["agent_key"].as_str().map(str::to_owned),
        ));
        let input = body["input"].as_str().unwrap_or_default().to_owned();
        let session_key = body["session_key"].as_str().unwrap_or_default().to_owned();
        let if_active = body["if_active"].as_str().unwrap_or_default().to_owned();
        let id = {
            let mut state = runtime.state.lock().unwrap();
            state.next_id += 1;
            let id = format!("invk_019b0a12-8d51-7f34-aed2-{:012x}", state.next_id);
            state.invocations.insert(
                id.clone(),
                InvocationState {
                    input,
                    session_key,
                    if_active,
                    submitted: false,
                    cancelled: false,
                },
            );
            id
        };
        let mut invocation = invocation_payload(&id, "queued");
        invocation["deduplicated"] = json!(false);
        write_json(&mut stream, 202, &invocation).await;
        return;
    }

    // One stream, filtered to one turn by query parameter.
    if request.method == "GET" && request.path.starts_with("/v1/sessions/") {
        let id = request
            .path
            .split_once("invocation_id=")
            .map(|(_, value)| value.split('&').next().unwrap_or("").to_owned())
            .unwrap_or_default();
        stream_turn(&mut stream, &runtime, &id).await;
        return;
    }
    let Some(rest) = request.path.strip_prefix("/v1/invocations/") else {
        write_json(&mut stream, 404, &json!({"message": "not found"})).await;
        return;
    };

    if let Some(id) = rest.strip_suffix("/tool-results") {
        if request.method == "POST" {
            submit_tool_results(&mut stream, &runtime, id).await;
            return;
        }
    }
    if let Some(id) = rest.strip_suffix("/cancel") {
        if request.method == "POST" {
            cancel_invocation(&mut stream, &runtime, id).await;
            return;
        }
    }
    if let Some(id) = rest.strip_suffix("/result") {
        if request.method == "GET" {
            invocation_result(&mut stream, &runtime, id).await;
            return;
        }
    }
    if request.method == "GET" {
        get_invocation(&mut stream, &runtime, rest).await;
        return;
    }
    write_json(&mut stream, 404, &json!({"message": "not found"})).await;
}

async fn get_invocation(stream: &mut TcpStream, runtime: &TestRuntime, id: &str) {
    let state = runtime.state_of(id);
    if !state.cancelled && !needs_tool(&state.input) && state.input.contains("slow") {
        runtime.wait_for_slow_release().await;
    }
    let state = runtime.state_of(id);
    let status = if state.cancelled {
        "cancelled"
    } else if needs_tool(&state.input) && !state.submitted {
        "waiting"
    } else {
        "completed"
    };
    write_json(stream, 200, &invocation_payload(id, status)).await;
}

async fn stream_turn(stream: &mut TcpStream, runtime: &Arc<TestRuntime>, id: &str) {
    let head = "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n\r\n";
    if stream.write_all(head.as_bytes()).await.is_err() {
        return;
    }
    let state = runtime.state_of(id);
    if needs_tool(&state.input) {
        if stream
            .write_all(change_frame(id, "waiting", "cursor-waiting").as_bytes())
            .await
            .is_err()
        {
            return;
        }
        let _ = stream.flush().await;
        let deadline = tokio::time::Instant::now() + Duration::from_secs(1);
        loop {
            let state = runtime.state_of(id);
            if state.submitted || state.cancelled || tokio::time::Instant::now() > deadline {
                break;
            }
            tokio::time::sleep(Duration::from_millis(1)).await;
        }
        if !runtime.state_of(id).submitted {
            let _ = stream.shutdown().await;
            return;
        }
    }
    if state.input.contains("slow") {
        runtime.wait_for_slow_release().await;
    }
    // A turn is over when a change for it carries a terminal status.
    let _ = stream
        .write_all(change_frame(id, "completed", "cursor-settled").as_bytes())
        .await;
    let _ = stream.shutdown().await;
}

/// change_frame writes the one durable frame, carrying one lifecycle change for
/// the turn being followed.
fn change_frame(invocation_id: &str, status: &str, cursor: &str) -> String {
    let data = json!({
        "type": "transcript.update",
        "session_id": SESSION_ID,
        "messages": [],
        "invocation_changes": [{
            "invocation_id": invocation_id,
            "revision": 1,
            "status": status,
            "terminal": TERMINAL_STATUS_NAMES.contains(&status),
            "through_message_sequence": null,
            "error": null,
            "structured_output": null,
            "occurred_at": "2026-07-21T12:00:00Z",
        }],
        "cursor": cursor,
    });
    format!("id: {cursor}\nevent: transcript.update\ndata: {data}\n\n")
}

async fn submit_tool_results(stream: &mut TcpStream, runtime: &TestRuntime, id: &str) {
    {
        let mut state = runtime.state.lock().unwrap();
        if let Some(entry) = state.invocations.get_mut(id) {
            entry.submitted = true;
        }
        state.submissions += 1;
    }
    write_json(
        stream,
        202,
        &json!({
            "invocation_id": id,
            "session_id": SESSION_ID,
            "status": "queued",
            "results": [{
                "tool_call_id": TOOL_CALL_ID,
                "status": "completed",
                "deduplicated": false,
            }],
            "tool_calls": [],
        }),
    )
    .await;
}

async fn cancel_invocation(stream: &mut TcpStream, runtime: &TestRuntime, id: &str) {
    {
        let mut state = runtime.state.lock().unwrap();
        if let Some(entry) = state.invocations.get_mut(id) {
            entry.cancelled = true;
        }
        state.cancel_count += 1;
    }
    write_json(stream, 200, &invocation_payload(id, "cancelled")).await;
}

async fn invocation_result(stream: &mut TcpStream, runtime: &TestRuntime, id: &str) {
    let state = runtime.state_of(id);
    let mut invocation = invocation_payload(id, "completed");
    if state.input.contains("structured") {
        invocation["structured_output"] = json!({"answer": "world"});
    }
    let output_text =
        if state.input.contains("structured-only") || state.input.contains("tool-only") {
            Value::Null
        } else {
            json!("hello")
        };
    write_json(
        stream,
        200,
        &json!({
            "invocation": invocation,
            "messages": [],
            "output_text": output_text,
        }),
    )
    .await;
}

fn expect_err<T>(result: Result<T, NvokenError>, message: &str) -> NvokenError {
    match result {
        Ok(_) => panic!("{message}"),
        Err(error) => error,
    }
}

fn find_subslice(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

async fn start_server() -> (String, Arc<TestRuntime>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let runtime = TestRuntime::new();
    let accept_runtime = runtime.clone();
    tokio::spawn(async move {
        loop {
            let Ok((stream, _)) = listener.accept().await else {
                return;
            };
            let runtime = accept_runtime.clone();
            tokio::spawn(handle_connection(stream, runtime));
        }
    });
    (format!("http://{addr}"), runtime)
}

fn base_options() -> AgentOptions {
    AgentOptions::new("support")
}

#[tokio::test]
async fn agent_five_verbs_dispatch_and_structured_output() {
    let (base_url, runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let handler_calls = Arc::new(AtomicU64::new(0));
    let counted = handler_calls.clone();
    let options = base_options().tool(
        Tool::host(
            "weather",
            "Weather lookup",
            HashMap::from([("type".to_owned(), json!("object"))]),
        )
        .handler(move |input| {
            let counted = counted.clone();
            async move {
                counted.fetch_add(1, Ordering::SeqCst);
                assert_eq!(input["city"], json!("Paris"));
                Ok(json!({"temperature": 21}))
            }
        }),
    );
    let agent = client.agent(options).expect("agent");

    let handle = agent
        .invoke(
            "invoke",
            AgentInvocationOptions {
                if_active: Some(IfActivePolicy::Supersede),
                ..Default::default()
            },
        )
        .await
        .expect("invoke");
    assert!(!handle.invocation_id.is_empty());
    assert_eq!(runtime.last_if_active(), "supersede");

    let (_, mut events) = agent
        .stream("stream", AgentInvocationOptions::default())
        .await
        .expect("stream");
    let mut seen = Vec::new();
    while let Some(event) = events.next().await {
        seen.push(event.expect("stream event").event.event_type);
    }
    assert_eq!(seen, vec!["transcript.update"]);

    let result = agent
        .run("tool structured", AgentInvocationOptions::default())
        .await
        .expect("run");
    assert_eq!(result.output_text.as_deref(), Some("hello"));
    let decoded = result
        .structured_output
        .as_ref()
        .and_then(|value| value.get("answer"))
        .and_then(Value::as_str);
    assert_eq!(decoded, Some("world"));
    assert_eq!(handler_calls.load(Ordering::SeqCst), 1);
    assert_eq!(runtime.tool_submissions(), 1);

    let text = agent
        .text("text", AgentInvocationOptions::default())
        .await
        .expect("text");
    assert_eq!(text, "hello");

    let bound = agent
        .session(SessionBinding::by_key("customer-123"))
        .expect("session");
    let bound_text = bound
        .text("bound", AgentInvocationOptions::default())
        .await
        .expect("bound text");
    assert_eq!(bound_text, "hello");
    assert_eq!(runtime.last_session_key(), "customer-123");
}

#[tokio::test]
async fn agent_missing_handler_policy_and_no_output_kinds() {
    let (base_url, runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let missing_tool = Tool::host(
        "weather",
        "Weather lookup",
        HashMap::from([("type".to_owned(), json!("object"))]),
    );
    let options = base_options().tool(missing_tool);
    let agent = client.agent(options).expect("agent");

    let error = expect_err(
        agent
            .run("missing", AgentInvocationOptions::default())
            .await,
        "missing handler cancels by default",
    );
    assert_eq!(error.code.as_deref(), Some("missing_tool_handler"));
    assert_eq!(
        error
            .details
            .as_ref()
            .and_then(|d| d["invocation_cancelled"].as_bool()),
        Some(true)
    );
    assert_eq!(runtime.cancellations(), 1);

    let error = expect_err(
        agent
            .run(
                "missing opt-out",
                AgentInvocationOptions {
                    leave_waiting_on_missing_handler: true,
                    ..Default::default()
                },
            )
            .await,
        "missing handler left waiting on opt-out",
    );
    assert_eq!(error.code.as_deref(), Some("missing_tool_handler"));
    assert_eq!(
        error
            .details
            .as_ref()
            .and_then(|d| d["invocation_cancelled"].as_bool()),
        Some(false)
    );
    assert_eq!(runtime.cancellations(), 1);

    let error = agent
        .text("structured-only", AgentInvocationOptions::default())
        .await
        .expect_err("structured-only has no text");
    assert_eq!(error.category, ErrorCategory::UnexpectedResponse);
    assert_eq!(
        error
            .details
            .as_ref()
            .and_then(|d| d["result_kind"].as_str()),
        Some("structured output")
    );

    let error = agent
        .text("tool-only", AgentInvocationOptions::default())
        .await
        .expect_err("tool-only has no text");
    assert_eq!(
        error
            .details
            .as_ref()
            .and_then(|d| d["result_kind"].as_str()),
        Some("tool-only output")
    );
}

#[tokio::test]
async fn bound_session_serializes_admission() {
    let (base_url, runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let agent = client.agent(base_options()).expect("agent");
    let bound = agent
        .session(SessionBinding::by_id(SESSION_ID))
        .expect("session");

    let first_bound = bound.clone();
    let first = tokio::spawn(async move {
        first_bound
            .run("slow first", AgentInvocationOptions::default())
            .await
    });
    runtime.wait_for_admissions(1).await;
    let second_bound = bound.clone();
    let second = tokio::spawn(async move {
        second_bound
            .run("slow second", AgentInvocationOptions::default())
            .await
    });
    tokio::time::sleep(Duration::from_millis(20)).await;
    assert_eq!(
        runtime.admissions(),
        1,
        "second bound admission ran concurrently"
    );
    runtime.release_slow();

    let first_result = tokio::time::timeout(Duration::from_secs(1), first)
        .await
        .expect("first bound run finished")
        .unwrap();
    assert!(first_result.is_ok());
    let second_result = tokio::time::timeout(Duration::from_secs(1), second)
        .await
        .expect("second bound run finished")
        .unwrap();
    assert!(second_result.is_ok());
    assert_eq!(runtime.admissions(), 2);
}

#[tokio::test]
async fn agent_requires_exactly_one_identity() {
    let (base_url, _runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let error = expect_err(
        client.agent(AgentOptions {
            agent_id: Some("agent_test".to_owned()),
            agent_key: Some("support".to_owned()),
            ..AgentOptions::new("support")
        }),
        "identity is required",
    );
    assert_eq!(error.category, ErrorCategory::Validation);
}

#[tokio::test]
async fn agent_runs_by_stable_key() {
    let (base_url, _runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let agent = client
        .agent(AgentOptions::new("support"))
        .expect("agent binds by key");
    let text = agent
        .text("text", AgentInvocationOptions::default())
        .await
        .expect("text");
    assert_eq!(text, "hello");
}

#[tokio::test]
async fn tool_handler_failure_reports_as_error_content() {
    let (base_url, _runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let options = base_options().tool(
        Tool::host(
            "weather",
            "Weather lookup",
            HashMap::from([("type".to_owned(), json!("object"))]),
        )
        .handler(|_input| async move { Err(ToolHandlerError::new("boom")) }),
    );
    let agent = client.agent(options).expect("agent");
    let result = agent
        .run("tool structured", AgentInvocationOptions::default())
        .await
        .expect("run completes even though the handler failed");
    assert_eq!(result.output_text.as_deref(), Some("hello"));
}

fn _unused(_: NvokenError) {}

/// The declaration path: the keys a host already owns are enough to run a
/// turn, the record is created once, and later turns admit by the ID the
/// create returned.
#[tokio::test]
async fn declared_agent_creates_its_record_on_first_use() {
    let (base_url, runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let mut options = AgentOptions::declared("support", "support");
    options.tenant_key = Some("customer-482".to_owned());
    let agent = client.agent(options).unwrap();
    assert!(agent.id().await.is_none());
    assert!(agent.resource().await.is_none());

    for input in ["first", "second"] {
        agent
            .invoke(input, AgentInvocationOptions::default())
            .await
            .unwrap();
    }

    assert_eq!(agent.id().await.as_deref(), Some(AGENT_ID));
    let state = runtime.state.lock().unwrap();
    assert_eq!(state.agent_creates.len(), 1, "one create for two turns");
    assert_eq!(state.agent_creates[0]["definition_key"], json!("support"));
    assert_eq!(state.agent_creates[0]["tenant_key"], json!("customer-482"));
    assert!(state.agent_creates[0].get("agent_definition_id").is_none());
    for (agent_id, agent_key) in &state.admitted_identities {
        assert_eq!(agent_id.as_deref(), Some(AGENT_ID));
        assert_eq!(agent_key.as_deref(), None);
    }
}

/// The one substantive field a restatement can disagree about.
#[tokio::test]
async fn declared_agent_refuses_a_contradicted_pin() {
    let (base_url, _runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let mut options = AgentOptions::declared("support", "support");
    options.pinned_revision = Some(2);
    let agent = client.agent(options).unwrap();
    // The mock echoes the pin it was sent, so agreement is the ordinary case.
    assert_eq!(agent.ensure().await.unwrap().pinned_revision, Some(2));

    let mut disagreeing = AgentOptions::declared("pinned-elsewhere", "support");
    disagreeing.pinned_revision = Some(2);
    let other = client.agent(disagreeing).unwrap();
    let error = other.ensure().await.unwrap_err();
    assert_eq!(error.code.as_deref(), Some("agent_pin_conflict"));

    // Declaring no pin declares nothing about the pin.
    let silent = client
        .agent(AgentOptions::declared("pinned-elsewhere", "support"))
        .unwrap();
    assert_eq!(silent.ensure().await.unwrap().pinned_revision, Some(3));
}

/// An Agent that cannot create itself fails locally and explains which part of
/// the declaration is missing.
#[tokio::test]
async fn declared_agent_without_a_definition_cannot_create_itself() {
    let (base_url, _runtime) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();
    let agent = client.agent(AgentOptions::new("support")).unwrap();
    let error = agent.ensure().await.unwrap_err();
    assert_eq!(error.category, ErrorCategory::Validation);
    assert!(
        error.message.contains("definition_key"),
        "{}",
        error.message
    );

    let mut contradiction = AgentOptions::from_agent_id(AGENT_ID);
    contradiction.definition_key = Some("support".to_owned());
    assert!(client.agent(contradiction).is_err());
}
