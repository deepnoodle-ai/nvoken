//! The `ask_user` convention.
//!
//! A structured question to the end user is a host tool, not a new resource.
//! The park/webhook/resume machinery already *is* "block until someone answers",
//! so nvoken needs no pending-interaction state and no response endpoint to
//! deliver this. What it does need to supply is a standard shape, so the model
//! and the host UI agree on what a question looks like without every
//! integration inventing its own.
//!
//! This is a convention, not runtime behaviour: nvoken treats `ask_user` like
//! any other host tool. Adopting it costs nothing and means a UI written
//! against one agent renders questions from another. The shape matches dive's
//! `toolkit` ask_user, so an agent already written against that needs no
//! translation layer.

use std::collections::HashMap;

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::client::{Tool, ToolHandlerError};

/// The well-known name. No `nvoken_` prefix: the host executes it, not the
/// runtime.
pub const ASK_USER_TOOL_NAME: &str = "ask_user";

pub const ASK_USER_DESCRIPTION: &str = "Ask the user a question and wait for their answer. \
Use it when a decision is genuinely theirs to make, not to confirm work you can verify yourself.";

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum AskUserKind {
    Confirm,
    Select,
    Multiselect,
    Input,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct AskUserOption {
    pub value: String,
    pub label: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub description: Option<String>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub default: bool,
}

/// What the model sends.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct AskUserInput {
    pub question: String,
    #[serde(rename = "type")]
    pub kind: AskUserKind,
    /// Choices for `Select`/`Multiselect`. Ignored otherwise.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub options: Vec<AskUserOption>,
    /// Pre-filled answer: `"true"`/`"false"` for confirm, text for input.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub default: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub min_select: Option<u32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_select: Option<u32>,
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub multiline: bool,
}

/// What the host returns as the tool result.
///
/// `canceled` is not an error: a user declining to answer is a legitimate
/// outcome the model should see and reason about, whereas an error result
/// would read to it as a broken tool.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct AskUserOutput {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub response: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub values: Vec<String>,
    pub canceled: bool,
}

impl AskUserOutput {
    pub fn answer(response: impl Into<String>) -> Self {
        Self {
            response: Some(response.into()),
            values: Vec::new(),
            canceled: false,
        }
    }

    pub fn selection(values: Vec<String>) -> Self {
        Self {
            response: None,
            values,
            canceled: false,
        }
    }

    pub fn canceled() -> Self {
        Self {
            response: None,
            values: Vec::new(),
            canceled: true,
        }
    }
}

/// The tool input schema, in the bounded subset nvoken admits.
pub fn ask_user_input_schema() -> HashMap<String, Value> {
    let schema: Value = serde_json::from_str(ASK_USER_INPUT_SCHEMA_JSON)
        .expect("nvoken: ask_user input schema is invalid");
    match schema {
        Value::Object(map) => map.into_iter().collect(),
        _ => unreachable!("ask_user input schema is an object"),
    }
}

const ASK_USER_INPUT_SCHEMA_JSON: &str = r#"{
  "type": "object",
  "properties": {
    "question": {"type": "string", "minLength": 1, "maxLength": 2000,
      "description": "The question to put to the user."},
    "type": {"type": "string", "enum": ["confirm", "select", "multiselect", "input"],
      "description": "How the user answers."},
    "options": {
      "type": "array",
      "maxItems": 20,
      "description": "Choices for select and multiselect. Ignored otherwise.",
      "items": {
        "type": "object",
        "properties": {
          "value": {"type": "string", "minLength": 1, "maxLength": 200},
          "label": {"type": "string", "minLength": 1, "maxLength": 200},
          "description": {"type": "string", "maxLength": 500},
          "default": {"type": "boolean"}
        },
        "required": ["value", "label"],
        "additionalProperties": false
      }
    },
    "default": {"type": "string", "maxLength": 2000,
      "description": "Pre-filled answer: \"true\"/\"false\" for confirm, text for input."},
    "min_select": {"type": "integer", "minimum": 0, "maximum": 20},
    "max_select": {"type": "integer", "minimum": 0, "maximum": 20},
    "multiline": {"type": "boolean"}
  },
  "required": ["question", "type"],
  "additionalProperties": false
}"#;

/// The host tool declaration on its own, for a caller who wants to attach a
/// raw `Value` handler with [`Tool::handler`].
pub fn ask_user_tool() -> Tool {
    Tool::host(
        ASK_USER_TOOL_NAME,
        ASK_USER_DESCRIPTION,
        ask_user_input_schema(),
    )
}

/// A ready-to-use `ask_user` host tool. Supply a handler that renders the
/// question and resolves to an [`AskUserOutput`]; decoding the model's input
/// and encoding the answer are done here.
///
/// Return [`AskUserOutput::canceled`] rather than an error when the user
/// dismisses the question — a decline is an outcome the model should reason
/// about, not a broken tool.
pub fn ask_user_tool_with<F, Fut>(handler: F) -> Tool
where
    F: Fn(AskUserInput) -> Fut + Send + Sync + 'static,
    Fut: std::future::Future<Output = AskUserOutput> + Send + 'static,
{
    ask_user_tool().handler(move |input: Value| {
        // Dispatch before the async block so the handler stays borrowed by the
        // outer `Fn` rather than needing to move into a `'static` future.
        let dispatched = serde_json::from_value::<AskUserInput>(input).map(&handler);
        async move {
            match dispatched {
                Ok(pending) => serde_json::to_value(pending.await)
                    .map_err(|error| ToolHandlerError::new(error.to_string())),
                Err(error) => Err(ToolHandlerError::with_type(
                    error.to_string(),
                    "ask_user_input_invalid",
                )),
            }
        }
    })
}
