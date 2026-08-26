use std::path::PathBuf;
use std::time::{Duration, SystemTime};

use base64::engine::general_purpose::STANDARD;
use base64::Engine as _;
use http::{HeaderMap, HeaderName, HeaderValue};
use nvoken::{
    mint_client_token, verify_callback, verify_webhook, ClientTokenClaims,
    ClientTokenConversationAccess, ClientTokenMemoryAccess,
};
use serde_json::Value;

fn document(name: &str) -> Value {
    let path = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../docs/design")
        .join(name);
    serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap()
}

fn headers(vector: &Value) -> HeaderMap {
    vector["headers"]
        .as_object()
        .unwrap()
        .iter()
        .map(|(name, value)| {
            (
                HeaderName::from_bytes(name.as_bytes()).unwrap(),
                HeaderValue::from_str(value.as_str().unwrap()).unwrap(),
            )
        })
        .collect()
}

#[test]
fn signed_delivery_v2_exposes_turn_facts() {
    let document = document("delivery-signing-v1.json");
    let vectors = &document["vectors"];
    let now = SystemTime::UNIX_EPOCH + Duration::from_secs(1_784_635_200);
    let callback = &vectors["callback"];
    let verified_callback = verify_callback(
        b"0123456789abcdef0123456789abcdef",
        &headers(callback),
        callback["body"].as_str().unwrap().as_bytes(),
        now,
    )
    .unwrap();
    assert!(verified_callback.turn_id.starts_with("turn_"));
    assert!(verified_callback
        .conversation_id
        .as_deref()
        .unwrap()
        .starts_with("conv_"));
    assert_eq!(verified_callback.behavior_source["kind"], "agent_revision");
    assert_eq!(verified_callback.tenant_key, "acme");

    let webhook = &vectors["webhook"];
    let verified_webhook = verify_webhook(
        b"0123456789abcdef0123456789abcdef",
        &headers(webhook),
        webhook["body"].as_str().unwrap().as_bytes(),
        now,
    )
    .unwrap();
    assert_eq!(verified_webhook.event.to_string(), "turn.ended");
    assert_eq!(verified_webhook.turn_id, verified_callback.turn_id);
    assert_eq!(
        verified_webhook.conversation_id,
        verified_callback.conversation_id
    );
}

#[test]
fn client_token_matches_published_v2_vector() {
    let document = document("client-token-v2.json");
    let claims = &document["claims"];
    let token = mint_client_token(
        &STANDARD
            .decode(
                document["signing_key"]["private_key_seed"]
                    .as_str()
                    .unwrap(),
            )
            .unwrap(),
        &ClientTokenClaims {
            app_id: claims["iss"].as_str().unwrap().into(),
            key_id: document["signing_key"]["key_id"].as_str().unwrap().into(),
            subject: claims["sub"].as_str().unwrap().into(),
            tenant_key: claims["tenant_key"].as_str().unwrap().into(),
            agent_id: claims["agent_id"].as_str().unwrap().into(),
            agent_revision_id: claims["agent_revision_id"].as_str().unwrap().into(),
            memory_access: ClientTokenMemoryAccess::User {
                namespace: claims["memory_access"]["namespace"]
                    .as_str()
                    .unwrap()
                    .into(),
            },
            conversation_access: ClientTokenConversationAccess::Exact {
                conversation_id: claims["conversation_access"]["conversation_id"]
                    .as_str()
                    .unwrap()
                    .into(),
            },
            issued_at: Some(
                SystemTime::UNIX_EPOCH + Duration::from_secs(claims["iat"].as_u64().unwrap()),
            ),
            lifetime: Duration::from_secs(
                claims["exp"].as_u64().unwrap() - claims["iat"].as_u64().unwrap(),
            ),
        },
    )
    .unwrap();
    assert_eq!(token, document["token"].as_str().unwrap());
}
