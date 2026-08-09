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
use serde::{Serialize, Serializer};
use serde_json::{json, Value};

use crate::apis;
use crate::media_preflight::preflight_input_blocks;
use crate::models;
use crate::schema_preflight::preflight_output_schema;
use crate::stream::{stream_handle, stream_handle_with_options, StreamEvent};

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

#[derive(Debug, Clone, Default, PartialEq, Serialize)]
pub struct Model {
    pub provider: String,
    pub id: String,
}

impl Model {
    pub fn new(provider: impl Into<String>, id: impl Into<String>) -> Self {
        Self {
            provider: provider.into(),
            id: id.into(),
        }
    }

    pub(crate) fn is_unset(&self) -> bool {
        self.provider.is_empty() && self.id.is_empty()
    }
}

/// The error an Agent host tool handler failed with. Reported to nvoken as
/// `{"error": message, "type": type_name}`, mirroring the Go and Python
/// bindings' handler-failure shape.
#[derive(Debug, Clone)]
pub struct ToolHandlerError {
    pub message: String,
    pub type_name: String,
}

impl ToolHandlerError {
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            type_name: "Error".to_owned(),
        }
    }

    pub fn with_type(message: impl Into<String>, type_name: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            type_name: type_name.into(),
        }
    }
}

impl std::fmt::Display for ToolHandlerError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.message)
    }
}

impl std::error::Error for ToolHandlerError {}

type ToolHandlerFuture =
    std::pin::Pin<Box<dyn std::future::Future<Output = Result<Value, ToolHandlerError>> + Send>>;

/// An Agent's dispatch function for one host tool call. Boxed and
/// reference-counted so a `Tool` stays `Clone` without cloning the closure.
pub type HostToolHandler = Arc<dyn Fn(Value) -> ToolHandlerFuture + Send + Sync>;

#[derive(Clone)]
pub struct Tool {
    pub mode: ToolMode,
    pub name: String,
    pub description: String,
    pub input_schema: HashMap<String, Value>,
    /// Dispatched by an Agent when this host tool's ToolCall parks the
    /// Invocation in `waiting`. Absent for a `Tool` used only to declare a
    /// callback or builtin tool on the wire.
    pub handler: Option<HostToolHandler>,
}

impl std::fmt::Debug for Tool {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Tool")
            .field("mode", &self.mode)
            .field("name", &self.name)
            .field("description", &self.description)
            .field("input_schema", &self.input_schema)
            .field("handler", &self.handler.as_ref().map(|_| "Fn(..)"))
            .finish()
    }
}

#[derive(Debug, Clone)]
pub enum ToolMode {
    Builtin,
    Host,
    Callback { url: String },
}

impl Tool {
    pub fn fetch() -> Self {
        Self {
            mode: ToolMode::Builtin,
            name: "nvoken_fetch".to_string(),
            description: String::new(),
            input_schema: HashMap::new(),
            handler: None,
        }
    }

    pub fn host(
        name: impl Into<String>,
        description: impl Into<String>,
        input_schema: HashMap<String, Value>,
    ) -> Self {
        Self {
            mode: ToolMode::Host,
            name: name.into(),
            description: description.into(),
            input_schema,
            handler: None,
        }
    }

    pub fn callback(
        name: impl Into<String>,
        description: impl Into<String>,
        input_schema: HashMap<String, Value>,
        url: impl Into<String>,
    ) -> Self {
        Self {
            mode: ToolMode::Callback { url: url.into() },
            name: name.into(),
            description: description.into(),
            input_schema,
            handler: None,
        }
    }

    /// Attaches the dispatch function an Agent calls when this host tool's
    /// ToolCall parks the Invocation in `waiting`. Only meaningful on a
    /// `ToolMode::Host` tool; ignored for callback and builtin declarations,
    /// which nvoken itself resolves.
    pub fn handler<F, Fut>(mut self, handler: F) -> Self
    where
        F: Fn(Value) -> Fut + Send + Sync + 'static,
        Fut: std::future::Future<Output = Result<Value, ToolHandlerError>> + Send + 'static,
    {
        self.handler = Some(Arc::new(move |input| Box::pin(handler(input))));
        self
    }
}

#[derive(Debug, Clone, Default)]
pub struct McpTimeouts {
    pub discovery_seconds: Option<u32>,
    pub call_seconds: Option<u32>,
}

#[derive(Debug, Clone)]
pub struct McpServer {
    pub name: String,
    pub url: String,
    pub allowed_tools: Vec<String>,
    pub headers: HashMap<String, String>,
    pub timeouts: Option<McpTimeouts>,
}

impl McpServer {
    pub fn new(name: impl Into<String>, url: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            url: url.into(),
            allowed_tools: Vec::new(),
            headers: HashMap::new(),
            timeouts: None,
        }
    }

    pub fn allowed_tool(mut self, name: impl Into<String>) -> Self {
        self.allowed_tools.push(name.into());
        self
    }

    pub fn header(mut self, name: impl Into<String>, value: impl Into<String>) -> Self {
        self.headers.insert(name.into(), value.into());
        self
    }

    pub fn timeouts(mut self, timeouts: McpTimeouts) -> Self {
        self.timeouts = Some(timeouts);
        self
    }

    fn generated(&self) -> models::McpServer {
        let mut result = models::McpServer::new(self.name.clone(), self.url.clone());
        result.transport = Some(models::mcp_server::Transport::TransportStreamableHTTP);
        result.allowed_tools = (!self.allowed_tools.is_empty()).then(|| self.allowed_tools.clone());
        result.headers = (!self.headers.is_empty()).then(|| self.headers.clone());
        result.timeouts = self.timeouts.as_ref().map(|timeouts| {
            Box::new(models::McpTimeouts {
                discovery_seconds: timeouts.discovery_seconds,
                call_seconds: timeouts.call_seconds,
            })
        });
        result
    }
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

#[derive(Debug, Clone, Copy, Serialize)]
pub struct Sampling {
    pub temperature: f64,
}

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum ReasoningEffort {
    Low,
    Medium,
    High,
    #[serde(rename = "xhigh")]
    XHigh,
    Max,
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct Reasoning {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub effort: Option<ReasoningEffort>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub budget_tokens: Option<u32>,
}

#[derive(Debug, Clone, Copy)]
pub enum ContextCompactionTrigger {
    Auto,
    Tokens(u32),
}

impl Serialize for ContextCompactionTrigger {
    fn serialize<S>(&self, serializer: S) -> Result<S::Ok, S::Error>
    where
        S: Serializer,
    {
        match self {
            Self::Auto => serializer.serialize_str("auto"),
            Self::Tokens(tokens) => serializer.serialize_u32(*tokens),
        }
    }
}

#[derive(Debug, Clone, Serialize)]
pub struct ContextCompaction {
    pub trigger_tokens: ContextCompactionTrigger,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub model: Option<Model>,
}

/// Bounds how long an idle Session is retained. The window measures idle time
/// rather than lifetime: it restarts on every Invocation admission and
/// settlement, so a turn outlasting the window cannot expire underneath itself.
/// Automatic expiry never cancels running work.
#[derive(Debug, Clone, Copy, Serialize)]
pub struct SessionRetention {
    /// Idle window in seconds, from one hour to thirty days.
    pub ttl_seconds: u32,
}

/// Durable Session options. Every member is optional and at least one must be
/// present. Existing values are comparison-only: equal is accepted and
/// different returns `session_options_conflict`.
#[derive(Debug, Clone, Default, Serialize)]
pub struct SessionOptions {
    /// Requires an Invocation because the policy is validated against that
    /// turn's model. It may be installed on any Session that has no policy yet.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub compaction: Option<ContextCompaction>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub retention: Option<SessionRetention>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub budget: Option<SessionBudget>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub metadata: Option<HashMap<String, String>>,
}

/// Mutable Session-wide USD list-price guardrail, not a billing ledger.
#[derive(Debug, Clone, Copy, Serialize)]
pub struct SessionBudget {
    pub max_estimated_cost_usd: f64,
}

impl SessionOptions {
    pub fn compaction(mut self, compaction: ContextCompaction) -> Self {
        self.compaction = Some(compaction);
        self
    }

    pub fn retention(mut self, ttl_seconds: u32) -> Self {
        self.retention = Some(SessionRetention { ttl_seconds });
        self
    }

    pub fn budget(mut self, max_estimated_cost_usd: f64) -> Self {
        self.budget = Some(SessionBudget {
            max_estimated_cost_usd,
        });
        self
    }

    pub fn metadata(mut self, metadata: HashMap<String, String>) -> Self {
        self.metadata = Some(metadata);
        self
    }
}

/// Selects one provider server-side tool. Web search is Anthropic only for now,
/// and a model that does not declare `controls.tools.web_search` is refused at
/// admission rather than served a search the provider would ignore.
#[derive(Debug, Clone, Default)]
pub struct WebSearchTool {
    /// Searches this turn may run, 1 to 20. The only bound nvoken can place on
    /// search spend: the provider reports no per-search fee it could meter, so
    /// search charges ride the provider's bill outside nvoken's cost estimate.
    pub max_uses: Option<u32>,
    /// Restrict results to these hosts. Bare hostnames only — a scheme, path,
    /// or port is rejected rather than reinterpreted. Mutually exclusive with
    /// `blocked_domains`, which is the provider's rule.
    pub allowed_domains: Vec<String>,
    pub blocked_domains: Vec<String>,
    /// Biases results. Every member is optional; the host decides how precise
    /// to be about its end user.
    pub user_location: Option<WebSearchLocation>,
}

impl WebSearchTool {
    pub fn max_uses(mut self, max_uses: u32) -> Self {
        self.max_uses = Some(max_uses);
        self
    }

    pub fn allowed_domains(mut self, domains: Vec<String>) -> Self {
        self.allowed_domains = domains;
        self
    }

    pub fn blocked_domains(mut self, domains: Vec<String>) -> Self {
        self.blocked_domains = domains;
        self
    }

    pub fn user_location(mut self, location: WebSearchLocation) -> Self {
        self.user_location = Some(location);
        self
    }
}

#[derive(Debug, Clone, Default)]
pub struct WebSearchLocation {
    pub city: Option<String>,
    pub region: Option<String>,
    pub country: Option<String>,
    pub timezone: Option<String>,
}

/// One provider server-side tool declaration.
#[derive(Debug, Clone)]
pub enum ProviderTool {
    WebSearch(WebSearchTool),
}

impl ProviderTool {
    /// The generated declaration this reaches the Runtime as. Public so a
    /// caller can assert on the exact wire shape it will send.
    pub fn generated(&self) -> models::ProviderTool {
        let ProviderTool::WebSearch(search) = self;
        let mut definition = models::WebSearchTool::new();
        definition.max_uses = search.max_uses;
        definition.allowed_domains =
            (!search.allowed_domains.is_empty()).then(|| search.allowed_domains.clone());
        definition.blocked_domains =
            (!search.blocked_domains.is_empty()).then(|| search.blocked_domains.clone());
        definition.user_location = search.user_location.as_ref().map(|location| {
            let mut generated = models::WebSearchLocation::new();
            generated.city = location.city.clone();
            generated.region = location.region.clone();
            generated.country = location.country.clone();
            generated.timezone = location.timezone.clone();
            Box::new(generated)
        });
        models::ProviderTool::new(
            models::provider_tool::Type::ProviderToolWebSearch,
            definition,
        )
    }
}

#[derive(Debug, Clone, serde::Serialize)]
#[serde(tag = "mode", content = "name", rename_all = "lowercase")]
pub enum ToolChoice {
    Auto,
    None,
    Required,
    Named(String),
}

#[derive(Debug, Clone)]
pub struct InvokeRequest {
    pub agent_key: String,
    pub tenant_key: Option<String>,
    pub session_id: Option<String>,
    pub session_key: Option<String>,
    pub session_options: Option<SessionOptions>,
    pub idempotency_key: Option<String>,
    pub if_active: Option<IfActivePolicy>,
    pub on_budget_exhausted: Option<BudgetExhaustionBehavior>,
    pub input: String,
    /// Ordered blocks mixing text, images, and documents. Supply exactly one of
    /// `input` and `input_blocks`.
    pub input_blocks: Vec<models::InputBlock>,
    pub definition_id: Option<String>,
    pub model: Model,
    pub instructions: Option<String>,
    pub sampling: Option<Sampling>,
    pub reasoning: Option<Reasoning>,
    pub tool_choice: Option<ToolChoice>,
    pub limits: Option<Limits>,
    pub tools: Vec<Tool>,
    pub mcp_servers: Vec<McpServer>,
    pub provider_tools: Vec<ProviderTool>,
    pub output_schema: Option<HashMap<String, Value>>,
    pub provider_keys: Vec<ProviderKeySelection>,
    /// Endpoint nvoken posts a signed webhook to when this Invocation
    /// parks awaiting host tool results or settles. An empty event list selects
    /// every event, which is the safe default: dropping the waiting event would
    /// leave a parked host tool loop with nobody listening.
    pub webhook: Option<WebhookTarget>,
    /// Opaque host correlation data recorded on this Invocation. It is part of
    /// the admitted input, so it is immutable and material to idempotency: a
    /// replay carrying different metadata conflicts rather than updating it.
    /// Session metadata is separate and mutable — see `SessionOptions::metadata`
    /// and `Client::update_session`.
    pub metadata: Option<HashMap<String, String>>,
}

impl InvokeRequest {
    pub fn new(agent_key: impl Into<String>, input: impl Into<String>, model: Model) -> Self {
        Self {
            agent_key: agent_key.into(),
            tenant_key: None,
            session_id: None,
            session_key: None,
            session_options: None,
            idempotency_key: None,
            if_active: None,
            on_budget_exhausted: None,
            input: input.into(),
            input_blocks: Vec::new(),
            definition_id: None,
            model,
            instructions: None,
            sampling: None,
            reasoning: None,
            tool_choice: None,
            limits: None,
            tools: Vec::new(),
            mcp_servers: Vec::new(),
            provider_tools: Vec::new(),
            output_schema: None,
            provider_keys: Vec::new(),
            webhook: None,
            metadata: None,
        }
    }

    /// Builds a request without naming a model, resolved from the client's
    /// default model at `invoke` time. The server keeps requiring exact
    /// selection; this is client-side ergonomics only.
    pub fn without_model(agent_key: impl Into<String>, input: impl Into<String>) -> Self {
        Self::new(agent_key, input, Model::default())
    }

    /// Builds a request that reuses an immutable definition admitted by this app.
    pub fn from_definition(
        agent_key: impl Into<String>,
        input: impl Into<String>,
        definition_id: impl Into<String>,
    ) -> Self {
        let mut request = Self::without_model(agent_key, input);
        request.definition_id = Some(definition_id.into());
        request
    }

    pub fn tenant_key(mut self, tenant_key: impl Into<String>) -> Self {
        self.tenant_key = Some(tenant_key.into());
        self
    }

    pub fn session_id(mut self, session_id: impl Into<String>) -> Self {
        self.session_id = Some(session_id.into());
        self.session_key = None;
        self
    }

    pub fn session_key(mut self, session_key: impl Into<String>) -> Self {
        self.session_key = Some(session_key.into());
        self.session_id = None;
        self
    }

    pub fn session_options(mut self, options: SessionOptions) -> Self {
        self.session_options = Some(options);
        self
    }

    pub fn metadata(mut self, metadata: HashMap<String, String>) -> Self {
        self.metadata = Some(metadata);
        self
    }

    pub fn idempotency_key(mut self, idempotency_key: impl Into<String>) -> Self {
        self.idempotency_key = Some(idempotency_key.into());
        self
    }

    pub fn if_active(mut self, policy: IfActivePolicy) -> Self {
        self.if_active = Some(policy);
        self
    }

    pub fn on_budget_exhausted(mut self, behavior: BudgetExhaustionBehavior) -> Self {
        self.on_budget_exhausted = Some(behavior);
        self
    }

    pub fn instructions(mut self, instructions: impl Into<String>) -> Self {
        self.instructions = Some(instructions.into());
        self
    }

    pub fn limits(mut self, limits: Limits) -> Self {
        self.limits = Some(limits);
        self
    }

    pub fn sampling(mut self, sampling: Sampling) -> Self {
        self.sampling = Some(sampling);
        self
    }

    pub fn reasoning(mut self, reasoning: Reasoning) -> Self {
        self.reasoning = Some(reasoning);
        self
    }

    pub fn tool_choice(mut self, tool_choice: ToolChoice) -> Self {
        self.tool_choice = Some(tool_choice);
        self
    }

    pub fn tool(mut self, tool: Tool) -> Self {
        self.tools.push(tool);
        self
    }

    pub fn mcp_server(mut self, server: McpServer) -> Self {
        self.mcp_servers.push(server);
        self
    }

    pub fn provider_tool(mut self, tool: ProviderTool) -> Self {
        self.provider_tools.push(tool);
        self
    }

    pub fn output_schema(mut self, schema: HashMap<String, Value>) -> Self {
        self.output_schema = Some(schema);
        self
    }

    pub fn provider_key(mut self, selection: ProviderKeySelection) -> Self {
        self.provider_keys.push(selection);
        self
    }

    pub fn webhook(mut self, target: WebhookTarget) -> Self {
        self.webhook = Some(target);
        self
    }
}

#[derive(Debug, Clone, Default)]
pub struct WebhookTarget {
    pub url: String,
    pub events: Vec<WebhookEvent>,
}

impl WebhookTarget {
    pub fn new(url: impl Into<String>) -> Self {
        Self {
            url: url.into(),
            events: Vec::new(),
        }
    }

    pub fn event(mut self, event: WebhookEvent) -> Self {
        self.events.push(event);
        self
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WebhookEvent {
    Waiting,
    Paused,
    Settled,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IfActivePolicy {
    Reject,
    Supersede,
    Interrupt,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BudgetExhaustionBehavior {
    Settle,
    Pause,
}

#[derive(Debug, Clone)]
pub struct ProviderKeySelection {
    pub provider: String,
    pub source: ProviderKeySource,
}

#[derive(Debug, Clone)]
pub enum ProviderKeySource {
    CallerEphemeral { api_key: String },
    AppByok,
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
pub struct ListAgentsOptions {
    pub agent_key: Option<String>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct ListInvocationsOptions {
    pub tenant_key: Option<String>,
    pub default_tenant: Option<bool>,
    pub user_key: Option<String>,
    pub session_id: Option<String>,
    pub agent_id: Option<String>,
    pub agent_key: Option<String>,
    pub status: Option<models::InvocationStatus>,
    pub statuses: Vec<models::InvocationStatus>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Copy, Default)]
pub struct StreamOptions {
    pub deltas: Option<bool>,
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
    pub user_key: Option<String>,
    pub agent_id: Option<String>,
    pub agent_key: Option<String>,
    pub session_key: Option<String>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct MessageListOptions {
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct CompactionListOptions {
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct ToolCallListOptions {
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum WaitCondition {
    Terminal,
    Actionable,
}

#[derive(Debug, Clone)]
pub struct WaitOptions {
    pub until: WaitCondition,
    pub timeout: Option<Duration>,
    pub min_poll_interval: Duration,
    pub max_poll_interval: Duration,
}

impl Default for WaitOptions {
    fn default() -> Self {
        Self {
            until: WaitCondition::Terminal,
            timeout: None,
            min_poll_interval: Duration::from_millis(100),
            max_poll_interval: Duration::from_secs(2),
        }
    }
}

#[derive(Clone)]
pub struct Client {
    pub(crate) configuration: Arc<apis::configuration::Configuration>,
    pub(crate) stream_client: reqwest::Client,
    response_metadata: ResponseMetadataStore,
    default_model: Option<Model>,
    /// One lock per bound Agent Session key, shared across every clone of
    /// this `Client` so two `AgentSession` handles for the same Session
    /// (even built from different `Agent` values) serialize correctly.
    session_locks: Arc<Mutex<HashMap<String, Arc<tokio::sync::Mutex<()>>>>>,
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
            default_model: None,
            session_locks: Arc::new(Mutex::new(HashMap::new())),
        })
    }

    /// Sets the model an `invoke` call uses when its request does not name
    /// one itself. Resolved client-side into `model` before the
    /// request; the server keeps requiring exact selection, always.
    pub fn with_default_model(mut self, model: Model) -> Self {
        self.default_model = Some(model);
        self
    }

    pub(crate) fn default_model(&self) -> Option<Model> {
        self.default_model.clone()
    }

    /// Returns the shared lock for one Agent Session key, creating it on
    /// first use. Every `Client` clone shares the same underlying map.
    pub(crate) fn session_lock(&self, key: &str) -> Arc<tokio::sync::Mutex<()>> {
        let mut locks = self
            .session_locks
            .lock()
            .unwrap_or_else(|error| error.into_inner());
        locks
            .entry(key.to_owned())
            .or_insert_with(|| Arc::new(tokio::sync::Mutex::new(())))
            .clone()
    }

    pub fn raw(&self) -> &apis::configuration::Configuration {
        &self.configuration
    }

    pub async fn create_budget(
        &self,
        request: models::CreateBudgetRequest,
    ) -> Result<models::Budget, NvokenError> {
        apis::budgets_api::create_budget(&self.configuration, request)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_budget(&self, budget_id: &str) -> Result<models::Budget, NvokenError> {
        apis::budgets_api::get_budget(&self.configuration, budget_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn update_budget(
        &self,
        budget_id: &str,
        max_estimated_cost_usd: f64,
    ) -> Result<models::Budget, NvokenError> {
        apis::budgets_api::update_budget(
            &self.configuration,
            budget_id,
            models::UpdateBudgetRequest::new(max_estimated_cost_usd),
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn delete_budget(&self, budget_id: &str) -> Result<(), NvokenError> {
        apis::budgets_api::delete_budget(&self.configuration, budget_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn invoke(&self, request: InvokeRequest) -> Result<InvocationHandle, NvokenError> {
        let body = self.invocation_body(request)?;
        let idempotency_key = body.idempotency_key.clone();
        let invocation = apis::invocations_api::create_invocation(
            &self.configuration,
            body,
            None,
            None,
            None,
            None,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))?;
        Ok(InvocationHandle {
            client: self.clone(),
            invocation_id: invocation.id,
            idempotency_key: Some(idempotency_key),
            session_id: Some(invocation.session_id),
            agent_id: Some(invocation.agent_id),
            status: Some(invocation.status),
            deduplicated: Some(invocation.deduplicated.unwrap_or(false)),
            deadline_at: invocation.deadline_at,
        })
    }

    /// Validates and converts a request into the exact body `invoke` sends,
    /// including the generated idempotency key. Useful for inspecting what an
    /// admission will carry without making one.
    pub fn invocation_body(
        &self,
        mut request: InvokeRequest,
    ) -> Result<models::CreateInvocationRequest, NvokenError> {
        if request.agent_key.is_empty() {
            return Err(NvokenError::validation("agent key and input are required"));
        }
        let has_inline_definition = !request.model.is_unset()
            || request.instructions.is_some()
            || request.sampling.is_some()
            || request.reasoning.is_some()
            || request.tool_choice.is_some()
            || request.limits.is_some()
            || !request.tools.is_empty()
            || !request.mcp_servers.is_empty()
            || !request.provider_tools.is_empty()
            || request.output_schema.is_some();
        if request.definition_id.is_some() && has_inline_definition {
            return Err(NvokenError::validation(
                "definition_id is mutually exclusive with every inline definition field",
            ));
        }
        if request.definition_id.is_none() && request.model.is_unset() {
            request.model = self
                .default_model
                .clone()
                .ok_or_else(|| NvokenError::validation("model is required"))?;
        }
        if request.input.is_empty() == request.input_blocks.is_empty() {
            return Err(NvokenError::validation(
                "supply exactly one of input and input blocks",
            ));
        }
        if !request.input_blocks.is_empty() {
            preflight_input_blocks(&request.input_blocks)?;
        }
        if let Some(schema) = &request.output_schema {
            preflight_output_schema(schema)?;
        }
        let model = if request.definition_id.is_none() {
            let provider = model_provider(&request.model.provider)?;
            Some(models::ModelInput::Model(Box::new(models::Model::new(
                provider,
                request.model.id,
            ))))
        } else {
            None
        };
        let instructions = request.instructions;
        let sampling = request
            .sampling
            .map(|value| models::Sampling::new(value.temperature))
            .map(Box::new);
        let reasoning = request.reasoning.map(|value| {
            let effort = value.effort.map(|effort| match effort {
                ReasoningEffort::Low => models::ReasoningEffort::EffortLow,
                ReasoningEffort::Medium => models::ReasoningEffort::EffortMedium,
                ReasoningEffort::High => models::ReasoningEffort::EffortHigh,
                ReasoningEffort::XHigh => models::ReasoningEffort::EffortXHigh,
                ReasoningEffort::Max => models::ReasoningEffort::EffortMax,
            });
            Box::new(models::Reasoning {
                effort,
                budget_tokens: value.budget_tokens,
            })
        });
        let tool_choice = request.tool_choice.map(|value| {
            let (mode, name) = match value {
                ToolChoice::Auto => (models::ModelToolChoiceMode::ChoiceAuto, None),
                ToolChoice::None => (models::ModelToolChoiceMode::ChoiceNone, None),
                ToolChoice::Required => (models::ModelToolChoiceMode::ChoiceRequired, None),
                ToolChoice::Named(name) => (models::ModelToolChoiceMode::ChoiceNamed, Some(name)),
            };
            Box::new(models::ToolChoice { mode, name })
        });
        let limits = request
            .limits
            .map(|value| serde_json::from_value(json!(value)))
            .transpose()
            .map_err(|error| NvokenError::validation(error.to_string()))?
            .map(Box::new);
        let structured_output = request
            .output_schema
            .map(models::StructuredOutput::new)
            .map(Box::new);
        let mut tools = Vec::with_capacity(request.tools.len());
        for tool in request.tools {
            let mode = match tool.mode {
                ToolMode::Builtin => {
                    if tool.name != "nvoken_fetch"
                        || !tool.description.is_empty()
                        || !tool.input_schema.is_empty()
                    {
                        return Err(NvokenError::validation(
                            "builtin tool must be the unmodified nvoken_fetch declaration",
                        ));
                    }
                    models::ToolDeclaration::Builtin(Box::new(models::BuiltinToolDeclaration::new(
                        models::builtin_tool_declaration::Name::NameNvokenFetch,
                        models::builtin_tool_declaration::Mode::ModeBuiltin,
                    )))
                }
                ToolMode::Host => {
                    let declaration = models::HostToolDeclaration::new(
                        tool.name,
                        tool.description,
                        models::host_tool_declaration::Mode::ModeHost,
                        tool.input_schema,
                    );
                    models::ToolDeclaration::Host(Box::new(declaration))
                }
                ToolMode::Callback { url } => models::ToolDeclaration::Callback(Box::new(
                    models::CallbackToolDeclaration::new(
                        tool.name,
                        tool.description,
                        models::callback_tool_declaration::Mode::ModeCallback,
                        tool.input_schema,
                        models::CallbackTarget::new(url),
                    ),
                )),
            };
            tools.push(mode);
        }
        let tools = (!tools.is_empty()).then_some(tools);
        let mcp_servers = (!request.mcp_servers.is_empty()).then(|| {
            request
                .mcp_servers
                .iter()
                .map(McpServer::generated)
                .collect()
        });
        let provider_tools = (!request.provider_tools.is_empty()).then(|| {
            request
                .provider_tools
                .iter()
                .map(ProviderTool::generated)
                .collect()
        });
        let input = if request.input_blocks.is_empty() {
            models::InvocationInput::String(request.input)
        } else {
            models::InvocationInput::ArrayVecInputBlock(request.input_blocks)
        };
        let mut body = models::CreateInvocationRequest::new(
            request.agent_key,
            request
                .idempotency_key
                .unwrap_or_else(generated_idempotency_key),
            input,
        );
        body.definition_id = request.definition_id;
        body.model = model.map(Box::new);
        body.instructions = instructions;
        body.sampling = sampling;
        body.reasoning = reasoning;
        body.tool_choice = tool_choice;
        body.limits = limits;
        body.structured_output = structured_output;
        body.tools = tools;
        body.mcp_servers = mcp_servers;
        body.provider_tools = provider_tools;
        body.tenant_key = request.tenant_key;
        body.session_id = request.session_id;
        body.session_key = request.session_key;
        body.session_options = request
            .session_options
            .map(|value| {
                if value.compaction.is_none()
                    && value.retention.is_none()
                    && value.budget.is_none()
                    && value.metadata.is_none()
                {
                    return Err(NvokenError::validation(
                        "session_options requires at least one member",
                    ));
                }
                // Every session option is independently optional, so the
                // generated constructor takes none of them.
                let mut options = models::SessionOptions::new();
                options.compaction = value
                    .compaction
                    .map(|value| {
                        let trigger_tokens = match value.trigger_tokens {
                            ContextCompactionTrigger::Auto => {
                                models::CompactionPolicyTriggerTokens::String("auto".to_string())
                            }
                            ContextCompactionTrigger::Tokens(tokens) => {
                                models::CompactionPolicyTriggerTokens::Integer(tokens)
                            }
                        };
                        let mut compaction = models::CompactionPolicy::new(trigger_tokens);
                        compaction.model = value
                            .model
                            .map(|model| {
                                let provider = model_provider(&model.provider)?;
                                Ok::<_, NvokenError>(Box::new(models::Model::new(
                                    provider, model.id,
                                )))
                            })
                            .transpose()?;
                        Ok::<_, NvokenError>(Box::new(compaction))
                    })
                    .transpose()?;
                options.retention = value
                    .retention
                    .map(|retention| Box::new(models::RetentionPolicy::new(retention.ttl_seconds)));
                options.budget = value.budget.map(|budget| {
                    Box::new(models::SessionBudget::new(budget.max_estimated_cost_usd))
                });
                options.metadata = value.metadata;
                Ok::<_, NvokenError>(Box::new(options))
            })
            .transpose()?;
        body.metadata = request.metadata;
        body.if_active = request.if_active.map(|policy| match policy {
            IfActivePolicy::Reject => models::create_invocation_request::IfActive::Reject,
            IfActivePolicy::Supersede => models::create_invocation_request::IfActive::Supersede,
            IfActivePolicy::Interrupt => models::create_invocation_request::IfActive::Interrupt,
        });
        body.on_budget_exhausted = request.on_budget_exhausted.map(|behavior| match behavior {
            BudgetExhaustionBehavior::Settle => {
                models::create_invocation_request::OnBudgetExhausted::Settle
            }
            BudgetExhaustionBehavior::Pause => {
                models::create_invocation_request::OnBudgetExhausted::Pause
            }
        });
        if request.provider_keys.len() > 1 {
            return Err(NvokenError::validation(
                "at most one provider key selection is supported",
            ));
        }
        body.provider_keys = if request.provider_keys.is_empty() {
            None
        } else {
            Some(
                request
                    .provider_keys
                    .into_iter()
                    .map(provider_key_selection)
                    .collect::<Result<Vec<_>, _>>()?,
            )
        };
        body.webhook = match request.webhook {
            None => None,
            Some(target) => {
                if target.url.is_empty() {
                    return Err(NvokenError::validation("webhook.url is required"));
                }
                let mut generated = models::WebhookTarget::new(target.url);
                // An empty list stays absent on the wire. The Runtime applies
                // the complete-set default, and an empty array is a rejected
                // request, so materializing the default here would change what
                // a replay fingerprints against.
                generated.events = if target.events.is_empty() {
                    None
                } else {
                    Some(
                        target
                            .events
                            .into_iter()
                            .map(|event| match event {
                                WebhookEvent::Waiting => models::WebhookEvent::WebhookEventWaiting,
                                WebhookEvent::Paused => models::WebhookEvent::WebhookEventPaused,
                                WebhookEvent::Settled => models::WebhookEvent::WebhookEventSettled,
                            })
                            .collect(),
                    )
                };
                Some(Box::new(generated))
            }
        };
        Ok(body)
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

    pub async fn list_mcp_tools(
        &self,
        server: &McpServer,
    ) -> Result<models::McpListToolsResponse, NvokenError> {
        apis::mcp_api::list_mcp_tools(
            &self.configuration,
            models::McpListToolsRequest::new(server.generated()),
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

    pub async fn get_agent_identity(&self, agent_id: &str) -> Result<models::Agent, NvokenError> {
        apis::agents_api::get_agent(&self.configuration, agent_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_agent_identities(
        &self,
        options: ListAgentsOptions,
    ) -> Result<models::AgentList, NvokenError> {
        apis::agents_api::list_agents(
            &self.configuration,
            options.agent_key.as_deref(),
            options.cursor.as_deref(),
            options.limit,
        )
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

    /// Stops an Invocation gracefully and keeps its work. The turn settles
    /// `completed` with stop reason `interrupted` once it reaches an execution
    /// seam, so the messages it already produced stay in the Session.
    /// [`Self::cancel_invocation`] is the discarding stop.
    pub async fn interrupt_invocation(
        &self,
        invocation_id: &str,
    ) -> Result<models::Invocation, NvokenError> {
        apis::invocations_api::interrupt_invocation(&self.configuration, invocation_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    /// Appends steering to a running Invocation without ending it. The turn
    /// keeps everything it has already produced — the difference from
    /// supersession, which rewinds — and the model sees the input at its next
    /// execution seam rather than immediately.
    ///
    /// Input the turn never reaches is settled `expired` when the Invocation
    /// settles; nvoken never re-homes it onto a later turn, so re-sending
    /// missed direction as the next Invocation's input is the caller's call.
    ///
    /// `idempotency_key` makes a retry safe: the same key with the same
    /// content returns the original acknowledgement with `deduped` set, and
    /// the same key with different content is refused.
    pub async fn nudge_invocation(
        &self,
        invocation_id: &str,
        content: &str,
        idempotency_key: Option<&str>,
    ) -> Result<models::NudgeAcknowledgement, NvokenError> {
        let mut request = models::NudgeInvocationRequest::new(models::InvocationInput::String(
            content.to_string(),
        ));
        request.idempotency_key = idempotency_key.map(str::to_string);
        apis::invocations_api::nudge_invocation(&self.configuration, invocation_id, request)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    /// Reads the staged queue in the order the turn will consume it, settled
    /// rows included.
    pub async fn list_pending_inputs(
        &self,
        invocation_id: &str,
        status: Option<models::PendingInputStatus>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::PendingInputList, NvokenError> {
        apis::invocations_api::list_pending_inputs(
            &self.configuration,
            invocation_id,
            status,
            cursor,
            limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    /// Reads durable ToolCall lifecycle records in discovery order.
    pub async fn list_tool_calls(
        &self,
        invocation_id: &str,
        options: ToolCallListOptions,
    ) -> Result<models::ToolCallList, NvokenError> {
        apis::invocations_api::list_tool_calls(
            &self.configuration,
            invocation_id,
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    /// Withdraws staged input the turn has not taken. Input the executor
    /// already drained is reported as a conflict rather than removed from a
    /// transcript it is already part of.
    pub async fn cancel_pending_input(
        &self,
        invocation_id: &str,
        pending_input_id: &str,
    ) -> Result<models::PendingInput, NvokenError> {
        apis::invocations_api::cancel_pending_input(
            &self.configuration,
            invocation_id,
            pending_input_id,
        )
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
        let mut statuses = options.statuses;
        if let Some(status) = options.status {
            statuses.push(status);
        }
        apis::invocations_api::list_invocations(
            &self.configuration,
            options.tenant_key.as_deref(),
            options.default_tenant,
            options.user_key.as_deref(),
            options.session_id.as_deref(),
            options.agent_id.as_deref(),
            options.agent_key.as_deref(),
            (!statuses.is_empty()).then_some(statuses),
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

    /// Erases a Session and everything under it: its Invocations, transcript,
    /// checkpoints, tool calls, artifacts, and undelivered webhooks. The
    /// erasure is immediate and irreversible.
    ///
    /// A running Invocation is stopped, and no cancellation is recorded — the
    /// Invocation is removed rather than settled, so no `invocation.settled`
    /// webhook is emitted for it. Cancel first if you need a settled
    /// record.
    ///
    /// An unknown or out-of-scope Session is not found, so a retry after a lost
    /// response can treat that as already-done.
    ///
    /// This is not account erasure by itself: nvoken keeps no account
    /// tombstone, so a caller honouring a deletion request must stop admitting
    /// work for the tenant before paginating and deleting.
    pub async fn delete_session(&self, session_id: &str) -> Result<(), NvokenError> {
        apis::sessions_api::delete_session(&self.configuration, session_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    /// Merges a metadata patch into a Session: a present key replaces, a `None`
    /// value deletes, and an unmentioned key survives. Merging rather than
    /// replacing is what stops independent writers — a title UI and correlation
    /// tooling — from silently discarding each other's keys.
    ///
    /// The request is sent directly rather than through the generated client
    /// because the generator flattens the patch value's `string | null` union
    /// to `string`, so the deleting half of the patch is not expressible in the
    /// generated type. It still goes through the configured middleware, so
    /// retry and response-metadata behaviour are unchanged.
    pub async fn update_session(
        &self,
        session_id: &str,
        metadata: HashMap<String, Option<String>>,
    ) -> Result<models::Session, NvokenError> {
        let mut request = self
            .configuration
            .client
            .patch(format!(
                "{}/v1/sessions/{}",
                self.configuration.base_path,
                apis::urlencode(session_id),
            ))
            .json(&serde_json::json!({ "metadata": metadata }));
        if let Some(token) = &self.configuration.bearer_access_token {
            request = request.bearer_auth(token);
        }
        let response = request
            .send()
            .await
            .map_err(|error| NvokenError::transport(error.to_string()))?;
        let status = response.status();
        let headers = response.headers().clone();
        if !status.is_success() {
            let body = response.json::<Value>().await.unwrap_or(Value::Null);
            return Err(NvokenError::response_with_headers(status, body, &headers));
        }
        response
            .json::<models::Session>()
            .await
            .map_err(|error| NvokenError::transport(error.to_string()))
    }

    pub async fn list_sessions(
        &self,
        options: ListSessionsOptions,
    ) -> Result<models::SessionList, NvokenError> {
        apis::sessions_api::list_sessions(
            &self.configuration,
            options.tenant_key.as_deref(),
            options.default_tenant,
            options.user_key.as_deref(),
            options.agent_id.as_deref(),
            options.agent_key.as_deref(),
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

    /// Returns newest-first immutable records for applied and fell-through
    /// Session compaction passes.
    pub async fn list_session_compactions(
        &self,
        session_id: &str,
        options: CompactionListOptions,
    ) -> Result<models::SessionCompactionList, NvokenError> {
        apis::sessions_api::list_session_compactions(
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

pub fn fetch_tool() -> Tool {
    Tool::fetch()
}

fn provider_key_selection(
    selection: ProviderKeySelection,
) -> Result<models::ProviderKeySelection, NvokenError> {
    let provider = model_provider(&selection.provider)?;
    match selection.source {
        ProviderKeySource::CallerEphemeral { api_key } => {
            if api_key.is_empty() {
                return Err(NvokenError::validation(
                    "caller-ephemeral provider keys require an API key",
                ));
            }
            Ok(models::ProviderKeySelection::ProviderKeySelectionOneOf(
                Box::new(models::ProviderKeySelectionOneOf::new(
                    provider,
                    models::provider_key_selection_one_of::Source::SourceCallerEphemeral,
                    models::ProviderStaticKey::new(api_key),
                )),
            ))
        }
        source => {
            let source = match source {
                ProviderKeySource::AppByok => {
                    models::provider_key_selection_one_of_1::Source::AppByok
                }
                ProviderKeySource::TenantByok => {
                    models::provider_key_selection_one_of_1::Source::TenantByok
                }
                ProviderKeySource::Platform => {
                    models::provider_key_selection_one_of_1::Source::Platform
                }
                ProviderKeySource::CallerEphemeral { .. } => unreachable!(),
            };
            Ok(models::ProviderKeySelection::ProviderKeySelectionOneOf1(
                Box::new(models::ProviderKeySelectionOneOf1::new(provider, source)),
            ))
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
        self.deadline_at = invocation.deadline_at;
    }

    pub async fn wait(
        &mut self,
        timeout: Option<Duration>,
    ) -> Result<models::Invocation, NvokenError> {
        self.wait_with_options(WaitOptions {
            timeout,
            ..WaitOptions::default()
        })
        .await
    }

    pub async fn wait_with_options(
        &mut self,
        options: WaitOptions,
    ) -> Result<models::Invocation, NvokenError> {
        if options.min_poll_interval.is_zero()
            || options.max_poll_interval < options.min_poll_interval
        {
            return Err(NvokenError::validation(
                "wait poll intervals must be positive and ordered",
            ));
        }
        let future = async {
            let mut delay = options.min_poll_interval;
            loop {
                let invocation = self.refresh().await?;
                let satisfied = match options.until {
                    WaitCondition::Terminal => terminal(invocation.status),
                    WaitCondition::Actionable => {
                        invocation.status == models::InvocationStatus::Waiting
                            || terminal(invocation.status)
                    }
                };
                if satisfied {
                    return Ok(invocation);
                }
                tokio::time::sleep(delay).await;
                delay = delay.saturating_mul(2).min(options.max_poll_interval);
            }
        };
        match options.timeout {
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

    pub async fn cancel(&self) -> Result<models::Invocation, NvokenError> {
        self.client.cancel_invocation(&self.invocation_id).await
    }

    pub async fn interrupt(&self) -> Result<models::Invocation, NvokenError> {
        self.client.interrupt_invocation(&self.invocation_id).await
    }

    /// Appends steering to this running turn. Not an interrupt: the model sees
    /// the input at the next execution seam, and nothing in flight is aborted.
    pub async fn nudge(&self, content: &str) -> Result<models::NudgeAcknowledgement, NvokenError> {
        self.client
            .nudge_invocation(&self.invocation_id, content, None)
            .await
    }

    /// [`Self::nudge`] with an idempotency key, so a retried call stages the
    /// direction once.
    pub async fn nudge_with_key(
        &self,
        content: &str,
        idempotency_key: &str,
    ) -> Result<models::NudgeAcknowledgement, NvokenError> {
        self.client
            .nudge_invocation(&self.invocation_id, content, Some(idempotency_key))
            .await
    }

    pub async fn list_pending_inputs(
        &self,
        status: Option<models::PendingInputStatus>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::PendingInputList, NvokenError> {
        self.client
            .list_pending_inputs(&self.invocation_id, status, cursor, limit)
            .await
    }

    pub async fn list_tool_calls(
        &self,
        options: ToolCallListOptions,
    ) -> Result<models::ToolCallList, NvokenError> {
        self.client
            .list_tool_calls(&self.invocation_id, options)
            .await
    }

    pub async fn cancel_pending_input(
        &self,
        pending_input_id: &str,
    ) -> Result<models::PendingInput, NvokenError> {
        self.client
            .cancel_pending_input(&self.invocation_id, pending_input_id)
            .await
    }

    pub async fn submit_tool_results(
        &self,
        results: Vec<ToolResult>,
    ) -> Result<models::SubmitHostToolResultsResponse, NvokenError> {
        self.client
            .submit_tool_results(&self.invocation_id, results)
            .await
    }

    pub async fn wait_for_action(
        &mut self,
        timeout: Option<Duration>,
    ) -> Result<models::Invocation, NvokenError> {
        self.wait_with_options(WaitOptions {
            until: WaitCondition::Actionable,
            timeout,
            ..WaitOptions::default()
        })
        .await
    }

    pub async fn wait_for_result(
        &mut self,
        timeout: Option<Duration>,
    ) -> Result<models::InvocationResult, NvokenError> {
        self.wait_for_result_with_options(WaitOptions {
            timeout,
            ..WaitOptions::default()
        })
        .await
    }

    pub async fn wait_for_result_with_options(
        &mut self,
        mut options: WaitOptions,
    ) -> Result<models::InvocationResult, NvokenError> {
        options.until = WaitCondition::Terminal;
        let invocation = self.wait_with_options(options).await?;
        if invocation.status != models::InvocationStatus::Completed {
            let mut error = NvokenError::new(
                ErrorCategory::Conflict,
                match invocation.stop_reason {
                    Some(reason) => format!(
                        "Invocation {} ended with status {} ({reason})",
                        self.invocation_id, invocation.status
                    ),
                    None => format!(
                        "Invocation {} ended with status {}",
                        self.invocation_id, invocation.status
                    ),
                },
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

    pub fn stream_with_options(
        &self,
        options: StreamOptions,
    ) -> impl futures_core::Stream<Item = Result<StreamEvent, NvokenError>> + '_ {
        stream_handle_with_options(self, options)
    }
}

fn generated_idempotency_key() -> String {
    format!(
        "nvoken-{:016x}{:016x}",
        fastrand::u64(..),
        fastrand::u64(..)
    )
}

/// Whether an Invocation has stopped for good. `Incomplete` is one of the four:
/// a turn the runtime cut off at a budget is over, and a wait helper that
/// treated only `Completed` as an ending would poll it forever.
fn terminal(status: models::InvocationStatus) -> bool {
    matches!(
        status,
        models::InvocationStatus::Completed
            | models::InvocationStatus::Incomplete
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
    fn provider_keys_map_ephemeral_and_stored_sources() {
        let ephemeral = provider_key_selection(ProviderKeySelection {
            provider: "openai".to_owned(),
            source: ProviderKeySource::CallerEphemeral {
                api_key: "secret".to_owned(),
            },
        })
        .unwrap();
        assert_eq!(
            serde_json::to_value(ephemeral).unwrap(),
            json!({
                "provider": "openai",
                "source": "caller_ephemeral",
                "key": {"api_key": "secret"},
            })
        );

        let stored = provider_key_selection(ProviderKeySelection {
            provider: "openai".to_owned(),
            source: ProviderKeySource::AppByok,
        })
        .unwrap();
        assert_eq!(
            serde_json::to_value(stored).unwrap(),
            json!({"provider": "openai", "source": "app_byok"})
        );
    }
}
