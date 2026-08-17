from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass
from typing import Any, AsyncIterator, Awaitable, Callable, TYPE_CHECKING

from nvoken_generated.models.invocation_change import InvocationChange
from nvoken_generated.models.session_message import SessionMessage
from nvoken_generated.models.transcript_update_event import TranscriptUpdateEvent

from .invocation_status import is_turn_over

if TYPE_CHECKING:
    from .client import Client, InvocationHandle


@dataclass(frozen=True)
class StreamEvent:
    type: str
    data: Any
    id: str | None = None
    retry: float | None = None


@dataclass(frozen=True)
class ReducedSnapshot:
    messages: list[SessionMessage]
    invocation_changes: list[InvocationChange]
    previews: list[StreamPreview]
    cursor: str | None


@dataclass(frozen=True)
class StreamPreview:
    """One message the model is writing, before it is saved.

    ``delta`` carries the fragments for every ``kind``, because one accumulator
    handles all of them. ``message_id`` names the saved message this preview is
    building, and it is the key: the handoff to the saved message updates a row
    that already has its permanent identity.
    """

    invocation_id: str
    attempt: int
    message_id: str
    content_index: int
    kind: str
    delta: str
    tool_call_id: str | None = None
    name: str | None = None


class Reducer:
    def __init__(self) -> None:
        self._messages: dict[int, SessionMessage] = {}
        self._changes: dict[tuple[str, int], InvocationChange] = {}
        self._previews: dict[tuple[str, int], StreamPreview] = {}
        self._latest_attempts: dict[str, int] = {}
        self._terminal_invocations: set[str] = set()
        self._cursor: str | None = None

    def apply(self, event: StreamEvent) -> None:
        if event.type == "message.delta":
            self._append_preview(event.data)
            return
        if event.type == "stream.resync":
            # An absent Invocation is scope: discard previews for the whole
            # Session.
            invocation_id = event.data.get("invocation_id")
            if invocation_id is None:
                self._previews.clear()
                self._latest_attempts.clear()
            else:
                self._discard_previews(invocation_id)
            return
        if event.type != "transcript.update":
            return
        update = TranscriptUpdateEvent.from_dict(event.data)
        assert update is not None
        # Messages before changes, so a turn is never marked settled before its
        # final message exists.
        for message in update.messages:
            self._messages[message.sequence] = message
            if message.role.value == "assistant" and message.invocation_id:
                self._discard_previews(message.invocation_id)
        for change in update.invocation_changes:
            self._changes[(change.invocation_id, change.revision)] = change
            if is_turn_over(change):
                self._terminal_invocations.add(change.invocation_id)
                self._discard_previews(change.invocation_id)
        self._cursor = event.id or update.cursor or self._cursor

    def settled(self, invocation_id: str) -> bool:
        """Whether a change carrying a terminal status has arrived for this turn.

        That is the terminal signal, and there is no other.
        """
        return invocation_id in self._terminal_invocations

    def snapshot(self) -> ReducedSnapshot:
        return ReducedSnapshot(
            messages=sorted(self._messages.values(), key=lambda message: message.sequence),
            invocation_changes=sorted(
                self._changes.values(),
                key=lambda change: (change.invocation_id, change.revision),
            ),
            previews=sorted(
                self._previews.values(),
                key=lambda preview: (preview.message_id, preview.content_index),
            ),
            cursor=self._cursor,
        )

    def _append_preview(self, data: dict[str, Any]) -> None:
        invocation_id = data["invocation_id"]
        attempt = data["attempt"]
        if invocation_id in self._terminal_invocations:
            return
        latest = self._latest_attempts.get(invocation_id)
        if latest is not None and attempt < latest:
            return
        if latest is None or attempt > latest:
            self._discard_previews(invocation_id)
        self._latest_attempts[invocation_id] = attempt
        key = (data["message_id"], data["content_index"])
        current = self._previews.get(key)
        self._previews[key] = StreamPreview(
            invocation_id=invocation_id,
            attempt=attempt,
            message_id=data["message_id"],
            content_index=data["content_index"],
            kind=data["kind"],
            delta=(current.delta if current else "") + data["delta"],
            tool_call_id=data.get("tool_call_id") or (current.tool_call_id if current else None),
            name=data.get("name") or (current.name if current else None),
        )

    def _discard_previews(self, invocation_id: str) -> None:
        self._previews = {
            key: preview
            for key, preview in self._previews.items()
            if preview.invocation_id != invocation_id
        }
        self._latest_attempts.pop(invocation_id, None)


async def stream_session(
    client: Client,
    session_id: str,
    reducer: Reducer,
    consume: Callable[[StreamEvent, ReducedSnapshot], Awaitable[None] | None],
    *,
    deltas: bool = True,
) -> None:
    """Subscribe to a Session.

    It never returns on its own: the stream stays open while the Session is
    idle and a turn started later appears on it. Leave it by cancelling the
    task, or by raising from ``consume``.
    """
    async for event in _read_stream(client, session_id, None, reducer, deltas=deltas):
        consumed = consume(event, reducer.snapshot())
        if consumed is not None:
            await consumed


async def stream_invocation(
    client: Client,
    handle: InvocationHandle,
    consume: Callable[[StreamEvent], Awaitable[None] | None],
    *,
    deltas: bool = True,
) -> None:
    async for event in iter_invocation(client, handle, deltas=deltas):
        consumed = consume(event)
        if consumed is not None:
            await consumed


async def iter_invocation(
    client: Client,
    handle: InvocationHandle,
    *,
    deltas: bool = True,
) -> AsyncIterator[StreamEvent]:
    """Follow one turn, ending once a change for it carries a terminal status."""
    session_id = await handle.require_session_id()
    reducer = Reducer()
    async for event in _read_stream(
        client, session_id, handle.invocation_id, reducer, deltas=deltas
    ):
        yield event
        if reducer.settled(handle.invocation_id):
            return


async def _read_stream(
    client: Client,
    session_id: str,
    invocation_id: str | None,
    reducer: Reducer,
    *,
    deltas: bool,
) -> AsyncIterator[StreamEvent]:
    """The one read loop.

    It reconnects from its last durable cursor on any connection end. A
    ``connection.closing`` frame says only that, and a silent drop says nothing
    at all, so neither is a reason to stop.
    """
    retry = 1.0
    while True:
        response = await client.stream_sessions.stream_session_without_preload_content(
            session_id,
            invocation_id=invocation_id,
            cursor=None,
            deltas=deltas,
            last_event_id=reducer.snapshot().cursor,
        )
        try:
            if response.is_error:
                from .client import normalize_httpx_response
                raise await normalize_httpx_response(response)
            async for event in parse_sse(response.aiter_lines()):
                if event.retry is not None:
                    retry = min(event.retry, 30.0)
                if event.data is None:
                    # A frame carrying only `retry:` is a control frame, not an
                    # event. The runtime opens every stream with one. Its
                    # bookkeeping is applied above; there is nothing to reduce.
                    continue
                reducer.apply(event)
                yield event
        except asyncio.CancelledError:
            raise
        except Exception as error:
            from .client import NvokenError
            if isinstance(error, NvokenError):
                raise
        finally:
            await response.aclose()
        await asyncio.sleep(retry)


async def parse_sse(lines: AsyncIterator[str]) -> AsyncIterator[StreamEvent]:
    event_type: str | None = None
    event_id: str | None = None
    retry: float | None = None
    data: list[str] = []
    async for line in lines:
        if line == "":
            if event_type is not None or event_id is not None or data or retry is not None:
                yield StreamEvent(
                    type=event_type or "message",
                    id=event_id,
                    retry=retry,
                    data=json.loads("\n".join(data)) if data else None,
                )
            event_type = None
            event_id = None
            retry = None
            data = []
            continue
        if line.startswith(":"):
            continue
        field, separator, raw = line.partition(":")
        value = raw[1:] if separator and raw.startswith(" ") else raw
        if field == "event":
            event_type = value
        elif field == "id":
            event_id = value
        elif field == "data":
            data.append(value)
        elif field == "retry" and value.isdigit():
            retry = int(value) / 1000
    if event_type is not None or event_id is not None or data or retry is not None:
        yield StreamEvent(
            type=event_type or "message",
            id=event_id,
            retry=retry,
            data=json.loads("\n".join(data)) if data else None,
        )
