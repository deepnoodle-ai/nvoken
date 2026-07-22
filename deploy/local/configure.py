#!/usr/bin/env python3
"""Create the ignored, mode-0600 environment file for local development."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import secrets
import sys


ROOT = Path(__file__).resolve().parents[2]
TEMPLATE = Path(__file__).with_name("nvoken.env.example")
PROVIDER_VARIABLES = {
    "anthropic": "ANTHROPIC_API_KEY",
    "openai": "OPENAI_API_KEY",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate local nvoken secrets and write the ignored root .env file.",
    )
    parser.add_argument("--provider", choices=sorted(PROVIDER_VARIABLES), required=True)
    parser.add_argument("--output", type=Path, default=ROOT / ".env", help=argparse.SUPPRESS)
    parser.add_argument("--force", action="store_true", help="replace an existing output file")
    return parser.parse_args()


def render(template: str, provider: str, environment: dict[str, str]) -> str:
    provider_variable = PROVIDER_VARIABLES[provider]
    provider_key = environment.get(provider_variable, "")
    if not provider_key:
        raise ValueError(f"export {provider_variable} before running this command")

    values = {
        "RUNTIME_API_KEY": secrets.token_hex(32),
        "BOOTSTRAP_OWNER_SECRET": secrets.token_hex(32),
        "CREDENTIAL_DELIVERY_KEY": secrets.token_urlsafe(32),
        provider_variable: provider_key,
    }
    rendered: list[str] = []
    replaced: set[str] = set()
    for line in template.splitlines():
        name, separator, _value = line.partition("=")
        if separator and name in values:
            rendered.append(f"{name}={values[name]}")
            replaced.add(name)
        else:
            rendered.append(line)
    missing = set(values) - replaced
    if missing:
        raise ValueError(f"environment template is missing: {', '.join(sorted(missing))}")
    return "\n".join(rendered) + "\n"


def ambient_provider_warnings(provider: str, environment: dict[str, str]) -> list[str]:
    selected = PROVIDER_VARIABLES[provider]
    return [
        f"warning: {variable} is already exported and will override the empty value in .env; "
        "unset both provider variables before migration and startup"
        for variable in PROVIDER_VARIABLES.values()
        if variable != selected and environment.get(variable)
    ]


def main() -> int:
    args = parse_args()
    output = args.output.resolve()
    if output.exists() and not args.force:
        print(f"refusing to replace existing {output}; move it or pass --force", file=sys.stderr)
        return 2
    try:
        environment = dict(os.environ)
        content = render(TEMPLATE.read_text(encoding="utf-8"), args.provider, environment)
    except ValueError as error:
        print(error, file=sys.stderr)
        return 2

    for warning in ambient_provider_warnings(args.provider, environment):
        print(warning, file=sys.stderr)

    output.parent.mkdir(parents=True, exist_ok=True)
    descriptor = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
        stream.write(content)
    output.chmod(0o600)
    print(f"wrote {output} with generated local secrets and {PROVIDER_VARIABLES[args.provider]}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
