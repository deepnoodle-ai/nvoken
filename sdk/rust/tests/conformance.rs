use nvoken::{models, ConversationOwner, ConversationRef, Memory};

#[tokio::test]
async fn shared_conformance_server_uses_current_model_contract() {
    let Some(base_url) = std::env::var("NVOKEN_CONFORMANCE_URL").ok() else {
        return;
    };
    let client = nvoken::Client::with_base_url("conformance", base_url);
    let models = client
        .list_models(Some("future_provider"), None, None)
        .await
        .expect("list models from shared conformance server");
    assert_eq!(models.items.len(), 1);
    assert_eq!(models.items[0].provider, "future_provider");
    assert_eq!(models.items[0].id, "future-model");
}

#[test]
fn generated_runtime_resources_remain_available() {
    let _ = std::any::TypeId::of::<models::AgentRevision>();
    let _ = std::any::TypeId::of::<models::MemorySpace>();
    let _ = std::any::TypeId::of::<models::Conversation>();
    let _ = std::any::TypeId::of::<models::Turn>();
    assert_eq!(Memory::None, Memory::None);
    assert_eq!(
        ConversationRef::by_key("case", ConversationOwner::Tenant),
        ConversationRef::by_key("case", ConversationOwner::Tenant)
    );
}
