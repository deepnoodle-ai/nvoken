use std::collections::HashMap;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use nvoken::models::{
    AppSigningKeyPurpose, ForkSessionRequestFromMessage, ProviderKeyScope, ProviderStaticKey,
};
use nvoken::{
    is_not_found, issue_anonymous_token, Client, CreateClientKeyRequest, CreateCredentialRequest,
    CreateProviderKeyRequest, CreateSessionRequest, CredentialProfile, ErrorCategory,
    ForkSessionRequest, ListInvocationLogsOptions, ListMemoriesOptions, MemoryKind,
    MemorySearchMode, MintAppSigningKeyRequest, NvokenError, RegisterAppRequest,
    RegisterOrgRequest, RetryPolicy, RotateCredentialRequest, RotateProviderKeyRequest,
    TranscriptOptions, UpdateAppRequest, UpdateOrgRequest, ACTIVE_INVOCATION_STATUSES,
    ALL_INVOCATION_STATUSES,
};
use serde_json::Value;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{TcpListener, TcpStream};

#[derive(Clone, Debug)]
struct SentRequest {
    method: String,
    path: String,
    headers: HashMap<String, String>,
    body: Value,
}

async fn start_server() -> (String, Arc<Mutex<Vec<SentRequest>>>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let sent = Arc::new(Mutex::new(Vec::new()));
    let recorded = sent.clone();
    tokio::spawn(async move {
        loop {
            let Ok((mut stream, _)) = listener.accept().await else {
                return;
            };
            let Some(request) = read_request(&mut stream).await else {
                continue;
            };
            recorded.lock().unwrap().push(request);
            write_error(&mut stream).await;
        }
    });
    (format!("http://{address}"), sent)
}

async fn read_request(stream: &mut TcpStream) -> Option<SentRequest> {
    let mut buffer = Vec::new();
    let mut chunk = [0u8; 4096];
    let header_end = loop {
        if let Some(index) = buffer.windows(4).position(|window| window == b"\r\n\r\n") {
            break index;
        }
        let read = stream.read(&mut chunk).await.ok()?;
        if read == 0 {
            return None;
        }
        buffer.extend_from_slice(&chunk[..read]);
    };
    let header_text = String::from_utf8_lossy(&buffer[..header_end]);
    let mut lines = header_text.split("\r\n");
    let mut request_line = lines.next()?.split_whitespace();
    let method = request_line.next()?.to_owned();
    let path = request_line.next()?.to_owned();
    let mut headers = HashMap::new();
    let mut content_length = 0usize;
    for line in lines {
        let Some((name, value)) = line.split_once(':') else {
            continue;
        };
        let name = name.trim().to_ascii_lowercase();
        let value = value.trim().to_owned();
        if name == "content-length" {
            content_length = value.parse().unwrap_or(0);
        }
        headers.insert(name, value);
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
        headers,
        body: serde_json::from_slice(&body).unwrap_or(Value::Null),
    })
}

async fn write_error(stream: &mut TcpStream) {
    let body = br#"{"code":"internal","message":"captured"}"#;
    let head = format!(
        "HTTP/1.1 500 Error\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
        body.len()
    );
    let _ = stream.write_all(head.as_bytes()).await;
    let _ = stream.write_all(body).await;
    let _ = stream.shutdown().await;
}

#[tokio::test]
async fn facade_batch_reaches_every_operation_with_complete_inputs() {
    let (base_url, sent) = start_server().await;
    let client = Client::with_retry_policy(
        &base_url,
        "test-key",
        RetryPolicy {
            max_attempts: 1,
            min_delay: Duration::ZERO,
            max_delay: Duration::ZERO,
        },
    )
    .unwrap();

    let mut org = RegisterOrgRequest::new("Acme".to_owned());
    org.external_ref = Some("org-acme".to_owned());
    let _ = client.register_org(org).await;
    let _ = client
        .update_org("org_1", UpdateOrgRequest::new("Acme, Inc.".to_owned()))
        .await;

    let mut app = RegisterAppRequest::new("support".to_owned());
    app.org_id = Some("org_1".to_owned());
    app.external_ref = Some("app-support".to_owned());
    app.display_name = Some("Support".to_owned());
    app.callback_timeout_seconds = Some(20);
    let _ = client.register_app(app).await;
    let mut app_update = UpdateAppRequest::new();
    app_update.display_name = Some("Support 2".to_owned());
    app_update.anonymous_access = Some(None);
    let _ = client.update_app("app_1", app_update).await;
    let _ = client
        .create_app_client_key(
            "app_1",
            CreateClientKeyRequest::new("browser".to_owned(), vec![7; 32]),
        )
        .await;
    let _ = client
        .mint_app_signing_key(
            "app_1",
            MintAppSigningKeyRequest::new(AppSigningKeyPurpose::Callback),
        )
        .await;

    let mut credential =
        CreateCredentialRequest::new("operator".to_owned(), CredentialProfile::Operator);
    credential.org_id = Some("org_1".to_owned());
    let _ = client
        .create_credential(credential, Some("credential-create"))
        .await;
    let _ = client
        .rotate_credential(
            "cred_1",
            RotateCredentialRequest::new(60),
            Some("credential-rotate"),
        )
        .await;

    let provider = CreateProviderKeyRequest::new(
        "anthropic".to_owned(),
        ProviderKeyScope::Tenant,
        ProviderStaticKey::new("provider-secret".to_owned()),
        "provider-create".to_owned(),
    );
    let _ = client.create_provider_key(provider).await;
    let provider_rotation = RotateProviderKeyRequest::new(
        ProviderStaticKey::new("provider-secret-2".to_owned()),
        "provider-rotate".to_owned(),
    );
    let _ = client
        .rotate_provider_key("pkey_1", provider_rotation)
        .await;

    let _ = client
        .list_invocation_logs(
            "inv_1",
            ListInvocationLogsOptions {
                cursor: Some("logs-2".to_owned()),
                limit: Some(20),
                trace_id: Some("0123456789abcdef0123456789abcdef".to_owned()),
            },
        )
        .await;
    let _ = client
        .list_memories(ListMemoriesOptions {
            agent_id: "agent_1".to_owned(),
            tenant_key: Some("acme".to_owned()),
            user_key: Some("user-1".to_owned()),
            query: Some("refund policy".to_owned()),
            search_mode: Some(MemorySearchMode::Hybrid),
            kind: Some(MemoryKind::Fact),
            cursor: Some("memories-2".to_owned()),
            limit: Some(10),
        })
        .await;

    let mut session = CreateSessionRequest::new();
    session.agent_key = Some("support".to_owned());
    session.tenant_key = Some("acme".to_owned());
    session.user_key = Some("user-1".to_owned());
    session.session_key = Some("case-1".to_owned());
    let _ = client.create_session(session).await;
    let mut fork = ForkSessionRequest::new(ForkSessionRequestFromMessage::Integer(1));
    fork.session_key = Some("case-1-alt".to_owned());
    let _ = client.fork_session("sess_1", fork).await;
    let _ = client
        .get_session_transcript(
            "sess_1",
            TranscriptOptions {
                cursor: Some("stream-2".to_owned()),
                page_token: Some("page-2".to_owned()),
                limit: Some(100),
            },
        )
        .await;

    let _ = issue_anonymous_token(
        &base_url,
        "app_1",
        "https://app.example.test",
        "anonymous-exchange-1",
        Some("visitor-1".to_owned()),
    )
    .await;

    let requests = sent.lock().unwrap().clone();
    assert_eq!(requests.len(), 16);
    assert_eq!(requests[0].path, "/v1/orgs");
    assert_eq!(requests[0].body["external_ref"], "org-acme");
    assert_eq!(requests[2].body["callback_timeout_seconds"], 20);
    assert_eq!(requests[3].body["anonymous_access"], Value::Null);
    assert_eq!(requests[6].headers["idempotency-key"], "credential-create");
    assert_eq!(requests[7].body["overlap_seconds"], 60);
    assert!(requests[10].path.contains("trace_id="));
    assert!(requests[11].path.contains("search_mode=hybrid"));
    assert_eq!(requests[12].body["user_key"], "user-1");
    assert_eq!(requests[13].body["from_message"], 1);
    assert!(requests[14].path.contains("page_token=page-2"));
    assert_eq!(requests[15].headers.get("authorization"), None);
    assert_eq!(requests[15].headers["origin"], "https://app.example.test");
    assert_eq!(
        requests[15].headers["idempotency-key"],
        "anonymous-exchange-1"
    );
    assert_eq!(requests[15].body["visitor_token"], "visitor-1");
    assert!(requests[..15]
        .iter()
        .all(|request| request.headers["authorization"] == "Bearer test-key"));
    assert_eq!(requests[0].method, "POST");
}

#[tokio::test]
async fn one_time_secret_facades_do_not_retry_failed_responses() {
    let (base_url, sent) = start_server().await;
    let client = Client::new(&base_url, "test-key").unwrap();

    let mut app = RegisterAppRequest::new("support".to_owned());
    app.external_ref = Some("app-support".to_owned());
    let _ = client.register_app(app).await;
    let _ = client
        .mint_app_signing_key(
            "app_1",
            MintAppSigningKeyRequest::new(AppSigningKeyPurpose::Callback),
        )
        .await;

    let requests = sent.lock().unwrap();
    assert_eq!(requests.len(), 2);
    assert_eq!(requests[0].path, "/v1/apps");
    assert_eq!(requests[1].path, "/v1/apps/app_1/signing-keys");
}

#[test]
fn public_status_values_resources_and_error_helper_are_reachable() {
    assert_eq!(ALL_INVOCATION_STATUSES.len(), 8);
    assert_eq!(ACTIVE_INVOCATION_STATUSES.len(), 4);
    let error = NvokenError {
        category: ErrorCategory::NotFound,
        message: "missing".to_owned(),
        status: Some(404),
        code: Some("not_found".to_owned()),
        request_id: None,
        retry_after: None,
        details: None,
    };
    assert!(is_not_found(&error));
}
