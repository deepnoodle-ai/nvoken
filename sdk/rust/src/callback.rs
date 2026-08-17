use std::collections::HashMap;
use std::future::Future;
use std::time::SystemTime;

use async_trait::async_trait;
use http::HeaderMap;
use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::signed_delivery::{
    delivery_signing_keys, select_delivery_key, verify_signed_delivery, DeliveryError,
    DeliveryKeyError, DeliveryKeyTable, DeliveryKeyTableError, DeliverySigningKey,
};

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
    /// What the Session was bound to at creation, in the host's own terms,
    /// absent when it was created without one.
    ///
    /// It is a sibling of `nvoken` rather than a member of it, and the
    /// placement is the rule: everything inside `nvoken` is a fact nvoken
    /// minted or resolved, while this is a value the host asserted and nvoken
    /// carried unchanged. Signing proves it reached the receiver as recorded,
    /// not that it is true.
    ///
    /// A value repeated in tool input may only agree with this, never establish
    /// it. The model writes the input; it does not write this.
    #[serde(default, skip_serializing_if = "HashMap::is_empty")]
    pub authorization_context: HashMap<String, String>,
    pub input: Value,
}

#[derive(Debug, Clone)]
pub struct VerifiedCallback {
    pub envelope: CallbackEnvelope,
    pub raw_body: Vec<u8>,
    pub delivery_id: String,
    pub tool_call_id: String,
    pub tool_name: String,
    /// The Session's authorization context, read off the signed body.
    /// Authorize the delivery from this rather than from anything in
    /// `envelope.input`, and treat a value that appears in both as agreement to
    /// check rather than as a second source.
    pub authorization_context: HashMap<String, String>,
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
    let authorization_context = envelope.authorization_context.clone();
    Ok(VerifiedCallback {
        envelope,
        raw_body: raw_body.to_vec(),
        delivery_id: delivery.delivery_id,
        tool_call_id: delivery.idempotency_key,
        tool_name,
        authorization_context,
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

/// Where a receiver records what it already answered, so a redelivery returns
/// that answer instead of running the tool again.
///
/// Both operations are needed and they are needed in this order. `find` runs
/// before the tool does, because a redelivery that re-runs it repeats every
/// effect it had. `put_if_absent` runs after, because two deliveries of one
/// ToolCall can be in flight at once and only one answer may win.
#[async_trait]
pub trait CallbackResultStore: Send + Sync {
    async fn find(&self, tool_call_id: &str) -> Result<Option<CallbackReply>, String>;
    async fn put_if_absent(
        &self,
        tool_call_id: &str,
        reply: CallbackReply,
    ) -> Result<(CallbackReply, bool), String>;
}

/// Runs one tool for one delivery. Return the reply — [`callback_result`] to
/// settle the call, [`acknowledge_callback`] to take it away and settle it
/// later.
///
/// A tool that failed still returns: `callback_result(reason, true)` settles the
/// call carrying `is_error`, which the model can read and correct itself
/// against. Returning `Err` means something in the *receiver* failed, not the
/// tool, and answers 503 so nvoken redelivers.
#[async_trait]
pub trait CallbackTool: Send + Sync {
    async fn run(&self, delivery: &VerifiedCallback) -> Result<CallbackReply, String>;
}

#[async_trait]
impl<F, Fut> CallbackTool for F
where
    F: Fn(&VerifiedCallback) -> Fut + Send + Sync,
    Fut: Future<Output = Result<CallbackReply, String>> + Send,
{
    async fn run(&self, delivery: &VerifiedCallback) -> Result<CallbackReply, String> {
        self(delivery).await
    }
}

/// What a receiver did with one delivery.
///
/// It is what the status alone cannot say — a 200 that replayed a recorded
/// answer did no work.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CallbackOutcome {
    Settled,
    Acknowledged,
    Replayed,
    Refused,
    Failed,
}

/// One answered delivery: the reply the host writes, and enough about what
/// happened to log it.
///
/// `reason` is a stable token for a log line and is never echoed into the reply
/// body, because nvoken is not the audience for it and a refused sender should
/// learn nothing.
#[derive(Debug, Clone)]
pub struct CallbackDelivery {
    pub reply: CallbackReply,
    pub outcome: CallbackOutcome,
    pub reason: &'static str,
    /// Set once the signature checked out, whatever happened afterwards.
    pub delivery: Option<VerifiedCallback>,
    /// The failure behind a refused or failed outcome, for the host's logger.
    /// It is never rendered into the reply.
    pub cause: Option<String>,
}

/// Answers a tool-callback endpoint: key selection, signature verification,
/// dispatch on the signed tool name, deduplication, and the reply discipline
/// nvoken reads.
///
/// That discipline is the part worth having in one place, because every status
/// here is a decision about whether nvoken tries again:
///
/// | situation | status | why |
/// | --- | --- | --- |
/// | no keys configured | 503 | an operator error, still fixable inside the retry window |
/// | signing identity not held | 401 | a real identity failure; redelivery reproduces it |
/// | signature, timestamp, or envelope invalid | 401 | the same bytes fail the same way |
/// | no handler for the signed tool name | 400 | nothing here can ever run it |
/// | handler returned a reply | 200 or 202 | the tool answered, or took the call away |
/// | handler returned `Err` | 503 | the receiver failed, not the tool — and the store makes retrying safe |
///
/// A tool that failed is not a receiver that failed. Settle it with
/// `callback_result(reason, true)`: the model can read a tool error and correct
/// itself, while a 5xx only has nvoken deliver the same doomed call again.
///
/// The endpoint is public because nvoken must reach it, and it is not
/// anonymous: nothing below the signature check runs until the HMAC over the
/// raw bytes verifies.
pub struct CallbackReceiver {
    keys: DeliveryKeyTable,
    tools: HashMap<String, Box<dyn CallbackTool>>,
    store: Option<Box<dyn CallbackResultStore>>,
}

impl CallbackReceiver {
    /// Starts a receiver with the secrets this endpoint accepts. Two entries
    /// span a key rotation.
    pub fn builder(keys: Vec<DeliverySigningKey>) -> CallbackReceiverBuilder {
        CallbackReceiverBuilder {
            keys,
            tools: HashMap::new(),
            store: None,
        }
    }

    /// Answers one delivery. It never returns `Err`: everything that can go
    /// wrong is a status nvoken understands, and the outcome says which.
    pub async fn handle(
        &self,
        headers: &HeaderMap,
        raw_body: &[u8],
        now: SystemTime,
    ) -> CallbackDelivery {
        let secret = match select_delivery_key(&self.keys, headers) {
            Ok(secret) => secret,
            Err(error) => return refused_key(error),
        };
        let delivery = match verify_callback(secret, headers, raw_body, now) {
            Ok(delivery) => delivery,
            Err(error) => {
                return CallbackDelivery {
                    reply: CallbackReply {
                        status: 401,
                        body: None,
                    },
                    outcome: CallbackOutcome::Refused,
                    reason: "invalid_signature",
                    delivery: None,
                    cause: Some(error.to_string()),
                }
            }
        };
        self.dispatch(delivery).await
    }

    async fn dispatch(&self, delivery: VerifiedCallback) -> CallbackDelivery {
        let Some(tool) = self.tools.get(&delivery.tool_name) else {
            return CallbackDelivery {
                reply: CallbackReply {
                    status: 400,
                    body: None,
                },
                outcome: CallbackOutcome::Refused,
                reason: "unknown_tool",
                delivery: Some(delivery),
                cause: None,
            };
        };

        if let Some(store) = &self.store {
            match store.find(&delivery.tool_call_id).await {
                Err(error) => return failed(delivery, "store_unreadable", error),
                Ok(Some(recorded)) => {
                    return CallbackDelivery {
                        reply: recorded,
                        outcome: CallbackOutcome::Replayed,
                        reason: "recorded",
                        delivery: Some(delivery),
                        cause: None,
                    }
                }
                Ok(None) => {}
            }
        }

        let reply = match tool.run(&delivery).await {
            Ok(reply) => reply,
            Err(error) => return failed(delivery, "handler_failed", error),
        };
        let Some(store) = &self.store else {
            return CallbackDelivery {
                outcome: settled_or_acknowledged(&reply),
                reply,
                reason: "ran",
                delivery: Some(delivery),
                cause: None,
            };
        };
        match store.put_if_absent(&delivery.tool_call_id, reply).await {
            Err(error) => failed(delivery, "store_unwritable", error),
            Ok((stored, true)) => CallbackDelivery {
                outcome: settled_or_acknowledged(&stored),
                reply: stored,
                reason: "ran",
                delivery: Some(delivery),
                cause: None,
            },
            // Another delivery of the same ToolCall answered first. Its reply is
            // the one nvoken already has, so returning ours would be a second
            // answer to a call that has one.
            Ok((stored, false)) => CallbackDelivery {
                reply: stored,
                outcome: CallbackOutcome::Replayed,
                reason: "raced",
                delivery: Some(delivery),
                cause: None,
            },
        }
    }
}

pub struct CallbackReceiverBuilder {
    keys: Vec<DeliverySigningKey>,
    tools: HashMap<String, Box<dyn CallbackTool>>,
    store: Option<Box<dyn CallbackResultStore>>,
}

impl CallbackReceiverBuilder {
    /// Registers a handler for the tool name nvoken signs into the body.
    pub fn tool(mut self, name: impl Into<String>, tool: impl CallbackTool + 'static) -> Self {
        self.tools.insert(name.into(), Box::new(tool));
        self
    }

    /// Records answered ToolCalls. Leave it unset only when every tool here is
    /// safe to run twice: without a store, a redelivery runs the tool again.
    pub fn store(mut self, store: impl CallbackResultStore + 'static) -> Self {
        self.store = Some(Box::new(store));
        self
    }

    /// Builds the receiver, refusing a key table that could only fail later at
    /// delivery time.
    pub fn build(self) -> Result<CallbackReceiver, DeliveryKeyTableError> {
        Ok(CallbackReceiver {
            keys: delivery_signing_keys(self.keys)?,
            tools: self.tools,
            store: self.store,
        })
    }
}

fn settled_or_acknowledged(reply: &CallbackReply) -> CallbackOutcome {
    if reply.status == 202 {
        CallbackOutcome::Acknowledged
    } else {
        CallbackOutcome::Settled
    }
}

fn refused_key(error: DeliveryKeyError) -> CallbackDelivery {
    CallbackDelivery {
        reply: CallbackReply {
            status: if error.retryable() { 503 } else { 401 },
            body: None,
        },
        outcome: if error.retryable() {
            CallbackOutcome::Failed
        } else {
            CallbackOutcome::Refused
        },
        reason: error.reason(),
        delivery: None,
        cause: Some(error.to_string()),
    }
}

fn failed(delivery: VerifiedCallback, reason: &'static str, cause: String) -> CallbackDelivery {
    CallbackDelivery {
        reply: CallbackReply {
            status: 503,
            body: None,
        },
        outcome: CallbackOutcome::Failed,
        reason,
        delivery: Some(delivery),
        cause: Some(cause),
    }
}
