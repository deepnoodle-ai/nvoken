#!/usr/bin/env python3
"""Exercise the documented daemon, packed TypeScript SDK, and newcomer UX."""

from __future__ import annotations

import base64
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tarfile
import tempfile
import time
from urllib.error import URLError
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


def check_daemon(work: Path, database_url: str) -> None:
    binary = work / "nvokend"
    run(["go", "build", "-o", str(binary), "./cmd/nvokend"])
    run([str(binary), "migrate"], environment={**os.environ, "DATABASE_URL": database_url})

    port = available_port()
    log_path = work / "nvokend.log"
    environment = {
        **os.environ,
        "DATABASE_URL": database_url,
        "PORT": str(port),
        "NVOKEN_PUBLIC_BASE_URL": f"http://127.0.0.1:{port}",
        "RUNTIME_API_KEY": "runtime-local-onboarding-secret-000000000000",
        "BOOTSTRAP_OWNER_SECRET": "bootstrap-local-onboarding-secret-00000000",
        "CREDENTIAL_DELIVERY_KEY": base64.urlsafe_b64encode(b"d" * 32).decode().rstrip("="),
        "OPENAI_API_KEY": "onboarding-provider-key",
        "ANTHROPIC_API_KEY": "",
        "SHUTDOWN_TIMEOUT": "5s",
        "ENGINE_DRAIN_GRACE": "2s",
    }
    with log_path.open("wb") as log:
        process = subprocess.Popen([str(binary), "serve"], cwd=ROOT, env=environment, stdout=log, stderr=log)
        try:
            wait_for(f"http://127.0.0.1:{port}/health", process)
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
    }
    if started is None or any(started.get(key) != value for key, value in expected.items()):
        raise RuntimeError(f"process_started did not match local guide: {started}")
    log_text = log_path.read_text(encoding="utf-8")
    for secret in (environment["RUNTIME_API_KEY"], environment["OPENAI_API_KEY"]):
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
    if metadata[0]["name"] != "@deepnoodle/nvoken" or metadata[0]["version"] != "0.1.0":
        raise RuntimeError(f"unexpected packed identity: {metadata[0]}")
    with tarfile.open(artifact, "r:gz") as archive:
        names = set(archive.getnames())
    required = {
        "package/LICENSE",
        "package/package.json",
        "package/README.md",
        "package/dist/index.js",
        "package/dist/index.d.ts",
    }
    if not required.issubset(names):
        raise RuntimeError(f"packed artifact is missing {sorted(required - names)}")
    excluded = ("/src/", "/dist/test/", "/dist/examples/")
    if any(name.endswith("/.env") or any(part in name for part in excluded) for name in names):
        raise RuntimeError("packed artifact contains local secrets, source, tests, or examples")
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
  agentRef: "packed-consumer",
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


def run_node(
    command: list[str],
    base_url: str,
    *,
    input_text: str = "",
    api_key: str = "test-key",
    model: str = "gpt-test",
    session_key: str | None = None,
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


def check_examples(base_url: str) -> None:
    quickstart = run_node(
        ["node", "sdk/typescript/dist/examples/quickstart.js"],
        base_url,
    )
    if "code word is cedar" not in quickstart.stdout or "agent> cedar" not in quickstart.stdout:
        raise RuntimeError(f"quickstart did not prove two-turn context: {quickstart.stdout}")

    run(["npm", "ci", "--prefix", "examples/typescript-chat"])
    run(["npm", "run", "build", "--prefix", "examples/typescript-chat"])
    chat = ["node", "examples/typescript-chat/dist/chat.js"]
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
        ROOT / "README.md": "docs/guides/local-development.md",
        ROOT / "sdk/typescript/README.md": "https://github.com/deepnoodle-ai/nvoken/blob/main/docs/guides/local-development.md",
        ROOT / "examples/typescript-chat/README.md": "../../docs/guides/local-development.md",
        ROOT / "docs/guides/local-development.md": "docker compose -f deploy/local/compose.yaml down --volumes",
        ROOT / "sdk/typescript/package.json": '"name": "@deepnoodle/nvoken"',
    }
    for path, text in expectations.items():
        if text not in path.read_text(encoding="utf-8"):
            raise RuntimeError(f"{path.relative_to(ROOT)} is missing {text!r}")
    for relative in (
        "deploy/local/compose.yaml",
        "deploy/local/nvoken.env.example",
        "deploy/local/configure.py",
    ):
        if not (ROOT / relative).is_file():
            raise RuntimeError(f"documented local artifact is missing: {relative}")


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
            check_examples(base_url)
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
