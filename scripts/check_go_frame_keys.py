#!/usr/bin/env python3
"""Hold the Go SDK's required-member table against the contract.

Go has no generated validator, so `sdk/go/frame_validation.go` keeps its own
copy of which members the contract requires on each stream payload. That copy is
what lets the Go reducer refuse a frame `encoding/json` would otherwise fill in
with zero values — a required bool arriving absent is a confident wrong answer.

The check lives here rather than in a Go test because the published `sdk/go`
module has no YAML dependency and should not gain one to read the contract.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

import yaml

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


def contract_required() -> dict[str, list[str]]:
    document = yaml.safe_load(CONTRACT.read_text())
    return {
        name: schema.get("required", [])
        for name, schema in document["components"]["schemas"].items()
    }


def main() -> int:
    table = go_table(SOURCE.read_text())
    required = contract_required()

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
