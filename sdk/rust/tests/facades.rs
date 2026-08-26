use nvoken::Client;

#[test]
fn raw_exposes_the_exact_generated_transport_configuration() {
    let client = Client::with_base_url("test", "http://localhost:9090/");
    assert_eq!(client.raw().base_path, "http://localhost:9090");
    assert_eq!(client.raw().bearer_access_token.as_deref(), Some("test"));
    assert_eq!(
        client.raw().configuration().base_path,
        "http://localhost:9090"
    );
}

#[test]
fn turn_recovery_is_local() {
    let client = Client::new("test");
    let turn = client.turn("turn_123", "acme", Some("alice".into()));
    assert_eq!(turn.id, "turn_123");
    assert_eq!(turn.tenant, "acme");
    assert_eq!(turn.user.as_deref(), Some("alice"));
}
