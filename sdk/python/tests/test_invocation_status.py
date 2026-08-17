from __future__ import annotations

from types import SimpleNamespace

from nvoken import (
    InvocationStatus,
    TERMINAL_INVOCATION_STATUSES,
    is_terminal_status,
    is_turn_over,
)

# The classification is exhaustive over the contract's enum, so a status added
# later fails here until someone says which side it belongs on. Exporting the
# predicate means hosts stopped keeping their own copy, which makes this the
# copy that has to be right.
CLASSIFICATION = {
    "queued": False,
    "running": False,
    "waiting": False,
    "budget_hold": False,
    "completed": True,
    "incomplete": True,
    "failed": True,
    "cancelled": True,
}


def test_classifies_every_status_the_contract_declares() -> None:
    declared = {status.value for status in InvocationStatus}
    assert declared == set(CLASSIFICATION)
    for status, terminal in CLASSIFICATION.items():
        assert is_terminal_status(status) is terminal, status
    assert TERMINAL_INVOCATION_STATUSES == {
        status for status, terminal in CLASSIFICATION.items() if terminal
    }


def test_budget_hold_is_not_terminal() -> None:
    # It stopped on spending capacity with its deadlines on hold. It still owns
    # the Session and resumes on its own once the account is funded, so reading
    # it as over abandons a turn that is still going.
    assert is_terminal_status("budget_hold") is False
    assert is_terminal_status(InvocationStatus.BUDGET_HOLD) is False


def test_unrecognized_status_is_not_terminal() -> None:
    assert is_terminal_status("some_status_added_later") is False


def test_is_turn_over_accepts_either_witness() -> None:
    # Both are present and agreeing in normal operation. The status alone covers
    # a server too old to send the field; the field alone covers a status this
    # build has never heard of.
    assert is_turn_over(SimpleNamespace(status="completed", terminal=True)) is True
    assert is_turn_over(SimpleNamespace(status="completed", terminal=None)) is True
    assert is_turn_over(SimpleNamespace(status="something_new", terminal=True)) is True
    assert is_turn_over({"status": "completed"}) is True


def test_is_turn_over_answers_for_the_change_not_the_turn() -> None:
    # A replayed `running` change from a turn that has since ended reports
    # False, which is what lets a reader fold messages before changes and never
    # mark a turn settled before its final message exists.
    assert is_turn_over(SimpleNamespace(status="running", terminal=False)) is False
