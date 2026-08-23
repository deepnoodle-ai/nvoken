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
type InvocationTrigger = generated.InvocationTrigger
type InvocationChildCounts = generated.InvocationChildCounts
type App = generated.App
type AppList = generated.AppList
type AppRegistration = generated.AppRegistration
type AppSigningKeyPurpose = generated.AppSigningKeyPurpose
type AppSigningKeySecret = generated.AppSigningKeySecret
type AppSigningKey = generated.AppSigningKey
type AppSigningKeyList = generated.AppSigningKeyList
type AnonymousTokenResponse = generated.AnonymousTokenResponse
type ArchiveStatus = generated.ArchiveStatus
type ClientKey = generated.ClientKey

// ListOrder is the sequence order for a message page.
type ListOrder = generated.ListSessionMessagesParamsOrder
type ClientKeyList = generated.ClientKeyList

// AgentResource is one tenant's Agent exactly as the server stores it: the
// serialization behind Agent, reachable through (*Agent).Resource.
type AgentResource = generated.Agent
type AgentDefinitionResourceList = generated.AgentDefinitionResourceList
type Session = generated.Session
type SessionContext = generated.SessionContext
type SessionMessage = generated.SessionMessage
type SessionCompaction = generated.SessionCompaction
type MessagePhase = generated.MessagePhase
type SeedMessageRole = generated.SeedMessageRole
type ToolCallSummary = generated.ToolCallSummary
type ToolResultResponse = generated.SubmitHostToolResultsResponse
type ModelProvider = generated.ModelProvider
type ModelDescriptor = generated.ModelDescriptor
type ModelPricing = generated.ModelPricing
type ProviderKey = generated.ProviderKey
type ProviderKeyUsage = generated.ProviderKeyUsage
type CreditBlock = generated.CreditBlock
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
type Memory = generated.Memory
type MemoryList = generated.MemoryList
type MemoryKind = generated.MemoryKind
type MemorySearchMode = generated.MemorySearchMode
type MemoryConfig = generated.MemoryConfig
type MemoryConfigScope = generated.MemoryConfigScope
type MemoryContextConfig = generated.MemoryContextConfig
type MemoryContextMode = generated.MemoryContextMode

type CredentialIssuance struct {
	Credential        Credential
	Secret            string
	DeliveryExpiresAt time.Time
	Replayed          bool
}

const (
	InvocationQueued                                  = generated.InvocationStatusQueued
	InvocationRunning                                 = generated.InvocationStatusRunning
	InvocationWaiting                                 = generated.InvocationStatusWaiting
	InvocationBudgetHold                              = generated.InvocationStatusBudgetHold
	InvocationCompleted                               = generated.InvocationStatusCompleted
	InvocationIncomplete                              = generated.InvocationStatusIncomplete
	InvocationFailed                                  = generated.InvocationStatusFailed
	InvocationCancelled                               = generated.InvocationStatusCancelled
	StopReasonEndTurn                                 = generated.InvocationStopReasonEndTurn
	StopReasonInterrupted                             = generated.InvocationStopReasonInterrupted
	StopReasonMaxIterations                           = generated.InvocationStopReasonMaxIterations
	StopReasonDeadline                                = generated.InvocationStopReasonDeadline
	StopReasonMaxOutputTokens                         = generated.InvocationStopReasonMaxOutputTokens
	StopReasonMaxEstimatedCost                        = generated.InvocationStopReasonMaxEstimatedCost
	StopReasonInsufficientCredits                     = generated.InvocationStopReasonInsufficientCredits
	MessagePhaseCommentary                            = generated.Commentary
	MessagePhaseFinalAnswer                           = generated.FinalAnswer
	SeedMessageRoleUser                               = generated.SeedMessageRoleUser
	SeedMessageRoleAssistant                          = generated.SeedMessageRoleAssistant
	AppSigningKeyPurposeCallback                      = generated.AppSigningKeyPurposeCallback
	AppSigningKeyPurposeWebhook                       = generated.AppSigningKeyPurposeWebhook
	ToolCallModeBuiltin                               = generated.ToolCallModeBuiltin
	ToolCallModeCallback                              = generated.ToolCallModeCallback
	ToolCallModeHost                                  = generated.ToolCallModeHost
	ToolCallModeMCP                                   = generated.ToolCallModeMcp
	ModelProviderAnthropic              ModelProvider = "anthropic"
	ModelProviderOpenAI                 ModelProvider = "openai"
	ModelProviderXAI                    ModelProvider = "xai"
	ModelProviderGoogle                 ModelProvider = "google"
	ProviderKeyScopeApp                               = generated.ProviderKeyScopeApp
	ProviderKeyScopeTenant                            = generated.ProviderKeyScopeTenant
	ProviderKeyStatusActive                           = generated.ProviderKeyStatusActive
	ProviderKeyStatusRevoked                          = generated.ProviderKeyStatusRevoked
	NudgePending                                      = generated.NudgeStatusPending
	NudgeDrained                                      = generated.NudgeStatusDrained
	NudgeExpired                                      = generated.NudgeStatusExpired
	NudgeCancelled                                    = generated.NudgeStatusCancelled
	UsageIntervalDay                                  = generated.Day
	UsageIntervalWeek                                 = generated.Week
	UsageIntervalMonth                                = generated.Month
	CredentialProfileRuntime                          = generated.CredentialProfileRuntime
	CredentialProfileViewer                           = generated.CredentialProfileViewer
	CredentialProfileOperator                         = generated.CredentialProfileOperator
	CredentialStatusActive                            = generated.CredentialStatusActive
	CredentialStatusRevoked                           = generated.CredentialStatusRevoked
	ArchiveStatusActive                               = generated.ArchiveStatusActive
	ArchiveStatusAll                                  = generated.ArchiveStatusAll
	ArchiveStatusArchived                             = generated.ArchiveStatusArchived
	ListOrderAscending                                = generated.ListOrderAscending
	ListOrderDescending                               = generated.ListOrderDescending
	OperationCreateInvocation                         = generated.CreateInvocation
	OperationCreateSession                            = generated.CreateSession
	OperationGetAgent                                 = generated.GetAgent
	OperationListAgents                               = generated.ListAgents
	OperationGetInvocation                            = generated.GetInvocation
	OperationSubmitToolResults                        = generated.SubmitToolResults
	OperationCancelInvocation                         = generated.CancelInvocation
	OperationInterruptInvocation                      = generated.InterruptInvocation
	OperationManageInvocationNudges                   = generated.ManageInvocationNudges
	OperationResumeInvocation                         = generated.ResumeInvocation
	OperationListInvocations                          = generated.ListInvocations
	OperationGetSession                               = generated.GetSession
	OperationUpdateSession                            = generated.UpdateSession
	OperationDeleteSession                            = generated.DeleteSession
	OperationListSessions                             = generated.ListSessions
	OperationListSessionMessages                      = generated.ListSessionMessages
	OperationGetSessionTranscript                     = generated.GetSessionTranscript
	OperationGetIdentity                              = generated.GetIdentity
	OperationListCredentials                          = generated.ListCredentials
	OperationCreateCredential                         = generated.CreateCredential
	OperationGetCredential                            = generated.GetCredential
	OperationRotateCredential                         = generated.RotateCredential
	OperationRevokeCredential                         = generated.RevokeCredential
	OperationListProviderKeys                         = generated.ListProviderKeys
	OperationCreateProviderKey                        = generated.CreateProviderKey
	OperationGetProviderKey                           = generated.GetProviderKey
	OperationRotateProviderKey                        = generated.RotateProviderKey
	OperationRevokeProviderKey                        = generated.RevokeProviderKey
	OperationReadUsage                                = generated.ReadUsage
	OperationReadCredits                              = generated.ReadCredits
	OperationAllocateCredits                          = generated.AllocateCredits
	OperationCreateAgentDefinition                    = generated.CreateAgentDefinition
	OperationListAgentDefinitions                     = generated.ListAgentDefinitions
	OperationGetAgentDefinition                       = generated.GetAgentDefinition
	OperationGetAgentDefinitionRevision               = generated.GetAgentDefinitionRevision
	OperationUpdateAgentDefinition                    = generated.UpdateAgentDefinition
	OperationCreateAgent                              = generated.CreateAgent
	OperationUpdateAgent                              = generated.UpdateAgent
	OperationArchiveAgent                             = generated.ArchiveAgent
	OperationRestoreAgent                             = generated.RestoreAgent
	OperationRegisterApp                              = generated.RegisterApp
	OperationListApps                                 = generated.ListApps
	OperationGetApp                                   = generated.GetApp
	OperationUpdateApp                                = generated.UpdateApp
	OperationGetOrg                                   = generated.GetOrg
	OperationListOrgs                                 = generated.ListOrgs
	OperationRegisterOrg                              = generated.RegisterOrg
	OperationUpdateOrg                                = generated.UpdateOrg
	MemoryKindEpisode                                 = generated.Episode
	MemoryKindFact                                    = generated.Fact
	MemoryKindPreference                              = generated.Preference
	MemoryKindSummary                                 = generated.Summary
	MemorySearchModeHybrid                            = generated.Hybrid
	MemorySearchModeKeyword                           = generated.Keyword
	MemorySearchModeSemantic                          = generated.Semantic
	MemoryConfigScopeTenant                           = generated.MemoryConfigScopeTenant
	MemoryConfigScopeUser                             = generated.MemoryConfigScopeUser
	MemoryContextModeFull                             = generated.MemoryContextModeFull
	MemoryContextModeIndex                            = generated.MemoryContextModeIndex
	MemoryContextModeOff                              = generated.MemoryContextModeOff
)

type ModelList struct {
	CatalogVersion string            `json:"catalog_version"`
	Items          []ModelDescriptor `json:"items"`
}

type InvocationList struct {
	HasMore bool         `json:"has_more"`
	Items   []Invocation `json:"items"`
	// NextCursor is nil once an ordinary listing is exhausted. Under the ended
	// feed it is always set, including on an empty page.
	NextCursor *string `json:"next_cursor"`
	// CompleteThrough is set only by ListEndedInvocations, where it is the
	// instant the feed is complete to.
	CompleteThrough *time.Time `json:"complete_through,omitempty"`
}

type AgentList struct {
	HasMore    bool            `json:"has_more"`
	Items      []AgentResource `json:"items"`
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

// MintAppSigningKeyInput names the purpose to mint the next version for.
//
// Activate collapses mint and activate into one call. It is correct only when
// no verifier still holds the current secret, because it moves signing before
// any receiver can be configured.
type MintAppSigningKeyInput struct {
	Purpose  AppSigningKeyPurpose
	Activate bool
}

type TranscriptSnapshot struct {
	HasMore           bool               `json:"has_more"`
	InvocationChanges []InvocationChange `json:"invocation_changes"`
	Messages          []SessionMessage   `json:"messages"`
	NextPageToken     *string            `json:"next_page_token"`
	Cursor            string             `json:"cursor"`
}

type TranscriptDrain struct {
	InvocationChanges []InvocationChange `json:"invocation_changes"`
	Messages          []SessionMessage   `json:"messages"`
	Cursor            string             `json:"cursor"`
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
	// AuthorizationContext binds the Session to the host's own authorization
	// facts. It is written only by the request that creates the Session, never
	// interpreted by nvoken, never visible to the model, and carried inside the
	// signed callback envelope so a receiver authorizes a delivery without
	// reading the Invocation back. What nvoken guarantees is integrity, not
	// authentication: what creation recorded is what a signed delivery carries.
	AuthorizationContext map[string]string `json:"authorization_context,omitempty"`
	// PinnedRevision fixes the Agent Definition revision for the lifetime of a
	// newly created Session. Omit it to follow the Agent's resolution policy.
	PinnedRevision *int64 `json:"pinned_revision,omitempty"`
	// OnConflict says what you are asserting about a Session that already
	// exists. Empty and SessionOptionsRefuse compare every member you sent;
	// SessionOptionsJoin reaches whatever Session is there without asserting
	// how it is configured. Join never relaxes AuthorizationContext,
	// PinnedRevision, or the Session's user key.
	OnConflict SessionOptionsConflict `json:"on_conflict,omitempty"`
}

// SessionOptionsConflict says what a request asserts about a Session that
// already exists.
type SessionOptionsConflict string

const (
	// SessionOptionsRefuse, the default, compares every option sent.
	SessionOptionsRefuse SessionOptionsConflict = "refuse"
	// SessionOptionsJoin reaches whatever Session is there without asserting
	// how it is configured, so compaction and retention stop conflicting. It
	// never relaxes the authorization context, the revision pin, or the
	// Session's end user: those catch a caller acting on the wrong
	// conversation, and a flag that suppressed them would be a way around the
	// check rather than a way to express intent.
	SessionOptionsJoin SessionOptionsConflict = "join"
)

func (o *SessionOptions) empty() bool {
	return o.Retention == nil && len(o.AuthorizationContext) == 0 && o.PinnedRevision == nil
}

func (o *SessionOptions) conflictPolicy() (*generated.SessionOptionsOnConflict, error) {
	switch o.OnConflict {
	case "":
		return nil, nil
	case SessionOptionsRefuse, SessionOptionsJoin:
		policy := generated.SessionOptionsOnConflict(o.OnConflict)
		return &policy, nil
	default:
		return nil, fmt.Errorf("session options on conflict must be refuse or join")
	}
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
	if o.empty() && o.OnConflict == "" {
		return nil, fmt.Errorf("session options require at least one member")
	}
	options := &generated.SessionOptions{}
	if o.Retention != nil {
		options.Retention = &generated.RetentionPolicy{TTLSeconds: o.Retention.TTLSeconds}
	}
	if len(o.AuthorizationContext) > 0 {
		context := generated.AuthorizationContext(o.AuthorizationContext)
		options.AuthorizationContext = &context
	}
	options.PinnedRevision = o.PinnedRevision
	policy, err := o.conflictPolicy()
	if err != nil {
		return nil, err
	}
	options.OnConflict = policy
	return options, nil
}

// generatedFork converts the child's creation-only options. on_conflict has no
// meaning here: a fork always creates, so there is no existing Session to
// assert anything about.
func (o *SessionOptions) generatedFork() (*generated.ForkSessionOptions, error) {
	if o.Compaction != nil {
		return nil, fmt.Errorf("forked Sessions start uncompacted; set compaction on the child Session's first invocation")
	}
	if o.OnConflict != "" {
		return nil, fmt.Errorf("on conflict does not apply to a fork, which always creates")
	}
	if o.empty() {
		return nil, fmt.Errorf("session options require at least one member")
	}
	options := &generated.ForkSessionOptions{}
	if o.Retention != nil {
		options.Retention = &generated.RetentionPolicy{TTLSeconds: o.Retention.TTLSeconds}
	}
	if len(o.AuthorizationContext) > 0 {
		context := generated.AuthorizationContext(o.AuthorizationContext)
		options.AuthorizationContext = &context
	}
	options.PinnedRevision = o.PinnedRevision
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

// AgentDefinition is everything writable on an App-owned Agent Definition,
// flat, matching the contract's AgentDefinitionWrite. Reads return
// AgentDefinitionResource, which is this same flat object plus ID, Revision,
// and timestamps, so a read-modify-write is a conversion and a field
// assignment:
//
//	current, err := client.GetAgentDefinition(ctx, id)
//	definition, err := AgentDefinitionFromResource(current)
//	definition.Instructions = "Be concise and warm."
//	next, err := client.UpdateAgentDefinition(ctx, id, definition,
//		nvoken.UpdateAgentDefinitionOptions{ExpectedRevision: current.Revision})
type AgentDefinition struct {
	// DefinitionKey is the caller-chosen immutable key, unique within the App.
	// It is required to create. A replacement cannot move a resource to
	// another key, so it is ignored there.
	DefinitionKey string `json:"definition_key,omitempty"`
	// Name defaults to DefinitionKey. Because a replacement replaces the whole
	// resource, leaving it empty on update resets the name to the key rather
	// than keeping the current one.
	Name            string           `json:"name,omitempty"`
	Instructions    string           `json:"instructions,omitempty"`
	Model           Model            `json:"model"`
	Sampling        *Sampling        `json:"sampling,omitempty"`
	Reasoning       *Reasoning       `json:"reasoning,omitempty"`
	ToolChoice      *ToolChoice      `json:"tool_choice,omitempty"`
	Limits          *Limits          `json:"limits,omitempty"`
	Tools           []Tool           `json:"tools,omitempty"`
	MCPServers      []MCPServer      `json:"mcp_servers,omitempty"`
	ProviderTools   []ProviderTool   `json:"provider_tools,omitempty"`
	Memory          *MemoryConfig    `json:"memory,omitempty"`
	ClientInterface *ClientInterface `json:"client_interface,omitempty"`
	OutputSchema    map[string]any   `json:"output_schema,omitempty"`
}

type AgentDefinitionResource = generated.AgentDefinitionResource

// ClientInterface is one definition-specific browser authorization. It grants
// authorship and settlement only, never selective read visibility: every
// public transcript item in a browser-reachable Session must be treated as
// client-visible.
//
// A nil ClientInterface means the definition is not client-token-capable; an
// empty one opts in with no client-authored context or tools.
type ClientInterface struct {
	// ContextNames are recorded-context names a client may append or
	// supersede, contextual tier only.
	ContextNames []string `json:"context_names,omitempty"`
	// ToolNames are host-mode tools whose parked calls a client may see and
	// settle.
	ToolNames []string `json:"tool_names,omitempty"`
}

// AgentDefinitionFromResource reads a resource back into the definition that
// produced it, so a replacement can change one field and keep the rest.
//
// A replacement replaces the whole resource, so a field this drops would be
// erased on write. It therefore carries every writable field across by name
// rather than listing them, and leaves the read-only ones — ID, Revision, and
// the timestamps — behind.
func AgentDefinitionFromResource(resource *AgentDefinitionResource) (AgentDefinition, error) {
	if resource == nil {
		return AgentDefinition{}, &Error{
			Category: ErrorValidation,
			Message:  "Agent Definition resource is required",
		}
	}
	encoded, err := json.Marshal(resource)
	if err != nil {
		return AgentDefinition{}, &Error{
			Category: ErrorValidation,
			Message:  err.Error(),
			Cause:    err,
		}
	}
	var definition AgentDefinition
	if err := json.Unmarshal(encoded, &definition); err != nil {
		return AgentDefinition{}, &Error{
			Category: ErrorValidation,
			Message:  err.Error(),
			Cause:    err,
		}
	}
	return definition, nil
}

// CreateAgentDefinitionOptions carries what a create sends outside its body.
type CreateAgentDefinitionOptions struct {
	// IdempotencyKey pins replay to this specific create, so the same key keeps
	// returning that create's revision-1 resource even after later revisions
	// moved it on. Leave it empty for the ordinary case: DefinitionKey is
	// unique within the App and already scopes replay. Nothing is invented on
	// your behalf, because a key the SDK made up would be new on every attempt
	// and so could never deduplicate one.
	IdempotencyKey string
}

// UpdateAgentDefinitionOptions carries what a replacement sends outside its
// body.
type UpdateAgentDefinitionOptions struct {
	// ExpectedRevision is the revision the definition was read at, sent as
	// If-Match. AnyDefinitionRevision sends `*` instead.
	ExpectedRevision int64
}

// AnyDefinitionRevision is the ExpectedRevision that sends `If-Match: *`,
// meaning "I read no revision; replace whichever is current".
//
// It is the honest precondition for a caller syncing from its own source of
// truth, which has nothing to be stale against. It is never refused as stale,
// and it still cannot create: the Definition must already exist.
//
// A real revision keeps its own meaning — "I am replacing the revision I
// read" — so one that has since moved is refused even if the replacement
// happens to match it, because the caller is acting on a state it has not
// seen. Reach for this constant when that is genuinely not what you meant.
const AnyDefinitionRevision int64 = 0

// DefinitionSyncOutcome reports what one definition's sync did.
type DefinitionSyncOutcome string

const (
	// DefinitionCreated means the key named nothing and now names this.
	DefinitionCreated DefinitionSyncOutcome = "created"
	// DefinitionUpdated means a revision was published over different
	// contents.
	DefinitionUpdated DefinitionSyncOutcome = "updated"
	// DefinitionUnchanged means nvoken already held exactly this, so nothing
	// was published and the revision did not move.
	DefinitionUnchanged DefinitionSyncOutcome = "unchanged"
)

// DefinitionSync is one definition's result from SyncDefinitions.
type DefinitionSync struct {
	DefinitionKey string
	Outcome       DefinitionSyncOutcome
	Definition    *AgentDefinitionResource
}

// AgentDefinitionOverrides replaces a safe subset of one resolved Agent
// Definition for a single Invocation. It cannot add tools, data access, or
// memory authority.
type AgentDefinitionOverrides struct {
	Model        *Model
	Sampling     *Sampling
	Reasoning    *Reasoning
	ToolChoice   *ToolChoice
	Limits       *Limits
	OutputSchema map[string]any
}

type InvokeRequest struct {
	// Supply exactly one identity. AgentID avoids tenant/key lookup; AgentKey
	// resolves within TenantKey or the default tenant.
	AgentID   string
	AgentKey  string
	TenantKey *string
	// UserKey says who this turn is for. The first request that opens a Session
	// fixes its user key, including fixing it to absent; every later turn either
	// sends the same one or leaves it out and inherits it. A turn naming a
	// different end user is refused with session_user_key_conflict.
	//
	// It is a filter, and on an Agent whose Definition sets memory.scope: user
	// it is also the memory partition — it decides whose durable memories the
	// model can recall — so it is required on the turn that opens a Session for
	// such an Agent.
	UserKey        *string
	SessionID      *string
	SessionKey     *string
	SessionOptions *SessionOptions
	// TriggeredBy records that this Invocation was admitted because one
	// durable ToolCall on another Invocation requested it. The parent and
	// ToolCall pair is verified by nvoken; it does not couple their lifecycles.
	TriggeredBy        *InvocationTrigger
	IdempotencyKey     string
	DefinitionRevision *int64
	Overrides          *AgentDefinitionOverrides
	IfActive           IfActivePolicy
	OnBudgetExhausted  BudgetExhaustionBehavior
	Input              string
	// InputBlocks carries ordered multi-block input mixing text, images, and
	// documents. Supply exactly one of Input and InputBlocks.
	InputBlocks []InputBlock
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
	// Client.UpdateSession.
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
	WebhookEventBudgetHold WebhookEvent = "invocation.budget_hold"
	WebhookEventEnded      WebhookEvent = "invocation.ended"
	WebhookEventWaiting    WebhookEvent = "invocation.waiting"
)

func (e WebhookEvent) valid() bool {
	return e == WebhookEventWaiting || e == WebhookEventBudgetHold || e == WebhookEventEnded
}

type IfActivePolicy string

const (
	IfActiveReject    IfActivePolicy = "reject"
	IfActiveSupersede IfActivePolicy = "supersede"
	IfActiveInterrupt IfActivePolicy = "interrupt"
)

type BudgetExhaustionBehavior string

const (
	BudgetExhaustionHold BudgetExhaustionBehavior = "hold"
	BudgetExhaustionStop BudgetExhaustionBehavior = "stop"
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
	OrgID          *string
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
	// ParentInvocationID selects direct children of one Invocation. Point it
	// at the literal "null" to select top-level Invocations; leave it nil for
	// the unfiltered collection.
	ParentInvocationID *string
	Cursor             *string
	Limit              *int
}

// ListEndedInvocationsOptions filters the reconciliation feed. It takes the
// same filters as the ordinary listing, because it is the same collection read
// in a different order.
type ListEndedInvocationsOptions struct {
	TenantKey     *string
	DefaultTenant *bool
	UserKey       *string
	SessionID     *string
	AgentID       *string
	AgentKey      *string
	Status        *InvocationStatus
	Statuses      []InvocationStatus
	// ParentInvocationID has the same direct-child, top-level, or unfiltered
	// meaning as ListInvocationsOptions.ParentInvocationID.
	ParentInvocationID *string
	// EndedSince starts a feed that has no cursor yet. It is mutually exclusive
	// with Cursor, which already carries a position.
	EndedSince *time.Time
	Cursor     *string
	Limit      *int
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
	AgentID    *string
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
	// SessionOptions applies retention, a revision pin, and an authorization
	// context to the new child. Nothing is inherited from the source except the
	// end user it was opened for, and compaction is set on the child's first
	// turn.
	SessionOptions *SessionOptions
}

type ListAgentsOptions struct {
	TenantKey       *string
	AgentKey        *string
	DefinitionID    *string
	IncludeArchived *bool
	Cursor          *string
	Limit           *int
}

// CreateAgentInput declares one tenant-scoped Agent. Creation is an upsert on
// (TenantKey, AgentKey): the same keys backed by the same Definition return
// the existing Agent, a different Definition pointer conflicts, and Name
// defaults to AgentKey.
type CreateAgentInput struct {
	TenantKey *string
	AgentKey  string
	Name      string
	// DefinitionID and DefinitionKey are two spellings of one pointer:
	// supply exactly one. The key spelling lets a caller declare an Agent from
	// the keys it already owns, with no lookup first, and carries the
	// Definition's own field name because it carries the Definition's value.
	DefinitionID   string
	DefinitionKey  string
	PinnedRevision *int64
}

// UpdateAgentInput changes the Agent's display name and/or revision pin.
// ClearPinnedRevision emits an explicit JSON null and cannot be combined with
// PinnedRevision.
type UpdateAgentInput struct {
	Name                *string
	PinnedRevision      *int64
	ClearPinnedRevision bool
}

type ListMemoriesOptions struct {
	AgentID    string
	TenantKey  *string
	UserKey    *string
	Query      *string
	SearchMode *MemorySearchMode
	Kind       *MemoryKind
	Cursor     *string
	Limit      *int
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
	// BrowserAccess configures browser-direct callers at registration. Omit it
	// to register the App with browser access disabled; UpdateApp turns it on
	// later. Enabling it requires DefaultRateLimits.
	BrowserAccess *BrowserAccess
	// DefaultRateLimits sets the shared App admission ceilings. Omit for
	// unlimited machine admission, which browser access does not allow.
	DefaultRateLimits *AppDefaultRateLimits
	// MachineConcurrencyLimits sets per-tenant and per-user fairness ceilings
	// for Invocations admitted through machine credentials. Omit for no
	// machine-specific concurrency ceilings.
	MachineConcurrencyLimits *MachineConcurrencyLimits
	// CreditPolicy defaults to CreditPolicyOff. Anonymous browser access
	// requires CreditPolicyRequired.
	CreditPolicy *CreditPolicy
}

// BrowserAccess is the complete browser-direct configuration for an App. It is
// replaced whole rather than merged, so a partial update cannot leave an origin
// list or a ceiling behind from a configuration nobody meant to keep.
type BrowserAccess struct {
	// AllowedOrigins are exact origins. Wildcards, userinfo, paths, queries,
	// and fragments are invalid, and HTTPS is required except for exact
	// localhost and loopback origins in development. A browser request must
	// match one exactly before nvoken emits CORS headers at all.
	AllowedOrigins []string
	// InvocationWebhook is where nvoken reports turns started from a browser.
	// It is required, because a browser client is the one caller that cannot
	// be trusted to tell the host what its own turn did.
	InvocationWebhook BrowserInvocationWebhook
	// Limits are the browser-specific ceilings enforced at admission, per end
	// user and per tenant rather than App-wide.
	Limits BrowserRateLimits
}

// BrowserInvocationWebhook is the endpoint nvoken posts browser-started turn
// events to.
type BrowserInvocationWebhook struct {
	// URL is an absolute HTTPS endpoint.
	URL string
	// Events defaults to every Invocation webhook event when empty, and must
	// include WebhookEventWaiting and WebhookEventEnded when set: a browser
	// turn that parks or ends with nobody told is a turn the host never learns
	// about.
	Events []WebhookEvent
}

// BrowserRateLimits are the ceilings a browser-direct caller is admitted under.
type BrowserRateLimits struct {
	MaxAdmissionsPerUserPerMinute     int64
	MaxConcurrentInvocationsPerTenant int64
	MaxConcurrentInvocationsPerUser   int64
}

// AppDefaultRateLimits are the App-wide admission ceilings.
type AppDefaultRateLimits struct {
	MaxAdmissionsPerMinute   int64
	MaxConcurrentInvocations int64
}

// MachineConcurrencyLimits are the per-tenant and per-user fairness ceilings
// for Invocations admitted through machine credentials.
type MachineConcurrencyLimits struct {
	MaxConcurrentInvocationsPerTenant int64
	MaxConcurrentInvocationsPerUser   int64
}

func (l *AppDefaultRateLimits) generated() *generated.AppDefaultRateLimits {
	if l == nil {
		return nil
	}
	return &generated.AppDefaultRateLimits{
		MaxAdmissionsPerMinute:   l.MaxAdmissionsPerMinute,
		MaxConcurrentInvocations: l.MaxConcurrentInvocations,
	}
}

func (l *MachineConcurrencyLimits) generated() *generated.MachineConcurrencyLimits {
	if l == nil {
		return nil
	}
	return &generated.MachineConcurrencyLimits{
		MaxConcurrentInvocationsPerTenant: l.MaxConcurrentInvocationsPerTenant,
		MaxConcurrentInvocationsPerUser:   l.MaxConcurrentInvocationsPerUser,
	}
}

func (b *BrowserAccess) generated() (*generated.BrowserAccess, error) {
	if b == nil {
		return nil, nil
	}
	if len(b.AllowedOrigins) == 0 {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "browser access requires at least one allowed origin",
		}
	}
	if b.InvocationWebhook.URL == "" {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "browser access requires an Invocation webhook URL",
		}
	}
	webhook := generated.BrowserInvocationWebhook{URL: b.InvocationWebhook.URL}
	if len(b.InvocationWebhook.Events) != 0 {
		events := make([]generated.WebhookEvent, 0, len(b.InvocationWebhook.Events))
		for _, event := range b.InvocationWebhook.Events {
			if !event.valid() {
				return nil, &Error{
					Category: ErrorValidation,
					Message:  fmt.Sprintf("unknown webhook event %q", string(event)),
				}
			}
			events = append(events, generated.WebhookEvent(event))
		}
		webhook.Events = &events
	}
	return &generated.BrowserAccess{
		AllowedOrigins:    append([]string(nil), b.AllowedOrigins...),
		InvocationWebhook: webhook,
		Limits: generated.BrowserRateLimits{
			MaxAdmissionsPerUserPerMinute:     b.Limits.MaxAdmissionsPerUserPerMinute,
			MaxConcurrentInvocationsPerTenant: b.Limits.MaxConcurrentInvocationsPerTenant,
			MaxConcurrentInvocationsPerUser:   b.Limits.MaxConcurrentInvocationsPerUser,
		},
	}, nil
}

func (a *AnonymousAccess) generated() (*generated.AnonymousAccess, error) {
	if a == nil {
		return nil, nil
	}
	if a.AgentID == "" {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "anonymous access requires the Agent visitors reach",
		}
	}
	return &generated.AnonymousAccess{
		AgentID:          a.AgentID,
		VisitorAllowance: a.VisitorAllowance,
		Limits: generated.AnonymousRateLimits{
			MaxAdmissionsPerMinute:     a.Limits.MaxAdmissionsPerMinute,
			MaxTokenExchangesPerMinute: a.Limits.MaxTokenExchangesPerMinute,
		},
		SessionRetention: generated.RetentionPolicy{
			TTLSeconds: a.SessionRetention.TTLSeconds,
		},
		WebhookDelivery: generated.AnonymousAccessWebhookDelivery(a.WebhookDelivery),
	}, nil
}

// encoded renders the update body, writing an explicit null for each member
// the caller asked to clear. Setting a member and clearing it in the same
// request is refused rather than resolved, because either resolution would be
// somebody's surprise.
func (o *UpdateAppOptions) encoded() ([]byte, error) {
	wire := map[string]any{}
	if o.DisplayName != nil {
		wire["display_name"] = *o.DisplayName
	}
	if o.OrgID != nil {
		wire["org_id"] = *o.OrgID
	}
	if o.CallbackTimeoutSeconds != nil {
		wire["callback_timeout_seconds"] = *o.CallbackTimeoutSeconds
	}
	if o.CreditPolicy != nil {
		wire["credit_policy"] = string(*o.CreditPolicy)
	}
	browser, err := o.BrowserAccess.generated()
	if err != nil {
		return nil, err
	}
	anonymous, err := o.AnonymousAccess.generated()
	if err != nil {
		return nil, err
	}
	for _, member := range []struct {
		name  string
		value any
		clear bool
	}{
		{name: "browser_access", value: browser, clear: o.ClearBrowserAccess},
		{name: "anonymous_access", value: anonymous, clear: o.ClearAnonymousAccess},
		{
			name:  "default_rate_limits",
			value: o.DefaultRateLimits.generated(),
			clear: o.ClearDefaultRateLimits,
		},
		{
			name:  "machine_concurrency_limits",
			value: o.MachineConcurrencyLimits.generated(),
			clear: o.ClearMachineConcurrencyLimits,
		},
	} {
		set := !isNilValue(member.value)
		if set && member.clear {
			return nil, &Error{
				Category: ErrorValidation,
				Message:  fmt.Sprintf("app update cannot both set and clear %s", member.name),
			}
		}
		switch {
		case set:
			wire[member.name] = member.value
		case member.clear:
			wire[member.name] = nil
		}
	}
	if len(wire) == 0 {
		return nil, &Error{
			Category: ErrorValidation,
			Message:  "app update requires at least one member",
		}
	}
	return json.Marshal(wire)
}

// isNilValue reports whether an `any` holding a typed nil pointer is nil, which
// a bare comparison against nil does not.
func isNilValue(value any) bool {
	switch typed := value.(type) {
	case *generated.BrowserAccess:
		return typed == nil
	case *generated.AnonymousAccess:
		return typed == nil
	case *generated.AppDefaultRateLimits:
		return typed == nil
	case *generated.MachineConcurrencyLimits:
		return typed == nil
	default:
		return value == nil
	}
}

// CreditPolicy says whether an App's turns are admitted against a credit
// balance.
type CreditPolicy string

const (
	// CreditPolicyOff, the default, admits turns without consulting credits.
	CreditPolicyOff CreditPolicy = "off"
	// CreditPolicyRequired refuses a turn a tenant has no balance for.
	// Anonymous browser access requires it, because a visitor with no account
	// is the one caller nobody can be billed for after the fact.
	CreditPolicyRequired CreditPolicy = "required"
)

// AnonymousWebhookDelivery controls whether anonymous turns inherit the App's
// browser webhook.
type AnonymousWebhookDelivery string

const (
	AnonymousWebhookDeliveryBrowserAccess AnonymousWebhookDelivery = "browser_access"
	AnonymousWebhookDeliveryDisabled      AnonymousWebhookDelivery = "disabled"
)

// AnonymousRateLimits bound anonymous admissions and credential exchange.
type AnonymousRateLimits struct {
	// MaxAdmissionsPerMinute is shared across every anonymous visitor in the
	// tenant rather than applied per visitor, because a visitor is not an
	// account somebody can be held to.
	MaxAdmissionsPerMinute int64
	// MaxTokenExchangesPerMinute is shared across service replicas for this App
	// and browser Origin.
	MaxTokenExchangesPerMinute int64
}

// AnonymousAccess is the complete managed-anonymous configuration. Enabling
// it requires browser access, finite App limits, CreditPolicyRequired, and a
// memory-free Agent Definition.
type AnonymousAccess struct {
	AgentID string
	// VisitorAllowance is what one visitor may spend before the grant stops
	// admitting turns.
	VisitorAllowance Money
	Limits           AnonymousRateLimits
	SessionRetention SessionRetention
	WebhookDelivery  AnonymousWebhookDelivery
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

// UpdateAppOptions replaces whole members rather than merging into them.
// Every field is a replacement of the stored value; omitting one preserves it,
// and the Clear* fields send an explicit null where the difference between
// "leave it alone" and "turn it off" matters.
type UpdateAppOptions struct {
	DisplayName *string
	OrgID       *string
	// CallbackTimeoutSeconds replaces the App's callback HTTP reply deadline.
	CallbackTimeoutSeconds *int64
	// BrowserAccess replaces the whole browser configuration. Set
	// ClearBrowserAccess instead to disable browser access without deleting
	// the App's client keys.
	BrowserAccess      *BrowserAccess
	ClearBrowserAccess bool
	// AnonymousAccess replaces the whole anonymous-visitor configuration.
	// Set ClearAnonymousAccess to stop minting and refuse new anonymous-token
	// requests. This is the only way to enable anonymous access: registration
	// deliberately cannot, because it requires browser access and finite
	// limits that must already be stored.
	AnonymousAccess      *AnonymousAccess
	ClearAnonymousAccess bool
	// DefaultRateLimits replaces the App-wide ceilings. Set
	// ClearDefaultRateLimits to restore unlimited machine admission, which is
	// refused while browser access remains enabled.
	DefaultRateLimits      *AppDefaultRateLimits
	ClearDefaultRateLimits bool
	// MachineConcurrencyLimits replaces both machine-credential fairness
	// ceilings. Set ClearMachineConcurrencyLimits to disable them.
	MachineConcurrencyLimits      *MachineConcurrencyLimits
	ClearMachineConcurrencyLimits bool
	// CreditPolicy changes enforcement for turns admitted from now on.
	// Invocations already running keep the policy they were admitted under.
	CreditPolicy *CreditPolicy
}

type RegisterOrgOptions struct {
	ExternalRef *string
}

type MessageListOptions struct {
	Cursor *string
	Limit  *int
	// Order defaults to ListOrderAscending, oldest first. ListOrderDescending
	// starts at the newest message. A cursor belongs to the direction that
	// issued it and is refused by the other, so page one direction to its end
	// rather than turning around mid-walk.
	Order *ListOrder
}

type CompactionListOptions struct {
	Cursor *string
	Limit  *int
}

type TranscriptOptions struct {
	Cursor    *string
	PageToken *string
	Limit     *int
	// Tail reads the newest bounded window. Leave it nil when following the
	// returned older-history PageToken.
	Tail *bool
}

type StreamOptions struct {
	Deltas *bool
	// Cursor starts the first connection after a durable stream cursor. Once a
	// newer durable event arrives, reconnects resume from that newer cursor.
	Cursor *string
	// InvocationID narrows a Session subscription to one Invocation. An
	// InvocationHandle already supplies its own ID, so this field is for
	// Client.StreamSessionWithOptions.
	InvocationID *string
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

// encoded validates one Agent Definition on its own content and renders its
// reusable configuration fields. It deliberately checks only what the
// definition itself can settle: installation state, App
// signing keys, budgets, provider keys, and model lifecycle are re-checked when
// a turn is admitted, so a definition can be created before its App is fully
// configured to run it.
// includeKey is false for a replacement, which cannot move a resource to
// another key. A definition read back from the server always carries one, so
// it is dropped there rather than refused.
func (d AgentDefinition) encoded(includeKey bool) (map[string]any, error) {
	if d.Model.Provider == "" || d.Model.ID == "" {
		return nil, fmt.Errorf("agent definition model is required")
	}
	if d.ToolChoice != nil {
		named := d.ToolChoice.Mode == ToolChoiceNamed
		if named && d.ToolChoice.Name == "" {
			return nil, fmt.Errorf("tool choice mode named requires a tool name")
		}
		if !named && d.ToolChoice.Name != "" {
			return nil, fmt.Errorf(
				"tool choice mode %q cannot name a tool",
				d.ToolChoice.Mode,
			)
		}
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
	if includeKey && d.DefinitionKey != "" {
		body["definition_key"] = d.DefinitionKey
	}
	// An omitted name is left omitted rather than filled in with the key: the
	// runtime applies that default, and duplicating it would make two places
	// to change it.
	if d.Name != "" {
		body["name"] = d.Name
	}
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
	if d.Memory != nil {
		body["memory"] = d.Memory
	}
	if d.ClientInterface != nil {
		body["client_interface"] = d.ClientInterface
	}
	if d.OutputSchema != nil {
		body["output_schema"] = d.OutputSchema
	}
	return body, nil
}

func (o AgentDefinitionOverrides) encoded() (map[string]any, error) {
	body := make(map[string]any)
	if o.Model != nil {
		if o.Model.Provider == "" || o.Model.ID == "" {
			return nil, fmt.Errorf("override model requires provider and id")
		}
		body["model"] = o.Model
	}
	if o.Sampling != nil {
		body["sampling"] = o.Sampling
	}
	if o.Reasoning != nil {
		body["reasoning"] = o.Reasoning
	}
	if o.ToolChoice != nil {
		body["tool_choice"] = o.ToolChoice
	}
	if o.Limits != nil {
		body["limits"] = o.Limits
	}
	if o.OutputSchema != nil {
		if err := PreflightOutputSchema(o.OutputSchema); err != nil {
			return nil, err
		}
		body["output_schema"] = o.OutputSchema
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("overrides require at least one member")
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
	if (r.AgentID == "") == (r.AgentKey == "") {
		return nil, fmt.Errorf("supply exactly one of agent id and agent key")
	}
	if (r.Input == "") == (len(r.InputBlocks) == 0) {
		return nil, fmt.Errorf("supply exactly one of input and input blocks")
	}
	if len(r.InputBlocks) != 0 {
		if err := PreflightInputBlocks(r.InputBlocks); err != nil {
			return nil, err
		}
	}
	var input any = r.Input
	if len(r.InputBlocks) != 0 {
		input = inputBlocksWire(r.InputBlocks)
	}
	wire := map[string]any{
		"idempotency_key": r.IdempotencyKey,
		"input":           input,
	}
	if r.AgentID != "" {
		wire["agent_id"] = r.AgentID
	} else {
		wire["agent_key"] = r.AgentKey
	}
	if r.DefinitionRevision != nil {
		wire["definition_revision"] = *r.DefinitionRevision
	}
	if r.Overrides != nil {
		overrides, err := r.Overrides.encoded()
		if err != nil {
			return nil, err
		}
		wire["overrides"] = overrides
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
		if r.SessionOptions.Compaction == nil && r.SessionOptions.empty() &&
			r.SessionOptions.OnConflict == "" {
			return nil, fmt.Errorf("session options require at least one member")
		}
		if _, err := r.SessionOptions.conflictPolicy(); err != nil {
			return nil, err
		}
		wire["session_options"] = r.SessionOptions
	}
	if r.TriggeredBy != nil {
		wire["triggered_by"] = r.TriggeredBy
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
	case BudgetExhaustionStop, BudgetExhaustionHold:
		wire["on_budget_exhausted"] = r.OnBudgetExhausted
	default:
		return nil, fmt.Errorf("on budget exhausted must be stop or hold")
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

// TerminalInvocationStatuses are the statuses that mean a turn has stopped for
// good. Exported so no caller keeps a copy, for the same reason
// AnswerableToolCalls is: the classification is part of the protocol, and
// rediscovering it in every application is how one of them gets it wrong.
var TerminalInvocationStatuses = []InvocationStatus{
	InvocationCompleted,
	InvocationIncomplete,
	InvocationFailed,
	InvocationCancelled,
}

// AllInvocationStatuses is every status the contract defines, in lifecycle
// order. It is the source ActiveInvocationStatuses is derived from, so a status
// added to the contract only has to be classified once.
var AllInvocationStatuses = []InvocationStatus{
	InvocationQueued,
	InvocationRunning,
	InvocationWaiting,
	InvocationBudgetHold,
	InvocationCompleted,
	InvocationIncomplete,
	InvocationFailed,
	InvocationCancelled,
}

// ActiveInvocationStatuses are the statuses that mean a turn is still going.
// This is what ListInvocations wants for a teardown, sweep, or reconciler,
// which filters server-side and takes values rather than a predicate.
//
// It is derived rather than written out, so a status added to the contract
// lands here without anyone remembering to add it. That is the safe direction:
// a turn nobody knew about shows up in "still live" and gets waited on, rather
// than being dropped from the sweep meant to find it.
var ActiveInvocationStatuses = activeInvocationStatuses()

func activeInvocationStatuses() []InvocationStatus {
	active := make([]InvocationStatus, 0, len(AllInvocationStatuses))
	for _, status := range AllInvocationStatuses {
		if !IsTerminalStatus(status) {
			active = append(active, status)
		}
	}
	return active
}

// IsTerminalStatus reports whether a status means the turn is over.
//
// There are eight statuses and four of them are terminal, so the interesting
// mistake is writing the other four out. `queued`, `running`, `waiting`, and
// `budget_hold` differ only in what unblocks them — a budget-held turn stopped on
// spending capacity with its deadlines on hold, and resumes on its own once its
// account is funded — and a turn wrongly believed finished is one nobody
// settles, reattaches to, or cancels before erasing its Session.
//
// A status this build does not recognize is reported as not terminal, which is
// the safe direction: you wait on a turn that already ended rather than
// abandoning one that has not.
func IsTerminalStatus(status InvocationStatus) bool {
	for _, candidate := range TerminalInvocationStatuses {
		if status == candidate {
			return true
		}
	}
	return false
}

// IsTurnOver reports whether a change ends the turn. This is the terminal
// signal, and there is no other.
//
// It answers for the change, not for the turn: a replayed `running` change
// reports false even after the turn has ended, which is what lets a client fold
// messages before changes and never mark a turn settled before its final
// message exists.
//
// Either witness suffices. The field and the status always agree when both are
// present — nvoken computes one from the other — so accepting either keeps this
// correct against a server too old to send the field, where a required bool
// decodes as false and is indistinguishable from a genuine one.
func IsTurnOver(change InvocationChange) bool {
	return change.Terminal || IsTerminalStatus(change.Status)
}

func terminal(status InvocationStatus) bool {
	return IsTerminalStatus(status)
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

// AnswerableToolCalls returns the tool calls this caller is expected to run.
//
// There is one tool-call collection. An entry you have to answer is the one
// carrying the arguments to answer it with; builtin and MCP calls nvoken runs
// itself, and calls that have already settled, carry none. Filtering on that
// is what replaced the separate pending list.
func AnswerableToolCalls(invocation *Invocation) []ToolCallSummary {
	if invocation == nil || invocation.ToolCalls == nil {
		return nil
	}
	answerable := make([]ToolCallSummary, 0, len(*invocation.ToolCalls))
	for _, call := range *invocation.ToolCalls {
		if call.Arguments != nil {
			answerable = append(answerable, call)
		}
	}
	return answerable
}

// HostToolCalls returns the tool calls this caller must run itself.
//
// Answerable is not the same as yours. Once an App declares callback tools,
// nvoken delivers those to an endpoint — but a machine credential may still
// settle one after its receiver acknowledged delivery, so a pending
// callback-mode call carries arguments and is answerable. Running it here as
// well would double the side effect.
//
// Yours is answerable and mode host. Partitioning on that beats keeping a list
// of your own tool names, which goes stale the first time an agent gains a tool
// and nobody updates the list.
func HostToolCalls(invocation *Invocation) []ToolCallSummary {
	answerable := AnswerableToolCalls(invocation)
	mine := make([]ToolCallSummary, 0, len(answerable))
	for _, call := range answerable {
		if call.Mode == ToolCallModeHost {
			mine = append(mine, call)
		}
	}
	return mine
}

// toolCallArguments hands a handler the arguments as the plain map a tool
// handler expects, rather than the generated pointer.
func toolCallArguments(call ToolCallSummary) any {
	if call.Arguments == nil {
		return map[string]any{}
	}
	return map[string]any(*call.Arguments)
}
