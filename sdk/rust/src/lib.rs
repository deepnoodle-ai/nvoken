pub mod agent;
pub mod apis;
pub mod ask_user;
pub mod callback;
pub mod client;
pub mod media_preflight;
#[allow(unused_imports)]
pub mod models;
pub mod routes;
pub mod schema_preflight;
pub mod stream;

/// The released version of the Rust SDK.
pub const VERSION: &str = env!("CARGO_PKG_VERSION");

pub use agent::{
    Agent, AgentEventStream, AgentInvocationOptions, AgentOptions, AgentResult, AgentSession,
    AgentStreamEvent, AnswerToolCallsOptions, SessionBinding, ToolCallClaim,
};
pub use ask_user::{
    ask_user_input_schema, ask_user_tool, ask_user_tool_with, AskUserInput, AskUserKind,
    AskUserOption, AskUserOutput, ASK_USER_DESCRIPTION, ASK_USER_TOOL_NAME,
};
pub use callback::{
    acknowledge_callback, callback_result, deduplicate_callback_result, verify_callback,
    CallbackEnvelope, CallbackError, CallbackReply, CallbackResultStore, VerifiedCallback,
};
pub use client::{
    fetch_tool, AgentDefinition, BudgetExhaustionBehavior, Client, CompactionListOptions,
    ContextCompaction, ContextCompactionTrigger, ContextItem, ContextTier, ErrorCategory,
    HostToolHandler, IfActivePolicy, InvocationHandle, InvokeRequest, Limits,
    ListAgentDefinitionsOptions, ListAgentsOptions, ListInvocationsOptions, ListModelsOptions,
    ListSessionsOptions, McpServer, McpServerHeaders, MessageListOptions, Model, NvokenError,
    ProviderKeySelection, ProviderKeySource, ProviderTool, Reasoning, ReasoningEffort, RetryPolicy,
    Sampling, SessionOptions, SessionRetention, StreamOptions, Tool, ToolCallListOptions,
    ToolChoice, ToolHandlerError, ToolMode, ToolResult, WaitCondition, WaitOptions,
    WebSearchLocation, WebSearchTool, WebhookEvent, WebhookTarget,
};
pub use media_preflight::{
    document_block, document_url_block, image_block, image_url_block, preflight_input_blocks,
    text_block, MediaIssue,
};
pub use schema_preflight::{preflight_output_schema, SchemaIssue};
pub use stream::{ReducedSnapshot, Reducer, StreamEvent, StreamPreview};
