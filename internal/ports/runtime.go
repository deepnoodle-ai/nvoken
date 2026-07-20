package ports

import (
	"context"
	"errors"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrRetryable       = errors.New("retryable database conflict")
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
	GetInvocationByIdempotencyKey(context.Context, string, string, string, string) (domain.Invocation, error)
	GetNonterminalInvocationBySession(context.Context, string) (domain.Invocation, error)
	LockInvocationAdmissionKey(context.Context, string) error
	UpdateInvocationStatus(context.Context, string, domain.InvocationStatus, int64, []byte, *time.Time) error
}

// RuntimeAuthenticator turns a presented bearer secret into the durable scope
// and permissions used by Runtime services. Implementations own credential
// verification; request bodies never supply Account identity.
type RuntimeAuthenticator interface {
	Authenticate(context.Context, string) (domain.RuntimeAuthContext, error)
}

type InvocationStateRepository interface {
	AppendInvocationState(context.Context, domain.InvocationState) error
	ListInvocationStates(context.Context, string) ([]domain.InvocationState, error)
}
