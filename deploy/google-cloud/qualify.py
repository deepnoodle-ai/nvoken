#!/usr/bin/env python3
"""Run the bounded nvoken Google Cloud qualification exercise.

This is deliberately an operator-facing runner, not a test control plane. It
qualifies an already deployed Terraform environment using the public Runtime
API and the existing Google Cloud resources. Temporary mutations are restored
in a best-effort cleanup pass and every outcome is written without prompts,
outputs, credentials, callback bodies, transcripts, or Terraform state.
"""

from __future__ import annotations

import argparse
import dataclasses
import datetime as dt
import getpass
import hashlib
import json
import os
import pathlib
import re
import shlex
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from collections.abc import Callable, Iterable, Iterator, Sequence
from typing import Any


MINIMUM_PYTHON = (3, 11)
TERMINAL_STATUSES = frozenset({"completed", "failed", "cancelled"})
REPO_ROOT = pathlib.Path(__file__).resolve().parents[2]
SCENARIOS = (
    "baseline",
    "queue-control",
    "delivery-control",
    "backlog",
    "revision-replacement",
    "redis",
    "callback",
    "alert",
)
DEFAULT_EVIDENCE_DIR = REPO_ROOT / "docs/testing/readiness/evidence"
MUTATING_SCENARIOS = frozenset(SCENARIOS[1:])


class QualificationError(RuntimeError):
    """A bounded qualification assertion or prerequisite failed."""


@dataclasses.dataclass(frozen=True)
class CommandResult:
    args: tuple[str, ...]
    returncode: int
    stdout: str
    stderr: str


class Commands:
    def __init__(self, *, verbose: bool = False) -> None:
        self.verbose = verbose

    def run(
        self,
        args: Sequence[str | os.PathLike[str]],
        *,
        check: bool = True,
        timeout: float | None = 120,
        input_text: str | None = None,
        env: dict[str, str] | None = None,
    ) -> CommandResult:
        command = tuple(os.fspath(part) for part in args)
        if self.verbose:
            print("+", shlex.join(command), file=sys.stderr)
        completed = subprocess.run(
            command,
            check=False,
            capture_output=True,
            text=True,
            timeout=timeout,
            input=input_text,
            env=env,
        )
        result = CommandResult(
            args=command,
            returncode=completed.returncode,
            stdout=completed.stdout,
            stderr=completed.stderr,
        )
        if check and result.returncode != 0:
            detail = result.stderr.strip() or result.stdout.strip() or "no output"
            raise QualificationError(
                f"command failed ({result.returncode}): {shlex.join(command)}: {detail}"
            )
        return result

    def json(
        self,
        args: Sequence[str | os.PathLike[str]],
        *,
        timeout: float | None = 120,
    ) -> Any:
        result = self.run(args, timeout=timeout)
        try:
            return json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise QualificationError(
                f"command did not return JSON: {shlex.join(result.args)}"
            ) from error


@dataclasses.dataclass(frozen=True)
class HTTPResult:
    status: int
    headers: dict[str, str]
    body: bytes

    def json(self) -> Any:
        try:
            return json.loads(self.body)
        except json.JSONDecodeError as error:
            raise QualificationError(f"HTTP {self.status} response was not JSON") from error


class RuntimeClient:
    def __init__(self, service_url: str, token: str, timeout: float) -> None:
        self.service_url = service_url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def request(
        self,
        method: str,
        path: str,
        *,
        body: Any | None = None,
        headers: dict[str, str] | None = None,
        expected: Iterable[int] = (200,),
    ) -> HTTPResult:
        request_headers = {
            "Accept": "application/json",
            "Authorization": f"Bearer {self.token}",
        }
        data = None
        if body is not None:
            data = json.dumps(body, separators=(",", ":")).encode()
            request_headers["Content-Type"] = "application/json"
        if headers:
            request_headers.update(headers)
        request = urllib.request.Request(
            self.service_url + path,
            method=method,
            headers=request_headers,
            data=data,
        )
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                result = HTTPResult(
                    status=response.status,
                    headers={key.lower(): value for key, value in response.headers.items()},
                    body=response.read(),
                )
        except urllib.error.HTTPError as error:
            result = HTTPResult(
                status=error.code,
                headers={key.lower(): value for key, value in error.headers.items()},
                body=error.read(),
            )
        if result.status not in set(expected):
            raise QualificationError(
                f"{method} {path} returned HTTP {result.status}; expected {sorted(expected)}"
            )
        return result

    def stream(
        self, session_id: str, *, last_event_id: str | None = None
    ) -> Iterator[tuple[str, str | None, Any]]:
        headers = {
            "Accept": "text/event-stream",
            "Authorization": f"Bearer {self.token}",
        }
        if last_event_id:
            headers["Last-Event-ID"] = last_event_id
        request = urllib.request.Request(
            f"{self.service_url}/v1/sessions/{urllib.parse.quote(session_id)}/transcript/stream",
            method="GET",
            headers=headers,
        )
        try:
            response = urllib.request.urlopen(request, timeout=self.timeout)
        except urllib.error.HTTPError as error:
            error.read()
            raise QualificationError(
                f"transcript stream returned HTTP {error.code}"
            ) from error
        with response:
            event_name = "message"
            event_id: str | None = None
            data_lines: list[str] = []
            for raw_line in response:
                line = raw_line.decode("utf-8").rstrip("\r\n")
                if line == "":
                    if data_lines:
                        try:
                            payload = json.loads("\n".join(data_lines))
                        except json.JSONDecodeError as error:
                            raise QualificationError("SSE data was not valid JSON") from error
                        yield event_name, event_id, payload
                    event_name = "message"
                    event_id = None
                    data_lines = []
                    continue
                if line.startswith(":") or line.startswith("retry:"):
                    continue
                field, _, value = line.partition(":")
                value = value[1:] if value.startswith(" ") else value
                if field == "event":
                    event_name = value
                elif field == "id":
                    event_id = value
                elif field == "data":
                    data_lines.append(value)


@dataclasses.dataclass
class ScenarioResult:
    name: str
    status: str
    duration_seconds: float
    summary: str
    references: list[str] = dataclasses.field(default_factory=list)


@dataclasses.dataclass
class CleanupResult:
    name: str
    status: str
    detail: str


class CleanupStack:
    def __init__(self) -> None:
        self._actions: list[tuple[str, Callable[[], None]]] = []

    def push(self, name: str, action: Callable[[], None]) -> None:
        self._actions.append((name, action))

    def run(self, deadline: float) -> list[CleanupResult]:
        results: list[CleanupResult] = []
        while self._actions:
            name, action = self._actions.pop()
            if time.monotonic() >= deadline:
                results.append(CleanupResult(name, "fail", "cleanup deadline exceeded"))
                continue
            try:
                action()
            except Exception as error:  # best effort must continue through every action
                results.append(CleanupResult(name, "fail", bounded(str(error), 240)))
            else:
                results.append(CleanupResult(name, "pass", "restored"))
        return results


@dataclasses.dataclass(frozen=True)
class Config:
    terraform_dir: pathlib.Path
    terraform_var_file: pathlib.Path | None
    evidence_dir: pathlib.Path
    environment: str
    provider: str
    model: str
    callback_fixture_url: str | None
    notification_channel: str | None
    scenarios: tuple[str, ...]
    max_provider_calls: int
    timeout_seconds: int
    cleanup_timeout_seconds: int
    dry_run: bool
    operator: str
    runtime_token_env: str
    verbose: bool


class Qualification:
    def __init__(self, config: Config, commands: Commands) -> None:
        self.config = config
        self.commands = commands
        self.cleanup = CleanupStack()
        self.outputs: dict[str, Any] = {}
        self.profile: dict[str, Any] = {}
        self.runtime: RuntimeClient | None = None
        self.run_id = dt.datetime.now(dt.UTC).strftime("%Y%m%dT%H%M%SZ") + "-" + uuid.uuid4().hex[:8]
        self.started_at = dt.datetime.now(dt.UTC)
        self.deadline = time.monotonic() + config.timeout_seconds
        self.provider_calls = 0
        self.results: list[ScenarioResult] = []
        self.cleanup_results: list[CleanupResult] = []
        self.references: dict[str, str] = {}
        self.revision_result = "not run"
        self.alert_result = "not run"
        self.collateral_incidents = "none observed by the runner"
        self.start_plan_status = "unknown"
        self.end_plan_status = "unknown"
        self.git_revision = "unknown"
        self.terraform_revision = "unknown"
        self.schema_expectation = expected_schema_version()

    @property
    def project(self) -> str:
        return required_string(self.profile, "project_id")

    @property
    def region(self) -> str:
        return required_string(self.profile, "region")

    @property
    def queue_name(self) -> str:
        return required_string(self.profile, "execution_queue_name")

    @property
    def runtime_client(self) -> RuntimeClient:
        if self.runtime is None:
            raise QualificationError("Runtime client is not initialized")
        return self.runtime

    def run(self) -> int:
        self.preflight()
        self.print_plan()
        if self.config.dry_run:
            print("Dry run complete; no provider request or resource mutation was made.")
            return 0

        self.confirm_project()

        failure: BaseException | None = None
        current_scenario: str | None = None
        try:
            for name in SCENARIOS:
                if name not in self.config.scenarios:
                    self.results.append(
                        ScenarioResult(name, "skipped", 0, "not selected for this run")
                    )
                    continue
                if failure is not None:
                    self.results.append(
                        ScenarioResult(name, "skipped", 0, "not run after an earlier failure")
                    )
                    continue
                current_scenario = name
                try:
                    self.run_scenario(name)
                except Exception as error:
                    failure = error
                else:
                    current_scenario = None
        except KeyboardInterrupt as error:
            failure = QualificationError("qualification interrupted by operator")
            if current_scenario and not any(
                result.name == current_scenario for result in self.results
            ):
                self.results.append(
                    ScenarioResult(
                        current_scenario,
                        "fail",
                        0,
                        "interrupted; cleanup attempted",
                    )
                )
            for name in SCENARIOS:
                if not any(result.name == name for result in self.results):
                    self.results.append(
                        ScenarioResult(name, "skipped", 0, "not run after interruption")
                    )
        finally:
            self.cleanup_results = self.cleanup.run(
                time.monotonic() + self.config.cleanup_timeout_seconds
            )
            try:
                self.end_plan_status = self.terraform_plan_status()
            except Exception as error:
                self.end_plan_status = f"failed: {bounded(str(error), 160)}"

        evidence_path = self.write_evidence()
        print(f"Evidence: {evidence_path}")
        if failure is not None:
            raise QualificationError(str(failure)) from failure
        if any(result.status != "pass" for result in self.cleanup_results):
            raise QualificationError("one or more cleanup actions failed")
        return 0

    def confirm_project(self, read: Callable[[str], str] | None = None) -> None:
        if read is None:
            read = input
        confirmation = read(
            f"Type the exact project ID {self.project!r} to start the qualification: "
        )
        if confirmation != self.project:
            raise QualificationError(
                "project confirmation did not match; qualification scenarios were not started"
            )

    def preflight(self) -> None:
        if sys.version_info < MINIMUM_PYTHON:
            raise QualificationError("Python 3.11 or newer is required")
        for command in ("gcloud", "git", "terraform"):
            self.commands.run([command, "--version"])
        self.git_revision = self.commands.run(
            ["git", "-C", REPO_ROOT, "rev-parse", "HEAD"]
        ).stdout.strip()
        status = self.commands.run(
            ["git", "-C", REPO_ROOT, "status", "--porcelain"]
        ).stdout.strip()
        if status:
            raise QualificationError("qualification requires a committed, clean checkout")
        self.terraform_revision = self.commands.run(
            ["git", "-C", REPO_ROOT, "rev-parse", "HEAD:deploy/google-cloud"]
        ).stdout.strip()

        raw_outputs = self.commands.json(self.terraform_command("output", "-json"))
        if not isinstance(raw_outputs, dict):
            raise QualificationError("Terraform outputs must be a JSON object")
        self.outputs = {
            key: value.get("value") if isinstance(value, dict) and "value" in value else value
            for key, value in raw_outputs.items()
        }
        profile = self.outputs.get("qualification_profile")
        if not isinstance(profile, dict):
            raise QualificationError(
                "Terraform output qualification_profile is missing; deploy this revision first"
            )
        self.profile = profile
        self.validate_profile()
        self.start_plan_status = self.terraform_plan_status()
        if (
            not self.config.dry_run
            and set(self.config.scenarios) & MUTATING_SCENARIOS
            and self.start_plan_status != "no drift"
        ):
            raise QualificationError(
                "live mutation requires a no-drift starting Terraform plan"
            )
        self.validate_google_resources()

        if self.config.dry_run:
            return
        token = os.environ.get(self.config.runtime_token_env, "")
        if not token:
            secret_id = required_string(self.profile, "runtime_api_key_secret_id")
            token = self.commands.run(
                [
                    "gcloud",
                    "secrets",
                    "versions",
                    "access",
                    "latest",
                    f"--secret={secret_id}",
                    f"--project={self.project}",
                ]
            ).stdout.strip()
        if not token:
            raise QualificationError("Runtime credential resolved to an empty value")
        self.runtime = RuntimeClient(
            required_string(self.profile, "service_url"),
            token,
            min(120, self.config.timeout_seconds),
        )
        health = self.runtime_client.request("GET", "/health", expected=(200,))
        if health.body.strip() != b"ok":
            raise QualificationError("public /health returned an unexpected body")

    def validate_profile(self) -> None:
        required = (
            "project_id",
            "environment",
            "region",
            "image",
            "service_name",
            "service_url",
            "executor_service_name",
            "executor_service_url",
            "execution_queue",
            "execution_queue_name",
            "dispatch_smoke_job_name",
            "migration_job_name",
            "redis_instance_name",
            "runtime_api_key_secret_id",
            "task_caller_service_account_email",
            "runtime_service_account_email",
        )
        for key in required:
            required_string(self.profile, key)
        if self.profile["environment"] != self.config.environment:
            raise QualificationError(
                f"selected environment {self.config.environment!r} does not match Terraform "
                f"environment {self.profile['environment']!r}"
            )
        if self.profile.get("invocation_execution_mode") != "cloud_tasks":
            raise QualificationError("qualification requires invocation_execution_mode=cloud_tasks")
        if ":latest" in required_string(self.profile, "image"):
            raise QualificationError("deployed image must be immutable, not latest")
        provider_secrets = self.profile.get("provider_secrets_configured", {})
        if not isinstance(provider_secrets, dict) or not provider_secrets.get(self.config.provider):
            raise QualificationError(
                f"no Terraform-configured {self.config.provider} provider secret"
            )
        selected = set(self.config.scenarios)
        if "callback" in selected:
            if not self.config.callback_fixture_url:
                raise QualificationError("callback scenario requires --callback-fixture-url")
            if not self.profile.get("callback_signing_configured"):
                raise QualificationError("callback signing is not configured in Terraform")
        if "alert" in selected:
            if not self.config.notification_channel:
                raise QualificationError("alert scenario requires --notification-channel")
            channels = self.profile.get("monitoring_notification_channels", [])
            if self.config.notification_channel not in channels:
                raise QualificationError(
                    "selected notification channel is not attached to the Terraform alerts"
                )
        if selected & {"backlog", "revision-replacement"}:
            if self.config.terraform_var_file is None:
                raise QualificationError(
                    "Terraform mutation scenarios require --terraform-var-file"
                )

    def validate_google_resources(self) -> None:
        runtime = self.gcloud_json(
            "run",
            "services",
            "describe",
            required_string(self.profile, "service_name"),
            f"--region={self.region}",
        )
        executor = self.gcloud_json(
            "run",
            "services",
            "describe",
            required_string(self.profile, "executor_service_name"),
            f"--region={self.region}",
        )
        queue = self.gcloud_json(
            "tasks",
            "queues",
            "describe",
            self.queue_name,
            f"--location={self.region}",
        )
        redis = self.gcloud_json(
            "redis",
            "instances",
            "describe",
            required_string(self.profile, "redis_instance_name"),
            f"--region={self.region}",
        )
        artifact = self.gcloud_json(
            "artifacts",
            "docker",
            "images",
            "describe",
            required_string(self.profile, "image"),
        )
        runtime_uri = first_string(
            nested(runtime, "uri"), nested(runtime, "status", "url")
        )
        if runtime_uri is None:
            raise QualificationError("Cloud Run public service URI could not be resolved")
        if runtime_uri.rstrip("/") != required_string(
            self.profile, "service_url"
        ).rstrip("/"):
            raise QualificationError("Terraform and Cloud Run public service URLs disagree")
        ingress = nested(executor, "ingress") or nested(
            executor, "metadata", "annotations", "run.googleapis.com/ingress"
        )
        if ingress not in ("INGRESS_TRAFFIC_INTERNAL_ONLY", "internal"):
            raise QualificationError("executor ingress is not internal-only")
        queue_state = str(nested(queue, "state") or "").upper()
        if queue_state != "RUNNING":
            raise QualificationError(
                f"execution queue state is {queue_state or 'unknown'}, not RUNNING"
            )
        redis_state = str(nested(redis, "state") or "").upper()
        if redis_state != "READY":
            raise QualificationError(f"Memorystore state is {redis_state or 'unknown'}, not READY")
        digest = (
            nested(artifact, "image_summary", "digest")
            or nested(artifact, "imageSummary", "digest")
            or nested(artifact, "digest")
        )
        if not isinstance(digest, str) or not digest.startswith("sha256:"):
            raise QualificationError("deployed image digest could not be resolved")
        configured_image = required_string(self.profile, "image")
        image_repository = configured_image.rsplit(":", 1)[0]
        immutable_image = f"{image_repository}@{digest}"
        self.references["immutable_image"] = immutable_image
        for service_name, service in (("Runtime", runtime), ("executor", executor)):
            service_image = cloud_run_image(service)
            if service_image is None:
                raise QualificationError(
                    f"{service_name} image could not be resolved from Cloud Run"
                )
            if service_image not in (configured_image, immutable_image):
                raise QualificationError(
                    f"{service_name} image does not match the Terraform qualification profile"
                )
        runtime_revision = cloud_run_revision(runtime)
        executor_revision = cloud_run_revision(executor)
        if runtime_revision is None:
            raise QualificationError("Runtime latest ready revision is unavailable")
        if executor_revision is None:
            raise QualificationError("executor latest ready revision is unavailable")
        self.references["runtime_revision"] = runtime_revision
        self.references["executor_revision"] = executor_revision
        self.references["queue_start_state"] = queue_state
        self.references["redis_start_state"] = redis_state

    def print_plan(self) -> None:
        print("Google Cloud qualification plan")
        print(f"  project: {self.project}")
        print(f"  environment: {self.profile['environment']}")
        print(f"  region: {self.region}")
        print(f"  runtime: {self.profile['service_name']} ({self.references.get('runtime_revision')})")
        print(f"  executor: {self.profile['executor_service_name']} ({self.references.get('executor_revision')})")
        print(f"  queue: {self.profile['execution_queue']}")
        print(f"  redis: {self.profile['redis_instance_name']}")
        print(f"  image: {self.references['immutable_image']}")
        print(f"  provider/model: {self.config.provider}/{self.config.model}")
        print(f"  maximum provider calls: {self.config.max_provider_calls}")
        print(f"  wall-clock bound: {self.config.timeout_seconds}s")
        print(f"  cleanup bound: {self.config.cleanup_timeout_seconds}s")
        print(f"  scenarios: {', '.join(self.config.scenarios)}")
        print(f"  starting Terraform plan: {self.start_plan_status}")
        print("  planned temporary mutations: queue state, selected Terraform limits/delay, Redis size, known test tasks")

    def run_scenario(self, name: str) -> None:
        if time.monotonic() >= self.deadline:
            raise QualificationError("qualification wall-clock deadline exceeded")
        method_name = "scenario_" + name.replace("-", "_")
        method = getattr(self, method_name)
        started = time.monotonic()
        print(f"Running scenario: {name}")
        try:
            summary, references = method()
        except Exception as error:
            duration = time.monotonic() - started
            self.results.append(
                ScenarioResult(name, "fail", duration, bounded(str(error), 400))
            )
            raise
        duration = time.monotonic() - started
        self.results.append(ScenarioResult(name, "pass", duration, summary, references))

    def scenario_baseline(self) -> tuple[str, list[str]]:
        tasks_before = self.list_task_names()
        admitted, request_id = self.admit("baseline", prompt_kind="brief")
        invocation_id = required_string(admitted, "invocation_id")
        session_id = required_string(admitted, "session_id")
        task_path = self.wait_new_task(tasks_before)
        dispatch_id = task_path.rsplit("/", 1)[-1]
        cursor, durable_keys, saw_delta = self.read_initial_stream(session_id, invocation_id)
        if not saw_delta:
            raise QualificationError("baseline stream did not observe a live generation.delta")
        terminal_change = self.resume_stream(
            session_id, invocation_id, cursor, durable_keys, require_delta=False
        )
        first = self.get_invocation(invocation_id)
        second = self.get_invocation(invocation_id)
        if first != second or first.get("status") != "completed":
            raise QualificationError("authoritative terminal Invocation reads did not agree")
        if terminal_change.get("status") != first.get("status"):
            raise QualificationError("terminal stream and JSON read status disagree")
        messages = self.list_messages(session_id)
        if not any(message.get("role") == "assistant" for message in messages):
            raise QualificationError("canonical transcript has no assistant message")
        executor_status = self.unauthenticated_executor_status()
        if executor_status not in (401, 403, 404):
            raise QualificationError(
                f"direct unauthenticated executor request returned HTTP {executor_status}"
            )
        runtime_log = self.wait_log(
            f'resource.type="cloud_run_revision" AND '
            f'resource.labels.service_name="{required_string(self.profile, "service_name")}" AND '
            f'jsonPayload.request_id="{request_id}" AND jsonPayload.status=202',
            timeout=120,
        )
        executor_log = self.wait_log(
            f'resource.type="cloud_run_revision" AND '
            f'resource.labels.service_name="{required_string(self.profile, "executor_service_name")}" AND '
            f'jsonPayload.dispatch_id="{dispatch_id}" AND '
            'jsonPayload.event="dispatch_attempt_decided"',
            timeout=120,
        )
        provider_log = self.wait_log(
            f'resource.type="cloud_run_revision" AND '
            f'resource.labels.service_name="{required_string(self.profile, "executor_service_name")}" AND '
            f'jsonPayload.invocation_id="{invocation_id}" AND '
            'jsonPayload.event="provider_generation" AND jsonPayload.outcome="success"',
            timeout=120,
        )
        runtime_revision = required_log_label(runtime_log, "revision_name")
        executor_revision = required_log_label(executor_log, "revision_name")
        if required_log_label(provider_log, "revision_name") != executor_revision:
            raise QualificationError("provider and dispatch evidence came from different executor revisions")
        references = [
            self.logs_link(f'jsonPayload.request_id="{request_id}"'),
            self.logs_link(f'jsonPayload.dispatch_id="{dispatch_id}"'),
            self.logs_link(
                f'jsonPayload.invocation_id="{invocation_id}" AND jsonPayload.event="provider_generation"'
            ),
        ]
        return (
            f"Invocation {invocation_id} completed through Runtime revision {runtime_revision}, dispatch {dispatch_id}, "
            f"and executor revision {executor_revision}; SSE resumed, JSON/transcript reads agreed, and direct "
            f"executor access returned {executor_status}",
            references,
        )

    def scenario_queue_control(self) -> tuple[str, list[str]]:
        self.pause_queue()
        cancelled, _ = self.admit("queued-cancel", prompt_kind="brief")
        queued, _ = self.admit("queued-resume", prompt_kind="brief")
        cancelled_id = required_string(cancelled, "invocation_id")
        queued_id = required_string(queued, "invocation_id")
        cancelled_row = self.cancel_invocation(cancelled_id)
        if cancelled_row.get("status") != "cancelled":
            raise QualificationError("queued cancellation did not settle as cancelled")
        if self.get_invocation(queued_id).get("status") != "queued":
            raise QualificationError("noncancelled acknowledgement was not durably queued")
        self.resume_queue()
        self.wait_invocation(queued_id, expected={"completed"})
        unchanged = self.get_invocation(cancelled_id)
        if unchanged != cancelled_row:
            raise QualificationError("queued cancellation changed after queue resume")
        return (
            f"queued Invocation {cancelled_id} stayed cancelled and {queued_id} completed after resume",
            [self.logs_link(f'jsonPayload.invocation_id="{queued_id}"')],
        )

    def scenario_delivery_control(self) -> tuple[str, list[str]]:
        self.pause_queue()
        before = self.list_task_names()
        duplicate_ack, _ = self.admit("duplicate-delivery", prompt_kind="long")
        duplicate_invocation = required_string(duplicate_ack, "invocation_id")
        original_task = self.wait_new_task(before)
        dispatch_id = original_task.rsplit("/", 1)[-1]
        duplicate_task = f"qual-{self.run_id.lower()}-{uuid.uuid4().hex[:8]}"
        duplicate_task_path = (
            required_string(self.profile, "execution_queue") + "/tasks/" + duplicate_task
        )
        self.cleanup.push(
            f"delete duplicate task {duplicate_task}",
            lambda: self.delete_task_if_safe(duplicate_task_path, duplicate_invocation),
        )
        self.create_http_task(
            duplicate_task,
            dispatch_id,
            required_string(self.profile, "task_caller_service_account_email"),
        )
        self.resume_queue()
        self.wait_invocation(duplicate_invocation, expected={"completed"})
        self.wait_log(
            f'jsonPayload.dispatch_id="{dispatch_id}" AND jsonPayload.event="dispatch_attempt_retry"',
            timeout=120,
        )
        self.wait_log(
            f'jsonPayload.dispatch_id="{dispatch_id}" AND jsonPayload.event="dispatch_attempt_decided"',
            timeout=120,
        )

        active_ack, _ = self.admit("active-cancel", prompt_kind="long")
        active_id = required_string(active_ack, "invocation_id")
        self.wait_invocation(active_id, expected={"running"}, terminal_is_error=True)
        cancelled_at = dt.datetime.now(dt.UTC)
        cancelled = self.cancel_invocation(active_id)
        if cancelled.get("status") != "cancelled":
            raise QualificationError("active cancellation did not settle as cancelled")
        self.wait_for_stable_terminal(active_id, "cancelled")
        messages = self.list_messages(required_string(active_ack, "session_id"))
        late_assistant = [
            message
            for message in messages
            if message.get("role") == "assistant"
            and parse_time(str(message.get("created_at", "1970-01-01T00:00:00Z"))) > cancelled_at
        ]
        if late_assistant:
            raise QualificationError("assistant output committed after active cancellation")
        return (
            f"dispatch {dispatch_id} received a real duplicate/retry and active Invocation {active_id} settled once as cancelled",
            [self.logs_link(f'jsonPayload.dispatch_id="{dispatch_id}"')],
        )

    def scenario_backlog(self) -> tuple[str, list[str]]:
        original = {
            "executor_max_instances": self.profile_number("executor_max_instances"),
            "executor_request_concurrency": self.profile_number(
                "executor_request_concurrency"
            ),
            "task_queue_max_concurrent_dispatches": self.profile_number(
                "task_queue_max_concurrent_dispatches"
            ),
        }
        self.cleanup.push(
            "restore backlog Terraform limits",
            lambda: self.apply_terraform_overrides(original),
        )
        self.apply_terraform_overrides(
            {
                "executor_max_instances": 1,
                "executor_request_concurrency": 1,
                "task_queue_max_concurrent_dispatches": 1,
            }
        )
        acknowledgements = [self.admit(f"backlog-{index}", prompt_kind="brief")[0] for index in range(3)]
        ids = [required_string(item, "invocation_id") for item in acknowledgements]
        observed_queued = False
        end = min(self.deadline, time.monotonic() + 120)
        while time.monotonic() < end:
            statuses = [self.get_invocation(invocation_id).get("status") for invocation_id in ids]
            if "running" in statuses and "queued" in statuses:
                observed_queued = True
                break
            if all(status in TERMINAL_STATUSES for status in statuses):
                break
            time.sleep(0.2)
        if not observed_queued:
            raise QualificationError("bounded backlog did not expose running plus durably queued work")
        for invocation_id in ids:
            self.wait_invocation(invocation_id, expected={"completed"})
        return (
            "three acknowledged Invocations were accounted for with one-at-a-time executor and queue capacity",
            [self.logs_link("jsonPayload.event=\"invocation_claimed\"")],
        )

    def scenario_revision_replacement(self) -> tuple[str, list[str]]:
        original_delay = self.profile_number("synthetic_dispatch_delay_seconds")
        held_delay = max(20, original_delay + 1)
        self.cleanup.push(
            "restore synthetic dispatch delay",
            lambda: self.apply_terraform_overrides(
                {"synthetic_dispatch_delay_seconds": original_delay}
            ),
        )
        self.apply_terraform_overrides({"synthetic_dispatch_delay_seconds": held_delay})
        held_revision = self.executor_revision()
        started = dt.datetime.now(dt.UTC)
        self.commands.run(
            [
                "gcloud",
                "run",
                "jobs",
                "execute",
                required_string(self.profile, "dispatch_smoke_job_name"),
                f"--project={self.project}",
                f"--region={self.region}",
                "--wait",
            ],
            timeout=180,
        )
        dispatch_id = self.wait_synthetic_dispatch_id(started)
        task_path = required_string(self.profile, "execution_queue") + "/tasks/" + dispatch_id
        self.wait_task_active(task_path)
        self.apply_terraform_overrides({"synthetic_dispatch_delay_seconds": original_delay})
        replacement_revision = self.executor_revision()
        if replacement_revision == held_revision:
            raise QualificationError("Terraform delay restoration did not create a replacement revision")
        entry = self.wait_log(
            f'jsonPayload.dispatch_id="{dispatch_id}" AND jsonPayload.event="dispatch_attempt_decided"',
            timeout=180,
        )
        settled_revision = str(nested(entry, "resource", "labels", "revision_name") or "unknown")
        if settled_revision != held_revision:
            self.revision_result = (
                f"delivery retried on {settled_revision}; synthetic work does not prove Invocation checkpoint recovery"
            )
            raise QualificationError(self.revision_result)
        self.revision_result = f"graceful drain on {held_revision} before replacement {replacement_revision}"
        return (
            f"synthetic dispatch {dispatch_id} completed by the draining revision before replacement",
            [self.logs_link(f'jsonPayload.dispatch_id="{dispatch_id}"')],
        )

    def scenario_redis(self) -> tuple[str, list[str]]:
        instance = required_string(self.profile, "redis_instance_name")
        original_size = self.profile_number("redis_memory_size_gb")
        interrupted_size = original_size + 1
        self.cleanup.push(
            "restore Redis size",
            lambda: self.resize_redis(original_size),
        )
        command = [
            "gcloud",
            "redis",
            "instances",
            "update",
            instance,
            f"--size={interrupted_size}",
            f"--region={self.region}",
            f"--project={self.project}",
            "--quiet",
        ]
        process = subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        self.cleanup.push(
            "wait for Redis update operation",
            lambda: wait_process(process, timeout=900),
        )
        self.wait_redis_not_ready()
        outage_ack, _ = self.admit("redis-interruption", prompt_kind="brief")
        outage_id = required_string(outage_ack, "invocation_id")
        outage_session = required_string(outage_ack, "session_id")
        terminal = self.consume_stream_to_terminal(outage_session, outage_id)
        if terminal.get("status") != "completed":
            raise QualificationError("canonical stream did not complete during Redis interruption")
        stdout, stderr = process.communicate(timeout=900)
        if process.returncode != 0:
            raise QualificationError(
                f"Redis interruption update failed: {bounded(stderr or stdout, 240)}"
            )
        self.wait_redis_ready()
        recovered_ack, _ = self.admit("redis-recovered", prompt_kind="brief")
        recovered_id = required_string(recovered_ack, "invocation_id")
        recovered_session = required_string(recovered_ack, "session_id")
        _, _, saw_delta = self.read_initial_stream(recovered_session, recovered_id)
        if not saw_delta:
            raise QualificationError("live fan-out did not recover after Redis became READY")
        self.wait_invocation(recovered_id, expected={"completed"})
        self.resize_redis(original_size)
        return (
            f"Invocation {outage_id} remained canonically readable through Redis interruption and live deltas recovered",
            [self.logs_link("jsonPayload.event=\"live_event_stream_resync\"")],
        )

    def scenario_callback(self) -> tuple[str, list[str]]:
        callback_url = self.config.callback_fixture_url
        if callback_url is None:
            raise QualificationError("callback fixture URL is missing")
        admitted, _ = self.admit("callback", prompt_kind="callback", callback_url=callback_url)
        invocation_id = required_string(admitted, "invocation_id")
        self.wait_invocation(invocation_id, expected={"completed"})
        tool_call_id = callback_tool_call_id(
            self.list_messages(required_string(admitted, "session_id"))
        )
        retry = self.wait_log(
            f'jsonPayload.tool_call_id="{tool_call_id}" AND '
            'jsonPayload.event="callback_delivery_retry"',
            timeout=120,
        )
        settled = self.wait_log(
            f'jsonPayload.tool_call_id="{tool_call_id}" AND '
            'jsonPayload.event="callback_delivery_settled"',
            timeout=120,
        )
        for key in ("delivery_id", "tool_call_id"):
            if nested(retry, "jsonPayload", key) != nested(settled, "jsonPayload", key):
                raise QualificationError(f"callback {key} changed across retry")
        return (
            f"callback Invocation {invocation_id} retried once with stable delivery and ToolCall identities",
            [self.logs_link(f'jsonPayload.tool_call_id="{tool_call_id}"')],
        )

    def scenario_alert(self) -> tuple[str, list[str]]:
        started = dt.datetime.now(dt.UTC).isoformat().replace("+00:00", "Z")
        task_name = f"qual-auth-{self.run_id.lower()}-{uuid.uuid4().hex[:8]}"
        dispatch_id = "dsp_00000000-0000-7000-8000-000000000024"
        task_path = required_string(self.profile, "execution_queue") + "/tasks/" + task_name
        self.cleanup.push(
            f"delete alert task {task_name}", lambda: self.delete_task(task_path)
        )
        self.create_http_task(
            task_name,
            dispatch_id,
            required_string(self.profile, "runtime_service_account_email"),
        )
        entry = self.wait_log(
            f'resource.type="cloud_run_revision" AND '
            f'resource.labels.service_name="{required_string(self.profile, "executor_service_name")}" AND '
            f'httpRequest.requestUrl:"/internal/execution-dispatches/{dispatch_id}/attempts" AND '
            f'timestamp>="{started}" AND (httpRequest.status=401 OR httpRequest.status=403)',
            timeout=300,
        )
        status = nested(entry, "httpRequest", "status")
        console_url = (
            "https://console.cloud.google.com/monitoring/alerting/incidents"
            f"?project={urllib.parse.quote(self.project)}"
        )
        print(f"Executor rejected the known task with HTTP {status}.")
        print(f"Observe the attached channel and incident here: {console_url}")
        observation = input(
            "After the notification arrives, enter the nonsecret incident ID (or 'fail'): "
        ).strip()
        if not observation or observation.lower() == "fail":
            raise QualificationError("operator did not confirm alert notification delivery")
        self.delete_task(task_path)
        closure = input(
            "After the incident closes, type 'closed' to confirm recovery: "
        ).strip()
        if closure != "closed":
            raise QualificationError("operator did not confirm alert incident closure")
        self.alert_result = f"incident {bounded(observation, 120)} notified and closed"
        return (
            f"known unauthorized task was rejected with HTTP {status}; {self.alert_result}",
            [console_url, self.logs_link("httpRequest.status=403 OR httpRequest.status=401")],
        )

    def admit(
        self,
        label: str,
        *,
        prompt_kind: str,
        callback_url: str | None = None,
    ) -> tuple[dict[str, Any], str]:
        expected_provider_calls = 2 if callback_url else 1
        if self.provider_calls + expected_provider_calls > self.config.max_provider_calls:
            raise QualificationError("maximum provider call count reached")
        self.provider_calls += expected_provider_calls
        correlation = f"qual-{self.run_id.lower()}-{label}-{uuid.uuid4().hex[:8]}"
        if prompt_kind == "long":
            prompt = "Explain durable workflow recovery in ten concise numbered points."
        elif prompt_kind == "callback":
            prompt = "Use the qualification_check tool exactly once, then briefly confirm completion."
        else:
            prompt = "Reply with one short sentence."
        spec: dict[str, Any] = {
            "instructions": "You are a concise staging qualification agent.",
            "model": {"provider": self.config.provider, "name": self.config.model},
        }
        if callback_url:
            spec["tools"] = [
                {
                    "name": "qualification_check",
                    "description": "Return one harmless staging qualification result.",
                    "mode": "callback",
                    "input_schema": {
                        "type": "object",
                        "properties": {},
                        "additionalProperties": False,
                    },
                    "callback": {"url": callback_url},
                }
            ]
        response = self.runtime_client.request(
            "POST",
            "/v1/invocations",
            body={
                "agent_ref": "google-cloud-qualification",
                "session_key": correlation,
                "idempotency_key": correlation,
                "input": {"content": [{"type": "text", "text": prompt}]},
                "spec": spec,
                "budgets": {"max_iterations": expected_provider_calls},
            },
            expected=(202,),
        )
        payload = response.json()
        if not isinstance(payload, dict) or payload.get("deduplicated") is not False:
            raise QualificationError("qualification admission was not a new durable Invocation")
        request_id = response.headers.get("x-request-id", "unknown")
        if request_id == "unknown":
            raise QualificationError("admission response omitted X-Request-ID")
        return payload, request_id

    def get_invocation(self, invocation_id: str) -> dict[str, Any]:
        result = self.runtime_client.request(
            "GET", f"/v1/invocations/{urllib.parse.quote(invocation_id)}"
        ).json()
        if not isinstance(result, dict):
            raise QualificationError("Invocation response was not an object")
        return result

    def wait_invocation(
        self,
        invocation_id: str,
        *,
        expected: set[str],
        terminal_is_error: bool = False,
        timeout: float = 300,
    ) -> dict[str, Any]:
        end = min(self.deadline, time.monotonic() + timeout)
        last: dict[str, Any] = {}
        while time.monotonic() < end:
            last = self.get_invocation(invocation_id)
            status = str(last.get("status", ""))
            if status in expected:
                return last
            if status in TERMINAL_STATUSES and (terminal_is_error or status not in expected):
                raise QualificationError(
                    f"Invocation {invocation_id} settled as {status}; expected {sorted(expected)}"
                )
            time.sleep(0.2)
        raise QualificationError(
            f"Invocation {invocation_id} did not reach {sorted(expected)} before timeout"
        )

    def wait_for_stable_terminal(self, invocation_id: str, status: str) -> None:
        first = self.get_invocation(invocation_id)
        time.sleep(1)
        second = self.get_invocation(invocation_id)
        if first != second or second.get("status") != status:
            raise QualificationError("terminal Invocation did not remain immutable")

    def cancel_invocation(self, invocation_id: str) -> dict[str, Any]:
        result = self.runtime_client.request(
            "POST",
            f"/v1/invocations/{urllib.parse.quote(invocation_id)}/cancel",
            expected=(200,),
        ).json()
        if not isinstance(result, dict):
            raise QualificationError("cancellation response was not an object")
        return result

    def list_messages(self, session_id: str) -> list[dict[str, Any]]:
        items: list[dict[str, Any]] = []
        cursor: str | None = None
        while True:
            path = f"/v1/sessions/{urllib.parse.quote(session_id)}/messages?limit=100"
            if cursor:
                path += "&cursor=" + urllib.parse.quote(cursor)
            payload = self.runtime_client.request("GET", path).json()
            if not isinstance(payload, dict) or not isinstance(payload.get("items"), list):
                raise QualificationError("message list response was malformed")
            items.extend(item for item in payload["items"] if isinstance(item, dict))
            if not payload.get("has_more"):
                return items
            cursor = payload.get("next_cursor")
            if not isinstance(cursor, str) or not cursor:
                raise QualificationError("message list omitted its continuation cursor")

    def read_initial_stream(
        self, session_id: str, invocation_id: str
    ) -> tuple[str, set[tuple[str, str]], bool]:
        cursor: str | None = None
        durable_keys: set[tuple[str, str]] = set()
        saw_delta = False
        end = min(self.deadline, time.monotonic() + 180)
        for event, event_id, payload in self.runtime_client.stream(session_id):
            if time.monotonic() >= end:
                break
            if event == "transcript.snapshot":
                if not event_id:
                    raise QualificationError("transcript snapshot omitted its durable SSE ID")
                cursor = event_id
                durable_keys.update(snapshot_keys(payload))
            elif event == "generation.delta" and payload.get("invocation_id") == invocation_id:
                saw_delta = True
            elif event == "stream.end" and payload.get("reason") == "terminal":
                break
            if cursor and saw_delta:
                break
        if not cursor:
            raise QualificationError("initial stream did not return a durable cursor")
        return cursor, durable_keys, saw_delta

    def resume_stream(
        self,
        session_id: str,
        invocation_id: str,
        cursor: str,
        prior_keys: set[tuple[str, str]],
        *,
        require_delta: bool,
    ) -> dict[str, Any]:
        saw_delta = False
        terminal_change: dict[str, Any] | None = None
        for event, _, payload in self.runtime_client.stream(
            session_id, last_event_id=cursor
        ):
            if event == "transcript.snapshot":
                repeated = prior_keys & snapshot_keys(payload)
                if repeated:
                    raise QualificationError(
                        "resumed stream replayed an already acknowledged durable row"
                    )
                for change in payload.get("invocation_changes", []):
                    if (
                        isinstance(change, dict)
                        and change.get("invocation_id") == invocation_id
                        and change.get("status") in TERMINAL_STATUSES
                    ):
                        terminal_change = change
            elif event == "generation.delta" and payload.get("invocation_id") == invocation_id:
                saw_delta = True
            elif event == "stream.end" and payload.get("reason") == "terminal":
                break
        if terminal_change is None:
            row = self.get_invocation(invocation_id)
            if row.get("status") not in TERMINAL_STATUSES:
                raise QualificationError("resumed stream ended without terminal durable state")
            terminal_change = {"status": row["status"]}
        if require_delta and not saw_delta:
            raise QualificationError("resumed stream did not observe a live generation delta")
        return terminal_change

    def consume_stream_to_terminal(
        self, session_id: str, invocation_id: str
    ) -> dict[str, Any]:
        terminal: dict[str, Any] | None = None
        for event, _, payload in self.runtime_client.stream(session_id):
            if event == "transcript.snapshot":
                for change in payload.get("invocation_changes", []):
                    if (
                        isinstance(change, dict)
                        and change.get("invocation_id") == invocation_id
                        and change.get("status") in TERMINAL_STATUSES
                    ):
                        terminal = change
            elif event == "stream.end" and payload.get("reason") == "terminal":
                break
        return terminal or self.get_invocation(invocation_id)

    def unauthenticated_executor_status(self) -> int:
        request = urllib.request.Request(
            required_string(self.profile, "executor_service_url") + "/health",
            method="GET",
        )
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                response.read()
                return response.status
        except urllib.error.HTTPError as error:
            error.read()
            return error.code

    def pause_queue(self) -> None:
        state = self.queue_state()
        if state == "PAUSED":
            return
        self.cleanup.push("resume execution queue", self.resume_queue)
        self.commands.run(
            [
                "gcloud",
                "tasks",
                "queues",
                "pause",
                self.queue_name,
                f"--location={self.region}",
                f"--project={self.project}",
                "--quiet",
            ]
        )
        self.wait_queue_state("PAUSED")

    def resume_queue(self) -> None:
        if self.queue_state() == "RUNNING":
            return
        self.commands.run(
            [
                "gcloud",
                "tasks",
                "queues",
                "resume",
                self.queue_name,
                f"--location={self.region}",
                f"--project={self.project}",
                "--quiet",
            ]
        )
        self.wait_queue_state("RUNNING")

    def queue_state(self) -> str:
        payload = self.gcloud_json(
            "tasks",
            "queues",
            "describe",
            self.queue_name,
            f"--location={self.region}",
        )
        return str(payload.get("state", "")).upper()

    def wait_queue_state(self, expected: str) -> None:
        end = min(self.deadline, time.monotonic() + 120)
        while time.monotonic() < end:
            if self.queue_state() == expected:
                return
            time.sleep(1)
        raise QualificationError(f"execution queue did not reach {expected}")

    def list_task_names(self) -> set[str]:
        payload = self.gcloud_json(
            "tasks",
            "list",
            f"--queue={self.queue_name}",
            f"--location={self.region}",
            "--limit=1000",
        )
        if not isinstance(payload, list):
            return set()
        return {
            str(item.get("name"))
            for item in payload
            if isinstance(item, dict) and item.get("name")
        }

    def wait_new_task(self, before: set[str]) -> str:
        end = min(self.deadline, time.monotonic() + 60)
        while time.monotonic() < end:
            created = self.list_task_names() - before
            if len(created) == 1:
                return created.pop()
            if len(created) > 1:
                raise QualificationError("more than one new task appeared during exact dispatch capture")
            time.sleep(0.2)
        raise QualificationError("new Invocation dispatch task did not appear")

    def create_http_task(self, task_name: str, dispatch_id: str, service_account: str) -> None:
        target = (
            required_string(self.profile, "executor_service_url")
            + "/internal/execution-dispatches/"
            + urllib.parse.quote(dispatch_id)
            + "/attempts"
        )
        self.commands.run(
            [
                "gcloud",
                "tasks",
                "create-http-task",
                task_name,
                f"--queue={self.queue_name}",
                f"--location={self.region}",
                f"--project={self.project}",
                f"--url={target}",
                "--method=POST",
                "--header=Content-Type:application/octet-stream",
                f"--oidc-service-account-email={service_account}",
                f"--oidc-token-audience={required_string(self.profile, 'executor_service_url')}",
            ]
        )

    def delete_task(self, task_path: str) -> None:
        result = self.commands.run(
            [
                "gcloud",
                "tasks",
                "delete",
                task_path.rsplit("/", 1)[-1],
                f"--queue={self.queue_name}",
                f"--location={self.region}",
                f"--project={self.project}",
                "--quiet",
            ],
            check=False,
        )
        if result.returncode != 0 and "NOT_FOUND" not in (result.stderr + result.stdout).upper():
            raise QualificationError(result.stderr.strip() or "delete task failed")

    def delete_task_if_safe(self, task_path: str, invocation_id: str) -> None:
        if self.get_invocation(invocation_id).get("status") not in TERMINAL_STATUSES:
            raise QualificationError("refusing to delete a task for uncertain durable work")
        self.delete_task(task_path)

    def apply_terraform_overrides(self, overrides: dict[str, int]) -> None:
        args = self.terraform_command("apply", "-input=false", "-auto-approve", "-no-color")
        if self.config.terraform_var_file:
            args.append(f"-var-file={self.config.terraform_var_file.resolve()}")
        args.extend(f"-var={key}={value}" for key, value in sorted(overrides.items()))
        self.commands.run(args, timeout=1200)

    def terraform_plan_status(self) -> str:
        args = self.terraform_command(
            "plan", "-input=false", "-detailed-exitcode", "-no-color", "-lock-timeout=30s"
        )
        if self.config.terraform_var_file:
            args.append(f"-var-file={self.config.terraform_var_file.resolve()}")
        result = self.commands.run(args, check=False, timeout=300)
        if result.returncode == 0:
            return "no drift"
        if result.returncode == 2:
            digest = hashlib.sha256(result.stdout.encode()).hexdigest()[:12]
            return f"changes present (plan sha256 {digest})"
        raise QualificationError(result.stderr.strip() or "Terraform plan failed")

    def terraform_command(self, *args: str) -> list[str]:
        return ["terraform", f"-chdir={self.config.terraform_dir.resolve()}", *args]

    def profile_number(self, key: str) -> int:
        value = self.profile.get(key)
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise QualificationError(f"qualification profile field {key} is not numeric")
        return int(value)

    def executor_revision(self) -> str:
        payload = self.gcloud_json(
            "run",
            "services",
            "describe",
            required_string(self.profile, "executor_service_name"),
            f"--region={self.region}",
        )
        revision = cloud_run_revision(payload)
        if revision is None:
            raise QualificationError("executor latest ready revision is unavailable")
        return revision

    def wait_synthetic_dispatch_id(self, started: dt.datetime) -> str:
        timestamp = started.isoformat().replace("+00:00", "Z")
        entry = self.wait_log(
            f'resource.type="cloud_run_job" AND '
            f'resource.labels.job_name="{required_string(self.profile, "dispatch_smoke_job_name")}" AND '
            'jsonPayload.message="created synthetic execution dispatch" AND '
            f'timestamp>="{timestamp}"',
            timeout=120,
        )
        dispatch_id = nested(entry, "jsonPayload", "dispatch_id")
        if not isinstance(dispatch_id, str) or not dispatch_id:
            raise QualificationError("synthetic dispatch log omitted dispatch_id")
        return dispatch_id

    def wait_task_active(self, task_path: str) -> None:
        name = task_path.rsplit("/", 1)[-1]
        end = min(self.deadline, time.monotonic() + 120)
        while time.monotonic() < end:
            result = self.commands.run(
                [
                    "gcloud",
                    "tasks",
                    "describe",
                    name,
                    f"--queue={self.queue_name}",
                    f"--location={self.region}",
                    f"--project={self.project}",
                    "--format=json",
                ],
                check=False,
            )
            if result.returncode == 0:
                payload = json.loads(result.stdout)
                dispatch_count = int(payload.get("dispatchCount", 0))
                response_count = int(payload.get("responseCount", 0))
                if dispatch_count >= 1 and response_count == 0:
                    return
            time.sleep(0.2)
        raise QualificationError("synthetic task did not expose an in-flight request")

    def resize_redis(self, size: int) -> None:
        current = self.gcloud_json(
            "redis",
            "instances",
            "describe",
            required_string(self.profile, "redis_instance_name"),
            f"--region={self.region}",
        )
        if str(current.get("state", "")).upper() != "READY":
            self.wait_redis_ready()
            current = self.gcloud_json(
                "redis",
                "instances",
                "describe",
                required_string(self.profile, "redis_instance_name"),
                f"--region={self.region}",
            )
        if int(current.get("memorySizeGb", 0)) == size and str(current.get("state")) == "READY":
            return
        self.commands.run(
            [
                "gcloud",
                "redis",
                "instances",
                "update",
                required_string(self.profile, "redis_instance_name"),
                f"--size={size}",
                f"--region={self.region}",
                f"--project={self.project}",
                "--quiet",
            ],
            timeout=1200,
        )
        self.wait_redis_ready()

    def wait_redis_not_ready(self) -> None:
        end = min(self.deadline, time.monotonic() + 120)
        while time.monotonic() < end:
            payload = self.gcloud_json(
                "redis",
                "instances",
                "describe",
                required_string(self.profile, "redis_instance_name"),
                f"--region={self.region}",
            )
            if str(payload.get("state", "")).upper() != "READY":
                return
            time.sleep(0.5)
        raise QualificationError("Redis update did not expose an interruption boundary")

    def wait_redis_ready(self) -> None:
        end = min(self.deadline, time.monotonic() + 900)
        while time.monotonic() < end:
            payload = self.gcloud_json(
                "redis",
                "instances",
                "describe",
                required_string(self.profile, "redis_instance_name"),
                f"--region={self.region}",
            )
            if str(payload.get("state", "")).upper() == "READY":
                return
            time.sleep(2)
        raise QualificationError("Redis did not return to READY")

    def gcloud_json(self, *args: str) -> Any:
        command = ["gcloud", *args, f"--project={self.project}", "--format=json"]
        return self.commands.json(command)

    def read_logs(self, filter_text: str, *, limit: int = 20) -> list[dict[str, Any]]:
        result = self.commands.run(
            [
                "gcloud",
                "logging",
                "read",
                filter_text,
                f"--project={self.project}",
                "--freshness=2h",
                f"--limit={limit}",
                "--order=desc",
                "--format=json",
            ]
        )
        if not result.stdout.strip():
            return []
        try:
            payload = json.loads(result.stdout)
        except json.JSONDecodeError as error:
            raise QualificationError("gcloud logging read did not return JSON") from error
        if not isinstance(payload, list):
            return []
        return [entry for entry in payload if isinstance(entry, dict)]

    def wait_log(self, filter_text: str, *, timeout: float) -> dict[str, Any]:
        end = min(self.deadline, time.monotonic() + timeout)
        while time.monotonic() < end:
            entries = self.read_logs(filter_text, limit=10)
            if entries:
                return entries[0]
            time.sleep(2)
        raise QualificationError(f"log evidence did not appear for: {bounded(filter_text, 160)}")

    def logs_link(self, filter_text: str) -> str:
        query = urllib.parse.quote(filter_text, safe="")
        return (
            "https://console.cloud.google.com/logs/query;query="
            f"{query}?project={urllib.parse.quote(self.project)}"
        )

    def nonsecret_configuration(self) -> str:
        selected = {
            "callback_signing_configured": bool(
                self.profile.get("callback_signing_configured")
            ),
            "executor_max_instances": self.profile_number("executor_max_instances"),
            "executor_request_concurrency": self.profile_number(
                "executor_request_concurrency"
            ),
            "invocation_execution_mode": self.profile.get(
                "invocation_execution_mode"
            ),
            "monitoring_notification_channels": self.profile.get(
                "monitoring_notification_channels", []
            ),
            "redis_memory_size_gb": self.profile_number("redis_memory_size_gb"),
            "synthetic_dispatch_delay_seconds": self.profile_number(
                "synthetic_dispatch_delay_seconds"
            ),
            "task_queue_max_concurrent_dispatches": self.profile_number(
                "task_queue_max_concurrent_dispatches"
            ),
        }
        return json.dumps(selected, sort_keys=True, separators=(",", ":"))

    def write_evidence(self) -> pathlib.Path:
        finished = dt.datetime.now(dt.UTC)
        short_revision = self.git_revision[:12]
        base_path = self.config.evidence_dir / (
            f"{finished.date().isoformat()}-google-cloud-{short_revision}.md"
        )
        path = available_evidence_path(base_path)
        path.parent.mkdir(parents=True, exist_ok=True)
        selected_results = [result for result in self.results if result.name in self.config.scenarios]
        all_pass = (
            len(selected_results) == len(SCENARIOS)
            and all(result.status == "pass" for result in selected_results)
            and all(result.status == "pass" for result in self.cleanup_results)
            and self.end_plan_status == "no drift"
        )
        lines = [
            "# Google Cloud qualification evidence",
            "",
            f"**Result:** {'Pass' if all_pass else 'Incomplete or failed'}",
            "",
            "| Field | Value |",
            "| --- | --- |",
            f"| Target | `{md(self.project)}` / `{md(self.config.environment)}` / `{md(self.region)}` / `google_cloud` |",
            f"| Operator | `{md(self.config.operator)}` |",
            f"| Build | git `{md(self.git_revision)}`; image `{md(self.references.get('immutable_image', required_string(self.profile, 'image')))}`; schema `{self.schema_expectation:06d}`; Terraform tree `{md(self.terraform_revision)}` |",
            f"| Bounds | `{md(self.config.provider)}/{md(self.config.model)}`; {self.provider_calls}/{self.config.max_provider_calls} provider calls; {self.started_at.isoformat()} to {finished.isoformat()} |",
            f"| Configuration | {md(self.nonsecret_configuration())} |",
            f"| Revision result | {md(self.revision_result)} |",
            f"| Alerts | {md(self.alert_result)}; collateral incidents: {md(self.collateral_incidents)} |",
            f"| Cleanup | start plan: {md(self.start_plan_status)}; end plan: {md(self.end_plan_status)}; {cleanup_summary(self.cleanup_results)} |",
            "| Deferred proof | Real-cloud forced process crash and commit/publish crash injection. |",
            "",
            "## Scenarios",
            "",
            "| Scenario | Result | Duration | Evidence |",
            "| --- | --- | ---: | --- |",
        ]
        for result in self.results:
            refs = ", ".join(
                f"[reference {index}]({reference})"
                for index, reference in enumerate(result.references, start=1)
            )
            evidence = md(result.summary)
            if refs:
                evidence += "; " + refs
            lines.append(
                f"| `{result.name}` | {result.status} | {result.duration_seconds:.1f}s | {evidence} |"
            )
        lines.extend(["", "## Cleanup", "", "| Resource | Result | Detail |", "| --- | --- | --- |"])
        if self.cleanup_results:
            for result in self.cleanup_results:
                lines.append(
                    f"| {md(result.name)} | {result.status} | {md(result.detail)} |"
                )
        else:
            lines.append("| No temporary mutation | pass | Nothing to restore. |")
        lines.extend(
            [
                "",
                "This record intentionally excludes credentials, provider prompts and outputs, callback bodies, transcripts, and Terraform state.",
                "",
            ]
        )
        content = "\n".join(lines)
        assert_secret_free(content)
        path.write_text(content, encoding="utf-8")
        return path


def snapshot_keys(payload: Any) -> set[tuple[str, str]]:
    if not isinstance(payload, dict):
        raise QualificationError("transcript snapshot payload was not an object")
    keys: set[tuple[str, str]] = set()
    for message in payload.get("messages", []):
        if isinstance(message, dict):
            keys.add(("message", str(message.get("sequence"))))
    for change in payload.get("invocation_changes", []):
        if isinstance(change, dict):
            keys.add(
                (
                    str(change.get("invocation_id")),
                    str(change.get("revision")),
                )
            )
    return keys


def callback_tool_call_id(messages: Sequence[dict[str, Any]]) -> str:
    for message in messages:
        if message.get("role") != "assistant":
            continue
        content = message.get("content")
        if not isinstance(content, list):
            continue
        for block in content:
            if (
                isinstance(block, dict)
                and block.get("type") == "tool_use"
                and block.get("name") == "qualification_check"
                and isinstance(block.get("id"), str)
                and block["id"]
            ):
                return block["id"]
    raise QualificationError("canonical transcript did not expose the qualification ToolCall ID")


def available_evidence_path(base_path: pathlib.Path) -> pathlib.Path:
    if not base_path.exists():
        return base_path
    for index in range(2, 1000):
        candidate = base_path.with_name(f"{base_path.stem}-{index}{base_path.suffix}")
        if not candidate.exists():
            return candidate
    raise QualificationError("could not allocate a unique evidence filename")


def expected_schema_version() -> int:
    migration_dir = pathlib.Path(__file__).resolve().parents[2] / "internal/adapters/postgres/migrations"
    versions = []
    for path in migration_dir.glob("*.up.sql"):
        match = re.match(r"^(\d{6})_", path.name)
        if match:
            versions.append(int(match.group(1)))
    if not versions:
        raise QualificationError("could not determine expected schema migration")
    return max(versions)


def parse_time(value: str) -> dt.datetime:
    try:
        return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise QualificationError(f"invalid timestamp in Runtime response: {value!r}") from error


def wait_process(process: subprocess.Popen[str], *, timeout: float) -> None:
    try:
        stdout, stderr = process.communicate(timeout=timeout)
    except subprocess.TimeoutExpired as error:
        process.terminate()
        raise QualificationError("child process did not finish before cleanup timeout") from error
    if process.returncode != 0:
        raise QualificationError(
            "child process failed during cleanup: " + bounded(stderr or stdout, 240)
        )


def nested(value: Any, *path: str | int) -> Any:
    current = value
    for key in path:
        if isinstance(key, int) and isinstance(current, list) and len(current) > key:
            current = current[key]
        elif isinstance(key, str) and isinstance(current, dict):
            current = current.get(key)
        else:
            return None
    return current


def first_string(*values: Any) -> str | None:
    for value in values:
        if isinstance(value, str) and value:
            return value
    return None


def cloud_run_image(service: Any) -> str | None:
    return first_string(
        nested(service, "template", "containers", 0, "image"),
        nested(service, "spec", "template", "spec", "containers", 0, "image"),
    )


def cloud_run_revision(service: Any) -> str | None:
    revision = first_string(
        nested(service, "latestReadyRevision"),
        nested(service, "status", "latestReadyRevisionName"),
        nested(service, "status", "traffic", 0, "revisionName"),
    )
    if revision is None:
        return None
    normalized = revision.rstrip("/").rsplit("/", 1)[-1]
    return normalized or None


def required_string(value: dict[str, Any], key: str) -> str:
    selected = value.get(key)
    if not isinstance(selected, str) or not selected.strip():
        raise QualificationError(f"required field {key} is missing or blank")
    return selected


def required_log_label(entry: dict[str, Any], key: str) -> str:
    value = nested(entry, "resource", "labels", key)
    if not isinstance(value, str) or not value:
        raise QualificationError(f"Cloud Logging entry omitted resource label {key}")
    return value


def bounded(value: str, maximum: int) -> str:
    normalized = " ".join(value.split())
    return normalized if len(normalized) <= maximum else normalized[: maximum - 3] + "..."


def md(value: str) -> str:
    return value.replace("|", "\\|").replace("\n", " ")


def cleanup_summary(results: Sequence[CleanupResult]) -> str:
    if not results:
        return "nothing to restore"
    passed = sum(result.status == "pass" for result in results)
    return f"{passed}/{len(results)} cleanup actions passed"


def assert_secret_free(content: str) -> None:
    # Evidence is assembled from an explicit nonsecret allowlist. This scan is
    # defense in depth for common accidental credential-bearing additions.
    forbidden = (
        "authorization: bearer",
        "api_key=",
        "api-key=",
        "private_key",
        "terraform.tfstate",
        "callback body",
        "provider output",
    )
    lowered = content.lower()
    matches = [term for term in forbidden if term in lowered]
    if matches:
        raise QualificationError(
            "refusing to write evidence containing forbidden material: " + ", ".join(matches)
        )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Run the bounded nvoken Google Cloud qualification exercise."
    )
    parser.add_argument("--environment", required=True)
    parser.add_argument("--provider", required=True, choices=("anthropic", "openai"))
    parser.add_argument("--model", required=True)
    parser.add_argument("--callback-fixture-url")
    parser.add_argument("--notification-channel")
    parser.add_argument(
        "--scenario",
        action="append",
        choices=("all", *SCENARIOS),
        help="Run one scenario; repeat the flag to select more. Defaults to all.",
    )
    parser.add_argument("--max-provider-calls", type=int, default=12)
    parser.add_argument("--timeout-seconds", type=int, default=3600)
    parser.add_argument("--cleanup-timeout-seconds", type=int, default=1200)
    parser.add_argument("--terraform-dir", type=pathlib.Path, default=pathlib.Path(__file__).parent)
    parser.add_argument("--terraform-var-file", type=pathlib.Path)
    parser.add_argument("--evidence-dir", type=pathlib.Path, default=DEFAULT_EVIDENCE_DIR)
    parser.add_argument(
        "--runtime-token-env",
        default="NVOKEN_QUALIFICATION_RUNTIME_TOKEN",
        help="Environment variable containing a managed Runtime token; falls back to the legacy Secret Manager output.",
    )
    parser.add_argument("--operator", default=getpass.getuser())
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--verbose", action="store_true")
    return parser


def config_from_args(args: argparse.Namespace) -> Config:
    selected = args.scenario or ["all"]
    scenarios = SCENARIOS if "all" in selected else tuple(dict.fromkeys(selected))
    if args.max_provider_calls < 1 or args.max_provider_calls > 20:
        raise QualificationError("--max-provider-calls must be from 1 through 20")
    if args.timeout_seconds < 300 or args.timeout_seconds > 7200:
        raise QualificationError("--timeout-seconds must be from 300 through 7200")
    if args.cleanup_timeout_seconds < 120 or args.cleanup_timeout_seconds > 3600:
        raise QualificationError("--cleanup-timeout-seconds must be from 120 through 3600")
    if not re.fullmatch(r"[a-z][a-z0-9-]{0,8}[a-z0-9]", args.environment):
        raise QualificationError("--environment must match the Terraform environment format")
    if not args.model.strip():
        raise QualificationError("--model must not be blank")
    if args.callback_fixture_url:
        parsed = urllib.parse.urlsplit(args.callback_fixture_url)
        if parsed.scheme != "https" or not parsed.netloc or parsed.username or parsed.fragment:
            raise QualificationError("--callback-fixture-url must be a public HTTPS URL without userinfo or fragment")
    return Config(
        terraform_dir=args.terraform_dir,
        terraform_var_file=args.terraform_var_file,
        evidence_dir=args.evidence_dir,
        environment=args.environment,
        provider=args.provider,
        model=args.model,
        callback_fixture_url=args.callback_fixture_url,
        notification_channel=args.notification_channel,
        scenarios=scenarios,
        max_provider_calls=args.max_provider_calls,
        timeout_seconds=args.timeout_seconds,
        cleanup_timeout_seconds=args.cleanup_timeout_seconds,
        dry_run=args.dry_run,
        operator=args.operator,
        runtime_token_env=args.runtime_token_env,
        verbose=args.verbose,
    )


def main(argv: Sequence[str] | None = None) -> int:
    try:
        args = build_parser().parse_args(argv)
        config = config_from_args(args)
        return Qualification(config, Commands(verbose=config.verbose)).run()
    except (QualificationError, KeyboardInterrupt) as error:
        print(f"qualification failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
