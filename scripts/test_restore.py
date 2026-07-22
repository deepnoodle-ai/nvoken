#!/usr/bin/env python3
"""Run the disposable Postgres logical backup and restore drill."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import random
import shutil
import subprocess
import sys
import time


REPO_ROOT = Path(__file__).resolve().parent.parent


def run(
    args: list[str],
    *,
    check: bool = True,
    capture_output: bool = False,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        cwd=REPO_ROOT,
        check=check,
        capture_output=capture_output,
        env=env,
        text=True,
    )


def require_command(name: str) -> None:
    if shutil.which(name) is None:
        raise RuntimeError(f"{name} is required for the logical backup and restore drill")


def docker_available() -> None:
    require_command("docker")
    result = run(
        ["docker", "info"],
        check=False,
        capture_output=True,
    )
    if result.returncode != 0:
        raise RuntimeError("Docker is installed but its daemon is unavailable")


def wait_for_postgres(container: str) -> None:
    for _ in range(60):
        ready = run(
            [
                "docker",
                "exec",
                container,
                "pg_isready",
                "--username",
                "nvoken",
                "--dbname",
                "nvoken_test",
            ],
            check=False,
            capture_output=True,
        )
        if ready.returncode == 0:
            return
        running = run(
            ["docker", "inspect", "--format", "{{.State.Running}}", container],
            check=False,
            capture_output=True,
        )
        if running.returncode != 0 or running.stdout.strip() != "true":
            logs = run(
                ["docker", "logs", container],
                check=False,
                capture_output=True,
            )
            raise RuntimeError(
                "Disposable Postgres exited before becoming ready:\n" + logs.stdout + logs.stderr
            )
        time.sleep(1)
    raise RuntimeError("Disposable Postgres did not become ready within 60 seconds")


def container_database_url(container: str) -> str:
    binding = run(
        ["docker", "port", container, "5432/tcp"],
        capture_output=True,
    ).stdout.strip()
    _, separator, port = binding.rpartition(":")
    if not separator or not port.isdigit():
        raise RuntimeError("Could not determine the disposable Postgres host port")
    return f"postgres://nvoken:nvoken-test@127.0.0.1:{port}/nvoken_test?sslmode=disable"


def run_tests(database_url: str) -> None:
    test_env = os.environ.copy()
    test_env["NVOKEN_TEST_DATABASE_URL"] = database_url
    test_env["NVOKEN_RUN_LOGICAL_RESTORE_DRILL"] = "1"
    run(["go", "test", "./...", "-count=1"], env=test_env)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="parse the runner without starting dependencies",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if args.check:
        return 0

    for command in ("go", "pg_dump", "pg_restore"):
        require_command(command)

    configured_url = os.environ.get("NVOKEN_TEST_DATABASE_URL")
    if configured_url:
        print("Using NVOKEN_TEST_DATABASE_URL for the restore drill.", flush=True)
        run_tests(configured_url)
        return 0

    docker_available()
    image = os.environ.get("NVOKEN_TEST_POSTGRES_IMAGE", "postgres:17")
    container = f"nvoken-restore-postgres-{os.getpid()}-{random.randrange(1_000_000)}"
    print(f"Starting disposable {image} for the restore drill.", flush=True)
    run(
        [
            "docker",
            "run",
            "--detach",
            "--rm",
            "--name",
            container,
            "--env",
            "POSTGRES_USER=nvoken",
            "--env",
            "POSTGRES_PASSWORD=nvoken-test",
            "--env",
            "POSTGRES_DB=nvoken_test",
            "--publish",
            "127.0.0.1::5432",
            image,
        ],
        capture_output=True,
    )
    try:
        wait_for_postgres(container)
        run_tests(container_database_url(container))
    finally:
        run(
            ["docker", "rm", "--force", container],
            check=False,
            capture_output=True,
        )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        raise SystemExit(1) from error
