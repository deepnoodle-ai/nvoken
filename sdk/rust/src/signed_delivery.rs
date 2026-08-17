use std::time::{Duration, SystemTime, UNIX_EPOCH};

use hmac::{Hmac, Mac};
use http::HeaderMap;
use sha2::Sha256;

type HmacSha256 = Hmac<Sha256>;

/// How far a delivery's signed timestamp may sit from the receiver's clock.
pub const SIGNATURE_TIMESTAMP_WINDOW: Duration = Duration::from_secs(300);

/// What can go wrong receiving a signed delivery.
///
/// One enum covers callbacks and Invocation webhooks because one scheme signs
/// both; the variants that name a delivery kind are the ones about what that
/// kind's body must say.
#[derive(Debug, thiserror::Error)]
pub enum DeliveryError {
    #[error("delivery signing key must be at least 32 bytes")]
    KeyTooShort,
    #[error("missing or invalid {name} header")]
    MissingOrInvalidHeader { name: String },
    #[error("unsupported delivery signature version")]
    UnsupportedSignatureVersion,
    #[error("invalid delivery timestamp")]
    InvalidTimestamp,
    #[error("delivery timestamp is outside the accepted window")]
    TimestampOutsideWindow,
    #[error("invalid delivery key version")]
    InvalidKeyVersion,
    #[error("delivery identity headers are invalid")]
    InvalidIdentity,
    #[error("delivery signature must use sha256 prefix")]
    InvalidSignaturePrefix,
    #[error("delivery signature must be hexadecimal")]
    InvalidSignatureEncoding,
    #[error("delivery signature mismatch")]
    SignatureMismatch,
    #[error("delivery signature initialization failed")]
    SignatureInitialization,
    #[error("invalid delivery envelope: {0}")]
    InvalidEnvelope(#[source] serde_json::Error),
    #[error("unsupported delivery schema version")]
    UnsupportedSchemaVersion,
    #[error("delivery identity header does not match signed body")]
    IdentityMismatch,
    #[error("callback envelope is missing tool_name")]
    MissingToolName,
    #[error("webhook sequence must be positive")]
    InvalidWebhookSequence,
}

/// One delivery whose signature has been checked, before its body is read.
///
/// nvoken signs tool callbacks and Invocation webhooks the same way, so
/// everything up to and including the HMAC comparison lives here once rather
/// than in two copies that have to be kept in step. What differs is only what
/// the verified body then means: a callback settles a named ToolCall, a webhook
/// reports a transition that already happened.
#[derive(Debug, Clone)]
pub struct SignedDelivery {
    pub delivery_id: String,
    /// The ToolCall id on a callback and the delivery id on a webhook. In both
    /// it is the value a receiver deduplicates on.
    pub idempotency_key: String,
    pub key_id: String,
    pub key_version: u64,
    pub timestamp: SystemTime,
}

/// Checks the signature headers and the HMAC over the raw body.
///
/// This reads nothing out of the body, so it is total over both delivery kinds
/// and cannot acquire a requirement that belongs to one of them.
pub fn verify_signed_delivery(
    key: &[u8],
    headers: &HeaderMap,
    raw_body: &[u8],
    now: SystemTime,
) -> Result<SignedDelivery, DeliveryError> {
    if key.len() < 32 {
        return Err(DeliveryError::KeyTooShort);
    }
    if header(headers, "x-nvoken-signature-version")? != "v1" {
        return Err(DeliveryError::UnsupportedSignatureVersion);
    }
    let timestamp_seconds = header(headers, "x-nvoken-timestamp")?
        .parse::<u64>()
        .map_err(|_| DeliveryError::InvalidTimestamp)?;
    let timestamp = UNIX_EPOCH + Duration::from_secs(timestamp_seconds);
    let distance = now
        .duration_since(timestamp)
        .or_else(|_| timestamp.duration_since(now))
        .map_err(|_| DeliveryError::InvalidTimestamp)?;
    if distance > SIGNATURE_TIMESTAMP_WINDOW {
        return Err(DeliveryError::TimestampOutsideWindow);
    }
    let delivery_id = header(headers, "x-nvoken-delivery-id")?.to_owned();
    let idempotency_key = header(headers, "idempotency-key")?.to_owned();
    let key_id = header(headers, "x-nvoken-signing-key-id")?.to_owned();
    let key_version = header(headers, "x-nvoken-signing-key-version")?
        .parse::<u64>()
        .map_err(|_| DeliveryError::InvalidKeyVersion)?;
    if delivery_id.is_empty() || idempotency_key.is_empty() || key_id.is_empty() || key_version == 0
    {
        return Err(DeliveryError::InvalidIdentity);
    }
    let signature = header(headers, "x-nvoken-signature")?;
    let supplied = signature
        .strip_prefix("sha256=")
        .ok_or(DeliveryError::InvalidSignaturePrefix)?;
    let supplied = hex::decode(supplied).map_err(|_| DeliveryError::InvalidSignatureEncoding)?;
    let mut mac =
        HmacSha256::new_from_slice(key).map_err(|_| DeliveryError::SignatureInitialization)?;
    mac.update(format!("v1.{delivery_id}.{timestamp_seconds}.").as_bytes());
    mac.update(raw_body);
    mac.verify_slice(&supplied)
        .map_err(|_| DeliveryError::SignatureMismatch)?;
    Ok(SignedDelivery {
        delivery_id,
        idempotency_key,
        key_id,
        key_version,
        timestamp,
    })
}

pub(crate) fn header<'a>(headers: &'a HeaderMap, name: &str) -> Result<&'a str, DeliveryError> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| DeliveryError::MissingOrInvalidHeader {
            name: name.to_owned(),
        })
}
