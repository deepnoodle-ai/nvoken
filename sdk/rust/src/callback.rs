use std::time::{Duration, SystemTime, UNIX_EPOCH};

use async_trait::async_trait;
use hmac::{Hmac, Mac};
use http::HeaderMap;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::Sha256;

type HmacSha256 = Hmac<Sha256>;

#[derive(Debug, thiserror::Error)]
pub enum CallbackError {
    #[error("callback signing key must be at least 32 bytes")]
    KeyTooShort,
    #[error("missing or invalid {name} header")]
    MissingOrInvalidHeader { name: String },
    #[error("unsupported callback signature version")]
    UnsupportedSignatureVersion,
    #[error("invalid callback timestamp")]
    InvalidTimestamp,
    #[error("callback timestamp is outside the accepted window")]
    TimestampOutsideWindow,
    #[error("invalid callback key version")]
    InvalidKeyVersion,
    #[error("callback identity headers are invalid")]
    InvalidIdentity,
    #[error("callback signature must use sha256 prefix")]
    InvalidSignaturePrefix,
    #[error("callback signature must be hexadecimal")]
    InvalidSignatureEncoding,
    #[error("callback signature mismatch")]
    SignatureMismatch,
    #[error("callback signature initialization failed")]
    SignatureInitialization,
    #[error("invalid callback envelope: {0}")]
    InvalidEnvelope(#[source] serde_json::Error),
    #[error("unsupported callback schema version")]
    UnsupportedSchemaVersion,
    #[error("callback identity header does not match signed body")]
    IdentityMismatch,
    #[error("callback envelope is missing tool_name")]
    MissingToolName,
}

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

pub fn verify_callback(
    key: &[u8],
    headers: &HeaderMap,
    raw_body: &[u8],
    now: SystemTime,
) -> Result<VerifiedCallback, CallbackError> {
    if key.len() < 32 {
        return Err(CallbackError::KeyTooShort);
    }
    if header(headers, "x-nvoken-signature-version")? != "v1" {
        return Err(CallbackError::UnsupportedSignatureVersion);
    }
    let timestamp_seconds = header(headers, "x-nvoken-timestamp")?
        .parse::<u64>()
        .map_err(|_| CallbackError::InvalidTimestamp)?;
    let timestamp = UNIX_EPOCH + Duration::from_secs(timestamp_seconds);
    let distance = now
        .duration_since(timestamp)
        .or_else(|_| timestamp.duration_since(now))
        .map_err(|_| CallbackError::InvalidTimestamp)?;
    if distance > Duration::from_secs(300) {
        return Err(CallbackError::TimestampOutsideWindow);
    }
    let delivery_id = header(headers, "x-nvoken-delivery-id")?.to_owned();
    let tool_call_id = header(headers, "idempotency-key")?.to_owned();
    let key_id = header(headers, "x-nvoken-signing-key-id")?.to_owned();
    let key_version = header(headers, "x-nvoken-signing-key-version")?
        .parse::<u64>()
        .map_err(|_| CallbackError::InvalidKeyVersion)?;
    if delivery_id.is_empty() || tool_call_id.is_empty() || key_id.is_empty() || key_version == 0 {
        return Err(CallbackError::InvalidIdentity);
    }
    let signature = header(headers, "x-nvoken-signature")?;
    let supplied = signature
        .strip_prefix("sha256=")
        .ok_or(CallbackError::InvalidSignaturePrefix)?;
    let supplied = hex::decode(supplied).map_err(|_| CallbackError::InvalidSignatureEncoding)?;
    let mut mac =
        HmacSha256::new_from_slice(key).map_err(|_| CallbackError::SignatureInitialization)?;
    mac.update(format!("v1.{delivery_id}.{timestamp_seconds}.").as_bytes());
    mac.update(raw_body);
    mac.verify_slice(&supplied)
        .map_err(|_| CallbackError::SignatureMismatch)?;
    let envelope: CallbackEnvelope =
        serde_json::from_slice(raw_body).map_err(CallbackError::InvalidEnvelope)?;
    if envelope.nvoken.schema_version != 1 {
        return Err(CallbackError::UnsupportedSchemaVersion);
    }
    if envelope.nvoken.delivery_id != delivery_id || envelope.nvoken.tool_call_id != tool_call_id {
        return Err(CallbackError::IdentityMismatch);
    }
    // tool_name is required on the wire, so an empty one is a sender that is
    // not nvoken or a body that is not a callback. Failing here keeps the
    // dispatch below it total: no receiver needs a branch for "no name".
    if envelope.nvoken.tool_name.is_empty() {
        return Err(CallbackError::MissingToolName);
    }
    let tool_name = envelope.nvoken.tool_name.clone();
    Ok(VerifiedCallback {
        envelope,
        raw_body: raw_body.to_vec(),
        delivery_id,
        tool_call_id,
        tool_name,
        key_id,
        key_version,
        timestamp,
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

fn header<'a>(headers: &'a HeaderMap, name: &str) -> Result<&'a str, CallbackError> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| CallbackError::MissingOrInvalidHeader {
            name: name.to_owned(),
        })
}
