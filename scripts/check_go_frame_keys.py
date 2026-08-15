#!/usr/bin/env python3
"""Hold the Go SDK's required-member table against the contract.

Go has no generated validator, so `sdk/go/frame_validation.go` keeps its own
copy of which members the contract requires on each stream payload. That copy is
what lets the Go reducer refuse a frame `encoding/json` would otherwise fill in
with zero values — a required bool arriving absent is a confident wrong answer.

The check lives here rather than in a Go test because the published `sdk/go`
module has no YAML dependency and should not gain one to read the contract.
This script reads the contract with the standard library for the same reason:
`scripts-check` runs on a bare Python and installs nothing.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CONTRACT = ROOT / "openapi" / "nvoken.yaml"
SOURCE = ROOT / "sdk" / "go" / "frame_validation.go"
TABLE = "requiredFrameKeys"


def go_table(source: str) -> dict[str, list[str]]:
    """Read the map literal. Deliberately strict: an unparseable table fails."""
    match = re.search(
        rf"var {TABLE} = map\[string\]\[\]string\{{(.*?)\n\}}", source, re.S
    )
    if match is None:
        raise SystemExit(f"{SOURCE}: could not find the {TABLE} literal")
    body = re.sub(r"//[^\n]*", "", match.group(1))
    entries: dict[str, list[str]] = {}
    for schema, members in re.findall(r'"([A-Za-z]+)":\s*\{(.*?)\}', body, re.S):
        entries[schema] = re.findall(r'"([a-z_]+)"', members)
    if not entries:
        raise SystemExit(f"{SOURCE}: {TABLE} parsed as empty")
    return entries


# A schema under `components.schemas` is a 4-space key; its own `required:` sits
# at 6. A nested object's `required:` is deeper, so the indent is what keeps this
# reading the schema's list rather than one belonging to a property.
SCHEMA = re.compile(r"^    ([A-Za-z][A-Za-z0-9]*):$")
REQUIRED = re.compile(r"^      required:\s*\[(.*)$")
ITEM = re.compile(r"^        - ([A-Za-z_][A-Za-z0-9_]*)\s*$")


def contract_required(text: str) -> dict[str, list[str]]:
    """Read `required` for every schema, with the standard library only.

    This is a reader for one line shape in a file this repository owns and
    lints, not a YAML parser. It refuses a `required:` it cannot read rather
    than reporting a schema as having none, because "no required members" and
    "I could not tell" must not look the same to the caller.
    """
    required: dict[str, list[str]] = {}
    schema: str | None = None
    in_schemas = False
    lines = text.splitlines()
    for index, line in enumerate(lines):
        # `required` is two different keywords in this document: a schema's list
        # of members and a parameter's boolean. Only the schemas block is ours,
        # so scope to it rather than matching 4-space keys anywhere.
        if re.match(r"^  [A-Za-z]", line):
            in_schemas = line.startswith("  schemas:")
            schema = None
            continue
        if not in_schemas:
            continue
        matched_schema = SCHEMA.match(line)
        if matched_schema:
            schema = matched_schema.group(1)
            continue
        if schema is None:
            continue
        if not line.startswith("      required:"):
            continue
        matched = REQUIRED.match(line)
        if matched is not None:
            # Flow style, possibly wrapped across lines: `required: [a, b, c]`.
            body = matched.group(1)
            cursor = index
            while "]" not in body:
                cursor += 1
                if cursor >= len(lines):
                    raise SystemExit(
                        f"{CONTRACT.name}: {schema} has an unterminated required list"
                    )
                body += " " + lines[cursor].strip()
            required[schema] = re.findall(r"[A-Za-z_][A-Za-z0-9_]*", body.split("]")[0])
            continue
        if line.strip() == "required:":
            # Block style: `required:` then one `- name` per line.
            members: list[str] = []
            cursor = index + 1
            while cursor < len(lines):
                item = ITEM.match(lines[cursor])
                if item is None:
                    break
                members.append(item.group(1))
                cursor += 1
            if not members:
                raise SystemExit(
                    f"{CONTRACT.name}: {schema} has an empty block required list"
                )
            required[schema] = members
            continue
        raise SystemExit(
            f"{CONTRACT.name}: {schema} has a required list this reader "
            f"cannot read: {line.strip()!r}"
        )
    if not required:
        raise SystemExit(f"{CONTRACT.name}: no schema required lists found")
    return required


def main() -> int:
    table = go_table(SOURCE.read_text())
    required = contract_required(CONTRACT.read_text())

    problems: list[str] = []
    for schema, members in sorted(table.items()):
        if schema not in required:
            problems.append(f"{schema} is not a schema in {CONTRACT.name}")
            continue
        if sorted(members) != sorted(required[schema]):
            problems.append(
                f"{schema} required members {sorted(members)} "
                f"but the contract says {sorted(required[schema])}"
            )

    if problems:
        for problem in problems:
            print(f"go frame keys: {problem}", file=sys.stderr)
        return 1

    print(f"Go required-member table matches the contract for {len(table)} schemas")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
