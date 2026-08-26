package nvoken

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

// Exact resource projections. The handwritten facade deliberately gives the
// runnable handles distinct names; request-shaped control remains on Raw().
type AgentResource = generated.Agent
type AgentRevision = generated.AgentRevision
type OutputSchema = generated.OutputSchema
type ModelInput = generated.ModelInput
type ToolDeclaration = generated.ToolDeclaration
type DefaultMemoryPolicy = generated.DefaultMemoryPolicy
type ConversationResource = generated.Conversation
type ConversationMessage = generated.ConversationMessage
type MemorySpace = generated.MemorySpace
type MemorySpaceList = generated.MemorySpaceList
type TurnResource = generated.Turn
type TurnResultResource = generated.TurnResult
type TurnChange = generated.TurnChange
type TurnStatus = generated.TurnStatus
type TurnStopReason = generated.TurnStopReason
type ToolCallSummary = generated.ToolCallSummary
type ToolCallMode = generated.ToolCallMode
type ToolCallStatus = generated.ToolCallStatus
type Limits = generated.Limits
type Metadata = generated.Metadata
type Model = generated.Model
type ModelProvider = generated.ModelProvider
type ModelDescriptor = generated.ModelDescriptor
type ModelPricing = generated.ModelPricing
type CreditBlock = generated.CreditBlock
type Nudge = generated.Nudge
type NudgeStatus = generated.NudgeStatus
type NudgeAcknowledgement = generated.NudgeAcknowledgement
type NudgeList = generated.NudgeList
type MCPServer = generated.MCPServer
type MCPTimeouts = generated.MCPTimeouts
type MCPListToolsResponse = generated.MCPListToolsResponse

type WebhookEvent = generated.WebhookEvent

const (
	WebhookEventWaiting    = generated.WebhookEventWaiting
	WebhookEventBudgetHold = generated.WebhookEventBudgetHold
	WebhookEventEnded      = generated.WebhookEventEnded
)

type ModelList struct {
	CatalogVersion string            `json:"catalog_version"`
	Items          []ModelDescriptor `json:"items"`
}

type ListModelsOptions struct {
	Provider          *ModelProvider
	IncludeDeprecated *bool
}

type CredentialIssuance struct {
	Credential        Credential
	Secret            string
	DeliveryExpiresAt time.Time
	Replayed          bool
}

type ListCredentialsOptions struct {
	Status *CredentialStatus
	Cursor *string
	Limit  *int
}

type CreateCredentialInput struct {
	Name           string
	Type           CredentialType
	AppID          *string
	ExpiresAt      *time.Time
	IdempotencyKey string
}

type RotateCredentialInput struct {
	OverlapSeconds int
	IdempotencyKey string
}

type ProviderKeyList struct {
	HasMore    bool          `json:"has_more"`
	Items      []ProviderKey `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type ListProviderKeysOptions struct {
	Provider  *ModelProvider
	Scope     *ProviderKeyScope
	Status    *ProviderKeyStatus
	TenantKey *string
	Cursor    *string
	Limit     *int
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

type MintAppSigningKeyInput struct {
	Purpose  AppSigningKeyPurpose
	Activate bool
}

type AppDefaultRateLimits struct {
	MaxAdmissionsPerMinute int64
	MaxConcurrentTurns     int64
}

func (l *AppDefaultRateLimits) generated() *generated.AppDefaultRateLimits {
	if l == nil {
		return nil
	}
	return &generated.AppDefaultRateLimits{
		MaxAdmissionsPerMinute: l.MaxAdmissionsPerMinute,
		MaxConcurrentTurns:     l.MaxConcurrentTurns,
	}
}

type MachineConcurrencyLimits struct {
	MaxConcurrentTurnsPerTenant int64
	MaxConcurrentTurnsPerUser   int64
}

func (l *MachineConcurrencyLimits) generated() *generated.MachineConcurrencyLimits {
	if l == nil {
		return nil
	}
	return &generated.MachineConcurrencyLimits{
		MaxConcurrentTurnsPerTenant: l.MaxConcurrentTurnsPerTenant,
		MaxConcurrentTurnsPerUser:   l.MaxConcurrentTurnsPerUser,
	}
}

type BrowserTurnWebhook struct {
	URL    string
	Events []WebhookEvent
}

type BrowserRateLimits struct {
	MaxAdmissionsPerUserPerMinute int64
	MaxConcurrentTurnsPerTenant   int64
	MaxConcurrentTurnsPerUser     int64
}

type BrowserAccess struct {
	AllowedOrigins []string
	TurnWebhook    BrowserTurnWebhook
	Limits         BrowserRateLimits
}

func (b *BrowserAccess) generated() (*generated.BrowserAccess, error) {
	if b == nil {
		return nil, nil
	}
	if len(b.AllowedOrigins) == 0 || b.TurnWebhook.URL == "" {
		return nil, &Error{Category: ErrorValidation, Message: "browser access requires origins and a Turn webhook URL"}
	}
	webhook := generated.BrowserTurnWebhook{URL: b.TurnWebhook.URL}
	if len(b.TurnWebhook.Events) != 0 {
		events := make([]generated.WebhookEvent, 0, len(b.TurnWebhook.Events))
		for _, event := range b.TurnWebhook.Events {
			if !event.Valid() {
				return nil, &Error{Category: ErrorValidation, Message: fmt.Sprintf("unknown webhook event %q", event)}
			}
			events = append(events, generated.WebhookEvent(event))
		}
		webhook.Events = &events
	}
	return &generated.BrowserAccess{
		AllowedOrigins: append([]string(nil), b.AllowedOrigins...),
		TurnWebhook:    webhook,
		Limits: generated.BrowserRateLimits{
			MaxAdmissionsPerUserPerMinute: b.Limits.MaxAdmissionsPerUserPerMinute,
			MaxConcurrentTurnsPerTenant:   b.Limits.MaxConcurrentTurnsPerTenant,
			MaxConcurrentTurnsPerUser:     b.Limits.MaxConcurrentTurnsPerUser,
		},
	}, nil
}

type CreditPolicy string

const (
	CreditPolicyOff      CreditPolicy = "off"
	CreditPolicyRequired CreditPolicy = "required"
)

type RegisterAppOptions struct {
	ExternalRef              *string
	DisplayName              *string
	OrgID                    *string
	CallbackTimeoutSeconds   *int64
	BrowserAccess            *BrowserAccess
	DefaultRateLimits        *AppDefaultRateLimits
	MachineConcurrencyLimits *MachineConcurrencyLimits
	CreditPolicy             *CreditPolicy
}

type AnonymousAccess = generated.AnonymousAccess

type UpdateAppOptions struct {
	DisplayName                   *string
	OrgID                         *string
	CallbackTimeoutSeconds        *int64
	BrowserAccess                 *BrowserAccess
	ClearBrowserAccess            bool
	AnonymousAccess               *AnonymousAccess
	ClearAnonymousAccess          bool
	DefaultRateLimits             *AppDefaultRateLimits
	ClearDefaultRateLimits        bool
	MachineConcurrencyLimits      *MachineConcurrencyLimits
	ClearMachineConcurrencyLimits bool
	CreditPolicy                  *CreditPolicy
}

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
		wire["credit_policy"] = *o.CreditPolicy
	}
	browser, err := o.BrowserAccess.generated()
	if err != nil {
		return nil, err
	}
	for _, member := range []struct {
		name  string
		value any
		clear bool
	}{
		{name: "browser_access", value: browser, clear: o.ClearBrowserAccess},
		{name: "anonymous_access", value: o.AnonymousAccess, clear: o.ClearAnonymousAccess},
		{name: "default_rate_limits", value: o.DefaultRateLimits.generated(), clear: o.ClearDefaultRateLimits},
		{name: "machine_concurrency_limits", value: o.MachineConcurrencyLimits.generated(), clear: o.ClearMachineConcurrencyLimits},
	} {
		set := member.value != nil && !(reflect.ValueOf(member.value).Kind() == reflect.Pointer && reflect.ValueOf(member.value).IsNil())
		if set && member.clear {
			return nil, &Error{Category: ErrorValidation, Message: "cannot both set and clear " + member.name}
		}
		if set {
			wire[member.name] = member.value
		} else if member.clear {
			wire[member.name] = nil
		}
	}
	if len(wire) == 0 {
		return nil, &Error{Category: ErrorValidation, Message: "app update requires at least one member"}
	}
	return json.Marshal(wire)
}

type ListAppsOptions struct {
	ExternalRef *string
	Status      *ArchiveStatus
}

type ListOrgsOptions struct{ Status *ArchiveStatus }

type RegisterOrgOptions struct{ ExternalRef *string }

const (
	TurnQueued     = generated.TurnStatusQueued
	TurnRunning    = generated.TurnStatusRunning
	TurnWaiting    = generated.TurnStatusWaiting
	TurnBudgetHold = generated.TurnStatusBudgetHold
	TurnCompleted  = generated.TurnStatusCompleted
	TurnIncomplete = generated.TurnStatusIncomplete
	TurnFailed     = generated.TurnStatusFailed
	TurnCancelled  = generated.TurnStatusCancelled

	ToolCallModeHost     = generated.ToolCallModeHost
	ToolCallModeCallback = generated.ToolCallModeCallback
	ToolCallModeBuiltin  = generated.ToolCallModeBuiltin
	ToolCallModeMCP      = generated.ToolCallModeMcp
)

// Behavior is the portable high-level behavior surface. Wire-exact provider,
// MCP, reasoning, sampling, client-token, and tool-choice controls remain on
// Raw().
type Behavior struct {
	Instructions string
	Model        ModelInput
	Tools        []ToolDeclaration
	Limits       *Limits
	OutputSchema *OutputSchema
	Memory       *DefaultMemoryPolicy
}

func (b Behavior) generated() generated.BehaviorInput {
	var tools *[]generated.ToolDeclaration
	if b.Tools != nil {
		value := append([]generated.ToolDeclaration(nil), b.Tools...)
		tools = &value
	}
	return generated.BehaviorInput{
		Instructions: b.Instructions,
		Model:        b.Model,
		Tools:        tools,
		Limits:       cloneLimits(b.Limits),
		OutputSchema: b.OutputSchema,
		Memory:       b.Memory,
	}
}

type agentOwnerKind uint8

const (
	agentOwnerApp agentOwnerKind = iota
	agentOwnerTenant
	agentOwnerUser
)

// AgentOwner selects the namespace in which an Agent key is looked up or
// created. Construct it with AppOwned, TenantOwned, or UserOwned so an empty
// tenant or user is rejected instead of silently selecting another namespace.
type AgentOwner struct {
	kind   agentOwnerKind
	tenant string
	user   string
}

func AppOwned() AgentOwner { return AgentOwner{kind: agentOwnerApp} }

func TenantOwned(tenant string) AgentOwner {
	return AgentOwner{kind: agentOwnerTenant, tenant: tenant}
}

func UserOwned(tenant, user string) AgentOwner {
	return AgentOwner{kind: agentOwnerUser, tenant: tenant, user: user}
}

type AgentLookupOptions struct {
	OwnedBy AgentOwner
}

type CreateAgentOptions struct {
	Key            string
	Name           string
	OwnedBy        AgentOwner
	Behavior       Behavior
	IdempotencyKey string
}

type ListAgentsOptions struct {
	OwnedBy  AgentOwner
	Archived bool
	Cursor   string
	Limit    int
}

// AgentPage preserves paging coordinates while keeping every returned item
// directly runnable through the same Client.
type AgentPage struct {
	Items      []*Agent
	HasMore    bool
	NextCursor *string
}

type PublishOptions struct {
	IdempotencyKey string
}

// MemorySelection is one explicit per-Turn memory choice. Nil means use the
// selected behavior's default. NoneMemory disables memory.
type MemorySelection struct {
	Scope     string
	Namespace string
}

func NoneMemory() *MemorySelection { return &MemorySelection{Scope: "none"} }

func TenantMemory(namespace string) *MemorySelection {
	return &MemorySelection{Scope: "tenant", Namespace: namespace}
}

func UserMemory(namespace string) *MemorySelection {
	return &MemorySelection{Scope: "user", Namespace: namespace}
}

type conversationOwnerKind uint8

const (
	conversationOwnerTenant conversationOwnerKind = iota
	conversationOwnerUser
)

type ConversationOwner struct {
	kind conversationOwnerKind
	user string
}

func TenantConversation() ConversationOwner {
	return ConversationOwner{kind: conversationOwnerTenant}
}

func UserConversation(user string) ConversationOwner {
	return ConversationOwner{kind: conversationOwnerUser, user: user}
}

// ConversationSelection selects existing continuity by ID or atomically
// resolves a caller-owned key with continue-or-create semantics.
type ConversationSelection struct {
	ID       string
	Key      string
	Owner    ConversationOwner
	Metadata map[string]any
}

func ContinueConversation(id string) *ConversationSelection {
	return &ConversationSelection{ID: id}
}

func ContinueOrCreateConversation(key string, owner ConversationOwner) *ConversationSelection {
	return &ConversationSelection{Key: key, Owner: owner}
}

// TurnInput accepts either a string or []InputBlock. Go cannot express that
// parameter union while preserving the concise bare-string call, so the
// facade validates the dynamic value before making a request.
type TurnInput = any

type TurnOptions struct {
	TenantKey      string
	UserKey        string
	IdempotencyKey string
	Conversation   *ConversationSelection
	Memory         *MemorySelection
	Limits         *Limits
	Metadata       map[string]string
	Wait           WaitOptions
}

// ConversationOptions binds all execution context that must stay fixed across
// calls through one local Conversation handle.
type ConversationOptions struct {
	TenantKey string
	UserKey   string
	Selection ConversationSelection
	Memory    *MemorySelection
	Limits    *Limits
}

// ConversationTurnOptions contains only facts a Conversation call may vary.
// Limits may narrow, but never widen, the binding's limits.
type ConversationTurnOptions struct {
	IdempotencyKey string
	Metadata       map[string]string
	Limits         *Limits
	Wait           WaitOptions
}

type TurnAccess struct {
	TenantKey string
	UserKey   string
}

type WaitOptions struct {
	PollInterval time.Duration
}

type TurnToolContext struct {
	TurnID     string
	ToolCallID string
}

type ToolHandler func(context.Context, any, TurnToolContext) (any, error)

// Tool binds one exact durable tool name to a process-local handler. The tool
// contract remains part of the AgentRevision or inline Behavior.
type Tool struct {
	Name    string
	Handler ToolHandler
}

type toolResult struct {
	ToolCallID string
	Content    any
	IsError    bool
}

type TurnResult struct {
	TurnSnapshot
	Turn      *Turn
	Admission *TurnAdmission
}

type TurnSnapshot struct {
	Resource   TurnResource
	Messages   []ConversationMessage
	OutputText *string
}

type TurnAdmission struct {
	IdempotencyKey string
	Deduplicated   bool
}

type NoOutputTextError struct {
	TurnID string
}

func (e *NoOutputTextError) Error() string {
	return "Turn " + e.TurnID + " completed without text output"
}

// Administrative aliases retained outside the runtime facade.
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
type ClientKeyList = generated.ClientKeyList
type Credential = generated.Credential
type CredentialList = generated.CredentialList
type CredentialType = generated.CredentialType
type CredentialStatus = generated.CredentialStatus
type CurrentIdentity = generated.CurrentIdentity
type ProviderKey = generated.ProviderKey
type ProviderKeyUsage = generated.ProviderKeyUsage
type ProviderKeyScope = generated.ProviderKeyScope
type ProviderKeyStatus = generated.ProviderKeyStatus
type Money = generated.Money
type CreditAccount = generated.CreditAccount
type CreditAccountList = generated.CreditAccountList
type CreditAllocation = generated.CreditAllocation
type CreditAllocationList = generated.CreditAllocationList
type AllocateCreditsResult = generated.AllocateCreditsResult
type Org = generated.Org
type OrgList = generated.OrgList
type Tenant = generated.Tenant
type TenantList = generated.TenantList
type Trace = generated.Trace
type TraceList = generated.TraceList
type AdmissionAttempt = generated.AdmissionAttempt
type AdmissionAttemptList = generated.AdmissionAttemptList
type AdmissionOutcome = generated.AdmissionOutcome
type AdmissionSummary = generated.AdmissionSummary
type UsageBreakdown = generated.UsageBreakdown
type UsageTimeseries = generated.UsageTimeseries
type UsageRecords = generated.UsageRecords
type UsageMetrics = generated.UsageMetrics
type UsageInterval = generated.UsageInterval
type ListAdmissionsParams = generated.ListAdmissionsParams
type SummarizeAdmissionsParams = generated.SummarizeAdmissionsParams
type ListTenantsParams = generated.ListTenantsParams
type GetUsageBreakdownParams = generated.GetUsageBreakdownParams
type GetUsageBreakdownParamsGroupBy = generated.GetUsageBreakdownParamsGroupBy
type GetUsageBreakdownParamsSort = generated.GetUsageBreakdownParamsSort
type GetUsageTimeseriesParams = generated.GetUsageTimeseriesParams
type GetUsageTimeseriesParamsGroupBy = generated.GetUsageTimeseriesParamsGroupBy
type ListUsageRecordsParams = generated.ListUsageRecordsParams
type ListUsageRecordsParamsFormat = generated.ListUsageRecordsParamsFormat
type ListCreditAccountsParams = generated.ListCreditAccountsParams
type ListCreditAllocationsParams = generated.ListCreditAllocationsParams

const (
	AppSigningKeyPurposeCallback    = generated.AppSigningKeyPurposeCallback
	AppSigningKeyPurposeWebhook     = generated.AppSigningKeyPurposeWebhook
	CredentialTypeInstallationAdmin = generated.CredentialTypeInstallationAdmin
	CredentialTypeApp               = generated.CredentialTypeApp
	CredentialTypeAppReadOnly       = generated.CredentialTypeAppReadOnly
	CredentialStatusActive          = generated.CredentialStatusActive
	CredentialStatusRevoked         = generated.CredentialStatusRevoked
	ArchiveStatusActive             = generated.ArchiveStatusActive
	ArchiveStatusAll                = generated.ArchiveStatusAll
	ArchiveStatusArchived           = generated.ArchiveStatusArchived
	ProviderKeyScopeApp             = generated.ProviderKeyScopeApp
	ProviderKeyScopeTenant          = generated.ProviderKeyScopeTenant
	ProviderKeyStatusActive         = generated.ProviderKeyStatusActive
	ProviderKeyStatusRevoked        = generated.ProviderKeyStatusRevoked
)
