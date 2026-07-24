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
use crate::stream::{stream_handle, StreamEvent};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ErrorCategory {
    Authentication,
    Permission,
    Validation,
    NotFound,
    Conflict,
    RateLimit,
    Server,
    Transport,
    Cancelled,
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
    pub max_attempts: u32,
    pub min_delay: Duration,
    pub max_delay: Duration,
}

impl Default for RetryPolicy {
    fn default() -> Self {
        Self {
            max_attempts: 4,
            min_delay: Duration::from_millis(100),
            max_delay: Duration::from_secs(2),
        }
    }
}

#[derive(Clone)]
struct ReplaySafeRetry {
    policy: RetryPolicy,
}

#[derive(Clone)]
struct ResponseMetadataObserver {
    metadata: ResponseMetadataStore,
}

#[derive(Debug, Clone)]
struct ResponseMetadata {
    retry_after: Option<Duration>,
}

#[derive(Clone, Default)]
struct ResponseMetadataStore {
    metadata: Arc<Mutex<HashMap<String, ResponseMetadata>>>,
}

impl ResponseMetadataStore {
    fn observe(&self, status: StatusCode, headers: &reqwest::header::HeaderMap) {
        let request_id = headers
            .get("x-request-id")
            .and_then(|value| value.to_str().ok());
        let Some(request_id) = request_id else {
            return;
        };
        let Ok(mut metadata) = self.metadata.lock() else {
            return;
        };
        if status.is_client_error() || status.is_server_error() {
            let retry_after = headers
                .get(reqwest::header::RETRY_AFTER)
                .and_then(|value| value.to_str().ok())
                .and_then(parse_retry_after);
            metadata.insert(request_id.to_owned(), ResponseMetadata { retry_after });
        } else {
            metadata.remove(request_id);
        }
    }

    fn take(&self, request_id: &str) -> Option<ResponseMetadata> {
        self.metadata
            .lock()
            .ok()
            .and_then(|mut metadata| metadata.remove(request_id))
    }
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
            self.metadata.observe(response.status(), response.headers());
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
            if !retry || attempt >= self.policy.max_attempts {
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
                .min_delay
                .saturating_mul(1_u32 << (attempt - 1))
                .min(self.policy.max_delay);
            let delay = retry_after
                .unwrap_or_else(|| jitter(exponential))
                .min(self.policy.max_delay);
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
    pub id: String,
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
    Host,
    Callback { url: String },
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct Limits {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub total_timeout_seconds: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub active_timeout_seconds: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub waiting_timeout_seconds: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_output_tokens: Option<u32>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_estimated_cost_usd: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub max_iterations: Option<u32>,
}

#[derive(Debug, Clone)]
pub struct ExecutionSpec {
    pub instructions: Option<String>,
    pub model: Model,
    pub limits: Option<Limits>,
    pub tools: Vec<Tool>,
    pub output_schema: Option<HashMap<String, Value>>,
}

#[derive(Debug, Clone)]
pub struct InvokeRequest {
    pub agent_key: String,
    pub tenant_key: Option<String>,
    pub session_id: Option<String>,
    pub session_key: Option<String>,
    pub idempotency_key: Option<String>,
    pub input: String,
    pub spec: ExecutionSpec,
    pub provider_credentials: Vec<ProviderCredentialSelection>,
}

impl InvokeRequest {
    pub fn new(agent_key: impl Into<String>, input: impl Into<String>, model: Model) -> Self {
        Self {
            agent_key: agent_key.into(),
            tenant_key: None,
            session_id: None,
            session_key: None,
            idempotency_key: None,
            input: input.into(),
            spec: ExecutionSpec {
                instructions: None,
                model,
                limits: None,
                tools: Vec::new(),
                output_schema: None,
            },
            provider_credentials: Vec::new(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct ProviderCredentialSelection {
    pub provider: String,
    pub source: ProviderCredentialSource,
}

#[derive(Debug, Clone)]
pub enum ProviderCredentialSource {
    CallerEphemeral { api_key: String },
    AccountByok,
    TenantByok,
    Platform,
}

#[derive(Debug, Clone)]
pub struct ToolResult {
    pub tool_call_id: String,
    pub content: Value,
    pub is_error: bool,
}

#[derive(Debug, Clone, Default)]
pub struct ListInvocationsOptions {
    pub tenant_key: Option<String>,
    pub default_tenant: Option<bool>,
    pub session_id: Option<String>,
    pub agent_id: Option<String>,
    pub status: Option<models::InvocationStatus>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct ListModelsOptions {
    pub provider: Option<String>,
    pub include_deprecated: Option<bool>,
}

#[derive(Debug, Clone, Default)]
pub struct ListSessionsOptions {
    pub tenant_key: Option<String>,
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
    response_metadata: ResponseMetadataStore,
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
        let response_metadata = ResponseMetadataStore::default();
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

    pub async fn invoke(&self, request: InvokeRequest) -> Result<InvocationHandle, NvokenError> {
        if request.agent_key.is_empty() || request.input.is_empty() {
            return Err(NvokenError::validation("agent key and input are required"));
        }
        let provider = model_provider(&request.spec.model.provider)?;
        let model = models::ModelSelection::new(provider, request.spec.model.id);
        let mut spec = models::InlineExecutionSpec::new(model);
        spec.instructions = request.spec.instructions;
        spec.limits = request
            .spec
            .limits
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
                ToolMode::Host => {
                    models::ToolSpec::HostToolSpec(Box::new(models::HostToolSpec::new(
                        tool.name,
                        tool.description,
                        models::host_tool_spec::Mode::ModeHost,
                        tool.input_schema,
                    )))
                }
                ToolMode::Callback { url } => {
                    models::ToolSpec::CallbackToolSpec(Box::new(models::CallbackToolSpec::new(
                        tool.name,
                        tool.description,
                        models::callback_tool_spec::Mode::ModeCallback,
                        tool.input_schema,
                        models::CallbackTarget::new(url),
                    )))
                }
            };
            tools.push(mode);
        }
        spec.tools = (!tools.is_empty()).then_some(tools);
        let input = models::InvocationInput::String(request.input);
        let idempotency_key = request
            .idempotency_key
            .unwrap_or_else(generated_idempotency_key);
        let mut body = models::CreateInvocationRequest::new(
            request.agent_key,
            idempotency_key.clone(),
            input,
            spec,
        );
        body.tenant_key = request.tenant_key;
        body.session_id = request.session_id;
        body.session_key = request.session_key;
        if request.provider_credentials.len() > 1 {
            return Err(NvokenError::validation(
                "at most one provider credential selection is supported",
            ));
        }
        body.provider_credentials = if request.provider_credentials.is_empty() {
            None
        } else {
            Some(
                request
                    .provider_credentials
                    .into_iter()
                    .map(provider_credential_selection)
                    .collect::<Result<Vec<_>, _>>()?,
            )
        };
        let acknowledgement = apis::invocations_api::create_invocation(&self.configuration, body)
            .await
            .map_err(|error| self.normalize_generated_error(error))?;
        Ok(InvocationHandle {
            client: self.clone(),
            invocation_id: acknowledgement.invocation_id,
            idempotency_key: Some(idempotency_key),
            session_id: Some(acknowledgement.session_id),
            agent_id: Some(acknowledgement.agent_id),
            status: Some(acknowledgement.status),
            deduplicated: Some(acknowledgement.deduplicated),
            deadline_at: Some(acknowledgement.deadline_at),
        })
    }

    pub fn invocation(&self, invocation_id: impl Into<String>) -> InvocationHandle {
        InvocationHandle {
            client: self.clone(),
            invocation_id: invocation_id.into(),
            idempotency_key: None,
            session_id: None,
            agent_id: None,
            status: None,
            deduplicated: None,
            deadline_at: None,
        }
    }

    pub async fn list_models(
        &self,
        options: ListModelsOptions,
    ) -> Result<models::ModelList, NvokenError> {
        let provider = options
            .provider
            .as_deref()
            .map(model_provider)
            .transpose()?;
        apis::models_api::list_models(
            &self.configuration,
            provider.as_deref(),
            options.include_deprecated,
            None,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_model(&self, model: &Model) -> Result<models::ModelDescriptor, NvokenError> {
        if model.id.is_empty() {
            return Err(NvokenError::validation("model id is required"));
        }
        let provider = model_provider(&model.provider)?;
        apis::models_api::get_model(&self.configuration, &provider, &model.id, None)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_invocation(
        &self,
        invocation_id: &str,
    ) -> Result<models::Invocation, NvokenError> {
        apis::invocations_api::get_invocation(&self.configuration, invocation_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_invocation_result(
        &self,
        invocation_id: &str,
    ) -> Result<models::InvocationResult, NvokenError> {
        apis::invocations_api::get_invocation_result(&self.configuration, invocation_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn cancel_invocation(
        &self,
        invocation_id: &str,
    ) -> Result<models::Invocation, NvokenError> {
        apis::invocations_api::cancel_invocation(&self.configuration, invocation_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn submit_tool_results(
        &self,
        invocation_id: &str,
        results: Vec<ToolResult>,
    ) -> Result<models::SubmitHostToolResultsResponse, NvokenError> {
        let request = models::SubmitHostToolResultsRequest::new(
            results
                .into_iter()
                .map(|result| {
                    let mut value = models::SubmitHostToolResultsRequestResultsInner::new(
                        result.tool_call_id,
                        Some(result.content),
                    );
                    value.is_error = result.is_error.then_some(true);
                    value
                })
                .collect(),
        );
        apis::invocations_api::submit_host_tool_results(&self.configuration, invocation_id, request)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_invocations(
        &self,
        options: ListInvocationsOptions,
    ) -> Result<models::InvocationList, NvokenError> {
        apis::invocations_api::list_invocations(
            &self.configuration,
            options.tenant_key.as_deref(),
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
            options.tenant_key.as_deref(),
            options.default_tenant,
            options.agent_id.as_deref(),
            options.session_key.as_deref(),
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_session_messages(
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
            if let Some(observed) = self.response_metadata.take(request_id) {
                normalized.retry_after = observed.retry_after;
            }
        }
        normalized
    }
}

fn provider_credential_selection(
    selection: ProviderCredentialSelection,
) -> Result<models::InvocationProviderCredentialSelection, NvokenError> {
    let provider = model_provider(&selection.provider)?;
    match selection.source {
        ProviderCredentialSource::CallerEphemeral { api_key } => {
            if api_key.is_empty() {
                return Err(NvokenError::validation(
                    "caller-ephemeral provider credentials require an API key",
                ));
            }
            Ok(
                models::InvocationProviderCredentialSelection::InvocationProviderCredentialSelectionOneOf(
                    Box::new(models::InvocationProviderCredentialSelectionOneOf::new(
                        provider,
                        models::invocation_provider_credential_selection_one_of::Source::SourceCallerEphemeral,
                        models::ProviderStaticCredential::new(api_key),
                    )),
                ),
            )
        }
        source => {
            let source = match source {
                ProviderCredentialSource::AccountByok => {
                    models::invocation_provider_credential_selection_one_of_1::Source::AccountByok
                }
                ProviderCredentialSource::TenantByok => {
                    models::invocation_provider_credential_selection_one_of_1::Source::TenantByok
                }
                ProviderCredentialSource::Platform => {
                    models::invocation_provider_credential_selection_one_of_1::Source::Platform
                }
                ProviderCredentialSource::CallerEphemeral { .. } => unreachable!(),
            };
            Ok(
                models::InvocationProviderCredentialSelection::InvocationProviderCredentialSelectionOneOf1(
                    Box::new(models::InvocationProviderCredentialSelectionOneOf1::new(
                        provider, source,
                    )),
                ),
            )
        }
    }
}

fn model_provider(provider: &str) -> Result<String, NvokenError> {
    let mut characters = provider.chars();
    if !matches!(characters.next(), Some('a'..='z'))
        || !characters.all(|character| {
            character.is_ascii_lowercase() || character.is_ascii_digit() || character == '_'
        })
    {
        return Err(NvokenError::validation(
            "model provider must be a valid canonical identifier",
        ));
    }
    Ok(provider.to_owned())
}

#[derive(Clone)]
pub struct InvocationHandle {
    pub(crate) client: Client,
    pub invocation_id: String,
    pub idempotency_key: Option<String>,
    pub session_id: Option<String>,
    pub agent_id: Option<String>,
    pub status: Option<models::InvocationStatus>,
    pub deduplicated: Option<bool>,
    pub deadline_at: Option<chrono::DateTime<chrono::FixedOffset>>,
}

impl InvocationHandle {
    pub async fn refresh(&mut self) -> Result<models::Invocation, NvokenError> {
        let invocation = self.client.get_invocation(&self.invocation_id).await?;
        self.apply(&invocation);
        Ok(invocation)
    }

    fn apply(&mut self, invocation: &models::Invocation) {
        self.session_id = Some(invocation.session_id.clone());
        self.agent_id = Some(invocation.agent_id.clone());
        self.status = Some(invocation.status);
        self.deadline_at = Some(invocation.deadline_at);
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
        let result = self
            .client
            .get_invocation_result(&self.invocation_id)
            .await?;
        self.apply(&result.invocation);
        Ok(result)
    }

    /// Returns this Invocation's canonical messages from the composed result
    /// read.
    pub async fn list_messages(&mut self) -> Result<Vec<models::SessionMessage>, NvokenError> {
        Ok(self.result().await?.messages)
    }

    /// Returns the completed turn's canonical assistant text. Fails with
    /// an `unexpected_response` error when the wire `output_text` is null
    /// or the empty string: the wire keeps those distinct, but this helper
    /// deliberately treats both as "no useful answer". Read `result()`
    /// directly to observe the distinction.
    pub async fn output_text(&mut self) -> Result<String, NvokenError> {
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
        let invocation = self.client.cancel_invocation(&self.invocation_id).await?;
        self.apply(&invocation);
        Ok(invocation)
    }

    pub async fn submit_tool_results(
        &mut self,
        results: Vec<ToolResult>,
    ) -> Result<models::SubmitHostToolResultsResponse, NvokenError> {
        let response = self
            .client
            .submit_tool_results(&self.invocation_id, results)
            .await?;
        self.session_id = Some(response.session_id.clone());
        self.status = Some(response.status);
        Ok(response)
    }

    pub async fn wait_for_action(
        &mut self,
        timeout: Option<Duration>,
    ) -> Result<models::Invocation, NvokenError> {
        let future = async {
            let mut delay = Duration::from_millis(100);
            loop {
                let invocation = self.refresh().await?;
                if invocation.status == models::InvocationStatus::Waiting
                    || terminal(invocation.status)
                {
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

    pub async fn wait_for_result(
        &mut self,
        timeout: Option<Duration>,
    ) -> Result<models::InvocationResult, NvokenError> {
        let invocation = self.wait(timeout).await?;
        if invocation.status != models::InvocationStatus::Completed {
            let mut error = NvokenError::new(
                ErrorCategory::Conflict,
                format!(
                    "Invocation {} ended with status {}",
                    self.invocation_id, invocation.status
                ),
            );
            error.code = invocation
                .error
                .as_ref()
                .and_then(|value| serde_json::to_value(value.code).ok())
                .and_then(|value| value.as_str().map(str::to_owned));
            error.details = invocation
                .error
                .as_ref()
                .and_then(|value| value.details.clone())
                .map(|value| json!(value));
            return Err(error);
        }
        self.result().await
    }

    pub fn stream(
        &self,
    ) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
        stream_handle(self)
    }
}

fn generated_idempotency_key() -> String {
    format!(
        "nvoken-{:016x}{:016x}",
        fastrand::u64(..),
        fastrand::u64(..)
    )
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
            401 => ErrorCategory::Authentication,
            403 => ErrorCategory::Permission,
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

#[cfg(test)]
mod tests {
    use super::*;
    use reqwest::header::{HeaderMap, HeaderValue, RETRY_AFTER};

    #[test]
    fn response_metadata_is_bounded_after_success_and_error_handling() {
        let metadata = ResponseMetadataStore::default();
        for index in 0..1_000 {
            let request_id = format!("req_{index}");
            let mut headers = HeaderMap::new();
            headers.insert("x-request-id", HeaderValue::from_str(&request_id).unwrap());
            headers.insert(RETRY_AFTER, HeaderValue::from_static("1"));
            metadata.observe(StatusCode::TOO_MANY_REQUESTS, &headers);
            assert_eq!(
                metadata.take(&request_id).unwrap().retry_after,
                Some(Duration::from_secs(1))
            );
        }
        assert!(metadata.metadata.lock().unwrap().is_empty());

        let mut headers = HeaderMap::new();
        headers.insert("x-request-id", HeaderValue::from_static("req_retry"));
        metadata.observe(StatusCode::TOO_MANY_REQUESTS, &headers);
        metadata.observe(StatusCode::OK, &headers);
        assert!(metadata.metadata.lock().unwrap().is_empty());
    }

    #[test]
    fn provider_credentials_map_ephemeral_and_stored_sources() {
        let ephemeral = provider_credential_selection(ProviderCredentialSelection {
            provider: "openai".to_owned(),
            source: ProviderCredentialSource::CallerEphemeral {
                api_key: "secret".to_owned(),
            },
        })
        .unwrap();
        assert_eq!(
            serde_json::to_value(ephemeral).unwrap(),
            json!({
                "provider": "openai",
                "source": "caller_ephemeral",
                "credential": {"api_key": "secret"},
            })
        );

        let stored = provider_credential_selection(ProviderCredentialSelection {
            provider: "openai".to_owned(),
            source: ProviderCredentialSource::AccountByok,
        })
        .unwrap();
        assert_eq!(
            serde_json::to_value(stored).unwrap(),
            json!({"provider": "openai", "source": "account_byok"})
        );
    }
}
