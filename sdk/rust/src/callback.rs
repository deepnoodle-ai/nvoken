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
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CallbackContext {
    pub schema_version: u32,
    pub delivery_id: String,
    pub tool_call_id: String,
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
    Ok(VerifiedCallback {
        envelope,
        raw_body: raw_body.to_vec(),
        delivery_id,
        tool_call_id,
        key_id,
        key_version,
        timestamp,
    })
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
