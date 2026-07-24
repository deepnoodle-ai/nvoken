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
    previews: list[StreamPreview]
    resume_cursor: str | None


@dataclass(frozen=True)
class StreamPreview:
    invocation_id: str
    attempt: int
    iteration: int
    content_index: int
    output_text: str
    thinking: str


class Reducer:
    def __init__(self) -> None:
        self._messages: dict[int, SessionMessage] = {}
        self._changes: dict[tuple[str, int], InvocationChange] = {}
        self._previews: dict[tuple[str, int, int, int], StreamPreview] = {}
        self._latest_attempts: dict[str, int] = {}
        self._terminal_invocations: set[str] = set()
        self._cursor: str | None = None

    def apply(self, event: StreamEvent) -> None:
        if event.type in {"output_text.delta", "thinking.delta"}:
            data = event.data
            self._append_preview(
                invocation_id=data["invocation_id"],
                attempt=data["attempt"],
                iteration=data["iteration"],
                content_index=data["content_index"],
                output_text=data.get("text", ""),
                thinking=data.get("thinking", ""),
            )
            return
        if event.type == "stream.resync":
            invocation_id = event.data.get("invocation_id")
            if invocation_id is None:
                self._previews.clear()
                self._latest_attempts.clear()
            else:
                self._discard_previews(invocation_id)
            return
        if event.type != "transcript.update":
            return
        update = TranscriptUpdate.from_dict(event.data)
        assert update is not None
        for message in update.messages:
            self._messages[message.sequence] = message
            if message.role.value == "assistant":
                self._discard_previews(message.invocation_id)
        for change in update.invocation_changes:
            self._changes[(change.invocation_id, change.revision)] = change
            if change.status.value in {"completed", "failed", "cancelled"}:
                self._terminal_invocations.add(change.invocation_id)
                self._discard_previews(change.invocation_id)
        self._cursor = event.id or update.resume_cursor or self._cursor

    def snapshot(self) -> ReducedSnapshot:
        return ReducedSnapshot(
            messages=sorted(self._messages.values(), key=lambda message: message.sequence),
            invocation_changes=sorted(
                self._changes.values(),
                key=lambda change: (change.invocation_id, change.revision),
            ),
            previews=sorted(
                self._previews.values(),
                key=lambda preview: (
                    preview.invocation_id,
                    preview.attempt,
                    preview.iteration,
                    preview.content_index,
                ),
            ),
            resume_cursor=self._cursor,
        )

    def _append_preview(
        self,
        *,
        invocation_id: str,
        attempt: int,
        iteration: int,
        content_index: int,
        output_text: str,
        thinking: str,
    ) -> None:
        if invocation_id in self._terminal_invocations:
            return
        latest = self._latest_attempts.get(invocation_id)
        if latest is not None and attempt < latest:
            return
        if latest is None or attempt > latest:
            self._discard_previews(invocation_id)
            self._latest_attempts[invocation_id] = attempt
        key = (invocation_id, attempt, iteration, content_index)
        current = self._previews.get(key)
        self._previews[key] = StreamPreview(
            invocation_id=invocation_id,
            attempt=attempt,
            iteration=iteration,
            content_index=content_index,
            output_text=(current.output_text if current else "") + output_text,
            thinking=(current.thinking if current else "") + thinking,
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
