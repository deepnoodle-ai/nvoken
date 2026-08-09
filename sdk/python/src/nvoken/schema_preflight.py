from __future__ import annotations

import json
import math
from dataclasses import dataclass
from typing import Any

MAX_OUTPUT_SCHEMA_BYTES = 32 * 1024
MAX_OUTPUT_SCHEMA_DEPTH = 16
MAX_OUTPUT_PATTERN_BYTES = 1024
SCHEMA_PREFLIGHT_CODE = "schema_preflight_failed"

_ALLOWED_KEYWORDS = {
    "type",
    "title",
    "description",
    "properties",
    "required",
    "additionalProperties",
    "items",
    "enum",
    "pattern",
    "minLength",
    "maxLength",
    "minItems",
    "maxItems",
    "uniqueItems",
    "minimum",
    "maximum",
}


@dataclass(frozen=True)
class SchemaIssue:
    code: str
    path: str
    message: str
    keyword: str | None = None


def output_schema_issue(schema: dict[str, Any]) -> SchemaIssue | None:
    try:
        compact = json.dumps(
            schema,
            separators=(",", ":"),
            ensure_ascii=False,
            allow_nan=False,
        ).encode()
    except (TypeError, ValueError):
        return SchemaIssue(
            code="invalid_schema",
            path="",
            message="schema must be JSON serializable",
        )
    if len(compact) > MAX_OUTPUT_SCHEMA_BYTES:
        return SchemaIssue(
            code="limit_exceeded",
            path="",
            message=f"compact schema exceeds {MAX_OUTPUT_SCHEMA_BYTES} bytes",
        )
    return _validate_schema_node(schema, "", 1, True)


def _validate_schema_node(
    node: dict[str, Any],
    path: str,
    depth: int,
    root: bool,
) -> SchemaIssue | None:
    if depth > MAX_OUTPUT_SCHEMA_DEPTH:
        return SchemaIssue(
            code="limit_exceeded",
            path=path,
            message=(
                "schema exceeds the maximum nesting depth of "
                f"{MAX_OUTPUT_SCHEMA_DEPTH}"
            ),
        )
    if not all(isinstance(keyword, str) for keyword in node):
        return SchemaIssue(
            code="invalid_schema",
            path=path,
            message="schema member names must be strings",
        )
    for keyword in sorted(node):
        if keyword not in _ALLOWED_KEYWORDS:
            return _member_issue(
                "unsupported_keyword",
                path,
                keyword,
                f"unsupported schema keyword {keyword!r}",
            )
    type_name = node.get("type")
    if not isinstance(type_name, str) or type_name not in {
        "object",
        "array",
        "string",
        "number",
        "integer",
        "boolean",
    }:
        return _member_issue(
            "invalid_schema",
            path,
            "type",
            "every schema position requires one supported string type",
        )
    if root and type_name != "object":
        return _member_issue(
            "invalid_schema",
            path,
            "type",
            "schema root type must be object",
        )
    for keyword in ("description", "title"):
        if keyword in node and not isinstance(node[keyword], str):
            return _member_issue(
                "invalid_schema",
                path,
                keyword,
                f"schema {keyword} must be a string",
            )
    for keyword in ("maxItems", "maxLength", "minItems", "minLength"):
        if keyword in node and not _nonnegative_integer(node[keyword]):
            return _member_issue(
                "invalid_schema",
                path,
                keyword,
                f"schema {keyword} must be a nonnegative integer",
            )
    for keyword in ("maximum", "minimum"):
        if keyword in node and not _json_number(node[keyword]):
            return _member_issue(
                "invalid_schema",
                path,
                keyword,
                f"schema {keyword} must be a number",
            )
    if "uniqueItems" in node and not isinstance(node["uniqueItems"], bool):
        return _member_issue(
            "invalid_schema",
            path,
            "uniqueItems",
            "schema uniqueItems must be a boolean",
        )
    bound_issue = _validate_bound_order(node, path)
    if bound_issue:
        return bound_issue
    if type_name != "string":
        keyword = _first_present(node, "maxLength", "minLength", "pattern")
        if keyword:
            return _member_issue(
                "invalid_schema",
                path,
                keyword,
                "string schema keywords require type string",
            )
    if type_name != "array":
        keyword = _first_present(node, "maxItems", "minItems", "uniqueItems")
        if keyword:
            return _member_issue(
                "invalid_schema",
                path,
                keyword,
                "array schema bounds require type array",
            )
    if type_name not in {"number", "integer"}:
        keyword = _first_present(node, "maximum", "minimum")
        if keyword:
            return _member_issue(
                "invalid_schema",
                path,
                keyword,
                "numeric schema keywords require type number or integer",
            )
    if root and "enum" in node:
        return _member_issue(
            "invalid_schema",
            path,
            "enum",
            "schema root enum is not supported",
        )
    if "pattern" in node:
        pattern = node["pattern"]
        if not isinstance(pattern, str):
            return _member_issue(
                "invalid_schema",
                path,
                "pattern",
                "schema pattern must be a string",
            )
        if len(pattern.encode()) > MAX_OUTPUT_PATTERN_BYTES:
            return _member_issue(
                "limit_exceeded",
                path,
                "pattern",
                (
                    "schema pattern exceeds the maximum size of "
                    f"{MAX_OUTPUT_PATTERN_BYTES} bytes"
                ),
            )
    if "enum" in node and (
        not isinstance(node["enum"], list) or not node["enum"]
    ):
        return _member_issue(
            "invalid_schema",
            path,
            "enum",
            "schema enum must be a nonempty array",
        )
    has_properties = "properties" in node
    has_required = "required" in node
    has_additional = "additionalProperties" in node
    if (has_properties or has_required or has_additional) and type_name != "object":
        keyword = _first_present(
            node,
            "additionalProperties",
            "properties",
            "required",
        )
        return _member_issue(
            "invalid_schema",
            path,
            keyword or "properties",
            "object schema keywords require type object",
        )
    property_names: set[str] = set()
    if has_properties:
        properties = node["properties"]
        if not isinstance(properties, dict):
            return _member_issue(
                "invalid_schema",
                path,
                "properties",
                "schema properties must be an object",
            )
        if not all(isinstance(name, str) for name in properties):
            return _member_issue(
                "invalid_schema",
                path,
                "properties",
                "schema property names must be strings",
            )
        for name in sorted(properties):
            child = properties[name]
            child_path = _pointer(_pointer(path, "properties"), name)
            if not isinstance(child, dict) or not child:
                return SchemaIssue(
                    code="invalid_schema",
                    path=child_path,
                    message=f"property {name!r} must contain a schema object",
                )
            property_names.add(name)
            child_issue = _validate_schema_node(child, child_path, depth + 1, False)
            if child_issue:
                return child_issue
    if has_required:
        required = node["required"]
        if not isinstance(required, list):
            return _member_issue(
                "invalid_schema",
                path,
                "required",
                "schema required must be an array of property names",
            )
        seen: set[str] = set()
        for index, value in enumerate(required):
            item_path = _pointer(_pointer(path, "required"), str(index))
            if not isinstance(value, str) or not value:
                return SchemaIssue(
                    code="invalid_schema",
                    path=item_path,
                    keyword="required",
                    message="schema required must contain nonempty strings",
                )
            if value in seen:
                return SchemaIssue(
                    code="invalid_schema",
                    path=item_path,
                    keyword="required",
                    message="schema required must not contain duplicates",
                )
            if value not in property_names:
                return SchemaIssue(
                    code="invalid_schema",
                    path=item_path,
                    keyword="required",
                    message=f"required property {value!r} is not declared",
                )
            seen.add(value)
    if has_additional:
        additional = node["additionalProperties"]
        if not isinstance(additional, bool):
            child_path = _pointer(path, "additionalProperties")
            if not isinstance(additional, dict):
                return _member_issue(
                    "invalid_schema",
                    path,
                    "additionalProperties",
                    "additionalProperties must be a boolean or schema object",
                )
            if not additional:
                return SchemaIssue(
                    code="invalid_schema",
                    path=child_path,
                    keyword="additionalProperties",
                    message="additionalProperties must contain a schema object",
                )
            child_issue = _validate_schema_node(
                additional,
                child_path,
                depth + 1,
                False,
            )
            if child_issue:
                return child_issue
    has_items = "items" in node
    if type_name == "array" and not has_items:
        return _member_issue(
            "invalid_schema",
            path,
            "items",
            "array schemas require items",
        )
    if has_items:
        if type_name != "array":
            return _member_issue(
                "invalid_schema",
                path,
                "items",
                "schema items requires type array",
            )
        items = node["items"]
        if not isinstance(items, dict) or not items:
            return _member_issue(
                "invalid_schema",
                path,
                "items",
                "schema items must be a schema object",
            )
        child_issue = _validate_schema_node(
            items,
            _pointer(path, "items"),
            depth + 1,
            False,
        )
        if child_issue:
            return child_issue
    return None


def _validate_bound_order(
    node: dict[str, Any],
    path: str,
) -> SchemaIssue | None:
    for lower, upper, message in (
        ("minLength", "maxLength", "schema minLength must not exceed maxLength"),
        ("minItems", "maxItems", "schema minItems must not exceed maxItems"),
        ("minimum", "maximum", "schema minimum must not exceed maximum"),
    ):
        if (
            _json_number(node.get(lower))
            and _json_number(node.get(upper))
            and node[lower] > node[upper]
        ):
            return _member_issue("invalid_schema", path, upper, message)
    return None


def _json_number(value: Any) -> bool:
    return (
        isinstance(value, (int, float))
        and not isinstance(value, bool)
        and (not isinstance(value, float) or math.isfinite(value))
    )


def _nonnegative_integer(value: Any) -> bool:
    """Accept any nonnegative JSON number whose value is an integer.

    ``160``, ``160.0``, and ``1.6e2`` are the same JSON number, and canonical
    JSON -- which the runtime applies to tool input schemas before compiling
    them -- renders every number in the last of those forms. Reading the
    spelling rather than the value rejected two-digit bounds outright.
    """
    if isinstance(value, bool):
        return False
    if isinstance(value, int):
        return value >= 0
    if isinstance(value, float):
        return math.isfinite(value) and value.is_integer() and value >= 0
    return False


def _first_present(node: dict[str, Any], *keywords: str) -> str | None:
    return next((keyword for keyword in keywords if keyword in node), None)


def _member_issue(
    code: str,
    path: str,
    keyword: str,
    message: str,
) -> SchemaIssue:
    return SchemaIssue(
        code=code,
        path=_pointer(path, keyword),
        keyword=keyword,
        message=message,
    )


def _pointer(path: str, member: str) -> str:
    escaped = member.replace("~", "~0").replace("/", "~1")
    return f"{path}/{escaped}"
