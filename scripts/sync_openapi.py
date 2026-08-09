#!/usr/bin/env python3
"""Synchronize public OpenAPI snapshots from the authoritative cloud repo."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[1]
OPENAPI_DIR = ROOT / "openapi"
DEFAULT_CLOUD_REPO = ROOT.parent / "nvoken-cloud"
UPSTREAM_REPOSITORY = "https://github.com/deepnoodle-ai/nvoken-cloud"
CONTRACTS = ("runtime.yaml", "identity.yaml")


def git(repo: Path, *arguments: str) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo), *arguments],
        check=True,
        capture_output=True,
        text=True,
    )
    return result.stdout.strip()


def validate_source(repo: Path, *, allow_dirty: bool) -> tuple[str, bool]:
    missing = [name for name in CONTRACTS if not (repo / "openapi" / name).is_file()]
    if missing:
        raise ValueError(
            f"{repo} is not an nvoken-cloud checkout; missing: "
            + ", ".join(f"openapi/{name}" for name in missing)
        )
    commit = git(repo, "rev-parse", "HEAD")
    status = git(
        repo,
        "status",
        "--porcelain",
        "--",
        *(f"openapi/{name}" for name in CONTRACTS),
    )
    dirty = bool(status)
    if dirty and not allow_dirty:
        raise ValueError(
            "authoritative OpenAPI files have uncommitted changes; commit them "
            "or pass --allow-dirty for local iteration"
        )
    return commit, dirty


def provenance(commit: str, dirty: bool) -> dict[str, object]:
    return {
        "repository": UPSTREAM_REPOSITORY,
        "commit": commit,
        "dirty": dirty,
        "contracts": {
            name: f"openapi/{name}"
            for name in CONTRACTS
        },
    }


def synchronize(
    repo: Path,
    destination: Path = OPENAPI_DIR,
    *,
    allow_dirty: bool = False,
) -> None:
    commit, dirty = validate_source(repo, allow_dirty=allow_dirty)
    destination.mkdir(parents=True, exist_ok=True)
    for name in CONTRACTS:
        shutil.copyfile(repo / "openapi" / name, destination / name)
    (destination / "SOURCE.json").write_text(
        json.dumps(provenance(commit, dirty), indent=2) + "\n",
        encoding="utf-8",
    )
    suffix = " (dirty local source)" if dirty else ""
    print(f"synchronized OpenAPI from nvoken-cloud {commit[:12]}{suffix}")


def check(repo: Path, destination: Path = OPENAPI_DIR) -> list[str]:
    commit, dirty = validate_source(repo, allow_dirty=True)
    failures: list[str] = []
    for name in CONTRACTS:
        local = destination / name
        upstream = repo / "openapi" / name
        if not local.is_file() or local.read_bytes() != upstream.read_bytes():
            failures.append(f"openapi/{name} differs from {upstream}")
    source_path = destination / "SOURCE.json"
    try:
        actual = json.loads(source_path.read_text(encoding="utf-8"))
    except (FileNotFoundError, json.JSONDecodeError) as error:
        failures.append(f"openapi/SOURCE.json is missing or invalid: {error}")
    else:
        expected = provenance(commit, dirty)
        if actual != expected:
            failures.append(
                "openapi/SOURCE.json does not identify the checked-out "
                f"nvoken-cloud contract at {commit}"
            )
    return failures


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--repo",
        default=os.getenv("NVOKEN_CLOUD_REPO", ""),
        help="path to nvoken-cloud (default: ../nvoken-cloud)",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="compare without modifying the public snapshot",
    )
    parser.add_argument(
        "--allow-dirty",
        action="store_true",
        help="allow local uncommitted contract changes and record dirty provenance",
    )
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    repo = Path(args.repo).expanduser().resolve() if args.repo else DEFAULT_CLOUD_REPO
    try:
        if args.check:
            failures = check(repo)
            if failures:
                for failure in failures:
                    print(f"openapi sync: {failure}", file=sys.stderr)
                return 1
            print(f"OpenAPI snapshots match nvoken-cloud {git(repo, 'rev-parse', '--short=12', 'HEAD')}")
            return 0
        synchronize(repo, allow_dirty=args.allow_dirty)
        return 0
    except (OSError, subprocess.CalledProcessError, ValueError) as error:
        print(f"openapi sync: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
