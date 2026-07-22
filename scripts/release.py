#!/usr/bin/env python3
"""Build checksummed nvoken and nvokend release archives."""

from __future__ import annotations

import gzip
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
from typing import NoReturn
import zipfile


PLATFORMS = (
    ("darwin", "arm64"),
    ("darwin", "amd64"),
    ("linux", "arm64"),
    ("linux", "amd64"),
    ("windows", "amd64"),
)
VERSION_PATTERN = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)"
    r"(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$"
)


def fail(message: str) -> NoReturn:
    print(message, file=sys.stderr)
    raise SystemExit(1)


def validate_version(version: str) -> str:
    if not VERSION_PATTERN.fullmatch(version):
        fail(f"invalid release version {version!r}; expected X.Y.Z or X.Y.Z-prerelease")
    return version


def archive_name(version: str, goos: str, goarch: str) -> str:
    extension = "zip" if goos == "windows" else "tar.gz"
    return f"nvoken_{version}_{goos}_{goarch}.{extension}"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def run(command: list[str], *, cwd: Path, environment: dict[str, str]) -> None:
    subprocess.run(command, cwd=cwd, env=environment, check=True)


def write_archive(archive_path: Path, files: list[tuple[Path, str]]) -> None:
    if archive_path.suffix == ".zip":
        with zipfile.ZipFile(archive_path, "w", compression=zipfile.ZIP_DEFLATED) as archive:
            for source, name in files:
                info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
                info.compress_type = zipfile.ZIP_DEFLATED
                info.create_system = 3
                info.external_attr = (source.stat().st_mode & 0o777) << 16
                archive.writestr(info, source.read_bytes())
        return

    with archive_path.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w", format=tarfile.PAX_FORMAT) as archive:
                for source, name in files:
                    info = archive.gettarinfo(str(source), arcname=name)
                    info.uid = 0
                    info.gid = 0
                    info.uname = ""
                    info.gname = ""
                    info.mtime = 0
                    with source.open("rb") as content:
                        archive.addfile(info, content)


def build_platform(
    project: Path,
    staging: Path,
    dist: Path,
    version: str,
    goos: str,
    goarch: str,
) -> Path:
    platform = f"{goos}-{goarch}"
    platform_dir = staging / platform
    platform_dir.mkdir(parents=True)
    suffix = ".exe" if goos == "windows" else ""
    client = platform_dir / f"nvoken{suffix}"
    daemon = platform_dir / f"nvokend{suffix}"
    environment = os.environ.copy()
    environment.update({"CGO_ENABLED": "0", "GOOS": goos, "GOARCH": goarch})

    print(f">> Building {platform}")
    run(
        [
            "go",
            "build",
            "-buildvcs=false",
            "-trimpath",
            "-ldflags",
            f"-s -w -X main.version={version}",
            "-o",
            str(client),
            "./cmd/nvoken",
        ],
        cwd=project,
        environment=environment,
    )
    run(
        [
            "go",
            "build",
            "-buildvcs=false",
            "-trimpath",
            "-ldflags",
            f"-s -w -X main.buildVersion={version}",
            "-o",
            str(daemon),
            "./cmd/nvokend",
        ],
        cwd=project,
        environment=environment,
    )

    output = dist / archive_name(version, goos, goarch)
    write_archive(
        output,
        [
            (client, client.name),
            (daemon, daemon.name),
            (project / "LICENSE", "LICENSE"),
        ],
    )
    return output


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        fail("usage: scripts/release.py <version>\nexample: scripts/release.py 0.1.1")
    version = validate_version(argv[1])
    project = Path(__file__).resolve().parent.parent
    dist = project / "dist"
    if dist.exists():
        shutil.rmtree(dist)
    dist.mkdir()

    print(f"=== Building nvoken v{version} ===")
    assets: list[Path] = []
    with tempfile.TemporaryDirectory(prefix="nvoken-release-") as temporary:
        staging = Path(temporary)
        for goos, goarch in PLATFORMS:
            assets.append(build_platform(project, staging, dist, version, goos, goarch))

    checksums = dist / "checksums.txt"
    checksums.write_text(
        "".join(f"{sha256_file(asset)}  {asset.name}\n" for asset in assets),
        encoding="utf-8",
    )
    print(checksums.read_text(encoding="utf-8"), end="")
    print(f"Artifacts written to {dist}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
