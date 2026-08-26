from nvoken.turn_status import TERMINAL_TURN_STATUSES


def test_turn_terminal_statuses() -> None:
    assert TERMINAL_TURN_STATUSES == {"completed", "incomplete", "failed", "cancelled"}
