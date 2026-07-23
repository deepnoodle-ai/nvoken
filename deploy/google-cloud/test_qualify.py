import contextlib
import http.server
import json
import pathlib
import tempfile
import threading
import unittest
from unittest import mock

import qualify


FIXTURE_DIR = pathlib.Path(__file__).with_name("testdata")


def fixture(name):
    return json.loads((FIXTURE_DIR / name).read_text())


def runtime_v2_fixture():
    service = fixture("cloud-run-v2-service.json")
    service["name"] = "projects/nvoken-stage/locations/us-central1/services/nvoken-stage"
    service["latestReadyRevision"] = (
        "projects/nvoken-stage/locations/us-central1/services/nvoken-stage/"
        "revisions/nvoken-stage-00042-new"
    )
    service["uri"] = "https://runtime.example.test"
    return service


class PayloadCommands:
    def __init__(self, payloads):
        self.payloads = list(payloads)

    def json(self, _args, *, timeout=120):
        del timeout
        return self.payloads.pop(0)


class OutputCommands:
    def __init__(self, stdout):
        self.stdout = stdout

    def run(self, args, **_kwargs):
        return qualify.CommandResult(tuple(str(part) for part in args), 0, self.stdout, "")


class RuntimeHandler(http.server.BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def do_GET(self):
        if self.path == "/json":
            body = b'{"status":"completed"}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        if self.path.endswith("/transcript/stream"):
            body = (
                b"retry: 1000\n\n"
                b"event: transcript.update\n"
                b"id: cursor-1\n"
                b'data: {"messages":[],"invocation_changes":[]}\n\n'
                b"event: stream.end\n"
                b'data: {"reason":"terminal"}\n\n'
            )
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        self.send_response(404)
        self.end_headers()


@contextlib.contextmanager
def runtime_server():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), RuntimeHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield f"http://127.0.0.1:{server.server_port}"
    finally:
        server.shutdown()
        thread.join()
        server.server_close()


def config(**overrides):
    values = {
        "terraform_dir": pathlib.Path("deploy/google-cloud"),
        "terraform_var_file": None,
        "evidence_dir": pathlib.Path("docs/testing/readiness/evidence"),
        "environment": "stage",
        "provider": "anthropic",
        "model": "current-model",
        "callback_fixture_url": None,
        "notification_channel": None,
        "scenarios": ("baseline",),
        "max_provider_calls": 12,
        "timeout_seconds": 3600,
        "cleanup_timeout_seconds": 1200,
        "dry_run": True,
        "operator": "operator@example.test",
        "runtime_token_env": "NVOKEN_TEST_TOKEN",
        "verbose": False,
    }
    values.update(overrides)
    return qualify.Config(**values)


def profile(**overrides):
    values = {
        "project_id": "nvoken-stage",
        "environment": "stage",
        "region": "us-central1",
        "image": "us-central1-docker.pkg.dev/nvoken-stage/nvoken/nvokend:abc123",
        "service_name": "nvoken-stage",
        "service_url": "https://runtime.example.test",
        "executor_service_name": "nvoken-stage-executor",
        "executor_service_url": "https://executor.example.test",
        "execution_queue": "projects/nvoken-stage/locations/us-central1/queues/nvoken-stage-execution",
        "execution_queue_name": "nvoken-stage-execution",
        "dispatch_smoke_job_name": "nvoken-stage-dispatch-smoke",
        "migration_job_name": "nvoken-stage-migrate",
        "redis_instance_name": "nvoken-stage-live",
        "runtime_api_key_secret_id": "nvoken-stage-runtime",
        "task_caller_service_account_email": "task@nvoken-stage.iam.gserviceaccount.com",
        "runtime_service_account_email": "runtime@nvoken-stage.iam.gserviceaccount.com",
        "invocation_execution_mode": "cloud_tasks",
        "provider_secrets_configured": {"anthropic": True, "openai": False},
        "callback_signing_configured": False,
        "monitoring_notification_channels": [],
        "executor_max_instances": 3,
        "executor_request_concurrency": 4,
        "task_queue_max_concurrent_dispatches": 12,
        "synthetic_dispatch_delay_seconds": 0,
        "redis_memory_size_gb": 1,
    }
    values.update(overrides)
    return values


class ConfigTests(unittest.TestCase):
    def parse(self, *args):
        namespace = qualify.build_parser().parse_args(args)
        return qualify.config_from_args(namespace)

    def test_defaults_to_all_scenarios_with_bounded_limits(self):
        selected = self.parse(
            "--environment",
            "stage",
            "--provider",
            "anthropic",
            "--model",
            "current-model",
        )
        self.assertEqual(selected.scenarios, qualify.SCENARIOS)
        self.assertEqual(selected.max_provider_calls, 12)
        self.assertFalse(selected.dry_run)

    def test_repeated_scenario_flags_are_deduplicated_in_order(self):
        selected = self.parse(
            "--environment",
            "stage",
            "--provider",
            "openai",
            "--model",
            "current-model",
            "--scenario",
            "redis",
            "--scenario",
            "baseline",
            "--scenario",
            "redis",
        )
        self.assertEqual(selected.scenarios, ("redis", "baseline"))

    def test_rejects_unbounded_provider_count(self):
        with self.assertRaisesRegex(qualify.QualificationError, "max-provider-calls"):
            self.parse(
                "--environment",
                "stage",
                "--provider",
                "anthropic",
                "--model",
                "current-model",
                "--max-provider-calls",
                "21",
            )

    def test_rejects_unsafe_callback_url(self):
        with self.assertRaisesRegex(qualify.QualificationError, "public HTTPS"):
            self.parse(
                "--environment",
                "stage",
                "--provider",
                "anthropic",
                "--model",
                "current-model",
                "--callback-fixture-url",
                "https://secret@example.test/callback#fragment",
            )


class ProfileTests(unittest.TestCase):
    def qualification(self, selected_config=None):
        qualification = qualify.Qualification(
            selected_config or config(), qualify.Commands()
        )
        qualification.profile = profile()
        return qualification

    def test_accepts_minimum_baseline_profile(self):
        self.qualification().validate_profile()

    def test_rejects_environment_mismatch(self):
        qualification = self.qualification()
        qualification.profile["environment"] = "prod"
        with self.assertRaisesRegex(qualify.QualificationError, "does not match"):
            qualification.validate_profile()

    def test_requires_var_file_for_terraform_mutation(self):
        qualification = self.qualification(
            config(scenarios=("backlog",), dry_run=False)
        )
        with self.assertRaisesRegex(qualify.QualificationError, "terraform-var-file"):
            qualification.validate_profile()

    def test_requires_attached_notification_channel(self):
        qualification = self.qualification(
            config(
                scenarios=("alert",),
                notification_channel="projects/nvoken-stage/notificationChannels/1",
            )
        )
        with self.assertRaisesRegex(qualify.QualificationError, "not attached"):
            qualification.validate_profile()

    def test_baseline_live_run_requires_project_confirmation(self):
        qualification = self.qualification(config(dry_run=False))
        qualification.preflight = mock.Mock()
        qualification.print_plan = mock.Mock()
        qualification.confirm_project = mock.Mock(
            side_effect=qualify.QualificationError("stop after confirmation")
        )
        with self.assertRaisesRegex(qualify.QualificationError, "stop after confirmation"):
            qualification.run()
        qualification.confirm_project.assert_called_once_with()


class CloudRunPayloadTests(unittest.TestCase):
    def test_reads_knative_v1_describe_shape(self):
        service = fixture("cloud-run-v1-service.json")
        self.assertEqual(
            qualify.cloud_run_image(service),
            "us-central1-docker.pkg.dev/nvoken-stage/nvoken/nvokend:abc123",
        )
        self.assertEqual(
            qualify.cloud_run_revision(service),
            "nvoken-stage-executor-00041-old",
        )

    def test_reads_run_v2_describe_shape_and_normalizes_revision_name(self):
        service = fixture("cloud-run-v2-service.json")
        self.assertEqual(
            qualify.cloud_run_image(service),
            "us-central1-docker.pkg.dev/nvoken-stage/nvoken/nvokend:abc123",
        )
        self.assertEqual(
            qualify.cloud_run_revision(service),
            "nvoken-stage-executor-00042-new",
        )

    def test_executor_revision_accepts_run_v2_describe_shape(self):
        qualification = qualify.Qualification(
            config(), PayloadCommands([fixture("cloud-run-v2-service.json")])
        )
        qualification.profile = profile()
        self.assertEqual(
            qualification.executor_revision(),
            "nvoken-stage-executor-00042-new",
        )

    def test_resource_validation_fails_closed_when_service_image_is_missing(self):
        runtime = runtime_v2_fixture()
        del runtime["template"]["containers"][0]["image"]
        executor = fixture("cloud-run-v1-service.json")
        commands = PayloadCommands(
            [
                runtime,
                executor,
                {"state": "RUNNING"},
                {"state": "READY"},
                {"imageSummary": {"digest": "sha256:" + "a" * 64}},
            ]
        )
        qualification = qualify.Qualification(config(), commands)
        qualification.profile = profile()
        with self.assertRaisesRegex(
            qualify.QualificationError, "Runtime image could not be resolved"
        ):
            qualification.validate_google_resources()

    def test_resource_validation_accepts_v1_and_v2_describe_shapes(self):
        runtime = runtime_v2_fixture()
        executor = fixture("cloud-run-v1-service.json")
        commands = PayloadCommands(
            [
                runtime,
                executor,
                {"state": "RUNNING"},
                {"state": "READY"},
                {"imageSummary": {"digest": "sha256:" + "a" * 64}},
            ]
        )
        qualification = qualify.Qualification(config(), commands)
        qualification.profile = profile()
        qualification.validate_google_resources()
        self.assertEqual(
            qualification.references["runtime_revision"],
            "nvoken-stage-00042-new",
        )
        self.assertEqual(
            qualification.references["executor_revision"],
            "nvoken-stage-executor-00041-old",
        )


class LoggingTests(unittest.TestCase):
    def qualification(self, stdout):
        qualification = qualify.Qualification(config(), OutputCommands(stdout))
        qualification.profile = profile()
        return qualification

    def test_empty_logging_stdout_means_no_matches(self):
        self.assertEqual(self.qualification("").read_logs("test-filter"), [])

    def test_non_list_logging_payload_means_no_matches(self):
        self.assertEqual(self.qualification("{}").read_logs("test-filter"), [])

    def test_invalid_logging_payload_still_fails(self):
        with self.assertRaisesRegex(qualify.QualificationError, "did not return JSON"):
            self.qualification("not-json").read_logs("test-filter")


class RuntimeClientTests(unittest.TestCase):
    def test_reads_json_and_sse_frames(self):
        with runtime_server() as service_url:
            client = qualify.RuntimeClient(service_url, "test-token", 5)
            response = client.request("GET", "/json")
            self.assertEqual(response.json()["status"], "completed")
            frames = list(client.stream("sesn_test"))
        self.assertEqual(frames[0][0:2], ("transcript.update", "cursor-1"))
        self.assertEqual(frames[1][0], "stream.end")


class EvidenceTests(unittest.TestCase):
    def test_snapshot_keys_are_stable_identities(self):
        keys = qualify.snapshot_keys(
            {
                "messages": [{"sequence": 2}],
                "invocation_changes": [
                    {"invocation_id": "invk_test", "revision": 3}
                ],
            }
        )
        self.assertEqual(keys, {("message", "2"), ("invk_test", "3")})

    def test_secret_scanner_rejects_bearer_material(self):
        with self.assertRaisesRegex(qualify.QualificationError, "forbidden"):
            qualify.assert_secret_free("Authorization: Bearer secret")

    def test_callback_tool_call_is_read_from_canonical_transcript(self):
        tool_call_id = qualify.callback_tool_call_id(
            [
                {
                    "role": "assistant",
                    "content": [
                        {
                            "type": "tool_use",
                            "id": "tcal_test",
                            "name": "qualification_check",
                            "input": {},
                        }
                    ],
                }
            ]
        )
        self.assertEqual(tool_call_id, "tcal_test")

    def test_evidence_path_does_not_overwrite_an_existing_run(self):
        with tempfile.TemporaryDirectory() as directory:
            base = pathlib.Path(directory) / "2026-07-21-google-cloud-abc.md"
            base.write_text("first")
            selected = qualify.available_evidence_path(base)
        self.assertEqual(selected.name, "2026-07-21-google-cloud-abc-2.md")

    def test_nonsecret_configuration_is_bounded_to_named_values(self):
        qualification = qualify.Qualification(config(), qualify.Commands())
        qualification.profile = profile()
        selected = qualification.nonsecret_configuration()
        self.assertIn('"redis_memory_size_gb":1', selected)
        self.assertNotIn("runtime_api_key_secret_id", selected)

    def test_evidence_contains_outcomes_but_not_runtime_material(self):
        with tempfile.TemporaryDirectory() as directory:
            qualification = qualify.Qualification(
                config(evidence_dir=pathlib.Path(directory)), qualify.Commands()
            )
            qualification.profile = profile()
            qualification.git_revision = "a" * 40
            qualification.terraform_revision = "b" * 40
            qualification.start_plan_status = "no drift"
            qualification.end_plan_status = "no drift"
            qualification.provider_calls = 1
            qualification.results = [
                qualify.ScenarioResult("baseline", "pass", 1.25, "Invocation completed")
            ]
            path = qualification.write_evidence()
            content = path.read_text()
        self.assertIn("Invocation completed", content)
        self.assertIn("| Profile | `google_cloud` |", content)
        self.assertIn(f"| Tested revision | `{'a' * 40}` |", content)
        self.assertIn("| Result | `incomplete` |", content)
        self.assertNotIn("test-token", content)
        self.assertNotIn("Reply with", content)


class CleanupTests(unittest.TestCase):
    def test_cleanup_is_lifo_and_continues_after_failure(self):
        calls = []
        stack = qualify.CleanupStack()
        stack.push("first", lambda: calls.append("first"))

        def fail():
            calls.append("second")
            raise RuntimeError("expected")

        stack.push("second", fail)
        results = stack.run(float("inf"))
        self.assertEqual(calls, ["second", "first"])
        self.assertEqual([result.status for result in results], ["fail", "pass"])


if __name__ == "__main__":
    unittest.main()
