use std::time::SystemTime;

use http::HeaderMap;
use serde::{Deserialize, Serialize};

use crate::models;
use crate::signed_delivery::{verify_signed_delivery, DeliveryError};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebhookContext {
    pub schema_version: u32,
    pub delivery_id: String,
    /// Typed as the generated enum, so an event nvoken adds later fails to
    /// decode rather than reaching a receiver that has no branch for it.
    /// Answering such a delivery successfully would settle a transition the
    /// host in fact ignored.
    pub event: models::WebhookEvent,
    /// Counts transitions within one Invocation, from 1. See
    /// [`VerifiedWebhook::supersedes`] for what a receiver does with it.
    pub sequence: i64,
    pub invocation_id: String,
    pub session_id: String,
    pub agent_key: String,
    pub tenant_key: Option<String>,
}

/// A pointer to the turn, not a projection of it.
///
/// It carries no transcript content, tool arguments, structured output, usage,
/// provenance, or failure message: read `Client::get_invocation` or
/// `Client::get_invocation_result` for anything beyond what is here.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebhookSubject {
    pub status: models::InvocationStatus,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stop_reason: Option<models::InvocationStopReason>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub failure_code: Option<String>,
    /// The host tools the turn is parked on, on `invocation.waiting` only.
    /// Tools nvoken delivers itself are absent: they are not work the host has
    /// been handed.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub waiting_tool_call_ids: Option<Vec<String>>,
    /// Names the account that could not fund the next attempt, when a spending
    /// limit stopped the turn.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub credit_block: Option<models::CreditBlock>,
}

/// The signed body of one Invocation webhook.
///
/// It mirrors [`crate::CallbackEnvelope`]: everything nvoken asserts sits under
/// `nvoken`, and the subject of the delivery sits beside it.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebhookEnvelope {
    pub nvoken: WebhookContext,
    pub invocation: WebhookSubject,
}

/// One Invocation webhook whose signature has been checked.
#[derive(Debug, Clone)]
pub struct VerifiedWebhook {
    pub envelope: WebhookEnvelope,
    pub raw_body: Vec<u8>,
    pub delivery_id: String,
    /// Read from the signed body. The endpoint URL may carry an unsigned
    /// per-event suffix; that belongs in logs, not in a dispatch decision.
    pub event: models::WebhookEvent,
    pub sequence: i64,
    pub invocation_id: String,
    pub session_id: String,
    pub key_id: String,
    pub key_version: u64,
    pub timestamp: SystemTime,
}

impl VerifiedWebhook {
    /// Reports whether this delivery describes a later transition of its
    /// Invocation than the one already applied.
    ///
    /// Delivery is at least once, so the same transition can arrive twice and a
    /// redelivery can land after a later one. Keep the highest sequence applied
    /// per Invocation and fold only what supersedes it; a receiver that applies
    /// whichever arrived last rolls its own state backwards. Pass 0 for an
    /// Invocation nothing has been applied for yet.
    ///
    /// This is also the dedup: a repeat carries a sequence already applied, so
    /// nothing further is needed to make handling idempotent. Answer it with
    /// [`accept_webhook`] all the same — it was delivered, and asking for
    /// redelivery of something already handled only produces the same repeat.
    pub fn supersedes(&self, applied_sequence: i64) -> bool {
        self.sequence > applied_sequence
    }
}

/// Checks one Invocation webhook delivery and returns its signed body.
///
/// It shares its signature scheme with [`crate::verify_callback`], so a host
/// that receives both implements verification once and dispatches on what the
/// verified body says.
///
/// The key is the App's `webhook`-purpose signing key. Callbacks are signed
/// with the `callback`-purpose key, so a receiver serving both endpoints holds
/// two keys and must not try either against the other's deliveries.
pub fn verify_webhook(
    key: &[u8],
    headers: &HeaderMap,
    raw_body: &[u8],
    now: SystemTime,
) -> Result<VerifiedWebhook, DeliveryError> {
    let delivery = verify_signed_delivery(key, headers, raw_body, now)?;
    let envelope: WebhookEnvelope =
        serde_json::from_slice(raw_body).map_err(DeliveryError::InvalidEnvelope)?;
    if envelope.nvoken.schema_version != 1 {
        return Err(DeliveryError::UnsupportedSchemaVersion);
    }
    // The idempotency key on a webhook is the delivery id, so both headers pin
    // the same fact and both must agree with the body that was signed.
    if envelope.nvoken.delivery_id != delivery.delivery_id
        || delivery.idempotency_key != delivery.delivery_id
    {
        return Err(DeliveryError::IdentityMismatch);
    }
    if envelope.nvoken.sequence < 1 {
        return Err(DeliveryError::InvalidWebhookSequence);
    }
    Ok(VerifiedWebhook {
        event: envelope.nvoken.event,
        sequence: envelope.nvoken.sequence,
        invocation_id: envelope.nvoken.invocation_id.clone(),
        session_id: envelope.nvoken.session_id.clone(),
        envelope,
        raw_body: raw_body.to_vec(),
        delivery_id: delivery.delivery_id,
        key_id: delivery.key_id,
        key_version: delivery.key_version,
        timestamp: delivery.timestamp,
    })
}

/// The HTTP answer to one webhook delivery.
///
/// nvoken ignores the response body, so only the status carries meaning, and no
/// answer ever affects the Invocation the webhook describes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct WebhookReply {
    pub status: u16,
}

/// Takes responsibility for the delivery. nvoken will not send it again.
pub fn accept_webhook() -> WebhookReply {
    WebhookReply { status: 200 }
}

/// Asks nvoken to deliver again, for a receiver that could not record the
/// transition right now — its store was unreachable, or it is shedding load.
///
/// Retries are bounded, so a receiver that answers this forever still ends with
/// a transition nobody recorded; `Client::list_ended_invocations` is the
/// backstop that finds those.
pub fn retry_webhook() -> WebhookReply {
    WebhookReply { status: 503 }
}

/// Reports whether nvoken redelivers after a receiver answers with this status.
///
/// Any 5xx is retried, as are 408, 425, and 429. Every other non-2xx answer —
/// 400, 401, 403, 404, 409, 410, 422 among them — is permanent, and the
/// transition it described is never delivered again. Refusing a body that
/// genuinely failed verification with 401 is therefore right: redelivering it
/// would fail the same way. Refusing one because the signing key could not be
/// read is not, and should answer [`retry_webhook`] instead, since the two are
/// indistinguishable to nvoken and only one of them is the sender's fault.
pub fn webhook_status_is_retried(status: u16) -> bool {
    matches!(status, 408 | 425 | 429) || status >= 500
}
