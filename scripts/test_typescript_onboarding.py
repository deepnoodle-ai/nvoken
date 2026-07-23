#!/usr/bin/env python3
"""Exercise the documented daemon, packed TypeScript SDK, and newcomer UX."""

from __future__ import annotations

import json
import os
from pathlib import Path
import re
import shutil
import socket
import subprocess
import tarfile
import tempfile
import time
from urllib.error import URLError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


ROOT = Path(__file__).resolve().parents[1]


def run(
    command: list[str],
    *,
    cwd: Path = ROOT,
    environment: dict[str, str] | None = None,
    expected: int = 0,
) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(
        command,
        cwd=cwd,
        env=environment,
        check=False,
        text=True,
        capture_output=True,
    )
    if result.returncode != expected:
        raise RuntimeError(
            f"{' '.join(command)} returned {result.returncode}, want {expected}\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
    return result


def available_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


def wait_for(url: str, process: subprocess.Popen[bytes], authorization: str | None = None) -> None:
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"process exited before {url} became ready")
        try:
            headers = {"Authorization": authorization} if authorization else {}
            with urlopen(Request(url, headers=headers), timeout=0.5) as response:
                if response.status < 500:
                    return
        except (OSError, URLError):
            time.sleep(0.1)
    raise RuntimeError(f"timed out waiting for {url}")


def stop(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


def require_fragments(label: str, output: str, fragments: tuple[str, ...]) -> None:
    missing = [fragment for fragment in fragments if fragment not in output]
    if missing:
        raise RuntimeError(f"{label} missed {missing}: {output}")


def check_daemon(work: Path, database_url: str) -> None:
    binary = work / "nvokend"
    run(["go", "build", "-o", str(binary), "./cmd/nvokend"])

    selected_provider_key = "onboarding-provider-key"
    ambient_provider_key = "ambient-provider-key"
    configure_environment = {
        **os.environ,
        "OPENAI_API_KEY": selected_provider_key,
        "ANTHROPIC_API_KEY": ambient_provider_key,
    }
    configured = run(
        [
            "python3",
            str(ROOT / "deploy/local/configure.py"),
            "--provider",
            "openai",
            "--output",
            str(work / ".env"),
        ],
        cwd=work,
        environment=configure_environment,
    )
    if "ANTHROPIC_API_KEY is already exported" not in configured.stderr:
        raise RuntimeError(f"configurator did not warn about ambient provider: {configured.stderr}")
    if selected_provider_key in configured.stdout + configured.stderr or ambient_provider_key in configured.stdout + configured.stderr:
        raise RuntimeError("configurator warning exposed a provider key")
    configured_values = dict(
        line.split("=", 1)
        for line in (work / ".env").read_text(encoding="utf-8").splitlines()
        if "=" in line
    )
    runtime_api_key = configured_values["RUNTIME_API_KEY"]
    local_environment = dict(configure_environment)
    local_environment.pop("OPENAI_API_KEY", None)
    local_environment.pop("ANTHROPIC_API_KEY", None)
    local_environment["DATABASE_URL"] = database_url
    run([str(binary), "migrate"], cwd=work, environment=local_environment)

    port = available_port()
    log_path = work / "nvokend.log"
    environment = {
        **local_environment,
        "PORT": str(port),
        "NVOKEN_PUBLIC_BASE_URL": f"http://127.0.0.1:{port}",
        "SHUTDOWN_TIMEOUT": "5s",
        "ENGINE_DRAIN_GRACE": "2s",
    }
    with log_path.open("wb") as log:
        process = subprocess.Popen([str(binary), "serve"], cwd=work, env=environment, stdout=log, stderr=log)
        try:
            wait_for(f"http://127.0.0.1:{port}/health", process)
            pricing_cases = (
                ("openai", "gpt-5.4-mini", "priced"),
                ("anthropic", "claude-sonnet-4-6", "priced"),
                ("openai", "claude-sonnet-4-6", "unpriced"),
                ("anthropic", "gpt-5.4-mini", "unpriced"),
            )
            for provider, model, expected_status in pricing_cases:
                query = urlencode({"provider": provider, "model": model})
                request = Request(
                    f"http://127.0.0.1:{port}/v1/model-pricing-capabilities?{query}",
                    headers={"Authorization": f"Bearer {runtime_api_key}"},
                )
                with urlopen(request, timeout=2) as response:
                    capability = json.load(response)
                if capability.get("status") != expected_status or not capability.get("registry_version"):
                    raise RuntimeError(
                        f"pricing capability for {provider}/{model} = {capability}, want {expected_status}"
                    )
        finally:
            stop(process)

    entries = []
    for line in log_path.read_text(encoding="utf-8").splitlines():
        try:
            entries.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    started = next((entry for entry in entries if entry.get("event") == "process_started"), None)
    expected = {
        "process_role": "combined",
        "execution_mode": "embedded",
        "schema_compatibility": "compatible",
        "openai_enabled": True,
        "anthropic_enabled": False,
    }
    if started is None or any(started.get(key) != value for key, value in expected.items()):
        raise RuntimeError(f"process_started did not match local guide: {started}")
    log_text = log_path.read_text(encoding="utf-8")
    for secret in (selected_provider_key, ambient_provider_key):
        if secret in log_text:
            raise RuntimeError("daemon onboarding log exposed a secret")


def start_conformance(work: Path) -> tuple[subprocess.Popen[bytes], str]:
    binary = work / "nvoken-conformance"
    run(["go", "build", "-o", str(binary), "./sdk/conformance/server"])
    port = available_port()
    environment = {
        **os.environ,
        "NVOKEN_CONFORMANCE_ADDR": f"127.0.0.1:{port}",
        "NVOKEN_CONFORMANCE_ONBOARDING": "1",
    }
    log = (work / "conformance.log").open("wb")
    process = subprocess.Popen([str(binary)], cwd=ROOT, env=environment, stdout=log, stderr=log)
    process._nvoken_log = log  # type: ignore[attr-defined]
    wait_for(f"http://127.0.0.1:{port}/healthz", process)
    return process, f"http://127.0.0.1:{port}"


def pack_sdk(work: Path) -> Path:
    run(["npm", "ci", "--prefix", "sdk/typescript"])
    run(["npm", "run", "build", "--prefix", "sdk/typescript"])
    packed = run(
        ["npm", "pack", "./sdk/typescript", "--json", "--pack-destination", str(work)],
    )
    metadata = json.loads(packed.stdout)
    artifact = work / metadata[0]["filename"]
    package = json.loads((ROOT / "sdk/typescript/package.json").read_text(encoding="utf-8"))
    if metadata[0]["name"] != package["name"] or metadata[0]["version"] != package["version"]:
        raise RuntimeError(f"unexpected packed identity: {metadata[0]}")
    with tarfile.open(artifact, "r:gz") as archive:
        names = set(archive.getnames())
    required = {
        "package/LICENSE",
        "package/package.json",
        "package/README.md",
        "package/dist/index.js",
        "package/dist/index.d.ts",
        "package/dist/examples/quickstart.js",
    }
    if not required.issubset(names):
        raise RuntimeError(f"packed artifact is missing {sorted(required - names)}")
    excluded = ("/src/", "/dist/test/")
    unexpected_examples = {
        name
        for name in names
        if "/dist/examples/" in name and name != "package/dist/examples/quickstart.js"
    }
    if unexpected_examples or any(
        name.endswith("/.env") or any(part in name for part in excluded)
        for name in names
    ):
        raise RuntimeError("packed artifact contains local secrets, source, tests, or unexpected examples")
    return artifact


def check_empty_consumer(work: Path, artifact: Path, base_url: str) -> None:
    consumer = work / "consumer"
    consumer.mkdir()
    (consumer / "package.json").write_text(
        json.dumps({"name": "nvoken-packed-consumer", "private": True, "type": "module"}) + "\n",
        encoding="utf-8",
    )
    run(["npm", "install", "--save-exact", str(artifact)], cwd=consumer)
    run(
        ["npm", "install", "--save-dev", "typescript@5.8.3", "@types/node@24.0.15"],
        cwd=consumer,
    )
    (consumer / "tsconfig.json").write_text(
        json.dumps(
            {
                "compilerOptions": {
                    "module": "NodeNext",
                    "moduleResolution": "NodeNext",
                    "outDir": "dist",
                    "strict": True,
                    "target": "ES2022",
                },
                "include": ["index.ts"],
            }
        )
        + "\n",
        encoding="utf-8",
    )
    (consumer / "index.ts").write_text(
        """import { Client, isTextContentBlock } from "@deepnoodle/nvoken";
const client = new Client({ baseUrl: process.env.NVOKEN_BASE_URL!, apiKey: "test-key" });
const handle = await client.invoke({
  agentKey: "packed-consumer",
  sessionKey: "packed-consumer",
  idempotencyKey: "packed-consumer:message-1",
  input: "hello",
  spec: { instructions: "help", model: { provider: "openai", name: "gpt-test" } },
});
const invocation = await handle.wait();
if (invocation.status !== "completed") throw new Error(invocation.status);
const messages = await handle.listMessages();
if (!messages.flatMap((message) => message.content).some(isTextContentBlock)) throw new Error("no text block");
console.log(await handle.text());
""",
        encoding="utf-8",
    )
    run(["npm", "exec", "--", "tsc", "-p", "tsconfig.json"], cwd=consumer)
    result = run(
        ["node", "dist/index.js"],
        cwd=consumer,
        environment={**os.environ, "NVOKEN_BASE_URL": base_url},
    )
    if result.stdout.strip() != "world":
        raise RuntimeError(f"packed facade output = {result.stdout!r}")

    installed_readme = (consumer / "node_modules/@deepnoodle/nvoken/README.md").read_text(encoding="utf-8")
    snippet = re.search(
        r"<!-- public-quickstart:start -->\s*```js\n(.*?)\n```\s*<!-- public-quickstart:end -->",
        installed_readme,
        re.DOTALL,
    )
    if snippet is None:
        raise RuntimeError("packed README has no executable public quickstart")
    (consumer / "quickstart.mjs").write_text(snippet.group(1) + "\n", encoding="utf-8")
    public_environment = {
        **os.environ,
        "NVOKEN_BASE_URL": base_url,
        "NVOKEN_API_KEY": "test-key",
        "NVOKEN_PROVIDER": "openai",
        "NVOKEN_MODEL": "gpt-test",
    }
    public_quickstart = run(
        ["node", "quickstart.mjs"],
        cwd=consumer,
        environment=public_environment,
    )
    if "pricing=priced registry=conformance-v1" not in public_quickstart.stdout or "agent> world" not in public_quickstart.stdout:
        raise RuntimeError(f"packed public quickstart output = {public_quickstart.stdout!r}")

    packaged_command = run(
        [str(consumer / "node_modules/.bin/nvoken-quickstart")],
        cwd=consumer,
        environment=public_environment,
    )
    if "code word is cedar" not in packaged_command.stdout or "agent> cedar" not in packaged_command.stdout:
        raise RuntimeError(f"packed command did not prove durable context: {packaged_command.stdout!r}")

    (consumer / ".env").write_text(
        "# Generated by nvokend quickstart. Disposable local use only.\n"
        f"NVOKEN_BASE_URL={base_url}\n"
        "NVOKEN_API_KEY=test-key\n"
        "NVOKEN_PROVIDER=openai\n"
        "NVOKEN_MODEL=gpt-test\n",
        encoding="utf-8",
    )
    environment_from_file = dict(os.environ)
    for name in ("NVOKEN_BASE_URL", "NVOKEN_API_KEY", "NVOKEN_PROVIDER", "NVOKEN_MODEL"):
        environment_from_file.pop(name, None)
    packaged_from_file = run(
        [str(consumer / "node_modules/.bin/nvoken-quickstart")],
        cwd=consumer,
        environment=environment_from_file,
    )
    if "code word is cedar" not in packaged_from_file.stdout or "agent> cedar" not in packaged_from_file.stdout:
        raise RuntimeError(f"packed command did not load generated .env: {packaged_from_file.stdout!r}")

    invalid_credential = run(
        ["node", "quickstart.mjs"],
        cwd=consumer,
        environment={**public_environment, "NVOKEN_API_KEY": "invalid-key"},
        expected=1,
    )
    credential_error = invalid_credential.stdout + invalid_credential.stderr
    if "nvoken error [authentication] code=unauthenticated request_id=req_" not in credential_error:
        raise RuntimeError(f"packed public quickstart credential error was not actionable: {credential_error}")

    invalid_model = run(
        ["node", "quickstart.mjs"],
        cwd=consumer,
        environment={**public_environment, "NVOKEN_MODEL": "invalid-model"},
        expected=1,
    )
    model_error = invalid_model.stdout + invalid_model.stderr
    require_fragments(
        "packed public quickstart model error",
        model_error,
        (
            "Invocation invk_",
            "failed: provider_error:",
            "The provider rejected the requested model.",
            "Safe details:",
            "classification",
            "upstream_rejected",
            "https://developers.openai.com/api/docs/models.",
        ),
    )
    for output in (credential_error, model_error):
        if any(forbidden in output for forbidden in ("ResponseError", "node:internal", "\n    at ")):
            raise RuntimeError(f"packed public quickstart printed an internal stack: {output}")


def run_node(
    command: list[str],
    base_url: str,
    *,
    input_text: str = "",
    api_key: str = "test-key",
    model: str = "gpt-test",
    session_key: str | None = None,
    run_key: str | None = None,
    expected: int = 0,
) -> subprocess.CompletedProcess[str]:
    environment = {
        **os.environ,
        "NVOKEN_BASE_URL": base_url,
        "NVOKEN_API_KEY": api_key,
        "NVOKEN_PROVIDER": "openai",
        "NVOKEN_MODEL": model,
    }
    if session_key is not None:
        environment["NVOKEN_SESSION_KEY"] = session_key
    if run_key is not None:
        environment["NVOKEN_RUN_KEY"] = run_key
    result = subprocess.run(
        command,
        cwd=ROOT,
        env=environment,
        input=input_text,
        check=False,
        text=True,
        capture_output=True,
    )
    if result.returncode != expected:
        raise RuntimeError(
            f"{' '.join(command)} returned {result.returncode}, want {expected}\n"
            f"stdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
    return result


def check_examples(work: Path, artifact: Path, base_url: str) -> None:
    quickstart = run_node(
        ["node", "sdk/typescript/dist/examples/quickstart.js"],
        base_url,
    )
    if "code word is cedar" not in quickstart.stdout or "agent> cedar" not in quickstart.stdout:
        raise RuntimeError(f"quickstart did not prove two-turn context: {quickstart.stdout}")
    session = re.search(r"^session_key=(.+)$", quickstart.stdout, re.MULTILINE)
    if session is None:
        raise RuntimeError(f"quickstart did not print a Session key: {quickstart.stdout}")
    missing_run_key = run_node(
        ["node", "sdk/typescript/dist/examples/quickstart.js"],
        base_url,
        session_key=session.group(1),
        expected=1,
    )
    missing_run_error = missing_run_key.stdout + missing_run_key.stderr
    if missing_run_error.strip() != "NVOKEN_RUN_KEY is required when NVOKEN_SESSION_KEY resumes an existing Session":
        raise RuntimeError(f"quickstart missing-run-key error was not concise: {missing_run_error}")
    resumed_quickstart = run_node(
        ["node", "sdk/typescript/dist/examples/quickstart.js"],
        base_url,
        session_key=session.group(1),
        run_key="onboarding-quickstart-resume",
    )
    if resumed_quickstart.stdout.count("agent> cedar") != 1:
        raise RuntimeError(f"quickstart did not append a resumed turn: {resumed_quickstart.stdout}")

    failed_quickstart = run_node(
        ["node", "sdk/typescript/dist/examples/quickstart.js"],
        base_url,
        model="invalid-model",
        expected=1,
    )
    rendered_failure = failed_quickstart.stdout + failed_quickstart.stderr
    require_fragments(
        "source quickstart model error",
        rendered_failure,
        (
            "Invocation invk_",
            "failed: provider_error:",
            "The provider rejected the requested model.",
            "Safe details:",
            "classification",
            "upstream_rejected",
            "https://developers.openai.com/api/docs/models.",
            "Inspect structured daemon logs",
        ),
    )

    chat_root = work / "typescript-chat"
    shutil.copytree(
        ROOT / "examples/typescript-chat",
        chat_root,
        ignore=shutil.ignore_patterns("dist", "node_modules"),
    )
    package_path = chat_root / "package.json"
    package = json.loads(package_path.read_text(encoding="utf-8"))
    package["dependencies"]["@deepnoodle/nvoken"] = str(artifact)
    package_path.write_text(json.dumps(package, indent=2) + "\n", encoding="utf-8")
    run(["npm", "install"], cwd=chat_root)
    run(["npm", "run", "build"], cwd=chat_root)
    chat = ["node", str(chat_root / "dist/chat.js")]
    first = run_node(
        chat,
        base_url,
        input_text="Remember that the launch city is Lisbon.\n",
        session_key="onboarding-resume",
    )
    second = run_node(
        chat,
        base_url,
        input_text="What is the launch city? Reply only with the city.\n",
        session_key="onboarding-resume",
    )
    if "Lisbon" not in first.stdout or "agent> Lisbon" not in second.stdout:
        raise RuntimeError("chat example did not resume the durable Session across processes")

    invalid_credential = run_node(
        chat,
        base_url,
        input_text="hello\n",
        api_key="invalid-key",
        expected=1,
    )
    credential_error = invalid_credential.stdout + invalid_credential.stderr
    if "authentication" not in credential_error or "request_id=req_" not in credential_error:
        raise RuntimeError(f"invalid credential was not actionable: {credential_error}")

    invalid_model = run_node(
        chat,
        base_url,
        input_text="hello\n",
        model="invalid-model",
        expected=1,
    )
    model_error = invalid_model.stdout + invalid_model.stderr
    if "provider_error" not in model_error or "Invocation invk_" not in model_error:
        raise RuntimeError(f"invalid model was not actionable: {model_error}")
    for output in (credential_error, model_error):
        if "provider-secret" in output or "raw provider" in output.lower():
            raise RuntimeError("failure UX leaked a provider secret or body")


def check_documentation() -> None:
    expectations = {
        ROOT / "README.md": "docs/guides/run-locally.md",
        ROOT / "sdk/typescript/README.md": "https://github.com/deepnoodle-ai/nvoken/blob/main/docs/guides/run-locally.md",
        ROOT / "examples/typescript-chat/README.md": "../../docs/guides/run-locally.md",
        ROOT / "docs/guides/run-locally.md": "brew install deepnoodle-ai/tap/nvoken",
        ROOT / "docs/guides/developing-nvoken.md": "go run ./cmd/nvokend quickstart",
        ROOT / "sdk/typescript/package.json": '"name": "@deepnoodle/nvoken"',
    }
    for path, text in expectations.items():
        if text not in path.read_text(encoding="utf-8"):
            raise RuntimeError(f"{path.relative_to(ROOT)} is missing {text!r}")
    local_guide = (ROOT / "docs/guides/run-locally.md").read_text(encoding="utf-8")
    for text in (
        "nvokend quickstart --provider openai --model",
        "@deepnoodle/nvoken@$(nvokend --version)",
        "nvokend quickstart cleanup",
        "https://developers.openai.com/api/docs/models",
        "https://platform.claude.com/docs/en/about-claude/models/overview",
    ):
        if text not in local_guide:
            raise RuntimeError(f"local guide is missing {text!r}")
    for relative in (
        "deploy/local/compose.yaml",
        "deploy/local/nvoken.env.example",
        "deploy/local/configure.py",
    ):
        if not (ROOT / relative).is_file():
            raise RuntimeError(f"documented local artifact is missing: {relative}")
    sdk_package = json.loads((ROOT / "sdk/typescript/package.json").read_text(encoding="utf-8"))
    example_package = json.loads(
        (ROOT / "examples/typescript-chat/package.json").read_text(encoding="utf-8")
    )
    if example_package["dependencies"]["@deepnoodle/nvoken"] != sdk_package["version"]:
        raise RuntimeError("TypeScript chat dependency does not match the repository release version")


def main() -> int:
    database_url = os.environ.get("NVOKEN_TEST_DATABASE_URL")
    if not database_url:
        raise SystemExit("NVOKEN_TEST_DATABASE_URL is required")
    with tempfile.TemporaryDirectory(prefix="nvoken-onboarding-") as temporary:
        work = Path(temporary)
        if shutil.which("docker"):
            run(["docker", "compose", "-f", "deploy/local/compose.yaml", "config", "--quiet"])
        check_daemon(work, database_url)
        conformance, base_url = start_conformance(work)
        try:
            artifact = pack_sdk(work)
            check_empty_consumer(work, artifact, base_url)
            check_examples(work, artifact, base_url)
        finally:
            stop(conformance)
            log = getattr(conformance, "_nvoken_log", None)
            if log is not None:
                log.close()
        check_documentation()
    print("TypeScript onboarding check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
