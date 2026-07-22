use std::collections::HashMap;
use std::fmt::Debug;
use std::sync::Arc;
use std::sync::Mutex;
use std::time::Duration;

use http::Extensions;
use reqwest::{Request, Response, StatusCode};
use reqwest_middleware::{
    ClientBuilder as MiddlewareClientBuilder, Error as MiddlewareError, Middleware, Next,
};
use serde::Serialize;
use serde_json::{json, Value};

use crate::apis;
use crate::models;
use crate::stream::{stream_handle, ReducedSnapshot, StreamEvent};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorCategory {
    Authentication,
    Validation,
    NotFound,
    Conflict,
    RateLimit,
    Server,
    Transport,
    Timeout,
    UnexpectedResponse,
}

#[derive(Debug, thiserror::Error)]
#[error("{message}")]
pub struct NvokenError {
    pub category: ErrorCategory,
    pub message: String,
    pub status: Option<u16>,
    pub code: Option<String>,
    pub request_id: Option<String>,
    pub retry_after: Option<Duration>,
    pub details: Option<Value>,
}

#[derive(Debug, Clone)]
pub struct RetryPolicy {
    pub maximum_attempts: u32,
    pub minimum_delay: Duration,
    pub maximum_delay: Duration,
}

impl Default for RetryPolicy {
    fn default() -> Self {
        Self {
            maximum_attempts: 4,
            minimum_delay: Duration::from_millis(100),
            maximum_delay: Duration::from_secs(2),
        }
    }
}

#[derive(Clone)]
struct ReplaySafeRetry {
    policy: RetryPolicy,
}

#[derive(Clone)]
struct ResponseMetadataObserver {
    metadata: Arc<Mutex<HashMap<String, ResponseMetadata>>>,
}

#[derive(Debug, Clone)]
struct ResponseMetadata {
    retry_after: Option<Duration>,
}

#[async_trait::async_trait]
impl Middleware for ResponseMetadataObserver {
    async fn handle(
        &self,
        request: Request,
        extensions: &mut Extensions,
        next: Next<'_>,
    ) -> reqwest_middleware::Result<Response> {
        let result = next.run(request, extensions).await;
        if let Ok(response) = &result {
            if response.status().is_client_error() || response.status().is_server_error() {
                let request_id = response
                    .headers()
                    .get("x-request-id")
                    .and_then(|value| value.to_str().ok());
                if let Some(request_id) = request_id {
                    let retry_after = response
                        .headers()
                        .get(reqwest::header::RETRY_AFTER)
                        .and_then(|value| value.to_str().ok())
                        .and_then(parse_retry_after);
                    if let Ok(mut metadata) = self.metadata.lock() {
                        metadata.insert(request_id.to_owned(), ResponseMetadata { retry_after });
                    }
                }
            }
        }
        result
    }
}

#[async_trait::async_trait]
impl Middleware for ReplaySafeRetry {
    async fn handle(
        &self,
        request: Request,
        extensions: &mut Extensions,
        next: Next<'_>,
    ) -> reqwest_middleware::Result<Response> {
        let mut attempt = 1;
        loop {
            let cloned = request.try_clone().ok_or_else(|| {
                MiddlewareError::Middleware(anyhow::anyhow!(
                    "Runtime request body cannot be replayed"
                ))
            })?;
            let result = next.clone().run(cloned, extensions).await;
            let retry = match &result {
                Ok(response) => retryable_status(response.status()),
                Err(MiddlewareError::Reqwest(_)) => true,
                Err(MiddlewareError::Middleware(_)) => false,
            };
            if !retry || attempt >= self.policy.maximum_attempts {
                return result;
            }
            let retry_after = result
                .as_ref()
                .ok()
                .and_then(|response| response.headers().get(reqwest::header::RETRY_AFTER))
                .and_then(|value| value.to_str().ok())
                .and_then(parse_retry_after);
            let exponential = self
                .policy
                .minimum_delay
                .saturating_mul(1_u32 << (attempt - 1))
                .min(self.policy.maximum_delay);
            let delay = retry_after
                .unwrap_or_else(|| jitter(exponential))
                .min(self.policy.maximum_delay);
            tokio::time::sleep(delay).await;
            attempt += 1;
        }
    }
}

fn jitter(delay: Duration) -> Duration {
    let half = delay / 2;
    let upper = delay.saturating_sub(half).as_nanos().min(u64::MAX as u128) as u64;
    half + Duration::from_nanos(fastrand::u64(0..=upper))
}

fn parse_retry_after(value: &str) -> Option<Duration> {
    if let Ok(seconds) = value.parse::<u64>() {
        return Some(Duration::from_secs(seconds));
    }
    let when = chrono::DateTime::parse_from_rfc2822(value)
        .ok()?
        .with_timezone(&chrono::Utc);
    let now = chrono::Utc::now();
    (when > now).then(|| (when - now).to_std().ok()).flatten()
}

fn retryable_status(status: StatusCode) -> bool {
    matches!(status.as_u16(), 408 | 425 | 429 | 500 | 502 | 503 | 504)
}

#[derive(Debug, Clone, Serialize)]
pub struct Model {
    pub provider: String,
    pub name: String,
}

#[derive(Debug, Clone)]
pub struct Tool {
    pub mode: ToolMode,
    pub name: String,
    pub description: String,
    pub input_schema: HashMap<String, Value>,
}

#[derive(Debug, Clone)]
pub enum ToolMode {
    Client,
    Callback { url: String },
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct Budgets {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub wall_clock_timeout_seconds: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub active_execution_timeout_seconds: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_output_tokens: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_estimated_cost_usd: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_iterations: Option<u32>,
}

#[derive(Debug, Clone)]
pub struct ExecutionSpec {
    pub instructions: String,
    pub model: Model,
    pub budgets: Option<Budgets>,
    pub tools: Vec<Tool>,
    pub output_schema: Option<HashMap<String, Value>>,
}

#[derive(Debug, Clone)]
pub struct InvokeRequest {
    pub agent_ref: String,
    pub tenant_ref: Option<String>,
    pub session_id: Option<String>,
    pub session_key: Option<String>,
    pub idempotency_key: String,
    pub input: String,
    pub spec: ExecutionSpec,
}

#[derive(Debug, Clone)]
pub struct ToolResult {
    pub tool_call_id: String,
    pub content: Value,
    pub is_error: bool,
}

#[derive(Debug, Clone, Default)]
pub struct ListInvocationsOptions {
    pub tenant_ref: Option<String>,
    pub default_tenant: Option<bool>,
    pub session_id: Option<String>,
    pub agent_id: Option<String>,
    pub status: Option<models::InvocationStatus>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct ListSessionsOptions {
    pub tenant_ref: Option<String>,
    pub default_tenant: Option<bool>,
    pub agent_id: Option<String>,
    pub session_key: Option<String>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct MessageListOptions {
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Clone)]
pub struct Client {
    pub(crate) configuration: Arc<apis::configuration::Configuration>,
    pub(crate) stream_client: reqwest::Client,
    response_metadata: Arc<Mutex<HashMap<String, ResponseMetadata>>>,
}

impl Client {
    pub fn new(
        base_url: impl Into<String>,
        api_key: impl Into<String>,
    ) -> Result<Self, NvokenError> {
        Self::with_retry_policy(base_url, api_key, RetryPolicy::default())
    }

    pub fn with_retry_policy(
        base_url: impl Into<String>,
        api_key: impl Into<String>,
        retry_policy: RetryPolicy,
    ) -> Result<Self, NvokenError> {
        let base_url = base_url.into().trim_end_matches('/').to_owned();
        let api_key = api_key.into();
        if base_url.is_empty() || api_key.is_empty() {
            return Err(NvokenError::validation("base URL and API key are required"));
        }
        let transport = reqwest::Client::builder()
            .user_agent("nvoken-rust/0.1.0")
            .build()
            .map_err(|error| NvokenError::transport(error.to_string()))?;
        let response_metadata = Arc::new(Mutex::new(HashMap::new()));
        let middleware = MiddlewareClientBuilder::new(transport.clone())
            .with(ResponseMetadataObserver {
                metadata: response_metadata.clone(),
            })
            .with(ReplaySafeRetry {
                policy: retry_policy,
            })
            .build();
        let configuration = apis::configuration::Configuration {
            base_path: base_url,
            user_agent: Some("nvoken-rust/0.1.0".to_owned()),
            client: middleware,
            bearer_access_token: Some(api_key),
            ..Default::default()
        };
        Ok(Self {
            configuration: Arc::new(configuration),
            stream_client: transport,
            response_metadata,
        })
    }

    pub fn raw(&self) -> &apis::configuration::Configuration {
        &self.configuration
    }

    pub async fn invoke(&self, request: InvokeRequest) -> Result<Handle, NvokenError> {
        if request.agent_ref.is_empty()
            || request.idempotency_key.is_empty()
            || request.input.is_empty()
        {
            return Err(NvokenError::validation(
                "agent reference, idempotency key, and input are required",
            ));
        }
        let provider = match request.spec.model.provider.as_str() {
            "anthropic" => models::ModelProvider::Anthropic,
            "openai" => models::ModelProvider::Openai,
            _ => {
                return Err(NvokenError::validation(
                    "model provider must be anthropic or openai",
                ))
            }
        };
        let model = models::ModelSelection::new(provider, request.spec.model.name);
        let mut spec = models::InlineExecutionSpec::new(request.spec.instructions, model);
        spec.budgets = request
            .spec
            .budgets
            .map(|value| serde_json::from_value(json!(value)))
            .transpose()
            .map_err(|error| NvokenError::validation(error.to_string()))?
            .map(Box::new);
        spec.output = request
            .spec
            .output_schema
            .map(models::StructuredOutputSpec::new)
            .map(Box::new);
        let mut tools = Vec::with_capacity(request.spec.tools.len());
        for tool in request.spec.tools {
            let mode = match tool.mode {
                ToolMode::Client => {
                    models::ToolSpec::ClientToolSpec(Box::new(models::ClientToolSpec::new(
                        tool.name,
                        tool.description,
                        Some(json!("client")),
                        tool.input_schema,
                    )))
                }
                ToolMode::Callback { url } => {
                    models::ToolSpec::CallbackToolSpec(Box::new(models::CallbackToolSpec::new(
                        tool.name,
                        tool.description,
                        Some(json!("callback")),
                        tool.input_schema,
                        models::CallbackTarget::new(url),
                    )))
                }
            };
            tools.push(mode);
        }
        spec.tools = (!tools.is_empty()).then_some(tools);
        let input = models::InvocationInput::new(vec![models::TextInputBlock::new(
            Some(json!("text")),
            request.input,
        )]);
        let mut body = models::CreateInvocationRequest::new(
            request.agent_ref,
            request.idempotency_key,
            input,
            spec,
        );
        body.tenant_ref = request.tenant_ref;
        body.session_id = request.session_id;
        body.session_key = request.session_key;
        let acknowledgement = apis::invocations_api::create_invocation(&self.configuration, body)
            .await
            .map_err(|error| self.normalize_generated_error(error))?;
        Ok(Handle {
            client: self.clone(),
            invocation_id: acknowledgement.invocation_id,
            session_id: acknowledgement.session_id,
            status: acknowledgement.status,
        })
    }

    pub async fn resume(&self, invocation_id: impl Into<String>) -> Result<Handle, NvokenError> {
        let invocation_id = invocation_id.into();
        let invocation = self.get(&invocation_id).await?;
        Ok(Handle {
            client: self.clone(),
            invocation_id: invocation.id,
            session_id: invocation.session_id,
            status: invocation.status,
        })
    }

    pub async fn get(&self, invocation_id: &str) -> Result<models::Invocation, NvokenError> {
        apis::invocations_api::get_invocation(&self.configuration, invocation_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_result(
        &self,
        invocation_id: &str,
    ) -> Result<models::InvocationResult, NvokenError> {
        apis::invocations_api::get_invocation_result(&self.configuration, invocation_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn cancel(&self, invocation_id: &str) -> Result<models::Invocation, NvokenError> {
        apis::invocations_api::cancel_invocation(&self.configuration, invocation_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn submit_tool_results(
        &self,
        invocation_id: &str,
        results: Vec<ToolResult>,
    ) -> Result<models::SubmitClientToolResultsResponse, NvokenError> {
        let request = models::SubmitClientToolResultsRequest::new(
            results
                .into_iter()
                .map(|result| {
                    let mut value = models::SubmitClientToolResultsRequestResultsInner::new(
                        result.tool_call_id,
                        Some(result.content),
                    );
                    value.is_error = result.is_error.then_some(true);
                    value
                })
                .collect(),
        );
        apis::invocations_api::submit_client_tool_results(
            &self.configuration,
            invocation_id,
            request,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_invocations(
        &self,
        options: ListInvocationsOptions,
    ) -> Result<models::InvocationList, NvokenError> {
        apis::invocations_api::list_invocations(
            &self.configuration,
            options.tenant_ref.as_deref(),
            options.default_tenant,
            options.session_id.as_deref(),
            options.agent_id.as_deref(),
            options.status,
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_session(&self, session_id: &str) -> Result<models::Session, NvokenError> {
        apis::sessions_api::get_session(&self.configuration, session_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_sessions(
        &self,
        options: ListSessionsOptions,
    ) -> Result<models::SessionList, NvokenError> {
        apis::sessions_api::list_sessions(
            &self.configuration,
            options.tenant_ref.as_deref(),
            options.default_tenant,
            options.agent_id.as_deref(),
            options.session_key.as_deref(),
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_messages(
        &self,
        session_id: &str,
        options: MessageListOptions,
    ) -> Result<models::SessionMessageList, NvokenError> {
        apis::sessions_api::list_session_messages(
            &self.configuration,
            session_id,
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    fn normalize_generated_error<T>(&self, error: apis::Error<T>) -> NvokenError
    where
        T: Serialize + Debug,
    {
        let mut normalized = normalize_generated_error(error);
        if let Some(request_id) = normalized.request_id.as_ref() {
            if let Ok(mut metadata) = self.response_metadata.lock() {
                if let Some(observed) = metadata.remove(request_id) {
                    normalized.retry_after = observed.retry_after;
                }
            }
        }
        normalized
    }
}

#[derive(Clone)]
pub struct Handle {
    pub(crate) client: Client,
    pub invocation_id: String,
    pub session_id: String,
    pub status: models::InvocationStatus,
}

impl Handle {
    pub async fn refresh(&mut self) -> Result<models::Invocation, NvokenError> {
        let invocation = self.client.get(&self.invocation_id).await?;
        self.status = invocation.status;
        Ok(invocation)
    }

    pub async fn wait(
        &mut self,
        timeout: Option<Duration>,
    ) -> Result<models::Invocation, NvokenError> {
        let future = async {
            let mut delay = Duration::from_millis(100);
            loop {
                let invocation = self.refresh().await?;
                if terminal(invocation.status) {
                    return Ok(invocation);
                }
                tokio::time::sleep(delay).await;
                delay = delay.saturating_mul(2).min(Duration::from_secs(2));
            }
        };
        match timeout {
            Some(timeout) => tokio::time::timeout(timeout, future)
                .await
                .map_err(|_| NvokenError::timeout("local wait timed out"))?,
            None => future.await,
        }
    }

    /// Reads the composed InvocationResult at any status: the authoritative
    /// Invocation, this Invocation's canonical messages, and the output_text
    /// projection.
    pub async fn result(&mut self) -> Result<models::InvocationResult, NvokenError> {
        let result = self.client.get_result(&self.invocation_id).await?;
        self.status = result.invocation.status;
        Ok(result)
    }

    /// Returns this Invocation's canonical messages from the composed result
    /// read.
    pub async fn list_messages(&mut self) -> Result<Vec<models::SessionMessage>, NvokenError> {
        Ok(self.result().await?.messages)
    }

    /// Returns the completed turn's canonical assistant text.
    pub async fn text(&mut self) -> Result<String, NvokenError> {
        let result = self.result().await?;
        match result.output_text {
            Some(text) if !text.is_empty() => Ok(text),
            _ => Err(NvokenError::unexpected(format!(
                "Invocation {} has no canonical assistant text",
                self.invocation_id
            ))),
        }
    }

    pub async fn cancel(&mut self) -> Result<models::Invocation, NvokenError> {
        let invocation = self.client.cancel(&self.invocation_id).await?;
        self.status = invocation.status;
        Ok(invocation)
    }

    pub async fn submit_tool_results(
        &mut self,
        results: Vec<ToolResult>,
    ) -> Result<models::SubmitClientToolResultsResponse, NvokenError> {
        let response = self
            .client
            .submit_tool_results(&self.invocation_id, results)
            .await?;
        self.status = response.status;
        Ok(response)
    }

    pub fn stream(
        &self,
    ) -> impl futures_core::Stream<Item = Result<(StreamEvent, ReducedSnapshot), NvokenError>> + '_
    {
        stream_handle(self)
    }
}

fn terminal(status: models::InvocationStatus) -> bool {
    matches!(
        status,
        models::InvocationStatus::Completed
            | models::InvocationStatus::Failed
            | models::InvocationStatus::Cancelled
    )
}

fn normalize_generated_error<T>(error: apis::Error<T>) -> NvokenError
where
    T: Serialize + Debug,
{
    match error {
        apis::Error::ResponseError(response) => {
            let body: Value = serde_json::from_str(&response.content).unwrap_or(Value::Null);
            NvokenError::response(response.status, body)
        }
        apis::Error::Reqwest(error) => NvokenError::transport(error.to_string()),
        apis::Error::ReqwestMiddleware(error) => NvokenError::transport(error.to_string()),
        apis::Error::Serde(error) => NvokenError::unexpected(error.to_string()),
        apis::Error::Io(error) => NvokenError::transport(error.to_string()),
    }
}

impl NvokenError {
    pub(crate) fn validation(message: impl Into<String>) -> Self {
        Self::new(ErrorCategory::Validation, message)
    }

    pub(crate) fn transport(message: impl Into<String>) -> Self {
        Self::new(ErrorCategory::Transport, message)
    }

    pub(crate) fn timeout(message: impl Into<String>) -> Self {
        Self::new(ErrorCategory::Timeout, message)
    }

    pub(crate) fn unexpected(message: impl Into<String>) -> Self {
        Self::new(ErrorCategory::UnexpectedResponse, message)
    }

    pub(crate) fn response(status: StatusCode, body: Value) -> Self {
        let category = match status.as_u16() {
            401 | 403 => ErrorCategory::Authentication,
            400 | 422 => ErrorCategory::Validation,
            404 => ErrorCategory::NotFound,
            409 => ErrorCategory::Conflict,
            429 => ErrorCategory::RateLimit,
            value if value >= 500 => ErrorCategory::Server,
            _ => ErrorCategory::UnexpectedResponse,
        };
        Self {
            category,
            message: body
                .get("message")
                .and_then(Value::as_str)
                .map(str::to_owned)
                .unwrap_or_else(|| format!("nvoken returned HTTP {}", status.as_u16())),
            status: Some(status.as_u16()),
            code: body.get("code").and_then(Value::as_str).map(str::to_owned),
            request_id: body
                .get("request_id")
                .and_then(Value::as_str)
                .map(str::to_owned),
            retry_after: None,
            details: body.get("details").cloned(),
        }
    }

    pub(crate) fn response_with_headers(
        status: StatusCode,
        body: Value,
        headers: &reqwest::header::HeaderMap,
    ) -> Self {
        let mut error = Self::response(status, body);
        if error.request_id.is_none() {
            error.request_id = headers
                .get("x-request-id")
                .and_then(|value| value.to_str().ok())
                .map(str::to_owned);
        }
        error.retry_after = headers
            .get(reqwest::header::RETRY_AFTER)
            .and_then(|value| value.to_str().ok())
            .and_then(parse_retry_after);
        error
    }

    fn new(category: ErrorCategory, message: impl Into<String>) -> Self {
        Self {
            category,
            message: message.into(),
            status: None,
            code: None,
            request_id: None,
            retry_after: None,
            details: None,
        }
    }
}
