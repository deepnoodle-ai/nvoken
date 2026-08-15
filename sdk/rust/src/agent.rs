//! A high-level Agent binding on top of the low-level `Client` verbs:
//! `text`/`run`/`invoke`/`stream`/`session`, a host-tool dispatch loop, and
//! structured-output access. Parity target with the Go and Python bindings.

use std::collections::{HashMap, HashSet};
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context as TaskContext, Poll};
use std::time::Duration;

use async_stream::try_stream;
use futures_core::Stream;
use futures_util::{pin_mut, StreamExt};
use serde_json::{json, Value};

use crate::client::{
    AgentDefinition, BudgetExhaustionBehavior, Client, ContextItem, ErrorCategory, IfActivePolicy,
    InvocationHandle, InvokeRequest, Limits, McpServer, McpServerHeaders, Model, NvokenError,
    ProviderKeySelection, ProviderTool, Reasoning, Sampling, SessionOptions, Tool, ToolChoice,
    ToolMode, ToolResult, WaitCondition, WaitOptions, WebhookTarget,
};
use crate::models;
use crate::stream::StreamEvent;

/// Fixed identity and the Agent Definition every call through an `Agent`
/// admits with. Built with `Client::agent`.
#[derive(Clone)]
pub struct AgentOptions {
    pub agent_key: String,
    pub tenant_key: Option<String>,
    /// The inline configuration every turn from this Agent runs with. When
    /// `agent_definition_id` is set, only its host tool handlers are used
    /// locally; nvoken resolves the reusable definition resource.
    pub agent_definition: AgentDefinition,
    /// An App-owned reusable Agent Definition. Exactly one of this ID or the
    /// inline configuration is sent on every Invocation.
    pub agent_definition_id: Option<String>,
    /// Per-turn secret headers for the MCP servers the definition declares.
    pub mcp_server_headers: Vec<McpServerHeaders>,
    pub provider_keys: Vec<ProviderKeySelection>,
    /// Endpoint every Invocation this Agent admits notifies by default. A
    /// per-call target on `AgentInvocationOptions` overrides it.
    pub webhook: Option<WebhookTarget>,
    pub on_budget_exhausted: Option<BudgetExhaustionBehavior>,
}

impl AgentOptions {
    pub fn new(agent_key: impl Into<String>, model: Model) -> Self {
        Self {
            agent_key: agent_key.into(),
            tenant_key: None,
            agent_definition: AgentDefinition::new(model),
            agent_definition_id: None,
            mcp_server_headers: Vec::new(),
            provider_keys: Vec::new(),
            webhook: None,
            on_budget_exhausted: None,
        }
    }

    /// Builds an Agent backed by an App-owned reusable Agent Definition.
    /// Host tool handlers may still be attached with `tool`; the matching
    /// declarations must already exist on the resource.
    pub fn from_definition_id(
        agent_key: impl Into<String>,
        agent_definition_id: impl Into<String>,
    ) -> Self {
        Self {
            agent_key: agent_key.into(),
            tenant_key: None,
            agent_definition: AgentDefinition::default(),
            agent_definition_id: Some(agent_definition_id.into()),
            mcp_server_headers: Vec::new(),
            provider_keys: Vec::new(),
            webhook: None,
            on_budget_exhausted: None,
        }
    }

    pub fn instructions(mut self, instructions: impl Into<String>) -> Self {
        self.agent_definition.instructions = Some(instructions.into());
        self
    }

    pub fn limits(mut self, limits: Limits) -> Self {
        self.agent_definition.limits = Some(limits);
        self
    }

    pub fn sampling(mut self, sampling: Sampling) -> Self {
        self.agent_definition.sampling = Some(sampling);
        self
    }

    pub fn reasoning(mut self, reasoning: Reasoning) -> Self {
        self.agent_definition.reasoning = Some(reasoning);
        self
    }

    pub fn tool_choice(mut self, tool_choice: ToolChoice) -> Self {
        self.agent_definition.tool_choice = Some(tool_choice);
        self
    }

    pub fn tool(mut self, tool: Tool) -> Self {
        self.agent_definition.tools.push(tool);
        self
    }

    pub fn mcp_server(mut self, server: McpServer) -> Self {
        self.agent_definition.mcp_servers.push(server);
        self
    }

    pub fn provider_tool(mut self, tool: ProviderTool) -> Self {
        self.agent_definition.provider_tools.push(tool);
        self
    }

    pub fn output_schema(mut self, schema: HashMap<String, Value>) -> Self {
        self.agent_definition.output_schema = Some(schema);
        self
    }

    pub fn tenant_key(mut self, tenant_key: impl Into<String>) -> Self {
        self.tenant_key = Some(tenant_key.into());
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

    pub fn on_budget_exhausted(mut self, behavior: BudgetExhaustionBehavior) -> Self {
        self.on_budget_exhausted = Some(behavior);
        self
    }

    /// Replaces the whole inline Agent Definition.
    pub fn agent_definition(mut self, definition: AgentDefinition) -> Self {
        self.agent_definition = definition;
        self.agent_definition_id = None;
        self
    }

    pub fn mcp_server_headers(mut self, headers: McpServerHeaders) -> Self {
        self.mcp_server_headers.push(headers);
        self
    }
}

/// Per-call overrides for one Agent invocation.
#[derive(Clone, Default)]
pub struct AgentInvocationOptions {
    pub idempotency_key: Option<String>,
    pub tenant_key: Option<String>,
    pub session_id: Option<String>,
    pub session_key: Option<String>,
    pub session_options: Option<SessionOptions>,
    pub if_active: Option<IfActivePolicy>,
    pub on_budget_exhausted: Option<BudgetExhaustionBehavior>,
    pub webhook: Option<WebhookTarget>,
    pub wait: WaitOptions,
    /// Application state snapshots to record ahead of this turn's input.
    /// Per-call rather than per-Agent, because a snapshot is what changes
    /// between turns while the Agent Definition stays fixed.
    pub context: Vec<ContextItem>,
    /// Opaque host correlation data recorded on this Invocation. Immutable and
    /// material to idempotency: a replay carrying different metadata conflicts
    /// rather than updating it.
    pub metadata: Option<HashMap<String, String>>,
    /// When a waiting Invocation names a host tool this Agent has no handler
    /// for, the default is to cancel it so it never parks unattended. Set
    /// this to leave it waiting instead.
    pub leave_waiting_on_missing_handler: bool,
}

/// An execution lease taken before an unattended tool runs. Returning `false`
/// skips that call and leaves it for whoever holds the claim.
pub type ToolCallClaim = Arc<
    dyn Fn(
            models::ToolCallSummary,
        ) -> Pin<Box<dyn std::future::Future<Output = Result<bool, NvokenError>> + Send>>
        + Send
        + Sync,
>;

/// Options for [`Agent::answer_tool_calls`].
#[derive(Clone, Default)]
pub struct AnswerToolCallsOptions {
    /// Runs before each tool. Returning `false` skips that call — use it to
    /// take an execution lease keyed by the ToolCall id, so a streaming reader
    /// and this worker cannot both start the same non-idempotent tool.
    pub claim: Option<ToolCallClaim>,
    /// Report an error rather than skipping a call this Agent has no handler
    /// for. The default skips, because an unattended worker is often one of
    /// several answering different tools.
    pub leave_waiting_on_missing_handler: bool,
}

impl AnswerToolCallsOptions {
    pub fn claim<F, Fut>(mut self, claim: F) -> Self
    where
        F: Fn(models::ToolCallSummary) -> Fut + Send + Sync + 'static,
        Fut: std::future::Future<Output = Result<bool, NvokenError>> + Send + 'static,
    {
        self.claim = Some(Arc::new(move |pending| Box::pin(claim(pending))));
        self
    }

    pub fn leave_waiting_on_missing_handler(mut self, leave_waiting: bool) -> Self {
        self.leave_waiting_on_missing_handler = leave_waiting;
        self
    }
}

/// The composed result of a completed `run`/`text` call.
#[derive(Clone)]
pub struct AgentResult {
    pub handle: InvocationHandle,
    pub invocation: models::Invocation,
    pub messages: Vec<models::SessionMessage>,
    pub output_text: Option<String>,
    pub structured_output: Option<HashMap<String, Value>>,
    pub raw: models::InvocationResult,
}

/// One event observed while streaming an Agent invocation, paired with the
/// handle it belongs to.
#[derive(Clone)]
pub struct AgentStreamEvent {
    pub handle: InvocationHandle,
    pub event: StreamEvent,
}

/// A live event stream returned by `Agent::stream`. Host tool dispatch runs
/// as a side effect of polling this stream, exactly as it does when nothing
/// but `while let Some(event) = stream.next().await {}` drains it.
pub struct AgentEventStream {
    inner: Pin<Box<dyn Stream<Item = Result<AgentStreamEvent, NvokenError>> + Send>>,
}

impl Stream for AgentEventStream {
    type Item = Result<AgentStreamEvent, NvokenError>;

    fn poll_next(mut self: Pin<&mut Self>, cx: &mut TaskContext<'_>) -> Poll<Option<Self::Item>> {
        self.inner.as_mut().poll_next(cx)
    }
}

struct AgentInner {
    options: AgentOptions,
    host_tools: HashMap<String, Tool>,
}

/// Whether a pending call is this caller's to execute.
///
/// The call's own mode is the authority, not what the Agent happens to
/// declare: an Invocation running a server-owned Agent Definition can park on
/// callback tools the local object never listed, and answering those here would
/// run work nvoken is already delivering elsewhere. That replaced a set of the
/// Agent's own callback tool names, which only ever knew about its own.
///
/// `mode` is required on the wire and non-optional in the decoded summary, so
/// there is no case where it is missing to accommodate.
fn runs_locally(call: &models::ToolCallSummary) -> bool {
    call.mode == models::ToolCallMode::Host
}

/// A high-level binding for one Agent identity: `text`/`run`/`invoke`/
/// `stream`/`session` on top of the low-level `Client::invoke` verb, with a
/// host-tool dispatch loop and structured-output access built in. Cheap to
/// clone; every clone shares the same underlying definition and host tools.
#[derive(Clone)]
pub struct Agent {
    client: Client,
    inner: Arc<AgentInner>,
}

impl Client {
    /// Builds a high-level Agent. Inline definitions resolve an unset `model`
    /// from the client's default model once. Reusable definitions are selected
    /// by ID and may still carry local host tool handlers.
    pub fn agent(&self, mut options: AgentOptions) -> Result<Agent, NvokenError> {
        if options.agent_key.is_empty() {
            return Err(NvokenError::validation("agent_key is required"));
        }
        if let Some(definition_id) = options.agent_definition_id.as_ref() {
            if definition_id.is_empty() {
                return Err(NvokenError::validation(
                    "agent_definition_id must not be empty",
                ));
            }
            let definition = &options.agent_definition;
            if !definition.model.is_unset()
                || definition.instructions.is_some()
                || definition.sampling.is_some()
                || definition.reasoning.is_some()
                || definition.tool_choice.is_some()
                || definition.limits.is_some()
                || !definition.mcp_servers.is_empty()
                || !definition.provider_tools.is_empty()
                || definition.output_schema.is_some()
            {
                return Err(NvokenError::validation(
                    "agent_definition_id cannot be combined with inline Agent Definition fields",
                ));
            }
        } else if options.agent_definition.model.is_unset() {
            options.agent_definition.model = self
                .default_model()
                .ok_or_else(|| NvokenError::validation("model is required"))?;
        }
        let host_tools = options
            .agent_definition
            .tools
            .iter()
            .filter(|tool| matches!(tool.mode, ToolMode::Host))
            .map(|tool| (tool.name.clone(), tool.clone()))
            .collect();
        Ok(Agent {
            client: self.clone(),
            inner: Arc::new(AgentInner {
                options,
                host_tools,
            }),
        })
    }
}

impl Agent {
    /// Composes this Agent's identity and execution controls with one call's overrides into
    /// the request `invoke` admits. It is the single place a per-call option
    /// reaches the wire, so the conformance suite can pin the whole admitted
    /// body without a server.
    pub fn request(&self, input: String, options: &AgentInvocationOptions) -> InvokeRequest {
        let agent_options = &self.inner.options;
        InvokeRequest {
            agent_key: agent_options.agent_key.clone(),
            tenant_key: options
                .tenant_key
                .clone()
                .or_else(|| agent_options.tenant_key.clone()),
            session_id: options.session_id.clone(),
            session_key: options.session_key.clone(),
            session_options: options.session_options.clone(),
            idempotency_key: options.idempotency_key.clone(),
            if_active: options.if_active,
            on_budget_exhausted: options
                .on_budget_exhausted
                .or(agent_options.on_budget_exhausted),
            input,
            input_blocks: Vec::new(),
            agent_definition: agent_options
                .agent_definition_id
                .is_none()
                .then(|| agent_options.agent_definition.clone()),
            agent_definition_id: agent_options.agent_definition_id.clone(),
            mcp_server_headers: agent_options.mcp_server_headers.clone(),
            context: options.context.clone(),
            provider_keys: agent_options.provider_keys.clone(),
            // A per-call target overrides the Agent default so one Agent can
            // webhook different endpoints without a second Agent.
            webhook: options
                .webhook
                .clone()
                .or_else(|| agent_options.webhook.clone()),
            metadata: options.metadata.clone(),
        }
    }

    pub async fn invoke(
        &self,
        input: impl Into<String>,
        options: AgentInvocationOptions,
    ) -> Result<InvocationHandle, NvokenError> {
        let request = self.request(input.into(), &options);
        self.client.invoke(request).await
    }

    pub async fn run(
        &self,
        input: impl Into<String>,
        options: AgentInvocationOptions,
    ) -> Result<AgentResult, NvokenError> {
        self.run_with_handle(input, options).await.1
    }

    async fn run_with_handle(
        &self,
        input: impl Into<String>,
        options: AgentInvocationOptions,
    ) -> (Option<InvocationHandle>, Result<AgentResult, NvokenError>) {
        let mut handle = match self.invoke(input, options.clone()).await {
            Ok(handle) => handle,
            Err(error) => return (None, Err(error)),
        };
        let release_handle = handle.clone();
        match self.settle_by_read(&mut handle, &options).await {
            Ok(result) => (
                Some(release_handle),
                Ok(Self::to_agent_result(handle, result)),
            ),
            Err(error) => (Some(release_handle), Err(error)),
        }
    }

    pub async fn text(
        &self,
        input: impl Into<String>,
        options: AgentInvocationOptions,
    ) -> Result<String, NvokenError> {
        let result = self.run(input, options).await?;
        self.text_from_result(&result)
    }

    fn text_from_result(&self, result: &AgentResult) -> Result<String, NvokenError> {
        if let Some(text) = result.output_text.as_ref().filter(|text| !text.is_empty()) {
            return Ok(text.clone());
        }
        let result_kind = if result.structured_output.is_some() {
            "structured output"
        } else if !self.inner.options.agent_definition.tools.is_empty() {
            "tool-only output"
        } else {
            "no assistant output"
        };
        Err(no_output_text_error(
            &result.handle.invocation_id,
            result_kind,
        ))
    }

    pub async fn stream(
        &self,
        input: impl Into<String>,
        options: AgentInvocationOptions,
    ) -> Result<(InvocationHandle, AgentEventStream), NvokenError> {
        let leave_waiting = options.leave_waiting_on_missing_handler;
        let handle = self.invoke(input, options).await?;
        let agent = self.clone();
        let events_handle = handle.clone();
        let raw_source = handle.clone();
        let generator = try_stream! {
            let mut handle = events_handle;
            let mut submitted: HashSet<String> = HashSet::new();
            let inner = raw_source.stream();
            pin_mut!(inner);
            while let Some(item) = inner.next().await {
                let event = item?;
                yield AgentStreamEvent { handle: handle.clone(), event: event.clone() };
                // A turn that stopped for your tools says so on a change, which
                // replays on reconnect like every other change. The stream ends
                // on the terminal change, so there is nothing else to watch for.
                if waiting_for(&event, &handle.invocation_id) {
                    let invocation = handle.refresh().await?;
                    if invocation.status == models::InvocationStatus::Waiting {
                        agent
                            .dispatch_waiting(&handle, &invocation, &mut submitted, leave_waiting)
                            .await?;
                    }
                }
            }
        };
        Ok((
            handle,
            AgentEventStream {
                inner: Box::pin(generator),
            },
        ))
    }

    /// Binds this Agent to one Session; every call through the returned
    /// `AgentSession` serializes so a Session never admits two concurrent
    /// Invocations from this process.
    pub fn session(&self, binding: SessionBinding) -> Result<AgentSession, NvokenError> {
        if binding.session_id.is_none() == binding.session_key.is_none() {
            return Err(NvokenError::validation(
                "exactly one of session_id or session_key is required",
            ));
        }
        let tenant_key = binding
            .tenant_key
            .or_else(|| self.inner.options.tenant_key.clone());
        let key = match &binding.session_id {
            Some(session_id) => format!("id:{session_id}"),
            None => format!(
                "key:{}:{}",
                tenant_key.as_deref().unwrap_or("default"),
                binding.session_key.as_deref().unwrap_or_default(),
            ),
        };
        let lock = self.client.session_lock(&key);
        Ok(AgentSession {
            agent: self.clone(),
            lock,
            session_id: binding.session_id,
            session_key: binding.session_key,
            tenant_key,
        })
    }

    async fn settle_by_read(
        &self,
        handle: &mut InvocationHandle,
        options: &AgentInvocationOptions,
    ) -> Result<models::InvocationResult, NvokenError> {
        let mut submitted = HashSet::new();
        loop {
            let mut wait_options = options.wait.clone();
            wait_options.until = WaitCondition::Actionable;
            let invocation = handle.wait_with_options(wait_options).await?;
            if invocation.status == models::InvocationStatus::Waiting {
                let dispatched = self
                    .dispatch_waiting(
                        handle,
                        &invocation,
                        &mut submitted,
                        options.leave_waiting_on_missing_handler,
                    )
                    .await?;
                if !dispatched {
                    tokio::time::sleep(Duration::from_millis(50)).await;
                }
                continue;
            }
            if invocation.status != models::InvocationStatus::Completed {
                return Err(invocation_ended_error(&handle.invocation_id, &invocation));
            }
            return handle.result().await;
        }
    }

    /// Answers the host tool calls a parked Invocation is waiting on, without
    /// streaming it.
    ///
    /// This is the unattended path. An Invocation's `webhook` target receives a
    /// signed `invocation.waiting` post when the turn parks, and a worker calls
    /// this to finish it, so a turn makes progress with nobody watching. The
    /// same handlers still serve an attached reader — the first accepted result
    /// per call wins, so the two coexist rather than being a choice made per
    /// deployment.
    ///
    /// Acknowledge the webhook before calling this. Webhook delivery uses
    /// a 10 second request timeout while a host tool budget is minutes, so a
    /// receiver that executes tools inline has its delivery marked failed and
    /// retried while the work is still running. Verify the signature, enqueue,
    /// return 2xx, and call this from the worker.
    ///
    /// Fence side effects with `claim`. First-accepted-result deduplicates the
    /// transcript; it does not stop two processes from both *beginning* a call.
    /// An attached reader and this worker can race, and webhooks are
    /// at-least-once, so two deliveries can race each other.
    ///
    /// Reports how many results were submitted. Zero means the Invocation was
    /// no longer waiting or every call was claimed elsewhere — both ordinary
    /// outcomes rather than errors.
    pub async fn answer_tool_calls(
        &self,
        invocation_id: &str,
        options: AnswerToolCallsOptions,
    ) -> Result<usize, NvokenError> {
        let invocation = self.client.get_invocation(invocation_id).await?;
        if invocation.status != models::InvocationStatus::Waiting {
            return Ok(0);
        }
        let pending_calls = answerable_tool_calls(invocation.tool_calls.as_ref());
        if pending_calls.is_empty() {
            return Ok(0);
        }
        let handle = self.client.invocation(invocation_id);
        let mut results = Vec::new();
        for pending in pending_calls {
            if !runs_locally(pending) {
                continue;
            }
            let handler = self
                .inner
                .host_tools
                .get(&pending.name)
                .and_then(|tool| tool.handler.as_ref());
            let Some(handler) = handler else {
                if options.leave_waiting_on_missing_handler {
                    return Err(missing_unattended_handler(invocation_id, &pending.name));
                }
                continue;
            };
            if let Some(claim) = options.claim.as_ref() {
                if !claim(pending.clone()).await? {
                    continue;
                }
            }
            let input = tool_call_arguments(pending);
            results.push(match handler(input).await {
                Ok(content) => ToolResult {
                    tool_call_id: pending.id.clone(),
                    content,
                    is_error: false,
                },
                Err(error) => ToolResult {
                    tool_call_id: pending.id.clone(),
                    content: json!({"error": error.message, "type": error.type_name}),
                    is_error: true,
                },
            });
        }
        if results.is_empty() {
            return Ok(0);
        }
        let submitted = results.len();
        handle.submit_tool_results(results).await?;
        Ok(submitted)
    }

    /// Dispatches every unresolved pending ToolCall this Agent has a host
    /// handler for. Returns whether anything was submitted; the caller backs
    /// off before polling again when nothing was.
    async fn dispatch_waiting(
        &self,
        handle: &InvocationHandle,
        invocation: &models::Invocation,
        submitted: &mut HashSet<String>,
        leave_waiting: bool,
    ) -> Result<bool, NvokenError> {
        let pending_calls = answerable_tool_calls(invocation.tool_calls.as_ref());
        if pending_calls.is_empty() {
            return Ok(false);
        }
        let mut results = Vec::new();
        for pending in pending_calls {
            if submitted.contains(&pending.id) {
                continue;
            }
            if !runs_locally(pending) {
                continue;
            }
            let handler = self
                .inner
                .host_tools
                .get(&pending.name)
                .and_then(|tool| tool.handler.as_ref());
            let Some(handler) = handler else {
                return Err(self
                    .missing_tool_handler(handle, &pending.name, leave_waiting)
                    .await);
            };
            let input = tool_call_arguments(pending);
            let result = match handler(input).await {
                Ok(content) => ToolResult {
                    tool_call_id: pending.id.clone(),
                    content,
                    is_error: false,
                },
                Err(error) => ToolResult {
                    tool_call_id: pending.id.clone(),
                    content: json!({"error": error.message, "type": error.type_name}),
                    is_error: true,
                },
            };
            results.push(result);
        }
        if results.is_empty() {
            return Ok(false);
        }
        let submitted_ids: Vec<String> = results.iter().map(|r| r.tool_call_id.clone()).collect();
        handle.submit_tool_results(results).await?;
        submitted.extend(submitted_ids);
        Ok(true)
    }

    async fn missing_tool_handler(
        &self,
        handle: &InvocationHandle,
        tool_name: &str,
        leave_waiting: bool,
    ) -> NvokenError {
        let mut cancelled = false;
        let mut cancel_error = None;
        if !leave_waiting {
            match handle.cancel().await {
                Ok(_) => cancelled = true,
                Err(error) => cancel_error = Some(error.message),
            }
        }
        let mut details = json!({
            "invocation_id": handle.invocation_id,
            "tool_name": tool_name,
            "invocation_cancelled": cancelled,
        });
        if let Some(error) = cancel_error {
            details["cancel_error"] = json!(error);
        }
        NvokenError {
            category: ErrorCategory::Conflict,
            message: format!(
                "Invocation {} is waiting for unhandled tool {tool_name:?} and was {}",
                handle.invocation_id,
                if cancelled {
                    "cancelled"
                } else {
                    "left waiting"
                },
            ),
            status: None,
            code: Some("missing_tool_handler".to_owned()),
            request_id: None,
            retry_after: None,
            details: Some(details),
        }
    }

    fn to_agent_result(handle: InvocationHandle, result: models::InvocationResult) -> AgentResult {
        let invocation = (*result.invocation).clone();
        let messages = result.messages.clone();
        let output_text = result.output_text.clone();
        let structured_output = invocation.structured_output.clone();
        AgentResult {
            handle,
            invocation,
            messages,
            output_text,
            structured_output,
            raw: result,
        }
    }
}

fn no_output_text_error(invocation_id: &str, result_kind: &str) -> NvokenError {
    NvokenError {
        category: ErrorCategory::UnexpectedResponse,
        message: format!("Invocation {invocation_id} completed with {result_kind}, not text"),
        status: None,
        code: Some("no_output_text".to_owned()),
        request_id: None,
        retry_after: None,
        details: Some(json!({
            "invocation_id": invocation_id,
            "result_kind": result_kind,
        })),
    }
}

/// The unattended counterpart to `missing_tool_handler`. It never cancels: a
/// worker answering one tool of several has no business settling a turn whose
/// other calls belong to somebody else, which is why skipping is the default
/// and this error is opt-in.
fn missing_unattended_handler(invocation_id: &str, tool_name: &str) -> NvokenError {
    NvokenError {
        category: ErrorCategory::Conflict,
        message: format!("Invocation {invocation_id} is waiting for unhandled tool {tool_name:?}"),
        status: None,
        code: Some("missing_tool_handler".to_owned()),
        request_id: None,
        retry_after: None,
        details: Some(json!({
            "invocation_id": invocation_id,
            "tool_name": tool_name,
            "invocation_cancelled": false,
        })),
    }
}

/// Explains an ending that was not the answer asked for. An `Incomplete` turn
/// carries no error, so its stop reason is the only thing that names the budget
/// that stopped it.
fn invocation_ended_error(invocation_id: &str, invocation: &models::Invocation) -> NvokenError {
    let mut error = NvokenError {
        category: ErrorCategory::Conflict,
        message: match invocation.stop_reason {
            Some(reason) => format!(
                "Invocation {invocation_id} ended with status {} ({reason})",
                invocation.status
            ),
            None => format!(
                "Invocation {invocation_id} ended with status {}",
                invocation.status
            ),
        },
        status: None,
        code: None,
        request_id: None,
        retry_after: None,
        details: None,
    };
    if let Some(failure) = &invocation.error {
        error.code = serde_json::to_value(failure.code)
            .ok()
            .and_then(|value| value.as_str().map(str::to_owned));
        error.details = failure.details.clone().map(|value| json!(value));
    }
    error
}

/// Names the Session an `Agent::session` call binds to. Exactly one of
/// `session_id` or `session_key` is required.
#[derive(Clone, Default)]
pub struct SessionBinding {
    pub session_id: Option<String>,
    pub session_key: Option<String>,
    pub tenant_key: Option<String>,
}

impl SessionBinding {
    pub fn by_id(session_id: impl Into<String>) -> Self {
        Self {
            session_id: Some(session_id.into()),
            session_key: None,
            tenant_key: None,
        }
    }

    pub fn by_key(session_key: impl Into<String>) -> Self {
        Self {
            session_id: None,
            session_key: Some(session_key.into()),
            tenant_key: None,
        }
    }

    pub fn tenant_key(mut self, tenant_key: impl Into<String>) -> Self {
        self.tenant_key = Some(tenant_key.into());
        self
    }
}

/// An Agent bound to one Session. Every call serializes on a lock held from
/// admission until the admitted Invocation reaches a terminal state, so a
/// Session bound this way never admits two concurrent Invocations from this
/// process regardless of how many `AgentSession` handles reference it.
#[derive(Clone)]
pub struct AgentSession {
    agent: Agent,
    lock: Arc<tokio::sync::Mutex<()>>,
    session_id: Option<String>,
    session_key: Option<String>,
    tenant_key: Option<String>,
}

impl AgentSession {
    fn bind(&self, options: &mut AgentInvocationOptions) -> Result<(), NvokenError> {
        if options.session_id.is_some() || options.session_key.is_some() {
            return Err(NvokenError::validation(
                "bound Session calls cannot override their Session",
            ));
        }
        options.tenant_key = self.tenant_key.clone();
        options.session_id = self.session_id.clone();
        options.session_key = self.session_key.clone();
        Ok(())
    }

    pub async fn invoke(
        &self,
        input: impl Into<String>,
        mut options: AgentInvocationOptions,
    ) -> Result<InvocationHandle, NvokenError> {
        self.bind(&mut options)?;
        let guard = self.lock.clone().lock_owned().await;
        match self.agent.invoke(input, options.clone()).await {
            Ok(handle) => {
                self.spawn_release(handle.clone(), options.wait, guard);
                Ok(handle)
            }
            Err(error) => {
                drop(guard);
                Err(error)
            }
        }
    }

    pub async fn run(
        &self,
        input: impl Into<String>,
        mut options: AgentInvocationOptions,
    ) -> Result<AgentResult, NvokenError> {
        self.bind(&mut options)?;
        let guard = self.lock.clone().lock_owned().await;
        let (handle, result) = self.agent.run_with_handle(input, options.clone()).await;
        match handle {
            Some(handle) => self.spawn_release(handle, options.wait, guard),
            None => drop(guard),
        }
        result
    }

    pub async fn text(
        &self,
        input: impl Into<String>,
        options: AgentInvocationOptions,
    ) -> Result<String, NvokenError> {
        let result = self.run(input, options).await?;
        self.agent.text_from_result(&result)
    }

    pub async fn stream(
        &self,
        input: impl Into<String>,
        mut options: AgentInvocationOptions,
    ) -> Result<(InvocationHandle, AgentEventStream), NvokenError> {
        self.bind(&mut options)?;
        let guard = self.lock.clone().lock_owned().await;
        match self.agent.stream(input, options.clone()).await {
            Ok((handle, events)) => {
                self.spawn_release(handle.clone(), options.wait, guard);
                Ok((handle, events))
            }
            Err(error) => {
                drop(guard);
                Err(error)
            }
        }
    }

    fn spawn_release(
        &self,
        mut handle: InvocationHandle,
        wait: WaitOptions,
        guard: tokio::sync::OwnedMutexGuard<()>,
    ) {
        tokio::spawn(async move {
            let mut options = wait;
            options.timeout = None;
            options.until = WaitCondition::Terminal;
            loop {
                match handle.wait_with_options(options.clone()).await {
                    Ok(_) => break,
                    Err(_) => tokio::time::sleep(Duration::from_secs(1)).await,
                }
            }
            drop(guard);
        });
    }
}

/// The tool calls this caller is expected to run.
///
/// There is one tool-call collection. A call you have to answer is the one
/// carrying the arguments to answer it with; builtin and MCP calls nvoken runs
/// itself, and calls that have already settled, carry none. Filtering on that
/// is what replaced the separate pending list.
pub fn answerable_tool_calls(
    tool_calls: Option<&Vec<models::ToolCallSummary>>,
) -> Vec<&models::ToolCallSummary> {
    tool_calls
        .map(|calls| {
            calls
                .iter()
                .filter(|call| call.arguments.is_some())
                .collect()
        })
        .unwrap_or_default()
}

/// The tool calls this caller must run itself.
///
/// Answerable is not the same as yours. Once an App declares callback tools,
/// nvoken delivers those to an endpoint — but a machine credential may still
/// settle one after its receiver acknowledged delivery, so a pending
/// callback-mode call carries arguments and is answerable. Running it here as
/// well would double the side effect.
///
/// Yours is answerable and `mode` is `host`. Partitioning on that beats keeping
/// a list of your own tool names, which goes stale the first time an agent
/// gains a tool and nobody updates the list.
pub fn host_tool_calls(
    tool_calls: Option<&Vec<models::ToolCallSummary>>,
) -> Vec<&models::ToolCallSummary> {
    answerable_tool_calls(tool_calls)
        .into_iter()
        .filter(|call| call.mode == models::ToolCallMode::Host)
        .collect()
}

fn tool_call_arguments(call: &models::ToolCallSummary) -> Value {
    match call.arguments.as_ref() {
        Some(arguments) => Value::Object(arguments.clone().into_iter().collect()),
        None => Value::Object(serde_json::Map::new()),
    }
}

/// Whether a `transcript.update` carries a change parking this turn on your
/// tools.
fn waiting_for(event: &StreamEvent, invocation_id: &str) -> bool {
    if event.event_type != "transcript.update" {
        return false;
    }
    event
        .data
        .get("invocation_changes")
        .and_then(|changes| changes.as_array())
        .is_some_and(|changes| {
            changes.iter().any(|change| {
                change.get("invocation_id").and_then(|value| value.as_str()) == Some(invocation_id)
                    && change.get("status").and_then(|value| value.as_str()) == Some("waiting")
            })
        })
}
