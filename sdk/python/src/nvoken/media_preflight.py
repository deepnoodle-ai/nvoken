from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Literal
from urllib.parse import urlparse

MAX_MEDIA_INPUT_BLOCKS = 8
MAX_IMAGE_INPUT_BYTES = 5 * 1024 * 1024
MAX_DOCUMENT_INPUT_BYTES = 16 * 1024 * 1024
MAX_MEDIA_INPUT_BYTES = 16 * 1024 * 1024
MAX_MEDIA_TITLE_CHARACTERS = 255
MEDIA_PREFLIGHT_CODE = "media_preflight_failed"

IMAGE_MEDIA_TYPES = ("image/gif", "image/jpeg", "image/png", "image/webp")
DOCUMENT_MEDIA_TYPES = ("application/pdf",)

_BASE64_ALPHABET = set(
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)


@dataclass(frozen=True)
class MediaIssue:
    code: str
    path: str
    message: str


@dataclass(frozen=True)
class ImageSource:
    media_type: Literal["image/gif", "image/jpeg", "image/png", "image/webp"] | None = None
    data: str | None = None
    """Standard padded base64 with no whitespace."""
    url: str | None = None
    """Public HTTPS URL fetched once when the Turn is accepted."""

    @classmethod
    def from_url(
        cls,
        url: str,
        media_type: Literal["image/gif", "image/jpeg", "image/png", "image/webp"] | None = None,
    ) -> ImageSource:
        return cls(media_type=media_type, url=url)


@dataclass(frozen=True)
class DocumentSource:
    media_type: Literal["application/pdf"] | None = None
    data: str | None = None
    """Standard padded base64 with no whitespace."""
    url: str | None = None
    """Public HTTPS URL fetched once when the Turn is accepted."""

    @classmethod
    def from_url(
        cls,
        url: str,
        media_type: Literal["application/pdf"] | None = None,
    ) -> DocumentSource:
        return cls(media_type=media_type, url=url)


@dataclass(frozen=True)
class TextBlock:
    text: str
    type: Literal["text"] = "text"


@dataclass(frozen=True)
class ImageBlock:
    source: ImageSource
    type: Literal["image"] = "image"


@dataclass(frozen=True)
class DocumentBlock:
    source: DocumentSource
    title: str | None = None
    """Filename for providers that require one; omission uses their default."""
    type: Literal["document"] = "document"


InputBlock = TextBlock | ImageBlock | DocumentBlock


def input_block_wire(block: InputBlock) -> dict[str, Any]:
    """Render one block in the exact canonical member shape."""
    if isinstance(block, TextBlock):
        return {"type": "text", "text": block.text}
    if isinstance(block, ImageBlock):
        source = {
            "media_type": block.source.media_type,
            "data": block.source.data,
            "url": block.source.url,
        }
        return {
            "type": "image",
            "source": {key: value for key, value in source.items() if value is not None},
        }
    source = {
        "media_type": block.source.media_type,
        "data": block.source.data,
        "url": block.source.url,
    }
    wire: dict[str, Any] = {
        "type": "document",
        "source": {key: value for key, value in source.items() if value is not None},
    }
    if block.title is not None:
        wire["title"] = block.title
    return wire


def media_input_issue(blocks: tuple[InputBlock, ...]) -> MediaIssue | None:
    """Reproduce the checks the Runtime can make without decoding bytes.

    Format sniffing, pixel bounds, and per-model modality support are
    Runtime-only and stay server-side.
    """
    if not blocks:
        return MediaIssue(
            code="invalid_media",
            path="input.content",
            message="input must contain at least one block",
        )
    media_blocks = 0
    media_bytes = 0
    for index, block in enumerate(blocks):
        path = f"input.content[{index}]"
        if isinstance(block, TextBlock):
            if not block.text.strip():
                return MediaIssue(
                    code="invalid_media",
                    path=f"{path}.text",
                    message="text must not be blank",
                )
            continue
        if not isinstance(block, (ImageBlock, DocumentBlock)):
            return MediaIssue(
                code="invalid_media",
                path=f"{path}.type",
                message="type must be text, image, or document",
            )
        image = isinstance(block, ImageBlock)
        allowed = IMAGE_MEDIA_TYPES if image else DOCUMENT_MEDIA_TYPES
        limit = MAX_IMAGE_INPUT_BYTES if image else MAX_DOCUMENT_INPUT_BYTES
        if isinstance(block, DocumentBlock) and block.title is not None:
            if not block.title.strip() or len(block.title) > MAX_MEDIA_TITLE_CHARACTERS:
                return MediaIssue(
                    code="invalid_media",
                    path=f"{path}.title",
                    message=(
                        f"title must be 1 to {MAX_MEDIA_TITLE_CHARACTERS} characters"
                    ),
                )
        has_data = block.source.data is not None
        has_url = block.source.url is not None
        if has_data == has_url:
            return MediaIssue(
                code="invalid_media",
                path=f"{path}.source",
                message="source requires exactly one of data or url",
            )
        if has_url:
            parsed = urlparse(block.source.url or "")
            if parsed.scheme != "https" or not parsed.hostname:
                return MediaIssue(
                    code="invalid_media",
                    path=f"{path}.source.url",
                    message="url must be an HTTPS URL",
                )
            if block.source.media_type is not None and block.source.media_type not in allowed:
                return MediaIssue(
                    code="unsupported_media_type",
                    path=f"{path}.source.media_type",
                    message="media_type must be one of " + ", ".join(allowed),
                )
            media_blocks += 1
            if media_blocks > MAX_MEDIA_INPUT_BLOCKS:
                return MediaIssue(
                    code="limit_exceeded",
                    path=path,
                    message=f"input carries at most {MAX_MEDIA_INPUT_BLOCKS} media blocks",
                )
            continue
        if block.source.media_type not in allowed:
            return MediaIssue(
                code="unsupported_media_type",
                path=f"{path}.source.media_type",
                message="media_type must be one of " + ", ".join(allowed),
            )
        decoded = _decoded_length(block.source.data or "")
        if decoded is None or decoded == 0:
            return MediaIssue(
                code="invalid_media",
                path=f"{path}.source.data",
                message="data must be standard padded base64 without whitespace",
            )
        if decoded > limit:
            return MediaIssue(
                code="limit_exceeded",
                path=f"{path}.source.data",
                message=f"data must decode to at most {limit} bytes",
            )
        media_blocks += 1
        media_bytes += decoded
        if media_blocks > MAX_MEDIA_INPUT_BLOCKS:
            return MediaIssue(
                code="limit_exceeded",
                path=path,
                message=(
                    f"input carries at most {MAX_MEDIA_INPUT_BLOCKS} media blocks"
                ),
            )
        if media_bytes > MAX_MEDIA_INPUT_BYTES:
            return MediaIssue(
                code="limit_exceeded",
                path=path,
                message=(
                    "input media must decode to at most "
                    f"{MAX_MEDIA_INPUT_BYTES} bytes"
                ),
            )
    return None


def _decoded_length(data: str) -> int | None:
    """Report the decoded size of standard padded base64, or None otherwise."""
    if not data or len(data) % 4 != 0:
        return None
    padding = 0
    for index, character in enumerate(data):
        if character == "=":
            if index < len(data) - 2:
                return None
            padding += 1
        elif character in _BASE64_ALPHABET:
            if padding:
                return None
        else:
            return None
    return len(data) // 4 * 3 - padding
