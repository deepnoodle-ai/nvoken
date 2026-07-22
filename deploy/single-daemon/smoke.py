#!/usr/bin/env python3
"""Exercise the nvoken single-daemon profile and record restart-safe state."""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import pathlib
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from typing import Any


TERMINAL_STATUSES = {"completed", "failed", "cancelled"}
PROFILE_DIR = pathlib.Path(__file__).resolve().parent


def required(name: str) -> str:
    value = os.environ.get(name, "").strip()
    if not value:
        raise ValueError(f"set {name}")
    return value


def positive_integer(name: str, default: int) -> int:
    raw = os.environ.get(name, str(default))
    try:
        value = int(raw)
    except ValueError as exc:
        raise ValueError(f"{name} must be a positive whole number") from exc
    if value <= 0:
        raise ValueError(f"{name} must be a positive whole number")
    return value


class RuntimeClient:
    def __init__(self, base_url: str, api_key: str, timeout_seconds: int) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout_seconds = timeout_seconds

    def health(self) -> None:
        request = urllib.request.Request(self.base_url + "/health")
        try:
            with urllib.request.urlopen(request, timeout=10) as response:
                if response.status != 200:
                    raise RuntimeError(f"GET /health returned HTTP {response.status}")
        except urllib.error.HTTPError as exc:
            raise RuntimeError(f"GET /health returned HTTP {exc.code}") from exc

    def get(self, path: str, query: dict[str, str | int] | None = None) -> dict[str, Any]:
        if query:
            path = path + "?" + urllib.parse.urlencode(query)
        return self._json_request("GET", path)

    def post(self, path: str, body: dict[str, Any]) -> dict[str, Any]:
        return self._json_request("POST", path, body)

    def wait_for_status(self, invocation_id: str, wanted: str) -> dict[str, Any]:
        deadline = time.monotonic() + self.timeout_seconds
        while time.monotonic() < deadline:
            invocation = self.get(f"/v1/invocations/{invocation_id}")
            status = invocation["status"]
            if status == wanted:
                return invocation
            if status in TERMINAL_STATUSES:
                raise RuntimeError(
                    f"Invocation {invocation_id} settled as {status}, wanted {wanted}: "
                    f"{json.dumps(invocation.get('error'))}"
                )
            time.sleep(1)
        raise RuntimeError(
            f"Invocation {invocation_id} did not reach {wanted} within "
            f"{self.timeout_seconds} seconds"
        )

    def _json_request(
        self,
        method: str,
        path: str,
        body: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
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
        try:
            with urllib.request.urlopen(request, timeout=self.timeout_seconds) as response:
                wanted_status = 202 if method == "POST" else 200
                if response.status != wanted_status:
                    raise RuntimeError(f"{method} {path} returned HTTP {response.status}")
                return json.load(response)
        except urllib.error.HTTPError as exc:
            safe_body = exc.read(4096).decode(errors="replace")
            raise RuntimeError(f"{method} {path} returned HTTP {exc.code}: {safe_body}") from exc


def model_selection() -> tuple[str, str]:
    provider = required("NVOKEN_SMOKE_PROVIDER")
    model = required("NVOKEN_SMOKE_MODEL")
    if provider not in {"anthropic", "openai"}:
        raise ValueError("NVOKEN_SMOKE_PROVIDER must be anthropic or openai")
    return provider, model


def invocation_request(
    *,
    run_key: str,
    agent_ref: str,
    input_text: str,
    instructions: str,
    provider: str,
    model: str,
    tools: list[dict[str, Any]] | None = None,
) -> dict[str, Any]:
    spec: dict[str, Any] = {
        "instructions": instructions,
        "model": {"provider": provider, "name": model},
    }
    if tools:
        spec["budgets"] = {"max_iterations": 3}
        spec["tools"] = tools
    return {
        "agent_ref": agent_ref,
        "session_key": run_key,
        "idempotency_key": run_key,
        "input": {"content": [{"type": "text", "text": input_text}]},
        "spec": spec,
    }


def write_state(path: pathlib.Path, state: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", dir=path.parent, delete=False) as handle:
        temporary = pathlib.Path(handle.name)
        json.dump(state, handle, indent=2, sort_keys=True)
        handle.write("\n")
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def run_smoke(
    client: RuntimeClient,
    state_path: pathlib.Path,
    tested_revision: str,
) -> None:
    provider, model = model_selection()
    client.health()
    run_key = f"single-daemon-smoke:{uuid.uuid4().hex}"
    acknowledgement = client.post(
        "/v1/invocations",
        invocation_request(
            run_key=run_key,
            agent_ref="single-daemon-smoke",
            input_text="Reply briefly to confirm this nvoken installation is working.",
            instructions="You are a concise deployment smoke-test agent.",
            provider=provider,
            model=model,
        ),
    )
    invocation_id = acknowledgement["invocation_id"]
    session_id = acknowledgement["session_id"]
    client.wait_for_status(invocation_id, "completed")

    second_read = client.get(f"/v1/invocations/{invocation_id}")
    if second_read.get("id") != invocation_id or second_read.get("status") != "completed":
        raise RuntimeError("second authoritative Invocation read did not match completion")
    transcript = client.get(f"/v1/sessions/{session_id}/transcript", {"limit": 100})
    if (
        transcript.get("has_more") is not False
        or len(transcript.get("messages", [])) < 2
        or len(transcript.get("invocation_changes", [])) < 1
    ):
        raise RuntimeError("terminal transcript did not contain the expected fixed-cut evidence")
    resume_cursor = transcript["resume_cursor"]
    resumed = client.get(
        f"/v1/sessions/{session_id}/transcript",
        {"cursor": resume_cursor, "limit": 100},
    )
    if (
        resumed.get("has_more") is not False
        or resumed.get("messages") != []
        or resumed.get("invocation_changes") != []
    ):
        raise RuntimeError("resume cursor replayed already acknowledged durable state")
    write_state(
        state_path,
        {
            "profile": "single_daemon",
            "tested_revision": tested_revision,
            "smoke_completed_at": dt.datetime.now(dt.timezone.utc).isoformat(),
            "invocation_id": invocation_id,
            "session_id": session_id,
            "resume_cursor": resume_cursor,
            "restart_verified_at": None,
        },
    )
    print("single_daemon smoke passed")
    print(f"invocation: {invocation_id}")
    print(f"state: {state_path}")
    print("Restart the daemon, then run: python3 deploy/single-daemon/smoke.py verify-restart")


def verify_restart(
    client: RuntimeClient,
    state_path: pathlib.Path,
    tested_revision: str,
) -> None:
    client.health()
    if not state_path.is_file():
        raise RuntimeError(f"smoke state not found: {state_path}")
    state = json.loads(state_path.read_text())
    if state.get("profile") != "single_daemon" or state.get("tested_revision") != tested_revision:
        raise RuntimeError("smoke state profile or tested revision does not match this run")
    invocation = client.get(f"/v1/invocations/{state['invocation_id']}")
    if invocation.get("status") != "completed":
        raise RuntimeError("completed Invocation was not durable across restart")
    resumed = client.get(
        f"/v1/sessions/{state['session_id']}/transcript",
        {"cursor": state["resume_cursor"], "limit": 100},
    )
    if (
        resumed.get("has_more") is not False
        or resumed.get("messages") != []
        or resumed.get("invocation_changes") != []
    ):
        raise RuntimeError("restart resume replayed already acknowledged durable state")
    state["restart_verified_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
    write_state(state_path, state)
    print("single_daemon restart readback passed")
    print(f"invocation: {state['invocation_id']}")


def admit_client_tool(
    client: RuntimeClient,
    state_path: pathlib.Path,
    tested_revision: str,
) -> None:
    provider, model = model_selection()
    client.health()
    run_key = f"single-daemon-client-tool-smoke:{uuid.uuid4().hex}"
    acknowledgement = client.post(
        "/v1/invocations",
        invocation_request(
            run_key=run_key,
            agent_ref="single-daemon-client-tool-smoke",
            input_text="Use get_smoke_value for key health, then report its value.",
            instructions="Call get_smoke_value exactly once before answering. Do not invent its result.",
            provider=provider,
            model=model,
            tools=[{
                "name": "get_smoke_value",
                "description": "Return a deterministic smoke-test value for one key.",
                "mode": "client",
                "input_schema": {
                    "type": "object",
                    "properties": {"key": {"type": "string"}},
                    "required": ["key"],
                    "additionalProperties": False,
                },
            }],
        ),
    )
    invocation_id = acknowledgement["invocation_id"]
    waiting = client.wait_for_status(invocation_id, "waiting")
    pending = waiting.get("pending_tool_calls", [])
    if len(pending) != 1:
        raise RuntimeError(f"expected one pending client ToolCall, got {len(pending)}")
    tool_call_id = pending[0]["id"]
    write_state(
        state_path,
        {
            "profile": "single_daemon",
            "exercise": "client_tool",
            "tested_revision": tested_revision,
            "admitted_at": dt.datetime.now(dt.timezone.utc).isoformat(),
            "invocation_id": invocation_id,
            "session_id": acknowledgement["session_id"],
            "tool_call_id": tool_call_id,
            "result_submitted_at": None,
        },
    )
    print("single_daemon client ToolCall is durably waiting")
    print(f"invocation: {invocation_id}")
    print(f"tool_call: {tool_call_id}")
    print(f"state: {state_path}")


def resume_client_tool(
    client: RuntimeClient,
    state_path: pathlib.Path,
    tested_revision: str,
) -> None:
    client.health()
    if not state_path.is_file():
        raise RuntimeError(f"client ToolCall state not found: {state_path}")
    state = json.loads(state_path.read_text())
    if (
        state.get("profile") != "single_daemon"
        or state.get("exercise") != "client_tool"
        or state.get("tested_revision") != tested_revision
    ):
        raise RuntimeError("client ToolCall state profile, exercise, or revision does not match")
    invocation_id = state["invocation_id"]
    tool_call_id = state["tool_call_id"]
    waiting = client.get(f"/v1/invocations/{invocation_id}")
    pending_ids = {item["id"] for item in waiting.get("pending_tool_calls", [])}
    if waiting.get("status") != "waiting" or tool_call_id not in pending_ids:
        raise RuntimeError("client ToolCall was not still waiting before result submission")
    result_body = {
        "results": [{
            "tool_call_id": tool_call_id,
            "content": {"key": "health", "value": "ok"},
        }]
    }
    accepted = client.post(f"/v1/invocations/{invocation_id}/tool-results", result_body)
    if accepted["results"][0]["deduplicated"] is not False:
        raise RuntimeError("first client ToolCall result was unexpectedly deduplicated")
    replayed = client.post(f"/v1/invocations/{invocation_id}/tool-results", result_body)
    if replayed["results"][0]["deduplicated"] is not True:
        raise RuntimeError("equal client ToolCall result replay did not deduplicate")
    client.wait_for_status(invocation_id, "completed")
    state["result_submitted_at"] = dt.datetime.now(dt.timezone.utc).isoformat()
    write_state(state_path, state)
    print("single_daemon client ToolCall smoke passed")
    print(f"invocation: {invocation_id}")
    print(f"tool_call: {tool_call_id}")


def run_client_tool(
    client: RuntimeClient,
    state_path: pathlib.Path,
    tested_revision: str,
) -> None:
    admit_client_tool(client, state_path, tested_revision)
    resume_client_tool(client, state_path, tested_revision)


def run_callback(client: RuntimeClient) -> None:
    provider, model = model_selection()
    callback_url = required("NVOKEN_SMOKE_CALLBACK_URL")
    client.health()
    run_key = f"single-daemon-callback-smoke:{uuid.uuid4().hex}"
    acknowledgement = client.post(
        "/v1/invocations",
        invocation_request(
            run_key=run_key,
            agent_ref="single-daemon-callback-smoke",
            input_text="Use callback_smoke for key health, then report its value.",
            instructions="Call callback_smoke exactly once before answering. Do not invent its result.",
            provider=provider,
            model=model,
            tools=[{
                "name": "callback_smoke",
                "description": "Return a deterministic callback smoke-test value.",
                "mode": "callback",
                "input_schema": {
                    "type": "object",
                    "properties": {"key": {"type": "string"}},
                    "required": ["key"],
                    "additionalProperties": False,
                },
                "callback": {"url": callback_url},
            }],
        ),
    )
    invocation_id = acknowledgement["invocation_id"]
    client.wait_for_status(invocation_id, "completed")
    transcript = client.get(
        f"/v1/sessions/{acknowledgement['session_id']}/transcript",
        {"limit": 100},
    )
    tool_messages = [
        message for message in transcript.get("messages", []) if message.get("role") == "tool"
    ]
    if transcript.get("has_more") is not False or len(tool_messages) != 1:
        raise RuntimeError("callback smoke did not persist exactly one canonical tool result")
    print("single_daemon callback ToolCall smoke passed")
    print(f"invocation: {invocation_id}")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "action",
        nargs="?",
        choices=(
            "run",
            "verify-restart",
            "client-tool",
            "client-tool-admit",
            "client-tool-resume",
            "callback",
        ),
        default="run",
    )
    parser.add_argument(
        "--state-file",
        type=pathlib.Path,
        default=None,
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        default_state_name = (
            "client-tool-state.json" if args.action.startswith("client-tool") else "smoke-state.json"
        )
        state_path = args.state_file or pathlib.Path(
            os.environ.get("NVOKEN_SMOKE_STATE_FILE", PROFILE_DIR / default_state_name)
        )
        client = RuntimeClient(
            os.environ.get("NVOKEN_BASE_URL", "http://127.0.0.1:8080"),
            required("NVOKEN_API_KEY"),
            positive_integer("NVOKEN_SMOKE_TIMEOUT_SECONDS", 300),
        )
        tested_revision = required("NVOKEN_TESTED_REVISION")
        if args.action == "run":
            run_smoke(client, state_path, tested_revision)
        elif args.action == "verify-restart":
            verify_restart(client, state_path, tested_revision)
        elif args.action == "client-tool":
            run_client_tool(client, state_path, tested_revision)
        elif args.action == "client-tool-admit":
            admit_client_tool(client, state_path, tested_revision)
        elif args.action == "client-tool-resume":
            resume_client_tool(client, state_path, tested_revision)
        else:
            run_callback(client)
        return 0
    except (KeyError, OSError, RuntimeError, ValueError, json.JSONDecodeError) as exc:
        print(f"single_daemon smoke failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
