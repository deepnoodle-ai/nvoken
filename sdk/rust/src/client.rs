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
use serde::{Deserialize, Serialize, Serializer};
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

/// Omits an empty value rather than sending it, so a field the runtime
/// defaults stays defaulted there instead of being filled in twice.
fn optional_name(value: &str) -> Option<String> {
    (!value.is_empty()).then(|| value.to_string())
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
/// Declares a remote MCP server.
///
/// It carries no secrets: an Agent Definition may be shared
/// across turns, so authentication headers travel per Invocation in
/// `McpServerHeaders` instead.
pub struct McpServer {
    pub name: String,
    pub url: String,
    pub allowed_tools: Vec<String>,
    pub timeouts: Option<McpTimeouts>,
}

impl McpServer {
    pub fn new(name: impl Into<String>, url: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            url: url.into(),
            allowed_tools: Vec::new(),
            timeouts: None,
        }
    }

    pub fn allowed_tool(mut self, name: impl Into<String>) -> Self {
        self.allowed_tools.push(name.into());
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
        result.timeouts = self.timeouts.as_ref().map(|timeouts| {
            Box::new(models::McpTimeouts {
                discovery_seconds: timeouts.discovery_seconds,
                call_seconds: timeouts.call_seconds,
            })
        });
        result
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
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
/// completion, so a turn outlasting the window cannot expire underneath itself.
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
    /// Fixes the Agent Definition revision for the lifetime of a newly created
    /// Session. Omit it to follow the Agent's resolution policy.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub pinned_revision: Option<u32>,
    /// Binds the Session to the host's own authorization facts. It is written
    /// only by the request that creates the Session, never interpreted by
    /// nvoken, never visible to the model, and carried inside the signed
    /// callback envelope so a receiver authorizes a delivery without reading
    /// the Invocation back. What nvoken guarantees is integrity, not
    /// authentication: what creation recorded is what a signed delivery
    /// carries.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub authorization_context: Option<HashMap<String, String>>,
    /// What this request asserts about a Session that already exists.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub on_conflict: Option<SessionOptionsConflict>,
}

/// What a request asserts about a Session that already exists.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
pub enum SessionOptionsConflict {
    /// The default: compares every option sent.
    #[serde(rename = "refuse")]
    Refuse,
    /// Reaches whatever Session is there without asserting how it is
    /// configured, so compaction and retention stop conflicting. It never
    /// relaxes the authorization context, the revision pin, or the Session's
    /// end user: those catch a caller acting on the wrong conversation, and a
    /// flag that suppressed them would be a way around the check rather than a
    /// way to express intent.
    #[serde(rename = "join")]
    Join,
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

    pub fn pinned_revision(mut self, revision: u32) -> Self {
        self.pinned_revision = Some(revision);
        self
    }

    pub fn authorization_context(mut self, context: HashMap<String, String>) -> Self {
        self.authorization_context = Some(context);
        self
    }

    pub fn on_conflict(mut self, policy: SessionOptionsConflict) -> Self {
        self.on_conflict = Some(policy);
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

/// Which principal a definition's durable memories belong to.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MemoryScope {
    Tenant,
    /// Requires a user key on every admitted Invocation.
    User,
}

/// How much memory text a turn receives.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MemoryContextMode {
    Index,
    Full,
    /// Attaches the memory tools without putting memory text in the turn.
    Off,
}

#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct MemoryContextConfig {
    pub mode: Option<MemoryContextMode>,
    /// Defaults to 1536 for index and 131072 for full; must be zero for off.
    pub max_bytes: Option<u32>,
}

/// Opts a definition into durable memory and its three memory tools.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct MemoryConfig {
    pub scope: Option<MemoryScope>,
    pub context: Option<MemoryContextConfig>,
}

impl MemoryConfig {
    pub fn scope(mut self, scope: MemoryScope) -> Self {
        self.scope = Some(scope);
        self
    }

    pub fn context(mut self, context: MemoryContextConfig) -> Self {
        self.context = Some(context);
        self
    }
}

/// One definition-specific browser authorization.
///
/// It grants authorship and settlement only, never selective read visibility:
/// every public transcript item in a browser-reachable Session must be treated
/// as client-visible. `None` on a definition means it is not
/// client-token-capable; an empty `ClientInterface::default()` opts in with no
/// client-authored context or tools.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct ClientInterface {
    /// Recorded-context names a client may append or supersede, contextual
    /// tier only.
    pub context_names: Vec<String>,
    /// Host-mode tools whose parked calls a client may see and settle.
    pub tool_names: Vec<String>,
}

impl ClientInterface {
    pub fn context_name(mut self, name: impl Into<String>) -> Self {
        self.context_names.push(name.into());
        self
    }

    pub fn tool_name(mut self, name: impl Into<String>) -> Self {
        self.tool_names.push(name.into());
        self
    }
}

/// Everything writable on an App-owned Agent Definition.
///
/// It is flat, matching the contract's `AgentDefinitionWrite`. Reads return an
/// `AgentDefinitionResource`, which is this same flat object plus `id`,
/// `revision`, and timestamps, so a read-modify-write is a conversion and a
/// replace:
///
/// ```ignore
/// let current = client.get_agent_definition(id).await?;
/// let mut definition = AgentDefinition::from_resource(&current)?;
/// definition.instructions = Some("Be concise and warm.".to_string());
/// client
///     .update_agent_definition(
///         &current.id,
///         definition,
///         UpdateAgentDefinitionOptions::new(current.revision as u32),
///     )
///     .await?;
/// ```
#[derive(Debug, Clone, Default)]
pub struct AgentDefinition {
    pub model: Model,
    /// Caller-chosen immutable key, unique within the App. Required to create.
    /// A replacement cannot move a resource to another key, so it is dropped
    /// there and a definition read back from the server may carry one along.
    pub definition_key: Option<String>,
    /// Display name. Defaults to `definition_key`, and because a replacement
    /// replaces the whole resource, omitting it on update resets the name to
    /// the key rather than keeping the current one.
    pub name: Option<String>,
    pub instructions: Option<String>,
    pub sampling: Option<Sampling>,
    pub reasoning: Option<Reasoning>,
    pub tool_choice: Option<ToolChoice>,
    pub limits: Option<Limits>,
    pub tools: Vec<Tool>,
    pub mcp_servers: Vec<McpServer>,
    pub provider_tools: Vec<ProviderTool>,
    pub memory: Option<MemoryConfig>,
    pub client_interface: Option<ClientInterface>,
    pub output_schema: Option<HashMap<String, Value>>,
}

/// Safe per-turn replacements. These cannot expand tools, data access, memory,
/// browser authority, or instructions.
#[derive(Debug, Clone, Default)]
pub struct AgentDefinitionOverrides {
    pub model: Option<Model>,
    pub sampling: Option<Sampling>,
    pub reasoning: Option<Reasoning>,
    pub tool_choice: Option<ToolChoice>,
    pub limits: Option<Limits>,
    pub output_schema: Option<HashMap<String, Value>>,
}

impl AgentDefinitionOverrides {
    pub fn model(mut self, model: Model) -> Self {
        self.model = Some(model);
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

    pub fn limits(mut self, limits: Limits) -> Self {
        self.limits = Some(limits);
        self
    }

    pub fn output_schema(mut self, schema: HashMap<String, Value>) -> Self {
        self.output_schema = Some(schema);
        self
    }
}

impl AgentDefinition {
    pub fn new(model: Model) -> Self {
        Self {
            model,
            ..Self::default()
        }
    }

    /// Reads a resource back into the definition that produced it.
    ///
    /// A replacement replaces the whole resource, so a field this dropped
    /// would be erased on write. It therefore carries every writable field
    /// across and leaves the read-only ones — `id`, `revision`, and the
    /// timestamps — behind.
    pub fn from_resource(resource: &models::AgentDefinitionResource) -> Result<Self, NvokenError> {
        let mut tools = Vec::new();
        for declaration in resource.tools.iter().flatten() {
            tools.push(match declaration {
                models::ToolDeclaration::Builtin(_) => Tool::fetch(),
                models::ToolDeclaration::Host(host) => Tool::host(
                    host.name.clone(),
                    host.description.clone(),
                    host.input_schema.clone(),
                ),
                models::ToolDeclaration::Callback(callback) => Tool::callback(
                    callback.name.clone(),
                    callback.description.clone(),
                    callback.input_schema.clone(),
                    callback.callback.url.clone(),
                ),
            });
        }
        let limits = resource
            .limits
            .as_ref()
            .map(|limits| serde_json::from_value(json!(limits)))
            .transpose()
            .map_err(|error| NvokenError::validation(error.to_string()))?;
        Ok(Self {
            model: Model::new(
                resource.model.provider.to_string(),
                resource.model.id.clone(),
            ),
            definition_key: Some(resource.definition_key.clone()),
            name: Some(resource.name.clone()),
            instructions: resource.instructions.clone(),
            sampling: resource.sampling.as_ref().map(|sampling| Sampling {
                temperature: sampling.temperature,
            }),
            reasoning: resource.reasoning.as_ref().map(|reasoning| Reasoning {
                effort: reasoning.effort.map(|effort| match effort {
                    models::ReasoningEffort::EffortLow => ReasoningEffort::Low,
                    models::ReasoningEffort::EffortMedium => ReasoningEffort::Medium,
                    models::ReasoningEffort::EffortHigh => ReasoningEffort::High,
                    models::ReasoningEffort::EffortXHigh => ReasoningEffort::XHigh,
                    models::ReasoningEffort::EffortMax => ReasoningEffort::Max,
                }),
                budget_tokens: reasoning.budget_tokens,
            }),
            tool_choice: resource
                .tool_choice
                .as_ref()
                .map(|choice| match choice.mode {
                    models::ModelToolChoiceMode::ChoiceAuto => Ok(ToolChoice::Auto),
                    models::ModelToolChoiceMode::ChoiceNone => Ok(ToolChoice::None),
                    models::ModelToolChoiceMode::ChoiceRequired => Ok(ToolChoice::Required),
                    models::ModelToolChoiceMode::ChoiceNamed => {
                        choice.name.clone().map(ToolChoice::Named).ok_or_else(|| {
                            NvokenError::validation("tool choice named requires name")
                        })
                    }
                })
                .transpose()?,
            limits,
            tools,
            mcp_servers: resource
                .mcp_servers
                .iter()
                .flatten()
                .map(|server| McpServer {
                    name: server.name.clone(),
                    url: server.url.clone(),
                    allowed_tools: server.allowed_tools.clone().unwrap_or_default(),
                    timeouts: server.timeouts.as_ref().map(|timeouts| McpTimeouts {
                        discovery_seconds: timeouts.discovery_seconds,
                        call_seconds: timeouts.call_seconds,
                    }),
                })
                .collect(),
            provider_tools: resource
                .provider_tools
                .iter()
                .flatten()
                .map(|tool| {
                    let search = &tool.web_search;
                    ProviderTool::WebSearch(WebSearchTool {
                        max_uses: search.max_uses,
                        allowed_domains: search.allowed_domains.clone().unwrap_or_default(),
                        blocked_domains: search.blocked_domains.clone().unwrap_or_default(),
                        user_location: search.user_location.as_ref().map(|location| {
                            WebSearchLocation {
                                city: location.city.clone(),
                                region: location.region.clone(),
                                country: location.country.clone(),
                                timezone: location.timezone.clone(),
                            }
                        }),
                    })
                })
                .collect(),
            memory: resource.memory.as_ref().map(|memory| MemoryConfig {
                scope: memory.scope.map(|scope| match scope {
                    models::memory_config::Scope::Tenant => MemoryScope::Tenant,
                    models::memory_config::Scope::User => MemoryScope::User,
                }),
                context: memory.context.as_ref().map(|context| MemoryContextConfig {
                    mode: context.mode.map(|mode| match mode {
                        models::MemoryContextMode::Index => MemoryContextMode::Index,
                        models::MemoryContextMode::Full => MemoryContextMode::Full,
                        models::MemoryContextMode::False => MemoryContextMode::Off,
                    }),
                    max_bytes: context.max_bytes,
                }),
            }),
            client_interface: resource
                .client_interface
                .as_ref()
                .map(|interface| ClientInterface {
                    context_names: interface.context_names.clone().unwrap_or_default(),
                    tool_names: interface.tool_names.clone().unwrap_or_default(),
                }),
            output_schema: resource.output_schema.clone(),
        })
    }

    pub fn definition_key(mut self, key: impl Into<String>) -> Self {
        self.definition_key = Some(key.into());
        self
    }

    pub fn name(mut self, name: impl Into<String>) -> Self {
        self.name = Some(name.into());
        self
    }

    pub fn memory(mut self, memory: MemoryConfig) -> Self {
        self.memory = Some(memory);
        self
    }

    pub fn client_interface(mut self, client_interface: ClientInterface) -> Self {
        self.client_interface = Some(client_interface);
        self
    }

    pub fn instructions(mut self, instructions: impl Into<String>) -> Self {
        self.instructions = Some(instructions.into());
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

    pub fn limits(mut self, limits: Limits) -> Self {
        self.limits = Some(limits);
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
}

/// Secret headers for one MCP server named by the selected Agent Definition.
///
/// They are encrypted for a single turn, and are never stored in, hashed into,
/// or returned with the Agent Definition.
#[derive(Debug, Clone, Default)]
pub struct McpServerHeaders {
    pub name: String,
    pub headers: HashMap<String, String>,
}

impl McpServerHeaders {
    pub fn new(name: impl Into<String>, headers: HashMap<String, String>) -> Self {
        Self {
            name: name.into(),
            headers,
        }
    }
}

/// The recorded context bounds the Runtime enforces at admission.
const MAX_CONTEXT_ITEMS: usize = 8;
const MAX_CONTEXT_NAME_CHARS: usize = 64;
const MAX_CONTEXT_CONTENT_BYTES: usize = 8 << 10;
const MAX_CONTEXT_TOTAL_BYTES: usize = 16 << 10;

/// Matches the contract's `^[a-z][a-z0-9-]*$` without pulling in a regex crate.
fn valid_context_name(name: &str) -> bool {
    let mut chars = name.chars();
    match chars.next() {
        Some(first) if first.is_ascii_lowercase() => {}
        _ => return false,
    }
    chars.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-')
}

/// Controls how a recorded snapshot reaches the model.
///
/// `Contextual` is for conversation-adjacent facts, `Operator` for policy or
/// other application-authoritative state. The tier stays typed in the
/// transcript; the provider-native role is chosen when the turn generates.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ContextTier {
    Contextual,
    Operator,
}

/// One application-owned state snapshot recorded ahead of a turn's input.
///
/// `name` is a stable identity: sending it again supersedes the earlier value,
/// and an unchanged resend adds no transcript message, so a stateless host may
/// resend its whole snapshot every turn. Omit the reserved `app-` prefix the
/// model sees; nvoken adds it. Context is durable Session history rather than
/// an Agent Definition field, so it never changes the selected
/// `definition_id`.
#[derive(Debug, Clone)]
pub struct ContextItem {
    pub name: String,
    pub tier: ContextTier,
    pub content: String,
}

impl ContextItem {
    pub fn new(name: impl Into<String>, tier: ContextTier, content: impl Into<String>) -> Self {
        Self {
            name: name.into(),
            tier,
            content: content.into(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct InvokeRequest {
    pub agent_id: Option<String>,
    pub agent_key: Option<String>,
    pub tenant_key: Option<String>,
    /// Who this turn is for. The first request that opens a Session fixes its
    /// user key, including fixing it to absent; every later turn either sends
    /// the same one or leaves it out and inherits it. A turn naming a different
    /// end user is refused with `session_user_key_conflict`.
    ///
    /// It is a filter, and on an Agent whose Definition sets
    /// `memory.scope: user` it is also the memory partition — it decides whose
    /// durable memories the model can recall — so it is required on the turn
    /// that opens a Session for such an Agent.
    pub user_key: Option<String>,
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
    pub definition_revision: Option<u32>,
    pub overrides: Option<AgentDefinitionOverrides>,
    /// Per-turn secret headers, keyed to MCP server names in the selected
    /// Agent Definition. They live here rather than on `McpServer` because an
    /// Agent Definition may be reused across turns.
    pub mcp_server_headers: Vec<McpServerHeaders>,
    /// Ordered application state snapshots recorded before this turn's input.
    /// The list is order-sensitive and material to idempotency: a replay that
    /// reorders or edits an item conflicts rather than updating it.
    pub context: Vec<ContextItem>,
    pub provider_keys: Vec<ProviderKeySelection>,
    /// Endpoint nvoken posts a signed webhook to when this Invocation
    /// parks awaiting host tool results or ends. An empty event list selects
    /// every event, which is the safe default: dropping the waiting event would
    /// leave a parked host tool loop with nobody listening.
    pub webhook: Option<WebhookTarget>,
    /// Opaque host correlation data recorded on this Invocation. It is part of
    /// the admitted input, so it is immutable and material to idempotency: a
    /// replay carrying different metadata conflicts rather than updating it.
    /// Session metadata is separate and mutable — see `Client::update_session`.
    pub metadata: Option<HashMap<String, String>>,
}

impl InvokeRequest {
    pub fn new(agent_key: impl Into<String>, input: impl Into<String>) -> Self {
        Self {
            agent_id: None,
            agent_key: Some(agent_key.into()),
            tenant_key: None,
            user_key: None,
            session_id: None,
            session_key: None,
            session_options: None,
            idempotency_key: None,
            if_active: None,
            on_budget_exhausted: None,
            input: input.into(),
            input_blocks: Vec::new(),
            definition_revision: None,
            overrides: None,
            mcp_server_headers: Vec::new(),
            context: Vec::new(),
            provider_keys: Vec::new(),
            webhook: None,
            metadata: None,
        }
    }

    pub fn from_agent_id(agent_id: impl Into<String>, input: impl Into<String>) -> Self {
        let mut request = Self::new("", input);
        request.agent_key = None;
        request.agent_id = Some(agent_id.into());
        request
    }

    pub fn mcp_server_headers(mut self, headers: McpServerHeaders) -> Self {
        self.mcp_server_headers.push(headers);
        self
    }

    /// Appends one recorded context snapshot, in the order it should reach the
    /// model.
    pub fn context(mut self, item: ContextItem) -> Self {
        self.context.push(item);
        self
    }

    pub fn tenant_key(mut self, tenant_key: impl Into<String>) -> Self {
        self.tenant_key = Some(tenant_key.into());
        self
    }

    pub fn user_key(mut self, user_key: impl Into<String>) -> Self {
        self.user_key = Some(user_key.into());
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

    pub fn limits(mut self, limits: Limits) -> Self {
        self.overrides.get_or_insert_with(Default::default).limits = Some(limits);
        self
    }

    pub fn sampling(mut self, sampling: Sampling) -> Self {
        self.overrides.get_or_insert_with(Default::default).sampling = Some(sampling);
        self
    }

    pub fn reasoning(mut self, reasoning: Reasoning) -> Self {
        self.overrides
            .get_or_insert_with(Default::default)
            .reasoning = Some(reasoning);
        self
    }

    pub fn tool_choice(mut self, tool_choice: ToolChoice) -> Self {
        self.overrides
            .get_or_insert_with(Default::default)
            .tool_choice = Some(tool_choice);
        self
    }

    pub fn model(mut self, model: Model) -> Self {
        self.overrides.get_or_insert_with(Default::default).model = Some(model);
        self
    }

    pub fn output_schema(mut self, schema: HashMap<String, Value>) -> Self {
        self.overrides
            .get_or_insert_with(Default::default)
            .output_schema = Some(schema);
        self
    }

    pub fn definition_revision(mut self, revision: u32) -> Self {
        self.definition_revision = Some(revision);
        self
    }

    pub fn overrides(mut self, overrides: AgentDefinitionOverrides) -> Self {
        self.overrides = Some(overrides);
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
    Ended,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IfActivePolicy {
    Reject,
    Supersede,
    Interrupt,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum BudgetExhaustionBehavior {
    Stop,
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
    pub tenant_key: Option<String>,
    pub agent_key: Option<String>,
    pub definition_id: Option<String>,
    pub include_archived: Option<bool>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

#[derive(Debug, Clone)]
pub struct CreateAgentInput {
    pub tenant_key: Option<String>,
    pub agent_key: String,
    /// Display name recorded at creation. Empty defaults to the Agent key.
    pub name: String,
    /// The Agent Definition this Agent follows, by opaque ID or by
    /// `definition_key`. Supply exactly one.
    pub definition_id: Option<String>,
    pub definition_key: Option<String>,
    pub pinned_revision: Option<u32>,
}

#[derive(Debug, Clone, Default)]
pub struct UpdateAgentInput {
    pub name: Option<String>,
    pub pinned_revision: Option<u32>,
    pub clear_pinned_revision: bool,
}

/// How one create is sent, as opposed to what it says.
#[derive(Debug, Clone, Default)]
pub struct CreateAgentDefinitionOptions {
    /// Optional, and nothing is invented for it: a key this SDK made up would
    /// be new on every attempt and so could never deduplicate anything. The
    /// definition key already scopes replay.
    pub idempotency_key: Option<String>,
}

impl CreateAgentDefinitionOptions {
    pub fn idempotency_key(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }
}

/// How one replacement is sent, as opposed to what it says.
#[derive(Debug, Clone, Copy, Default)]
pub struct UpdateAgentDefinitionOptions {
    /// The revision the caller read, sent as `If-Match`. Required, because a
    /// replacement with no expectation is a lost update waiting to happen.
    pub expected_revision: u32,
}

impl UpdateAgentDefinitionOptions {
    pub fn new(expected_revision: u32) -> Self {
        Self { expected_revision }
    }
}

#[derive(Debug, Clone, Default)]
pub struct ListAgentDefinitionsOptions {
    /// Narrows the page to the resource this caller-owned key names. The key
    /// is unique within the App, so the page holds zero or one item.
    pub definition_key: Option<String>,
    pub include_archived: Option<bool>,
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

/// Filters for the reconciliation feed. It takes the same filters as the
/// ordinary listing, because it is the same collection read in a different
/// order.
#[derive(Debug, Clone, Default)]
pub struct ListEndedInvocationsOptions {
    pub tenant_key: Option<String>,
    pub default_tenant: Option<bool>,
    pub user_key: Option<String>,
    pub session_id: Option<String>,
    pub agent_id: Option<String>,
    pub agent_key: Option<String>,
    pub status: Option<models::InvocationStatus>,
    pub statuses: Vec<models::InvocationStatus>,
    /// Starts a feed that has no cursor yet. Mutually exclusive with `cursor`,
    /// which already carries a position.
    pub ended_since: Option<chrono::DateTime<chrono::FixedOffset>>,
    pub cursor: Option<String>,
    pub limit: Option<u32>,
}

/// One Session metadata patch: a present key replaces, a `None` value deletes,
/// and an unmentioned key survives.
#[derive(Debug, Clone, Default)]
pub struct UpdateSessionOptions {
    pub metadata: HashMap<String, Option<String>>,
}

impl UpdateSessionOptions {
    pub fn new(metadata: HashMap<String, Option<String>>) -> Self {
        Self { metadata }
    }
}

#[derive(Debug, Clone, Copy, Default)]
pub struct DeleteSessionOptions {
    /// Erases a Session even when it holds a nonterminal Invocation,
    /// discarding that turn's settlement.
    pub force: bool,
}

impl DeleteSessionOptions {
    pub fn force() -> Self {
        Self { force: true }
    }
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
    /// Defaults to [`ListOrder::Ascending`], oldest first.
    pub order: Option<ListOrder>,
}

/// Sequence order for a message page.
///
/// A cursor belongs to the direction that issued it and is refused by the
/// other, so page one direction to its end rather than turning around
/// mid-walk.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum ListOrder {
    /// Oldest first. The default when no order is requested.
    #[default]
    Ascending,
    /// Newest first.
    Descending,
}

impl ListOrder {
    pub fn as_str(self) -> &'static str {
        match self {
            ListOrder::Ascending => "asc",
            ListOrder::Descending => "desc",
        }
    }
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

/// Narrows every request a `Client` makes to one tenant, one end user, or
/// both. Anything outside it is reported as not found, so an id that arrives
/// from the wrong place cannot be acted on — which is what lets one app-wide
/// credential serve a whole application without an ownership check written at
/// every call site. A scope may only narrow: naming a tenant the credential is
/// not bound to is refused rather than silently returning nothing.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Scope {
    pub tenant_key: Option<String>,
    pub user_key: Option<String>,
}

impl Scope {
    pub fn tenant(tenant_key: impl Into<String>) -> Self {
        Self {
            tenant_key: Some(tenant_key.into()),
            user_key: None,
        }
    }

    pub fn user(mut self, user_key: impl Into<String>) -> Self {
        self.user_key = Some(user_key.into());
        self
    }

    /// Drops a scope that names nobody, so an empty one is not a silent no-op.
    fn narrowing(self) -> Option<Self> {
        let tenant_key = self.tenant_key.and_then(|key| non_blank(key));
        let user_key = self.user_key.and_then(|key| non_blank(key));
        if tenant_key.is_none() && user_key.is_none() {
            return None;
        }
        Some(Self {
            tenant_key,
            user_key,
        })
    }
}

fn narrowing_scope(scope: Scope) -> Result<Scope, NvokenError> {
    scope.narrowing().ok_or_else(|| {
        NvokenError::validation("a scope requires a tenant key, a user key, or both")
    })
}

fn scope_headers(scope: Option<&Scope>) -> Result<reqwest::header::HeaderMap, NvokenError> {
    let mut headers = reqwest::header::HeaderMap::new();
    let Some(scope) = scope else {
        return Ok(headers);
    };
    for (name, value) in [
        ("x-nvoken-tenant-key", scope.tenant_key.as_deref()),
        ("x-nvoken-user-key", scope.user_key.as_deref()),
    ] {
        let Some(value) = value else {
            continue;
        };
        let value = reqwest::header::HeaderValue::from_str(value).map_err(|_| {
            NvokenError::validation(format!("{name} must be a valid HTTP header value"))
        })?;
        headers.insert(name, value);
    }
    Ok(headers)
}

fn non_blank(value: String) -> Option<String> {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        None
    } else {
        Some(trimmed.to_owned())
    }
}

#[derive(Clone)]
pub struct Client {
    pub(crate) configuration: Arc<apis::configuration::Configuration>,
    pub(crate) stream_client: reqwest::Client,
    response_metadata: ResponseMetadataStore,
    retry_policy: RetryPolicy,
    scope: Option<Scope>,
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
        Self::build(base_url, api_key, retry_policy, None, None)
    }

    /// Narrows the client at construction, for the common case where an
    /// application makes one client per tenant rather than deriving them.
    pub fn with_scope(
        base_url: impl Into<String>,
        api_key: impl Into<String>,
        scope: Scope,
    ) -> Result<Self, NvokenError> {
        let scope = narrowing_scope(scope)?;
        Self::build(base_url, api_key, RetryPolicy::default(), Some(scope), None)
    }

    /// Returns a client that stamps this scope on every request it makes. The
    /// receiver is unchanged, so a scoped client can be handed to the part of
    /// an application that handles one tenant's or one end user's work while
    /// the unscoped one keeps doing administrative reads.
    pub fn scoped(&self, scope: Scope) -> Result<Self, NvokenError> {
        let scope = narrowing_scope(scope)?;
        Self::build(
            self.configuration.base_path.clone(),
            self.configuration
                .bearer_access_token
                .clone()
                .unwrap_or_default(),
            self.retry_policy.clone(),
            Some(scope),
            Some(self.session_locks.clone()),
        )
    }

    /// Reports the scope this client stamps, `None` when it stamps none.
    pub fn scope(&self) -> Option<&Scope> {
        self.scope.as_ref()
    }

    fn build(
        base_url: impl Into<String>,
        api_key: impl Into<String>,
        retry_policy: RetryPolicy,
        scope: Option<Scope>,
        session_locks: Option<Arc<Mutex<HashMap<String, Arc<tokio::sync::Mutex<()>>>>>>,
    ) -> Result<Self, NvokenError> {
        let base_url = base_url.into().trim_end_matches('/').to_owned();
        let api_key = api_key.into();
        if base_url.is_empty() || api_key.is_empty() {
            return Err(NvokenError::validation("base URL and API key are required"));
        }
        let user_agent = format!("nvoken-rust/{}", crate::VERSION);
        let transport = reqwest::Client::builder()
            .user_agent(&user_agent)
            .default_headers(scope_headers(scope.as_ref())?)
            .build()
            .map_err(|error| NvokenError::transport(error.to_string()))?;
        let response_metadata = ResponseMetadataStore::default();
        let middleware = MiddlewareClientBuilder::new(transport.clone())
            .with(ResponseMetadataObserver {
                metadata: response_metadata.clone(),
            })
            .with(ReplaySafeRetry {
                policy: retry_policy.clone(),
            })
            .build();
        let configuration = apis::configuration::Configuration {
            base_path: base_url,
            user_agent: Some(user_agent),
            client: middleware,
            bearer_access_token: Some(api_key),
            ..Default::default()
        };
        Ok(Self {
            configuration: Arc::new(configuration),
            stream_client: transport,
            response_metadata,
            retry_policy,
            scope,
            session_locks: session_locks.unwrap_or_else(|| Arc::new(Mutex::new(HashMap::new()))),
        })
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

    pub async fn allocate_credits(
        &self,
        request: models::AllocateCreditsRequest,
    ) -> Result<models::AllocateCreditsResult, NvokenError> {
        apis::credits_api::allocate_credits(&self.configuration, request)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_credit_accounts(
        &self,
        tenant_key: Option<&str>,
        default_tenant: Option<bool>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::CreditAccountList, NvokenError> {
        apis::credits_api::list_credit_accounts(
            &self.configuration,
            tenant_key,
            default_tenant,
            cursor,
            limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_credit_allocations(
        &self,
        tenant_key: Option<&str>,
        default_tenant: Option<bool>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::CreditAllocationList, NvokenError> {
        apis::credits_api::list_credit_allocations(
            &self.configuration,
            tenant_key,
            default_tenant,
            cursor,
            limit,
        )
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
    /// Renders one Agent Definition.
    ///
    /// Only the definition's own content is checked here; installation state,
    /// App signing keys, credits, provider keys, and model lifecycle are checked
    /// again when a turn is admitted.
    pub fn agent_definition_body(
        &self,
        definition: AgentDefinition,
    ) -> Result<models::AgentDefinitionWrite, NvokenError> {
        if definition.model.is_unset() {
            return Err(NvokenError::validation("model is required"));
        }
        if let Some(schema) = &definition.output_schema {
            preflight_output_schema(schema)?;
        }
        let provider = model_provider(&definition.model.provider)?;
        let model =
            models::ModelInput::Model(Box::new(models::Model::new(provider, definition.model.id)));
        let sampling = definition
            .sampling
            .map(|value| models::Sampling::new(value.temperature))
            .map(Box::new);
        let reasoning = definition.reasoning.map(|value| {
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
        let tool_choice = definition.tool_choice.map(|value| {
            let (mode, name) = match value {
                ToolChoice::Auto => (models::ModelToolChoiceMode::ChoiceAuto, None),
                ToolChoice::None => (models::ModelToolChoiceMode::ChoiceNone, None),
                ToolChoice::Required => (models::ModelToolChoiceMode::ChoiceRequired, None),
                ToolChoice::Named(name) => (models::ModelToolChoiceMode::ChoiceNamed, Some(name)),
            };
            Box::new(models::ToolChoice { mode, name })
        });
        let limits = definition
            .limits
            .map(|value| serde_json::from_value(json!(value)))
            .transpose()
            .map_err(|error| NvokenError::validation(error.to_string()))?
            .map(Box::new);
        let mut tools = Vec::with_capacity(definition.tools.len());
        for tool in definition.tools {
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
        let mut body = models::AgentDefinitionWrite::new(model);
        body.definition_key = definition.definition_key;
        body.name = definition.name;
        body.instructions = definition.instructions;
        body.memory = definition.memory.map(|memory| {
            Box::new(models::MemoryConfig {
                scope: memory.scope.map(|scope| match scope {
                    MemoryScope::Tenant => models::memory_config::Scope::Tenant,
                    MemoryScope::User => models::memory_config::Scope::User,
                }),
                context: memory.context.map(|context| {
                    Box::new(models::MemoryContextConfig {
                        mode: context.mode.map(|mode| match mode {
                            MemoryContextMode::Index => models::MemoryContextMode::Index,
                            MemoryContextMode::Full => models::MemoryContextMode::Full,
                            MemoryContextMode::Off => models::MemoryContextMode::False,
                        }),
                        max_bytes: context.max_bytes,
                    })
                }),
            })
        });
        // An empty `ClientInterface` is not the same as none: it opts the
        // definition into client tokens with no client-authored context or
        // tools, so the empty object has to reach the wire.
        body.client_interface = definition.client_interface.map(|interface| {
            Box::new(models::BrowserClientInterface {
                context_names: (!interface.context_names.is_empty())
                    .then_some(interface.context_names),
                tool_names: (!interface.tool_names.is_empty()).then_some(interface.tool_names),
            })
        });
        body.sampling = sampling;
        body.reasoning = reasoning;
        body.tool_choice = tool_choice;
        body.limits = limits;
        body.output_schema = definition.output_schema;
        body.tools = (!tools.is_empty()).then_some(tools);
        body.mcp_servers = (!definition.mcp_servers.is_empty()).then(|| {
            definition
                .mcp_servers
                .iter()
                .map(McpServer::generated)
                .collect()
        });
        body.provider_tools = (!definition.provider_tools.is_empty()).then(|| {
            definition
                .provider_tools
                .iter()
                .map(ProviderTool::generated)
                .collect()
        });
        Ok(body)
    }

    fn agent_definition_overrides_body(
        &self,
        overrides: AgentDefinitionOverrides,
    ) -> Result<models::AgentDefinitionOverrides, NvokenError> {
        if let Some(schema) = &overrides.output_schema {
            preflight_output_schema(schema)?;
        }
        let model = overrides
            .model
            .map(|model| {
                if model.is_unset() {
                    return Err(NvokenError::validation("override model is required"));
                }
                let provider = model_provider(&model.provider)?;
                Ok::<_, NvokenError>(Box::new(models::ModelInput::Model(Box::new(
                    models::Model::new(provider, model.id),
                ))))
            })
            .transpose()?;
        let sampling = overrides
            .sampling
            .map(|value| Box::new(models::Sampling::new(value.temperature)));
        let reasoning = overrides.reasoning.map(|value| {
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
        let tool_choice = overrides.tool_choice.map(|value| {
            let (mode, name) = match value {
                ToolChoice::Auto => (models::ModelToolChoiceMode::ChoiceAuto, None),
                ToolChoice::None => (models::ModelToolChoiceMode::ChoiceNone, None),
                ToolChoice::Required => (models::ModelToolChoiceMode::ChoiceRequired, None),
                ToolChoice::Named(name) => (models::ModelToolChoiceMode::ChoiceNamed, Some(name)),
            };
            Box::new(models::ToolChoice { mode, name })
        });
        let limits = overrides
            .limits
            .map(|value| serde_json::from_value(json!(value)))
            .transpose()
            .map_err(|error| NvokenError::validation(error.to_string()))?
            .map(Box::new);
        Ok(models::AgentDefinitionOverrides {
            model,
            sampling,
            reasoning,
            tool_choice,
            limits,
            output_schema: overrides.output_schema,
        })
    }

    /// Checks the per-turn MCP secret headers.
    ///
    /// Server names are resolved against the selected Agent Definition by the
    /// service.
    fn mcp_server_headers_body(
        request: &InvokeRequest,
    ) -> Result<Option<Vec<models::McpServerHeaders>>, NvokenError> {
        let mut seen = std::collections::HashSet::new();
        let mut entries = Vec::with_capacity(request.mcp_server_headers.len());
        for entry in &request.mcp_server_headers {
            if entry.name.is_empty() {
                return Err(NvokenError::validation(
                    "mcp server headers require a server name",
                ));
            }
            if entry.headers.is_empty() {
                return Err(NvokenError::validation(format!(
                    "mcp server headers for {} require at least one header",
                    entry.name
                )));
            }
            if !seen.insert(entry.name.clone()) {
                return Err(NvokenError::validation(format!(
                    "mcp server headers name {} is repeated",
                    entry.name
                )));
            }
            entries.push(models::McpServerHeaders::new(
                entry.name.clone(),
                entry.headers.clone(),
            ));
        }
        Ok((!entries.is_empty()).then_some(entries))
    }

    /// Checks the recorded context against the bounds the Runtime enforces at
    /// admission, so a snapshot that cannot be admitted fails before a request
    /// is spent. The per-Session limit of 16 distinct names is left to the
    /// service, which is the only side that knows what a Session has already
    /// recorded.
    fn context_body(
        request: &InvokeRequest,
    ) -> Result<Option<Vec<models::InvocationContextItem>>, NvokenError> {
        if request.context.len() > MAX_CONTEXT_ITEMS {
            return Err(NvokenError::validation(format!(
                "context accepts at most {MAX_CONTEXT_ITEMS} items"
            )));
        }
        let mut seen = std::collections::HashSet::new();
        let mut total = 0usize;
        let mut entries = Vec::with_capacity(request.context.len());
        for item in &request.context {
            if item.name.chars().count() > MAX_CONTEXT_NAME_CHARS || !valid_context_name(&item.name)
            {
                return Err(NvokenError::validation(format!(
                    "context name {} must match ^[a-z][a-z0-9-]*$ and be at most {MAX_CONTEXT_NAME_CHARS} characters",
                    item.name
                )));
            }
            if !seen.insert(item.name.clone()) {
                return Err(NvokenError::validation(format!(
                    "context name {} is repeated",
                    item.name
                )));
            }
            if item.content.is_empty() {
                return Err(NvokenError::validation(format!(
                    "context {} content cannot be empty",
                    item.name
                )));
            }
            if item.content.len() > MAX_CONTEXT_CONTENT_BYTES {
                return Err(NvokenError::validation(format!(
                    "context {} content exceeds {MAX_CONTEXT_CONTENT_BYTES} bytes",
                    item.name
                )));
            }
            total += item.content.len();
            if total > MAX_CONTEXT_TOTAL_BYTES {
                return Err(NvokenError::validation(format!(
                    "context content totals more than {MAX_CONTEXT_TOTAL_BYTES} bytes"
                )));
            }
            entries.push(models::InvocationContextItem::new(
                item.name.clone(),
                match item.tier {
                    ContextTier::Contextual => models::invocation_context_item::Tier::Contextual,
                    ContextTier::Operator => models::invocation_context_item::Tier::Operator,
                },
                item.content.clone(),
            ));
        }
        Ok((!entries.is_empty()).then_some(entries))
    }

    pub fn invocation_body(
        &self,
        request: InvokeRequest,
    ) -> Result<models::CreateInvocationRequest, NvokenError> {
        if request.agent_id.is_some() == request.agent_key.is_some() {
            return Err(NvokenError::validation(
                "supply exactly one of agent_id and agent_key",
            ));
        }
        if request.agent_id.as_ref().is_some_and(String::is_empty)
            || request.agent_key.as_ref().is_some_and(String::is_empty)
        {
            return Err(NvokenError::validation("agent identity must not be empty"));
        }
        if request.input.is_empty() == request.input_blocks.is_empty() {
            return Err(NvokenError::validation(
                "supply exactly one of input and input blocks",
            ));
        }
        if !request.input_blocks.is_empty() {
            preflight_input_blocks(&request.input_blocks)?;
        }
        let mcp_server_headers = Self::mcp_server_headers_body(&request)?;
        let context = Self::context_body(&request)?;
        let overrides = request
            .overrides
            .map(|overrides| self.agent_definition_overrides_body(overrides))
            .transpose()?
            .map(Box::new);
        let input = if request.input_blocks.is_empty() {
            models::InvocationInput::String(request.input)
        } else {
            models::InvocationInput::ArrayVecInputBlock(request.input_blocks)
        };
        let mut body = models::CreateInvocationRequest::new(
            request
                .idempotency_key
                .unwrap_or_else(generated_idempotency_key),
            input,
        );
        body.agent_id = request.agent_id;
        body.agent_key = request.agent_key;
        body.definition_revision = request.definition_revision.map(u64::from);
        body.overrides = overrides;
        body.mcp_server_headers = mcp_server_headers;
        body.context = context;
        body.tenant_key = request.tenant_key;
        body.user_key = request.user_key;
        body.session_id = request.session_id;
        body.session_key = request.session_key;
        body.session_options = request
            .session_options
            .map(|value| {
                if value.compaction.is_none()
                    && value.retention.is_none()
                    && value.pinned_revision.is_none()
                    && value.authorization_context.is_none()
                    && value.on_conflict.is_none()
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
                options.pinned_revision = value.pinned_revision.map(u64::from);
                options.authorization_context = value.authorization_context;
                options.on_conflict = value.on_conflict.map(|policy| match policy {
                    SessionOptionsConflict::Refuse => models::session_options::OnConflict::Refuse,
                    SessionOptionsConflict::Join => models::session_options::OnConflict::Join,
                });
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
            BudgetExhaustionBehavior::Stop => {
                models::create_invocation_request::OnBudgetExhausted::Stop
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
                                WebhookEvent::Ended => models::WebhookEvent::WebhookEventEnded,
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

    /// Discovers the tools a remote MCP server projects.
    ///
    /// Headers are a separate argument because `McpServer` is durable Agent
    /// Definition configuration and therefore carries no secrets;
    /// these are used for this one discovery request and never stored.
    pub async fn list_mcp_tools(
        &self,
        server: &McpServer,
        headers: Option<HashMap<String, String>>,
    ) -> Result<models::McpListToolsResponse, NvokenError> {
        let mut request = models::McpListToolsRequest::new(server.generated());
        request.headers = headers.filter(|values| !values.is_empty());
        apis::mcp_api::list_mcp_tools(&self.configuration, request)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    /// Creates one reusable App-owned Agent Definition resource, or returns the
    /// one its key names.
    ///
    /// `definition_key` is unique within the App, so this is ensure-shaped:
    /// restating an existing definition returns it, and a key already held by a
    /// different definition is a conflict naming the resource to update
    /// instead. `name` defaults to the key. The optional idempotency key pins
    /// replay to a specific create; the definition key already scopes replay
    /// without it.
    pub async fn create_agent_definition(
        &self,
        definition: AgentDefinition,
        options: CreateAgentDefinitionOptions,
    ) -> Result<models::AgentDefinitionResource, NvokenError> {
        let write = self.agent_definition_body(definition)?;
        let definition_key = write
            .definition_key
            .filter(|key| !key.is_empty())
            .ok_or_else(|| NvokenError::validation("definition_key is required"))?;
        let body = models::AgentDefinitionCreate {
            definition_key,
            name: write.name,
            instructions: write.instructions,
            model: write.model,
            sampling: write.sampling,
            reasoning: write.reasoning,
            tool_choice: write.tool_choice,
            limits: write.limits,
            output_schema: write.output_schema,
            tools: write.tools,
            mcp_servers: write.mcp_servers,
            provider_tools: write.provider_tools,
            memory: write.memory,
            client_interface: write.client_interface,
        };
        apis::agent_definitions_api::create_agent_definition(
            &self.configuration,
            body,
            options
                .idempotency_key
                .as_deref()
                .filter(|key| !key.is_empty()),
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_agent_definition(
        &self,
        definition_id: &str,
    ) -> Result<models::AgentDefinitionResource, NvokenError> {
        apis::agent_definitions_api::get_agent_definition(&self.configuration, definition_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_agent_definitions(
        &self,
        options: ListAgentDefinitionsOptions,
    ) -> Result<models::AgentDefinitionResourceList, NvokenError> {
        apis::agent_definitions_api::list_agent_definitions(
            &self.configuration,
            options.definition_key.as_deref(),
            options.include_archived,
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_agent_definition_revision(
        &self,
        definition_id: &str,
        revision: u32,
    ) -> Result<models::AgentDefinitionResource, NvokenError> {
        apis::agent_definitions_api::get_agent_definition_revision(
            &self.configuration,
            definition_id,
            u64::from(revision),
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    /// Replaces one Agent Definition, failing when it has moved on.
    ///
    /// This replaces the whole resource, so send back everything you want
    /// kept. `AgentDefinition::from_resource` reads one back into a definition
    /// that carries every writable field across. The expected revision travels
    /// as `If-Match`, so a concurrent write fails rather than overwriting.
    pub async fn update_agent_definition(
        &self,
        definition_id: &str,
        definition: AgentDefinition,
        options: UpdateAgentDefinitionOptions,
    ) -> Result<models::AgentDefinitionResource, NvokenError> {
        if options.expected_revision < 1 {
            return Err(NvokenError::validation("expected_revision is required"));
        }
        let mut body = self.agent_definition_body(definition)?;
        // A replacement cannot move a resource to another key, so a definition
        // read back from the server carries one that is dropped here.
        body.definition_key = None;
        let expected_revision = options.expected_revision;
        apis::agent_definitions_api::update_agent_definition(
            &self.configuration,
            &format!("\"{expected_revision}\""),
            definition_id,
            body,
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

    pub async fn archive_agent_definition(&self, definition_id: &str) -> Result<(), NvokenError> {
        apis::agent_definitions_api::archive_agent_definition(&self.configuration, definition_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn restore_agent_definition(&self, definition_id: &str) -> Result<(), NvokenError> {
        apis::agent_definitions_api::restore_agent_definition(&self.configuration, definition_id)
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

    pub async fn create_agent(
        &self,
        input: CreateAgentInput,
    ) -> Result<models::Agent, NvokenError> {
        if input.definition_id.is_some() == input.definition_key.is_some() {
            return Err(NvokenError::validation(
                "supply exactly one of definition_id and definition_key",
            ));
        }
        let mut body = models::CreateAgentRequest::new(input.agent_key);
        body.name = optional_name(&input.name);
        body.tenant_key = input.tenant_key;
        body.definition_id = input.definition_id;
        body.definition_key = input.definition_key;
        body.pinned_revision = input.pinned_revision.map(u64::from);
        apis::agents_api::create_agent(&self.configuration, body)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn get_agent(&self, agent_id: &str) -> Result<models::Agent, NvokenError> {
        apis::agents_api::get_agent(&self.configuration, agent_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn list_agents(
        &self,
        options: ListAgentsOptions,
    ) -> Result<models::AgentList, NvokenError> {
        apis::agents_api::list_agents(
            &self.configuration,
            options.tenant_key.as_deref(),
            options.agent_key.as_deref(),
            options.definition_id.as_deref(),
            options.include_archived,
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn update_agent(
        &self,
        agent_id: &str,
        input: UpdateAgentInput,
    ) -> Result<models::Agent, NvokenError> {
        if input.pinned_revision.is_some() && input.clear_pinned_revision {
            return Err(NvokenError::validation(
                "pinned_revision and clear_pinned_revision are mutually exclusive",
            ));
        }
        let mut body = models::UpdateAgentRequest::new();
        body.name = input.name;
        body.pinned_revision = if input.clear_pinned_revision {
            Some(None)
        } else {
            input
                .pinned_revision
                .map(|revision| Some(u64::from(revision)))
        };
        apis::agents_api::update_agent(&self.configuration, agent_id, body)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn archive_agent(&self, agent_id: &str) -> Result<(), NvokenError> {
        apis::agents_api::archive_agent(&self.configuration, agent_id)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    pub async fn restore_agent(&self, agent_id: &str) -> Result<(), NvokenError> {
        apis::agents_api::restore_agent(&self.configuration, agent_id)
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
    /// Input the turn never reaches is marked `expired` when the Invocation
    /// settles; nvoken never re-homes it onto a later turn, so re-sending
    /// missed direction as the next Invocation's input is the caller's call.
    ///
    /// `idempotency_key` makes a retry safe: the same key with the same
    /// content returns the original acknowledgement with `deduplicated` set, and
    /// the same key with different content is refused.
    pub async fn create_nudge(
        &self,
        invocation_id: &str,
        content: &str,
        idempotency_key: Option<&str>,
    ) -> Result<models::NudgeAcknowledgement, NvokenError> {
        let mut request =
            models::CreateNudgeRequest::new(models::InvocationInput::String(content.to_string()));
        request.idempotency_key = idempotency_key.map(str::to_string);
        apis::invocations_api::create_nudge(&self.configuration, invocation_id, request)
            .await
            .map_err(|error| self.normalize_generated_error(error))
    }

    /// Reads the staged queue in the order the turn will consume it, ended
    /// rows included.
    pub async fn list_nudges(
        &self,
        invocation_id: &str,
        status: Option<models::NudgeStatus>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::NudgeList, NvokenError> {
        apis::invocations_api::list_nudges(
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
    pub async fn cancel_nudge(
        &self,
        invocation_id: &str,
        nudge_id: &str,
    ) -> Result<models::Nudge, NvokenError> {
        apis::invocations_api::cancel_nudge(&self.configuration, invocation_id, nudge_id)
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
            None,
            None,
            options.cursor.as_deref(),
            options.limit,
        )
        .await
        .map_err(|error| self.normalize_generated_error(error))
    }

    /// Reads one page of the reconciliation feed: turns that ended, oldest
    /// first by the moment they ended. Walk it and append by `id`.
    ///
    /// This is the backstop for settlement. `invocation.ended` webhooks are
    /// delivered at least once, so a delivery that never lands leaves a turn
    /// nobody settles — silently, since nothing errors and the only evidence is
    /// a ledger row that was never written. Reading this to the end is how you
    /// find out. [`Client::list_invocations`] cannot stand in: it is
    /// newest-first over current state, so a turn ending mid-page moves under
    /// the caller and a terminal status filter gives a set with no position in
    /// it.
    ///
    /// `next_cursor` is always set here, including on an empty page, so a
    /// consumer that catches up keeps its place without special-casing. Keep
    /// calling while `has_more`; when it is false you are caught up.
    ///
    /// `complete_through` is the instant the feed is complete to. Turns that
    /// ended after it are held back until their settling transactions are
    /// certainly visible, because a turn appearing behind the cursor is one you
    /// never see again. It is also the value to alarm on: one that stops
    /// advancing means settlement has stalled rather than that nothing ended.
    ///
    /// There is deliberately no auto-paging helper. The cursor is the one thing
    /// that has to survive the process, and hiding it is how a consumer loses
    /// its place; store it yourself between pages.
    pub async fn list_ended_invocations(
        &self,
        options: ListEndedInvocationsOptions,
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
            Some(true),
            options.ended_since,
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
    /// A Session holding a nonterminal Invocation is refused with
    /// `session_invocation_active` unless `force`. Erasure skips settlement —
    /// the Invocation is removed rather than ended, so it records no terminal
    /// status and emits no `invocation.ended` webhook — which is why a caller
    /// that bills or reconciles on settlement must cancel first and wait for
    /// the final state.
    ///
    /// `force` erases anyway, over a live turn. It is for erasing on an end
    /// user's behalf, where removing the transcript now outranks keeping a
    /// settled record: a deletion request has to be honoured, and a refusal
    /// thrown into that path leaves it unhonoured.
    ///
    /// An unknown or out-of-scope Session is not found, so a retry after a lost
    /// response can treat that as already-done.
    ///
    /// This is not account erasure by itself: nvoken keeps no account
    /// tombstone, so a caller honouring a deletion request must stop admitting
    /// work for the tenant before paginating and deleting.
    pub async fn delete_session(
        &self,
        session_id: &str,
        options: DeleteSessionOptions,
    ) -> Result<(), NvokenError> {
        apis::sessions_api::delete_session(&self.configuration, session_id, Some(options.force))
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
        options: UpdateSessionOptions,
    ) -> Result<models::Session, NvokenError> {
        let mut request = self
            .configuration
            .client
            .patch(format!(
                "{}/v1/sessions/{}",
                self.configuration.base_path,
                apis::urlencode(session_id),
            ))
            .json(&serde_json::json!({ "metadata": options.metadata }));
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
            options.order.map(ListOrder::as_str),
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

    /// The Session this turn belongs to, resolving it if the handle lacks it.
    /// The stream is Session-scoped, so a handle built from a bare Invocation
    /// ID resolves its Session before the stream opens.
    pub async fn require_session_id(&self) -> Result<String, NvokenError> {
        if let Some(session_id) = &self.session_id {
            return Ok(session_id.clone());
        }
        Ok(self
            .client
            .get_invocation(&self.invocation_id)
            .await?
            .session_id)
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
            .create_nudge(&self.invocation_id, content, None)
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
            .create_nudge(&self.invocation_id, content, Some(idempotency_key))
            .await
    }

    pub async fn list_nudges(
        &self,
        status: Option<models::NudgeStatus>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::NudgeList, NvokenError> {
        self.client
            .list_nudges(&self.invocation_id, status, cursor, limit)
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

    pub async fn cancel_nudge(&self, nudge_id: &str) -> Result<models::Nudge, NvokenError> {
        self.client
            .cancel_nudge(&self.invocation_id, nudge_id)
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

/// Whether an Invocation has stopped for good. One encoding, in
/// [`crate::invocation_status`]; `Incomplete` being one of the four is the part
/// callers get wrong, and a wait helper that treated only `Completed` as an
/// ending would poll a budget-stopped turn forever.
fn terminal(status: models::InvocationStatus) -> bool {
    crate::invocation_status::is_terminal_status(status)
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

    pub(crate) fn conflict(code: &str, message: impl Into<String>) -> Self {
        let mut error = Self::new(ErrorCategory::Conflict, message);
        error.status = Some(409);
        error.code = Some(code.to_string());
        error
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

    // A scope is worth nothing if it is only remembered locally, so this pins
    // the headers that actually leave the process, and that the client a scoped
    // one was derived from keeps making unscoped requests.
    #[test]
    fn a_scope_stamps_headers_and_leaves_its_parent_alone() {
        let client = Client::new("https://runtime.example.test", "key").unwrap();
        assert!(client.scope().is_none());
        assert!(scope_headers(None).unwrap().is_empty());

        let scoped = client
            .scoped(Scope::tenant("acme").user("user-7c1f"))
            .unwrap();
        let headers = scope_headers(scoped.scope()).unwrap();
        assert_eq!(headers["x-nvoken-tenant-key"], "acme");
        assert_eq!(headers["x-nvoken-user-key"], "user-7c1f");
        assert!(client.scope().is_none());

        let tenant_only =
            Client::with_scope("https://runtime.example.test", "key", Scope::tenant("acme"))
                .unwrap();
        let headers = scope_headers(tenant_only.scope()).unwrap();
        assert_eq!(headers["x-nvoken-tenant-key"], "acme");
        assert!(!headers.contains_key("x-nvoken-user-key"));

        // An empty scope would stamp nothing while reading as a narrowing,
        // which is the one failure mode a scope cannot have.
        assert!(client.scoped(Scope::default()).is_err());
        assert!(client.scoped(Scope::tenant("   ")).is_err());
    }
}
