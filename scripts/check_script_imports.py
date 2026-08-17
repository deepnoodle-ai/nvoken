#!/usr/bin/env python3
"""Hold this repository's Python tooling to the standard library.

`make check` runs on whatever Python is already on the machine. CI installs no
Python packages, and neither does a contributor cloning this repository for the
first time, so a check script that imports a third-party module does not fail
with a clear message about a missing dependency: it fails as the check itself,
and the thing it was guarding goes unchecked.

That is not hypothetical. `check_facade_parity.py` shipped importing PyYAML,
passed on a machine that happened to have it, and broke CI on the machine that
did not. `check_go_frame_keys.py` reads the same contract with `re` for exactly
this reason and says so in its docstring. This makes the rule enforceable
instead of remembered.

Imports guarded by `try`/`except ImportError` are still refused. A check that
quietly does less when a module is absent is worse than one that fails, because
a green run stops meaning anything.
"""

from __future__ import annotations

import ast
import pathlib
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]

# Every Python file that `make check` runs or imports.
TREES = ("scripts", "sdk/scripts")

# Modules that live in this repository rather than on the path.
LOCAL = {"scripts", "check_facade_parity"}


def imported_modules(tree: ast.Module) -> set[str]:
    """Top-level module names this file imports, at any nesting depth."""
    modules: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Import):
            modules.update(alias.name.split(".")[0] for alias in node.names)
        elif isinstance(node, ast.ImportFrom):
            # A relative import resolves inside this repository.
            if node.level == 0 and node.module:
                modules.add(node.module.split(".")[0])
    return modules


def main() -> int:
    problems: list[str] = []
    checked = 0
    for tree_name in TREES:
        for path in sorted((ROOT / tree_name).rglob("*.py")):
            checked += 1
            try:
                parsed = ast.parse(path.read_text())
            except SyntaxError as error:
                problems.append(f"{path.relative_to(ROOT)}: {error}")
                continue
            for module in sorted(imported_modules(parsed)):
                if module in LOCAL or module in sys.stdlib_module_names:
                    continue
                problems.append(
                    f"{path.relative_to(ROOT)} imports {module}, which is not in "
                    f"the standard library"
                )

    if not checked:
        print("script imports: found no Python to check", file=sys.stderr)
        return 1

    if problems:
        print("Script dependency problems:", file=sys.stderr)
        for problem in problems:
            print(f"  {problem}", file=sys.stderr)
        print(
            "\nThese scripts run on a bare Python that installs nothing, so a\n"
            "third-party import fails as the check rather than as a missing\n"
            "dependency. Read what you need with the standard library.",
            file=sys.stderr,
        )
        return 1

    print(f"script imports: {checked} files use the standard library only")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
