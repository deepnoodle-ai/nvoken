pub mod agent;
pub mod apis;
pub mod callback;
pub mod client;
pub mod client_token;
mod facade;
pub mod media_preflight;
#[allow(unused_imports)]
pub mod models;
pub mod routes;
pub mod signed_delivery;
pub mod stream;
pub mod turn_status;
pub mod webhook;

/// The released version of the Rust SDK.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

pub use callback::{
    acknowledge_callback, callback_result, verify_callback, CallbackContext, CallbackDelivery,
    CallbackEnvelope, CallbackOutcome, CallbackReceiver, CallbackReceiverBuilder, CallbackReply,
    CallbackResultStore, CallbackTool, VerifiedCallback,
};
pub use client_token::{
    mint_client_token, ClientTokenClaims, ClientTokenConversationAccess, ClientTokenError,
    ClientTokenMemoryAccess, CLIENT_TOKEN_LIFETIME_LIMIT, CLIENT_TOKEN_TYPE,
};
pub use facade::{
    Agent, AgentCollection, AgentPage, Behavior, BehaviorSourceKind, Client, Conversation,
    ConversationContext, ConversationOwner, ConversationRef, ConversationTurnOptions, InlineAgent,
    InlineConversationContext, InlineMemory, InlineTurnOptions, IntoModelInput, IntoTurnInput,
    Memory, NvokenError, OwnedBy, RawClient, Tool, ToolContext, ToolHandler, Turn, TurnAdmission,
    TurnAdmissionError, TurnExecutionError, TurnOptions, TurnResult, TurnSnapshot,
    TurnTimeoutError, TurnUpdate,
};
pub use media_preflight::{
    document_block, document_url_block, image_block, image_url_block, preflight_input_blocks,
    text_block, MediaIssue,
};
pub use models::{AgentRevision, MemorySpace};
pub use signed_delivery::{
    verify_signed_delivery, DeliveryError, DeliveryKeyError, DeliveryKeyTableError,
    DeliverySigningKey, SignedDelivery, SIGNATURE_TIMESTAMP_WINDOW,
};
pub use stream::{
    read_stream, stream_conversation, stream_turn, ReducedSnapshot, Reducer, StreamEvent,
    StreamOptions, StreamPreview,
};
pub use turn_status::{
    is_terminal_status, is_turn_over, ALL_TURN_STATUSES, TERMINAL_TURN_STATUSES,
};
pub use webhook::{
    accept_webhook, retry_webhook, verify_webhook, webhook_status_is_retried, VerifiedWebhook,
    WebhookContext, WebhookDelivery, WebhookEnvelope, WebhookHandler, WebhookOutcome,
    WebhookReceiver, WebhookReceiverBuilder, WebhookReply, WebhookSubject,
};
