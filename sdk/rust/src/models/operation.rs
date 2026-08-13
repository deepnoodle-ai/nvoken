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

///
#[derive(Clone, Copy, Debug, Eq, PartialEq, Ord, PartialOrd, Hash, Serialize, Deserialize)]
pub enum Operation {
    #[serde(rename = "create_invocation")]
    CreateInvocation,
    #[serde(rename = "create_agent_definition")]
    CreateAgentDefinition,
    #[serde(rename = "get_agent_definition")]
    GetAgentDefinition,
    #[serde(rename = "list_agent_definitions")]
    ListAgentDefinitions,
    #[serde(rename = "update_agent_definition")]
    UpdateAgentDefinition,
    #[serde(rename = "create_session")]
    CreateSession,
    #[serde(rename = "get_agent")]
    GetAgent,
    #[serde(rename = "list_agents")]
    ListAgents,
    #[serde(rename = "get_invocation")]
    GetInvocation,
    #[serde(rename = "submit_tool_results")]
    SubmitToolResults,
    #[serde(rename = "cancel_invocation")]
    CancelInvocation,
    #[serde(rename = "interrupt_invocation")]
    InterruptInvocation,
    #[serde(rename = "manage_invocation_nudges")]
    ManageInvocationNudges,
    #[serde(rename = "resume_invocation")]
    ResumeInvocation,
    #[serde(rename = "list_invocations")]
    ListInvocations,
    #[serde(rename = "get_session")]
    GetSession,
    #[serde(rename = "update_session")]
    UpdateSession,
    #[serde(rename = "delete_session")]
    DeleteSession,
    #[serde(rename = "list_sessions")]
    ListSessions,
    #[serde(rename = "list_session_messages")]
    ListSessionMessages,
    #[serde(rename = "get_session_transcript")]
    GetSessionTranscript,
    #[serde(rename = "read_memories")]
    ReadMemories,
    #[serde(rename = "delete_memory")]
    DeleteMemory,
    #[serde(rename = "get_identity")]
    GetIdentity,
    #[serde(rename = "list_credentials")]
    ListCredentials,
    #[serde(rename = "create_credential")]
    CreateCredential,
    #[serde(rename = "get_credential")]
    GetCredential,
    #[serde(rename = "rotate_credential")]
    RotateCredential,
    #[serde(rename = "revoke_credential")]
    RevokeCredential,
    #[serde(rename = "list_provider_keys")]
    ListProviderKeys,
    #[serde(rename = "create_provider_key")]
    CreateProviderKey,
    #[serde(rename = "get_provider_key")]
    GetProviderKey,
    #[serde(rename = "rotate_provider_key")]
    RotateProviderKey,
    #[serde(rename = "revoke_provider_key")]
    RevokeProviderKey,
    #[serde(rename = "read_usage")]
    ReadUsage,
    #[serde(rename = "read_credits")]
    ReadCredits,
    #[serde(rename = "allocate_credits")]
    AllocateCredits,
    #[serde(rename = "delete_tenant")]
    DeleteTenant,
    #[serde(rename = "register_app")]
    RegisterApp,
    #[serde(rename = "list_apps")]
    ListApps,
    #[serde(rename = "get_app")]
    GetApp,
    #[serde(rename = "update_app")]
    UpdateApp,
    #[serde(rename = "register_org")]
    RegisterOrg,
    #[serde(rename = "list_orgs")]
    ListOrgs,
    #[serde(rename = "get_org")]
    GetOrg,
    #[serde(rename = "update_org")]
    UpdateOrg,
}

impl std::fmt::Display for Operation {
    fn fmt(&self, f: &mut std::fmt::Formatter) -> std::fmt::Result {
        match self {
            Self::CreateInvocation => write!(f, "create_invocation"),
            Self::CreateAgentDefinition => write!(f, "create_agent_definition"),
            Self::GetAgentDefinition => write!(f, "get_agent_definition"),
            Self::ListAgentDefinitions => write!(f, "list_agent_definitions"),
            Self::UpdateAgentDefinition => write!(f, "update_agent_definition"),
            Self::CreateSession => write!(f, "create_session"),
            Self::GetAgent => write!(f, "get_agent"),
            Self::ListAgents => write!(f, "list_agents"),
            Self::GetInvocation => write!(f, "get_invocation"),
            Self::SubmitToolResults => write!(f, "submit_tool_results"),
            Self::CancelInvocation => write!(f, "cancel_invocation"),
            Self::InterruptInvocation => write!(f, "interrupt_invocation"),
            Self::ManageInvocationNudges => write!(f, "manage_invocation_nudges"),
            Self::ResumeInvocation => write!(f, "resume_invocation"),
            Self::ListInvocations => write!(f, "list_invocations"),
            Self::GetSession => write!(f, "get_session"),
            Self::UpdateSession => write!(f, "update_session"),
            Self::DeleteSession => write!(f, "delete_session"),
            Self::ListSessions => write!(f, "list_sessions"),
            Self::ListSessionMessages => write!(f, "list_session_messages"),
            Self::GetSessionTranscript => write!(f, "get_session_transcript"),
            Self::ReadMemories => write!(f, "read_memories"),
            Self::DeleteMemory => write!(f, "delete_memory"),
            Self::GetIdentity => write!(f, "get_identity"),
            Self::ListCredentials => write!(f, "list_credentials"),
            Self::CreateCredential => write!(f, "create_credential"),
            Self::GetCredential => write!(f, "get_credential"),
            Self::RotateCredential => write!(f, "rotate_credential"),
            Self::RevokeCredential => write!(f, "revoke_credential"),
            Self::ListProviderKeys => write!(f, "list_provider_keys"),
            Self::CreateProviderKey => write!(f, "create_provider_key"),
            Self::GetProviderKey => write!(f, "get_provider_key"),
            Self::RotateProviderKey => write!(f, "rotate_provider_key"),
            Self::RevokeProviderKey => write!(f, "revoke_provider_key"),
            Self::ReadUsage => write!(f, "read_usage"),
            Self::ReadCredits => write!(f, "read_credits"),
            Self::AllocateCredits => write!(f, "allocate_credits"),
            Self::DeleteTenant => write!(f, "delete_tenant"),
            Self::RegisterApp => write!(f, "register_app"),
            Self::ListApps => write!(f, "list_apps"),
            Self::GetApp => write!(f, "get_app"),
            Self::UpdateApp => write!(f, "update_app"),
            Self::RegisterOrg => write!(f, "register_org"),
            Self::ListOrgs => write!(f, "list_orgs"),
            Self::GetOrg => write!(f, "get_org"),
            Self::UpdateOrg => write!(f, "update_org"),
        }
    }
}

impl Default for Operation {
    fn default() -> Operation {
        Self::CreateInvocation
    }
}
