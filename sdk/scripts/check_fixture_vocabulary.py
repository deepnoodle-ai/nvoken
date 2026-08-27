#!/usr/bin/env python3
"""Reject pre-cut runtime vocabulary and retired identifier shapes.

Two rules with two scopes, because they went stale in different places.

**Legacy nouns** are checked in the conformance fixtures, which are the wire
shapes four SDKs verify against and the closest thing this repository has to a
worked example of the contract.

**Retired identifier shapes** are checked across every tracked source and
fixture. Identifiers stopped carrying a type prefix in 0.32.0, but a
`turn_01kc…` in a test still passes, because an ID is an opaque string now and
the server accepts whatever it is sent. Nothing fails; the repository just goes
on teaching a format it no longer uses. That is how 66 of them survived the
cut in 34 files.

An ID assembled by interpolation — `` `msg_0…000${sequence}` `` — reads as
opaque to a scan over source text, so each interpolation is collapsed to a
single body character before matching. One of those was the last survivor here.
"""

from __future__ import annotations

import json
import pathlib
import re
import subprocess
import sys


ROOT = pathlib.Path(__file__).resolve().parents[2]
FIXTURES = ROOT / "sdk" / "conformance" / "fixtures"
LEGACY = re.compile(
    r"(?i)(?:agent[ _-]?definitions?|agent[ _-]?instances?|invocations?|sessions?)"
)
RETIRED_ID = re.compile(r"\b[a-z]{2,6}_[0-7][0-9abcdefghjkmnpqrstvwxyz]{25}\b")
INTERPOLATION = re.compile(r"\$\{[^{}]*\}")
SCANNED_SUFFIXES = frozenset(
    {".go", ".json", ".mjs", ".py", ".rs", ".sh", ".ts", ".tsx", ".yaml", ".yml"}
)
# The release history exists to say what the format was.
EXEMPT = frozenset({"CHANGELOG.md"})


def tracked_sources() -> list[pathlib.Path]:
    listing = subprocess.run(
        ["git", "ls-files"], cwd=ROOT, capture_output=True, text=True, check=True
    )
    return [
        ROOT / name
        for name in listing.stdout.split()
        if name not in EXEMPT and pathlib.PurePath(name).suffix in SCANNED_SUFFIXES
    ]


def main() -> int:
    errors: list[str] = []
    fixtures = sorted(FIXTURES.glob("*.json"))
    if not fixtures:
        errors.append("no conformance fixtures found")

    for path in fixtures:
        try:
            json.loads(path.read_text())
        except (OSError, json.JSONDecodeError) as error:
            errors.append(f"{path.relative_to(ROOT)}: invalid JSON: {error}")
            continue

        for line_number, line in enumerate(path.read_text().splitlines(), 1):
            match = LEGACY.search(line)
            if match:
                errors.append(
                    f"{path.relative_to(ROOT)}:{line_number}: "
                    f"legacy public vocabulary {match.group(0)!r}"
                )

    sources = tracked_sources()
    for path in sources:
        try:
            text = path.read_text()
        except (OSError, UnicodeDecodeError):
            continue
        for line_number, line in enumerate(text.splitlines(), 1):
            match = RETIRED_ID.search(INTERPOLATION.sub("0", line))
            if match:
                errors.append(
                    f"{path.relative_to(ROOT)}:{line_number}: "
                    f"retired prefixed identifier {match.group(0)!r}"
                )

    if errors:
        print("fixture vocabulary check failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print(
        f"fixture vocabulary: {len(fixtures)} current JSON fixtures checked; "
        f"{len(sources)} tracked sources carry no retired identifier"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
