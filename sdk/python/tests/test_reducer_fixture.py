"""The shared client-side fold contract.

All four SDKs read this one fixture, so a language whose reducer drifts fails
here rather than in whichever consumer noticed first.
"""

from __future__ import annotations

import json
from pathlib import Path

from nvoken.stream import Reducer, StreamEvent

FIXTURE = Path(__file__).resolve().parents[2] / "conformance" / "fixtures" / "reducer.json"


def test_shared_reducer_fixture_folds_to_one_change_per_turn() -> None:
    fixture = json.loads(FIXTURE.read_text())
    reducer = Reducer()
    for event in fixture["events"]:
        reducer.apply(
            StreamEvent(type=event["event"], data=event["data"], id=event.get("id")),
        )

    snapshot = reducer.snapshot()
    expected = fixture["expected"]
    assert [message.sequence for message in snapshot.messages] == expected["message_sequences"]
    # Two revisions arrived for one Turn. The fold keeps the highest and
    # discards the earlier one, so the log never grows a second entry that
    # also claims to be current.
    assert [change.revision for change in snapshot.turn_changes] == expected["turn_revisions"]
    assert snapshot.cursor == expected["cursor"]
    assert snapshot.previews == expected["previews"]
    assert reducer.settled(snapshot.turn_changes[0].turn_id)
