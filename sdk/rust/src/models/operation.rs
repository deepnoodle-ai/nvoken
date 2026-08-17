/*
 * nvoken API
 *
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `stream.end` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `stream.end` never speaks about turns. It says this connection is closing and nothing more, so a client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `stream.end` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
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
    #[serde(rename = "get_agent_definition_revision")]
    GetAgentDefinitionRevision,
    #[serde(rename = "list_agent_definitions")]
    ListAgentDefinitions,
    #[serde(rename = "update_agent_definition")]
    UpdateAgentDefinition,
    #[serde(rename = "create_agent")]
    CreateAgent,
    #[serde(rename = "get_agent")]
    GetAgent,
    #[serde(rename = "list_agents")]
    ListAgents,
    #[serde(rename = "update_agent")]
    UpdateAgent,
    #[serde(rename = "archive_agent")]
    ArchiveAgent,
    #[serde(rename = "restore_agent")]
    RestoreAgent,
    #[serde(rename = "create_session")]
    CreateSession,
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
            Self::GetAgentDefinitionRevision => write!(f, "get_agent_definition_revision"),
            Self::ListAgentDefinitions => write!(f, "list_agent_definitions"),
            Self::UpdateAgentDefinition => write!(f, "update_agent_definition"),
            Self::CreateAgent => write!(f, "create_agent"),
            Self::GetAgent => write!(f, "get_agent"),
            Self::ListAgents => write!(f, "list_agents"),
            Self::UpdateAgent => write!(f, "update_agent"),
            Self::ArchiveAgent => write!(f, "archive_agent"),
            Self::RestoreAgent => write!(f, "restore_agent"),
            Self::CreateSession => write!(f, "create_session"),
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
