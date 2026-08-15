"""Which Invocation statuses mean a turn has stopped.

Exported so no caller keeps a copy, for the same reason
``answerable_tool_calls`` is: the classification is part of the protocol, and
rediscovering it in every application is how one of them gets it wrong.
"""

from __future__ import annotations

from typing import Any

TERMINAL_INVOCATION_STATUSES = frozenset(
    {"completed", "incomplete", "failed", "cancelled"}
)


def is_terminal_status(status: Any) -> bool:
    """Whether a status means the turn is over.

    There are eight statuses and four of them are terminal, so the interesting
    mistake is writing the *other* four out. ``queued``, ``running``,
    ``waiting``, and ``paused`` differ only in what unblocks them — a paused
    turn stopped on spending capacity with its deadlines on hold, and resumes
    on its own once its account is funded — and a turn wrongly believed
    finished is one nobody settles, reattaches to, or cancels before erasing
    its Session.

    A status this build does not recognize is reported as *not* terminal, which
    is the safe direction: you wait on a turn that already ended rather than
    abandoning one that has not.
    """
    return _status_value(status) in TERMINAL_INVOCATION_STATUSES


def is_turn_over(change: Any) -> bool:
    """Whether a change ends the turn.

    **This is the terminal signal, and there is no other** — there is no result
    frame, and ``stream.end`` speaks only about a connection.

    It answers for the change, not for the turn: a replayed ``running`` change
    reports ``False`` even after the turn has ended, which is what lets you fold
    messages before changes and never mark a turn settled before its final
    message exists.

    Either witness suffices. The field and the status always agree when both are
    present — nvoken computes one from the other — so accepting either keeps
    this correct against a server too old to send the field.
    """
    terminal = getattr(change, "terminal", None)
    if terminal is None and isinstance(change, dict):
        terminal = change.get("terminal")
    if terminal is True:
        return True
    status = getattr(change, "status", None)
    if status is None and isinstance(change, dict):
        status = change.get("status")
    return is_terminal_status(status)


def _status_value(status: Any) -> Any:
    """Accept the generated enum or the plain string it wraps."""
    return getattr(status, "value", status)
