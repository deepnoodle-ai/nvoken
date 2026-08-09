#!/usr/bin/env python3
"""Verify that the public SDK packages and generated metadata stay aligned."""

from __future__ import annotations

import json
from pathlib import Path
import re
import sys
import tomllib


ROOT = Path(__file__).resolve().parent.parent
GENERATED_VERSION_PATTERN = re.compile(r'^__version__ = "([^"]+)"$', re.MULTILINE)
GO_VERSION_PATTERN = re.compile(r'^const Version = "([^"]+)"$', re.MULTILINE)
TYPESCRIPT_VERSION_PATTERN = re.compile(
    r'^export const VERSION = "([^"]+)";$', re.MULTILINE
)


def source_version(path: Path, pattern: re.Pattern[str], name: str) -> str:
    match = pattern.search(path.read_text(encoding="utf-8"))
    if match is None:
        raise ValueError(f"{name} has no release version")
    return match.group(1)


def package_versions(root: Path) -> dict[str, str]:
    typescript = json.loads(
        (root / "sdk/typescript/package.json").read_text(encoding="utf-8")
    )["version"]
    python = tomllib.loads(
        (root / "sdk/python/pyproject.toml").read_text(encoding="utf-8")
    )["project"]["version"]
    rust = tomllib.loads(
        (root / "sdk/rust/Cargo.toml").read_text(encoding="utf-8")
    )["package"]["version"]
    return {
        "Go": source_version(
            root / "sdk/go/version.go", GO_VERSION_PATTERN, "Go SDK"
        ),
        "TypeScript": str(typescript),
        "TypeScript source": source_version(
            root / "sdk/typescript/src/version.ts",
            TYPESCRIPT_VERSION_PATTERN,
            "TypeScript SDK source",
        ),
        "Python": str(python),
        "Rust": str(rust),
        "generated Python": source_version(
            root / "sdk/python/src/nvoken_generated/__init__.py",
            GENERATED_VERSION_PATTERN,
            "generated Python package",
        ),
    }


def aligned_version(versions: dict[str, str]) -> str:
    unique = set(versions.values())
    if len(unique) != 1:
        details = ", ".join(f"{name}={version}" for name, version in versions.items())
        raise ValueError(f"SDK versions are not aligned: {details}")
    return next(iter(unique))


def main() -> int:
    try:
        version = aligned_version(package_versions(ROOT))
    except (KeyError, OSError, ValueError, tomllib.TOMLDecodeError) as error:
        print(error, file=sys.stderr)
        return 1
    print(f"SDK versions aligned at {version}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
