//! The shared client-side fold contract.
//!
//! All four SDKs read this one fixture, so a language whose reducer drifts
//! fails here rather than in whichever consumer noticed first.

use nvoken::{Reducer, StreamEvent};
use serde_json::Value;

#[test]
fn shared_reducer_fixture_folds_to_one_change_per_turn() {
    let raw = include_str!("../../conformance/fixtures/reducer.json");
    let fixture: Value = serde_json::from_str(raw).expect("decode reducer fixture");

    let mut reducer = Reducer::default();
    for event in fixture["events"].as_array().expect("events") {
        reducer
            .apply(&StreamEvent {
                id: event["id"].as_str().map(str::to_owned),
                event_type: event["event"].as_str().expect("event type").to_owned(),
                data: event["data"].clone(),
                retry: None,
            })
            .expect("apply fixture event");
    }

    let snapshot = reducer.snapshot();
    let expected = &fixture["expected"];
    let sequences: Vec<u64> = snapshot.messages.iter().map(|m| m.sequence).collect();
    let expected_sequences: Vec<u64> = expected["message_sequences"]
        .as_array()
        .expect("message_sequences")
        .iter()
        .map(|value| value.as_u64().expect("sequence"))
        .collect();
    assert_eq!(sequences, expected_sequences);

    // Two revisions arrived for one Turn. The fold keeps the highest and
    // discards the earlier one, so the log never grows a second entry that
    // also claims to be current.
    let revisions: Vec<u64> = snapshot.turn_changes.iter().map(|c| c.revision).collect();
    let expected_revisions: Vec<u64> = expected["turn_revisions"]
        .as_array()
        .expect("turn_revisions")
        .iter()
        .map(|value| value.as_u64().expect("revision"))
        .collect();
    assert_eq!(revisions, expected_revisions);

    assert_eq!(snapshot.cursor.as_deref(), expected["cursor"].as_str());
    assert_eq!(
        snapshot.previews.len(),
        expected["previews"].as_array().expect("previews").len()
    );
    assert!(reducer.settled(&snapshot.turn_changes[0].turn_id));
}
