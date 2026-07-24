from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass
from typing import Any, AsyncIterator, Awaitable, Callable, TYPE_CHECKING

from nvoken_generated.models.invocation_change import InvocationChange
from nvoken_generated.models.session_message import SessionMessage
from nvoken_generated.models.transcript_update import TranscriptUpdate

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
    resume_cursor: str | None


class Reducer:
    def __init__(self) -> None:
        self._messages: dict[int, SessionMessage] = {}
        self._changes: dict[tuple[str, int], InvocationChange] = {}
        self._cursor: str | None = None

    def apply(self, event: StreamEvent) -> None:
        if event.type != "transcript.update":
            return
        update = TranscriptUpdate.from_dict(event.data)
        assert update is not None
        for message in update.messages:
            self._messages[message.sequence] = message
        for change in update.invocation_changes:
            self._changes[(change.invocation_id, change.revision)] = change
        self._cursor = event.id or update.resume_cursor or self._cursor

    def snapshot(self) -> ReducedSnapshot:
        return ReducedSnapshot(
            messages=sorted(self._messages.values(), key=lambda message: message.sequence),
            invocation_changes=sorted(
                self._changes.values(),
                key=lambda change: (change.invocation_id, change.revision),
            ),
            resume_cursor=self._cursor,
        )


async def stream_session(
    client: Client,
    session_id: str,
    reducer: Reducer,
    consume: Callable[[StreamEvent, ReducedSnapshot], Awaitable[None] | None],
) -> None:
    retry = 1.0
    while True:
        response = await client.stream_sessions.stream_session_transcript_without_preload_content(
            session_id,
            cursor=None,
            last_event_id=reducer.snapshot().resume_cursor,
        )
        try:
            if response.is_error:
                from .client import normalize_httpx_response
                raise await normalize_httpx_response(response)
            async for event in parse_sse(response.aiter_lines()):
                if event.retry is not None:
                    retry = min(event.retry, 30.0)
                reducer.apply(event)
                consumed = consume(event, reducer.snapshot())
                if consumed is not None:
                    await consumed
        except asyncio.CancelledError:
            raise
        except Exception as error:
            from .client import NvokenError
            if isinstance(error, NvokenError):
                raise
            await asyncio.sleep(retry)
            continue
        finally:
            await response.aclose()
        await asyncio.sleep(retry)


async def stream_invocation(
    client: Client,
    handle: InvocationHandle,
    consume: Callable[[StreamEvent], Awaitable[None] | None],
) -> None:
    retry = 1.0
    cursor: str | None = None
    while True:
        response = await client.stream_invocations.stream_invocation_without_preload_content(
            handle.invocation_id,
            cursor=None,
            last_event_id=cursor,
        )
        try:
            if response.is_error:
                from .client import normalize_httpx_response
                raise await normalize_httpx_response(response)
            async for event in parse_sse(response.aiter_lines()):
                if event.retry is not None:
                    retry = min(event.retry, 30.0)
                if event.id:
                    cursor = event.id
                consumed = consume(event)
                if consumed is not None:
                    await consumed
                if event.type == "invocation.result":
                    return
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
