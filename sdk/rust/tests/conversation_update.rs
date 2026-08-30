use nvoken::models::{RetentionPolicy, UpdateConversationRequest};
use serde_json::json;

#[test]
fn conversation_policy_updates_preserve_omit_clear_and_replace() {
    assert_eq!(
        serde_json::to_value(UpdateConversationRequest::new()).unwrap(),
        json!({})
    );

    let mut clear = UpdateConversationRequest::new();
    clear.retention = Some(None);
    clear.compaction = Some(None);
    assert_eq!(
        serde_json::to_value(clear).unwrap(),
        json!({"retention": null, "compaction": null})
    );

    let mut replace = UpdateConversationRequest::new();
    replace.retention = Some(Some(Box::new(RetentionPolicy::new(3600))));
    assert_eq!(
        serde_json::to_value(replace).unwrap(),
        json!({"retention": {"ttl_seconds": 3600}})
    );
}
