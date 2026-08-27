#!/usr/bin/env python3
"""Reject pre-cut runtime vocabulary in current conformance fixtures."""

from __future__ import annotations

import json
import pathlib
import re
import sys


ROOT = pathlib.Path(__file__).resolve().parents[2]
FIXTURES = ROOT / "sdk" / "conformance" / "fixtures"
LEGACY = re.compile(
    r"(?i)(?:agent[ _-]?definitions?|agent[ _-]?instances?|invocations?|sessions?)"
)


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

    if errors:
        print("fixture vocabulary check failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print(f"fixture vocabulary: {len(fixtures)} current JSON fixtures checked")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
