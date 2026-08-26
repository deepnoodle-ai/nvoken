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

**Parameter parity** is a hard failure for operations the handwritten facade
deliberately wraps. Every query parameter and top-level request-body field in
that operation's facade contract must appear somewhere in the language's
handwritten sources. Exact-only operations and fields are listed explicitly
below because `raw()` is their front door; forcing transport controls into the
workflow facade would defeat the separation this check protects. This remains
a spelling-level check rather than a signature parse. Only top-level fields are
walked; nested shapes are pinned elsewhere.

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

The contract is read with PyYAML, declared in `requirements-dev.txt`.
"""

from __future__ import annotations

import pathlib
import re
import sys

import yaml

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
    # Console device authorization is an integration protocol for the CLI and
    # console host. The generated clients expose it; the product facades do not
    # retype that handshake.
    "approveConsoleDeviceAuthorization",
    "createConsoleDeviceAuthorization",
    "denyConsoleDeviceAuthorization",
    "exchangeConsoleDeviceAuthorization",
    "getConsoleDeviceAuthorization",
    "getUsageBreakdown",
    "getUsageTimeseries",
    "listUsageRecords",
    "listAdmissions",
    "summarizeAdmissions",
    "listApps",
    # Conversation, MemorySpace, and Turn administration stays request-shaped
    # under raw(). The workflow facade binds Conversation selection during
    # admission and exposes a Turn's status, result, and updates; it does not
    # duplicate the complete resource-management APIs.
    "cancelNudge",
    "cancelTurn",
    "createConversation",
    "createNudge",
    "deleteConversation",
    "deleteMemorySpace",
    "deleteTurn",
    "forkConversation",
    "getAgentRevision",
    "getConversation",
    "getConversationTranscript",
    "getMemorySpace",
    "getTrace",
    "getTurn",
    "getTurnTimeline",
    "interruptTurn",
    "listAgentRevisions",
    "listConversationCompactions",
    "listConversationMessages",
    "listConversations",
    "listMemorySpaces",
    "listNudges",
    "listToolCalls",
    "listTurnLogs",
    "listTurnTraces",
    "listTurns",
    "receiveToolCallback",
    "receiveTurnWebhook",
    "resolveMemorySpace",
    "resumeTurn",
    "streamConversation",
    "updateConversation",
}

# These operations back ordinary workflow methods, but the facade deliberately
# omits transport-only controls. Every omitted member remains available under
# raw(). Keep this list tied to the accepted facade rather than using it as a
# general escape hatch for parity failures.
EXACT_ONLY_FIELDS = {
    "createAgent": {
        "client_interface",
        "mcp_servers",
        "provider_tools",
        "reasoning",
        "sampling",
        "tool_choice",
    },
    "createTurn": {
        "context",
        "mcp_server_headers",
        "on_budget_exhausted",
        "provider_keys",
        "triggered_by",
        "webhook",
    },
    "listAgents": {"limit"},
    "publishAgentRevision": {
        "client_interface",
        "mcp_servers",
        "provider_tools",
        "reasoning",
        "sampling",
        "tool_choice",
    },
}

# Operations whose facade takes the generated request type as its parameter
# rather than retyping it. Every field is reachable by construction — the
# caller builds the wire shape — so a spelling search over the facade finds
# none of them and would report a gap that is really a design decision. This
# is deliberately a hand-kept list rather than an inference: a facade that
# merely mentions the type while building it internally is the case the check
# exists to catch, and no spelling search can tell the two apart.
WIRE_SHAPED = {
    ("allocateCredits", "rust"),
    ("createAppClientKey", "typescript"),
    ("createAppClientKey", "python"),
    ("createAppClientKey", "rust"),
    ("createCredential", "rust"),
    ("createProviderKey", "rust"),
    ("mintAppSigningKey", "typescript"),
    ("mintAppSigningKey", "python"),
    ("mintAppSigningKey", "rust"),
    ("registerApp", "typescript"),
    ("registerApp", "python"),
    ("registerApp", "rust"),
    ("registerOrg", "rust"),
    ("rotateCredential", "rust"),
    ("rotateProviderKey", "rust"),
    ("updateApp", "typescript"),
    ("updateApp", "python"),
    ("updateApp", "rust"),
    ("updateOrg", "rust"),
}

# Operations one language wraps and another does not belong here temporarily.
# Keep the baseline empty; a new entry is an explicit debt, not a way to make a
# parity failure pass.
COVERAGE_BASELINE: set[tuple[str, str]] = set()


def load_operations() -> dict[str, list[str]]:
    """Operation ID to the names a caller supplies: query and body alike."""
    spec = yaml.safe_load(CONTRACT.read_text())
    shared = spec.get("components", {}).get("parameters", {})
    schemas = spec.get("components", {}).get("schemas", {})

    def resolve(parameter: dict) -> dict:
        if "$ref" in parameter:
            return shared[parameter["$ref"].rsplit("/", 1)[-1]]
        return parameter

    def dereference(node: object) -> dict:
        depth = 0
        while isinstance(node, dict) and "$ref" in node and depth < 10:
            node = schemas.get(node["$ref"].rsplit("/", 1)[-1], {})
            depth += 1
        return node if isinstance(node, dict) else {}

    def body_fields(operation: dict) -> set[str]:
        """Top-level JSON body properties, through $ref and composition."""
        body = dereference(operation.get("requestBody"))
        schema = body.get("content", {}).get("application/json", {}).get("schema")
        if schema is None:
            return set()
        names: set[str] = set()
        pending = [schema]
        while pending:
            node = dereference(pending.pop())
            names.update(node.get("properties", {}))
            for composition in ("allOf", "oneOf", "anyOf"):
                pending.extend(node.get(composition, []))
        return names

    operations: dict[str, list[str]] = {}
    for item in spec["paths"].values():
        inherited = [resolve(parameter) for parameter in item.get("parameters", [])]
        for method in ("get", "post", "put", "patch", "delete"):
            operation = item.get(method)
            if not operation or "operationId" not in operation:
                continue
            own = [resolve(parameter) for parameter in operation.get("parameters", [])]
            names = {
                parameter["name"]
                for parameter in inherited + own
                if parameter.get("in") == "query"
            }
            names |= body_fields(operation)
            operations[operation["operationId"]] = sorted(names)
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


# Words Go writes in full caps. A facade spelling `MCPServers` does expose
# `mcp_servers`; a checker that only knew `McpServers` would report a gap that
# is really a naming convention, and a reported gap nobody can act on is how a
# check stops being read.
GO_INITIALISMS = {"api", "id", "mcp", "sse", "ttl", "url"}


def spelling(language: str, parameter: str) -> str:
    words = parameter.split("_")
    if language == "go":
        return "".join(
            word.upper() if word.lower() in GO_INITIALISMS else word.capitalize()
            for word in words
        )
    if language == "typescript":
        return words[0] + "".join(word.capitalize() for word in words[1:])
    return parameter


def facade_parameters(operation_id: str, parameters: list[str]) -> list[str]:
    """Return the wire members promised by the handwritten workflow facade."""
    if operation_id in RAW_ONLY:
        return []
    exact_only = EXACT_ONLY_FIELDS.get(operation_id, set())
    return [parameter for parameter in parameters if parameter not in exact_only]


def main() -> int:
    operations = load_operations()
    sources = load_sources()

    parameter_gaps: list[str] = []
    observed_coverage: set[tuple[str, str]] = set()

    for operation_id, parameters in sorted(operations.items()):
        parameters = facade_parameters(operation_id, parameters)
        if not parameters:
            continue
        for language, source in sorted(sources.items()):
            if (operation_id, language) in WIRE_SHAPED:
                continue
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
