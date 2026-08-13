/*
 * nvoken API
 *
 * nvoken runs agent turns for you. You describe a turn — an Agent Definition and some input — and nvoken queues it, runs it in the background, keeps running it across restarts and failures, and lets you either watch it live or come back for the result later.  Your application stays in charge of what your agents are and when they run. nvoken owns the conversation: it stores the messages, tracks the state of every turn, and handles talking to the model providers.  ## Getting started  `POST /v1/invocations` starts a turn and returns a `202` right away. From there:  - Follow it live with `GET /v1/invocations/{invocation_id}/stream`, or   read `GET /v1/invocations/{invocation_id}/result` whenever you want the   finished answer. Disconnecting never cancels anything. - If your agent uses tools you run yourself, the turn stops with status   `waiting` and lists what it needs. Run them, post the results to   `/tool-results`, and the turn continues where it left off. - Sessions carry conversation history from one turn to the next. They   last until you delete them, or until a retention window you set runs   out.  Also here: tools nvoken calls back to over HTTPS, remote MCP servers, structured output validated against your JSON Schema, reusable Agent Definitions, image and document input, your own model provider keys, and spending limits.  ## Authorization  Machine credentials use `nvk_…` bearer API keys. A configured trusted console may also present a short-lived Ed25519 issuer token; this is an authentication presentation only and does not create a user identity model in nvoken.  Browser-direct callers use narrow JWTs, exact-origin CORS, client-safe projections, and App/tenant/user admission limits. A host may mint client tokens from its own Ed25519 key. An App that explicitly enables anonymous access may instead let nvoken mint a short-lived access token and renewable visitor token from one allowed public origin.  - App-scoped Runtime credentials may call every Runtime operation and GET /v1/identity. - App-scoped Viewer credentials may call Runtime reads and GET /v1/identity. - App-scoped Operator credentials may call every Runtime operation, GET /v1/identity, and all credential lifecycle operations. - Org-scoped Viewer and Operator credentials are management and reporting   identities only. They resolve no tenant and cannot perform Runtime   operations; Operators can register and manage Apps and their App   credentials, while Viewers have read-only access.  Tenant, Session, operation, and expiry constraints only narrow these grants.  ## Familiar names  Where a name already means something in other agent APIs, nvoken uses it the same way rather than inventing its own. `metadata` follows OpenAI's limits of 16 keys, 64-character names, and 512-byte values. `output_text` is the assistant's text joined into one string. `reasoning.effort` takes `low`, `medium`, `high`, `xhigh`, and `max`. `stop_reason: end_turn`, status `running`, and the `commentary` and `final_answer` message phases are the same idea you have seen elsewhere. If you have integrated another agent API, these should need no translation.  ## You can always read back what applied  Anything nvoken decides on your behalf is readable on the resource that used it. You never have to work out what happened by combining the request you sent with your own assumptions about nvoken's defaults — just read the resource.  A turn reports the `limits` it is really running under, after defaults and minimums; the `agent_definition` it ran with, exactly as stored; and `provenance`, which records what actually served the request. A Session reports its compaction (summarization) policy with `auto` already resolved to a real number and a real model, and its retention window as accepted. New settings will work the same way: a default you cannot read back is a setting only the server knows about.  ## Streaming  A turn and an Invocation are the same thing. The endpoint summaries say turn; the schemas say Invocation.  Two streams carry the same frames. `GET /v1/invocations/{invocation_id}/stream` follows one turn and ends when that turn settles. `GET /v1/sessions/{session_id}/transcript/stream` follows every turn in a Session, and is the surface to use for a conversation. `POST /v1/invocations` with `Accept: text/event-stream` admits and streams one turn inline.  ### Saved frames and live frames  Every frame is one or the other, and the difference decides what you may store. A saved frame carries an SSE `id`. That ID is your resume position and the only value you need to keep. A live frame carries no `id`: it is a preview or a control signal, it is never stored, and it is never replayed.  The Invocation stream's saved frames are `invocation.accepted`, `invocation.update`, and `invocation.result`. The Session stream's only saved frame is `transcript.update`. Every other frame on either stream is live.  ### Resuming and finishing  The resume position has four spellings and one value: the SSE `id` line, `resume_cursor` inside a frame payload, the `cursor` query parameter, and the `Last-Event-ID` header. Send it back as `cursor` or as `Last-Event-ID`; `cursor` wins when a request carries both. Cursors are Session-scoped on both streams, so a position taken from one stream resumes the other.  Reconnecting to a turn that has already settled always yields `invocation.result` followed by `stream.end` with reason `terminal`, at any cursor. Both are valid signals that a turn is over, and a client may exit on either.  `invocation.accepted` is emitted only by the inline `POST` path. The `GET` stream never sends it, so a client that admits separately never sees it. The nvoken SDKs synthesize an equivalent locally so their callers see the same first event either way.  An `invocation.update` never carries a terminal status. Terminal state arrives as `invocation.result` and nowhere else on that stream. The `invocation` it carries is re-read when the frame is written, so it is current state with a resume position attached rather than a snapshot taken at the cursor.  ### Previews  `output_text.delta` and `thinking.delta` preview one model iteration. Their identity is `(invocation_id, attempt, iteration, content_index)`. Accumulate by that tuple, and discard everything provisional when `attempt` increases, when `stream.resync` arrives, when the saved message lands, and when the turn reaches a terminal status. One model iteration produces exactly one saved assistant message, so previews sharing an `(invocation_id, attempt, iteration)` build one message. Never store preview text as a message, and never use it to decide whether a turn succeeded.  `attempt`, `iteration`, `revision`, and `sequence` count from 1. `content_index` counts from 0.  ### Compatibility  Stream events may gain fields over time. Ignore fields you do not recognize rather than refusing the frame. New enum values may appear too. Treat an unknown `stream.resync` reason as `live_delivery_gap` and discard your previews. Treat an unknown `stream.end` reason as `rotate` and reconnect with your last saved `id`. Reconnecting is always safe: a turn that has settled re-yields its result.  ### Transport  The protocol is the frames and their durability rules. Server-Sent Events is how they travel today and the only binding. Three mechanics belong to SSE itself: the `id:` line, the `retry:` opener, and comment keepalives. Everything else, resumption included, lives in the frames.  ### What the stream carries  Frames carry structure and text. Images and documents travel as descriptors, with media type, size in bytes, and a `sha256:` digest, never as inline bytes. Frame sizes stay bounded by text no matter what a turn produced.
 *
 * The version of the OpenAPI document: 0.1.0
 *
 * Generated by: https://openapi-generator.tech
 */

use super::{configuration, ContentType, Error};
use crate::{apis::ResponseContent, models};
use reqwest;
use serde::{de::Error as _, Deserialize, Serialize};

/// struct for typed errors of method [`get_usage_breakdown`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum GetUsageBreakdownError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`get_usage_timeseries`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum GetUsageTimeseriesError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

/// struct for typed errors of method [`list_usage_records`]
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(untagged)]
pub enum ListUsageRecordsError {
    Status400(models::ErrorResponse),
    Status401(models::ErrorResponse),
    Status403(models::ErrorResponse),
    Status500(models::ErrorResponse),
    Status503(models::ErrorResponse),
    UnknownValue(serde_json::Value),
}

pub async fn get_usage_breakdown(
    configuration: &configuration::Configuration,
    start_at: chrono::DateTime<chrono::FixedOffset>,
    end_at: chrono::DateTime<chrono::FixedOffset>,
    group_by: &str,
    app_id: Option<&str>,
    tenant_key: Option<&str>,
    user_key: Option<&str>,
    agent_id: Option<&str>,
    provider: Option<&str>,
    model: Option<&str>,
    provider_key_source: Option<models::ProviderKeySource>,
    provider_key_id: Option<&str>,
    credential_family_id: Option<&str>,
    authentication_method: Option<models::AuthenticationMethod>,
    call_kind: Option<models::ModelCallKind>,
    tool_name: Option<&str>,
    tool_mode: Option<models::ToolCallMode>,
    sort: Option<&str>,
    cursor: Option<&str>,
    limit: Option<u32>,
) -> Result<models::UsageBreakdown, Error<GetUsageBreakdownError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_query_start_at = start_at;
    let p_query_end_at = end_at;
    let p_query_group_by = group_by;
    let p_query_app_id = app_id;
    let p_query_tenant_key = tenant_key;
    let p_query_user_key = user_key;
    let p_query_agent_id = agent_id;
    let p_query_provider = provider;
    let p_query_model = model;
    let p_query_provider_key_source = provider_key_source;
    let p_query_provider_key_id = provider_key_id;
    let p_query_credential_family_id = credential_family_id;
    let p_query_authentication_method = authentication_method;
    let p_query_call_kind = call_kind;
    let p_query_tool_name = tool_name;
    let p_query_tool_mode = tool_mode;
    let p_query_sort = sort;
    let p_query_cursor = cursor;
    let p_query_limit = limit;

    let uri_str = format!("{}/v1/usage/breakdown", configuration.base_path);
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    req_builder = req_builder.query(&[("start_at", &p_query_start_at.to_string())]);
    req_builder = req_builder.query(&[("end_at", &p_query_end_at.to_string())]);
    if let Some(ref param_value) = p_query_app_id {
        req_builder = req_builder.query(&[("app_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tenant_key {
        req_builder = req_builder.query(&[("tenant_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_user_key {
        req_builder = req_builder.query(&[("user_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_agent_id {
        req_builder = req_builder.query(&[("agent_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider {
        req_builder = req_builder.query(&[("provider", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_model {
        req_builder = req_builder.query(&[("model", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider_key_source {
        req_builder = req_builder.query(&[("provider_key_source", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider_key_id {
        req_builder = req_builder.query(&[("provider_key_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_credential_family_id {
        req_builder = req_builder.query(&[("credential_family_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_authentication_method {
        req_builder = req_builder.query(&[("authentication_method", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_call_kind {
        req_builder = req_builder.query(&[("call_kind", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tool_name {
        req_builder = req_builder.query(&[("tool_name", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tool_mode {
        req_builder = req_builder.query(&[("tool_mode", &param_value.to_string())]);
    }
    req_builder = req_builder.query(&[("group_by", &p_query_group_by.to_string())]);
    if let Some(ref param_value) = p_query_sort {
        req_builder = req_builder.query(&[("sort", &param_value.to_string())]);
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
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::UsageBreakdown`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::UsageBreakdown`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<GetUsageBreakdownError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Returns activity, model, tool, and model-cost metrics from retained, content-free facts. The half-open window totals use exact distinct counts and are not sums of bucket distincts. Grouping is bounded to ten selected series plus `other`. Session deletion does not rewrite history. An App credential is forced to its App, an Org credential to Apps currently owned by its Org, and only an installation-scoped admin issuer token can span every App.
pub async fn get_usage_timeseries(
    configuration: &configuration::Configuration,
    start_at: chrono::DateTime<chrono::FixedOffset>,
    end_at: chrono::DateTime<chrono::FixedOffset>,
    interval: models::UsageInterval,
    timezone: Option<&str>,
    app_id: Option<&str>,
    tenant_key: Option<&str>,
    user_key: Option<&str>,
    agent_id: Option<&str>,
    provider: Option<&str>,
    model: Option<&str>,
    provider_key_source: Option<models::ProviderKeySource>,
    provider_key_id: Option<&str>,
    credential_family_id: Option<&str>,
    authentication_method: Option<models::AuthenticationMethod>,
    call_kind: Option<models::ModelCallKind>,
    tool_name: Option<&str>,
    tool_mode: Option<models::ToolCallMode>,
    group_by: Option<&str>,
    top: Option<u32>,
    keys: Option<&str>,
) -> Result<models::UsageTimeseries, Error<GetUsageTimeseriesError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_query_start_at = start_at;
    let p_query_end_at = end_at;
    let p_query_interval = interval;
    let p_query_timezone = timezone;
    let p_query_app_id = app_id;
    let p_query_tenant_key = tenant_key;
    let p_query_user_key = user_key;
    let p_query_agent_id = agent_id;
    let p_query_provider = provider;
    let p_query_model = model;
    let p_query_provider_key_source = provider_key_source;
    let p_query_provider_key_id = provider_key_id;
    let p_query_credential_family_id = credential_family_id;
    let p_query_authentication_method = authentication_method;
    let p_query_call_kind = call_kind;
    let p_query_tool_name = tool_name;
    let p_query_tool_mode = tool_mode;
    let p_query_group_by = group_by;
    let p_query_top = top;
    let p_query_keys = keys;

    let uri_str = format!("{}/v1/usage/timeseries", configuration.base_path);
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    req_builder = req_builder.query(&[("start_at", &p_query_start_at.to_string())]);
    req_builder = req_builder.query(&[("end_at", &p_query_end_at.to_string())]);
    req_builder = req_builder.query(&[("interval", &p_query_interval.to_string())]);
    if let Some(ref param_value) = p_query_timezone {
        req_builder = req_builder.query(&[("timezone", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_app_id {
        req_builder = req_builder.query(&[("app_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tenant_key {
        req_builder = req_builder.query(&[("tenant_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_user_key {
        req_builder = req_builder.query(&[("user_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_agent_id {
        req_builder = req_builder.query(&[("agent_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider {
        req_builder = req_builder.query(&[("provider", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_model {
        req_builder = req_builder.query(&[("model", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider_key_source {
        req_builder = req_builder.query(&[("provider_key_source", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider_key_id {
        req_builder = req_builder.query(&[("provider_key_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_credential_family_id {
        req_builder = req_builder.query(&[("credential_family_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_authentication_method {
        req_builder = req_builder.query(&[("authentication_method", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_call_kind {
        req_builder = req_builder.query(&[("call_kind", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tool_name {
        req_builder = req_builder.query(&[("tool_name", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tool_mode {
        req_builder = req_builder.query(&[("tool_mode", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_group_by {
        req_builder = req_builder.query(&[("group_by", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_top {
        req_builder = req_builder.query(&[("top", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_keys {
        req_builder = req_builder.query(&[("keys", &param_value.to_string())]);
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
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::UsageTimeseries`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::UsageTimeseries`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<GetUsageTimeseriesError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}

/// Stable ascending `(started_at, id)` order; JSON and CSV contain the same logical columns and never content.
pub async fn list_usage_records(
    configuration: &configuration::Configuration,
    start_at: chrono::DateTime<chrono::FixedOffset>,
    end_at: chrono::DateTime<chrono::FixedOffset>,
    app_id: Option<&str>,
    tenant_key: Option<&str>,
    user_key: Option<&str>,
    agent_id: Option<&str>,
    provider: Option<&str>,
    model: Option<&str>,
    provider_key_source: Option<models::ProviderKeySource>,
    provider_key_id: Option<&str>,
    credential_family_id: Option<&str>,
    authentication_method: Option<models::AuthenticationMethod>,
    call_kind: Option<models::ModelCallKind>,
    tool_name: Option<&str>,
    tool_mode: Option<models::ToolCallMode>,
    cursor: Option<&str>,
    limit: Option<u32>,
    format: Option<&str>,
) -> Result<models::UsageRecords, Error<ListUsageRecordsError>> {
    // add a prefix to parameters to efficiently prevent name collisions
    let p_query_start_at = start_at;
    let p_query_end_at = end_at;
    let p_query_app_id = app_id;
    let p_query_tenant_key = tenant_key;
    let p_query_user_key = user_key;
    let p_query_agent_id = agent_id;
    let p_query_provider = provider;
    let p_query_model = model;
    let p_query_provider_key_source = provider_key_source;
    let p_query_provider_key_id = provider_key_id;
    let p_query_credential_family_id = credential_family_id;
    let p_query_authentication_method = authentication_method;
    let p_query_call_kind = call_kind;
    let p_query_tool_name = tool_name;
    let p_query_tool_mode = tool_mode;
    let p_query_cursor = cursor;
    let p_query_limit = limit;
    let p_query_format = format;

    let uri_str = format!("{}/v1/usage/records", configuration.base_path);
    let mut req_builder = configuration.client.request(reqwest::Method::GET, &uri_str);

    req_builder = req_builder.query(&[("start_at", &p_query_start_at.to_string())]);
    req_builder = req_builder.query(&[("end_at", &p_query_end_at.to_string())]);
    if let Some(ref param_value) = p_query_app_id {
        req_builder = req_builder.query(&[("app_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tenant_key {
        req_builder = req_builder.query(&[("tenant_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_user_key {
        req_builder = req_builder.query(&[("user_key", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_agent_id {
        req_builder = req_builder.query(&[("agent_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider {
        req_builder = req_builder.query(&[("provider", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_model {
        req_builder = req_builder.query(&[("model", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider_key_source {
        req_builder = req_builder.query(&[("provider_key_source", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_provider_key_id {
        req_builder = req_builder.query(&[("provider_key_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_credential_family_id {
        req_builder = req_builder.query(&[("credential_family_id", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_authentication_method {
        req_builder = req_builder.query(&[("authentication_method", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_call_kind {
        req_builder = req_builder.query(&[("call_kind", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tool_name {
        req_builder = req_builder.query(&[("tool_name", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_tool_mode {
        req_builder = req_builder.query(&[("tool_mode", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_cursor {
        req_builder = req_builder.query(&[("cursor", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_limit {
        req_builder = req_builder.query(&[("limit", &param_value.to_string())]);
    }
    if let Some(ref param_value) = p_query_format {
        req_builder = req_builder.query(&[("format", &param_value.to_string())]);
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
            ContentType::Text => return Err(Error::from(serde_json::Error::custom("Received `text/plain` content type response that cannot be converted to `models::UsageRecords`"))),
            ContentType::Unsupported(unknown_type) => return Err(Error::from(serde_json::Error::custom(format!("Received `{unknown_type}` content type response that cannot be converted to `models::UsageRecords`")))),
        }
    } else {
        let content = resp.text().await?;
        let entity: Option<ListUsageRecordsError> = serde_json::from_str(&content).ok();
        Err(Error::ResponseError(ResponseContent {
            status,
            content,
            entity,
        }))
    }
}
