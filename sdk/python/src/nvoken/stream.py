from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass
from typing import Any, AsyncIterator, Awaitable, Callable, Mapping, TYPE_CHECKING

from nvoken_generated.models.conversation_message import ConversationMessage
from nvoken_generated.models.transcript_update_event import TranscriptUpdateEvent
from nvoken_generated.models.turn_change import TurnChange
from httpx import TimeoutException, TransportError

from .turn_status import is_turn_over

if TYPE_CHECKING:
    from .facade import Client, Turn


@dataclass(frozen=True)
class StreamEvent:
    type: str
    data: Any
    id: str | None = None
    retry: float | None = None


@dataclass(frozen=True)
class StreamPreview:
    turn_id: str
    attempt: int
    message_id: str
    content_index: int
    kind: str
    delta: str
    tool_call_id: str | None = None
    name: str | None = None


@dataclass(frozen=True)
class ReducedSnapshot:
    messages: list[ConversationMessage]
    turn_changes: list[TurnChange]
    previews: list[StreamPreview]
    cursor: str | None


class Reducer:
    """Fold durable transcript frames and provisional deltas into one view."""

    def __init__(self) -> None:
        self._messages: dict[int, ConversationMessage] = {}
        self._changes: dict[tuple[str, int], TurnChange] = {}
        self._previews: dict[tuple[str, int], StreamPreview] = {}
        self._latest_attempts: dict[str, int] = {}
        self._terminal_turns: set[str] = set()
        self._cursor: str | None = None

    def apply(self, event: StreamEvent) -> None:
        if event.type == "message.delta":
            self._append_preview(event.data)
            return
        if event.type == "stream.resync":
            turn_id = event.data.get("turn_id")
            if turn_id is None:
                self._previews.clear()
                self._latest_attempts.clear()
            else:
                self._discard_previews(turn_id)
            return
        if event.type != "transcript.update":
            return
        update = TranscriptUpdateEvent.from_dict(event.data)
        assert update is not None
        for message in update.messages:
            self._messages[message.sequence] = message
            if message.role.value == "assistant" and message.turn_id:
                self._discard_previews(message.turn_id)
        for change in update.turn_changes:
            self._changes[(change.turn_id, change.revision)] = change
            if is_turn_over(change):
                self._terminal_turns.add(change.turn_id)
                self._discard_previews(change.turn_id)
        self._cursor = event.id or update.cursor or self._cursor

    def settled(self, turn_id: str) -> bool:
        return turn_id in self._terminal_turns

    def snapshot(self) -> ReducedSnapshot:
        return ReducedSnapshot(
            messages=sorted(self._messages.values(), key=lambda message: message.sequence),
            turn_changes=sorted(
                self._changes.values(), key=lambda change: (change.turn_id, change.revision)
            ),
            previews=sorted(
                self._previews.values(), key=lambda preview: (preview.message_id, preview.content_index)
            ),
            cursor=self._cursor,
        )

    def _append_preview(self, data: dict[str, Any]) -> None:
        turn_id = data["turn_id"]
        attempt = data["attempt"]
        if turn_id in self._terminal_turns:
            return
        latest = self._latest_attempts.get(turn_id)
        if latest is not None and attempt < latest:
            return
        if latest is None or attempt > latest:
            self._discard_previews(turn_id)
        self._latest_attempts[turn_id] = attempt
        key = (data["message_id"], data["content_index"])
        current = self._previews.get(key)
        self._previews[key] = StreamPreview(
            turn_id=turn_id,
            attempt=attempt,
            message_id=data["message_id"],
            content_index=data["content_index"],
            kind=data["kind"],
            delta=(current.delta if current else "") + data["delta"],
            tool_call_id=data.get("tool_call_id") or (current.tool_call_id if current else None),
            name=data.get("name") or (current.name if current else None),
        )

    def _discard_previews(self, turn_id: str) -> None:
        self._previews = {
            key: preview for key, preview in self._previews.items() if preview.turn_id != turn_id
        }
        self._latest_attempts.pop(turn_id, None)


async def stream_conversation(
    client: Client,
    conversation_id: str,
    reducer: Reducer,
    consume: Callable[[StreamEvent, ReducedSnapshot], Awaitable[None] | None],
    *,
    deltas: bool = True,
) -> None:
    async for event in _read_stream(
        client, conversation_id=conversation_id, turn_id=None, reducer=reducer, deltas=deltas
    ):
        consumed = consume(event, reducer.snapshot())
        if consumed is not None:
            await consumed


async def iter_turn(client: Client, turn: Turn, *, deltas: bool = True) -> AsyncIterator[StreamEvent]:
    reducer = Reducer()
    async for event in _read_stream(
        client,
        conversation_id=None,
        turn_id=turn.id,
        reducer=reducer,
        deltas=deltas,
        access_headers=turn._access_headers(),
    ):
        yield event
        if reducer.settled(turn.id):
            return


async def stream_turn(
    client: Client,
    turn: Turn,
    consume: Callable[[StreamEvent], Awaitable[None] | None],
    *,
    deltas: bool = True,
) -> None:
    async for event in iter_turn(client, turn, deltas=deltas):
        consumed = consume(event)
        if consumed is not None:
            await consumed


async def _read_stream(
    client: Client,
    *,
    conversation_id: str | None,
    turn_id: str | None,
    reducer: Reducer,
    deltas: bool,
    cursor: str | None = None,
    access_headers: Mapping[str, str] | None = None,
) -> AsyncIterator[StreamEvent]:
    retry = 1.0
    while True:
        resume_cursor = reducer.snapshot().cursor or cursor
        response = None
        try:
            if turn_id is not None:
                response = await client.raw.turns.stream_turn_without_preload_content(
                    turn_id,
                    cursor=None,
                    deltas=deltas,
                    last_event_id=resume_cursor,
                    _headers=dict(access_headers) if access_headers is not None else None,
                )
            else:
                assert conversation_id is not None
                response = await client.raw.conversations.stream_conversation_without_preload_content(
                    conversation_id, cursor=resume_cursor
                )
            if response.is_error:
                from .facade import NvokenError
                raise NvokenError("server", "nvoken stream request failed", status=response.status)
            async for event in parse_sse(response.aiter_lines()):
                if event.retry is not None:
                    retry = min(event.retry, 30.0)
                if event.data is None:
                    continue
                reducer.apply(event)
                yield event
        except asyncio.CancelledError:
            raise
        except (TimeoutException, TransportError):
            await asyncio.sleep(retry)
            continue
        finally:
            if response is not None:
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
                    type=event_type or "message", id=event_id, retry=retry,
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
            type=event_type or "message", id=event_id, retry=retry,
            data=json.loads("\n".join(data)) if data else None,
        )
