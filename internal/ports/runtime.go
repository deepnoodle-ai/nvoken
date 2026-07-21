package ports

import (
	"context"
	"errors"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

var (
	ErrNotFound               = errors.New("not found")
	ErrUnauthenticated        = errors.New("unauthenticated")
	ErrRetryable              = errors.New("retryable database conflict")
	ErrConcurrentAdmission    = errors.New("concurrent admission conflict")
	ErrLeaseLost              = errors.New("invocation lease lost")
	ErrProviderUnsupported    = errors.New("model provider unsupported")
	ErrProviderKeyMissing     = errors.New("model provider credential missing")
	ErrGenerationFailed       = errors.New("model generation failed")
	ErrGenerationInputInvalid = errors.New("durable model generation input invalid")
	ErrModelResponseInvalid   = errors.New("model response invalid")
	ErrExecutionResultInvalid = errors.New("invocation execution result invalid")
)

// Clock makes persisted timestamps deterministic in services and tests.
type Clock interface {
	Now() time.Time
}

// IDGenerator creates one prefixed durable identifier.
type IDGenerator interface {
	NewID(prefix domain.StableIDPrefix) (string, error)
}

// TransactionManager makes every repository call in fn share one atomic
// transaction. Nested calls join the transaction already carried by ctx.
type TransactionManager interface {
	WithTransaction(ctx context.Context, fn func(context.Context) error) error
}

type AccountRepository interface {
	CreateAccount(context.Context, domain.Account) error
	GetAccount(context.Context, string) (domain.Account, error)
	ListAccounts(context.Context) ([]domain.Account, error)
	LockInstallationBootstrap(context.Context) error
}

type TenantPartitionRepository interface {
	CreateTenantPartition(context.Context, domain.TenantPartition) error
	ResolveTenantPartition(context.Context, domain.TenantPartition) (domain.TenantPartition, error)
	GetTenantPartition(context.Context, string) (domain.TenantPartition, error)
	GetDefaultTenantPartition(context.Context, string) (domain.TenantPartition, error)
	GetTenantPartitionByRef(context.Context, string, string) (domain.TenantPartition, error)
}

type AgentRepository interface {
	CreateAgent(context.Context, domain.Agent) error
	ResolveAgent(context.Context, domain.Agent) (domain.Agent, error)
	GetAgentByRef(context.Context, string, string) (domain.Agent, error)
}

type SessionRepository interface {
	CreateSession(context.Context, domain.Session) error
	ResolveSessionByKey(context.Context, domain.Session) (domain.Session, error)
	GetSession(context.Context, string) (domain.Session, error)
	GetSessionForUpdate(context.Context, string) (domain.Session, error)
	GetSessionByKey(context.Context, string, string, string, string) (domain.Session, error)
	ReserveMessageSequence(context.Context, string) (int64, error)
	ReserveLifecycleRevision(context.Context, string) (int64, error)
}

type InvocationListQuery struct {
	AccountID          string
	TenantPartitionID  *string
	SessionID          *string
	AgentID            *string
	Status             *domain.InvocationStatus
	BeforeCreatedAt    *time.Time
	BeforeInvocationID *string
	Limit              int
}

type SessionListQuery struct {
	AccountID         string
	TenantPartitionID *string
	AgentID           *string
	SessionKey        *string
	BeforeCreatedAt   *time.Time
	BeforeSessionID   *string
	Limit             int
}

type SessionRecoveryRow struct {
	Session                domain.Session
	TenantRef              *string
	ActiveInvocationID     *string
	ActiveInvocationStatus *domain.InvocationStatus
}

type RecoveryRepository interface {
	ListInvocations(context.Context, InvocationListQuery) ([]domain.Invocation, error)
	ListSessions(context.Context, SessionListQuery) ([]SessionRecoveryRow, error)
	ListSessionMessagesRange(context.Context, string, int64, int64, int) ([]domain.SessionMessage, error)
	ListInvocationLifecycleChanges(context.Context, string, int64, int64, int) ([]domain.InvocationLifecycleChange, error)
}

type ExecutionSpecSnapshotRepository interface {
	CreateExecutionSpecSnapshot(context.Context, domain.ExecutionSpecSnapshot) error
	GetExecutionSpecSnapshot(context.Context, string) (domain.ExecutionSpecSnapshot, error)
}

type SessionMessageRepository interface {
	AppendSessionMessage(context.Context, domain.SessionMessage) error
	ListSessionMessages(context.Context, string) ([]domain.SessionMessage, error)
}

type InvocationRepository interface {
	CreateInvocation(context.Context, domain.Invocation) error
	GetInvocation(context.Context, string) (domain.Invocation, error)
	GetInvocationForUpdate(context.Context, string) (domain.Invocation, error)
	FindNextQueuedInvocationForUpdate(context.Context) (domain.Invocation, error)
	ListExpiredInvocationLeases(context.Context, time.Time, int) ([]domain.Invocation, error)
	GetInvocationByIdempotencyKey(context.Context, string, string, string, string) (domain.Invocation, error)
	GetNonterminalInvocationBySession(context.Context, string) (domain.Invocation, error)
	LockInvocationAdmissionKey(context.Context, string) error
	ClaimInvocation(context.Context, string, string, time.Time, int64, time.Time) (domain.Invocation, error)
	RenewInvocationLease(context.Context, string, string, int64, time.Time, time.Time) (domain.Invocation, error)
	SettleInvocation(context.Context, string, string, int64, domain.InvocationStatus, int64, []byte, []byte, []byte, time.Time) (domain.Invocation, error)
	ReapInvocationLease(context.Context, string, int64, int64, []byte, time.Time) (domain.Invocation, error)
}

// RuntimeAuthenticator turns a presented bearer secret into the durable scope
// and permissions used by Runtime services. Implementations own credential
// verification; request bodies never supply Account identity.
type RuntimeAuthenticator interface {
	Authenticate(context.Context, string) (domain.RuntimeAuthContext, error)
}

type InvocationStateRepository interface {
	AppendInvocationState(context.Context, domain.InvocationState) error
	GetCurrentInvocationState(context.Context, string) (domain.InvocationState, error)
	ListInvocationStates(context.Context, string) ([]domain.InvocationState, error)
}

const InvocationExecutionQueue = "invocation_execution"

// WorkSignaller reduces queue latency. Postgres remains authoritative, so
// callers must remain correct when notifications are absent or duplicated.
type WorkSignaller interface {
	Notify(context.Context, string)
	Subscribe(context.Context, []string) WorkSubscription
}

type WorkSubscription interface {
	Wait(context.Context, time.Duration) bool
	Close()
}

// InvocationExecutor performs one claimed execution. Implementations must
// honor context cancellation and return only the desired terminal result; the
// ownership service performs the fenced durable settlement.
type InvocationExecutor interface {
	Execute(context.Context, domain.InvocationClaim) (domain.InvocationExecutionResult, error)
}

// ModelGenerator performs one tool-free provider call. Implementations receive
// only normalized durable inputs and return no raw provider envelope.
type ModelGenerator interface {
	Generate(context.Context, domain.GenerationRequest) (domain.GenerationResponse, error)
}
