use nvoken::{models, ConversationOwner, ConversationRef, Memory};

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
