/*
 * nvoken API
 *
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/sessions/{session_id}/stream`, or read   `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  A browser grant sees less, and it sees it in the same shape. There is one schema per resource, and the fields a browser may not see are simply omitted from its responses, which is why those fields are not required. There are no parallel browser schemas and no response unions, so every payload decodes against one type and nothing about the shape has to be inferred from the credential that fetched it. `GET /v1/identity` reports which kind of caller you are, under `authentication.method`.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  There is one stream: `GET /v1/sessions/{session_id}/stream`. It carries every turn in the Session, which is what a conversation needs. Add `invocation_id` to narrow it to one turn. That is a filter on one log, not a second endpoint, because cursors were always Session-scoped and a position from a filtered read resumes an unfiltered one.  Admission is separate from streaming. `POST /v1/invocations` returns the turn, and that response is your acknowledgment; then you open the stream. A dropped connection costs you nothing, because the turn already exists and you already hold its ID.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  `transcript.update` is the one saved frame. `message.delta`, `stream.resync`, and `stream.end` are live.  ### Folding a transcript.update  It carries `cursor`, `messages`, and `invocation_changes`. Messages append by `sequence` and are never sent twice. Changes are an append log keyed by `(invocation_id, revision)`; fold to the highest revision for current state. Within one frame apply messages before changes, so a turn is never marked settled before its final message exists.  ### Resuming and finishing  The resume position has one name: `cursor`. It is the field on a durable frame and the query parameter that resumes a stream. Server-Sent Events mirrors it onto the `id:` line and accepts the `Last-Event-ID` header in the parameter's place, because a faithful SSE binding must; those are the binding's mechanics, not two more names for the value, and another binding would carry the same one name. `cursor` wins when a request supplies both.  **A turn is over when a change for it carries a terminal status.** That is the terminal signal, and there is no other. It is saved, so it replays on reconnect like every other change, at any cursor. Read `GET /v1/invocations/{invocation_id}` if you want the composed result.  The change tells you so directly: `terminal` is true on exactly the change that ends the turn. Read it instead of testing `status` against a set of your own, so a status added later cannot silently change what your code believes a turn's end is.  `stream.end` never speaks about turns. It says this connection is closing and nothing more, so a client that has not seen its terminal change reconnects and keeps reading.  ### The stream stays open  An unfiltered stream is a subscription. It stays open while the Session is idle, keepalives and all, and a turn started later by anyone appears on it. You do not poll. A server may still reclaim an idle connection, and when it does it says `stream.end` with reason `idle`, which means reconnect when you next need to read rather than immediately.  A filtered stream closes once it has delivered that turn's terminal change. A client following the exit rule has already left.  ### Previews  `message.delta` previews a message the model is writing. `kind` says what kind of content it is and `delta` carries the fragment, for every kind. Accumulate by `(message_id, content_index)`, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message with that ID lands, and when the turn's change carries a terminal status. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `message.delta` kind as content you do not render. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 * Generated by: https://openapi-generator.tech
 */

use super::{configuration, ContentType, Error};
use crate::{apis::ResponseContent, models};
use reqwest;
use serde::{de::Error as _, Deserialize, Serialize};

/// struct for typed errors of method [`cancel_invocation`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum CancelInvocationError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`cancel_nudge`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum CancelNudgeError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status409(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`create_invocation`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum CreateInvocationError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status409(models::ErrorResponse),
    Status422(models::ErrorResponse),
    Status429(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`create_nudge`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum CreateNudgeError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status409(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`get_invocation`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum GetInvocationError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status429(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`get_invocation_result`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum GetInvocationResultError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status429(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`get_invocation_timeline`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum GetInvocationTimelineError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`get_trace`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum GetTraceError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`interrupt_invocation`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum InterruptInvocationError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`list_invocation_logs`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ListInvocationLogsError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`list_invocation_traces`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ListInvocationTracesError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`list_invocations`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ListInvocationsError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status429(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`list_nudges`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ListNudgesError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`list_tool_calls`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ListToolCallsError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status429(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`resume_invocation`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ResumeInvocationError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status409(models::ErrorResponse),
    Status429(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`submit_host_tool_results`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum SubmitHostToolResultsError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status404(models::ErrorResponse),
    Status409(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// Stops a turn and discards what it produced. The turn ends `cancelled` and its work does not carry into the next turn — use interrupt instead if you want to keep it.  Safe to repeat. Cancelling a turn that already finished returns it unchanged rather than failing. A successful response means the cancellation is recorded and will stick. Work already sent to the model provider stops as soon as it can, so you may still be billed for what had run by then.  Send an empty request body.
pub async fn cancel_invocation(
    configuration: &configuration::Configuration,
    invocation_id: &str,
) -> Result<models::Invocation, Error<CancelInvocationError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/cancel",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration
        .client
        .request(reqwest::Method::POST, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::Invocation`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::Invocation`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<CancelInvocationError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Withdraws direction you sent with `/nudges`, as long as the turn has not picked it up yet. Cancelling something already cancelled returns it unchanged, so retrying is safe.  Cancelling races the turn, and whichever happens first wins outright: you either withdraw it cleanly or the turn uses it. It is never half-applied. If the turn got there first, you get a conflict and the entry stays `drained`.
pub async fn cancel_nudge(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    nudge_id: &str,
) -> Result<models::Nudge, Error<CancelNudgeError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_path_nudge_id = nudge_id;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/nudges/{nudge_id}/cancel",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id),
        nudge_id = crate::apis::urlencode(p_path_nudge_id)
    );
    let mut req_builder = configuration
        .client
        .request(reqwest::Method::POST, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::Nudge`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::Nudge`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<CancelNudgeError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Starts one agent turn and returns immediately. In a single database transaction nvoken resolves the deliberately created Agent, selects its Agent Definition revision, finds or creates the Session, appends your input as one message, and queues the turn. Admission never creates an Agent or reusable configuration. You get a response only after that transaction commits, so a `202` means the work is safely recorded and will run even if nvoken restarts. The model does not run on this request — it runs in the background, and you follow it with the stream or by polling.  Pick the Session with either `session_id` or `session_key`, not both. A Session ID must belong to the Agent you named, or to a Session created without an Agent — in which case this turn binds that Agent permanently. An App credential without a tenant constraint may omit `tenant_key` and use whichever tenant the Session already belongs to. A credential locked to one tenant cannot reach another; naming a different one returns `403 forbidden` without revealing whether the resource exists.  ## Retrying safely  Send `idempotency_key` and you can retry this request without risking a second turn. A repeat with the same key returns the original turn and does not add your input again, even if that turn has already finished. Keys are scoped to the tenant and Agent.  A repeat counts as the same request only if the Session selector, the Agent, explicit revision, per-turn overrides, and input all match. The original admitted revision is returned even if its Definition has advanced. Values are compared as sent, so omitting an override is not the same as supplying one that happens to equal the Definition. Key order inside JSON objects does not matter; array order does. Change anything material and you get `idempotency_conflict` rather than a surprise second turn.  ## When the Session is already busy  A Session runs one turn at a time, and `if_active` decides what happens when you start another. The default, `reject`, returns `session_invocation_active`.  `supersede` cancels the running turn and starts yours in its place, atomically — there is no moment where the Session has no turn or two turns. It requires permission to both create and cancel. Retrying the same request returns your original turn and never cancels newer work that started in the meantime.  `interrupt` needs the same permission but stops the running turn cleanly instead of discarding its work. If that turn can stop immediately, yours starts in the same transaction. If it is mid-step, nvoken records the interrupt and this request waits for it. If it has not stopped by the time the wait is up, you get `session_invocation_active` with `details.interrupt_requested = true` — the interrupt is still in effect, so just send the request again.  ## Retired models  A deprecated model keeps working. On and after its `retires_at` date, new turns are refused with `422 model_retired`, and `details` tells you what to do about it: the `model` you asked for, its `retires_at` date, the exact `replacement` provider and id to switch to, and the request `path`. Retrying an idempotency key from before the retirement still returns that original turn.  ## Size limits  A text-only body may be up to 1 MiB. A body with images or documents may be up to 24 MiB, and within that: at most 8 media blocks, 16 MiB of decoded media in total, 5 MiB per image, and 16 MiB per document. Anything over these is rejected before a turn is created.  URLs are fetched after the idempotency check and before anything is saved, so a retry does not download twice. nvoken accepts public HTTPS only, stops reading at the size limit, and checks what the bytes actually are. It stores them and never fetches the URL again.  ## Streaming  This response is the acknowledgment. Once you hold the returned `id`, follow the turn with `GET /v1/sessions/{session_id}/stream?invocation_id=…`. Admission and streaming are separate requests on purpose: a dropped stream costs you nothing, because the turn already exists and no reconnect can create a second one.
pub async fn create_invocation(
    configuration: &configuration::Configuration,
    create_invocation_request: models::CreateInvocationRequest,
    x_anthropic_api_key: Option<&str>,
    x_openai_api_key: Option<&str>,
    x_gemini_api_key: Option<&str>,
    x_xai_api_key: Option<&str>,
) -> Result<models::Invocation, Error<CreateInvocationError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_body_create_invocation_request = create_invocation_request;
    let p_header_x_anthropic_api_key = x_anthropic_api_key;
    let p_header_x_openai_api_key = x_openai_api_key;
    let p_header_x_gemini_api_key = x_gemini_api_key;
    let p_header_x_xai_api_key = x_xai_api_key;

    let uri_str = format!("{}/v1/invocations", configuration.base_path);
    let mut req_builder = configuration
        .client
        .request(reqwest::Method::POST, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(param_value) = p_header_x_anthropic_api_key {
        req_builder = req_builder.header("X-Anthropic-Api-Key", param_value.to_string());
    }
    if let Some(param_value) = p_header_x_openai_api_key {
        req_builder = req_builder.header("X-Openai-Api-Key", param_value.to_string());
    }
    if let Some(param_value) = p_header_x_gemini_api_key {
        req_builder = req_builder.header("X-Gemini-Api-Key", param_value.to_string());
    }
    if let Some(param_value) = p_header_x_xai_api_key {
        req_builder = req_builder.header("X-Xai-Api-Key", param_value.to_string());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };
    req_builder = req_builder.json(&p_body_create_invocation_request);

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::Invocation`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::Invocation`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<CreateInvocationError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Sends extra direction to a turn that is already running — \"focus on the marine segment\" — without stopping it and without losing the work you are steering. Use this when a long turn is heading the wrong way and you want to correct it in place.  Compare with `if_active: supersede` on a new Invocation, which replaces the running turn and discards what it had produced. Steering a long turn that way throws away exactly the work you were trying to redirect.  **A nudge is not an interrupt, and it is not immediate.** The turn picks it up at its next clean stopping point: when it starts its next step, when it pauses for you to run a tool, or when a turn that thought it was finished re-enters its loop to answer you. A model call or tool run already in flight is never aborted to deliver it. A turn you have interrupted is never given more work — the interrupt wins and the direction you staged expires unused.  Nudges and Invocations never turn into each other. Posting to `/v1/invocations` against a busy Session behaves exactly as its `if_active` setting says; it never quietly becomes a nudge, and a nudge never quietly becomes a new turn.  If the turn ends without ever picking it up, your Nudge is marked `expired` at that moment and has no effect on any later turn. Check `GET .../nudges` to see whether it was used or missed. Whether to re-send missed direction as the next turn's input is your call.  `content` must be text — a string, or an array of text blocks. Images and documents are fine on a turn's own input but are refused here, because a turn resuming in place carries text only, and silently dropping your attachment would be worse than telling you now.  Requires the same permission as cancelling the turn.
pub async fn create_nudge(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    create_nudge_request: models::CreateNudgeRequest,
) -> Result<models::NudgeAcknowledgement, Error<CreateNudgeError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_body_create_nudge_request = create_nudge_request;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/nudges",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration
        .client
        .request(reqwest::Method::POST, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };
    req_builder = req_builder.json(&p_body_create_nudge_request);

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::NudgeAcknowledgement`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::NudgeAcknowledgement`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<CreateNudgeError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// The turn's current state, including anything that went wrong after it started.  A credential that can authenticate but lacks permission for this read gets `forbidden`. A turn belonging to another tenant is reported as `not_found` rather than `forbidden`, so you cannot use this endpoint to discover whether an ID exists outside your scope.
pub async fn get_invocation(
    configuration: &configuration::Configuration,
    invocation_id: &str,
) -> Result<models::Invocation, Error<GetInvocationError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::Invocation`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::Invocation`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<GetInvocationError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Returns the turn and the messages it produced, at any status. This is the convenient read for \"what did the agent say?\" — `output_text` gives you the assistant's text already joined into a single string, so you do not have to walk the message blocks yourself.  The turn and its messages are read from one consistent database snapshot, so you will never see a finished turn whose last message is missing.  Authentication, tenant scoping, and the not-found behavior are the same as reading the Invocation on its own.
pub async fn get_invocation_result(
    configuration: &configuration::Configuration,
    invocation_id: &str,
) -> Result<models::InvocationResult, Error<GetInvocationResultError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/result",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::InvocationResult`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::InvocationResult`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<GetInvocationResultError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Assembles lifecycle waits, model calls, tool calls, nudges, and compactions from one database snapshot. It contains timings and usage, never prompts, responses, tool arguments, results, or error text. After Session erasure it degrades to the retained facts-only skeleton.
pub async fn get_invocation_timeline(
    configuration: &configuration::Configuration,
    invocation_id: &str,
) -> Result<models::InvocationTimeline, Error<GetInvocationTimelineError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/timeline",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::InvocationTimeline`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::InvocationTimeline`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<GetInvocationTimelineError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Returns a content-free projection of up to 200 OpenTelemetry spans. Use the pageable Invocation log endpoint with `trace_id` for associated logs. `is_partial` says when the agent root has not arrived or the bounded read omitted spans. nvoken grounds the trace's Invocation attribution in its durable Invocation record before returning it; knowing a W3C trace ID grants no authority.
pub async fn get_trace(
    configuration: &configuration::Configuration,
    trace_id: &str,
) -> Result<models::Trace, Error<GetTraceError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_trace_id = trace_id;

    let uri_str = format!(
        "{}/v1/traces/{trace_id}",
        configuration.base_path,
        trace_id = crate::apis::urlencode(p_path_trace_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::Trace`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::Trace`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<GetTraceError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Asks a running turn to stop at its next clean stopping point. It ends `completed` with `stop_reason: interrupted`, and everything it produced — the model's replies and any tool results — stays in the conversation for the next turn. That is the whole difference from cancelling, which throws the turn's work away.  The request is recorded and safe to repeat. What happens next depends on what the turn was doing:  - Between steps (`queued`, `waiting`, or `running` with nothing   actively executing) it stops before this call returns. Any tool   calls you still owed results for are closed out, so submitting one   afterwards returns `409`. - Mid-step, nvoken records the request and returns the turn still   `running`. It stops at the next checkpoint, at worst one model call   later. Watch the stream or re-read the turn to see it end.  Interrupting a turn that has already finished changes nothing and returns it as-is. A turn that was asked for structured output but never produced a valid object ends `failed` with `structured_output_unsatisfied` rather than `completed`. Either way usage is reported in full and billed, because the work was kept.  Send an empty request body.
pub async fn interrupt_invocation(
    configuration: &configuration::Configuration,
    invocation_id: &str,
) -> Result<models::Invocation, Error<InterruptInvocationError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/interrupt",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration
        .client
        .request(reqwest::Method::POST, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::Invocation`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::Invocation`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<InterruptInvocationError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Returns the content-free structured lifecycle logs associated by the Invocation ID. Arbitrary attributes and raw error values are omitted. `status` is `disabled` when this installation has no configured observation store.
pub async fn list_invocation_logs(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    cursor: Option<&str>,
    limit: Option<u32>,
    trace_id: Option<&str>,
) -> Result<models::InvocationLogList, Error<ListInvocationLogsError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_query_cursor = cursor;
    let p_query_limit = limit;
    let p_query_trace_id = trace_id;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/logs",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref param_value) = p_query_cursor {
        req_builder = req_builder.query(&[("cursor", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_limit {
        req_builder = req_builder.query(&[("limit", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_trace_id {
        req_builder = req_builder.query(&[("trace_id", &param_value.to_string())]);
    }
    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::InvocationLogList`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::InvocationLogList`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<ListInvocationLogsError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Returns newest-first, content-free summaries exported from Dive through OpenTelemetry. A child-only trace is returned as `is_partial: true` while its agent root is still open or if the process exits before that root is exported. Traces remain diagnostic and best-effort; the durable Invocation timeline is the execution authority. `status` is `disabled` when this installation has no configured observation store.
pub async fn list_invocation_traces(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    cursor: Option<&str>,
    limit: Option<u32>,
) -> Result<models::TraceList, Error<ListInvocationTracesError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_query_cursor = cursor;
    let p_query_limit = limit;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/traces",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref param_value) = p_query_cursor {
        req_builder = req_builder.query(&[("cursor", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_limit {
        req_builder = req_builder.query(&[("limit", &param_value.to_string())]);
    }
    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::TraceList`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::TraceList`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<ListInvocationTracesError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Returns newest-first durable Invocation state. Exact filters combine with AND. An App credential without a tenant constraint may list all tenant partitions in that App, one named partition with `tenant_key`, or the default partition with `default_tenant=true`. A tenant-constrained credential is always scoped to its partition. The opaque cursor is bound to the normalized filter set and credential tenant scope. `agent_id` and `agent_key` are mutually exclusive; both normalize to the resolved Agent ID for cursor binding, so an equivalent cursor may resume under either spelling.
pub async fn list_invocations(
    configuration: &configuration::Configuration,
    tenant_key: Option<&str>,
    default_tenant: Option<bool>,
    user_key: Option<&str>,
    session_id: Option<&str>,
    agent_id: Option<&str>,
    agent_key: Option<&str>,
    status: Option<Vec<models::InvocationStatus>>,
    cursor: Option<&str>,
    limit: Option<u32>,
) -> Result<models::InvocationList, Error<ListInvocationsError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_query_tenant_key = tenant_key;
    let p_query_default_tenant = default_tenant;
    let p_query_user_key = user_key;
    let p_query_session_id = session_id;
    let p_query_agent_id = agent_id;
    let p_query_agent_key = agent_key;
    let p_query_status = status;
    let p_query_cursor = cursor;
    let p_query_limit = limit;

    let uri_str = format!("{}/v1/invocations", configuration.base_path);
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref param_value) = p_query_tenant_key {
        req_builder = req_builder.query(&[("tenant_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_default_tenant {
        req_builder = req_builder.query(&[("default_tenant", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_user_key {
        req_builder = req_builder.query(&[("user_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_session_id {
        req_builder = req_builder.query(&[("session_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_agent_id {
        req_builder = req_builder.query(&[("agent_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_agent_key {
        req_builder = req_builder.query(&[("agent_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_status {
        req_builder = match "multi" {
            "multi" => req_builder.query(
                &param_value
                    .into_iter()
                    .map(|p| ("status".to_owned(), p.to_string()))
                    .collect::<Vec<(std::string::String, std::string::String)>>(),
            ),
            _ => req_builder.query(&[(
                "status",
                &param_value
                    .into_iter()
                    .map(|p| p.to_string())
                    .collect::<Vec<String>>()
                    .join(",")
                    .to_string(),
            )]),
        };
    }
    if let Some(ref param_value) = p_query_cursor {
        req_builder = req_builder.query(&[("cursor", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_limit {
        req_builder = req_builder.query(&[("limit", &param_value.to_string())]);
    }
    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::InvocationList`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::InvocationList`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<ListInvocationsError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Lists the direction you have sent to this turn with `/nudges`, in the order the turn will pick it up. Entries stay listed after they are used or missed, so you can answer \"what did the user say, and did the model ever see it?\"  Check `status` on each entry: `drained` means the turn used it, `expired` means the turn ended first, `cancelled` means you withdrew it.
pub async fn list_nudges(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    status: Option<models::NudgeStatus>,
    cursor: Option<&str>,
    limit: Option<u32>,
) -> Result<models::NudgeList, Error<ListNudgesError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_query_status = status;
    let p_query_cursor = cursor;
    let p_query_limit = limit;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/nudges",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref param_value) = p_query_status {
        req_builder = req_builder.query(&[("status", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_cursor {
        req_builder = req_builder.query(&[("cursor", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_limit {
        req_builder = req_builder.query(&[("limit", &param_value.to_string())]);
    }
    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::NudgeList`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::NudgeList`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<ListNudgesError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Returns ToolCalls in execution discovery order. Every execution mode is included. The records contain lifecycle and timing facts only. Tool inputs and results remain in the canonical Session transcript.  Callback records include a delivery object. Its terminal outcome, attempt count, and last HTTP status remain available after the bounded delivery transport row is pruned. These records use the same authentication, tenant scope, Session constraint, and nondisclosing not_found behavior as the Invocation read. Deleting the Session deletes these records with the rest of its subtree.
pub async fn list_tool_calls(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    cursor: Option<&str>,
    limit: Option<u32>,
) -> Result<models::ToolCallList, Error<ListToolCallsError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_query_cursor = cursor;
    let p_query_limit = limit;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/tool-calls",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    if let Some(ref param_value) = p_query_cursor {
        req_builder = req_builder.query(&[("cursor", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_limit {
        req_builder = req_builder.query(&[("limit", &param_value.to_string())]);
    }
    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::ToolCallList`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::ToolCallList`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<ListToolCallsError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Continues a turn that paused because one of its own spending limits ran out. Send `limits` containing only the limit that ran out, raised above both its old value and what the turn has already used, and still within what your installation allows.  If the turn paused because the tenant ran out of credits rather than on a limit of its own, allocate credits to that account instead — this endpoint refuses it, and funding the account continues the turn on its own. Deadlines never pause a turn, so they never bring you here.
pub async fn resume_invocation(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    resume_invocation_request: models::ResumeInvocationRequest,
) -> Result<models::Invocation, Error<ResumeInvocationError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_body_resume_invocation_request = resume_invocation_request;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/resume",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration
        .client
        .request(reqwest::Method::POST, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };
    req_builder = req_builder.json(&p_body_resume_invocation_request);

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::Invocation`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::Invocation`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<ResumeInvocationError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Atomically accepts one bounded batch for a waiting Invocation. The first committed result for each ToolCall wins. An equal replay is acknowledged as deduplicated; a changed replay conflicts. Partial batches leave the Invocation waiting. Closing the final pending call queues the same Invocation and its successor dispatch before returning `202`.  This command accepts only host- or callback-mode calls owned by the path Invocation and authenticated tenant scope. It is not a generic Session append endpoint. The body is limited to 1 MiB; each result content value is valid JSON limited to 256 KiB and 32 nesting levels.  `content` accepts any JSON value and the stored transcript retains it verbatim. Before a result reaches the model, a string or an array of content blocks passes through unchanged; any other value is serialized to its compact JSON text and sent as a string, so the model sees the same bytes a host that pre-stringifies would send.
pub async fn submit_host_tool_results(
    configuration: &configuration::Configuration,
    invocation_id: &str,
    submit_host_tool_results_request: models::SubmitHostToolResultsRequest,
) -> Result<models::SubmitHostToolResultsResponse, Error<SubmitHostToolResultsError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_path_invocation_id = invocation_id;
    let p_body_submit_host_tool_results_request = submit_host_tool_results_request;

    let uri_str = format!(
        "{}/v1/invocations/{invocation_id}/tool-results",
        configuration.base_path,
        invocation_id = crate::apis::urlencode(p_path_invocation_id)
    );
    let mut req_builder = configuration
        .client
        .request(reqwest::Method::POST, &uri_str);

    if let Some(ref user_agent) = configuration.user_agent {
        req_builder = req_builder.header(reqwest::header::USER_AGENT, user_agent.clone());
    }
    if let Some(ref token) = configuration.bearer_access_token {
        req_builder = req_builder.bearer_auth(token.to_owned());
    };
    req_builder = req_builder.json(&p_body_submit_host_tool_results_request);

    let req = req_builder.build()?;
    let resp = configuration.client.execute(req).await?;

    let status = resp.status();
    let content_type = resp
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream");
    let content_type = super::ContentType::from(content_type);

    if !status.is_client_error() && !status.is_server_error() {
        let content = resp.text().await?;
        match content_type {
            ContentType::Json => serde_json::from_str(&content).map_err(Error::from),
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::SubmitHostToolResultsResponse`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::SubmitHostToolResultsResponse`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<SubmitHostToolResultsError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}
