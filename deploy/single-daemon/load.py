#!/usr/bin/env python3
"""Run and record a bounded single-daemon reference load."""

from __future__ import annotations

import concurrent.futures
import datetime as dt
import json
import math
import os
import pathlib
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections import Counter
from typing import Any


TERMINAL_STATUSES = {"completed", "failed", "cancelled"}
PROFILE_DIR = pathlib.Path(__file__).resolve().parent


def required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise ValueError(f"set {name}")
    return value


def bounded_integer(name: str, default: int, minimum: int, maximum: int) -> int:
    raw = os.environ.get(name, str(default))
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be a whole number") from exc
    if value < minimum or value > maximum:
        raise ValueError(f"{name} must be between {minimum} and {maximum}")
    return value


class RuntimeClient:
    def __init__(self, base_url: str, api_key: str, timeout: float) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    def request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
    ) -> tuple[dict[str, Any], float]:
        data = None if body is None else json.dumps(body, separators=(",", ":")).encode()
        request = urllib.request.Request(
            self.base_url + path,
            data=data,
            method=method,
            headers={
                "Authorization": f"Bearer {self.api_key}",
                "Content-Type": "application/json",
            },
        )
        started = time.monotonic()
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                payload = json.load(response)
                if method == "POST" and response.status != 202:
                    raise RuntimeError(f"{method} {path} returned HTTP {response.status}")
                if method == "GET" and response.status != 200:
                    raise RuntimeError(f"{method} {path} returned HTTP {response.status}")
                return payload, time.monotonic() - started
        except urllib.error.HTTPError as exc:
            safe_body = exc.read(4096).decode(errors="replace")
            raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {safe_body}") from exc

    def stream(self, session_id: str, deadline: float) -> dict[str, Any]:
        path = f"/v1/sessions/{urllib.parse.quote(session_id)}/transcript/stream"
        request = urllib.request.Request(
            self.base_url + path,
            headers={
                "Accept": "text/event-stream",
                "Authorization": f"Bearer {self.api_key}",
            },
        )
        events: Counter[str] = Counter()
        durable_ids: set[str] = set()
        current_event = "message"
        current_id = ""
        with urllib.request.urlopen(request, timeout=max(1.0, deadline - time.monotonic())) as response:
            while time.monotonic() < deadline:
                raw_line = response.readline()
                if not raw_line:
                    break
                line = raw_line.decode(errors="replace").rstrip("\r\n")
                if line.startswith("event: "):
                    current_event = line[7:]
                elif line.startswith("id: "):
                    current_id = line[4:]
                elif line == "":
                    events[current_event] += 1
                    if current_id:
                        durable_ids.add(current_id)
                    if current_event == "stream.end":
                        break
                    current_event = "message"
                    current_id = ""
        return {
            "events": dict(sorted(events.items())),
            "durable_cursor_count": len(durable_ids),
            "ended": events["stream.end"] > 0,
        }


def percentile(values: list[float], fraction: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    position = max(0, math.ceil(len(ordered) * fraction) - 1)
    return round(ordered[position] * 1000, 2)


def resident_memory_bytes(pid: int) -> int | None:
    status = pathlib.Path(f"/proc/{pid}/status")
    if status.exists():
        for line in status.read_text().splitlines():
            if line.startswith("VmRSS:"):
                return int(line.split()[1]) * 1024
    try:
        output = subprocess.check_output(
            ["ps", "-o", "rss=", "-p", str(pid)],
            text=True,
            stderr=subprocess.DEVNULL,
            timeout=5,
        ).strip()
        return int(output) * 1024 if output else None
    except (OSError, subprocess.SubprocessError, ValueError):
        return None


def database_metrics() -> dict[str, float | int]:
    # PGSERVICE and the operator's .pgpass keep database credentials out of the
    # command line and the evidence record.
    query = """
SELECT
  (SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()),
  count(*) FILTER (WHERE status = 'queued'),
  COALESCE(EXTRACT(EPOCH FROM (
    clock_timestamp() - min(created_at) FILTER (WHERE status = 'queued')
  )), 0)
FROM invocations;
"""
    output = subprocess.check_output(
        ["psql", "--no-psqlrc", "--tuples-only", "--no-align", "--field-separator=|", "--command", query],
        text=True,
        stderr=subprocess.DEVNULL,
        timeout=10,
    ).strip()
    connections, queued, age = output.split("|")
    return {
        "connections": int(connections),
        "queued": int(queued),
        "oldest_queue_age_seconds": round(float(age), 3),
    }


def main() -> int:
    try:
        base_url = os.environ.get("NVOKEN_BASE_URL", "http://127.0.0.1:8080")
        api_key = required("NVOKEN_API_KEY")
        provider = required("NVOKEN_LOAD_PROVIDER")
        model = required("NVOKEN_LOAD_MODEL")
        revision = required("NVOKEN_TESTED_REVISION")
        machine = required("NVOKEN_LOAD_MACHINE")
        database = required("NVOKEN_LOAD_DATABASE")
        if provider not in {"anthropic", "openai"}:
            raise ValueError("NVOKEN_LOAD_PROVIDER must be anthropic or openai")
        if not os.environ.get("PGSERVICE", "").strip():
            raise ValueError("set PGSERVICE to a credential-safe libpq service for the nvoken database")
        requests = bounded_integer("NVOKEN_LOAD_REQUESTS", 12, 1, 100)
        concurrency = bounded_integer("NVOKEN_LOAD_CONCURRENCY", 4, 1, 32)
        engine_concurrency = bounded_integer("NVOKEN_ENGINE_CONCURRENCY", 8, 1, 10_000)
        database_max_connections = bounded_integer("NVOKEN_DATABASE_MAX_CONNS", 20, 2, 100_000)
        timeout_seconds = bounded_integer("NVOKEN_LOAD_TIMEOUT_SECONDS", 600, 10, 3600)
        daemon_pid = bounded_integer("NVOKEN_DAEMON_PID", 0, 1, 2**31 - 1)
    except ValueError as exc:
        print(exc, file=sys.stderr)
        return 2

    if not shutil_which("psql"):
        print("required command not found: psql", file=sys.stderr)
        return 2

    output_path = pathlib.Path(
        os.environ.get("NVOKEN_LOAD_OUTPUT", str(PROFILE_DIR / "load-evidence.json"))
    )
    client = RuntimeClient(base_url, api_key, min(60, timeout_seconds))
    started_at = dt.datetime.now(dt.timezone.utc)
    started = time.monotonic()
    deadline = started + timeout_seconds
    run_id = uuid.uuid4().hex[:12]
    admission_latencies: list[float] = []
    read_latencies: list[float] = []
    acknowledgements: list[dict[str, Any]] = []
    samples: list[dict[str, Any]] = []
    stream_result: dict[str, Any] | None = None
    stream_error: str | None = None

    # Health is deliberately dependency-free; the first database sample proves
    # the separately configured observation path.
    health_request = urllib.request.Request(base_url.rstrip("/") + "/health")
    with urllib.request.urlopen(health_request, timeout=10) as response:
        if response.status != 200:
            raise RuntimeError(f"health returned HTTP {response.status}")
    samples.append({
        "elapsed_seconds": 0,
        "memory_bytes": resident_memory_bytes(daemon_pid),
        "database": database_metrics(),
    })

    def admit(index: int) -> tuple[dict[str, Any], float]:
        key = f"single-daemon-load:{run_id}:{index}"
        return client.request(
            "POST",
            "/v1/invocations",
            {
                "agent_ref": "single-daemon-load",
                "session_key": key,
                "idempotency_key": key,
                "input": {
                    "content": [{
                        "type": "text",
                        "text": "Reply with only the word ready.",
                    }]
                },
                "spec": {
                    "instructions": "Reply with only the word ready.",
                    "model": {"provider": provider, "name": model},
                },
            },
        )

    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
        futures = [pool.submit(admit, index) for index in range(requests)]
        for future in concurrent.futures.as_completed(futures):
            acknowledgement, latency = future.result()
            acknowledgements.append(acknowledgement)
            admission_latencies.append(latency)
    admitted_at = time.monotonic()

    stream_thread_result: list[dict[str, Any]] = []
    stream_thread_error: list[str] = []

    def collect_stream() -> None:
        try:
            stream_thread_result.append(client.stream(acknowledgements[0]["session_id"], deadline))
        except Exception as exc:  # The error string contains no request headers.
            stream_thread_error.append(str(exc))

    stream_thread = threading.Thread(target=collect_stream, name="nvoken-load-stream", daemon=True)
    stream_thread.start()

    latest: dict[str, dict[str, Any]] = {}
    next_sample = 0.0
    while time.monotonic() < deadline:
        with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
            reads = {
                pool.submit(client.request, "GET", f"/v1/invocations/{ack['invocation_id']}"): ack[
                    "invocation_id"
                ]
                for ack in acknowledgements
            }
            for future in concurrent.futures.as_completed(reads):
                invocation, latency = future.result()
                latest[reads[future]] = invocation
                read_latencies.append(latency)
        now = time.monotonic()
        if now >= next_sample:
            samples.append({
                "elapsed_seconds": round(now - started, 3),
                "memory_bytes": resident_memory_bytes(daemon_pid),
                "database": database_metrics(),
            })
            next_sample = now + 1
        if len(latest) == len(acknowledgements) and all(
            item.get("status") in TERMINAL_STATUSES for item in latest.values()
        ):
            break
        time.sleep(0.25)

    # Final reads account for every acknowledgement even when the bounded run
    # ends with queued or active work.
    queryable = True
    for acknowledgement in acknowledgements:
        invocation_id = acknowledgement["invocation_id"]
        try:
            invocation, latency = client.request("GET", f"/v1/invocations/{invocation_id}")
            latest[invocation_id] = invocation
            read_latencies.append(latency)
        except RuntimeError:
            queryable = False
    stream_thread.join(timeout=max(0.0, deadline - time.monotonic()) + 2)
    if stream_thread_result:
        stream_result = stream_thread_result[0]
    if stream_thread_error:
        stream_error = stream_thread_error[0]
    finished_at = dt.datetime.now(dt.timezone.utc)
    status_counts = Counter(item.get("status", "unknown") for item in latest.values())
    connection_samples = [sample["database"]["connections"] for sample in samples]
    queue_age_samples = [sample["database"]["oldest_queue_age_seconds"] for sample in samples]
    memory_samples = [sample["memory_bytes"] for sample in samples if sample["memory_bytes"] is not None]
    admission_seconds = admitted_at - started
    elapsed_seconds = time.monotonic() - started
    report = {
        "profile": "single_daemon",
        "tested_revision": revision,
        "started_at": started_at.isoformat(),
        "finished_at": finished_at.isoformat(),
        "environment": {
            "machine": machine,
            "database": database,
            "provider": provider,
            "model": model,
            "engine_concurrency": engine_concurrency,
            "database_max_connections": database_max_connections,
        },
        "workload": {
            "requested_invocations": requests,
            "admission_concurrency": concurrency,
            "timeout_seconds": timeout_seconds,
        },
        "observed": {
            "admission_throughput_per_second": round(requests / admission_seconds, 3),
            "admission_latency_ms": {
                "p50": percentile(admission_latencies, 0.50),
                "p95": percentile(admission_latencies, 0.95),
                "max": percentile(admission_latencies, 1.0),
            },
            "read_latency_ms": {
                "p50": percentile(read_latencies, 0.50),
                "p95": percentile(read_latencies, 0.95),
                "max": percentile(read_latencies, 1.0),
            },
            "elapsed_seconds": round(elapsed_seconds, 3),
            "status_counts": dict(sorted(status_counts.items())),
            "all_acknowledged_queryable": queryable and len(latest) == requests,
            "memory_bytes_max": max(memory_samples) if memory_samples else None,
            "database_connections_max": max(connection_samples),
            "oldest_queue_age_seconds_max": max(queue_age_samples),
            "stream": stream_result,
            "stream_error": stream_error,
        },
        "samples": samples,
    }
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(json.dumps(report, indent=2, sort_keys=True) + "\n")
    os.chmod(output_path, 0o600)
    print(f"wrote single_daemon load evidence: {output_path}")
    print(json.dumps(report["observed"], indent=2, sort_keys=True))

    if not report["observed"]["all_acknowledged_queryable"]:
        print("one or more acknowledged Invocations were not durably queryable", file=sys.stderr)
        return 1
    if stream_error or not stream_result or not stream_result.get("ended"):
        print("the representative transcript stream did not reconcile to terminal state", file=sys.stderr)
        return 1
    return 0


def shutil_which(command: str) -> str | None:
    for directory in os.environ.get("PATH", "").split(os.pathsep):
        candidate = pathlib.Path(directory) / command
        if candidate.is_file() and os.access(candidate, os.X_OK):
            return str(candidate)
    return None


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, subprocess.SubprocessError) as exc:
        print(f"single_daemon load failed: {exc}", file=sys.stderr)
        raise SystemExit(1) from exc
