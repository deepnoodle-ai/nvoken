use nvoken::{Behavior, Client, InlineMemory, InlineTurnOptions, Memory, Tool, TurnOptions};
use serde_json::json;

#[test]
fn inline_tool_binding_is_local_and_immutable() {
    let client = Client::with_base_url("test", "http://localhost");
    let inline = client.inline(
        Behavior::new("Help", "openai/gpt-5")
            .host_tool("lookup", "Find a record", json!({"type": "object"}))
            .unwrap(),
    );
    let bound = inline
        .bind_tools([Tool::new("lookup", |arguments, _context| async move {
            Ok(arguments)
        })])
        .unwrap();
    assert_eq!(inline.behavior.tools().len(), 1);
    assert_eq!(bound.behavior.tools().len(), 1);
}

#[test]
fn turn_options_keep_memory_and_actor_explicit() {
    let options = TurnOptions::new("acme")
        .user("alice")
        .memory(Memory::tenant("portfolio"));
    assert_eq!(options.tenant, "acme");
    assert_eq!(options.user.as_deref(), Some("alice"));
    assert_eq!(
        options.memory,
        Some(Memory::Tenant {
            namespace: Some("portfolio".into())
        })
    );
}

#[test]
fn inline_options_require_the_closed_inline_memory_type() {
    let memory = InlineMemory::tenant("portfolio").unwrap();
    let options = InlineTurnOptions::new("acme").memory(memory.clone());
    assert_eq!(options.memory, Some(memory));
}
