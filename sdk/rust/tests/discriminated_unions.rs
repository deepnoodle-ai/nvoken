//! Every discriminator union the contract publishes decodes from the wire
//! shape the service sends, and encodes back to exactly that shape.
//!
//! The Rust generator emits discriminator unions as internally tagged enums
//! whose branch structs also declare the tag as a required field. Serde
//! consumes the tag when it routes to the branch, and the branch then reports
//! the field missing, so with the tag left in place none of these decode.
//! `sdk/scripts/generate.sh` rewrites every one of them to `untagged`; the
//! closed literal on each branch does the discriminating. These tests are what
//! fails if a union is generated and not rewritten.

use nvoken::models;
use serde::{de::DeserializeOwned, Serialize};
use serde_json::{json, Value};

/// Decodes `wire`, encodes the result, and insists the encoding is `wire`
/// again: one discriminator, not two, and nothing dropped.
fn round_trip<T: DeserializeOwned + Serialize>(wire: Value) -> T {
    let decoded: T = serde_json::from_value(wire.clone())
        .unwrap_or_else(|error| panic!("{wire} did not decode: {error}"));
    let encoded = serde_json::to_value(&decoded).expect("encode");
    assert_eq!(encoded, wire, "re-encoding {wire} changed the wire shape");
    decoded
}

#[test]
fn agent_owner_decodes_every_branch() {
    assert!(matches!(
        round_trip::<models::AgentOwner>(json!({"kind": "app"})),
        models::AgentOwner::App(_)
    ));
    let models::AgentOwner::Tenant(tenant) =
        round_trip::<models::AgentOwner>(json!({"kind": "tenant", "tenant_key": "acme"}))
    else {
        panic!("tenant branch");
    };
    assert_eq!(tenant.tenant_key, "acme");
    let models::AgentOwner::User(user) = round_trip::<models::AgentOwner>(
        json!({"kind": "user", "tenant_key": "acme", "user_key": "alice"}),
    ) else {
        panic!("user branch");
    };
    assert_eq!(
        (user.tenant_key.as_str(), user.user_key.as_str()),
        ("acme", "alice")
    );
}

#[test]
fn conversation_owner_decodes_every_branch() {
    assert!(matches!(
        round_trip::<models::ConversationOwner>(json!({"kind": "tenant"})),
        models::ConversationOwner::Tenant(_)
    ));
    let models::ConversationOwner::User(user) =
        round_trip::<models::ConversationOwner>(json!({"kind": "user", "user_key": "alice"}))
    else {
        panic!("user branch");
    };
    assert_eq!(user.user_key, "alice");
}

#[test]
fn default_memory_policy_decodes_every_branch() {
    assert!(matches!(
        round_trip::<models::DefaultMemoryPolicy>(json!({"default_scope": "none"})),
        models::DefaultMemoryPolicy::None(_)
    ));
    let models::DefaultMemoryPolicy::User(user) = round_trip::<models::DefaultMemoryPolicy>(
        json!({"default_scope": "user", "namespace": "notes"}),
    ) else {
        panic!("user branch");
    };
    assert_eq!(user.namespace.as_deref(), Some("notes"));
    // The tenant and user branches share every field but the literal, so the
    // literal alone has to pick between them.
    let models::DefaultMemoryPolicy::Tenant(tenant) =
        round_trip::<models::DefaultMemoryPolicy>(json!({"default_scope": "tenant"}))
    else {
        panic!("tenant branch");
    };
    assert_eq!(tenant.namespace, None);
}

#[test]
fn turn_memory_selection_decodes_every_branch() {
    assert!(matches!(
        round_trip::<models::TurnMemorySelection>(json!({"scope": "none"})),
        models::TurnMemorySelection::None(_)
    ));
    assert!(matches!(
        round_trip::<models::TurnMemorySelection>(json!({"scope": "user", "namespace": "notes"})),
        models::TurnMemorySelection::User(_)
    ));
    assert!(matches!(
        round_trip::<models::TurnMemorySelection>(json!({"scope": "tenant"})),
        models::TurnMemorySelection::Tenant(_)
    ));
}

#[test]
fn memory_space_selector_decodes_every_branch() {
    let models::MemorySpaceSelector::User(user) = round_trip::<models::MemorySpaceSelector>(
        json!({"scope": "user", "user_key": "alice", "namespace": "notes"}),
    ) else {
        panic!("user branch");
    };
    assert_eq!(
        (user.user_key.as_str(), user.namespace.as_str()),
        ("alice", "notes")
    );
    assert!(matches!(
        round_trip::<models::MemorySpaceSelector>(
            json!({"scope": "tenant", "namespace": "shared"})
        ),
        models::MemorySpaceSelector::Tenant(_)
    ));
}

#[test]
fn turn_behavior_selection_decodes_every_branch() {
    let models::TurnBehaviorSelection::Agent(agent) =
        round_trip::<models::TurnBehaviorSelection>(json!({
            "kind": "agent",
            "agent": {"agent_id": "0192b1a0-5c3e-7d4f-8a6b-1c2d3e4f5a6b", "revision": "current"},
        }))
    else {
        panic!("agent branch");
    };
    assert!(matches!(
        *agent.agent,
        models::AgentSelector::AgentSelectorById(_)
    ));
    let models::TurnBehaviorSelection::Inline(inline) =
        round_trip::<models::TurnBehaviorSelection>(json!({
            "kind": "inline",
            "behavior": {"instructions": "Help", "model": "openai/gpt-5"},
        }))
    else {
        panic!("inline branch");
    };
    assert_eq!(inline.behavior.instructions, "Help");
}

#[test]
fn turn_behavior_source_decodes_every_branch() {
    let models::TurnBehaviorSource::AgentRevision(stored) =
        round_trip::<models::TurnBehaviorSource>(json!({
            "kind": "agent_revision",
            "agent_id": "0192b1a0-5c3e-7d4f-8a6b-1c2d3e4f5a6b",
            "agent_revision_id": "0192b1a0-6d4f-7e5a-9b7c-2d3e4f5a6b7c",
            "revision": 3,
        }))
    else {
        panic!("agent_revision branch");
    };
    assert_eq!(stored.revision, 3);
    let models::TurnBehaviorSource::Inline(inline) =
        round_trip::<models::TurnBehaviorSource>(json!({"kind": "inline", "digest": "sha256:abc"}))
    else {
        panic!("inline branch");
    };
    assert_eq!(inline.digest, "sha256:abc");
}

#[test]
fn delivery_behavior_source_decodes_every_branch() {
    assert!(matches!(
        round_trip::<models::DeliveryBehaviorSource>(json!({
            "kind": "agent_revision",
            "agent_id": "0192b1a0-5c3e-7d4f-8a6b-1c2d3e4f5a6b",
            "agent_revision_id": "0192b1a0-6d4f-7e5a-9b7c-2d3e4f5a6b7c",
            "revision": 1,
        })),
        models::DeliveryBehaviorSource::AgentRevision(_)
    ));
    assert!(matches!(
        round_trip::<models::DeliveryBehaviorSource>(
            json!({"kind": "inline", "digest": "sha256:abc"})
        ),
        models::DeliveryBehaviorSource::Inline(_)
    ));
}

#[test]
fn browser_conversation_access_decodes_every_branch() {
    assert!(matches!(
        round_trip::<models::BrowserConversationAccess>(json!({"scope": "standalone_only"})),
        models::BrowserConversationAccess::StandaloneOnly(_)
    ));
    let models::BrowserConversationAccess::Exact(exact) =
        round_trip::<models::BrowserConversationAccess>(
            json!({"scope": "exact", "conversation_id": "18325d9f-b9bc-797d-9259-96ece372defd"}),
        )
    else {
        panic!("exact branch");
    };
    assert_eq!(
        exact.conversation_id,
        "18325d9f-b9bc-797d-9259-96ece372defd"
    );
    assert!(matches!(
        round_trip::<models::BrowserConversationAccess>(json!({"scope": "user_conversations"})),
        models::BrowserConversationAccess::UserConversations(_)
    ));
}

#[test]
fn browser_memory_access_decodes_every_branch() {
    assert!(matches!(
        round_trip::<models::BrowserMemoryAccess>(json!({"scope": "none"})),
        models::BrowserMemoryAccess::None(_)
    ));
    let models::BrowserMemoryAccess::User(user) =
        round_trip::<models::BrowserMemoryAccess>(json!({"scope": "user", "namespace": "notes"}))
    else {
        panic!("user branch");
    };
    assert_eq!(user.namespace, "notes");
}

#[test]
fn a_wrong_literal_is_refused_rather_than_misrouted() {
    // Untagged decoding tries each branch in turn. A literal none of them
    // declares must fail, not land in the first branch that happens to have
    // no other required field.
    assert!(serde_json::from_value::<models::AgentOwner>(json!({"kind": "installation"})).is_err());
    assert!(serde_json::from_value::<models::ConversationOwner>(json!({"kind": "app"})).is_err());
    assert!(
        serde_json::from_value::<models::TurnMemorySelection>(json!({"scope": "everyone"}))
            .is_err()
    );
}

/// `Agent.owner` is required, so this is the response `client.agent(...)`
/// decodes on every call. It is the reason the defect was not a corner case.
/// The nullable fields are present as `null` because the contract requires
/// them, and the service sends them that way.
#[test]
fn an_agent_response_decodes() {
    let agent: models::Agent = round_trip(json!({
        "id": "0192b1a0-5c3e-7d4f-8a6b-1c2d3e4f5a6b",
        "agent_key": "support",
        "name": "Support",
        "owner": {"kind": "tenant", "tenant_key": "acme"},
        "current_revision": 4,
        "created_at": "2026-09-01T12:00:00Z",
        "updated_at": "2026-09-02T08:30:00Z",
        "archived_at": null,
    }));
    assert_eq!(agent.agent_key, "support");
    let models::AgentOwner::Tenant(owner) = *agent.owner else {
        panic!("owner");
    };
    assert_eq!(owner.tenant_key, "acme");
}

/// `Conversation.owner` is required too, so every Conversation-carrying
/// response, the transcript snapshot included, went through this.
#[test]
fn a_conversation_response_decodes() {
    let conversation: models::Conversation = round_trip(json!({
        "id": "18325d9f-b9bc-797d-9259-96ece372defd",
        "tenant_key": "acme",
        "owner": {"kind": "user", "user_key": "alice"},
        "conversation_key": "case-42",
        "forked_from": null,
        "active_turn_id": null,
        "active_turn_status": null,
        "retention": null,
        "compaction": null,
        "metadata": null,
        "expires_at": null,
        "created_at": "2026-09-01T12:00:00Z",
        "updated_at": "2026-09-02T08:30:00Z",
    }));
    assert_eq!(conversation.conversation_key.as_deref(), Some("case-42"));
    let models::ConversationOwner::User(owner) = *conversation.owner else {
        panic!("owner");
    };
    assert_eq!(owner.user_key, "alice");
}
