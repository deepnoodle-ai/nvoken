#!/usr/bin/env python3

from __future__ import annotations

import json
import os
import pathlib
import tempfile
import unittest
from unittest import mock

import smoke


class FakeRuntimeClient:
    def __init__(self) -> None:
        self.result_submissions = 0
        self.health_checks = 0
        self.exercise = ""

    def health(self) -> None:
        self.health_checks += 1

    def post(self, path: str, body: dict[str, object]) -> dict[str, object]:
        if path == "/v1/invocations":
            self.exercise = str(body["agent_ref"])
            return {
                "invocation_id": "invk_test",
                "session_id": "sesn_test",
                "status": "queued",
            }
        if path == "/v1/invocations/invk_test/tool-results":
            self.result_submissions += 1
            return {
                "results": [{"deduplicated": self.result_submissions > 1}],
            }
        raise AssertionError(f"unexpected POST {path}: {body}")

    def get(
        self,
        path: str,
        query: dict[str, str | int] | None = None,
    ) -> dict[str, object]:
        if path == "/v1/invocations/invk_test":
            if "client-tool" in self.exercise and self.result_submissions == 0:
                return {
                    "id": "invk_test",
                    "status": "waiting",
                    "pending_tool_calls": [{"id": "tcal_test"}],
                }
            return {"id": "invk_test", "status": "completed"}
        if path == "/v1/sessions/sesn_test/transcript":
            if query and "cursor" in query:
                return {
                    "has_more": False,
                    "messages": [],
                    "invocation_changes": [],
                }
            return {
                "has_more": False,
                "messages": [
                    {"id": "one", "role": "user"},
                    {"id": "two", "role": "assistant"},
                    {"id": "three", "role": "tool"},
                ],
                "invocation_changes": [{"status": "completed"}],
                "resume_cursor": "cursor_test",
            }
        raise AssertionError(f"unexpected GET {path}: {query}")

    def wait_for_status(self, invocation_id: str, wanted: str) -> dict[str, object]:
        if invocation_id != "invk_test":
            raise AssertionError(f"unexpected Invocation {invocation_id}")
        if wanted == "waiting":
            return {
                "id": invocation_id,
                "status": "waiting",
                "pending_tool_calls": [{"id": "tcal_test"}],
            }
        if wanted == "completed":
            return {"id": invocation_id, "status": "completed"}
        raise AssertionError(f"unexpected wanted status {wanted}")


class SmokeTest(unittest.TestCase):
    def setUp(self) -> None:
        self.environment = mock.patch.dict(
            os.environ,
            {
                "NVOKEN_SMOKE_PROVIDER": "anthropic",
                "NVOKEN_SMOKE_MODEL": "test-model",
                "NVOKEN_SMOKE_CALLBACK_URL": "https://callbacks.example.test/smoke",
            },
            clear=False,
        )
        self.environment.start()
        self.addCleanup(self.environment.stop)

    def test_normal_smoke_and_restart_state(self) -> None:
        client = FakeRuntimeClient()
        with tempfile.TemporaryDirectory() as directory:
            state_path = pathlib.Path(directory) / "smoke-state.json"
            smoke.run_smoke(client, state_path, "revision-test")
            state = json.loads(state_path.read_text())
            self.assertEqual(state["resume_cursor"], "cursor_test")
            self.assertIsNone(state["restart_verified_at"])
            self.assertEqual(state_path.stat().st_mode & 0o777, 0o600)

            smoke.verify_restart(client, state_path, "revision-test")
            state = json.loads(state_path.read_text())
            self.assertIsNotNone(state["restart_verified_at"])
            self.assertEqual(client.health_checks, 2)

    def test_client_tool_admit_resume_and_equal_replay(self) -> None:
        client = FakeRuntimeClient()
        with tempfile.TemporaryDirectory() as directory:
            state_path = pathlib.Path(directory) / "client-tool-state.json"
            smoke.admit_client_tool(client, state_path, "revision-test")
            state = json.loads(state_path.read_text())
            self.assertEqual(state["tool_call_id"], "tcal_test")

            smoke.resume_client_tool(client, state_path, "revision-test")
            state = json.loads(state_path.read_text())
            self.assertIsNotNone(state["result_submitted_at"])
            self.assertEqual(client.result_submissions, 2)

    def test_callback_smoke_uses_configured_receiver(self) -> None:
        client = FakeRuntimeClient()
        smoke.run_callback(client)
        self.assertEqual(client.health_checks, 1)


if __name__ == "__main__":
    unittest.main()
