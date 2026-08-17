#!/usr/bin/env python3
"""Assert every hand-written facade exposes every parameter its operation has.

The generated clients are complete by construction: they come from the
contract. The hand-written facades are not, and they are what integrators
actually call. A facade that wraps an operation but drops one of its query
parameters leaves that parameter unreachable through the SDK's front door,
while the changelog, the contract, and the generated client all say it is
there. Nothing else in this repository can see that: the conformance suite
pins wire shapes, and `sdk/operations.json` tracks operation IDs, methods, and
paths. Neither looks inside a facade signature.

Two things are checked, and they fail differently.

**Parameter parity** is a hard failure. If a language wraps an operation, every
query parameter of that operation must appear somewhere in that language's
hand-written sources. This is a spelling-level check rather than a signature
parse: it answers "is this parameter reachable at all", which is the question
that was being answered wrong.

**Facade coverage** is a baseline. Not every operation needs a facade — some
are reporting surfaces callers reach through the generated clients on purpose.
But an operation wrapped in one language and not another is an accident far
more often than a decision, so the known set is recorded below and the check
fails if it grows. Shrink it by writing the facade; never widen it to make a
run pass without saying why.

The parameter test is deliberately a spelling search over each language's
hand-written sources rather than a signature parse. Four languages give a
parameter four shapes and a facade may take it as a field, an argument, or a
builder call; what none of those can do is never mention it at all.

The contract is read with the standard library, for the reason
`scripts/check_go_frame_keys.py` gives: these checks run on a bare Python and
install nothing.
"""

from __future__ import annotations

import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
CONTRACT = ROOT / "openapi" / "nvoken.yaml"

# Hand-written SDK sources per language: the surface an integrator reads.
# Generated trees are excluded because they are complete by construction, and
# tests because a parameter named only by a test is not reachable.
SOURCES = {
    "go": ("sdk/go", ".go", ("generated",), ("_test.go",)),
    "typescript": ("sdk/typescript/src", ".ts", ("generated", "test", "examples"), (".test.ts",)),
    "python": ("sdk/python/src/nvoken", ".py", (), ("_test.py",)),
    "rust": ("sdk/rust/src", ".rs", ("apis", "models"), ()),
}

# Operations no facade wraps in any language. These are reached through the
# generated clients on `Client.raw()` deliberately: they are reporting and
# diagnostic surfaces with wide filter sets that a facade would only retype.
RAW_ONLY = {
    "getUsageBreakdown",
    "getUsageTimeseries",
    "listUsageRecords",
    "listAdmissions",
    "summarizeAdmissions",
    "listApps",
}

# Operations one language wraps and another does not. Every line here is a
# facade someone still has to write; the check exists so the list shrinks
# rather than grows.
COVERAGE_BASELINE = {
    ("getSessionTranscript", "rust"),
    ("listInvocationLogs", "typescript"),
    ("listInvocationLogs", "python"),
    ("listInvocationLogs", "rust"),
    ("listMemories", "typescript"),
    ("listMemories", "python"),
    ("listMemories", "rust"),
}


# Line shapes in the one document this repository owns and lints. This is a
# reader for those shapes, not a YAML parser: it refuses anything it does not
# recognize rather than reporting an operation as having no parameters, because
# "no query parameters" and "I could not tell" must not look the same here. A
# facade check that silently sees nothing to check is worse than no check.
SHARED_ENTRY = re.compile(r"^    ([A-Za-z][A-Za-z0-9]*):$")
SHARED_NAME = re.compile(r"^      name:\s*([A-Za-z0-9_-]+)\s*$")
SHARED_IN = re.compile(r"^      in:\s*([a-z]+)\s*$")
PATH_ITEM = re.compile(r"^  (/\S*):$")
METHOD = re.compile(r"^    (get|post|put|patch|delete):$")
OPERATION_ID = re.compile(r"^      operationId:\s*([A-Za-z][A-Za-z0-9]*)\s*$")
PARAMETER_REF = re.compile(
    r'^\s*-\s*\{?\s*\$ref:\s*"#/components/parameters/([A-Za-z0-9]+)"\s*\}?\s*$'
)
PARAMETER_NAME = re.compile(r"^\s*-\s*name:\s*([A-Za-z0-9_-]+)\s*$")
FIELD_IN = re.compile(r"^in:\s*([a-z]+)\s*$")


def shared_parameters(lines: list[str]) -> dict[str, tuple[str, str]]:
    """`components.parameters` by key, as (name, location)."""
    shared: dict[str, list[str | None]] = {}
    inside = False
    key: str | None = None
    for line in lines:
        if re.match(r"^  \S", line):
            inside = line.startswith("  parameters:")
            key = None
            continue
        if not inside:
            continue
        entry = SHARED_ENTRY.match(line)
        if entry is not None:
            key = entry.group(1)
            shared[key] = [None, None]
            continue
        if key is None:
            continue
        name = SHARED_NAME.match(line)
        if name is not None:
            shared[key][0] = name.group(1)
            continue
        location = SHARED_IN.match(line)
        if location is not None:
            shared[key][1] = location.group(1)
    if not shared:
        raise SystemExit(f"{CONTRACT.name}: no shared parameters found")
    resolved: dict[str, tuple[str, str]] = {}
    for key, (name, location) in shared.items():
        if name is None or location is None:
            raise SystemExit(f"{CONTRACT.name}: shared parameter {key} has no name or `in`")
        resolved[key] = (name, location)
    return resolved


def block_end(lines: list[str], start: int, indent: int) -> int:
    """The line after the block opened at `start`, by indentation."""
    index = start + 1
    while index < len(lines):
        line = lines[index]
        if line.strip() and len(line) - len(line.lstrip()) <= indent:
            break
        index += 1
    return index


def query_parameters(
    lines: list[str],
    start: int,
    indent: int,
    shared: dict[str, tuple[str, str]],
    where: str,
) -> list[str]:
    """The query parameter names in the list opened by `parameters:` at `start`."""
    item_indent = indent + 2
    items: list[list[str]] = []
    for index in range(start + 1, block_end(lines, start, indent)):
        line = lines[index]
        if not line.strip():
            continue
        here = len(line) - len(line.lstrip())
        if here == item_indent and line.lstrip().startswith("- "):
            items.append([line])
        elif not items:
            raise SystemExit(f"{where}: parameter list opens with {line.strip()!r}")
        else:
            items[-1].append(line)
    if not items:
        raise SystemExit(f"{where}: empty parameters list")

    names: list[str] = []
    for item in items:
        head = item[0]
        reference = PARAMETER_REF.match(head)
        if reference is not None:
            key = reference.group(1)
            if key not in shared:
                raise SystemExit(f"{where}: $ref to unknown shared parameter {key}")
            name, location = shared[key]
            if location == "query":
                names.append(name)
            continue
        inline = PARAMETER_NAME.match(head)
        if inline is None:
            raise SystemExit(
                f"{where}: parameter item this reader cannot read: {head.strip()!r}"
            )
        # `in` is required by OpenAPI, and it sits at the item's own field
        # indent. Matching the indent exactly keeps a nested `schema:` from
        # answering for the parameter.
        field_indent = len(head) - len(head.lstrip()) + 2
        location = None
        for line in item[1:]:
            if len(line) - len(line.lstrip()) != field_indent:
                continue
            field = FIELD_IN.match(line.strip())
            if field is not None:
                location = field.group(1)
                break
        if location is None:
            raise SystemExit(f"{where}: parameter {inline.group(1)} has no `in`")
        if location == "query":
            names.append(inline.group(1))
    return names


def load_operations(text: str) -> dict[str, list[str]]:
    """Operation ID to its query parameter names, path-level ones included."""
    lines = text.splitlines()
    shared = shared_parameters(lines)

    operations: dict[str, list[str]] = {}
    inside = False
    path: str | None = None
    inherited: list[str] = []
    for index, line in enumerate(lines):
        if re.match(r"^\S", line):
            inside = line.startswith("paths:")
            path = None
            continue
        if not inside:
            continue
        item = PATH_ITEM.match(line)
        if item is not None:
            path = item.group(1)
            inherited = []
            continue
        if path is None:
            continue
        if line == "    parameters:":
            inherited = query_parameters(lines, index, 4, shared, path)
            continue
        if METHOD.match(line) is None:
            continue
        end = block_end(lines, index, 4)
        operation_id: str | None = None
        own_at: int | None = None
        for cursor in range(index + 1, end):
            matched = OPERATION_ID.match(lines[cursor])
            if matched is not None:
                operation_id = matched.group(1)
            elif lines[cursor] == "      parameters:":
                own_at = cursor
        if operation_id is None:
            continue
        own: list[str] = []
        if own_at is not None:
            own = query_parameters(lines, own_at, 6, shared, operation_id)
        operations[operation_id] = sorted(set(inherited) | set(own))
    if not operations:
        raise SystemExit(f"{CONTRACT.name}: no operations found")
    return operations


LINE_COMMENTS = {"go": "//", "typescript": "//", "rust": "//", "python": "#"}
TRIPLE_QUOTED = re.compile(r'"""(?:.|\n)*?"""' + "|'''(?:.|\n)*?'''")
BLOCK_COMMENT = re.compile(r"/\*(?:.|\n)*?\*/")


def strip_prose(language: str, text: str) -> str:
    """Remove comments and docstrings, keeping code.

    A facade that documents a parameter it never forwards is precisely the
    defect this check exists for, so prose must not be able to satisfy it.
    Ordinary string literals are left alone: a parameter named in one is
    usually a query key being built, which is a real use.
    """
    if language == "python":
        text = TRIPLE_QUOTED.sub('""', text)
    else:
        text = BLOCK_COMMENT.sub(" ", text)
    marker = LINE_COMMENTS[language]
    return "\n".join(line.split(marker)[0] for line in text.splitlines())


def load_sources() -> dict[str, str]:
    sources: dict[str, str] = {}
    for language, (root, suffix, skip_dirs, skip_files) in SOURCES.items():
        chunks = []
        for path in sorted((ROOT / root).rglob("*" + suffix)):
            parts = path.relative_to(ROOT / root).parts
            if any(part in skip_dirs for part in parts):
                continue
            if path.name.endswith(skip_files):
                continue
            chunks.append(strip_prose(language, path.read_text()))
        sources[language] = "\n".join(chunks)
    return sources


def spelling(language: str, parameter: str) -> str:
    words = parameter.split("_")
    if language == "go":
        return "".join("ID" if word.lower() == "id" else word.capitalize() for word in words)
    if language == "typescript":
        return words[0] + "".join(word.capitalize() for word in words[1:])
    return parameter


def main() -> int:
    operations = load_operations(CONTRACT.read_text())
    sources = load_sources()

    parameter_gaps: list[str] = []
    observed_coverage: set[tuple[str, str]] = set()

    for operation_id, parameters in sorted(operations.items()):
        if operation_id in RAW_ONLY or not parameters:
            continue
        for language, source in sorted(sources.items()):
            missing = [
                parameter
                for parameter in parameters
                if not re.search(
                    r"\b" + re.escape(spelling(language, parameter)) + r"\b", source
                )
            ]
            if not missing:
                continue
            if (operation_id, language) in COVERAGE_BASELINE:
                observed_coverage.add((operation_id, language))
                continue
            parameter_gaps.append(
                f"{operation_id}: the {language} facade never names "
                f"{', '.join(missing)}."
            )

    failed = False
    if parameter_gaps:
        failed = True
        print("Facade parameter gaps:", file=sys.stderr)
        for gap in parameter_gaps:
            print(f"  {gap}", file=sys.stderr)
        print(
            "\nEach one is a parameter an integrator cannot reach through the "
            "SDK's front door.\nExpose it, or record the missing facade in "
            "COVERAGE_BASELINE with the reason.",
            file=sys.stderr,
        )

    resolved = COVERAGE_BASELINE - observed_coverage
    if resolved:
        failed = True
        print(
            "\nFacade coverage improved. Remove these from COVERAGE_BASELINE:",
            file=sys.stderr,
        )
        for operation_id, language in sorted(resolved):
            print(f"  {operation_id} / {language}", file=sys.stderr)

    if failed:
        return 1
    checked = len(operations) - len(RAW_ONLY)
    print(
        f"facade parity: {checked} operations checked, "
        f"{len(COVERAGE_BASELINE)} facades still to write"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
