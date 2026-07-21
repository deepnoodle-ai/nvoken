package domain

import (
	"encoding/json"
	"time"
)

type Account struct {
	ID        string
	CreatedAt time.Time
}

type TenantPartition struct {
	ID        string
	AccountID string
	TenantRef *string
	CreatedAt time.Time
}

type Agent struct {
	ID        string
	AccountID string
	AgentRef  string
	CreatedAt time.Time
}

type Session struct {
	ID                    string
	AccountID             string
	TenantPartitionID     string
	AgentID               string
	SessionKey            *string
	NextMessageSequence   int64
	NextLifecycleRevision int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type ExecutionSpecSnapshot struct {
	ID        string
	AccountID string
	Spec      json.RawMessage
	CreatedAt time.Time
}

type InvocationStatus string

const (
	InvocationQueued    InvocationStatus = "queued"
	InvocationRunning   InvocationStatus = "running"
	InvocationWaiting   InvocationStatus = "waiting"
	InvocationCompleted InvocationStatus = "completed"
	InvocationFailed    InvocationStatus = "failed"
	InvocationCancelled InvocationStatus = "cancelled"
)

func (s InvocationStatus) Terminal() bool {
	switch s {
	case InvocationCompleted, InvocationFailed, InvocationCancelled:
		return true
	default:
		return false
	}
}

type Invocation struct {
	ID                        string
	SessionID                 string
	AccountID                 string
	TenantPartitionID         string
	AgentID                   string
	SpecSnapshotID            string
	IdempotencyKey            string
	RequestFingerprint        []byte
	FingerprintVersion        int
	Status                    InvocationStatus
	CurrentStateRevision      int64
	LeaseOwner                *string
	LeaseExpiresAt            *time.Time
	LeaseAttempt              int64
	WallClockTimeoutMS        int64
	ActiveTimeoutMS           int64
	MaxOutputTokens           *int
	MaxEstimatedCostMicros    *int64
	MaxIterations             int
	ActiveExecutionMS         int64
	WallClockDeadlineAt       time.Time
	ActiveSegmentStartedAt    *time.Time
	ExecutionDeadlineAt       *time.Time
	ExecutionDeadlineScope    *string
	CurrentCheckpointSequence int64
	CurrentIteration          int
	OutputSchemaDigest        []byte
	Output                    json.RawMessage
	OutputProvenance          json.RawMessage
	Error                     json.RawMessage
	Usage                     json.RawMessage
	Provenance                json.RawMessage
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
	CompletedAt               *time.Time
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleTool      MessageRole = "tool"
)

type SessionMessage struct {
	ID                string
	SessionID         string
	AccountID         string
	TenantPartitionID string
	AgentID           string
	InvocationID      string
	Sequence          int64
	Role              MessageRole
	Content           json.RawMessage
	CreatedAt         time.Time
}

type InvocationState struct {
	ID                     string
	InvocationID           string
	SessionID              string
	AccountID              string
	TenantPartitionID      string
	AgentID                string
	Revision               int64
	Status                 InvocationStatus
	LeaseAttempt           int64
	ThroughMessageSequence *int64
	CreatedAt              time.Time
}

// InvocationLifecycleChange is the public recovery projection of one durable
// state revision. Terminal evidence comes from the immutable Invocation row;
// message content remains exclusively in SessionMessage.
type InvocationLifecycleChange struct {
	InvocationState
	Error            json.RawMessage
	Usage            json.RawMessage
	Provenance       json.RawMessage
	Output           json.RawMessage
	OutputProvenance json.RawMessage
}

type InvocationClaim struct {
	Invocation     Invocation
	Owner          string
	Attempt        int64
	LeaseExpiresAt time.Time
}

type InvocationExecutionResult struct {
	Status               InvocationStatus
	Error                json.RawMessage
	AssistantMessages    []GenerationMessage
	MessagesCheckpointed bool
	Usage                *ModelUsage
	Provenance           *ModelProvenance
	StructuredOutput     *StructuredOutput
}

// GenerationMessage is the provider-neutral message shape exchanged with the
// model adapter. Content is the same ordered block array stored in the Session
// transcript; no provider request or response envelope crosses this boundary.
type GenerationMessage struct {
	Role    MessageRole
	Content json.RawMessage
}

type GenerationRequest struct {
	Instructions     string
	Provider         string
	Model            string
	Messages         []GenerationMessage
	ClientTools      []ClientToolDefinition
	MaxOutputTokens  *int
	MaxIterations    int
	Claim            *InvocationClaim
	StructuredOutput *StructuredOutputRequest
	Resume           *GenerationResume
}

type GenerationResponse struct {
	Messages                []GenerationMessage
	Usage                   ModelUsage
	ServedModel             string
	MessagesCheckpointed    bool
	BudgetExceeded          string
	StructuredOutput        *StructuredOutput
	StructuredOutputFailure string
	ClientToolsPending      bool
}

type ClientToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type StructuredOutputRequest struct {
	Schema       json.RawMessage
	SchemaDigest []byte
}

// GenerationResume is a validated durable prefix. The model adapter may
// continue only after the service has checked these rows against the owning
// Invocation and latest checkpoint.
type GenerationResume struct {
	Iteration               int
	Usage                   ModelUsage
	StructuredOutput        *StructuredOutput
	StructuredOutputFailure string
	OpenToolCalls           []ResumableToolCall
}

type ResumableToolCall struct {
	Call  ToolCall
	Input json.RawMessage
}

type StructuredOutput struct {
	Value      json.RawMessage
	Provenance StructuredOutputProvenance
}

type StructuredOutputProvenance struct {
	Source       string `json:"source"`
	ToolCallID   string `json:"tool_call_id"`
	SchemaSHA256 string `json:"schema_sha256"`
}

type ModelUsage struct {
	InputTokens              int        `json:"input_tokens"`
	OutputTokens             int        `json:"output_tokens"`
	CacheCreationInputTokens int        `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int        `json:"cache_read_input_tokens,omitempty"`
	ReasoningTokens          int        `json:"reasoning_tokens,omitempty"`
	EstimatedCost            *ModelCost `json:"estimated_cost,omitempty"`
	Iterations               int        `json:"iterations,omitempty"`
}

// ModelCost is Dive's normalized list-price estimate, not a billing ledger.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Total      float64 `json:"total"`
	Currency   string  `json:"currency,omitempty"`
	Model      string  `json:"model,omitempty"`
}

type ModelProvenance struct {
	Provider         string `json:"provider"`
	RequestedModel   string `json:"requested_model"`
	ServedModel      string `json:"served_model"`
	CredentialSource string `json:"credential_source"`
}
