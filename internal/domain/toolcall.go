package domain

import (
	"encoding/json"
	"time"
)

type ToolCallMode string

const (
	ToolCallModeBuiltin  ToolCallMode = "builtin"
	ToolCallModeCallback ToolCallMode = "callback"
	ToolCallModeHost     ToolCallMode = "host"
	ToolCallModeMCP      ToolCallMode = "mcp"
)

type ToolCallStatus string

const (
	ToolCallPending   ToolCallStatus = "pending"
	ToolCallRunning   ToolCallStatus = "running"
	ToolCallCompleted ToolCallStatus = "completed"
	ToolCallFailed    ToolCallStatus = "failed"
	ToolCallCancelled ToolCallStatus = "cancelled"
)

func (s ToolCallStatus) Terminal() bool {
	return s == ToolCallCompleted || s == ToolCallFailed || s == ToolCallCancelled
}

type ToolCallResultOrigin string

const (
	ToolCallResultBuiltin  ToolCallResultOrigin = "builtin"
	ToolCallResultCallback ToolCallResultOrigin = "callback"
	ToolCallResultHost     ToolCallResultOrigin = "host"
	ToolCallResultMCP      ToolCallResultOrigin = "mcp"
	ToolCallResultSystem   ToolCallResultOrigin = "system"
)

type CallbackDeliveryStatus string

const (
	CallbackDeliveryBlocked    CallbackDeliveryStatus = "blocked"
	CallbackDeliveryPending    CallbackDeliveryStatus = "pending"
	CallbackDeliveryDelivering CallbackDeliveryStatus = "delivering"
	CallbackDeliverySucceeded  CallbackDeliveryStatus = "succeeded"
	CallbackDeliveryFailed     CallbackDeliveryStatus = "failed"
	CallbackDeliveryAbandoned  CallbackDeliveryStatus = "abandoned"
)

func (s CallbackDeliveryStatus) Terminal() bool {
	return s == CallbackDeliverySucceeded || s == CallbackDeliveryFailed || s == CallbackDeliveryAbandoned
}

type CallbackDelivery struct {
	ID                string
	ToolCallID        string
	InvocationID      string
	SessionID         string
	AccountID         string
	TenantPartitionID string
	AgentID           string
	EndpointURL       string
	Status            CallbackDeliveryStatus
	AvailableAt       *time.Time
	Owner             *string
	LeaseExpiresAt    *time.Time
	Attempt           int64
	LastErrorCode     *string
	ResponseStatus    *int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	TerminalAt        *time.Time
}

type CallbackDeliveryClaim struct {
	Delivery CallbackDelivery
	Owner    string
	Attempt  int64
}

type ToolCall struct {
	ID                     string
	InvocationID           string
	SessionID              string
	AccountID              string
	TenantPartitionID      string
	AgentID                string
	Iteration              int
	BatchOrdinal           int
	ProviderCallID         string
	Name                   string
	Mode                   ToolCallMode
	RequestMessageID       string
	RequestMessageSequence int64
	RequestDigest          []byte
	Status                 ToolCallStatus
	DeadlineAt             time.Time
	CurrentAttempt         int
	ResultMessageID        *string
	ResultMessageSequence  *int64
	ResultOrigin           *ToolCallResultOrigin
	CreatedAt              time.Time
	UpdatedAt              time.Time
	CompletedAt            *time.Time
}

type ToolCallAttempt struct {
	ID                     string
	ToolCallID             string
	InvocationID           string
	SessionID              string
	AccountID              string
	TenantPartitionID      string
	AgentID                string
	Attempt                int
	InvocationLeaseAttempt int64
	Status                 ToolCallStatus
	StartedAt              time.Time
	CompletedAt            *time.Time
}

type ModelUsageReceipt struct {
	ID                string
	InvocationID      string
	SessionID         string
	AccountID         string
	TenantPartitionID string
	AgentID           string
	Iteration         int
	MessageID         string
	MessageSequence   int64
	Usage             json.RawMessage
	Provenance        json.RawMessage
	EvidenceDigest    []byte
	CreatedAt         time.Time
}

type InvocationCheckpointKind string

const (
	InvocationCheckpointModel InvocationCheckpointKind = "model"
	InvocationCheckpointTool  InvocationCheckpointKind = "tool"
)

type InvocationCheckpoint struct {
	ID                     string
	InvocationID           string
	SessionID              string
	AccountID              string
	TenantPartitionID      string
	AgentID                string
	Sequence               int64
	Iteration              int
	Kind                   InvocationCheckpointKind
	LeaseAttempt           int64
	ThroughMessageSequence int64
	UsageReceiptID         *string
	ToolCallID             *string
	CreatedAt              time.Time
}

type ToolCallRequest struct {
	ProviderCallID string
	Name           string
	Mode           ToolCallMode
	Input          json.RawMessage
	CallbackURL    string
}

type ModelCheckpointInput struct {
	Iteration  int
	Message    GenerationMessage
	Usage      ModelUsage
	Provenance ModelProvenance
	ToolCalls  []ToolCallRequest
}

type ModelCheckpointResult struct {
	Checkpoint InvocationCheckpoint
	Message    SessionMessage
	ToolCalls  []ToolCall
	Usage      ModelUsage
}

type ToolCallExecution struct {
	Call    ToolCall
	Attempt ToolCallAttempt
}

type MCPToolCallStart struct {
	Execution        *ToolCallExecution
	RecoveredContent json.RawMessage
	RecoveredIsError bool
}
