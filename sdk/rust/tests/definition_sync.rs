//! `sync_definitions` writes and never reads, so what it sent is the assertion.
//!
//! nvoken canonicalizes a definition before comparing it, and a second copy of
//! that rule in the SDK would be free to disagree the first time either side
//! gains a field. So both write paths are ensure-shaped and the status carries
//! what moved: 201 published a revision, 200 means the current one already
//! satisfied the request.

use std::sync::{Arc, Mutex};

use nvoken::{AgentDefinition, Client, DefinitionSyncOutcome, Model, NvokenError};
use serde_json::{json, Value};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

/// One write the sync sent.
#[derive(Clone, Debug)]
struct SentRequest {
    method: String,
    path: String,
    if_match: Option<String>,
    body: Value,
}

type Responder = Arc<dyn Fn(&SentRequest) -> (u16, Value) + Send + Sync>;

/// A recording server answering each write from `respond`.
async fn start_server(respond: Responder) -> (String, Arc<Mutex<Vec<SentRequest>>>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let addr = listener.local_addr().unwrap();
    let sent: Arc<Mutex<Vec<SentRequest>>> = Arc::new(Mutex::new(Vec::new()));
    let recorded = sent.clone();
    tokio::spawn(async move {
        loop {
            let Ok((mut stream, _)) = listener.accept().await else {
                return;
            };
            let Some(request) = read_request(&mut stream).await else {
                continue;
            };
            recorded.lock().unwrap().push(request.clone());
            let (status, body) = respond(&request);
            write_json(&mut stream, status, &body).await;
        }
    });
    (format!("http://{addr}"), sent)
}

async fn read_request(stream: &mut TcpStream) -> Option<SentRequest> {
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
    let mut parts = lines.next()?.split_whitespace();
    let method = parts.next()?.to_owned();
    let path = parts.next()?.to_owned();
    let mut content_length = 0usize;
    let mut if_match = None;
    for line in lines {
        let Some((name, value)) = line.split_once(':') else {
            continue;
        };
        if name.trim().eq_ignore_ascii_case("content-length") {
            content_length = value.trim().parse().unwrap_or(0);
        }
        if name.trim().eq_ignore_ascii_case("if-match") {
            if_match = Some(value.trim().to_owned());
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
    Some(SentRequest {
        method,
        path,
        if_match,
        body: serde_json::from_slice(&body).unwrap_or(Value::Null),
    })
}

async fn write_json(stream: &mut TcpStream, status: u16, body: &Value) {
    let payload = serde_json::to_vec(body).unwrap();
    let head = format!(
        "HTTP/1.1 {status} Response\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        payload.len()
    );
    let _ = stream.write_all(head.as_bytes()).await;
    let _ = stream.write_all(&payload).await;
    let _ = stream.shutdown().await;
}

fn find_subslice(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

fn synced_definition(definition_key: &str, revision: u64) -> Value {
    json!({
        "id": format!("def_{definition_key}"),
        "definition_key": definition_key,
        "name": definition_key,
        "revision": revision,
        "model": {"provider": "anthropic", "id": "claude-sonnet-5"},
        "created_at": "2026-08-17T12:00:00Z",
        "updated_at": "2026-08-17T12:00:00Z",
        "archived_at": null,
    })
}

fn definition_conflict(code: &str, definition_id: &str) -> Value {
    json!({
        "code": code,
        "message": "definition_key is held by another definition",
        "details": {"definition_id": definition_id},
    })
}

fn model() -> Model {
    Model::new("anthropic", "claude-sonnet-5")
}

#[tokio::test]
async fn sync_definitions_writes_without_reading_and_reports_what_each_write_did() {
    let (base_url, sent) = start_server(Arc::new(|request: &SentRequest| {
        if request.method == "PUT" {
            return (201, synced_definition("changed", 7));
        }
        // A key the App has never used, then a restatement of one it holds,
        // then one whose contents differ.
        match request.body["definition_key"].as_str() {
            Some("new") => (201, synced_definition("new", 1)),
            Some("same") => (200, synced_definition("same", 3)),
            _ => (
                409,
                definition_conflict("agent_definition_key_conflict", "def_changed"),
            ),
        }
    }))
    .await;
    let client = Client::new(&base_url, "test-key").unwrap();

    let synced = client
        .sync_definitions(vec![
            AgentDefinition {
                definition_key: Some("new".to_owned()),
                model: model(),
                ..Default::default()
            },
            AgentDefinition {
                definition_key: Some("same".to_owned()),
                model: model(),
                ..Default::default()
            },
            AgentDefinition {
                definition_key: Some("changed".to_owned()),
                model: model(),
                instructions: Some("Be warm.".to_owned()),
                ..Default::default()
            },
        ])
        .await
        .expect("sync");

    let outcomes: Vec<(String, DefinitionSyncOutcome)> = synced
        .iter()
        .map(|one| (one.definition_key.clone(), one.outcome))
        .collect();
    assert_eq!(
        outcomes,
        vec![
            ("new".to_owned(), DefinitionSyncOutcome::Created),
            ("same".to_owned(), DefinitionSyncOutcome::Unchanged),
            ("changed".to_owned(), DefinitionSyncOutcome::Updated),
        ]
    );
    assert_eq!(synced[2].definition.revision, 7);

    // Nothing was read: three creates, and one replacement the conflict
    // addressed.
    let requests = sent.lock().unwrap().clone();
    let calls: Vec<String> = requests
        .iter()
        .map(|request| format!("{} {}", request.method, request.path))
        .collect();
    assert_eq!(
        calls,
        vec![
            "POST /v1/agent-definitions",
            "POST /v1/agent-definitions",
            "POST /v1/agent-definitions",
            "PUT /v1/agent-definitions/def_changed",
        ]
    );
    // `*`, because the conflict proves the resource exists and differs, not
    // which revision it is at. The replacement drops the immutable key.
    assert_eq!(requests[3].if_match.as_deref(), Some("*"));
    assert!(requests[3].body.get("definition_key").is_none());
    assert_eq!(requests[3].body["instructions"], json!("Be warm."));
}

#[tokio::test]
async fn sync_definitions_reports_a_raced_replacement_as_unchanged() {
    let (base_url, _sent) = start_server(Arc::new(|request: &SentRequest| {
        if request.method == "POST" {
            return (
                409,
                definition_conflict("agent_definition_key_conflict", "def_raced"),
            );
        }
        // Someone else published these exact contents between the two calls, so
        // the replacement had nothing left to publish.
        (200, synced_definition("support", 2))
    }))
    .await;
    let client = Client::new(&base_url, "test-key").unwrap();

    let synced = client
        .sync_definitions(vec![AgentDefinition {
            definition_key: Some("support".to_owned()),
            model: model(),
            ..Default::default()
        }])
        .await
        .expect("sync");
    assert_eq!(synced.len(), 1);
    assert_eq!(synced[0].outcome, DefinitionSyncOutcome::Unchanged);
}

#[tokio::test]
async fn sync_definitions_stops_at_the_first_error() {
    let (base_url, sent) = start_server(Arc::new(|_: &SentRequest| {
        // Restoring an archived key is a decision, not a sync step.
        (
            409,
            definition_conflict("agent_definition_archived", "def_archived"),
        )
    }))
    .await;
    let client = Client::new(&base_url, "test-key").unwrap();

    let error: NvokenError = client
        .sync_definitions(vec![
            AgentDefinition {
                definition_key: Some("gone".to_owned()),
                model: model(),
                ..Default::default()
            },
            AgentDefinition {
                definition_key: Some("next".to_owned()),
                model: model(),
                ..Default::default()
            },
        ])
        .await
        .expect_err("sync should stop");
    assert_eq!(error.code.as_deref(), Some("agent_definition_archived"));
    assert_eq!(sent.lock().unwrap().len(), 1);
}
