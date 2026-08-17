#!/usr/bin/env python3
"""Hold this repository's Python tooling and `requirements-dev.txt` together.

`make check` needs Go, Node, Java, a Rust toolchain, and a little Python. The
first four are declared somewhere a machine can read, so a missing one fails as
a missing toolchain. Python's were declared nowhere, so a missing one failed as
whichever check happened to import it: `check_facade_parity.py` imported PyYAML,
passed on a machine that had it, and read as a facade-parity failure on the
machine that did not.

Both directions are checked, because each catches a different mistake.

An import the file does not declare is the mistake that happened. A new script
reaches for a convenient package, it works locally, and nothing says the
environment has to grow.

A declaration that is not installed is the mistake a contributor meets. It says
so plainly here, with the command to fix it, rather than surfacing as an
ImportError from an unrelated check.
"""

from __future__ import annotations

import ast
import pathlib
import re
import sys
from importlib import metadata

ROOT = pathlib.Path(__file__).resolve().parents[1]
REQUIREMENTS = ROOT / "requirements-dev.txt"

# Every Python file `make check` runs or imports.
TREES = ("scripts", "sdk/scripts")

# Modules that live in this repository rather than on the path.
LOCAL = {"scripts", "check_facade_parity"}


def canonical(name: str) -> str:
    """PEP 503 normalization, so PyYAML and pyyaml are one name."""
    return re.sub(r"[-_.]+", "-", name).lower()


def declared() -> set[str]:
    names = set()
    for line in REQUIREMENTS.read_text().splitlines():
        line = line.split("#")[0].strip()
        if not line:
            continue
        names.add(canonical(re.split(r"[<>=!~\[;]", line)[0].strip()))
    if not names:
        raise SystemExit(f"{REQUIREMENTS.name}: declares nothing")
    return names


def imported(tree: ast.Module) -> set[str]:
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
    requirements = declared()
    # Import name to the distributions providing it: `yaml` comes from PyYAML,
    # and the import is what a script writes while the file declares the other.
    provided_by = metadata.packages_distributions()

    problems: list[str] = []
    missing: list[str] = []

    for name in sorted(requirements):
        try:
            metadata.distribution(name)
        except metadata.PackageNotFoundError:
            missing.append(name)

    checked = 0
    for tree in TREES:
        for path in sorted((ROOT / tree).rglob("*.py")):
            checked += 1
            relative = path.relative_to(ROOT)
            try:
                parsed = ast.parse(path.read_text())
            except SyntaxError as error:
                problems.append(f"{relative}: {error}")
                continue
            for module in sorted(imported(parsed)):
                if module in LOCAL or module in sys.stdlib_module_names:
                    continue
                distributions = provided_by.get(module)
                if distributions is None:
                    problems.append(
                        f"{relative} imports {module}, which is neither installed "
                        f"nor declared in {REQUIREMENTS.name}"
                    )
                    continue
                if not any(canonical(dist) in requirements for dist in distributions):
                    problems.append(
                        f"{relative} imports {module}, from {distributions[0]}, "
                        f"which {REQUIREMENTS.name} does not declare"
                    )

    if not checked:
        print("python deps: found no Python to check", file=sys.stderr)
        return 1

    if missing:
        print(
            f"{REQUIREMENTS.name} declares packages this environment does not have:",
            file=sys.stderr,
        )
        for name in missing:
            print(f"  {name}", file=sys.stderr)
        print(
            f"\nInstall them: python3 -m pip install -r {REQUIREMENTS.name}",
            file=sys.stderr,
        )
    if problems:
        print("Undeclared Python dependencies:", file=sys.stderr)
        for problem in problems:
            print(f"  {problem}", file=sys.stderr)
        print(
            f"\nAdd it to {REQUIREMENTS.name} so CI and the next contributor get "
            f"it too,\nor reach for something already there.",
            file=sys.stderr,
        )
    if missing or problems:
        return 1

    print(
        f"python deps: {checked} files, {len(requirements)} declared and installed"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
