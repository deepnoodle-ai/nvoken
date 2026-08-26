#!/usr/bin/env python3
"""Repair OpenAPI Generator's invalid array-of-union TurnInput guard."""

from __future__ import annotations

import pathlib
import sys


def fix(path: pathlib.Path) -> None:
    source = path.read_text()
    replacements = {
        "    instanceOfInputBlock,\n": "",
        """        if (json.every(item => typeof item === 'object')) {
            if (json.every(item => instanceOfInputBlock(item))) {
                return json.map(value => InputBlockFromJSONTyped(value, true));
            }
        }
        return json;
""": """        return json.map(value => InputBlockFromJSONTyped(value, true));
""",
        """        if (value.every(item => typeof item === 'object')) {
            if (value.every(item => instanceOfInputBlock(item))) {
                return value.map(value => InputBlockToJSON(value as InputBlock));
            }
        }
        return value;
""": """        return value.map(value => InputBlockToJSON(value as InputBlock));
""",
    }
    for old, new in replacements.items():
        count = source.count(old)
        if count != 1:
            raise RuntimeError(
                f"expected one TurnInput generator fragment in {path}, found {count}"
            )
        source = source.replace(old, new)
    path.write_text(source)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: fix_typescript_turn_input.py PATH")
    fix(pathlib.Path(sys.argv[1]))
