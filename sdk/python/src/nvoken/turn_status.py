"""Turn lifecycle classification shared by facade and stream reducers."""

from __future__ import annotations

from typing import Any

ALL_TURN_STATUSES: tuple[str, ...] = (
    "queued", "running", "waiting", "budget_hold",
    "completed", "incomplete", "failed", "cancelled",
)
TERMINAL_TURN_STATUSES = frozenset({"completed", "incomplete", "failed", "cancelled"})
ACTIVE_TURN_STATUSES = tuple(
    status for status in ALL_TURN_STATUSES if status not in TERMINAL_TURN_STATUSES
)


def is_terminal_status(status: Any) -> bool:
    return getattr(status, "value", status) in TERMINAL_TURN_STATUSES


def is_turn_over(change: Any) -> bool:
    terminal = getattr(change, "terminal", None)
    if terminal is None and isinstance(change, dict):
        terminal = change.get("terminal")
    if terminal is True:
        return True
    status = getattr(change, "status", None)
    if status is None and isinstance(change, dict):
        status = change.get("status")
    return is_terminal_status(status)
