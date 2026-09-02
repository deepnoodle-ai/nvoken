//! Idiomatic high-level Agent, Conversation, and Turn facade.

use std::{
    collections::{HashMap, HashSet},
    future::Future,
    ops::Deref,
    pin::Pin,
    sync::{Arc, Mutex as StdMutex},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use serde_json::Value;
use tokio::sync::Mutex;

use crate::{apis, models};

#[derive(Debug, thiserror::Error)]
pub enum NvokenError {
    #[error("nvoken request failed: {0}")]
    Api(String),
    #[error("Agent {0:?} was not found")]
    NotFound(String),
    #[error("this Turn completed without text output")]
    NoOutputText,
    #[error(transparent)]
    TurnExecution(#[from] TurnExecutionError),
    #[error(transparent)]
    TurnAdmission(#[from] TurnAdmissionError),
    #[error(transparent)]
    TurnTimeout(#[from] TurnTimeoutError),
    #[error("invalid SDK input: {0}")]
    InvalidInput(String),
}

fn api_error(error: impl std::fmt::Display) -> NvokenError {
    NvokenError::Api(error.to_string())
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct OwnedBy {
    pub tenant: String,
    pub user: Option<String>,
}

impl OwnedBy {
    pub fn tenant(tenant: impl Into<String>) -> Self {
        Self {
            tenant: tenant.into(),
            user: None,
        }
    }

    pub fn user(tenant: impl Into<String>, user: impl Into<String>) -> Self {
        Self {
            tenant: tenant.into(),
            user: Some(user.into()),
        }
    }
}

#[derive(Clone, Debug, PartialEq)]
pub struct Behavior(models::BehaviorInput);

pub trait IntoModelInput {
    fn into_model_input(self) -> models::ModelInput;
}

impl IntoModelInput for models::ModelInput {
    fn into_model_input(self) -> models::ModelInput {
        self
    }
}

impl IntoModelInput for models::Model {
    fn into_model_input(self) -> models::ModelInput {
        models::ModelInput::Model(Box::new(self))
    }
}

impl IntoModelInput for String {
    fn into_model_input(self) -> models::ModelInput {
        models::ModelInput::String(self)
    }
}

impl IntoModelInput for &str {
    fn into_model_input(self) -> models::ModelInput {
        models::ModelInput::String(self.into())
    }
}

impl Behavior {
    pub fn new(instructions: impl Into<String>, model: impl IntoModelInput) -> Self {
        Self(models::BehaviorInput::new(
            instructions.into(),
            model.into_model_input(),
        ))
    }

    pub fn limits(mut self, limits: models::Limits) -> Self {
        self.0.limits = Some(Box::new(limits));
        self
    }

    pub fn instructions(&self) -> &str {
        &self.0.instructions
    }

    pub fn model(&self) -> &models::ModelInput {
        &self.0.model
    }

    pub fn output_schema(mut self, schema: HashMap<String, Value>) -> Self {
        self.0.output_schema = Some(schema);
        self
    }

    pub fn memory(mut self, memory: models::DefaultMemoryPolicy) -> Self {
        self.0.memory = Some(Box::new(memory));
        self
    }

    pub fn tool(mut self, declaration: models::ToolDeclaration) -> Self {
        self.0.tools.get_or_insert_with(Vec::new).push(declaration);
        self
    }

    pub fn tools(&self) -> &[models::ToolDeclaration] {
        self.0.tools.as_deref().unwrap_or_default()
    }

    pub fn host_tool(
        mut self,
        name: impl Into<String>,
        description: impl Into<String>,
        input_schema: Value,
    ) -> Result<Self, NvokenError> {
        let schema = input_schema.as_object().cloned().ok_or_else(|| {
            NvokenError::InvalidInput("a host tool input schema must be an object".into())
        })?;
        let declaration =
            models::ToolDeclaration::Host(Box::new(models::HostToolDeclaration::new(
                name.into(),
                description.into(),
                models::host_tool_declaration::Mode::ModeHost,
                schema.into_iter().collect(),
            )));
        self.0.tools.get_or_insert_with(Vec::new).push(declaration);
        Ok(self)
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Memory {
    None,
    Tenant { namespace: Option<String> },
    User { namespace: Option<String> },
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum InlineMemory {
    None,
    Tenant { namespace: String },
    User { namespace: String },
}

impl InlineMemory {
    pub fn tenant(namespace: impl Into<String>) -> Result<Self, NvokenError> {
        let namespace = namespace.into();
        if namespace.is_empty() {
            return Err(NvokenError::InvalidInput(
                "inline tenant Memory requires an explicit namespace".into(),
            ));
        }
        Ok(Self::Tenant { namespace })
    }

    pub fn user(namespace: impl Into<String>) -> Result<Self, NvokenError> {
        let namespace = namespace.into();
        if namespace.is_empty() {
            return Err(NvokenError::InvalidInput(
                "inline user Memory requires an explicit namespace".into(),
            ));
        }
        Ok(Self::User { namespace })
    }

    fn into_stored_selection(self) -> Memory {
        match self {
            Self::None => Memory::None,
            Self::Tenant { namespace } => Memory::Tenant {
                namespace: Some(namespace),
            },
            Self::User { namespace } => Memory::User {
                namespace: Some(namespace),
            },
        }
    }
}

impl Memory {
    pub fn tenant(namespace: impl Into<String>) -> Self {
        Self::Tenant {
            namespace: Some(namespace.into()),
        }
    }

    pub fn user(namespace: impl Into<String>) -> Self {
        Self::User {
            namespace: Some(namespace.into()),
        }
    }

    fn selection(&self) -> models::TurnMemorySelection {
        match self {
            Self::None => models::TurnMemorySelection::None(Box::new(models::NoTurnMemory::new(
                models::no_turn_memory::Scope::None,
            ))),
            Self::Tenant { namespace } => {
                let mut memory =
                    models::TenantTurnMemory::new(models::tenant_turn_memory::Scope::Tenant);
                memory.namespace = namespace.clone();
                models::TurnMemorySelection::Tenant(Box::new(memory))
            }
            Self::User { namespace } => {
                let mut memory = models::UserTurnMemory::new(models::user_turn_memory::Scope::User);
                memory.namespace = namespace.clone();
                models::TurnMemorySelection::User(Box::new(memory))
            }
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum ConversationOwner {
    Tenant,
    User,
}

#[derive(Clone, Debug, PartialEq)]
pub enum ConversationRef {
    Id {
        id: String,
    },
    Key {
        key: String,
        owner: ConversationOwner,
        retention: Option<models::RetentionPolicy>,
        compaction: Option<models::CompactionPolicy>,
        metadata: Option<HashMap<String, Value>>,
    },
}

impl ConversationRef {
    pub fn by_id(id: impl Into<String>) -> Self {
        Self::Id { id: id.into() }
    }

    pub fn by_key(key: impl Into<String>, owner: ConversationOwner) -> Self {
        Self::Key {
            key: key.into(),
            owner,
            retention: None,
            compaction: None,
            metadata: None,
        }
    }

    pub fn retention(mut self, retention: models::RetentionPolicy) -> Self {
        if let Self::Key {
            retention: value, ..
        } = &mut self
        {
            *value = Some(retention);
        }
        self
    }

    pub fn compaction(mut self, compaction: models::CompactionPolicy) -> Self {
        if let Self::Key {
            compaction: value, ..
        } = &mut self
        {
            *value = Some(compaction);
        }
        self
    }

    pub fn metadata(mut self, metadata: HashMap<String, Value>) -> Self {
        if let Self::Key {
            metadata: value, ..
        } = &mut self
        {
            *value = Some(metadata);
        }
        self
    }

    fn selection(&self, user: Option<&str>) -> Result<models::TurnConversation, NvokenError> {
        Ok(match self {
            Self::Id { id } => {
                let value = models::ContinueTurnConversation::new(
                    models::continue_turn_conversation::Mode::Continue,
                    id.clone(),
                );
                models::TurnConversation::Continue(Box::new(value))
            }
            Self::Key {
                key,
                owner,
                retention,
                compaction,
                metadata,
            } => {
                let owner = match owner {
                    ConversationOwner::Tenant => models::ConversationOwner::Tenant(Box::new(
                        models::TenantConversationOwner::new(
                            models::tenant_conversation_owner::Kind::Tenant,
                        ),
                    )),
                    ConversationOwner::User => models::ConversationOwner::User(Box::new(
                        models::UserConversationOwner::new(
                            models::user_conversation_owner::Kind::User,
                            user.ok_or_else(|| {
                                NvokenError::InvalidInput(
                                    "a user-owned Conversation requires Turn user".into(),
                                )
                            })?
                            .into(),
                        ),
                    )),
                };
                let mut value = models::ContinueOrCreateTurnConversation::new(
                    models::continue_or_create_turn_conversation::Mode::ContinueOrCreate,
                    key.clone(),
                    owner,
                );
                value.retention = retention.clone().map(Box::new);
                value.compaction = compaction.clone().map(Box::new);
                value.metadata = metadata.clone();
                models::TurnConversation::ContinueOrCreate(Box::new(value))
            }
        })
    }

    fn lock_key(&self, tenant: &str, user: Option<&str>) -> String {
        match self {
            Self::Id { id } => id.clone(),
            Self::Key {
                key,
                owner: ConversationOwner::Tenant,
                ..
            } => format!("{tenant}:tenant:{key}"),
            Self::Key {
                key,
                owner: ConversationOwner::User,
                ..
            } => format!("{tenant}:user:{user:?}:{key}"),
        }
    }
}

pub trait IntoTurnInput {
    fn into_turn_input(self) -> models::TurnInput;
}

impl IntoTurnInput for models::TurnInput {
    fn into_turn_input(self) -> models::TurnInput {
        self
    }
}

impl IntoTurnInput for String {
    fn into_turn_input(self) -> models::TurnInput {
        models::TurnInput::String(self)
    }
}

impl IntoTurnInput for &str {
    fn into_turn_input(self) -> models::TurnInput {
        models::TurnInput::String(self.into())
    }
}

pub type ToolFuture = Pin<Box<dyn Future<Output = Result<Value, String>> + Send + 'static>>;
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ToolContext {
    pub turn_id: String,
    pub tool_call_id: String,
}

struct ToolAttemptGuard {
    handled: Arc<StdMutex<HashSet<String>>>,
    call_ids: Vec<String>,
    committed: bool,
}

impl Drop for ToolAttemptGuard {
    fn drop(&mut self) {
        if self.committed {
            return;
        }
        let mut handled = self.handled.lock().expect("tool replay guard poisoned");
        for call_id in &self.call_ids {
            handled.remove(call_id);
        }
    }
}

pub type ToolHandler = Arc<dyn Fn(Value, ToolContext) -> ToolFuture + Send + Sync>;

#[derive(Clone)]
pub struct Tool {
    pub name: String,
    handler: ToolHandler,
}

impl std::fmt::Debug for Tool {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Tool")
            .field("name", &self.name)
            .finish_non_exhaustive()
    }
}

impl Tool {
    pub fn new<F, Fut>(name: impl Into<String>, handler: F) -> Self
    where
        F: Fn(Value, ToolContext) -> Fut + Send + Sync + 'static,
        Fut: Future<Output = Result<Value, String>> + Send + 'static,
    {
        Self {
            name: name.into(),
            handler: Arc::new(move |arguments, context| Box::pin(handler(arguments, context))),
        }
    }
}

#[derive(Clone, Debug)]
pub struct TurnOptions {
    pub tenant: String,
    pub user: Option<String>,
    pub memory: Option<Memory>,
    pub conversation: Option<ConversationRef>,
    pub limits: Option<models::Limits>,
    pub metadata: Option<HashMap<String, String>>,
    pub idempotency_key: Option<String>,
    pub timeout: Option<Duration>,
}

impl TurnOptions {
    pub fn new(tenant: impl Into<String>) -> Self {
        Self {
            tenant: tenant.into(),
            user: None,
            memory: None,
            conversation: None,
            limits: None,
            metadata: None,
            idempotency_key: None,
            timeout: None,
        }
    }

    pub fn user(mut self, user: impl Into<String>) -> Self {
        self.user = Some(user.into());
        self
    }

    pub fn memory(mut self, memory: Memory) -> Self {
        self.memory = Some(memory);
        self
    }

    pub fn conversation(mut self, conversation: ConversationRef) -> Self {
        self.conversation = Some(conversation);
        self
    }

    pub fn limits(mut self, limits: models::Limits) -> Self {
        self.limits = Some(limits);
        self
    }

    pub fn idempotency_key(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }

    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }
}

#[derive(Clone, Debug)]
pub struct InlineTurnOptions {
    pub tenant: String,
    pub user: Option<String>,
    pub memory: Option<InlineMemory>,
    pub conversation: Option<ConversationRef>,
    pub limits: Option<models::Limits>,
    pub metadata: Option<HashMap<String, String>>,
    pub idempotency_key: Option<String>,
    pub timeout: Option<Duration>,
}

impl InlineTurnOptions {
    pub fn new(tenant: impl Into<String>) -> Self {
        Self {
            tenant: tenant.into(),
            user: None,
            memory: None,
            conversation: None,
            limits: None,
            metadata: None,
            idempotency_key: None,
            timeout: None,
        }
    }

    pub fn user(mut self, user: impl Into<String>) -> Self {
        self.user = Some(user.into());
        self
    }

    pub fn memory(mut self, memory: InlineMemory) -> Self {
        self.memory = Some(memory);
        self
    }

    pub fn conversation(mut self, conversation: ConversationRef) -> Self {
        self.conversation = Some(conversation);
        self
    }

    pub fn limits(mut self, limits: models::Limits) -> Self {
        self.limits = Some(limits);
        self
    }

    pub fn idempotency_key(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }

    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }
}

impl From<InlineTurnOptions> for TurnOptions {
    fn from(options: InlineTurnOptions) -> Self {
        Self {
            tenant: options.tenant,
            user: options.user,
            memory: options.memory.map(InlineMemory::into_stored_selection),
            conversation: options.conversation,
            limits: options.limits,
            metadata: options.metadata,
            idempotency_key: options.idempotency_key,
            timeout: options.timeout,
        }
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TurnAdmission {
    pub idempotency_key: String,
    pub deduplicated: bool,
}

#[derive(Clone)]
pub struct TurnSnapshot {
    pub status: models::TurnStatus,
    pub messages: Vec<models::ConversationMessage>,
    pub text: Option<String>,
    pub structured_output: Option<HashMap<String, Value>>,
    pub behavior_source: BehaviorSourceKind,
    pub agent_id: Option<String>,
    pub agent_revision_id: Option<String>,
    pub memory_space_id: Option<String>,
    pub conversation_id: Option<String>,
    pub content_expires_at: Option<chrono::DateTime<chrono::FixedOffset>>,
}

#[derive(Clone)]
pub struct TurnResult {
    pub status: models::TurnStatus,
    pub messages: Vec<models::ConversationMessage>,
    pub text: Option<String>,
    pub structured_output: Option<HashMap<String, Value>>,
    pub behavior_source: BehaviorSourceKind,
    pub agent_id: Option<String>,
    pub agent_revision_id: Option<String>,
    pub memory_space_id: Option<String>,
    pub conversation_id: Option<String>,
    pub content_expires_at: Option<chrono::DateTime<chrono::FixedOffset>>,
    pub turn: Turn,
    pub admission: Option<TurnAdmission>,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum BehaviorSourceKind {
    AgentRevision,
    Inline,
}

#[derive(Clone)]
pub struct TurnUpdate {
    pub snapshot: TurnSnapshot,
    pub frame: Option<crate::stream::StreamEvent>,
    pub cursor: Option<String>,
}

#[derive(Clone)]
pub struct TurnExecutionError {
    pub result: TurnResult,
}

impl std::fmt::Debug for TurnExecutionError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("TurnExecutionError")
            .field("turn_id", &self.result.turn.id)
            .field("status", &self.result.status)
            .finish()
    }
}

impl std::fmt::Display for TurnExecutionError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            formatter,
            "Turn {} ended {}",
            self.result.turn.id, self.result.status
        )
    }
}

impl std::error::Error for TurnExecutionError {}

#[derive(Clone, Debug)]
pub struct TurnAdmissionError {
    pub idempotency_key: String,
    pub message: String,
}

impl std::fmt::Display for TurnAdmissionError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            formatter,
            "Turn admission outcome is uncertain (retry with {}): {}",
            self.idempotency_key, self.message
        )
    }
}

impl std::error::Error for TurnAdmissionError {}

#[derive(Clone)]
pub struct TurnTimeoutError {
    pub turn: Option<Turn>,
    pub idempotency_key: Option<String>,
}

impl std::fmt::Debug for TurnTimeoutError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("TurnTimeoutError")
            .field("turn_id", &self.turn.as_ref().map(|turn| &turn.id))
            .field("idempotency_key", &self.idempotency_key)
            .finish()
    }
}

impl std::fmt::Display for TurnTimeoutError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match &self.turn {
            Some(turn) => write!(formatter, "timed out waiting for durable Turn {}", turn.id),
            None => write!(formatter, "timed out while admitting a durable Turn"),
        }
    }
}

impl std::error::Error for TurnTimeoutError {}

#[derive(Clone, Debug, Default)]
pub struct ConversationTurnOptions {
    pub limits: Option<models::Limits>,
    pub metadata: Option<HashMap<String, String>>,
    pub idempotency_key: Option<String>,
    pub timeout: Option<Duration>,
}

#[derive(Clone, Debug)]
pub struct ConversationContext {
    pub tenant: String,
    pub user: Option<String>,
    pub memory: Option<Memory>,
    pub limits: Option<models::Limits>,
}

#[derive(Clone, Debug)]
pub struct InlineConversationContext {
    pub tenant: String,
    pub user: Option<String>,
    pub memory: Option<InlineMemory>,
    pub limits: Option<models::Limits>,
}

impl InlineConversationContext {
    pub fn new(tenant: impl Into<String>) -> Self {
        Self {
            tenant: tenant.into(),
            user: None,
            memory: None,
            limits: None,
        }
    }

    pub fn user(mut self, user: impl Into<String>) -> Self {
        self.user = Some(user.into());
        self
    }

    pub fn memory(mut self, memory: InlineMemory) -> Self {
        self.memory = Some(memory);
        self
    }

    pub fn limits(mut self, limits: models::Limits) -> Self {
        self.limits = Some(limits);
        self
    }
}

impl ConversationContext {
    pub fn new(tenant: impl Into<String>) -> Self {
        Self {
            tenant: tenant.into(),
            user: None,
            memory: None,
            limits: None,
        }
    }

    pub fn user(mut self, user: impl Into<String>) -> Self {
        self.user = Some(user.into());
        self
    }

    pub fn memory(mut self, memory: Memory) -> Self {
        self.memory = Some(memory);
        self
    }

    pub fn limits(mut self, limits: models::Limits) -> Self {
        self.limits = Some(limits);
        self
    }
}

impl ConversationTurnOptions {
    pub fn limits(mut self, limits: models::Limits) -> Self {
        self.limits = Some(limits);
        self
    }

    pub fn idempotency_key(mut self, key: impl Into<String>) -> Self {
        self.idempotency_key = Some(key.into());
        self
    }

    pub fn timeout(mut self, timeout: Duration) -> Self {
        self.timeout = Some(timeout);
        self
    }
}

#[derive(Clone)]
pub struct Client {
    inner: Arc<ClientInner>,
}

struct ClientInner {
    raw: RawClient,
    conversation_locks: Mutex<HashMap<String, Arc<Mutex<()>>>>,
}

/// Explicit door to the exact generated OpenAPI transport.
///
/// Pass [`RawClient::configuration`] to functions in [`crate::apis`] when the
/// high-level facade intentionally omits a wire-level control.
pub struct RawClient {
    configuration: apis::configuration::Configuration,
}

impl RawClient {
    pub fn configuration(&self) -> &apis::configuration::Configuration {
        &self.configuration
    }
}

impl Deref for RawClient {
    type Target = apis::configuration::Configuration;

    fn deref(&self) -> &Self::Target {
        &self.configuration
    }
}

impl Client {
    pub fn new(api_key: impl Into<String>) -> Self {
        Self::with_base_url(api_key, "https://api.nvoken.com")
    }

    pub fn with_base_url(api_key: impl Into<String>, base_url: impl Into<String>) -> Self {
        let mut configuration = apis::configuration::Configuration::new();
        configuration.base_path = base_url.into().trim_end_matches('/').to_string();
        configuration.bearer_access_token = Some(api_key.into());
        Self {
            inner: Arc::new(ClientInner {
                raw: RawClient { configuration },
                conversation_locks: Mutex::new(HashMap::new()),
            }),
        }
    }

    /// The exact generated OpenAPI transport door.
    pub fn raw(&self) -> &RawClient {
        &self.inner.raw
    }

    pub fn agents(&self) -> AgentCollection<'_> {
        AgentCollection { client: self }
    }

    pub async fn agent(&self, key: &str, owned_by: Option<&OwnedBy>) -> Result<Agent, NvokenError> {
        let (owner_kind, tenant, user) = owner_coordinates(owned_by);
        let page = apis::agents_api::list_agents(
            self.raw(),
            owner_kind,
            tenant,
            user,
            Some(key),
            None,
            None,
            Some(1),
        )
        .await
        .map_err(api_error)?;
        page.items
            .into_iter()
            .next()
            .map(|resource| Agent::new(self.clone(), resource))
            .ok_or_else(|| NvokenError::NotFound(key.into()))
    }

    pub fn inline(&self, behavior: Behavior) -> InlineAgent {
        InlineAgent::new(self.clone(), behavior)
    }

    pub fn turn(
        &self,
        id: impl Into<String>,
        tenant: impl Into<String>,
        user: Option<String>,
    ) -> Turn {
        Turn::new(self.clone(), id.into(), tenant.into(), user, HashMap::new())
    }

    pub async fn issue_anonymous_token(
        &self,
        app_id: &str,
        origin: &str,
        idempotency_key: &str,
        visitor_token: Option<String>,
    ) -> Result<models::AnonymousTokenResponse, NvokenError> {
        let mut request = models::AnonymousTokenRequest::new();
        request.visitor_token = visitor_token;
        apis::apps_api::issue_anonymous_token(self.raw(), app_id, origin, idempotency_key, request)
            .await
            .map_err(api_error)
    }

    pub async fn list_credit_accounts(
        &self,
        tenant_key: Option<&str>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::CreditAccountList, NvokenError> {
        apis::credits_api::list_credit_accounts(self.raw(), tenant_key, cursor, limit)
            .await
            .map_err(api_error)
    }

    pub async fn list_credit_allocations(
        &self,
        tenant_key: Option<&str>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::CreditAllocationList, NvokenError> {
        apis::credits_api::list_credit_allocations(self.raw(), tenant_key, cursor, limit)
            .await
            .map_err(api_error)
    }

    pub async fn list_mcp_tools(
        &self,
        server: models::McpServer,
        headers: Option<HashMap<String, String>>,
    ) -> Result<models::McpListToolsResponse, NvokenError> {
        let mut request = models::McpListToolsRequest::new(server);
        request.headers = headers;
        apis::mcp_api::list_mcp_tools(self.raw(), request)
            .await
            .map_err(api_error)
    }

    pub async fn list_models(
        &self,
        provider: Option<&str>,
        include_deprecated: Option<bool>,
        if_none_match: Option<&str>,
    ) -> Result<models::ModelList, NvokenError> {
        apis::models_api::list_models(self.raw(), provider, include_deprecated, if_none_match)
            .await
            .map_err(api_error)
    }

    pub async fn list_provider_keys(
        &self,
        provider: Option<&str>,
        scope: Option<models::ProviderKeyScope>,
        status: Option<&str>,
        tenant_key: Option<&str>,
        cursor: Option<&str>,
        limit: Option<u32>,
    ) -> Result<models::ProviderKeyList, NvokenError> {
        apis::provider_keys_api::list_provider_keys(
            self.raw(),
            provider,
            scope,
            status,
            tenant_key,
            cursor,
            limit,
        )
        .await
        .map_err(api_error)
    }

    async fn conversation_lock(&self, key: String) -> Arc<Mutex<()>> {
        let mut locks = self.inner.conversation_locks.lock().await;
        locks
            .entry(key)
            .or_insert_with(|| Arc::new(Mutex::new(())))
            .clone()
    }
}

fn owner_coordinates(
    owned_by: Option<&OwnedBy>,
) -> (models::AgentOwnerKind, Option<&str>, Option<&str>) {
    match owned_by {
        None => (models::AgentOwnerKind::App, None, None),
        Some(owner) if owner.user.is_none() => (
            models::AgentOwnerKind::Tenant,
            Some(owner.tenant.as_str()),
            None,
        ),
        Some(owner) => (
            models::AgentOwnerKind::User,
            Some(owner.tenant.as_str()),
            owner.user.as_deref(),
        ),
    }
}

pub struct AgentCollection<'a> {
    client: &'a Client,
}

pub struct AgentPage {
    pub items: Vec<Agent>,
    pub has_more: bool,
    pub next_cursor: Option<String>,
}

impl AgentCollection<'_> {
    pub async fn get_by_id(&self, id: &str) -> Result<Agent, NvokenError> {
        let resource = apis::agents_api::get_agent(self.client.raw(), id)
            .await
            .map_err(api_error)?;
        Ok(Agent::new(self.client.clone(), resource))
    }

    pub async fn create(
        &self,
        key: &str,
        name: Option<&str>,
        behavior: Behavior,
        owned_by: Option<&OwnedBy>,
        idempotency_key: Option<&str>,
    ) -> Result<Agent, NvokenError> {
        let owner = match owned_by {
            None => models::AgentOwner::App(Box::new(models::AppAgentOwner::new(
                models::app_agent_owner::Kind::App,
            ))),
            Some(owner) if owner.user.is_none() => {
                models::AgentOwner::Tenant(Box::new(models::TenantAgentOwner::new(
                    models::tenant_agent_owner::Kind::Tenant,
                    owner.tenant.clone(),
                )))
            }
            Some(owner) => models::AgentOwner::User(Box::new(models::UserAgentOwner::new(
                models::user_agent_owner::Kind::User,
                owner.tenant.clone(),
                owner.user.clone().expect("matched a user owner"),
            ))),
        };
        let behavior = behavior.0;
        let mut request = models::CreateAgentRequest::new(
            key.into(),
            owner,
            behavior.instructions,
            *behavior.model,
        );
        request.name = Some(name.unwrap_or(key).into());
        request.limits = behavior.limits;
        request.output_schema = behavior.output_schema;
        request.tools = behavior.tools;
        request.memory = behavior.memory;
        let idempotency_key = idempotency_key.map(str::to_owned).unwrap_or_else(new_key);
        let resource = apis::agents_api::create_agent(self.client.raw(), &idempotency_key, request)
            .await
            .map_err(api_error)?;
        Ok(Agent::new(self.client.clone(), resource))
    }

    pub async fn list(
        &self,
        owned_by: Option<&OwnedBy>,
        include_archived: bool,
        cursor: Option<&str>,
    ) -> Result<AgentPage, NvokenError> {
        let (owner_kind, tenant, user) = owner_coordinates(owned_by);
        let page = apis::agents_api::list_agents(
            self.client.raw(),
            owner_kind,
            tenant,
            user,
            None,
            Some(include_archived),
            cursor,
            None,
        )
        .await
        .map_err(api_error)?;
        Ok(AgentPage {
            items: page
                .items
                .into_iter()
                .map(|resource| Agent::new(self.client.clone(), resource))
                .collect(),
            has_more: page.has_more,
            next_cursor: page.next_cursor,
        })
    }
}

#[derive(Clone)]
struct Runner {
    client: Client,
    behavior: BehaviorSource,
    tools: HashMap<String, Tool>,
}

#[derive(Clone)]
enum BehaviorSource {
    Agent(String),
    Inline(Behavior),
}

impl BehaviorSource {
    fn selection(&self) -> models::TurnBehaviorSelection {
        match self {
            Self::Agent(agent_id) => {
                let selector = models::AgentSelector::AgentSelectorById(Box::new(
                    models::AgentSelectorById::new(
                        agent_id.clone(),
                        models::AgentRevisionSelector::String("current".into()),
                    ),
                ));
                models::TurnBehaviorSelection::Agent(Box::new(models::AgentTurnBehavior::new(
                    models::agent_turn_behavior::Kind::Agent,
                    selector,
                )))
            }
            Self::Inline(behavior) => {
                models::TurnBehaviorSelection::Inline(Box::new(models::InlineTurnBehavior::new(
                    models::inline_turn_behavior::Kind::Inline,
                    behavior.0.clone(),
                )))
            }
        }
    }
}

impl Runner {
    fn bind_tools(&self, tools: impl IntoIterator<Item = Tool>) -> Result<Self, NvokenError> {
        let mut next = self.clone();
        for tool in tools {
            if let BehaviorSource::Inline(behavior) = &self.behavior {
                let declared = behavior.tools().iter().any(|declaration| {
                    matches!(declaration, models::ToolDeclaration::Host(host) if host.name == tool.name)
                });
                if !declared {
                    return Err(NvokenError::InvalidInput(format!(
                        "tool handler {:?} is not declared by the inline behavior",
                        tool.name
                    )));
                }
            }
            next.tools.insert(tool.name.clone(), tool);
        }
        Ok(next)
    }

    fn request(
        &self,
        input: models::TurnInput,
        options: &TurnOptions,
    ) -> Result<models::CreateTurnRequest, NvokenError> {
        if matches!(options.memory, Some(Memory::User { .. })) && options.user.is_none() {
            return Err(NvokenError::InvalidInput(
                "User Memory requires a Turn user".into(),
            ));
        }
        if matches!(self.behavior, BehaviorSource::Inline(_)) {
            let missing_namespace = match options.memory.as_ref() {
                Some(Memory::Tenant { namespace }) | Some(Memory::User { namespace }) => {
                    namespace.as_deref().map_or(true, str::is_empty)
                }
                _ => false,
            };
            if missing_namespace {
                return Err(NvokenError::InvalidInput(
                    "inline tenant and user Memory require an explicit namespace".into(),
                ));
            }
        }
        let mut request = models::CreateTurnRequest::new(
            options.idempotency_key.clone().unwrap_or_else(new_key),
            input,
        );
        request.tenant_key = Some(options.tenant.clone());
        request.user_key = options.user.clone();
        request.behavior = Some(Box::new(self.behavior.selection()));
        request.memory = options
            .memory
            .as_ref()
            .map(|memory| Box::new(memory.selection()));
        request.conversation = options
            .conversation
            .as_ref()
            .map(|conversation| {
                conversation
                    .selection(options.user.as_deref())
                    .map(Box::new)
            })
            .transpose()?;
        request.limits = options.limits.clone().map(Box::new);
        request.metadata = options.metadata.clone();
        Ok(request)
    }

    async fn start(
        &self,
        input: models::TurnInput,
        mut options: TurnOptions,
    ) -> Result<Turn, NvokenError> {
        let timeout = options.timeout;
        let admission_key = options.idempotency_key.clone().unwrap_or_else(new_key);
        options.idempotency_key = Some(admission_key.clone());
        let admission = apis::turns_api::create_turn(
            self.client.raw(),
            self.request(input, &options)?,
            None,
            None,
            None,
            None,
        );
        let admitted = match timeout {
            Some(timeout) => tokio::time::timeout(timeout, admission)
                .await
                .map_err(|_| TurnTimeoutError {
                    turn: None,
                    idempotency_key: Some(admission_key.clone()),
                })?,
            None => admission.await,
        };
        let resource = admitted.map_err(|error| {
            if matches!(&error, apis::Error::ResponseError(_)) {
                api_error(error)
            } else {
                TurnAdmissionError {
                    idempotency_key: admission_key.clone(),
                    message: error.to_string(),
                }
                .into()
            }
        })?;
        let deduplicated = resource.deduplicated.unwrap_or(false);
        Ok(Turn {
            client: self.client.clone(),
            id: resource.id.clone(),
            tenant: options.tenant,
            user: options.user,
            resource: Some(resource),
            tools: self.tools.clone(),
            handled_tool_call_ids: Arc::new(StdMutex::new(HashSet::new())),
            admission: Some(TurnAdmission {
                idempotency_key: admission_key,
                deduplicated,
            }),
        })
    }

    async fn run(
        &self,
        input: models::TurnInput,
        options: TurnOptions,
    ) -> Result<TurnResult, NvokenError> {
        let timeout = options.timeout;
        let started_at = Instant::now();
        let turn = self.start(input, options).await?;
        let remaining = timeout.map(|timeout| timeout.saturating_sub(started_at.elapsed()));
        turn.result(remaining).await
    }

    async fn text(
        &self,
        input: models::TurnInput,
        options: TurnOptions,
    ) -> Result<String, NvokenError> {
        self.run(input, options)
            .await?
            .text
            .ok_or(NvokenError::NoOutputText)
    }
}

fn merge_narrow_limits(
    bound: Option<models::Limits>,
    requested: Option<models::Limits>,
) -> Result<Option<models::Limits>, NvokenError> {
    let Some(requested) = requested else {
        return Ok(bound);
    };
    let Some(mut merged) = bound else {
        return Ok(Some(requested));
    };

    macro_rules! narrow {
        ($field:ident) => {
            if let Some(value) = requested.$field {
                if merged.$field.is_some_and(|ceiling| value > ceiling) {
                    return Err(NvokenError::InvalidInput(format!(
                        "Turn limit {:?} may only narrow its bound value",
                        stringify!($field)
                    )));
                }
                merged.$field = Some(value);
            }
        };
    }

    narrow!(total_timeout_seconds);
    narrow!(active_timeout_seconds);
    narrow!(waiting_timeout_seconds);
    narrow!(max_output_tokens);
    narrow!(max_estimated_cost_usd);
    narrow!(max_iterations);
    Ok(Some(merged))
}

fn new_key() -> String {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_nanos();
    format!("sdk-{nanos:x}-{:x}", fastrand::u64(..))
}

#[derive(Clone)]
pub struct Agent {
    client: Client,
    pub resource: models::Agent,
    runner: Runner,
}

impl Agent {
    fn new(client: Client, resource: models::Agent) -> Self {
        let behavior = BehaviorSource::Agent(resource.id.clone());
        Self {
            client: client.clone(),
            resource,
            runner: Runner {
                client,
                behavior,
                tools: HashMap::new(),
            },
        }
    }

    pub fn id(&self) -> &str {
        &self.resource.id
    }

    pub fn key(&self) -> &str {
        &self.resource.agent_key
    }

    pub fn owner(&self) -> &models::AgentOwner {
        &self.resource.owner
    }

    pub fn current_revision(&self) -> u64 {
        self.resource.current_revision
    }

    fn with_resource(&self, resource: models::Agent) -> Self {
        Self {
            client: self.client.clone(),
            resource,
            runner: self.runner.clone(),
        }
    }

    pub fn bind_tools(&self, tools: impl IntoIterator<Item = Tool>) -> Result<Self, NvokenError> {
        Ok(Self {
            client: self.client.clone(),
            resource: self.resource.clone(),
            runner: self.runner.bind_tools(tools)?,
        })
    }

    pub fn conversation(
        &self,
        reference: ConversationRef,
        context: ConversationContext,
    ) -> Conversation {
        Conversation {
            runner: self.runner.clone(),
            reference,
            tenant: context.tenant,
            user: context.user,
            memory: context.memory,
            limits: context.limits,
        }
    }

    pub async fn start(
        &self,
        input: impl IntoTurnInput,
        options: TurnOptions,
    ) -> Result<Turn, NvokenError> {
        self.runner.start(input.into_turn_input(), options).await
    }

    pub async fn run(
        &self,
        input: impl IntoTurnInput,
        options: TurnOptions,
    ) -> Result<TurnResult, NvokenError> {
        self.runner.run(input.into_turn_input(), options).await
    }

    pub async fn text(
        &self,
        input: impl IntoTurnInput,
        options: TurnOptions,
    ) -> Result<String, NvokenError> {
        self.runner.text(input.into_turn_input(), options).await
    }

    pub async fn publish(
        &self,
        behavior: Behavior,
        idempotency_key: Option<&str>,
    ) -> Result<models::AgentRevision, NvokenError> {
        let idempotency_key = idempotency_key.map(str::to_owned).unwrap_or_else(new_key);
        apis::agents_api::publish_agent_revision(
            self.client.raw(),
            &idempotency_key,
            &self.resource.id,
            behavior.0,
        )
        .await
        .map_err(api_error)
    }

    pub async fn archive(&self) -> Result<Self, NvokenError> {
        let resource = apis::agents_api::archive_agent(self.client.raw(), &self.resource.id)
            .await
            .map_err(api_error)?;
        Ok(self.with_resource(resource))
    }

    pub async fn restore(&self) -> Result<Self, NvokenError> {
        let resource = apis::agents_api::restore_agent(self.client.raw(), &self.resource.id)
            .await
            .map_err(api_error)?;
        Ok(self.with_resource(resource))
    }
}

#[derive(Clone)]
pub struct InlineAgent {
    pub behavior: Behavior,
    runner: Runner,
}

impl InlineAgent {
    fn new(client: Client, behavior: Behavior) -> Self {
        Self {
            behavior: behavior.clone(),
            runner: Runner {
                client,
                behavior: BehaviorSource::Inline(behavior),
                tools: HashMap::new(),
            },
        }
    }

    pub fn bind_tools(&self, tools: impl IntoIterator<Item = Tool>) -> Result<Self, NvokenError> {
        let runner = self.runner.bind_tools(tools)?;
        Ok(Self {
            behavior: self.behavior.clone(),
            runner,
        })
    }

    pub fn conversation(
        &self,
        reference: ConversationRef,
        context: InlineConversationContext,
    ) -> Conversation {
        Conversation {
            runner: self.runner.clone(),
            reference,
            tenant: context.tenant,
            user: context.user,
            memory: context.memory.map(InlineMemory::into_stored_selection),
            limits: context.limits,
        }
    }

    pub async fn start(
        &self,
        input: impl IntoTurnInput,
        options: InlineTurnOptions,
    ) -> Result<Turn, NvokenError> {
        self.runner
            .start(input.into_turn_input(), options.into())
            .await
    }

    pub async fn run(
        &self,
        input: impl IntoTurnInput,
        options: InlineTurnOptions,
    ) -> Result<TurnResult, NvokenError> {
        self.runner
            .run(input.into_turn_input(), options.into())
            .await
    }

    pub async fn text(
        &self,
        input: impl IntoTurnInput,
        options: InlineTurnOptions,
    ) -> Result<String, NvokenError> {
        self.runner
            .text(input.into_turn_input(), options.into())
            .await
    }
}

#[derive(Clone)]
pub struct Conversation {
    runner: Runner,
    reference: ConversationRef,
    tenant: String,
    user: Option<String>,
    memory: Option<Memory>,
    limits: Option<models::Limits>,
}

impl Conversation {
    async fn start_unlocked(
        &self,
        input: impl IntoTurnInput,
        options: ConversationTurnOptions,
    ) -> Result<Turn, NvokenError> {
        let mut turn_options = TurnOptions::new(&self.tenant)
            .conversation(self.reference.clone())
            .with_optional_user(self.user.clone());
        turn_options.memory = self.memory.clone();
        turn_options.limits = merge_narrow_limits(self.limits.clone(), options.limits)?;
        turn_options.metadata = options.metadata;
        turn_options.idempotency_key = options.idempotency_key;
        turn_options.timeout = options.timeout;
        self.runner
            .start(input.into_turn_input(), turn_options)
            .await
    }

    pub async fn start(
        &self,
        input: impl IntoTurnInput,
        options: ConversationTurnOptions,
    ) -> Result<Turn, NvokenError> {
        let lock = self
            .runner
            .client
            .conversation_lock(self.reference.lock_key(&self.tenant, self.user.as_deref()))
            .await;
        let _guard = lock.lock().await;
        self.start_unlocked(input, options).await
    }

    pub async fn run(
        &self,
        input: impl IntoTurnInput,
        mut options: ConversationTurnOptions,
    ) -> Result<TurnResult, NvokenError> {
        let timeout = options.timeout;
        let started_at = Instant::now();
        let lock = self
            .runner
            .client
            .conversation_lock(self.reference.lock_key(&self.tenant, self.user.as_deref()))
            .await;
        let _guard = lock.lock().await;
        options.timeout = timeout.map(|timeout| timeout.saturating_sub(started_at.elapsed()));
        self.start_unlocked(input, options)
            .await?
            .result(timeout.map(|timeout| timeout.saturating_sub(started_at.elapsed())))
            .await
    }

    pub async fn text(
        &self,
        input: impl IntoTurnInput,
        options: ConversationTurnOptions,
    ) -> Result<String, NvokenError> {
        self.run(input, options)
            .await?
            .text
            .ok_or(NvokenError::NoOutputText)
    }
}

impl TurnOptions {
    fn with_optional_user(mut self, user: Option<String>) -> Self {
        self.user = user;
        self
    }
}

#[derive(Clone)]
pub struct Turn {
    client: Client,
    pub id: String,
    pub tenant: String,
    pub user: Option<String>,
    resource: Option<models::Turn>,
    tools: HashMap<String, Tool>,
    handled_tool_call_ids: Arc<StdMutex<HashSet<String>>>,
    admission: Option<TurnAdmission>,
}

impl Turn {
    pub(crate) fn client(&self) -> &Client {
        &self.client
    }

    fn new(
        client: Client,
        id: String,
        tenant: String,
        user: Option<String>,
        tools: HashMap<String, Tool>,
    ) -> Self {
        Self {
            client,
            id,
            tenant,
            user,
            resource: None,
            tools,
            handled_tool_call_ids: Arc::new(StdMutex::new(HashSet::new())),
            admission: None,
        }
    }

    pub fn bind_tools(mut self, tools: impl IntoIterator<Item = Tool>) -> Self {
        self.tools
            .extend(tools.into_iter().map(|tool| (tool.name.clone(), tool)));
        self
    }

    fn request(&self, method: reqwest::Method, path: &str) -> reqwest_middleware::RequestBuilder {
        let mut request = self
            .client
            .raw()
            .client
            .request(method, format!("{}{}", self.client.raw().base_path, path))
            .header("X-Nvoken-Tenant-Key", &self.tenant);
        if let Some(user) = &self.user {
            request = request.header("X-Nvoken-User-Key", user);
        }
        if let Some(user_agent) = &self.client.raw().user_agent {
            request = request.header(reqwest::header::USER_AGENT, user_agent);
        }
        if let Some(token) = &self.client.raw().bearer_access_token {
            request = request.bearer_auth(token);
        }
        request
    }

    async fn get_result(&self) -> Result<models::TurnResult, NvokenError> {
        let path = format!("/v1/turns/{}/result", crate::apis::urlencode(&self.id));
        let response = self
            .request(reqwest::Method::GET, &path)
            .send()
            .await
            .map_err(api_error)?;
        let status = response.status();
        if !status.is_success() {
            let body = response.text().await.unwrap_or_default();
            return Err(NvokenError::Api(format!(
                "Turn result returned HTTP {status}: {body}"
            )));
        }
        response.json().await.map_err(api_error)
    }

    fn snapshot_from_wire(&mut self, result: models::TurnResult) -> TurnSnapshot {
        let resource = *result.turn;
        self.resource = Some(resource.clone());
        let (behavior_source, agent_id, agent_revision_id) = match resource
            .behavior_source
            .as_deref()
        {
            Some(models::TurnBehaviorSource::AgentRevision(source)) => (
                BehaviorSourceKind::AgentRevision,
                Some(source.agent_id.clone()),
                Some(source.agent_revision_id.clone()),
            ),
            Some(models::TurnBehaviorSource::Inline(_)) => (BehaviorSourceKind::Inline, None, None),
            None => {
                return TurnSnapshot {
                    status: resource.status,
                    messages: result.messages,
                    text: result.output_text,
                    structured_output: resource.structured_output,
                    behavior_source: BehaviorSourceKind::Inline,
                    agent_id: None,
                    agent_revision_id: None,
                    memory_space_id: resource.memory_space_id,
                    conversation_id: resource.conversation_id,
                    content_expires_at: resource.content_expires_at,
                };
            }
        };
        TurnSnapshot {
            status: resource.status,
            messages: result.messages,
            text: result.output_text,
            structured_output: resource.structured_output,
            behavior_source,
            agent_id,
            agent_revision_id,
            memory_space_id: resource.memory_space_id,
            conversation_id: resource.conversation_id,
            content_expires_at: resource.content_expires_at,
        }
    }

    pub async fn status(&mut self) -> Result<TurnSnapshot, NvokenError> {
        let result = self.get_result().await?;
        Ok(self.snapshot_from_wire(result))
    }

    /// Asks the Turn to stop at its next clean stopping point, keeping what it
    /// produced.
    ///
    /// The snapshot is the Turn's state as of the request, which is often
    /// still running: mid-step the runtime records the request and stops at the
    /// next checkpoint. Settlement is the stream's to report, so follow
    /// [`Turn::updates`] or [`Turn::result`] for it rather than reading this
    /// status as final. Interrupting a Turn that already ended returns it
    /// unchanged and is not an error. The snapshot carries no messages, because
    /// the interrupt response is the Turn resource alone.
    pub async fn interrupt(&mut self) -> Result<TurnSnapshot, NvokenError> {
        let path = format!("/v1/turns/{}/interrupt", crate::apis::urlencode(&self.id));
        // No body. The contract declares no request body for this operation,
        // and the other three SDKs' generated clients send none either.
        let response = self
            .request(reqwest::Method::POST, &path)
            .header(reqwest::header::CONTENT_LENGTH, 0)
            .send()
            .await
            .map_err(api_error)?;
        let status = response.status();
        if !status.is_success() {
            let body = response.text().await.unwrap_or_default();
            return Err(NvokenError::Api(format!(
                "Turn interrupt returned HTTP {status}: {body}"
            )));
        }
        let turn: models::Turn = response.json().await.map_err(api_error)?;
        Ok(self.snapshot_from_wire(models::TurnResult {
            turn: Box::new(turn),
            messages: Vec::new(),
            output_text: None,
        }))
    }

    pub async fn result(self, timeout: Option<Duration>) -> Result<TurnResult, NvokenError> {
        let recovery = self.clone();
        match timeout {
            Some(timeout) => tokio::time::timeout(timeout, self.wait_for_result())
                .await
                .map_err(|_| TurnTimeoutError {
                    turn: Some(recovery.clone()),
                    idempotency_key: recovery
                        .admission
                        .as_ref()
                        .map(|admission| admission.idempotency_key.clone()),
                })?,
            None => self.wait_for_result().await,
        }
    }

    async fn wait_for_result(mut self) -> Result<TurnResult, NvokenError> {
        loop {
            let snapshot = self.status().await?;
            if snapshot.status == models::TurnStatus::Waiting
                && self
                    .resource
                    .as_ref()
                    .is_some_and(|resource| !resource.tool_calls.is_empty())
            {
                let resource = self.resource.clone().expect("checked above");
                if self.run_host_tools(&resource).await? {
                    continue;
                }
            }
            if self
                .resource
                .as_ref()
                .is_some_and(|resource| resource.ended_at.is_some())
            {
                let result = TurnResult {
                    status: snapshot.status,
                    messages: snapshot.messages,
                    text: snapshot.text,
                    structured_output: snapshot.structured_output,
                    behavior_source: snapshot.behavior_source,
                    agent_id: snapshot.agent_id,
                    agent_revision_id: snapshot.agent_revision_id,
                    memory_space_id: snapshot.memory_space_id,
                    conversation_id: snapshot.conversation_id,
                    content_expires_at: snapshot.content_expires_at,
                    turn: self.clone(),
                    admission: self.admission.clone(),
                };
                if matches!(
                    result.status,
                    models::TurnStatus::Failed | models::TurnStatus::Cancelled
                ) {
                    return Err(TurnExecutionError { result }.into());
                }
                return Ok(result);
            }
            tokio::time::sleep(Duration::from_millis(250)).await;
        }
    }

    /// Resumable reduced-state updates. Dropping it detaches without cancelling the Turn.
    pub fn updates(
        mut self,
        options: crate::stream::StreamOptions,
    ) -> impl futures_core::Stream<Item = Result<TurnUpdate, NvokenError>> {
        async_stream::stream! {
            use futures_util::StreamExt;

            let initial = match self.status().await {
                Ok(snapshot) => snapshot,
                Err(error) => {
                    yield Err(error);
                    return;
                }
            };
            let initial_waiting = initial.status == models::TurnStatus::Waiting;
            let initial_resource = self.resource.clone();
            let initial_terminal = initial_resource.as_ref().is_some_and(|resource| resource.ended_at.is_some());
            yield Ok(TurnUpdate {
                snapshot: initial,
                frame: None,
                cursor: options.cursor.clone(),
            });
            if initial_waiting {
                if let Some(resource) = initial_resource.as_ref() {
                    if let Err(error) = self.run_host_tools(resource).await {
                        yield Err(error);
                        return;
                    }
                }
            }
            if initial_terminal {
                return;
            }

            let observer = self.clone();
            let mut reducer = crate::stream::Reducer::default();
            let inner = crate::stream::stream_turn(&observer, options.clone());
            futures_util::pin_mut!(inner);
            while let Some(item) = inner.next().await {
                let event = match item {
                    Ok(event) => event,
                    Err(error) => {
                        yield Err(error);
                        break;
                    }
                };
                if let Err(error) = reducer.apply(&event) {
                    yield Err(error);
                    break;
                }
                let reduced = reducer.snapshot();
                let mut snapshot = match self.status().await {
                    Ok(snapshot) => snapshot,
                    Err(error) => {
                        yield Err(error);
                        break;
                    }
                };
                if !reduced.messages.is_empty() {
                    snapshot.messages = reduced.messages;
                }
                let waiting = snapshot.status == models::TurnStatus::Waiting;
                let resource = self.resource.clone();
                let terminal = resource.as_ref().is_some_and(|resource| resource.ended_at.is_some())
                    || reducer.settled(&self.id);
                yield Ok(TurnUpdate {
                    snapshot,
                    frame: Some(event),
                    cursor: reduced.cursor,
                });
                if waiting {
                    if let Some(resource) = resource.as_ref() {
                        match self.run_host_tools(resource).await {
                            Ok(true) => {}
                            Ok(false) => {}
                            Err(error) => {
                                yield Err(error);
                                break;
                            }
                        }
                    }
                }
                if terminal {
                    break;
                }
            }
        }
    }

    async fn run_host_tools(&self, turn: &models::Turn) -> Result<bool, NvokenError> {
        let pending = {
            let mut handled = self
                .handled_tool_call_ids
                .lock()
                .expect("tool replay guard poisoned");
            let mut pending = Vec::new();
            for call in &turn.tool_calls {
                if call.mode == models::ToolCallMode::Host
                    && call.arguments.is_some()
                    && self.tools.contains_key(&call.name)
                    && !handled.contains(&call.id)
                {
                    handled.insert(call.id.clone());
                    pending.push(call);
                }
            }
            pending
        };
        if pending.is_empty() {
            return Ok(false);
        }
        let attempted = pending
            .iter()
            .map(|call| call.id.clone())
            .collect::<Vec<_>>();
        let mut attempt_guard = ToolAttemptGuard {
            handled: self.handled_tool_call_ids.clone(),
            call_ids: attempted,
            committed: false,
        };
        let mut results = Vec::new();
        for call in pending {
            let tool = &self.tools[&call.name];
            let arguments = match serde_json::to_value(
                call.arguments
                    .as_ref()
                    .expect("pending host call has arguments"),
            ) {
                Ok(arguments) => arguments,
                Err(error) => return Err(api_error(error)),
            };
            let outcome = (tool.handler)(
                arguments,
                ToolContext {
                    turn_id: self.id.clone(),
                    tool_call_id: call.id.clone(),
                },
            )
            .await;
            let mut result = models::SubmitHostToolResultsRequestResultsInner::new(
                call.id.clone(),
                Some(outcome.clone().unwrap_or_else(Value::String)),
            );
            result.is_error = Some(outcome.is_err());
            results.push(result);
        }
        let path = format!(
            "/v1/turns/{}/tool-results",
            crate::apis::urlencode(&self.id)
        );
        let response = match self
            .request(reqwest::Method::POST, &path)
            .json(&models::SubmitHostToolResultsRequest::new(results))
            .send()
            .await
        {
            Ok(response) => response,
            Err(error) => return Err(api_error(error)),
        };
        let status = response.status();
        if !status.is_success() {
            let body = response.text().await.unwrap_or_default();
            return Err(NvokenError::Api(format!(
                "host tool submission returned HTTP {status}: {body}"
            )));
        }
        attempt_guard.committed = true;
        Ok(true)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Arc as StdArc;
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpListener;

    async fn response_server(
        responses: Vec<(u16, String)>,
    ) -> (String, tokio::task::JoinHandle<Vec<String>>) {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base_url = format!("http://{}", listener.local_addr().unwrap());
        let task = tokio::spawn(async move {
            let mut requests = Vec::new();
            for (status, body) in responses {
                let (mut socket, _) = listener.accept().await.unwrap();
                let mut bytes = vec![0; 16_384];
                let count = socket.read(&mut bytes).await.unwrap();
                requests.push(String::from_utf8_lossy(&bytes[..count]).into_owned());
                let reason = if status == 200 {
                    "OK"
                } else {
                    "Service Unavailable"
                };
                let response = format!(
                    "HTTP/1.1 {status} {reason}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                    body.len()
                );
                socket.write_all(response.as_bytes()).await.unwrap();
            }
            requests
        });
        (base_url, task)
    }

    #[test]
    fn inline_request_keeps_runtime_coordinates_independent() {
        let client = Client::with_base_url("test", "http://localhost");
        let inline = client.inline(Behavior::new("Analyze", "openai/gpt-5"));
        let mut limits = models::Limits::default();
        limits.max_iterations = Some(6);
        let request = inline
            .runner
            .request(
                models::TurnInput::String("Compare".into()),
                &TurnOptions::new("acme")
                    .user("alice")
                    .memory(Memory::tenant("portfolio"))
                    .conversation(ConversationRef::by_key("deal-42", ConversationOwner::User))
                    .limits(limits)
                    .idempotency_key("turn-request-1"),
            )
            .unwrap();
        let value = serde_json::to_value(request).unwrap();
        assert_eq!(value["behavior"]["kind"], "inline");
        assert_eq!(value["tenant_key"], "acme");
        assert_eq!(value["user_key"], "alice");
        assert_eq!(value["memory"]["scope"], "tenant");
        assert_eq!(value["conversation"]["owner"]["kind"], "user");
        assert_eq!(value["limits"]["max_iterations"], 6);
    }

    #[test]
    fn inline_memory_requires_namespace_and_user_actor() {
        let client = Client::with_base_url("test", "http://localhost");
        let inline = client.inline(Behavior::new("Help", "openai/gpt-5"));
        let no_namespace = inline.runner.request(
            models::TurnInput::String("Help".into()),
            &TurnOptions::new("acme").memory(Memory::Tenant { namespace: None }),
        );
        assert!(matches!(no_namespace, Err(NvokenError::InvalidInput(_))));

        let no_actor = inline.runner.request(
            models::TurnInput::String("Help".into()),
            &TurnOptions::new("acme").memory(Memory::user("personal")),
        );
        assert!(matches!(no_actor, Err(NvokenError::InvalidInput(_))));
        assert!(InlineMemory::tenant("").is_err());
        assert!(InlineMemory::user("").is_err());
    }

    #[test]
    fn keyed_conversation_keeps_create_options_without_conflict_controls() {
        let retention = models::RetentionPolicy::new(3600);
        let compaction =
            models::CompactionPolicy::new(models::CompactionPolicyTriggerTokens::Integer(4_000));
        let reference = ConversationRef::by_key("case-42", ConversationOwner::Tenant)
            .retention(retention)
            .compaction(compaction)
            .metadata(HashMap::from([
                ("title".into(), Value::String("Home search".into())),
                ("rank".into(), Value::from(2)),
            ]));
        let value = serde_json::to_value(reference.selection(None).unwrap()).unwrap();
        assert_eq!(value["conversation_key"], "case-42");
        assert_eq!(value["retention"]["ttl_seconds"], 3600);
        assert_eq!(value["compaction"]["trigger_tokens"], 4_000);
        assert_eq!(value["metadata"]["rank"], 2);
        assert!(value.get("if_active").is_none());
    }

    #[test]
    fn structured_model_input_is_preserved() {
        let behavior = Behavior::new(
            "Analyze",
            models::Model::new("openai".into(), "gpt-5".into()),
        );
        assert!(matches!(behavior.model(), models::ModelInput::Model(model)
            if model.provider == "openai" && model.id == "gpt-5"));
    }

    #[test]
    fn inline_bind_rejects_a_handler_not_declared_by_the_behavior() {
        let client = Client::with_base_url("test", "http://localhost");
        let inline = client.inline(Behavior::new("Help", "openai/gpt-5"));
        let result = inline.bind_tools([Tool::new("lookup", |arguments, _context| async move {
            Ok(arguments)
        })]);
        assert!(matches!(result, Err(NvokenError::InvalidInput(_))));
    }

    #[test]
    fn conversation_limits_narrow_and_tenant_lock_ignores_actor() {
        let mut bound = models::Limits::default();
        bound.max_iterations = Some(6);
        bound.max_output_tokens = Some(1_000);
        let mut requested = models::Limits::default();
        requested.max_iterations = Some(3);
        let merged = merge_narrow_limits(Some(bound.clone()), Some(requested))
            .unwrap()
            .unwrap();
        assert_eq!(merged.max_iterations, Some(3));
        assert_eq!(merged.max_output_tokens, Some(1_000));

        let mut wider = models::Limits::default();
        wider.max_iterations = Some(7);
        assert!(matches!(
            merge_narrow_limits(Some(bound), Some(wider)),
            Err(NvokenError::InvalidInput(_))
        ));

        let reference = ConversationRef::by_key("shared", ConversationOwner::Tenant);
        assert_eq!(
            reference.lock_key("acme", Some("alice")),
            reference.lock_key("acme", Some("bob"))
        );
    }

    #[test]
    fn rich_turn_input_is_preserved_and_idempotency_is_generated() {
        let client = Client::with_base_url("test", "http://localhost");
        let inline = client.inline(Behavior::new("Help", "openai/gpt-5"));
        let block = models::InputBlock::TextInputBlock(Box::new(models::TextInputBlock::new(
            models::text_input_block::Type::InputTypeText,
            "hi".into(),
        )));
        let request = inline
            .runner
            .request(
                models::TurnInput::ArrayVecInputBlock(vec![block]),
                &TurnOptions::new("acme"),
            )
            .unwrap();
        assert!(matches!(
            *request.input,
            models::TurnInput::ArrayVecInputBlock(_)
        ));
        assert!(request.idempotency_key.starts_with("sdk-"));
    }

    #[test]
    fn turn_snapshot_flattens_the_high_level_result() {
        let client = Client::with_base_url("test", "http://localhost");
        let mut turn = client.turn("8f57d547-fa52-75fa-947d-41e21909db99", "acme", None);
        let source = models::StoredTurnBehaviorSource::new(
            models::stored_turn_behavior_source::Kind::AgentRevision,
            "47fc63e5-ae78-727c-ab52-a2872fe8728f".into(),
            "4e2c07c1-1b15-7f5e-b42b-8e1b29dc83fd".into(),
            1,
        );
        let resource = models::Turn {
            id: "8f57d547-fa52-75fa-947d-41e21909db99".into(),
            tenant_key: "acme".into(),
            status: models::TurnStatus::Running,
            behavior_source: Some(Box::new(models::TurnBehaviorSource::AgentRevision(
                Box::new(source),
            ))),
            structured_output: Some(HashMap::from([("score".into(), Value::from(9))])),
            memory_space_id: Some("b1d4c373-50e5-77d6-a4c3-20b23c06e787".into()),
            conversation_id: Some("18325d9f-b9bc-797d-9259-96ece372defd".into()),
            ..models::Turn::default()
        };
        let snapshot = turn.snapshot_from_wire(models::TurnResult::new(
            resource,
            Vec::new(),
            Some("done".into()),
        ));
        assert_eq!(snapshot.status, models::TurnStatus::Running);
        assert_eq!(snapshot.text.as_deref(), Some("done"));
        assert_eq!(snapshot.behavior_source, BehaviorSourceKind::AgentRevision);
        assert_eq!(
            snapshot.agent_id.as_deref(),
            Some("47fc63e5-ae78-727c-ab52-a2872fe8728f")
        );
        assert_eq!(
            snapshot.agent_revision_id.as_deref(),
            Some("4e2c07c1-1b15-7f5e-b42b-8e1b29dc83fd")
        );
        assert_eq!(
            snapshot.memory_space_id.as_deref(),
            Some("b1d4c373-50e5-77d6-a4c3-20b23c06e787")
        );
        assert_eq!(
            snapshot.conversation_id.as_deref(),
            Some("18325d9f-b9bc-797d-9259-96ece372defd")
        );
    }

    #[tokio::test]
    async fn missing_tool_handler_leaves_waiting_turn_unmodified() {
        let client = Client::with_base_url("test", "http://localhost");
        let turn = client.turn("8f57d547-fa52-75fa-947d-41e21909db99", "acme", None);
        let mut call = models::ToolCallSummary::default();
        call.id = "9f8fd6b3-9060-783d-b759-45c8ec70e8cb".into();
        call.name = "lookup".into();
        call.mode = models::ToolCallMode::Host;
        call.arguments = Some(HashMap::from([("id".into(), Value::from(1))]));
        let resource = models::Turn {
            tool_calls: vec![call],
            ..models::Turn::default()
        };
        assert!(!turn.run_host_tools(&resource).await.unwrap());
    }

    #[tokio::test]
    async fn known_tool_calls_run_once_with_context_while_unknown_calls_wait() {
        let (base_url, server) = response_server(vec![(200, "{}".into())]).await;
        let client = Client::with_base_url("test", base_url);
        let contexts = StdArc::new(Mutex::new(Vec::new()));
        let handler_contexts = contexts.clone();
        let tool = Tool::new("lookup", move |arguments, context| {
            let contexts = handler_contexts.clone();
            async move {
                contexts.lock().await.push(context);
                Ok(arguments)
            }
        });
        let turn = client
            .turn(
                "8f57d547-fa52-75fa-947d-41e21909db99",
                "acme",
                Some("alice".into()),
            )
            .bind_tools([tool]);
        let call = |id: &str, name: &str| {
            let mut call = models::ToolCallSummary::default();
            call.id = id.into();
            call.name = name.into();
            call.mode = models::ToolCallMode::Host;
            call.arguments = Some(HashMap::from([("id".into(), Value::from(1))]));
            call
        };
        let resource = models::Turn {
            tool_calls: vec![
                call("9f8fd6b3-9060-783d-b759-45c8ec70e8cb", "lookup"),
                call("8b6c8687-a698-7aeb-8440-29729d2fc4b7", "other"),
            ],
            ..models::Turn::default()
        };
        assert!(turn.run_host_tools(&resource).await.unwrap());
        assert!(!turn.run_host_tools(&resource).await.unwrap());
        let requests = server.await.unwrap();
        assert_eq!(requests.len(), 1);
        assert!(requests[0].contains("9f8fd6b3-9060-783d-b759-45c8ec70e8cb"));
        assert!(!requests[0].contains("8b6c8687-a698-7aeb-8440-29729d2fc4b7"));
        assert_eq!(
            *contexts.lock().await,
            vec![ToolContext {
                turn_id: "8f57d547-fa52-75fa-947d-41e21909db99".into(),
                tool_call_id: "9f8fd6b3-9060-783d-b759-45c8ec70e8cb".into(),
            }]
        );
    }

    #[tokio::test]
    async fn failed_tool_submission_clears_the_replay_guard() {
        let (base_url, server) =
            response_server(vec![(503, "{}".into()), (200, "{}".into())]).await;
        let client = Client::with_base_url("test", base_url);
        let calls = StdArc::new(Mutex::new(0));
        let handler_calls = calls.clone();
        let turn = client
            .turn("8f57d547-fa52-75fa-947d-41e21909db99", "acme", None)
            .bind_tools([Tool::new("lookup", move |arguments, _context| {
                let calls = handler_calls.clone();
                async move {
                    *calls.lock().await += 1;
                    Ok(arguments)
                }
            })]);
        let mut call = models::ToolCallSummary::default();
        call.id = "9f8fd6b3-9060-783d-b759-45c8ec70e8cb".into();
        call.name = "lookup".into();
        call.mode = models::ToolCallMode::Host;
        call.arguments = Some(HashMap::new());
        let resource = models::Turn {
            tool_calls: vec![call],
            ..models::Turn::default()
        };
        assert!(turn.run_host_tools(&resource).await.is_err());
        assert!(turn.run_host_tools(&resource).await.unwrap());
        assert_eq!(*calls.lock().await, 2);
        assert_eq!(server.await.unwrap().len(), 2);
    }

    #[tokio::test]
    async fn cancelled_tool_handler_clears_the_replay_guard() {
        let client = Client::with_base_url("test", "http://localhost");
        let (entered_tx, entered_rx) = tokio::sync::oneshot::channel();
        let entered_tx = StdArc::new(StdMutex::new(Some(entered_tx)));
        let signal = entered_tx.clone();
        let turn = client
            .turn("8f57d547-fa52-75fa-947d-41e21909db99", "acme", None)
            .bind_tools([Tool::new("lookup", move |_arguments, _context| {
                let signal = signal.clone();
                async move {
                    if let Some(sender) = signal.lock().unwrap().take() {
                        let _ = sender.send(());
                    }
                    futures_util::future::pending::<Result<Value, String>>().await
                }
            })]);
        let handled = turn.handled_tool_call_ids.clone();
        let mut call = models::ToolCallSummary::default();
        call.id = "9f8fd6b3-9060-783d-b759-45c8ec70e8cb".into();
        call.name = "lookup".into();
        call.mode = models::ToolCallMode::Host;
        call.arguments = Some(HashMap::new());
        let resource = models::Turn {
            tool_calls: vec![call],
            ..models::Turn::default()
        };
        let task = tokio::spawn(async move { turn.run_host_tools(&resource).await });
        entered_rx.await.unwrap();
        task.abort();
        let _ = task.await;
        assert!(!handled
            .lock()
            .unwrap()
            .contains("9f8fd6b3-9060-783d-b759-45c8ec70e8cb"));
    }

    const RUNNING_TURN: &str = r#"{"id":"476dd7be-97a1-78f3-8096-d7032468a80a","status":"running","tenant_key":"acme","attempt":1,"active_execution_ms":1,"conversation_id":"18325d9f-b9bc-797d-9259-96ece372defd","memory_space_id":null,"content_expires_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","deadline_at":null,"ended_at":null,"error":null,"stop_reason":null,"structured_output":null,"tool_calls":[]}"#;

    const COMPLETED_TURN: &str = r#"{"id":"476dd7be-97a1-78f3-8096-d7032468a80a","status":"completed","tenant_key":"acme","attempt":1,"active_execution_ms":1,"conversation_id":null,"memory_space_id":null,"content_expires_at":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:01Z","deadline_at":null,"ended_at":"2026-01-01T00:00:01Z","error":null,"stop_reason":"end_turn","structured_output":null,"tool_calls":[]}"#;

    #[tokio::test]
    async fn interrupt_returns_the_post_request_state_and_sends_no_body() {
        let (base_url, server) = response_server(vec![(200, RUNNING_TURN.to_string())]).await;
        let client = Client::with_base_url("test", base_url);
        let mut turn = client.turn("476dd7be-97a1-78f3-8096-d7032468a80a", "acme", None);

        // Mid-step the runtime records the request and leaves the Turn running.
        let snapshot = turn.interrupt().await.unwrap();
        assert_eq!(snapshot.status, models::TurnStatus::Running);
        assert_eq!(
            snapshot.conversation_id.as_deref(),
            Some("18325d9f-b9bc-797d-9259-96ece372defd")
        );
        // The interrupt response is the Turn resource alone; the transcript
        // stays the stream's to deliver.
        assert!(snapshot.messages.is_empty());
        assert!(snapshot.text.is_none());

        let requests = server.await.unwrap();
        assert!(requests[0]
            .starts_with("POST /v1/turns/476dd7be-97a1-78f3-8096-d7032468a80a/interrupt HTTP/1.1"));
        // The contract declares no request body for this operation.
        assert!(requests[0].contains("content-length: 0"));
    }

    #[tokio::test]
    async fn interrupting_a_finished_turn_returns_it_unchanged() {
        let (base_url, server) = response_server(vec![(200, COMPLETED_TURN.to_string())]).await;
        let client = Client::with_base_url("test", base_url);
        let mut turn = client.turn("476dd7be-97a1-78f3-8096-d7032468a80a", "acme", None);
        let snapshot = turn.interrupt().await.unwrap();
        assert_eq!(snapshot.status, models::TurnStatus::Completed);
        server.await.unwrap();
    }

    #[tokio::test]
    async fn uncertain_admission_keeps_the_generated_idempotency_key() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base_url = format!("http://{}", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            let (socket, _) = listener.accept().await.unwrap();
            drop(socket);
        });
        let client = Client::with_base_url("test", base_url);
        let inline = client.inline(Behavior::new("Help", "openai/gpt-5"));
        let outcome = inline
            .runner
            .start(
                models::TurnInput::String("hello".into()),
                TurnOptions::new("acme"),
            )
            .await;
        match outcome {
            Err(NvokenError::TurnAdmission(error)) => {
                assert!(error.idempotency_key.starts_with("sdk-"));
            }
            Err(other) => panic!("expected uncertain admission error, got {other:?}"),
            Ok(_) => panic!("expected uncertain admission error"),
        }
        server.abort();
    }

    #[tokio::test]
    async fn admission_timeout_keeps_the_generated_idempotency_key() {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base_url = format!("http://{}", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            let (_socket, _) = listener.accept().await.unwrap();
            futures_util::future::pending::<()>().await;
        });
        let client = Client::with_base_url("test", base_url);
        let inline = client.inline(Behavior::new("Help", "openai/gpt-5"));
        let outcome = inline
            .start(
                "hello",
                InlineTurnOptions::new("acme").timeout(Duration::from_millis(1)),
            )
            .await;
        match outcome {
            Err(NvokenError::TurnTimeout(error)) => {
                assert!(error.turn.is_none());
                assert!(error.idempotency_key.unwrap().starts_with("sdk-"));
            }
            Err(other) => panic!("expected admission timeout, got {other:?}"),
            Ok(_) => panic!("expected admission timeout"),
        }
        server.abort();
    }

    #[test]
    fn lifecycle_resource_refresh_preserves_bound_tools() {
        let archived = models::Agent {
            id: "47fc63e5-ae78-727c-ab52-a2872fe8728f".into(),
            agent_key: "analyst".into(),
            name: "Analyst".into(),
            ..models::Agent::default()
        };
        let client = Client::with_base_url("test", "http://localhost");
        let agent = Agent::new(client, archived)
            .bind_tools([Tool::new("lookup", |arguments, _context| async move {
                Ok(arguments)
            })])
            .unwrap();
        let archived = agent.with_resource(models::Agent {
            archived_at: Some(
                chrono::DateTime::parse_from_rfc3339("2026-08-26T12:00:00Z").unwrap(),
            ),
            ..agent.resource.clone()
        });
        assert!(archived.runner.tools.contains_key("lookup"));
    }

    #[tokio::test]
    async fn result_reports_typed_execution_and_timeout_errors_with_recovery_context() {
        let timestamp = chrono::DateTime::parse_from_rfc3339("2026-08-26T12:00:00Z").unwrap();
        let failed_resource = models::Turn {
            id: "ca2779a8-1755-7ea1-aed1-9e84834989cd".into(),
            tenant_key: "acme".into(),
            status: models::TurnStatus::Failed,
            ended_at: Some(timestamp),
            ..models::Turn::default()
        };
        let failed_body =
            serde_json::to_string(&models::TurnResult::new(failed_resource, Vec::new(), None))
                .unwrap();
        let (base_url, server) = response_server(vec![(200, failed_body)]).await;
        let client = Client::with_base_url("test", base_url);
        let mut failed = client.turn("ca2779a8-1755-7ea1-aed1-9e84834989cd", "acme", None);
        failed.admission = Some(TurnAdmission {
            idempotency_key: "idem-failed".into(),
            deduplicated: false,
        });
        match failed.result(None).await {
            Err(NvokenError::TurnExecution(error)) => {
                assert_eq!(error.result.turn.id, "ca2779a8-1755-7ea1-aed1-9e84834989cd");
                assert_eq!(
                    error.result.admission.unwrap().idempotency_key,
                    "idem-failed"
                );
            }
            Err(other) => panic!("expected execution error, got {other:?}"),
            Ok(_) => panic!("expected execution error"),
        }
        server.await.unwrap();

        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base_url = format!("http://{}", listener.local_addr().unwrap());
        let server = tokio::spawn(async move {
            let (_socket, _) = listener.accept().await.unwrap();
            futures_util::future::pending::<()>().await;
        });
        let client = Client::with_base_url("test", base_url);
        let mut running = client.turn("ac35fb8c-65ce-7b16-b612-be4f87a1c0ab", "acme", None);
        running.admission = Some(TurnAdmission {
            idempotency_key: "idem-running".into(),
            deduplicated: false,
        });
        match running.result(Some(Duration::from_millis(1))).await {
            Err(NvokenError::TurnTimeout(error)) => {
                assert_eq!(
                    error.turn.as_ref().unwrap().id,
                    "ac35fb8c-65ce-7b16-b612-be4f87a1c0ab"
                );
                assert_eq!(error.idempotency_key.as_deref(), Some("idem-running"));
            }
            Err(other) => panic!("expected timeout error, got {other:?}"),
            Ok(_) => panic!("expected timeout error"),
        }
        server.abort();
    }

    #[tokio::test]
    async fn conversation_run_uses_the_shared_identity_lock() {
        let client = Client::with_base_url("test", "http://localhost");
        let reference = ConversationRef::by_key("shared", ConversationOwner::Tenant);
        let key = reference.lock_key("acme", Some("alice"));
        let first = client.conversation_lock(key.clone()).await;
        let second = client.conversation_lock(key).await;
        assert!(Arc::ptr_eq(&first, &second));
        let _guard = first.lock().await;
        assert!(second.try_lock().is_err());
    }

    #[test]
    fn recovery_requests_carry_tenant_and_user_assertions() {
        let client = Client::with_base_url("test", "http://localhost");
        let turn = client.turn(
            "8f57d547-fa52-75fa-947d-41e21909db99",
            "acme",
            Some("alice".into()),
        );
        let request = turn
            .request(
                reqwest::Method::GET,
                "/v1/turns/8f57d547-fa52-75fa-947d-41e21909db99/result",
            )
            .build()
            .unwrap();
        assert_eq!(request.headers()["X-Nvoken-Tenant-Key"], "acme");
        assert_eq!(request.headers()["X-Nvoken-User-Key"], "alice");
    }
}
