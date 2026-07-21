package domain

import (
	"encoding/json"
	"time"
)

type ToolCallMode string

const (
	ToolCallModeBuiltin  ToolCallMode = "builtin"
	ToolCallModeCallback ToolCallMode = "callback"
	ToolCallModeClient   ToolCallMode = "client"
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
