use std::time::{Duration, SystemTime, UNIX_EPOCH};

use hmac::{Hmac, Mac};
use http::HeaderMap;
use sha2::Sha256;

type HmacSha256 = Hmac<Sha256>;

/// How far a delivery's signed timestamp may sit from the receiver's clock.
pub const SIGNATURE_TIMESTAMP_WINDOW: Duration = Duration::from_secs(300);

/// What can go wrong receiving a signed delivery.
///
/// One enum covers callbacks and Turn webhooks because one scheme signs
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
    #[error("delivery envelope is missing Turn attribution")]
    InvalidAttribution,
    #[error("webhook sequence must be positive")]
    InvalidWebhookSequence,
}

/// One delivery whose signature has been checked, before its body is read.
///
/// nvoken signs tool callbacks and Turn webhooks the same way, so
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

/// One secret a receiver will accept deliveries signed with.
///
/// The key id names the App and the purpose and does not change; the version
/// selects the secret within it. Holding two versions is what makes a rotation
/// survivable — nvoken mints the next version while still signing with the
/// current one, and a signature a receiver cannot verify fails its delivery
/// outright rather than retrying, so there is no forgiveness to lean on.
///
/// `version` is an integer rather than the string it arrives as in
/// configuration on purpose. A version that cannot be read as a positive
/// integer makes the receiver refuse to be built, which is loud, instead of
/// refusing live deliveries, which is permanent.
#[derive(Debug, Clone)]
pub struct DeliverySigningKey {
    pub key_id: String,
    pub version: u64,
    /// At least 32 bytes.
    pub secret: Vec<u8>,
}

impl DeliverySigningKey {
    pub fn new(key_id: impl Into<String>, version: u64, secret: impl Into<Vec<u8>>) -> Self {
        Self {
            key_id: key_id.into(),
            version,
            secret: secret.into(),
        }
    }
}

/// Why a receiver would not accept a delivery's signing identity.
///
/// `retryable` is the whole point of the distinction. An unconfigured receiver
/// is an operator error this deployment may still fix inside nvoken's retry
/// window. A configured receiver that does not know this key version is a real
/// signing-identity failure, and asking for redelivery only reproduces it.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum DeliveryKeyError {
    #[error("delivery signing key not_configured")]
    NotConfigured,
    #[error("delivery signing key unknown_key")]
    UnknownKey,
}

impl DeliveryKeyError {
    pub fn reason(self) -> &'static str {
        match self {
            Self::NotConfigured => "not_configured",
            Self::UnknownKey => "unknown_key",
        }
    }

    pub fn retryable(self) -> bool {
        matches!(self, Self::NotConfigured)
    }
}

/// What makes a key table unusable, refused when a receiver is built rather
/// than when a delivery arrives.
#[derive(Debug, Clone, thiserror::Error)]
pub enum DeliveryKeyTableError {
    #[error("delivery signing key is missing a key id")]
    MissingKeyID,
    #[error("delivery signing key {key_id} has a non-positive version")]
    NonPositiveVersion { key_id: String },
    #[error("delivery signing secret for {key_id} v{version} must be at least 32 bytes")]
    SecretTooShort { key_id: String, version: u64 },
    #[error("delivery signing key {key_id} v{version} is configured twice")]
    DuplicateKey { key_id: String, version: u64 },
}

pub(crate) type DeliveryKeyTable = std::collections::HashMap<(String, u64), Vec<u8>>;

/// Normalizes a receiver's key table, refusing at build time what would
/// otherwise be refused at delivery time.
///
/// Two entries with the same key id and version are a configuration mistake
/// rather than a redundancy: which secret wins would decide whether deliveries
/// verify, and nothing in the pair says which was meant.
pub(crate) fn delivery_signing_keys(
    keys: Vec<DeliverySigningKey>,
) -> Result<DeliveryKeyTable, DeliveryKeyTableError> {
    let mut table = DeliveryKeyTable::with_capacity(keys.len());
    for key in keys {
        if key.key_id.is_empty() {
            return Err(DeliveryKeyTableError::MissingKeyID);
        }
        if key.version == 0 {
            return Err(DeliveryKeyTableError::NonPositiveVersion { key_id: key.key_id });
        }
        if key.secret.len() < 32 {
            return Err(DeliveryKeyTableError::SecretTooShort {
                key_id: key.key_id,
                version: key.version,
            });
        }
        let slot = (key.key_id.clone(), key.version);
        if table.contains_key(&slot) {
            return Err(DeliveryKeyTableError::DuplicateKey {
                key_id: key.key_id,
                version: key.version,
            });
        }
        table.insert(slot, key.secret);
    }
    Ok(table)
}

/// Picks the secret a delivery says it was signed with, before anything parses
/// the body.
///
/// Selection reads only the two identity headers, so a delivery signed by an
/// identity this receiver does not hold is refused without its body ever being
/// decoded, logged, or dispatched on.
pub(crate) fn select_delivery_key<'a>(
    table: &'a DeliveryKeyTable,
    headers: &HeaderMap,
) -> Result<&'a [u8], DeliveryKeyError> {
    if table.is_empty() {
        return Err(DeliveryKeyError::NotConfigured);
    }
    let key_id = header(headers, "x-nvoken-signing-key-id")
        .map_err(|_| DeliveryKeyError::UnknownKey)?
        .to_owned();
    let version = header(headers, "x-nvoken-signing-key-version")
        .map_err(|_| DeliveryKeyError::UnknownKey)?
        .parse::<u64>()
        .map_err(|_| DeliveryKeyError::UnknownKey)?;
    if key_id.is_empty() || version == 0 {
        return Err(DeliveryKeyError::UnknownKey);
    }
    table
        .get(&(key_id, version))
        .map(Vec::as_slice)
        .ok_or(DeliveryKeyError::UnknownKey)
}

pub(crate) fn header<'a>(headers: &'a HeaderMap, name: &str) -> Result<&'a str, DeliveryError> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .ok_or_else(|| DeliveryError::MissingOrInvalidHeader {
            name: name.to_owned(),
        })
}
