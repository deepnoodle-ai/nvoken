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
	ID                   string
	SessionID            string
	AccountID            string
	TenantPartitionID    string
	AgentID              string
	SpecSnapshotID       string
	IdempotencyKey       string
	RequestFingerprint   []byte
	Status               InvocationStatus
	CurrentStateRevision int64
	LeaseOwner           *string
	LeaseExpiresAt       *time.Time
	LeaseAttempt         int64
	Error                json.RawMessage
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CompletedAt          *time.Time
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

type InvocationClaim struct {
	Invocation     Invocation
	Owner          string
	Attempt        int64
	LeaseExpiresAt time.Time
}

type InvocationExecutionResult struct {
	Status InvocationStatus
	Error  json.RawMessage
}
