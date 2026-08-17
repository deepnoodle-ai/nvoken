use std::time::SystemTime;

use async_trait::async_trait;
use http::HeaderMap;
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::signed_delivery::{verify_signed_delivery, DeliveryError};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CallbackContext {
    pub schema_version: u32,
    pub delivery_id: String,
    pub tool_call_id: String,
    /// The tool this delivery is for. It is inside the signed body, so a
    /// receiver serving several tools dispatches on it directly. Any per-tool
    /// path or query suffix on the endpoint URL is unsigned and belongs in
    /// logs, not in a dispatch decision.
    pub tool_name: String,
    pub invocation_id: String,
    pub session_id: String,
    pub agent_key: String,
    pub tenant_key: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CallbackEnvelope {
    pub nvoken: CallbackContext,
    pub input: Value,
}

#[derive(Debug, Clone)]
pub struct VerifiedCallback {
    pub envelope: CallbackEnvelope,
    pub raw_body: Vec<u8>,
    pub delivery_id: String,
    pub tool_call_id: String,
    pub tool_name: String,
    pub key_id: String,
    pub key_version: u64,
    pub timestamp: SystemTime,
}

/// Checks one tool-callback delivery and returns its signed body.
///
/// The signature scheme is shared with [`crate::webhook::verify_webhook`]; only
/// the checks below, which are about what a callback body must say, are
/// particular to it.
pub fn verify_callback(
    key: &[u8],
    headers: &HeaderMap,
    raw_body: &[u8],
    now: SystemTime,
) -> Result<VerifiedCallback, DeliveryError> {
    let delivery = verify_signed_delivery(key, headers, raw_body, now)?;
    let envelope: CallbackEnvelope =
        serde_json::from_slice(raw_body).map_err(DeliveryError::InvalidEnvelope)?;
    if envelope.nvoken.schema_version != 1 {
        return Err(DeliveryError::UnsupportedSchemaVersion);
    }
    if envelope.nvoken.delivery_id != delivery.delivery_id
        || envelope.nvoken.tool_call_id != delivery.idempotency_key
    {
        return Err(DeliveryError::IdentityMismatch);
    }
    // tool_name is required on the wire, so an empty one is a sender that is
    // not nvoken or a body that is not a callback. Failing here keeps the
    // dispatch below it total: no receiver needs a branch for "no name".
    if envelope.nvoken.tool_name.is_empty() {
        return Err(DeliveryError::MissingToolName);
    }
    let tool_name = envelope.nvoken.tool_name.clone();
    Ok(VerifiedCallback {
        envelope,
        raw_body: raw_body.to_vec(),
        delivery_id: delivery.delivery_id,
        tool_call_id: delivery.idempotency_key,
        tool_name,
        key_id: delivery.key_id,
        key_version: delivery.key_version,
        timestamp: delivery.timestamp,
    })
}

/// The HTTP answer to one callback delivery.
///
/// Rendering it is left to the host's web framework: write `status`, and `body`
/// when it is not `None`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CallbackReply {
    pub status: u16,
    pub body: Option<String>,
}

/// Settles the ToolCall inline.
///
/// Content may be any JSON value, encoded to at most 256 KiB and 32 levels of
/// nesting. The turn resumes as soon as nvoken records the reply.
pub fn callback_result(
    content: serde_json::Value,
    is_error: bool,
) -> Result<CallbackReply, serde_json::Error> {
    let mut payload = serde_json::Map::new();
    payload.insert("content".to_owned(), content);
    if is_error {
        payload.insert("is_error".to_owned(), serde_json::Value::Bool(true));
    }
    Ok(CallbackReply {
        status: 200,
        body: Some(serde_json::to_string(&serde_json::Value::Object(payload))?),
    })
}

/// Accepts delivery without settling the ToolCall, for work that will outlive
/// this tool's reply deadline — its declared `timeout_seconds`, or the App's
/// default when it declares none. Settle it later with
/// `Client::submit_tool_results`, reusing the delivery's ToolCall id.
///
/// This trades away the fail-loud guarantee. nvoken marks an unacknowledged
/// delivery failed once its retries are exhausted, so the turn always moves on.
/// An acknowledged call instead waits under the host's responsibility, bounded
/// only by the Invocation's `limits.waiting_timeout_seconds`. Acknowledge only
/// when something durable will settle the call.
pub fn acknowledge_callback() -> CallbackReply {
    CallbackReply {
        status: 202,
        body: None,
    }
}

#[async_trait]
pub trait CallbackResultStore<T> {
    async fn put_if_absent(&self, tool_call_id: &str, result: T) -> Result<(T, bool), String>;
}

pub async fn deduplicate_callback_result<T, S>(
    store: &S,
    tool_call_id: &str,
    result: T,
) -> Result<(T, bool), String>
where
    T: Send,
    S: CallbackResultStore<T> + Sync,
{
    let (stored, inserted) = store.put_if_absent(tool_call_id, result).await?;
    Ok((stored, !inserted))
}
