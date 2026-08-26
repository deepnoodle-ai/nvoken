use std::collections::HashMap;
use std::future::Future;
use std::time::SystemTime;

use async_trait::async_trait;
use http::HeaderMap;
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::models;
use crate::signed_delivery::{
    delivery_signing_keys, select_delivery_key, verify_signed_delivery, DeliveryError,
    DeliveryKeyTable, DeliveryKeyTableError, DeliverySigningKey,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebhookContext {
    pub schema_version: u32,
    pub delivery_id: String,
    /// Typed as the generated enum, so an event nvoken adds later fails to
    /// decode rather than reaching a receiver that has no branch for it.
    /// Answering such a delivery successfully would settle a transition the
    /// host in fact ignored.
    pub event: models::WebhookEvent,
    /// Counts transitions within one Turn, from 1. See
    /// [`VerifiedWebhook::supersedes`] for what a receiver does with it.
    pub sequence: i64,
    pub turn_id: String,
    pub conversation_id: Option<String>,
    pub memory_space_id: Option<String>,
    pub content_expires_at: Option<chrono::DateTime<chrono::FixedOffset>>,
    pub behavior_source: Value,
    pub tenant_key: String,
    pub user_key: Option<String>,
}

/// A pointer to the turn, not a projection of it.
///
/// It carries no transcript content, tool arguments, structured output, usage,
/// provenance, or failure message: read the authoritative Turn or TurnResult
/// for anything beyond what is here.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebhookSubject {
    pub status: models::TurnStatus,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub stop_reason: Option<models::TurnStopReason>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub failure_code: Option<String>,
    /// The host tools the turn is parked on, on `turn.waiting` only.
    /// Tools nvoken delivers itself are absent: they are not work the host has
    /// been handed.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub waiting_tool_call_ids: Option<Vec<String>>,
    /// Names the account that could not fund the next attempt, when a spending
    /// limit stopped the turn.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub credit_block: Option<models::CreditBlock>,
}

/// The signed body of one Turn webhook.
///
/// It mirrors [`crate::CallbackEnvelope`]: everything nvoken asserts sits under
/// `nvoken`, and the subject of the delivery sits beside it.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WebhookEnvelope {
    pub nvoken: WebhookContext,
    pub turn: WebhookSubject,
}

/// One Turn webhook whose signature has been checked.
#[derive(Debug, Clone)]
pub struct VerifiedWebhook {
    pub envelope: WebhookEnvelope,
    pub raw_body: Vec<u8>,
    pub delivery_id: String,
    /// Read from the signed body. The endpoint URL may carry an unsigned
    /// per-event suffix; that belongs in logs, not in a dispatch decision.
    pub event: models::WebhookEvent,
    pub sequence: i64,
    pub turn_id: String,
    pub conversation_id: Option<String>,
    pub memory_space_id: Option<String>,
    pub content_expires_at: Option<chrono::DateTime<chrono::FixedOffset>>,
    pub behavior_source: Value,
    pub tenant_key: String,
    pub user_key: Option<String>,
    pub key_id: String,
    pub key_version: u64,
    pub timestamp: SystemTime,
}

impl VerifiedWebhook {
    /// Reports whether this delivery describes a later transition of its
    /// Turn than the one already applied.
    ///
    /// Delivery is at least once, so the same transition can arrive twice and a
    /// redelivery can land after a later one. Keep the highest sequence applied
    /// per Turn and fold only what supersedes it; a receiver that applies
    /// whichever arrived last rolls its own state backwards. Pass 0 for an
    /// Turn nothing has been applied for yet.
    ///
    /// This is also the dedup: a repeat carries a sequence already applied, so
    /// nothing further is needed to make handling idempotent. Answer it with
    /// [`accept_webhook`] all the same — it was delivered, and asking for
    /// redelivery of something already handled only produces the same repeat.
    pub fn supersedes(&self, applied_sequence: i64) -> bool {
        self.sequence > applied_sequence
    }
}

/// Checks one Turn webhook delivery and returns its signed body.
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
    if envelope.nvoken.schema_version != 2 {
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
    if envelope.nvoken.turn_id.is_empty()
        || envelope.nvoken.tenant_key.is_empty()
        || !envelope.nvoken.behavior_source.is_object()
    {
        return Err(DeliveryError::InvalidAttribution);
    }
    let context = envelope.nvoken.clone();
    Ok(VerifiedWebhook {
        event: context.event,
        sequence: context.sequence,
        turn_id: context.turn_id,
        conversation_id: context.conversation_id,
        memory_space_id: context.memory_space_id,
        content_expires_at: context.content_expires_at,
        behavior_source: context.behavior_source,
        tenant_key: context.tenant_key,
        user_key: context.user_key,
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
/// answer ever affects the Turn the webhook describes.
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
/// a transition nobody recorded; listing ended Turns through `Client::raw` is
/// the backstop that finds those.
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

/// Records one transition. Returning `Err` asks nvoken to deliver it again, so
/// return one when the receiver could not record it and `Ok` when it did.
#[async_trait]
pub trait WebhookHandler: Send + Sync {
    async fn handle(&self, delivery: &VerifiedWebhook) -> Result<(), String>;
}

#[async_trait]
impl<F, Fut> WebhookHandler for F
where
    F: Fn(&VerifiedWebhook) -> Fut + Send + Sync,
    Fut: Future<Output = Result<(), String>> + Send,
{
    async fn handle(&self, delivery: &VerifiedWebhook) -> Result<(), String> {
        self(delivery).await
    }
}

/// What a receiver did with one webhook delivery.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WebhookOutcome {
    Handled,
    Ignored,
    Refused,
    Failed,
}

/// One answered delivery: the reply the host writes, and enough about what
/// happened to log it.
#[derive(Debug, Clone)]
pub struct WebhookDelivery {
    pub reply: WebhookReply,
    pub outcome: WebhookOutcome,
    /// A stable token for a log line. Never echoed to nvoken, which ignores
    /// webhook bodies.
    pub reason: &'static str,
    pub delivery: Option<VerifiedWebhook>,
    pub cause: Option<String>,
}

/// Answers a Turn-webhook endpoint. It is [`crate::CallbackReceiver`]'s
/// twin — same key table, same reply discipline — because nvoken signs both
/// deliveries the same way.
///
/// It is a separate receiver rather than a mode of one, because the two
/// endpoints hold different keys: callbacks are signed with the App's
/// `callback`-purpose key and webhooks with its `webhook`-purpose key, and
/// neither may be tried against the other's deliveries.
///
/// | situation | status | why |
/// | --- | --- | --- |
/// | no keys configured | 503 | an operator error, still fixable inside the retry window |
/// | signing identity not held | 401 | a real identity failure; redelivery reproduces it |
/// | signature, timestamp, or envelope invalid | 401 | the same bytes fail the same way |
/// | no handler for the signed event | 200 | it was delivered; redelivering finds the same absent handler |
/// | handler returned `Ok` | 200 | the transition is recorded |
/// | handler returned `Err` | 503 | the receiver could not record it, so ask for it again |
///
/// **Ordering stays yours.** Delivery is at least once and out of order, so the
/// highest applied `sequence` per Turn has to be read and written in the
/// same transaction as the state it guards — which is the host's transaction,
/// not one this kit can open. Call [`VerifiedWebhook::supersedes`] inside it. A
/// superseded delivery is still a delivery: record nothing and return `Ok`, so
/// it answers 200.
pub struct WebhookReceiver {
    keys: DeliveryKeyTable,
    events: HashMap<models::WebhookEvent, Box<dyn WebhookHandler>>,
}

impl WebhookReceiver {
    /// Starts a receiver with the secrets this endpoint accepts. Two entries
    /// span a key rotation.
    pub fn builder(keys: Vec<DeliverySigningKey>) -> WebhookReceiverBuilder {
        WebhookReceiverBuilder {
            keys,
            events: HashMap::new(),
        }
    }

    /// Answers one delivery. It never returns `Err`: everything that can go
    /// wrong is a status nvoken understands, and the outcome says which.
    pub async fn handle(
        &self,
        headers: &HeaderMap,
        raw_body: &[u8],
        now: SystemTime,
    ) -> WebhookDelivery {
        let secret = match select_delivery_key(&self.keys, headers) {
            Ok(secret) => secret,
            Err(error) => {
                return WebhookDelivery {
                    reply: WebhookReply {
                        status: if error.retryable() { 503 } else { 401 },
                    },
                    outcome: if error.retryable() {
                        WebhookOutcome::Failed
                    } else {
                        WebhookOutcome::Refused
                    },
                    reason: error.reason(),
                    delivery: None,
                    cause: Some(error.to_string()),
                }
            }
        };
        let delivery = match verify_webhook(secret, headers, raw_body, now) {
            Ok(delivery) => delivery,
            Err(error) => {
                return WebhookDelivery {
                    reply: WebhookReply { status: 401 },
                    outcome: WebhookOutcome::Refused,
                    reason: "invalid_signature",
                    delivery: None,
                    cause: Some(error.to_string()),
                }
            }
        };

        // A subscribed event with no handler is a gap in this receiver, not a
        // failure of the delivery. Answering 503 would only spend nvoken's
        // bounded retries reaching the same absent handler, and lose it anyway.
        let Some(handler) = self.events.get(&delivery.event) else {
            return WebhookDelivery {
                reply: accept_webhook(),
                outcome: WebhookOutcome::Ignored,
                reason: "unhandled_event",
                delivery: Some(delivery),
                cause: None,
            };
        };
        match handler.handle(&delivery).await {
            Ok(()) => WebhookDelivery {
                reply: accept_webhook(),
                outcome: WebhookOutcome::Handled,
                reason: "recorded",
                delivery: Some(delivery),
                cause: None,
            },
            Err(error) => WebhookDelivery {
                reply: retry_webhook(),
                outcome: WebhookOutcome::Failed,
                reason: "handler_failed",
                delivery: Some(delivery),
                cause: Some(error),
            },
        }
    }
}

pub struct WebhookReceiverBuilder {
    keys: Vec<DeliverySigningKey>,
    events: HashMap<models::WebhookEvent, Box<dyn WebhookHandler>>,
}

impl WebhookReceiverBuilder {
    /// Registers a handler for the event nvoken signs into the body.
    pub fn event(
        mut self,
        event: models::WebhookEvent,
        handler: impl WebhookHandler + 'static,
    ) -> Self {
        self.events.insert(event, Box::new(handler));
        self
    }

    /// Builds the receiver, refusing a key table that could only fail later at
    /// delivery time.
    pub fn build(self) -> Result<WebhookReceiver, DeliveryKeyTableError> {
        Ok(WebhookReceiver {
            keys: delivery_signing_keys(self.keys)?,
            events: self.events,
        })
    }
}
