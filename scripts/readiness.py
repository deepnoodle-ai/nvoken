#!/usr/bin/env python3
"""Run nvoken's profile-selectable production-readiness conformance gate."""

from __future__ import annotations

import argparse
import base64
import dataclasses
import datetime as dt
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
from typing import Iterable


ROOT = Path(__file__).resolve().parents[1]
MATRIX = ROOT / "docs/testing/production-readiness-profiles.md"
PROFILES = ("single_daemon", "google_cloud")
DIMENSION_ORDER = (
    "Installation",
    "Normal execution",
    "Process/dependency failure",
    "Upgrade/rollback",
    "Backup/restore",
    "Diagnosis",
    "Capacity",
    "Retention",
    "Secret handling",
)
DIMENSIONS = set(DIMENSION_ORDER)
GOOGLE_SCENARIO_ROWS = {
    "baseline": ("Installation", "Normal execution", "Secret handling"),
    "queue-control": ("Process/dependency failure",),
    "delivery-control": ("Process/dependency failure",),
    "backlog": ("Capacity",),
    "revision-replacement": ("Process/dependency failure",),
    "redis": ("Process/dependency failure",),
    "callback": ("Normal execution",),
    "alert": ("Diagnosis",),
}
MISSING = {"", "-", "--", "---", "none", "missing", "n/a", "not recorded", "—"}


@dataclasses.dataclass
class CheckResult:
    name: str
    status: str
    detail: str
    rows: tuple[str, ...] = ()


@dataclasses.dataclass
class EvidenceResult:
    dimension: str
    status: str
    freshness: str
    evidence: str | None
    tested_revision: str | None
    sensitive_paths: list[str]
    invalidated: bool
    changed_paths: list[str]


def run(command: list[str], *, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        cwd=ROOT,
        env=env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        check=False,
    )


def git(*args: str) -> subprocess.CompletedProcess[str]:
    return run(["git", *args])


def clean_cell(value: str) -> str:
    value = re.sub(r"\*\*([^*]+)\*\*", r"\1", value.strip())
    return value.replace("<br>", " ").strip()


def split_row(line: str) -> list[str]:
    return [clean_cell(cell) for cell in line.strip().strip("|").split("|")]


def table_after_heading(text: str, heading: str) -> list[dict[str, str]]:
    lines = text.splitlines()
    try:
        start = lines.index(heading)
    except ValueError as error:
        raise ValueError(f"missing matrix heading {heading!r}") from error
    header_index = next(
        (index for index in range(start + 1, len(lines)) if lines[index].lstrip().startswith("|")),
        None,
    )
    if header_index is None or header_index + 1 >= len(lines):
        raise ValueError(f"missing table after {heading!r}")
    headers = split_row(lines[header_index])
    rows: list[dict[str, str]] = []
    for line in lines[header_index + 2 :]:
        if not line.lstrip().startswith("|"):
            break
        cells = split_row(line)
        if len(cells) != len(headers):
            raise ValueError(f"malformed table row after {heading!r}")
        rows.append(dict(zip(headers, cells, strict=True)))
    return rows


def profile_section(text: str, profile: str) -> str:
    marker = f"### `{profile}`"
    try:
        section = text.split(marker, 1)[1]
    except IndexError as error:
        raise ValueError(f"missing profile section {profile!r}") from error
    return marker + section.split("\n### `", 1)[0]


def parse_matrix(text: str) -> tuple[dict[str, str], dict[str, dict[str, dict[str, str]]]]:
    status_rows = table_after_heading(text, "## Current profile status")
    claims = {
        row["Profile"].strip("`"): row["Status"].lower()
        for row in status_rows
    }
    if set(claims) != set(PROFILES) or not set(claims.values()).issubset({"pending", "ready"}):
        raise ValueError("profile status rows must use the readiness-v1 profiles and claims")
    profiles: dict[str, dict[str, dict[str, str]]] = {}
    for profile in PROFILES:
        section = profile_section(text, profile)
        evidence_rows = table_after_heading(section, f"### `{profile}`")
        rows = {row["Dimension"]: row for row in evidence_rows}
        if set(rows) != DIMENSIONS:
            raise ValueError(f"{profile} must contain the nine readiness-v1 dimensions")
        for dimension, row in rows.items():
            if row["Mode"].lower() not in {"automated", "manual"}:
                raise ValueError(f"{profile}/{dimension} has an invalid evidence mode")
            if row["State"].lower() not in {"pending", "proven"}:
                raise ValueError(f"{profile}/{dimension} has an invalid evidence state")
            if row["Freshness"].lower() not in {"missing", "stale", "current"}:
                raise ValueError(f"{profile}/{dimension} has invalid evidence freshness")
        profiles[profile] = {
            "rows": rows,
            "manual": {},
        }
        try:
            manual_rows = table_after_heading(section, "#### Manual evidence freshness")
        except ValueError:
            manual_rows = []
        profiles[profile]["manual"] = {
            row["Dimension"]: row for row in manual_rows
        }
    return claims, profiles


def inline_codes(value: str) -> list[str]:
    return re.findall(r"`([^`]+)`", value)


def markdown_link(value: str) -> str | None:
    match = re.search(r"\[[^]]+\]\(([^)#]+)(?:#[^)]+)?\)", value)
    return match.group(1) if match else None


def is_missing(value: str) -> bool:
    return value.strip().lower() in MISSING


def environment_file(path: Path) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw_line in path.read_text().splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        key, separator, value = line.partition("=")
        if not separator or not key or key.strip() != key or key in values:
            raise ValueError(f"invalid environment example line: {raw_line!r}")
        values[key] = value
    return values


def diagnostic_environment(database_url: str) -> dict[str, str]:
    environment = os.environ.copy()
    config_source = (ROOT / "cmd/nvokend/config.go").read_text()
    config_keys = set(re.findall(r'env:"([A-Z0-9_]+)"', config_source))
    # Mask ambient values and .env entries, then use the checked profile example
    # as the source of safe defaults. The Go profile test keeps this example in
    # sync when a new supported setting is added.
    for key in config_keys:
        environment[key] = ""
    environment.update(environment_file(ROOT / "deploy/single-daemon/nvoken.env.example"))
    environment.update({
        "DATABASE_URL": database_url,
        "BOOTSTRAP_OWNER_SECRET": "readiness-diagnostic-placeholder-0000",
        "CREDENTIAL_DELIVERY_KEY": base64.urlsafe_b64encode(bytes(32)).decode().rstrip("="),
        "ANTHROPIC_API_KEY": "readiness-diagnostic-placeholder",
    })
    return environment


def google_live_rows(live_args: list[str]) -> tuple[str, ...]:
    scenarios: list[str] = []
    for index, argument in enumerate(live_args):
        if argument == "--scenario" and index + 1 < len(live_args):
            scenarios.append(live_args[index + 1])
        elif argument.startswith("--scenario="):
            scenarios.append(argument.split("=", 1)[1])

    selected = scenarios or list(GOOGLE_SCENARIO_ROWS)
    if "all" in selected or any(item not in GOOGLE_SCENARIO_ROWS for item in selected):
        selected = list(GOOGLE_SCENARIO_ROWS)
    related = {
        dimension
        for scenario in selected
        for dimension in GOOGLE_SCENARIO_ROWS[scenario]
    }
    return tuple(dimension for dimension in DIMENSION_ORDER if dimension in related)


def validate_evidence_record(
    path: Path, profile: str, dimension: str, revision: str
) -> list[str]:
    try:
        record_rows = table_after_heading(path.read_text(), "## Record")
        fields = {row["Field"]: row["Value"] for row in record_rows}
    except (KeyError, ValueError):
        return [f"{profile}/{dimension}: evidence record metadata is malformed"]
    required = {"Profile", "Tested revision", "Dimensions", "Result"}
    if not required.issubset(fields):
        return [f"{profile}/{dimension}: evidence record metadata is incomplete"]
    problems = []
    if fields["Profile"].strip("`") != profile:
        problems.append("profile")
    if fields["Tested revision"].strip("`") != revision:
        problems.append("tested revision")
    if dimension not in inline_codes(fields["Dimensions"]):
        problems.append("dimensions")
    if fields["Result"].strip("`").lower() != "pass":
        problems.append("result")
    return [
        f"{profile}/{dimension}: evidence record disagrees on {', '.join(problems)}"
    ] if problems else []


def revision_exists(revision: str) -> bool:
    return git("cat-file", "-e", f"{revision}^{{commit}}").returncode == 0


def revision_is_ancestor(revision: str) -> bool:
    return git("merge-base", "--is-ancestor", revision, "HEAD").returncode == 0


def paths_changed(revision: str, paths: list[str]) -> list[str]:
    if not paths:
        return []
    result = git("diff", "--name-only", f"{revision}..HEAD", "--", *paths)
    if result.returncode != 0:
        return ["<git-diff-failed>"]
    return [line for line in result.stdout.splitlines() if line]


def evaluate_manual_evidence(
    profile: str,
    rows: dict[str, dict[str, str]],
    metadata: dict[str, dict[str, str]],
) -> tuple[dict[str, EvidenceResult], list[str]]:
    results: dict[str, EvidenceResult] = {}
    errors: list[str] = []
    manual_dimensions = {
        dimension for dimension, row in rows.items() if row["Mode"].lower() == "manual"
    }
    if set(metadata) != manual_dimensions:
        errors.append(f"{profile}: manual evidence metadata does not match the manual matrix rows")

    for dimension in sorted(manual_dimensions):
        meta = metadata.get(dimension)
        if meta is None:
            results[dimension] = EvidenceResult(
                dimension, "pending", "missing", None, None, [], False, []
            )
            continue
        evidence_value = meta["Latest evidence"]
        revision_value = meta["Tested revision"].strip("`").strip()
        revision_is_exact = re.fullmatch(r"[0-9a-f]{40}", revision_value) is not None
        sensitive_paths = inline_codes(meta["Evidence-sensitive paths"])
        invalidated = not is_missing(meta["Explicit invalidation"])
        if not sensitive_paths:
            errors.append(f"{profile}/{dimension}: evidence-sensitive paths are missing")
        link = markdown_link(evidence_value)
        evidence_path: Path | None = None
        if link is not None:
            evidence_path = (MATRIX.parent / link).resolve()
            try:
                evidence_path.relative_to((ROOT / "docs/testing/readiness/evidence").resolve())
            except ValueError:
                errors.append(f"{profile}/{dimension}: evidence link is outside the evidence directory")
                evidence_path = None

        record_errors: list[str] = []
        if evidence_path and evidence_path.is_file() and not is_missing(revision_value):
            if not revision_is_exact:
                record_errors.append(
                    f"{profile}/{dimension}: tested revision is not a full Git commit"
                )
            record_errors = validate_evidence_record(
                evidence_path, profile, dimension, revision_value
            ) + record_errors
            errors.extend(record_errors)
        missing = (
            link is None
            or evidence_path is None
            or not evidence_path.is_file()
            or is_missing(revision_value)
            or not revision_is_exact
            or bool(record_errors)
        )
        changed: list[str] = []
        if missing:
            status, freshness = "pending", "missing"
        elif not revision_exists(revision_value) or not revision_is_ancestor(revision_value):
            status, freshness = "pending", "stale"
        else:
            changed = paths_changed(revision_value, sensitive_paths)
            if invalidated or changed:
                status, freshness = "pending", "stale"
            else:
                status, freshness = "proven", "current"

        results[dimension] = EvidenceResult(
            dimension=dimension,
            status=status,
            freshness=freshness,
            evidence=str(evidence_path.relative_to(ROOT.resolve())) if evidence_path else None,
            tested_revision=None if is_missing(revision_value) else revision_value,
            sensitive_paths=sensitive_paths,
            invalidated=invalidated,
            changed_paths=changed,
        )
        recorded_state = rows[dimension]["State"].lower()
        recorded_freshness = rows[dimension]["Freshness"].lower()
        if recorded_state == "proven" and status != "proven":
            errors.append(f"{profile}/{dimension}: recorded proven evidence is {freshness}")
        if recorded_freshness == "current" and freshness != "current":
            errors.append(f"{profile}/{dimension}: recorded current evidence is {freshness}")
    return results, errors


def parse_expected_facts(text: str) -> dict[str, str]:
    return {
        row["Fact"].strip("`"): row["Expected value"]
        for row in table_after_heading(text, "## Checked repository facts")
    }


def yaml_schema_block(text: str, schema: str) -> str:
    match = re.search(
        rf"^    {re.escape(schema)}:\n(?P<body>.*?)(?=^    [A-Za-z][A-Za-z0-9_]*:\n|\Z)",
        text,
        flags=re.MULTILINE | re.DOTALL,
    )
    if match is None:
        raise ValueError(f"OpenAPI schema {schema!r} is missing")
    return match.group("body")


def comma_values(value: str) -> set[str]:
    codes = inline_codes(value)
    if codes:
        return set(codes)
    return {item.strip() for item in value.split(",") if item.strip()}


def singleton_schema_values(schema_block: str) -> set[str]:
    """Read equivalent OpenAPI const and single-value enum declarations."""
    constants = re.findall(
        r"^\s+const:\s*([A-Za-z0-9_.-]+)\s*$",
        schema_block,
        re.MULTILINE,
    )
    enums = re.findall(
        r"^\s+enum:\s*\[\s*([A-Za-z0-9_.-]+)\s*]\s*$",
        schema_block,
        re.MULTILINE,
    )
    return set(constants + enums)


def check_repository_facts(matrix_text: str, claims: dict[str, str]) -> list[str]:
    expected = parse_expected_facts(matrix_text)
    errors: list[str] = []
    required_facts = {
        "profile_names",
        "provider_registry",
        "openapi_tool_modes",
        "openapi_version",
        "migration_head",
        "readiness_links",
    }
    if set(expected) != required_facts:
        errors.append("checked fact rows do not match the readiness-v1 fact set")
        return errors

    if set(claims) != comma_values(expected["profile_names"]):
        errors.append("profile_names")

    generator = (ROOT / "internal/adapters/divegen/generator.go").read_text()
    try:
        new_model = generator.split("func newModel", 1)[1].split("\nfunc ", 1)[0]
        providers = set(re.findall(r'case\s+"([^"]+)"', new_model))
    except IndexError:
        providers = set()
    openapi = (ROOT / "openapi/runtime.yaml").read_text()
    try:
        provider_block = yaml_schema_block(openapi, "ModelProvider")
        provider_contract_open = (
            re.search(r"^\s+type:\s+string\s*$", provider_block, re.MULTILINE) is not None
            and re.search(
                r'^\s+pattern:\s+["\']\^\[a-z]\[a-z0-9_]\*\$["\']\s*$',
                provider_block,
                re.MULTILINE,
            ) is not None
            and re.search(r"^\s+enum:", provider_block, re.MULTILINE) is None
        )
    except ValueError:
        provider_contract_open = False
    wanted_providers = comma_values(expected["provider_registry"])
    readme = (ROOT / "README.md").read_text()
    header_match = re.search(r"<sub>Works with&nbsp;\*\*(.*?)\*\*</sub>", readme)
    readme_providers = {
        item.strip().lower() for item in header_match.group(1).split("·")
    } if header_match else set()
    runtime = (ROOT / "internal/services/runtime.go").read_text()
    admission_rejects_uninstalled = (
        "CanonicalModelProvider(input.Spec.Model.Provider)" in runtime
        and "spec.model.provider is not supported." in runtime
    )
    if (
        providers != wanted_providers
        or not provider_contract_open
        or not admission_rejects_uninstalled
        or readme_providers != wanted_providers
    ):
        errors.append("provider_registry")

    try:
        tool_block = "\n".join([
            yaml_schema_block(openapi, "HostToolSpec"),
            yaml_schema_block(openapi, "CallbackToolSpec"),
        ])
        tool_modes = singleton_schema_values(tool_block)
    except ValueError:
        tool_modes = set()
    wanted_modes = comma_values(expected["openapi_tool_modes"])
    admission_guide = (ROOT / "docs/guides/runtime-admission.md").read_text()
    guide_modes = set(re.findall(r'mode:\s*"(host|callback)"', admission_guide))
    if tool_modes != wanted_modes or not wanted_modes.issubset(guide_modes):
        errors.append("openapi_tool_modes")

    version_match = re.search(r"^info:\n(?:  .*\n)*?  version:\s*([^\s]+)", openapi, re.MULTILINE)
    actual_version = version_match.group(1) if version_match else ""
    if actual_version != expected["openapi_version"].strip("`"):
        errors.append("openapi_version")

    migration_versions = [
        path.name.split("_", 1)[0]
        for path in (ROOT / "internal/adapters/postgres/migrations").glob("*.up.sql")
    ]
    migration_head = max(migration_versions, default="")
    migrations_guide = (ROOT / "internal/adapters/postgres/migrations/README.md").read_text()
    wanted_head = expected["migration_head"].strip("`")
    if migration_head != wanted_head or f"`{wanted_head}`" not in migrations_guide:
        errors.append("migration_head")

    link_targets = {
        "README.md": "docs/testing/production-readiness-profiles.md",
        "docs/design/architecture.md": "../testing/production-readiness-profiles.md",
        "docs/guides/runtime-admission.md": "../testing/production-readiness-profiles.md",
        "deploy/google-cloud/README.md": "../../docs/testing/production-readiness-profiles.md",
    }
    if set(inline_codes(expected["readiness_links"])) != set(link_targets):
        errors.append("readiness_links")
    elif any(target not in (ROOT / source).read_text() for source, target in link_targets.items()):
        errors.append("readiness_links")
    return errors


def command_check(
    name: str,
    command: list[str],
    *,
    rows: Iterable[str] = (),
    env: dict[str, str] | None = None,
) -> CheckResult:
    result = run(command, env=env)
    if result.returncode == 0:
        return CheckResult(name, "pass", "completed", tuple(rows))
    return CheckResult(name, "fail", f"exited with status {result.returncode}", tuple(rows))


def google_qualification_check(command: list[str], rows: tuple[str, ...]) -> CheckResult:
    result = run(command)
    if result.returncode == 0:
        return CheckResult("live_smoke", "pass", "completed", rows)

    detail = f"exited with status {result.returncode}"
    scenarios = re.findall(r"^Running scenario: ([a-z-]+)$", result.stdout, re.MULTILINE)
    cleanup_failed = "one or more cleanup actions failed" in result.stdout
    if scenarios and not cleanup_failed:
        scenario = scenarios[-1]
        rows = GOOGLE_SCENARIO_ROWS.get(scenario, rows)
        detail = f"scenario {scenario} {detail}"
    return CheckResult("live_smoke", "fail", detail, rows)


def run_checks(profile: str, live: bool, live_args: list[str]) -> list[CheckResult]:
    checks: list[CheckResult] = []
    status = git("status", "--porcelain")
    if status.returncode != 0:
        checks.append(CheckResult("checkout", "fail", "could not inspect Git status"))
    elif status.stdout.strip():
        checks.append(CheckResult("checkout", "fail", "checkout has uncommitted changes"))
    else:
        checks.append(CheckResult("checkout", "pass", "clean"))

    checks.append(command_check("repository", ["make", "check"]))

    database_url = os.environ.get("READINESS_DATABASE_URL", "")
    database_confirmed = os.environ.get("READINESS_DATABASE_DISPOSABLE") == "1"
    if database_url and database_confirmed:
        database_env = os.environ.copy()
        database_env["NVOKEN_TEST_DATABASE_URL"] = database_url
        database_env["DATABASE_URL"] = database_url
        checks.append(command_check(
            "postgres",
            ["make", "test-postgres"],
            rows=("Installation", "Normal execution", "Retention"),
            env=database_env,
        ))
        checks.append(command_check(
            "migration",
            ["go", "run", "./cmd/nvokend", "migrate"],
            rows=("Installation",),
            env=database_env,
        ))
        if profile == "single_daemon":
            checks.append(command_check(
                "diagnostic",
                ["go", "run", "./cmd/nvokend", "diagnose"],
                rows=("Installation", "Secret handling"),
                env=diagnostic_environment(database_url),
            ))
    else:
        detail = "set READINESS_DATABASE_URL and READINESS_DATABASE_DISPOSABLE=1"
        checks.append(CheckResult(
            "postgres", "skip", detail, ("Installation", "Normal execution", "Retention")
        ))
        checks.append(CheckResult("migration", "skip", detail, ("Installation",)))
        if profile == "single_daemon":
            checks.append(CheckResult(
                "diagnostic", "skip", detail, ("Installation", "Secret handling")
            ))

    if profile == "google_cloud":
        checks.append(command_check(
            "deployment",
            ["make", "check-deploy"],
            rows=("Installation", "Secret handling"),
        ))

    if not live:
        rows = (
            ("Normal execution",)
            if profile == "single_daemon"
            else google_live_rows(live_args)
        )
        checks.append(CheckResult(
            "live_smoke", "skip", "live checks were not explicitly enabled", rows
        ))
    elif profile == "single_daemon":
        smoke = ROOT / "deploy/single-daemon/smoke.py"
        if smoke.is_file():
            checks.append(command_check(
                "live_smoke",
                [sys.executable, str(smoke), "run"],
                rows=("Normal execution",),
            ))
        else:
            checks.append(CheckResult(
                "live_smoke",
                "fail",
                "single-daemon smoke entry point is missing",
                ("Normal execution",),
            ))
    else:
        qualify = ROOT / "deploy/google-cloud/qualify.py"
        rows = google_live_rows(live_args)
        if qualify.is_file():
            checks.append(google_qualification_check(
                [sys.executable, str(qualify), *live_args],
                rows,
            ))
        else:
            checks.append(CheckResult(
                "live_smoke",
                "fail",
                "Google qualification entry point is missing",
                rows,
            ))
    return checks


def build_rows(
    matrix_rows: dict[str, dict[str, str]],
    manual: dict[str, EvidenceResult],
    checks: list[CheckResult],
) -> list[dict[str, str]]:
    results: list[dict[str, str]] = []
    for dimension, row in matrix_rows.items():
        status = row["State"].lower()
        freshness = row["Freshness"].lower()
        if row["Mode"].lower() == "manual" and dimension in manual:
            status = manual[dimension].status
            freshness = manual[dimension].freshness
        if status == "proven" and freshness == "current":
            related = [check for check in checks if dimension in check.rows]
            if any(check.status == "fail" for check in related):
                status, freshness = "pending", "missing"
        results.append({
            "dimension": dimension,
            "mode": row["Mode"].lower(),
            "status": status,
            "freshness": freshness,
        })
    return results


def write_summary(path: Path, summary: dict[str, object]) -> None:
    path = path.expanduser().resolve()
    path.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.NamedTemporaryFile("w", dir=path.parent, delete=False) as output:
        json.dump(summary, output, indent=2, sort_keys=True)
        output.write("\n")
        temporary = Path(output.name)
    os.replace(temporary, path)


def print_summary(summary: dict[str, object]) -> None:
    print("nvoken readiness conformance")
    print(f"profile: {summary['profile']}")
    print(f"revision: {summary['revision']}{' (dirty)' if summary['dirty'] else ''}")
    print(f"schema expectation: {summary['schema_expectation']}")
    print(f"recorded claim: {summary['recorded_claim']}")
    print("checks:")
    for check in summary["checks"]:
        rows = f" [rows: {', '.join(check['rows'])}]" if check["rows"] else ""
        print(f"  {check['status'].upper():4} {check['name']}: {check['detail']}{rows}")
    print("rows:")
    for row in summary["rows"]:
        print(f"  {row['status'].upper():7} {row['dimension']} ({row['freshness']})")
    print(f"result: {summary['result']}")
    print(f"claim gate: {summary['claim_gate']}")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", required=True, choices=PROFILES)
    parser.add_argument("--output", type=Path, help="write the secret-free JSON summary here")
    parser.add_argument("--live", action="store_true", help="explicitly enable the selected live smoke")
    parser.add_argument("live_args", nargs=argparse.REMAINDER, help="arguments passed to Google qualification")
    args = parser.parse_args(argv)
    live_args = args.live_args[1:] if args.live_args[:1] == ["--"] else args.live_args

    matrix_text = MATRIX.read_text()
    try:
        claims, profiles = parse_matrix(matrix_text)
        manual, manual_errors = evaluate_manual_evidence(
            args.profile,
            profiles[args.profile]["rows"],
            profiles[args.profile]["manual"],
        )
        fact_errors = check_repository_facts(matrix_text, claims)
    except (KeyError, ValueError) as error:
        print(f"readiness matrix error: {error}", file=sys.stderr)
        return 1

    checks = run_checks(args.profile, args.live, live_args)
    documentation_errors = manual_errors + fact_errors
    checks.append(CheckResult(
        "documentation",
        "pass" if not documentation_errors else "fail",
        "checked facts and evidence metadata agree" if not documentation_errors
        else f"{len(documentation_errors)} contradiction(s): "
        + ", ".join(sorted({error.split(":", 1)[0] for error in documentation_errors})),
    ))

    revision_result = git("rev-parse", "HEAD")
    revision = revision_result.stdout.strip() if revision_result.returncode == 0 else "unknown"
    migration_versions = [
        path.name.split("_", 1)[0]
        for path in (ROOT / "internal/adapters/postgres/migrations").glob("*.up.sql")
    ]
    schema_expectation = max(migration_versions, default="unknown")
    rows = build_rows(profiles[args.profile]["rows"], manual, checks)
    checks_failed = any(check.status == "fail" for check in checks)
    ready = not checks_failed and all(
        row["status"] == "proven" and row["freshness"] == "current" for row in rows
    )
    result = "ready" if ready else "pending"
    recorded_claim = claims[args.profile]
    claim_violation = recorded_claim == "ready" and result != "ready"
    claim_gate = "reject" if claim_violation else "pass"
    evidence = [dataclasses.asdict(item) for item in manual.values()]
    summary: dict[str, object] = {
        "contract": "readiness-v1",
        "profile": args.profile,
        "revision": revision,
        "dirty": checks[0].status != "pass",
        "schema_expectation": schema_expectation,
        "time": dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z"),
        "checks": [dataclasses.asdict(check) for check in checks],
        "evidence": evidence,
        "rows": rows,
        "recorded_claim": recorded_claim,
        "result": result,
        "claim_gate": claim_gate,
    }
    print_summary(summary)
    if args.output:
        write_summary(args.output, summary)
        print(f"machine summary: {args.output}")
    return 1 if checks_failed or claim_violation else 0


if __name__ == "__main__":
    raise SystemExit(main())
