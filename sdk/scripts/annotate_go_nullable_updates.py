#!/usr/bin/env python3
"""Add Go-only three-state types to nullable Conversation update fields."""

from __future__ import annotations

import pathlib
import sys


SCHEMA_START = "\n    UpdateConversationRequest:\n"
SCHEMA_END = "\n    ForkConversationRequest:\n"
FIELD_TYPES = {
    "retention": "RetentionPolicy",
    "compaction": "CompactionPolicy",
}


def annotate(text: str) -> str:
    if text.count(SCHEMA_START) != 1 or text.count(SCHEMA_END) != 1:
        raise ValueError("UpdateConversationRequest schema boundaries changed")
    start = text.index(SCHEMA_START)
    end = text.index(SCHEMA_END, start)
    block = text[start:end]

    for field, go_type in FIELD_TYPES.items():
        marker = f"        {field}:\n          oneOf:\n"
        if block.count(marker) != 1:
            raise ValueError(f"UpdateConversationRequest.{field} shape changed")
        replacement = (
            f"        {field}:\n"
            f"          x-go-type: nullable.Nullable[{go_type}]\n"
            "          x-go-type-import:\n"
            "            path: github.com/oapi-codegen/nullable\n"
            "          x-go-type-skip-optional-pointer: true\n"
            "          oneOf:\n"
        )
        block = block.replace(marker, replacement)

    return text[:start] + block + text[end:]


def main() -> int:
    if len(sys.argv) != 2:
        print(f"usage: {pathlib.Path(sys.argv[0]).name} OPENAPI_FILE", file=sys.stderr)
        return 2
    path = pathlib.Path(sys.argv[1])
    try:
        path.write_text(annotate(path.read_text()))
    except ValueError as error:
        print(f"annotate-go-nullable-updates: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
