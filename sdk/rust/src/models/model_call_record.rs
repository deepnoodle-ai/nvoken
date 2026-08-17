/*
 * nvoken API
 *
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  One turn is one Invocation. `Invocation` is the name in every path, field, schema, and error, and it is the word to reach for when you need to be exact. `turn` is the English for the same thing, and this document uses it in explanatory prose. They are not two resources, so there is no relationship between them for you to model.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## Keys and IDs  Every identifier here is one of two things, and its name tells you which.  A `*_key` is a name you choose. `tenant_key`, `agent_key`, `session_key`, and `definition_key` name a resource. Send one and nvoken either creates that resource or hands back the one your name already refers to, so the same request twice gives you the same resource rather than two. `user_key` is a label rather than a name: it records who a Session was opened for so you can filter on it later, and it is not a permission boundary. `idempotency_key` names one attempt, not a resource.  An `id` is an identifier nvoken mints. It carries a typed prefix, `sess_` for a Session and `inv_` for an Invocation, and everything after that prefix is opaque. Match on the prefix if it helps you route. Never parse past it, and never build one yourself.  A resource calls its own identifier `id`. A reference to a different resource carries that resource's name, so `session_id` on an Invocation is the Session it belongs to. There is no third pattern.  Paths only ever take IDs. Keys travel in request bodies and query filters. Every keyed resource reports both its key and its `id`, so you can always get from your name to nvoken's identifier by reading the resource, and you never have to keep a mapping of your own.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `connection.closing` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `connection.closing` says exactly what it says and nothing more. A client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `connection.closing` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `connection.closing` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 * Generated by: https://openapi-generator.tech
 */

use crate::models;
use serde::{Deserialize, Serialize};

#[derive(Clone, Default, Debug, PartialEq, Serialize, Deserialize)]
pub struct ModelCallRecord {
    /// Opaque model-call fact ID with the mcall_ prefix.
    #[serde(rename = "id")]
    pub id: String,
    #[serde(rename = "invocation_id", deserialize_with = "Option::deserialize")]
    pub invocation_id: Option<String>,
    #[serde(rename = "session_id", deserialize_with = "Option::deserialize")]
    pub session_id: Option<String>,
    /// Opaque identifier with the public `app_` prefix. Treat the body as opaque.
    #[serde(rename = "app_id")]
    pub app_id: String,
    #[serde(rename = "tenant_key", deserialize_with = "Option::deserialize")]
    pub tenant_key: Option<String>,
    /// Null after user erasure.
    #[serde(rename = "user_key", deserialize_with = "Option::deserialize")]
    pub user_key: Option<String>,
    #[serde(rename = "agent_id", deserialize_with = "Option::deserialize")]
    pub agent_id: Option<String>,
    #[serde(
        rename = "credential_family_id",
        deserialize_with = "Option::deserialize"
    )]
    pub credential_family_id: Option<String>,
    #[serde(
        rename = "authentication_method",
        deserialize_with = "Option::deserialize"
    )]
    pub authentication_method: Option<models::AuthenticationMethod>,
    #[serde(rename = "provider_key_source")]
    pub provider_key_source: models::ProviderKeySource,
    #[serde(rename = "provider_key_id", deserialize_with = "Option::deserialize")]
    pub provider_key_id: Option<String>,
    #[serde(
        rename = "provider_key_version_id",
        deserialize_with = "Option::deserialize"
    )]
    pub provider_key_version_id: Option<String>,
    #[serde(rename = "call_kind")]
    pub call_kind: models::ModelCallKind,
    #[serde(rename = "call_ordinal")]
    pub call_ordinal: u32,
    #[serde(rename = "lease_attempt")]
    pub lease_attempt: u32,
    #[serde(rename = "provider_attempt_ordinal")]
    pub provider_attempt_ordinal: u32,
    #[serde(rename = "requested_provider")]
    pub requested_provider: String,
    #[serde(rename = "requested_model")]
    pub requested_model: String,
    #[serde(rename = "served_provider", deserialize_with = "Option::deserialize")]
    pub served_provider: Option<String>,
    #[serde(rename = "served_model", deserialize_with = "Option::deserialize")]
    pub served_model: Option<String>,
    #[serde(rename = "status")]
    pub status: models::ModelCallFactStatus,
    #[serde(rename = "outcome", deserialize_with = "Option::deserialize")]
    pub outcome: Option<Outcome>,
    #[serde(rename = "failure_class", deserialize_with = "Option::deserialize")]
    pub failure_class: Option<String>,
    #[serde(rename = "input_tokens", deserialize_with = "Option::deserialize")]
    pub input_tokens: Option<u32>,
    #[serde(rename = "output_tokens", deserialize_with = "Option::deserialize")]
    pub output_tokens: Option<u32>,
    #[serde(
        rename = "cache_creation_input_tokens",
        deserialize_with = "Option::deserialize"
    )]
    pub cache_creation_input_tokens: Option<u32>,
    #[serde(
        rename = "cache_read_input_tokens",
        deserialize_with = "Option::deserialize"
    )]
    pub cache_read_input_tokens: Option<u32>,
    #[serde(rename = "reasoning_tokens", deserialize_with = "Option::deserialize")]
    pub reasoning_tokens: Option<u32>,
    #[serde(rename = "model_cost", deserialize_with = "Option::deserialize")]
    pub model_cost: Option<Box<models::Money>>,
    #[serde(rename = "cost_coverage")]
    pub cost_coverage: CostCoverage,
    #[serde(rename = "max_cost_at_risk", deserialize_with = "Option::deserialize")]
    pub max_cost_at_risk: Option<Box<models::Money>>,
    #[serde(rename = "pricing_version", deserialize_with = "Option::deserialize")]
    pub pricing_version: Option<String>,
    #[serde(rename = "created_at")]
    pub created_at: chrono::DateTime<chrono::FixedOffset>,
    #[serde(rename = "started_at", deserialize_with = "Option::deserialize")]
    pub started_at: Option<chrono::DateTime<chrono::FixedOffset>>,
    #[serde(rename = "first_output_at", deserialize_with = "Option::deserialize")]
    pub first_output_at: Option<chrono::DateTime<chrono::FixedOffset>>,
    #[serde(rename = "settled_at", deserialize_with = "Option::deserialize")]
    pub settled_at: Option<chrono::DateTime<chrono::FixedOffset>>,
}

impl ModelCallRecord {
    pub fn new(
        id: String,
        invocation_id: Option<String>,
        session_id: Option<String>,
        app_id: String,
        tenant_key: Option<String>,
        user_key: Option<String>,
        agent_id: Option<String>,
        credential_family_id: Option<String>,
        authentication_method: Option<models::AuthenticationMethod>,
        provider_key_source: models::ProviderKeySource,
        provider_key_id: Option<String>,
        provider_key_version_id: Option<String>,
        call_kind: models::ModelCallKind,
        call_ordinal: u32,
        lease_attempt: u32,
        provider_attempt_ordinal: u32,
        requested_provider: String,
        requested_model: String,
        served_provider: Option<String>,
        served_model: Option<String>,
        status: models::ModelCallFactStatus,
        outcome: Option<Outcome>,
        failure_class: Option<String>,
        input_tokens: Option<u32>,
        output_tokens: Option<u32>,
        cache_creation_input_tokens: Option<u32>,
        cache_read_input_tokens: Option<u32>,
        reasoning_tokens: Option<u32>,
        model_cost: Option<models::Money>,
        cost_coverage: CostCoverage,
        max_cost_at_risk: Option<models::Money>,
        pricing_version: Option<String>,
        created_at: chrono::DateTime<chrono::FixedOffset>,
        started_at: Option<chrono::DateTime<chrono::FixedOffset>>,
        first_output_at: Option<chrono::DateTime<chrono::FixedOffset>>,
        settled_at: Option<chrono::DateTime<chrono::FixedOffset>>,
    ) -> ModelCallRecord {
        ModelCallRecord {
            id,
            invocation_id,
            session_id,
            app_id,
            tenant_key,
            user_key,
            agent_id,
            credential_family_id,
            authentication_method,
            provider_key_source,
            provider_key_id,
            provider_key_version_id,
            call_kind,
            call_ordinal,
            lease_attempt,
            provider_attempt_ordinal,
            requested_provider,
            requested_model,
            served_provider,
            served_model,
            status,
            outcome,
            failure_class,
            input_tokens,
            output_tokens,
            cache_creation_input_tokens,
            cache_read_input_tokens,
            reasoning_tokens,
            model_cost: if let Some(x) = model_cost {
                Some(Box::new(x))
            } else {
                None
            },
            cost_coverage,
            max_cost_at_risk: if let Some(x) = max_cost_at_risk {
                Some(Box::new(x))
            } else {
                None
            },
            pricing_version,
            created_at,
            started_at,
            first_output_at,
            settled_at,
        }
    }
}
///
#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Hash, Serialize, Deserialize)]
pub enum Outcome {
    #[serde(rename = "succeeded")]
    Succeeded,
    #[serde(rename = "failed")]
    Failed,
    #[serde(rename = "cancelled")]
    Cancelled,
}

impl Default for Outcome {
    fn default() -> Outcome {
        Self::Succeeded
    }
}
///
#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Hash, Serialize, Deserialize)]
pub enum CostCoverage {
    #[serde(rename = "complete")]
    Complete,
    #[serde(rename = "none")]
    None,
    #[serde(rename = "not_applicable")]
    NotApplicable,
}

impl Default for CostCoverage {
    fn default() -> CostCoverage {
        Self::Complete
    }
}
