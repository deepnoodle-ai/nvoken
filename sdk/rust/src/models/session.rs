/*
 * nvoken API
 *
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or   read `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, client-safe projections, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  Two streams carry the same frames. `GET /v1/invocations/{invocation_id}/stream` follows one turn and ends when that turn settles. `GET /v1/sessions/{session_id}/transcript/stream` follows every turn in a Session, and is the surface to use for a conversation. `POST /v1/invocations` with `Accept: text/event-stream` admits and streams one turn inline.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  The Invocation stream's saved frames are `invocation.accepted`, `invocation.update`, and `invocation.result`. The Session stream's only saved frame is `transcript.update`. Every other frame on either stream is live.  ### Resuming and finishing  The resume position has four spellings and one value: the SSE `id` line, `resume_cursor` inside a frame payload, the `cursor` query parameter, and the `Last-Event-ID` header. Send it back as `cursor` or as `Last-Event-ID`; `cursor` wins when a request carries both. Cursors are Session-scoped on both streams, so a position taken from one stream resumes the other.  Reconnecting to a turn that has already settled always yields `invocation.result` followed by `stream.end` with reason `terminal`, at any cursor. Both are valid signals that a turn is over, and a client may exit on either.  `invocation.accepted` is emitted only by the inline `POST` path. The `GET` stream never sends it, so a client that admits separately never sees it. The nvoken SDKs synthesize an equivalent locally so their callers see the same first event either way.  An `invocation.update` never carries a terminal status. Terminal state arrives as `invocation.result` and nowhere else on that stream. The `invocation` it carries is re-read when the frame is written, so it is current state with a resume position attached rather than a snapshot taken at the cursor.  ### Previews  `output_text.delta` and `thinking.delta` preview one model iteration. Their identity is `(invocation_id, attempt, iteration, content_index)`. Accumulate by that tuple, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message lands, and when the turn reaches a terminal status. One model iteration produces exactly one saved assistant message, so previews sharing an `(invocation_id, attempt, iteration)` build one message. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `iteration`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe: a turn that has settled re-yields its result.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 * Generated by: https://openapi-generator.tech
 */

use crate::models;
use serde::{Deserialize, Serialize};

#[derive(Clone, Default, Debug, PartialEq, Serialize, Deserialize)]
pub struct Session {
    /// Opaque identifier with the public `sess_` prefix. Treat the body as opaque.
    #[serde(rename = "id")]
    pub id: String,
    /// Null only while a Session created ahead of time has not run a turn yet. The first turn binds the Agent, and after that it never changes.
    #[serde(rename = "agent_id", deserialize_with = "Option::deserialize")]
    pub agent_id: Option<String>,
    /// Immutable effective tenant partition reference.
    #[serde(rename = "tenant_key", deserialize_with = "Option::deserialize")]
    pub tenant_key: Option<String>,
    #[serde(rename = "session_key", deserialize_with = "Option::deserialize")]
    pub session_key: Option<String>,
    /// Host-owned end-user label recorded when this Session was opened. Filtering only; not an isolation boundary.
    #[serde(rename = "user_key", deserialize_with = "Option::deserialize")]
    pub user_key: Option<String>,
    /// Durable source prefix lineage, or null for an original Session.
    #[serde(rename = "forked_from", deserialize_with = "Option::deserialize")]
    pub forked_from: Option<Box<models::SessionForkLineage>>,
    /// The automatic compaction policy this Session actually applies, or null when it compacts nothing. It is echoed resolved: a request that asked for `trigger_tokens: auto` reads back the integer that resolved to, and a request that named no model reads back the model the policy bound. Nothing here is ever the unresolved request.
    #[serde(rename = "compaction", deserialize_with = "Option::deserialize")]
    pub compaction: Option<Box<models::CompactionPolicy>>,
    /// The idle retention window this Session was created with, or null when it is retained until deleted explicitly. A window outside the supported range is refused at creation rather than clamped, so what is read back is always exactly what applies.
    #[serde(rename = "retention", deserialize_with = "Option::deserialize")]
    pub retention: Option<Box<models::RetentionPolicy>>,
    /// When nvoken may automatically delete this Session, or null if it has no retention window. The date moves forward every time a turn starts and every time one finishes, so a Session in active use never reaches it.
    #[serde(rename = "expires_at", deserialize_with = "Option::deserialize")]
    pub expires_at: Option<chrono::DateTime<chrono::FixedOffset>>,
    /// Host correlation data, returned verbatim. Set at creation through `session_options.metadata` and changed with `PATCH /v1/sessions/{session_id}`.
    #[serde(rename = "metadata", deserialize_with = "Option::deserialize")]
    pub metadata: Option<std::collections::HashMap<String, String>>,
    /// The queued, running, waiting, or paused Invocation, if one exists.
    #[serde(
        rename = "active_invocation_id",
        deserialize_with = "Option::deserialize"
    )]
    pub active_invocation_id: Option<String>,
    /// Status of active_invocation_id; null exactly when that ID is null.
    #[serde(
        rename = "active_invocation_status",
        deserialize_with = "Option::deserialize"
    )]
    pub active_invocation_status: Option<ActiveInvocationStatus>,
    /// Tenant credit account blocking the active paused Invocation, otherwise null.
    #[serde(rename = "credit_block", deserialize_with = "Option::deserialize")]
    pub credit_block: Option<Box<models::CreditBlock>>,
    /// Read-time retained-context estimate and the model window it is measured against. Null until the Session has either a compaction model or an Invocation primary model. The object remains present for an uncataloged model, with `context_window_tokens: null`.
    #[serde(rename = "context", deserialize_with = "Option::deserialize")]
    pub context: Option<Box<models::SessionContext>>,
    /// Read-time sum of this Session's non-null Invocation usage and committed private compaction usage. Null until either exists. This normalized estimate is not a billing ledger.
    #[serde(rename = "usage", deserialize_with = "Option::deserialize")]
    pub usage: Option<Box<models::ModelUsage>>,
    #[serde(rename = "created_at")]
    pub created_at: chrono::DateTime<chrono::FixedOffset>,
    #[serde(rename = "updated_at")]
    pub updated_at: chrono::DateTime<chrono::FixedOffset>,
    /// Pending host and callback calls for the active waiting Invocation.
    #[serde(rename = "pending_tool_calls", skip_serializing_if = "Option::is_none")]
    pub pending_tool_calls: Option<Vec<models::PendingHostToolCall>>,
}

impl Session {
    pub fn new(
        id: String,
        agent_id: Option<String>,
        tenant_key: Option<String>,
        session_key: Option<String>,
        user_key: Option<String>,
        forked_from: Option<models::SessionForkLineage>,
        compaction: Option<models::CompactionPolicy>,
        retention: Option<models::RetentionPolicy>,
        expires_at: Option<chrono::DateTime<chrono::FixedOffset>>,
        metadata: Option<std::collections::HashMap<String, String>>,
        active_invocation_id: Option<String>,
        active_invocation_status: Option<ActiveInvocationStatus>,
        credit_block: Option<models::CreditBlock>,
        context: Option<models::SessionContext>,
        usage: Option<models::ModelUsage>,
        created_at: chrono::DateTime<chrono::FixedOffset>,
        updated_at: chrono::DateTime<chrono::FixedOffset>,
    ) -> Session {
        Session {
            id,
            agent_id,
            tenant_key,
            session_key,
            user_key,
            forked_from: if let Some(x) = forked_from {
                Some(Box::new(x))
            } else {
                None
            },
            compaction: if let Some(x) = compaction {
                Some(Box::new(x))
            } else {
                None
            },
            retention: if let Some(x) = retention {
                Some(Box::new(x))
            } else {
                None
            },
            expires_at,
            metadata,
            active_invocation_id,
            active_invocation_status,
            credit_block: if let Some(x) = credit_block {
                Some(Box::new(x))
            } else {
                None
            },
            context: if let Some(x) = context {
                Some(Box::new(x))
            } else {
                None
            },
            usage: if let Some(x) = usage {
                Some(Box::new(x))
            } else {
                None
            },
            created_at,
            updated_at,
            pending_tool_calls: None,
        }
    }
}
/// Status of active_invocation_id; null exactly when that ID is null.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Hash, Serialize, Deserialize)]
pub enum ActiveInvocationStatus {
    #[serde(rename = "queued")]
    Queued,
    #[serde(rename = "running")]
    Running,
    #[serde(rename = "waiting")]
    Waiting,
    #[serde(rename = "paused")]
    Paused,
}

impl Default for ActiveInvocationStatus {
    fn default() -> ActiveInvocationStatus {
        Self::Queued
    }
}
