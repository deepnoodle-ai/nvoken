use std::collections::{HashMap, HashSet};

use serde_json::{json, Map, Value};

use crate::client::{ErrorCategory, NvokenError};

pub const MAX_OUTPUT_SCHEMA_BYTES: usize = 32 * 1024;
pub const MAX_OUTPUT_SCHEMA_DEPTH: usize = 16;
pub const MAX_OUTPUT_PATTERN_BYTES: usize = 1024;
pub const SCHEMA_PREFLIGHT_CODE: &str = "schema_preflight_failed";

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SchemaIssue {
    pub code: String,
    pub path: String,
    pub keyword: Option<String>,
    pub message: String,
}

pub fn preflight_output_schema(schema: &HashMap<String, Value>) -> Result<(), NvokenError> {
    let compact = serde_json::to_vec(schema).map_err(|_| {
        schema_error(issue(
            "invalid_schema",
            "",
            None,
            "schema must be JSON serializable",
        ))
    })?;
    if compact.len() > MAX_OUTPUT_SCHEMA_BYTES {
        return Err(schema_error(issue(
            "limit_exceeded",
            "",
            None,
            format!("compact schema exceeds {MAX_OUTPUT_SCHEMA_BYTES} bytes"),
        )));
    }
    let root: Map<String, Value> = schema
        .iter()
        .map(|(key, value)| (key.clone(), value.clone()))
        .collect();
    if let Some(issue) = validate_schema_node(&root, "", 1, true) {
        return Err(schema_error(issue));
    }
    Ok(())
}

fn schema_error(issue: SchemaIssue) -> NvokenError {
    let mut details = json!({
        "kind": "structured_output_schema",
        "code": issue.code,
        "path": issue.path,
    });
    if let Some(keyword) = &issue.keyword {
        details["keyword"] = Value::String(keyword.clone());
    }
    NvokenError {
        category: ErrorCategory::Validation,
        message: format!("output schema is invalid: {}", issue.message),
        status: None,
        code: Some(SCHEMA_PREFLIGHT_CODE.to_owned()),
        request_id: None,
        retry_after: None,
        details: Some(details),
    }
}

fn validate_schema_node(
    node: &Map<String, Value>,
    path: &str,
    depth: usize,
    root: bool,
) -> Option<SchemaIssue> {
    if depth > MAX_OUTPUT_SCHEMA_DEPTH {
        return Some(issue(
            "limit_exceeded",
            path,
            None,
            format!("schema exceeds the maximum nesting depth of {MAX_OUTPUT_SCHEMA_DEPTH}"),
        ));
    }
    let allowed = allowed_keywords();
    let mut keywords = node.keys().collect::<Vec<_>>();
    keywords.sort();
    for keyword in keywords {
        if !allowed.contains(keyword.as_str()) {
            return Some(member_issue(
                "unsupported_keyword",
                path,
                keyword,
                format!("unsupported schema keyword {keyword:?}"),
            ));
        }
    }
    let type_name = node.get("type").and_then(Value::as_str);
    if !type_name.is_some_and(supported_type) {
        return Some(member_issue(
            "invalid_schema",
            path,
            "type",
            "every schema position requires one supported string type",
        ));
    }
    let type_name = type_name.unwrap_or_default();
    if root && type_name != "object" {
        return Some(member_issue(
            "invalid_schema",
            path,
            "type",
            "schema root type must be object",
        ));
    }
    for keyword in ["description", "title"] {
        if node.get(keyword).is_some_and(|value| !value.is_string()) {
            return Some(member_issue(
                "invalid_schema",
                path,
                keyword,
                format!("schema {keyword} must be a string"),
            ));
        }
    }
    for keyword in ["maxItems", "maxLength", "minItems", "minLength"] {
        if node
            .get(keyword)
            .is_some_and(|value| !nonnegative_integer(value))
        {
            return Some(member_issue(
                "invalid_schema",
                path,
                keyword,
                format!("schema {keyword} must be a nonnegative integer"),
            ));
        }
    }
    for keyword in ["maximum", "minimum"] {
        if node.get(keyword).is_some_and(|value| !value.is_number()) {
            return Some(member_issue(
                "invalid_schema",
                path,
                keyword,
                format!("schema {keyword} must be a number"),
            ));
        }
    }
    if node
        .get("uniqueItems")
        .is_some_and(|value| !value.is_boolean())
    {
        return Some(member_issue(
            "invalid_schema",
            path,
            "uniqueItems",
            "schema uniqueItems must be a boolean",
        ));
    }
    if let Some(bound_issue) = validate_bound_order(node, path) {
        return Some(bound_issue);
    }
    if type_name != "string" {
        if let Some(keyword) = first_present(node, &["maxLength", "minLength", "pattern"]) {
            return Some(member_issue(
                "invalid_schema",
                path,
                keyword,
                "string schema keywords require type string",
            ));
        }
    }
    if type_name != "array" {
        if let Some(keyword) = first_present(node, &["maxItems", "minItems", "uniqueItems"]) {
            return Some(member_issue(
                "invalid_schema",
                path,
                keyword,
                "array schema bounds require type array",
            ));
        }
    }
    if type_name != "number" && type_name != "integer" {
        if let Some(keyword) = first_present(node, &["maximum", "minimum"]) {
            return Some(member_issue(
                "invalid_schema",
                path,
                keyword,
                "numeric schema keywords require type number or integer",
            ));
        }
    }
    if root && node.contains_key("enum") {
        return Some(member_issue(
            "invalid_schema",
            path,
            "enum",
            "schema root enum is not supported",
        ));
    }
    if let Some(pattern) = node.get("pattern") {
        let Some(pattern) = pattern.as_str() else {
            return Some(member_issue(
                "invalid_schema",
                path,
                "pattern",
                "schema pattern must be a string",
            ));
        };
        if pattern.len() > MAX_OUTPUT_PATTERN_BYTES {
            return Some(member_issue(
                "limit_exceeded",
                path,
                "pattern",
                format!(
                    "schema pattern exceeds the maximum size of {MAX_OUTPUT_PATTERN_BYTES} bytes"
                ),
            ));
        }
    }
    if node
        .get("enum")
        .is_some_and(|value| !value.as_array().is_some_and(|items| !items.is_empty()))
    {
        return Some(member_issue(
            "invalid_schema",
            path,
            "enum",
            "schema enum must be a nonempty array",
        ));
    }
    let has_properties = node.contains_key("properties");
    let has_required = node.contains_key("required");
    let has_additional = node.contains_key("additionalProperties");
    if (has_properties || has_required || has_additional) && type_name != "object" {
        let keyword = first_present(node, &["additionalProperties", "properties", "required"])
            .unwrap_or("properties");
        return Some(member_issue(
            "invalid_schema",
            path,
            keyword,
            "object schema keywords require type object",
        ));
    }
    let mut property_names = HashSet::new();
    if has_properties {
        let Some(properties) = node.get("properties").and_then(Value::as_object) else {
            return Some(member_issue(
                "invalid_schema",
                path,
                "properties",
                "schema properties must be an object",
            ));
        };
        let mut names = properties.keys().collect::<Vec<_>>();
        names.sort();
        for name in names {
            let child_path = pointer(&pointer(path, "properties"), name);
            let Some(child) = properties.get(name).and_then(Value::as_object) else {
                return Some(issue(
                    "invalid_schema",
                    &child_path,
                    None,
                    format!("property {name:?} must contain a schema object"),
                ));
            };
            if child.is_empty() {
                return Some(issue(
                    "invalid_schema",
                    &child_path,
                    None,
                    format!("property {name:?} must contain a schema object"),
                ));
            }
            property_names.insert(name.as_str());
            if let Some(child_issue) = validate_schema_node(child, &child_path, depth + 1, false) {
                return Some(child_issue);
            }
        }
    }
    if has_required {
        let Some(required) = node.get("required").and_then(Value::as_array) else {
            return Some(member_issue(
                "invalid_schema",
                path,
                "required",
                "schema required must be an array of property names",
            ));
        };
        let mut seen = HashSet::new();
        for (index, value) in required.iter().enumerate() {
            let item_path = pointer(&pointer(path, "required"), &index.to_string());
            let Some(name) = value.as_str().filter(|name| !name.is_empty()) else {
                return Some(issue(
                    "invalid_schema",
                    &item_path,
                    Some("required"),
                    "schema required must contain nonempty strings",
                ));
            };
            if !seen.insert(name) {
                return Some(issue(
                    "invalid_schema",
                    &item_path,
                    Some("required"),
                    "schema required must not contain duplicates",
                ));
            }
            if !property_names.contains(name) {
                return Some(issue(
                    "invalid_schema",
                    &item_path,
                    Some("required"),
                    format!("required property {name:?} is not declared"),
                ));
            }
        }
    }
    if has_additional {
        let additional = node.get("additionalProperties").unwrap_or(&Value::Null);
        if !additional.is_boolean() {
            let child_path = pointer(path, "additionalProperties");
            let Some(child) = additional.as_object() else {
                return Some(member_issue(
                    "invalid_schema",
                    path,
                    "additionalProperties",
                    "additionalProperties must be a boolean or schema object",
                ));
            };
            if child.is_empty() {
                return Some(issue(
                    "invalid_schema",
                    &child_path,
                    Some("additionalProperties"),
                    "additionalProperties must contain a schema object",
                ));
            }
            if let Some(child_issue) = validate_schema_node(child, &child_path, depth + 1, false) {
                return Some(child_issue);
            }
        }
    }
    let has_items = node.contains_key("items");
    if type_name == "array" && !has_items {
        return Some(member_issue(
            "invalid_schema",
            path,
            "items",
            "array schemas require items",
        ));
    }
    if has_items {
        if type_name != "array" {
            return Some(member_issue(
                "invalid_schema",
                path,
                "items",
                "schema items requires type array",
            ));
        }
        let Some(items) = node.get("items").and_then(Value::as_object) else {
            return Some(member_issue(
                "invalid_schema",
                path,
                "items",
                "schema items must be a schema object",
            ));
        };
        if items.is_empty() {
            return Some(member_issue(
                "invalid_schema",
                path,
                "items",
                "schema items must be a schema object",
            ));
        }
        if let Some(child_issue) =
            validate_schema_node(items, &pointer(path, "items"), depth + 1, false)
        {
            return Some(child_issue);
        }
    }
    None
}

fn validate_bound_order(node: &Map<String, Value>, path: &str) -> Option<SchemaIssue> {
    for (lower, upper, message) in [
        (
            "minLength",
            "maxLength",
            "schema minLength must not exceed maxLength",
        ),
        (
            "minItems",
            "maxItems",
            "schema minItems must not exceed maxItems",
        ),
        (
            "minimum",
            "maximum",
            "schema minimum must not exceed maximum",
        ),
    ] {
        let lower = node.get(lower).and_then(Value::as_f64);
        let upper_value = node.get(upper).and_then(Value::as_f64);
        if lower
            .zip(upper_value)
            .is_some_and(|(left, right)| left > right)
        {
            return Some(member_issue("invalid_schema", path, upper, message));
        }
    }
    None
}

// A bound is an integer when its value is one, not when its spelling is.
// `160`, `160.0`, and `1.6e2` are the same JSON number, and canonical JSON --
// which the runtime applies to tool input schemas before compiling them --
// renders every number in the last of those forms.
fn nonnegative_integer(value: &Value) -> bool {
    if value.as_u64().is_some() {
        return true;
    }
    value
        .as_f64()
        .is_some_and(|number| number.is_finite() && number >= 0.0 && number.fract() == 0.0)
}

fn first_present<'a>(node: &Map<String, Value>, keywords: &'a [&'a str]) -> Option<&'a str> {
    keywords
        .iter()
        .copied()
        .find(|keyword| node.contains_key(*keyword))
}

fn supported_type(value: &str) -> bool {
    matches!(
        value,
        "object" | "array" | "string" | "number" | "integer" | "boolean"
    )
}

fn allowed_keywords() -> HashSet<&'static str> {
    HashSet::from([
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
    ])
}

fn member_issue(code: &str, path: &str, keyword: &str, message: impl Into<String>) -> SchemaIssue {
    issue(code, &pointer(path, keyword), Some(keyword), message)
}

fn issue(code: &str, path: &str, keyword: Option<&str>, message: impl Into<String>) -> SchemaIssue {
    SchemaIssue {
        code: code.to_owned(),
        path: path.to_owned(),
        keyword: keyword.map(str::to_owned),
        message: message.into(),
    }
}

fn pointer(path: &str, member: &str) -> String {
    let escaped = member.replace('~', "~0").replace('/', "~1");
    format!("{path}/{escaped}")
}
