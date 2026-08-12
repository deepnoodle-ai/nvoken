package nvoken

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

type Invocation = generated.Invocation
type InvocationResult = generated.InvocationResult
type InvocationStatus = generated.InvocationStatus
type InvocationStopReason = generated.InvocationStopReason
type App = generated.App
type AppList = generated.AppList
type AppRegistration = generated.AppRegistration
type AppSigningKeyPurpose = generated.AppSigningKeyPurpose
type AppSigningKeySecret = generated.AppSigningKeySecret
type ArchiveStatus = generated.ArchiveStatus
type ClientKey = generated.ClientKey
type ClientKeyList = generated.ClientKeyList
type AgentIdentity = generated.Agent
type AgentDefinitionResourceList = generated.AgentDefinitionResourceList
type Session = generated.Session
type SessionContext = generated.SessionContext
type SessionMessage = generated.SessionMessage
type SessionCompaction = generated.SessionCompaction
type MessagePhase = generated.MessagePhase
type SeedMessageRole = generated.SeedMessageRole
type PendingHostToolCall = generated.PendingHostToolCall
type ToolResultResponse = generated.SubmitHostToolResultsResponse
type ModelProvider = generated.ModelProvider
type ModelDescriptor = generated.ModelDescriptor
type ModelPricing = generated.ModelPricing
type ProviderKey = generated.ProviderKey
type ProviderKeyUsage = generated.ProviderKeyUsage
type CreditAccount = generated.CreditAccount
type CreditAccountList = generated.CreditAccountList
type CreditAllocation = generated.CreditAllocation
type CreditAllocationList = generated.CreditAllocationList
type AllocateCreditsResult = generated.AllocateCreditsResult
type ListCreditAccountsParams = generated.ListCreditAccountsParams
type ListCreditAllocationsParams = generated.ListCreditAllocationsParams
type InvocationTimeline = generated.InvocationTimeline
type InvocationTimelineStep = generated.InvocationTimelineStep
type InvocationTimelineStepKind = generated.InvocationTimelineStepKind
type ObservationStatus = generated.ObservationStatus
type TraceSummary = generated.TraceSummary
type TraceList = generated.TraceList
type TraceSpan = generated.TraceSpan
type Trace = generated.Trace
type InvocationLog = generated.InvocationLog
type InvocationLogList = generated.InvocationLogList
type AdmissionAttempt = generated.AdmissionAttempt
type AdmissionAttemptList = generated.AdmissionAttemptList
type AdmissionOutcome = generated.AdmissionOutcome
type AdmissionSummary = generated.AdmissionSummary
type ListAdmissionsParams = generated.ListAdmissionsParams
type SummarizeAdmissionsParams = generated.SummarizeAdmissionsParams
type Tenant = generated.Tenant
type TenantList = generated.TenantList
type ListTenantsParams = generated.ListTenantsParams
type Org = generated.Org
type OrgList = generated.OrgList
type UsageBreakdown = generated.UsageBreakdown
type UsageBreakdownItem = generated.UsageBreakdownItem
type ActivityMetrics = generated.ActivityMetrics
type CostMetrics = generated.CostMetrics
type ModelMetrics = generated.ModelMetrics
type ToolMetrics = generated.ToolMetrics
type Money = generated.Money
type ModelCallRecord = generated.ModelCallRecord
type UsageMetrics = generated.UsageMetrics
type UsageRecords = generated.UsageRecords
type UsageTimeseries = generated.UsageTimeseries
type UsageTimeseriesBucket = generated.UsageTimeseriesBucket
type UsageInterval = generated.UsageInterval
type GetUsageBreakdownParams = generated.GetUsageBreakdownParams
type GetUsageBreakdownParamsGroupBy = generated.GetUsageBreakdownParamsGroupBy
type GetUsageBreakdownParamsSort = generated.GetUsageBreakdownParamsSort
type ListUsageRecordsParams = generated.ListUsageRecordsParams
type ListUsageRecordsParamsFormat = generated.ListUsageRecordsParamsFormat
type GetUsageTimeseriesParams = generated.GetUsageTimeseriesParams
type GetUsageTimeseriesParamsGroupBy = generated.GetUsageTimeseriesParamsGroupBy
type ProviderKeyScope = generated.ProviderKeyScope
type ProviderKeyStatus = generated.ProviderKeyStatus
type InvocationChange = generated.InvocationChange
type MCPListToolsResponse = generated.MCPListToolsResponse
type MCPProjectedTool = generated.MCPProjectedTool
type MCPToolExclusion = generated.MCPToolExclusion
type Nudge = generated.Nudge
type NudgeStatus = generated.NudgeStatus
type NudgeAcknowledgement = generated.NudgeAcknowledgement
type ToolCall = generated.ToolCall
type ToolCallDelivery = generated.ToolCallDelivery
type ToolCallMode = generated.ToolCallMode
type ToolCallStatus = generated.ToolCallStatus
type CallbackDeliveryOutcome = generated.CallbackDeliveryOutcome
type Credential = generated.Credential
type CredentialList = generated.CredentialList
type CredentialProfile = generated.CredentialProfile
type CredentialStatus = generated.CredentialStatus
type CurrentIdentity = generated.CurrentIdentity
type RuntimeOperation = generated.Operation

type CredentialIssuance struct {
	Credential        Credential
	Secret            string
	DeliveryExpiresAt time.Time
	Replayed          bool
}

const (
	InvocationQueued                             = generated.InvocationStatusQueued
	InvocationRunning                            = generated.InvocationStatusRunning
	InvocationWaiting                            = generated.InvocationStatusWaiting
	InvocationPaused                             = generated.InvocationStatusPaused
	InvocationCompleted                          = generated.InvocationStatusCompleted
	InvocationIncomplete                         = generated.InvocationStatusIncomplete
	InvocationFailed                             = generated.InvocationStatusFailed
	InvocationCancelled                          = generated.InvocationStatusCancelled
	StopReasonEndTurn                            = generated.InvocationStopReasonEndTurn
	StopReasonInterrupted                        = generated.InvocationStopReasonInterrupted
	StopReasonMaxIterations                      = generated.InvocationStopReasonMaxIterations
	StopReasonDeadline                           = generated.InvocationStopReasonDeadline
	StopReasonMaxOutputTokens                    = generated.InvocationStopReasonMaxOutputTokens
	StopReasonMaxEstimatedCost                   = generated.InvocationStopReasonMaxEstimatedCost
	StopReasonInsufficientCredits                = generated.InvocationStopReasonInsufficientCredits
	MessagePhaseCommentary                       = generated.Commentary
	MessagePhaseFinalAnswer                      = generated.FinalAnswer
	SeedMessageRoleUser                          = generated.SeedMessageRoleUser
	SeedMessageRoleAssistant                     = generated.SeedMessageRoleAssistant
	ModelProviderAnthropic         ModelProvider = "anthropic"
	ModelProviderOpenAI            ModelProvider = "openai"
	ModelProviderXAI               ModelProvider = "xai"
	ModelProviderGoogle            ModelProvider = "google"
	ProviderKeyScopeApp                          = generated.ProviderKeyScopeApp
	ProviderKeyScopeTenant                       = generated.ProviderKeyScopeTenant
	ProviderKeyStatusActive                      = generated.ProviderKeyStatusActive
	ProviderKeyStatusRevoked                     = generated.ProviderKeyStatusRevoked
	NudgePending                                 = generated.NudgeStatusPending
	NudgeDrained                                 = generated.NudgeStatusDrained
	NudgeExpired                                 = generated.NudgeStatusExpired
	NudgeCancelled                               = generated.NudgeStatusCancelled
	UsageIntervalDay                             = generated.Day
	UsageIntervalWeek                            = generated.Week
	UsageIntervalMonth                           = generated.Month
	CredentialProfileRuntime                     = generated.CredentialProfileRuntime
	CredentialProfileViewer                      = generated.CredentialProfileViewer
	CredentialProfileOperator                    = generated.CredentialProfileOperator
	CredentialStatusActive                       = generated.CredentialStatusActive
	CredentialStatusRevoked                      = generated.CredentialStatusRevoked
	ArchiveStatusActive                          = generated.ArchiveStatusActive
	ArchiveStatusAll                             = generated.ArchiveStatusAll
	ArchiveStatusArchived                        = generated.ArchiveStatusArchived
	OperationCreateInvocation                    = generated.CreateInvocation
	OperationCreateSession                       = generated.CreateSession
	OperationGetAgent                            = generated.GetAgent
	OperationListAgents                          = generated.ListAgents
	OperationGetInvocation                       = generated.GetInvocation
	OperationSubmitToolResults                   = generated.SubmitToolResults
	OperationCancelInvocation                    = generated.CancelInvocation
	OperationResumeInvocation                    = generated.ResumeInvocation
	OperationListInvocations                     = generated.ListInvocations
	OperationGetSession                          = generated.GetSession
	OperationUpdateSession                       = generated.UpdateSession
	OperationDeleteSession                       = generated.DeleteSession
	OperationListSessions                        = generated.ListSessions
	OperationListSessionMessages                 = generated.ListSessionMessages
	OperationGetSessionTranscript                = generated.GetSessionTranscript
	OperationGetIdentity                         = generated.GetIdentity
	OperationListCredentials                     = generated.ListCredentials
	OperationCreateCredential                    = generated.CreateCredential
	OperationGetCredential                       = generated.GetCredential
	OperationRotateCredential                    = generated.RotateCredential
	OperationRevokeCredential                    = generated.RevokeCredential
	OperationListProviderKeys                    = generated.ListProviderKeys
	OperationCreateProviderKey                   = generated.CreateProviderKey
	OperationGetProviderKey                      = generated.GetProviderKey
	OperationRotateProviderKey                   = generated.RotateProviderKey
	OperationRevokeProviderKey                   = generated.RevokeProviderKey
	OperationReadUsage                           = generated.ReadUsage
	OperationReadCredits                         = generated.ReadCredits
	OperationAllocateCredits                     = generated.AllocateCredits
	OperationCreateAgentDefinition               = generated.CreateAgentDefinition
	OperationListAgentDefinitions                = generated.ListAgentDefinitions
	OperationGetAgentDefinition                  = generated.GetAgentDefinition
	OperationUpdateAgentDefinition               = generated.UpdateAgentDefinition
	OperationRegisterApp                         = generated.RegisterApp
	OperationListApps                            = generated.ListApps
	OperationGetApp                              = generated.GetApp
	OperationUpdateApp                           = generated.UpdateApp
	OperationGetOrg                              = generated.GetOrg
	OperationListOrgs                            = generated.ListOrgs
	OperationRegisterOrg                         = generated.RegisterOrg
	OperationUpdateOrg                           = generated.UpdateOrg
)

type ModelList struct {
	CatalogVersion string            `json:"catalog_version"`
	Items          []ModelDescriptor `json:"items"`
}

type InvocationList struct {
	HasMore    bool         `json:"has_more"`
	Items      []Invocation `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

type AgentList struct {
	HasMore    bool            `json:"has_more"`
	Items      []AgentIdentity `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

type SessionList struct {
	HasMore    bool      `json:"has_more"`
	Items      []Session `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}

type SessionMessageList struct {
	HasMore    bool             `json:"has_more"`
	Items      []SessionMessage `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

type SessionCompactionList struct {
	HasMore    bool                `json:"has_more"`
	Items      []SessionCompaction `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

type NudgeList = generated.NudgeList

type ToolCallList = generated.ToolCallList

// ListToolCallsOptions pages durable execution records in discovery order.
type ListToolCallsOptions struct {
	Cursor *string
	Limit  *int
}

// ObservationListOptions pages hosted traces or logs associated with one
// Invocation. The service reports a disabled status when no observation store
// is configured.
type ObservationListOptions struct {
	Cursor *string
	Limit  *int
	// TraceID applies only to Invocation log lists and narrows the page to
	// records correlated with one trace.
	TraceID *string
}

// ListNudgesOptions filters and pages the staged-input queue. A nil
// Status returns every status, in the order the turn consumes them.
type ListNudgesOptions struct {
	Status *NudgeStatus
	Cursor *string
	Limit  *int
}

// NudgeRequest is steering appended to a running Invocation. Content is text:
// staged input reaches the model through seams that carry text, so image and
// document blocks belong on an Invocation's own input rather than here.
//
// IdempotencyKey makes a retry safe: the same key with the same content
// returns the original acknowledgement with Deduplicated set, and the same key with
// different content is refused rather than silently reused.
type NudgeRequest struct {
	Content        string
	IdempotencyKey string
}

type ProviderKeyList struct {
	HasMore    bool          `json:"has_more"`
	Items      []ProviderKey `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type AllocateCreditsInput struct {
	Amount         Money
	TenantKey      *string
	DefaultTenant  *bool
	Reference      *string
	IdempotencyKey string
}

type CreateAppClientKeyInput struct {
	Name      string
	PublicKey []byte
}

type TranscriptSnapshot struct {
	HasMore           bool               `json:"has_more"`
	InvocationChanges []InvocationChange `json:"invocation_changes"`
	Messages          []SessionMessage   `json:"messages"`
	NextPageToken     *string            `json:"next_page_token"`
	ResumeCursor      string             `json:"resume_cursor"`
}

type TranscriptDrain struct {
	InvocationChanges []InvocationChange `json:"invocation_changes"`
	Messages          []SessionMessage   `json:"messages"`
	ResumeCursor      string             `json:"resume_cursor"`
}

type Model struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type Limits struct {
	TotalTimeoutSeconds   *int     `json:"total_timeout_seconds,omitempty"`
	ActiveTimeoutSeconds  *int     `json:"active_timeout_seconds,omitempty"`
	WaitingTimeoutSeconds *int     `json:"waiting_timeout_seconds,omitempty"`
	MaxOutputTokens       *int     `json:"max_output_tokens,omitempty"`
	MaxEstimatedCostUSD   *float32 `json:"max_estimated_cost_usd,omitempty"`
	MaxIterations         *int     `json:"max_iterations,omitempty"`
}

type Sampling struct {
	Temperature float64 `json:"temperature"`
}

type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
	ReasoningEffortXHigh  ReasoningEffort = "xhigh"
	ReasoningEffortMax    ReasoningEffort = "max"
)

type Reasoning struct {
	Effort       *ReasoningEffort `json:"effort,omitempty"`
	BudgetTokens *int             `json:"budget_tokens,omitempty"`
}

type ToolChoiceMode string

const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceNamed    ToolChoiceMode = "named"
)

type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode"`
	Name string         `json:"name,omitempty"`
}

type ContextCompactionTrigger struct {
	auto   bool
	tokens int
}

func AutoContextCompaction() ContextCompactionTrigger {
	return ContextCompactionTrigger{auto: true}
}

func ContextCompactionAt(tokens int) ContextCompactionTrigger {
	return ContextCompactionTrigger{tokens: tokens}
}

func (t ContextCompactionTrigger) MarshalJSON() ([]byte, error) {
	if t.auto {
		return json.Marshal("auto")
	}
	if t.tokens <= 0 {
		return nil, fmt.Errorf("context compaction trigger must be auto or positive")
	}
	return json.Marshal(t.tokens)
}

func (t *ContextCompactionTrigger) UnmarshalJSON(data []byte) error {
	var auto string
	if err := json.Unmarshal(data, &auto); err == nil {
		if auto != "auto" {
			return fmt.Errorf("context compaction trigger string must be auto")
		}
		*t = AutoContextCompaction()
		return nil
	}
	var tokens int
	if err := json.Unmarshal(data, &tokens); err != nil || tokens <= 0 {
		return fmt.Errorf("context compaction trigger must be auto or positive")
	}
	*t = ContextCompactionAt(tokens)
	return nil
}

type ContextCompaction struct {
	TriggerTokens ContextCompactionTrigger `json:"trigger_tokens"`
	Model         *Model                   `json:"model,omitempty"`
}

// SessionOptions carries durable Session options. Every member is optional and
// at least one must be present. Existing values are comparison-only: equal is
// accepted and different returns session_options_conflict.
type SessionOptions struct {
	// Compaction requires an Invocation because the policy is validated against
	// that turn's model. It may be installed on any Session that has no policy
	// yet, but CreateSession still cannot set it.
	Compaction *ContextCompaction `json:"compaction,omitempty"`
	Retention  *SessionRetention  `json:"retention,omitempty"`
	Metadata   map[string]string  `json:"metadata,omitempty"`
}

// generated converts creation-only options for POST /v1/sessions. Compaction is
// refused here rather than encoded: the runtime rejects it on a Session created
// without an Invocation, because the policy is validated against that
// Invocation's model, and a rejection naming the reason beats a round trip that
// comes back saying the same thing.
func (o *SessionOptions) generated() (*generated.SessionOptions, error) {
	if o.Compaction != nil {
		return nil, fmt.Errorf(
			"compaction requires an Invocation to validate its model against: " +
				"set it on an Invocation admission for the Session")
	}
	if o.Retention == nil && len(o.Metadata) == 0 {
		return nil, fmt.Errorf("session options require at least one member")
	}
	options := &generated.SessionOptions{}
	if o.Retention != nil {
		options.Retention = &generated.RetentionPolicy{TTLSeconds: o.Retention.TTLSeconds}
	}
	if len(o.Metadata) > 0 {
		metadata := generated.Metadata(o.Metadata)
		options.Metadata = &metadata
	}
	return options, nil
}

func (o *SessionOptions) generatedFork() (*generated.ForkSessionOptions, error) {
	if o.Compaction != nil {
		return nil, fmt.Errorf("forked Sessions start uncompacted; set compaction on the child Session's first invocation")
	}
	if o.Retention == nil && len(o.Metadata) == 0 {
		return nil, fmt.Errorf("session options require at least one member")
	}
	options := &generated.ForkSessionOptions{}
	if o.Retention != nil {
		options.Retention = &generated.RetentionPolicy{TTLSeconds: o.Retention.TTLSeconds}
	}
	if len(o.Metadata) > 0 {
		metadata := generated.Metadata(o.Metadata)
		options.Metadata = &metadata
	}
	return options, nil
}

// SessionRetention bounds how long an idle Session is retained. The window
// measures idle time rather than lifetime: it restarts on every Invocation
// acceptance and completion, so a turn outlasting the window cannot expire
// underneath itself. Automatic expiry never cancels running work.
type SessionRetention struct {
	// TTLSeconds is the idle window, from one hour to thirty days.
	TTLSeconds int `json:"ttl_seconds"`
}

type ToolMode string

const (
	ToolModeBuiltin  ToolMode = "builtin"
	ToolModeHost     ToolMode = "host"
	ToolModeCallback ToolMode = "callback"
)

type ToolHandler func(context.Context, any) (any, error)

type Tool struct {
	Mode        ToolMode        `json:"mode"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"input_schema"`
	Callback    *CallbackTarget `json:"callback,omitempty"`
	Handler     ToolHandler     `json:"-"`
}

func FetchTool() Tool {
	return Tool{
		Mode: ToolModeBuiltin,
		Name: "nvoken_fetch",
	}
}

func (t Tool) MarshalJSON() ([]byte, error) {
	if t.Mode == ToolModeBuiltin {
		return json.Marshal(struct {
			Mode ToolMode `json:"mode"`
			Name string   `json:"name"`
		}{
			Mode: t.Mode,
			Name: t.Name,
		})
	}
	type toolWire Tool
	return json.Marshal(toolWire(t))
}

type CallbackTarget struct {
	URL string `json:"url"`
}

type MCPTimeouts struct {
	DiscoverySeconds *int `json:"discovery_seconds,omitempty"`
	CallSeconds      *int `json:"call_seconds,omitempty"`
}

// MCPServer declares a remote MCP server. It carries no secrets: an Agent
// Definition may be shared across turns, so authentication
// headers travel per Invocation in MCPServerHeaders instead.
type MCPServer struct {
	Name         string       `json:"name"`
	URL          string       `json:"url"`
	Transport    string       `json:"transport,omitempty"`
	AllowedTools []string     `json:"allowed_tools,omitempty"`
	Timeouts     *MCPTimeouts `json:"timeouts,omitempty"`
}

// MCPServerHeaders carries the secret headers for one MCP server named by the
// selected Agent Definition. They are encrypted for a single turn, and are
// never stored in, hashed into, or returned with the Agent Definition.
type MCPServerHeaders struct {
	Name    string            `json:"name"`
	Headers map[string]string `json:"headers"`
}

// ContextTier controls how a recorded snapshot reaches the model. Use
// ContextTierContextual for conversation-adjacent facts and ContextTierOperator
// for policy or other application-authoritative state. The tier stays typed in
// the transcript; the provider-native role is chosen when the turn generates.
type ContextTier string

const (
	ContextTierContextual ContextTier = "contextual"
	ContextTierOperator   ContextTier = "operator"
)

func (t ContextTier) valid() bool {
	return t == ContextTierContextual || t == ContextTierOperator
}

// ContextItem is one application-owned state snapshot recorded ahead of a
// turn's input. Name is a stable identity: sending it again supersedes the
// earlier value, and an unchanged resend adds no transcript message, so a
// stateless host may resend its whole snapshot every turn. Omit the reserved
// "app-" prefix the model sees; nvoken adds it. Context is durable Session
// history rather than an Agent Definition field, so it never changes the
// admitted Agent Definition revision.
type ContextItem struct {
	Name    string      `json:"name"`
	Tier    ContextTier `json:"tier"`
	Content string      `json:"content"`
}

// ProviderTool selects one provider server-side tool. Web search is Anthropic
// only for now, and a model that does not declare controls.tools.web_search is
// refused at admission rather than served a search the provider would ignore.
type ProviderTool struct {
	Type      string         `json:"type"`
	WebSearch *WebSearchTool `json:"web_search,omitempty"`
}

const ProviderToolWebSearch = "web_search"

// WebSearchTool carries Anthropic's web search options, passed through as the
// provider defines them.
type WebSearchTool struct {
	// MaxUses bounds searches this turn may run, 1 to 20. It is the only bound
	// nvoken can place on search spend: the provider reports no per-search fee
	// it could meter, so search charges ride the provider's bill outside
	// nvoken's cost estimate and usage reporting.
	MaxUses int `json:"max_uses,omitempty"`
	// AllowedDomains restricts results to these hosts. Bare hostnames only —
	// a scheme, path, or port is rejected rather than reinterpreted. Mutually
	// exclusive with BlockedDomains, which is the provider's rule.
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
	// UserLocation biases results. Every member is optional; the host decides
	// how precise to be about its end user.
	UserLocation *WebSearchLocation `json:"user_location,omitempty"`
}

type WebSearchLocation struct {
	City     string `json:"city,omitempty"`
	Region   string `json:"region,omitempty"`
	Country  string `json:"country,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

// WebSearchTool declares Anthropic server-side web search with default options.
func WebSearchProviderTool() ProviderTool {
	return ProviderTool{Type: ProviderToolWebSearch, WebSearch: &WebSearchTool{}}
}

// AgentDefinition is the execution configuration a turn runs with. It may be
// sent inline on an Invocation or used to create a reusable App-owned resource.
type AgentDefinition struct {
	Instructions  string         `json:"instructions,omitempty"`
	Model         Model          `json:"model"`
	Sampling      *Sampling      `json:"sampling,omitempty"`
	Reasoning     *Reasoning     `json:"reasoning,omitempty"`
	ToolChoice    *ToolChoice    `json:"tool_choice,omitempty"`
	Limits        *Limits        `json:"limits,omitempty"`
	Tools         []Tool         `json:"tools,omitempty"`
	MCPServers    []MCPServer    `json:"mcp_servers,omitempty"`
	ProviderTools []ProviderTool `json:"provider_tools,omitempty"`
	OutputSchema  map[string]any `json:"output_schema,omitempty"`
}

type AgentDefinitionResource = generated.AgentDefinitionResource

type CreateAgentDefinitionInput struct {
	Definition     AgentDefinition
	IdempotencyKey string
}

type UpdateAgentDefinitionInput struct {
	Definition       AgentDefinition
	ExpectedRevision int64
}

type InvokeRequest struct {
	AgentKey  string
	TenantKey *string
	// UserKey labels the host end user who admitted this turn. The Session keeps
	// the opener's label, while each Invocation and its messages keep this one.
	// Filtering only; not an isolation boundary.
	UserKey           *string
	SessionID         *string
	SessionKey        *string
	SessionOptions    *SessionOptions
	IdempotencyKey    string
	IfActive          IfActivePolicy
	OnBudgetExhausted BudgetExhaustionBehavior
	Input             string
	// InputBlocks carries ordered multi-block input mixing text, images, and
	// documents. Supply exactly one of Input and InputBlocks.
	InputBlocks []InputBlock
	// AgentDefinition sends the complete execution configuration inline for
	// this turn. Supply exactly one of AgentDefinition and AgentDefinitionID.
	AgentDefinition *AgentDefinition
	// AgentDefinitionID selects an App-owned Agent Definition resource. The
	// current revision is pinned when this Invocation is admitted. Supply
	// exactly one of AgentDefinition and AgentDefinitionID.
	AgentDefinitionID string
	// MCPServerHeaders carries per-turn secret headers, keyed to MCP server
	// names in the selected Agent Definition. They live here rather than on
	// MCPServer because an Agent Definition may be reused.
	MCPServerHeaders []MCPServerHeaders
	ProviderKeys     []ProviderKeySelection
	// Context carries ordered application state snapshots recorded before this
	// turn's input. It is order-sensitive and material to idempotency: a replay
	// that reorders or edits an item conflicts rather than updating it.
	Context []ContextItem
	// Metadata is opaque host correlation data recorded on this Invocation. It
	// is part of the admitted input, so it is immutable and material to
	// idempotency: a replay carrying different metadata conflicts rather than
	// updating it. Session metadata is separate and mutable — see
	// SessionOptions.Metadata and Client.UpdateSession.
	Metadata map[string]string
	// Webhook is the optional endpoint nvoken posts a signed webhook to
	// when this Invocation parks awaiting host tool results or settles. The
	// payload carries identifiers and status only, so authoritative state is
	// still read through the API.
	Webhook *WebhookTarget
}

// WebhookTarget names where to report Invocation transitions. Leaving
// Events empty selects every event, which is the safe default: dropping
// WebhookEventWaiting would leave a parked host tool loop unannounced.
type WebhookTarget struct {
	URL    string
	Events []WebhookEvent
}

type WebhookEvent string

const (
	WebhookEventWaiting WebhookEvent = "invocation.waiting"
	WebhookEventPaused  WebhookEvent = "invocation.paused"
	WebhookEventEnded   WebhookEvent = "invocation.ended"
)

func (e WebhookEvent) valid() bool {
	return e == WebhookEventWaiting || e == WebhookEventPaused || e == WebhookEventEnded
}

type IfActivePolicy string

const (
	IfActiveReject    IfActivePolicy = "reject"
	IfActiveSupersede IfActivePolicy = "supersede"
	IfActiveInterrupt IfActivePolicy = "interrupt"
)

type BudgetExhaustionBehavior string

const (
	BudgetExhaustionStop  BudgetExhaustionBehavior = "stop"
	BudgetExhaustionPause BudgetExhaustionBehavior = "pause"
)

type ProviderKeySelection struct {
	Provider string
	Source   ProviderKeySource
	APIKey   string
}

type ProviderKeySource string

const (
	ProviderKeyCallerEphemeral ProviderKeySource = "caller_ephemeral"
	ProviderKeyAppBYOK         ProviderKeySource = "app_byok"
	ProviderKeyTenantBYOK      ProviderKeySource = "tenant_byok"
	ProviderKeyPlatform        ProviderKeySource = "platform"
)

type ListProviderKeysOptions struct {
	Provider  *ModelProvider
	Scope     *ProviderKeyScope
	Status    *ProviderKeyStatus
	TenantKey *string
	Cursor    *string
	Limit     *int
}

type ListCredentialsOptions struct {
	Status *CredentialStatus
	Cursor *string
	Limit  *int
}

type CreateCredentialInput struct {
	Name           string
	Profile        CredentialProfile
	AppID          *string
	TenantKey      *string
	SessionID      *string
	Operations     []RuntimeOperation
	ExpiresAt      *time.Time
	IdempotencyKey string
}

type RotateCredentialInput struct {
	OverlapSeconds int
	IdempotencyKey string
}

type ListModelsOptions struct {
	Provider          *ModelProvider
	IncludeDeprecated *bool
}

type CreateProviderKeyInput struct {
	Provider       ModelProvider
	Scope          ProviderKeyScope
	TenantKey      *string
	APIKey         string
	ExpiresAt      *time.Time
	IdempotencyKey string
}

type RotateProviderKeyInput struct {
	APIKey         string
	ExpiresAt      *time.Time
	OverlapSeconds *int
	IdempotencyKey string
}

type ToolResult struct {
	ToolCallID string
	Content    any
	IsError    bool
}

type ListInvocationsOptions struct {
	TenantKey     *string
	DefaultTenant *bool
	UserKey       *string
	SessionID     *string
	AgentID       *string
	AgentKey      *string
	Status        *InvocationStatus
	Statuses      []InvocationStatus
	Cursor        *string
	Limit         *int
}

type ListSessionsOptions struct {
	TenantKey     *string
	DefaultTenant *bool
	UserKey       *string
	AgentID       *string
	AgentKey      *string
	SessionKey    *string
	Cursor        *string
	Limit         *int
}

// SeedMessage is one host-asserted user or assistant text message at Session
// creation. Seed history requires AgentKey and is never appended to a keyed
// Session that already exists.
type SeedMessage struct {
	Role    SeedMessageRole `json:"role"`
	Content string          `json:"content"`
}

// CreateSessionOptions creates or seeds a Session without admitting an
// Invocation. Omitting AgentKey creates an unbound Session only when
// SeedMessages is empty. SessionKey requires AgentKey and makes creation an
// upsert.
type CreateSessionOptions struct {
	AgentKey   *string
	TenantKey  *string
	UserKey    *string
	SessionKey *string
	// SessionOptions configures the Session as it is created. Compaction is
	// rejected here because its policy is validated against an Invocation's
	// model, which a Session created on its own does not have; set retention
	// and metadata here and compaction on an Invocation admission.
	SessionOptions *SessionOptions
	SeedMessages   []SeedMessage
}

// ForkSessionOptions copies a source transcript through exactly one inclusive
// message ID or sequence into a new ordinary Session. Supply exactly one fork
// point. SessionKey makes retries and repeated calls resolve the same child.
type ForkSessionOptions struct {
	FromMessageID *string
	FromSequence  *int64
	SessionKey    *string
	UserKey       *string
	// SessionOptions applies retention and metadata to the new child. Nothing is
	// inherited from the source, and compaction is set on the child's first turn.
	SessionOptions *SessionOptions
}

type ListAgentsOptions struct {
	AgentKey *string
	Cursor   *string
	Limit    *int
}

type RegisterAppOptions struct {
	ExternalRef *string
	DisplayName *string
	OrgID       *string
	// CallbackTimeoutSeconds bounds each callback HTTP request, 1 to 60,
	// defaulting to 10. A callback that cannot answer within it should return
	// 202 and settle later through SubmitToolResults. Webhook delivery is
	// unaffected.
	CallbackTimeoutSeconds *int64
}

type ListAppsOptions struct {
	ExternalRef *string
	Status      *ArchiveStatus
}

type ListOrgsOptions struct {
	Status *ArchiveStatus
}

type ListAgentDefinitionsOptions struct {
	IncludeArchived *bool
	Cursor          *string
	Limit           *int
}

type UpdateAppOptions struct {
	DisplayName *string
	OrgID       *string
	// CallbackTimeoutSeconds replaces the App's callback HTTP reply deadline.
	CallbackTimeoutSeconds *int64
}

type RegisterOrgOptions struct {
	ExternalRef *string
}

type MessageListOptions struct {
	Cursor *string
	Limit  *int
}

type CompactionListOptions struct {
	Cursor *string
	Limit  *int
}

type TranscriptOptions struct {
	Cursor    *string
	PageToken *string
	Limit     *int
}

type StreamOptions struct {
	Deltas *bool
}

type WaitOptions struct {
	MinPollInterval time.Duration
	MaxPollInterval time.Duration
	Until           WaitCondition
	Timeout         time.Duration
}

type WaitCondition string

const (
	WaitUntilTerminal   WaitCondition = "terminal"
	WaitUntilActionable WaitCondition = "actionable"
)

func (o WaitOptions) normalized() WaitOptions {
	if o.MinPollInterval <= 0 {
		o.MinPollInterval = 100 * time.Millisecond
	}
	if o.MaxPollInterval <= 0 {
		o.MaxPollInterval = 2 * time.Second
	}
	if o.MaxPollInterval < o.MinPollInterval {
		o.MaxPollInterval = o.MinPollInterval
	}
	if o.Until == "" {
		o.Until = WaitUntilTerminal
	}
	return o
}

// encoded validates one Agent Definition on its own content and renders the
// body that invocation and resource creation both send. It deliberately
// checks only what the definition itself can settle: installation state, App
// signing keys, budgets, provider keys, and model lifecycle are re-checked when
// a turn is admitted, so a definition can be created before its App is fully
// configured to run it.
func (d AgentDefinition) encoded() (map[string]any, error) {
	if d.Model.Provider == "" || d.Model.ID == "" {
		return nil, fmt.Errorf("agent definition model is required")
	}
	for _, tool := range d.Tools {
		switch tool.Mode {
		case ToolModeBuiltin:
			if tool.Name != "nvoken_fetch" ||
				tool.Description != "" ||
				tool.InputSchema != nil ||
				tool.Callback != nil ||
				tool.Handler != nil {
				return nil, fmt.Errorf(
					"builtin tool must be the unmodified nvoken_fetch declaration",
				)
			}
		case ToolModeHost:
			if tool.Callback != nil {
				return nil, fmt.Errorf(
					"host tool %q cannot include a callback target",
					tool.Name,
				)
			}
		case ToolModeCallback:
			if tool.Callback == nil || tool.Callback.URL == "" {
				return nil, fmt.Errorf(
					"callback tool %q requires a callback target",
					tool.Name,
				)
			}
			if tool.Handler != nil {
				return nil, fmt.Errorf(
					"callback tool %q cannot include a local handler",
					tool.Name,
				)
			}
		default:
			return nil, fmt.Errorf(
				"tool %q has unsupported mode %q",
				tool.Name,
				tool.Mode,
			)
		}
	}
	if d.OutputSchema != nil {
		if err := PreflightOutputSchema(d.OutputSchema); err != nil {
			return nil, err
		}
	}
	body := map[string]any{"model": d.Model}
	if d.Instructions != "" {
		body["instructions"] = d.Instructions
	}
	if d.Limits != nil {
		body["limits"] = d.Limits
	}
	if d.Sampling != nil {
		body["sampling"] = d.Sampling
	}
	if d.Reasoning != nil {
		body["reasoning"] = d.Reasoning
	}
	if d.ToolChoice != nil {
		body["tool_choice"] = d.ToolChoice
	}
	if len(d.Tools) > 0 {
		body["tools"] = d.Tools
	}
	if len(d.MCPServers) > 0 {
		body["mcp_servers"] = d.MCPServers
	}
	if len(d.ProviderTools) > 0 {
		body["provider_tools"] = d.ProviderTools
	}
	if d.OutputSchema != nil {
		body["output_schema"] = d.OutputSchema
	}
	return body, nil
}

// encodedMCPServerHeaders checks the per-turn secret headers. When the
// selected definition is opaque here, so server-name validation is left to the
// service.
func (r InvokeRequest) encodedMCPServerHeaders() ([]MCPServerHeaders, error) {
	seen := make(map[string]struct{}, len(r.MCPServerHeaders))
	for _, entry := range r.MCPServerHeaders {
		if entry.Name == "" {
			return nil, fmt.Errorf("mcp server headers require a server name")
		}
		if len(entry.Headers) == 0 {
			return nil, fmt.Errorf(
				"mcp server headers for %q require at least one header",
				entry.Name,
			)
		}
		if _, duplicate := seen[entry.Name]; duplicate {
			return nil, fmt.Errorf(
				"mcp server headers name %q is repeated",
				entry.Name,
			)
		}
		seen[entry.Name] = struct{}{}
	}
	return r.MCPServerHeaders, nil
}

// The recorded context bounds the Runtime enforces. They are checked here so a
// snapshot that cannot be admitted fails before a request is spent, and the
// per-Session limit of 16 distinct names is left to the service, which is the
// only side that knows what a Session has already recorded.
const (
	maxContextItems        = 8
	maxContextNameRunes    = 64
	maxContextContentBytes = 8 << 10
	maxContextTotalBytes   = 16 << 10
)

var contextNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// encodedContext checks the recorded context snapshots against the bounds the
// Runtime enforces at admission.
func (r InvokeRequest) encodedContext() ([]ContextItem, error) {
	if len(r.Context) > maxContextItems {
		return nil, fmt.Errorf("context accepts at most %d items", maxContextItems)
	}
	seen := make(map[string]struct{}, len(r.Context))
	total := 0
	for _, item := range r.Context {
		if utf8.RuneCountInString(item.Name) > maxContextNameRunes ||
			!contextNamePattern.MatchString(item.Name) {
			return nil, fmt.Errorf(
				"context name %q must match ^[a-z][a-z0-9-]*$ and be at most %d characters",
				item.Name,
				maxContextNameRunes,
			)
		}
		if _, duplicate := seen[item.Name]; duplicate {
			return nil, fmt.Errorf("context name %q is repeated", item.Name)
		}
		seen[item.Name] = struct{}{}
		if !item.Tier.valid() {
			return nil, fmt.Errorf(
				"context %q tier must be contextual or operator",
				item.Name,
			)
		}
		if item.Content == "" {
			return nil, fmt.Errorf("context %q content cannot be empty", item.Name)
		}
		if len(item.Content) > maxContextContentBytes {
			return nil, fmt.Errorf(
				"context %q content exceeds %d bytes",
				item.Name,
				maxContextContentBytes,
			)
		}
		total += len(item.Content)
		if total > maxContextTotalBytes {
			return nil, fmt.Errorf(
				"context content totals more than %d bytes",
				maxContextTotalBytes,
			)
		}
	}
	return r.Context, nil
}

func (r InvokeRequest) encoded() ([]byte, error) {
	if r.AgentKey == "" {
		return nil, fmt.Errorf("agent key is required")
	}
	if (r.Input == "") == (len(r.InputBlocks) == 0) {
		return nil, fmt.Errorf("supply exactly one of input and input blocks")
	}
	if len(r.InputBlocks) != 0 {
		if err := PreflightInputBlocks(r.InputBlocks); err != nil {
			return nil, err
		}
	}
	if (r.AgentDefinition == nil) == (r.AgentDefinitionID == "") {
		return nil, fmt.Errorf(
			"supply exactly one of agent definition and agent definition id",
		)
	}
	var input any = r.Input
	if len(r.InputBlocks) != 0 {
		input = inputBlocksWire(r.InputBlocks)
	}
	wire := map[string]any{
		"agent_key":       r.AgentKey,
		"idempotency_key": r.IdempotencyKey,
		"input":           input,
	}
	if r.AgentDefinitionID != "" {
		wire["agent_definition_id"] = r.AgentDefinitionID
	} else {
		definition, err := r.AgentDefinition.encoded()
		if err != nil {
			return nil, err
		}
		wire["agent_definition"] = definition
	}
	headers, err := r.encodedMCPServerHeaders()
	if err != nil {
		return nil, err
	}
	if len(headers) > 0 {
		wire["mcp_server_headers"] = headers
	}
	recorded, err := r.encodedContext()
	if err != nil {
		return nil, err
	}
	if len(recorded) > 0 {
		wire["context"] = recorded
	}
	if r.TenantKey != nil {
		wire["tenant_key"] = *r.TenantKey
	}
	if r.UserKey != nil {
		wire["user_key"] = *r.UserKey
	}
	if r.SessionID != nil {
		wire["session_id"] = *r.SessionID
	}
	if r.SessionKey != nil {
		wire["session_key"] = *r.SessionKey
	}
	if r.SessionOptions != nil {
		if r.SessionOptions.Compaction == nil && r.SessionOptions.Retention == nil &&
			len(r.SessionOptions.Metadata) == 0 {
			return nil, fmt.Errorf("session options require at least one member")
		}
		wire["session_options"] = r.SessionOptions
	}
	if len(r.Metadata) > 0 {
		wire["metadata"] = r.Metadata
	}
	switch r.IfActive {
	case "":
	case IfActiveReject, IfActiveSupersede, IfActiveInterrupt:
		wire["if_active"] = r.IfActive
	default:
		return nil, fmt.Errorf("if active must be reject, supersede, or interrupt")
	}
	switch r.OnBudgetExhausted {
	case "":
	case BudgetExhaustionStop, BudgetExhaustionPause:
		wire["on_budget_exhausted"] = r.OnBudgetExhausted
	default:
		return nil, fmt.Errorf("on budget exhausted must be stop or pause")
	}
	if r.Webhook != nil {
		if r.Webhook.URL == "" {
			return nil, fmt.Errorf("webhook target url is required")
		}
		target := map[string]any{"url": r.Webhook.URL}
		if len(r.Webhook.Events) != 0 {
			events := make([]string, 0, len(r.Webhook.Events))
			for _, event := range r.Webhook.Events {
				if !event.valid() {
					return nil, fmt.Errorf("unsupported webhook event %q", event)
				}
				events = append(events, string(event))
			}
			target["events"] = events
		}
		wire["webhook"] = target
	}
	if len(r.ProviderKeys) > 1 {
		return nil, fmt.Errorf(
			"at most one provider key selection is supported",
		)
	}
	if len(r.ProviderKeys) == 1 {
		selection := r.ProviderKeys[0]
		if selection.Provider == "" {
			return nil, fmt.Errorf(
				"provider key selection provider is required",
			)
		}
		item := map[string]any{
			"provider": selection.Provider,
			"source":   selection.Source,
		}
		switch selection.Source {
		case ProviderKeyCallerEphemeral:
			if selection.APIKey == "" {
				return nil, fmt.Errorf(
					"caller-ephemeral provider keys require an API key",
				)
			}
			item["key"] = map[string]any{"api_key": selection.APIKey}
		case ProviderKeyAppBYOK, ProviderKeyTenantBYOK, ProviderKeyPlatform:
			if selection.APIKey != "" {
				return nil, fmt.Errorf(
					"%s provider keys cannot include an API key",
					selection.Source,
				)
			}
		default:
			return nil, fmt.Errorf(
				"unsupported provider key source %q",
				selection.Source,
			)
		}
		wire["provider_keys"] = []map[string]any{item}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encode invocation: %w", err)
	}
	return encoded, nil
}

func (r InvokeRequest) generated() (generated.CreateInvocationRequest, error) {
	encoded, err := r.encoded()
	if err != nil {
		return generated.CreateInvocationRequest{}, err
	}
	var request generated.CreateInvocationRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return generated.CreateInvocationRequest{}, fmt.Errorf("convert invocation to generated transport: %w", err)
	}
	return request, nil
}

func generatedModelProvider(provider string) (generated.ModelProvider, error) {
	value := generated.ModelProvider(provider)
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(value) {
		return "", fmt.Errorf("model provider must be a valid canonical identifier")
	}
	return value, nil
}

func generatedToolResults(results []ToolResult) (generated.SubmitHostToolResultsRequest, error) {
	wire := struct {
		Results []map[string]any `json:"results"`
	}{Results: make([]map[string]any, 0, len(results))}
	for _, result := range results {
		item := map[string]any{
			"tool_call_id": result.ToolCallID,
			"content":      result.Content,
		}
		if result.IsError {
			item["is_error"] = true
		}
		wire.Results = append(wire.Results, item)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return generated.SubmitHostToolResultsRequest{}, fmt.Errorf("encode tool results: %w", err)
	}
	var request generated.SubmitHostToolResultsRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return generated.SubmitHostToolResultsRequest{}, fmt.Errorf("convert tool results to generated transport: %w", err)
	}
	return request, nil
}

// terminal reports whether an Invocation has stopped for good. `incomplete` is
// one of the four: a turn the runtime cut off at a budget is over, and a wait
// helper that treated only `completed` as an ending would poll it forever.
func terminal(status InvocationStatus) bool {
	return status == InvocationCompleted || status == InvocationIncomplete ||
		status == InvocationFailed || status == InvocationCancelled
}

func waitSatisfied(status InvocationStatus, until WaitCondition) bool {
	switch until {
	case WaitUntilTerminal:
		return terminal(status)
	case WaitUntilActionable:
		return status == InvocationWaiting || terminal(status)
	default:
		return false
	}
}
