#!/usr/bin/env python3
"""Prove the TypeScript package ships every module its entry points import.

`package.json` lists `dist` files explicitly, so adding a module to the SDK
without adding it to that list produces a tarball whose own type declarations
import a file that is not present. A fresh consumer then fails to typecheck,
which only the packed onboarding check catches today — and that check needs a
database, so it is not part of `make sdk-check`.

This walks the local import graph from the published entry points and requires
every reachable file to be covered by `files`. Modules that are deliberately not
published, such as tests and unreferenced examples, are never reached and are
therefore never required.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

PACKAGE = Path(__file__).resolve().parents[1] / "typescript"
DIST = PACKAGE / "dist"

# Matches the relative specifier in `import ... from "./x.js"`, `export ... from
# "../y.js"`, and bare `import "./z.js"` side-effect forms.
RELATIVE_SPECIFIER = re.compile(
    r"""(?:from|import)\s*\(?\s*["'](\.[^"']+)["']""",
)


def entry_points(manifest: dict) -> list[Path]:
    """Return the published entry points: main, types, exports, and bin."""
    candidates: set[str] = set()
    for key in ("main", "types"):
        if isinstance(manifest.get(key), str):
            candidates.add(manifest[key])
    for value in (manifest.get("exports") or {}).values():
        if isinstance(value, str):
            candidates.add(value)
        elif isinstance(value, dict):
            candidates.update(item for item in value.values() if isinstance(item, str))
    for value in (manifest.get("bin") or {}).values():
        if isinstance(value, str):
            candidates.add(value)
    resolved = []
    for candidate in sorted(candidates):
        path = (PACKAGE / candidate.lstrip("./")).resolve()
        if path.exists():
            resolved.append(path)
    return resolved


def resolve(source: Path, specifier: str) -> Path:
    """Resolve one relative specifier within the graph its source belongs to.

    TypeScript writes ".js" specifiers in declaration files too, so a declaration
    resolves to the paired declaration while compiled code resolves to code. That
    keeps each graph honest: a bin script needs no declarations, and the type
    entry point requires every declaration it imports.
    """
    target = (source.parent / specifier).resolve()
    if source.name.endswith(".d.ts") and target.name.endswith(".js"):
        declaration = target.with_suffix(".d.ts")
        if declaration.exists():
            return declaration
    return target


def reachable(entries: list[Path]) -> set[Path]:
    """Walk local imports transitively from every published entry point."""
    seen: set[Path] = set()
    queue = list(entries)
    while queue:
        current = queue.pop()
        if current in seen or not current.exists():
            continue
        seen.add(current)
        for specifier in RELATIVE_SPECIFIER.findall(current.read_text()):
            queue.append(resolve(current, specifier))
    return seen


def covered(path: Path, patterns: list[str]) -> bool:
    """Report whether `files` publishes this path, directly or by directory."""
    relative = path.relative_to(PACKAGE).as_posix()
    for pattern in patterns:
        entry = pattern.strip("./")
        if relative == entry or relative.startswith(entry + "/"):
            return True
    return False


def main() -> int:
    manifest = json.loads((PACKAGE / "package.json").read_text())
    patterns = [item for item in manifest.get("files", []) if not item.startswith("!")]
    if not DIST.exists():
        print(
            "sdk/typescript/dist is missing; run pnpm --filter @deepnoodle/nvoken build first",
            file=sys.stderr,
        )
        return 1
    entries = entry_points(manifest)
    if not entries:
        print("no published entry points resolved", file=sys.stderr)
        return 1
    missing = sorted(
        path.relative_to(PACKAGE).as_posix()
        for path in reachable(entries)
        if not covered(path, patterns)
    )
    if missing:
        print(
            "package.json files does not publish these reachable modules:",
            file=sys.stderr,
        )
        for path in missing:
            print(f"  {path}", file=sys.stderr)
        print(
            "add them to sdk/typescript/package.json files",
            file=sys.stderr,
        )
        return 1
    print(f"TypeScript package publishes {len(entries)} entry points and their imports")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
