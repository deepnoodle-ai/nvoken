use nvoken::{Behavior, OwnedBy};

#[test]
fn behavior_and_owner_use_target_vocabulary() {
    let behavior = Behavior::new("Analyze", "openai/gpt-5");
    assert_eq!(behavior.instructions(), "Analyze");
    assert_eq!(
        OwnedBy::user("acme", "alice").user.as_deref(),
        Some("alice")
    );
}
