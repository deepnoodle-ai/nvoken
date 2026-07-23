package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
)

var (
	ErrNotFound                   = errors.New("not found")
	ErrUnauthenticated            = errors.New("unauthenticated")
	ErrRetryable                  = errors.New("retryable infrastructure failure")
	ErrConcurrentAdmission        = errors.New("concurrent admission conflict")
	ErrLeaseLost                  = errors.New("invocation lease lost")
	ErrProviderUnsupported        = errors.New("model provider unsupported")
	ErrProviderKeyMissing         = errors.New("model provider credential missing")
	ErrCredentialUnavailable      = errors.New("model provider credential unavailable")
	ErrPlatformFundingDenied      = errors.New("platform funding denied")
	ErrProviderCredentialConflict = errors.New("model provider credential conflict")
	ErrGenerationFailed           = errors.New("model generation failed")
	ErrGenerationInputInvalid     = errors.New("durable model generation input invalid")
	ErrGenerationRecoveryInvalid  = errors.New("durable generation recovery invalid")
	ErrModelResponseInvalid       = errors.New("model response invalid")
	ErrExecutionResultInvalid     = errors.New("invocation execution result invalid")
	ErrDispatchLeaseLost          = errors.New("execution dispatch publication lease lost")
	ErrTaskAlreadyExists          = errors.New("task already exists")
	ErrDispatchAttemptActive      = errors.New("execution dispatch attempt already active")
	ErrDispatchAttemptPending     = errors.New("execution dispatch attempt decision pending")
	ErrToolCallConflict           = errors.New("tool call identity or outcome conflict")
	ErrToolCallNotRunnable        = errors.New("tool call is not runnable")
	ErrCallbackDeliveryLeaseLost  = errors.New("callback delivery lease lost")
)

type ProviderFailureClass string

const (
	ProviderFailureConfiguration       ProviderFailureClass = "configuration"
	ProviderFailureCanceled            ProviderFailureClass = "canceled"
	ProviderFailureThrottled           ProviderFailureClass = "throttled"
	ProviderFailureUpstreamRejected    ProviderFailureClass = "upstream_rejected"
	ProviderFailureUpstreamUnavailable ProviderFailureClass = "upstream_unavailable"
	ProviderFailureTimeoutOrTransport  ProviderFailureClass = "timeout_or_transport"
	ProviderFailureInvalidResponse     ProviderFailureClass = "invalid_response"
	ProviderFailureUnknown             ProviderFailureClass = "unknown"
)

// ProviderCallError preserves a bounded provider failure class after an
// adapter discards the unsafe upstream error body.
type ProviderCallError struct {
	Class ProviderFailureClass
}

func (e *ProviderCallError) Error() string { return "model provider call failed" }

func (e *ProviderCallError) Unwrap() error { return ErrGenerationFailed }

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

// ReadSnapshotManager runs every repository read in fn against one
// repeatable-read snapshot, so a multi-query read cannot mix database states.
// Implemented by transaction managers that can offer snapshot isolation;
// callers fall back to sequential reads when the capability is absent.
type ReadSnapshotManager interface {
	WithReadSnapshot(ctx context.Context, fn func(context.Context) error) error
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
	TenantKey              *string
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
	ListSessionMessagesByInvocation(context.Context, string) ([]domain.SessionMessage, error)
}

// GenerationContextRepository returns the canonical message subset eligible
// for a provider request. Public transcript reads use SessionMessageRepository
// and remain lossless.
type GenerationContextRepository interface {
	ListSessionMessagesForGeneration(context.Context, string) ([]domain.SessionMessage, error)
}

type InvocationRepository interface {
	CreateInvocation(context.Context, domain.Invocation) error
	GetInvocation(context.Context, string) (domain.Invocation, error)
	GetInvocationForUpdate(context.Context, string) (domain.Invocation, error)
	FindNextQueuedInvocationForUpdate(context.Context, time.Time) (domain.Invocation, error)
	ListExpiredInvocationLeases(context.Context, time.Time, int) ([]domain.Invocation, error)
	ListExpiredInvocationDeadlines(context.Context, time.Time, int) ([]domain.Invocation, error)
	GetInvocationByIdempotencyKey(context.Context, string, string, string, string) (domain.Invocation, error)
	GetNonterminalInvocationBySession(context.Context, string) (domain.Invocation, error)
	LockInvocationAdmissionKey(context.Context, string) error
	ClaimInvocation(context.Context, string, string, time.Time, int64, time.Time, time.Time, string) (domain.Invocation, error)
	RenewInvocationLease(context.Context, string, string, int64, time.Time, time.Time) (domain.Invocation, error)
	SettleInvocation(context.Context, string, string, int64, domain.InvocationStatus, int64, []byte, []byte, []byte, []byte, []byte, time.Time) (domain.Invocation, error)
	ParkInvocationForHostTools(context.Context, string, string, int64, int64, time.Time) (domain.Invocation, error)
	QueueWaitingInvocation(context.Context, string, int64, time.Time) (domain.Invocation, error)
	RecoverInvocationLease(context.Context, string, int64, int64, time.Time) (domain.Invocation, error)
	CancelInvocation(context.Context, string, int64, time.Time) (domain.Invocation, error)
	ReapInvocationDeadline(context.Context, string, int64, []byte, time.Time) (domain.Invocation, error)
	FindQueuedInvocationWithoutActiveDispatchForUpdate(context.Context, time.Time) (domain.Invocation, error)
}

type ProviderCredentialListQuery struct {
	AccountID         string
	TenantPartitionID *string
	Provider          *string
	Scope             *domain.ProviderCredentialScope
	Status            *domain.ProviderCredentialStatus
	Limit             int
}

type ProviderCredentialRepository interface {
	CreateProviderCredential(context.Context, domain.ProviderCredential) error
	CreateProviderCredentialVersion(context.Context, domain.ProviderCredentialVersion) error
	GetProviderCredential(context.Context, string) (domain.ProviderCredential, error)
	GetProviderCredentialForUpdate(context.Context, string) (domain.ProviderCredential, error)
	GetProviderCredentialVersion(context.Context, string) (domain.ProviderCredentialVersion, error)
	GetProviderCredentialByCreateIdempotencyKey(context.Context, string, string) (domain.ProviderCredential, error)
	GetProviderCredentialVersionByRotationIdempotencyKey(context.Context, string, string) (domain.ProviderCredentialVersion, error)
	GetActiveProviderCredential(context.Context, string, *string, string) (domain.ProviderCredential, error)
	ListProviderCredentials(context.Context, ProviderCredentialListQuery) ([]domain.ProviderCredential, error)
	ActivateProviderCredentialVersion(context.Context, string, string, int, *time.Time, time.Time) (domain.ProviderCredential, error)
	RevokeProviderCredential(context.Context, string, time.Time) (domain.ProviderCredential, error)
	CreateInvocationProviderCredential(context.Context, domain.InvocationProviderCredential) error
	GetInvocationProviderCredential(context.Context, string, string) (domain.InvocationProviderCredential, error)
	ClearExpiredProviderCredentialMaterial(context.Context, time.Time, int) (int64, error)
}

type CredentialCipher interface {
	Encrypt([]byte, []byte) (domain.EncryptedCredential, error)
	Decrypt(domain.EncryptedCredential, []byte) ([]byte, error)
}

type ProviderCredentialResolver interface {
	ResolveProviderCredential(context.Context, string, string) (domain.ResolvedProviderCredential, error)
}

type PlatformFundingGate interface {
	AuthorizePlatformModelCall(context.Context, string, string, string, string) error
}

// RuntimeAuthenticator turns a presented bearer secret into the durable scope
// and permissions used by Runtime services. Implementations own credential
// verification; request bodies never supply Account identity.
type RuntimeAuthenticator interface {
	Authenticate(context.Context, string) (domain.RuntimeAuthContext, error)
}

// IdentityRepository persists the installation-owned identity substrate. The
// portable HTTP API exposes credentials, not membership mutation.
type IdentityRepository interface {
	CreateOperatorSubject(context.Context, domain.OperatorSubject) error
	GetOperatorSubject(context.Context, string) (domain.OperatorSubject, error)
	GetOperatorSubjectByIdentity(context.Context, string, string, string) (domain.OperatorSubject, error)
	UpsertMembership(context.Context, domain.Membership) (domain.Membership, error)
	GetMembershipBySubject(context.Context, string, string) (domain.Membership, error)
	DeleteMembershipBySubject(context.Context, string, string) error
	CreateCredential(context.Context, domain.Credential) error
	GetCredential(context.Context, string) (domain.Credential, error)
	GetCredentialForUpdate(context.Context, string) (domain.Credential, error)
	GetCredentialByPrefix(context.Context, string) (domain.Credential, error)
	ListCredentials(context.Context, string) ([]domain.Credential, error)
	TouchCredential(context.Context, string, time.Time) error
	RevokeCredential(context.Context, string, string, time.Time) (domain.Credential, error)
	SetCredentialRotationOverlap(context.Context, string, string, time.Time, time.Time) (domain.Credential, error)
	CreateCredentialIssuance(context.Context, domain.CredentialIssuance) error
	GetCredentialIssuance(context.Context, string, string, string) (domain.CredentialIssuance, error)
	ClearExpiredCredentialIssuance(context.Context, string, string, string, time.Time) error
	GetStaticCredentialImport(context.Context, string, string) (string, error)
	CreateStaticCredentialImport(context.Context, string, string, string, time.Time) error
	CreateDeviceAuthorization(context.Context, domain.DeviceAuthorization) error
	GetDeviceAuthorizationByDeviceCodeForUpdate(context.Context, []byte) (domain.DeviceAuthorization, error)
	GetDeviceAuthorizationByUserCodeForUpdate(context.Context, []byte) (domain.DeviceAuthorization, error)
	RecordDevicePoll(context.Context, domain.DeviceAuthorization) (domain.DeviceAuthorization, error)
	ApproveDeviceAuthorization(context.Context, domain.DeviceAuthorization) (domain.DeviceAuthorization, error)
	DenyDeviceAuthorization(context.Context, string, time.Time) (domain.DeviceAuthorization, error)
	ExchangeDeviceAuthorization(context.Context, string, time.Time) (domain.DeviceAuthorization, error)
	IncrementDeviceConfirmationAttempts(context.Context, string, time.Time) (domain.DeviceAuthorization, error)
	CreateBrowserSession(context.Context, domain.BrowserSession) error
	GetBrowserSessionByTokenHash(context.Context, []byte) (domain.BrowserSession, error)
	DeleteBrowserSession(context.Context, string) error
}

type InvocationStateRepository interface {
	AppendInvocationState(context.Context, domain.InvocationState) error
	GetCurrentInvocationState(context.Context, string) (domain.InvocationState, error)
	ListInvocationStates(context.Context, string) ([]domain.InvocationState, error)
}

// ToolCallRepository persists only lifecycle and transcript references. Tool
// request/result payloads remain canonical in SessionMessage.
type ToolCallRepository interface {
	CreateToolCall(context.Context, domain.ToolCall) error
	GetToolCall(context.Context, string) (domain.ToolCall, error)
	GetToolCallForUpdate(context.Context, string) (domain.ToolCall, error)
	GetToolCallByProviderIdentityForUpdate(context.Context, string, int, string) (domain.ToolCall, error)
	ListOpenToolCallsForUpdate(context.Context, string) ([]domain.ToolCall, error)
	ListToolCallsByInvocation(context.Context, string) ([]domain.ToolCall, error)
	ListToolCallsByIteration(context.Context, string, int) ([]domain.ToolCall, error)
	StartToolCallAttempt(context.Context, string, time.Time) (domain.ToolCall, error)
	RestartToolCallAttempt(context.Context, string, time.Time) (domain.ToolCall, error)
	GetCurrentToolCallAttemptForUpdate(context.Context, string, int) (domain.ToolCallAttempt, error)
	CreateToolCallAttempt(context.Context, domain.ToolCallAttempt) error
	SettleToolCall(context.Context, string, domain.ToolCallStatus, domain.ToolCallResultOrigin, string, int64, time.Time) (domain.ToolCall, error)
	SettleToolCallAttempt(context.Context, string, domain.ToolCallStatus, time.Time) (domain.ToolCallAttempt, error)
	SettleRunningToolCallAttempts(context.Context, string, domain.ToolCallStatus, time.Time) (int64, error)
	CreateModelUsageReceipt(context.Context, domain.ModelUsageReceipt) error
	GetModelUsageReceiptByIteration(context.Context, string, int) (domain.ModelUsageReceipt, error)
	ListModelUsageReceipts(context.Context, string) ([]domain.ModelUsageReceipt, error)
	CreateInvocationCheckpoint(context.Context, domain.InvocationCheckpoint) error
	GetLatestInvocationCheckpoint(context.Context, string) (domain.InvocationCheckpoint, error)
	ListInvocationCheckpoints(context.Context, string) ([]domain.InvocationCheckpoint, error)
	AdvanceInvocationCheckpoint(context.Context, string, string, int64, time.Time, int64, int) (domain.Invocation, error)
	AdvanceWaitingInvocationCheckpoint(context.Context, string, int64, int64, int, time.Time) (domain.Invocation, error)
	AdvanceInvocationCheckpointForTerminal(context.Context, string, int64, int) (domain.Invocation, error)
}

type CallbackDeliveryRepository interface {
	CreateCallbackDelivery(context.Context, domain.CallbackDelivery) error
	GetCallbackDelivery(context.Context, string) (domain.CallbackDelivery, error)
	GetCallbackDeliveryForUpdate(context.Context, string) (domain.CallbackDelivery, error)
	ActivateCallbackDeliveries(context.Context, string, time.Time) (int64, error)
	ClaimNextCallbackDelivery(context.Context, string, time.Time, time.Time) (domain.CallbackDelivery, error)
	ReturnCallbackDeliveryPending(context.Context, string, string, int64, time.Time, string, time.Time) (domain.CallbackDelivery, error)
	SettleCallbackDelivery(context.Context, string, string, int64, domain.CallbackDeliveryStatus, string, *int, time.Time) (domain.CallbackDelivery, error)
	AbandonActiveCallbackDeliveries(context.Context, string, string, time.Time) (int64, error)
	RecoverExpiredCallbackDeliveries(context.Context, time.Time, int) (int64, error)
	PruneTerminalCallbackDeliveries(context.Context, time.Time, int) (int64, error)
}

type CallbackTransportRequest struct {
	EndpointURL string
	DeliveryID  string
	ToolCallID  string
	Body        json.RawMessage
}

type CallbackTransportResult struct {
	StatusCode  int
	ContentType string
	Body        []byte
	ErrorCode   string
	Retryable   bool
}

type CallbackTransport interface {
	Send(context.Context, CallbackTransportRequest) CallbackTransportResult
}

// ToolCallCoordinator is the durable boundary used by model adapters and
// trusted builtins. Every method verifies Invocation ownership in Postgres.
type ToolCallCoordinator interface {
	RecordModelCheckpoint(context.Context, domain.InvocationClaim, domain.ModelCheckpointInput) (domain.ModelCheckpointResult, error)
	StartBuiltinToolCall(context.Context, domain.InvocationClaim, int, string) (domain.ToolCallExecution, error)
	AcceptBuiltinToolResult(context.Context, domain.InvocationClaim, domain.ToolCallExecution, json.RawMessage, bool) (domain.ToolCall, error)
}

type ExecutionDispatchRepository interface {
	CreateSyntheticDispatchWork(context.Context, domain.SyntheticDispatchWork) error
	GetSyntheticDispatchWork(context.Context, string) (domain.SyntheticDispatchWork, error)
	GetSyntheticDispatchWorkForUpdate(context.Context, string) (domain.SyntheticDispatchWork, error)
	SettleSyntheticDispatchWork(context.Context, string, time.Time) (domain.SyntheticDispatchWork, error)
	CreateExecutionDispatch(context.Context, domain.ExecutionDispatch) error
	GetExecutionDispatch(context.Context, string) (domain.ExecutionDispatch, error)
	GetExecutionDispatchForUpdate(context.Context, string) (domain.ExecutionDispatch, error)
	ClaimNextExecutionDispatch(context.Context, string, string, time.Time, time.Time) (domain.ExecutionDispatch, error)
	RenewExecutionDispatchPublication(context.Context, string, string, int64, time.Time, time.Time) (domain.ExecutionDispatch, error)
	MarkExecutionDispatchPublished(context.Context, string, string, int64, string, time.Time) (domain.ExecutionDispatch, error)
	ReturnExecutionDispatchPending(context.Context, string, string, int64, time.Time, string, time.Time) (domain.ExecutionDispatch, error)
	SettleExecutionDispatch(context.Context, string, time.Time) (domain.ExecutionDispatch, error)
	SettleActiveExecutionDispatchForWork(context.Context, domain.ExecutionDispatchKind, string, time.Time) (int64, error)
	AbandonExecutionDispatch(context.Context, string, string, time.Time) (domain.ExecutionDispatch, error)
	ListAgedExecutionDispatches(context.Context, time.Time, int) ([]domain.ExecutionDispatch, error)
	ListAlertableAgedExecutionDispatches(context.Context, time.Time, time.Time, int) ([]domain.ExecutionDispatch, error)
	ListStalePublishedExecutionDispatches(context.Context, time.Time, int) ([]domain.ExecutionDispatch, error)
	PruneTerminalExecutionDispatches(context.Context, time.Time, int) (int64, error)
}

type ExecutionTask struct {
	DispatchID  string
	AvailableAt time.Time
}

// ExecutionTaskQueue is transport only. Postgres dispatch and domain rows
// remain authoritative when tasks are delayed, duplicated, or absent.
type ExecutionTaskQueue interface {
	CreateTask(context.Context, ExecutionTask) (string, error)
	TaskExists(context.Context, string) (bool, error)
	Close() error
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

// CancellationSignaller lowers cancellation latency across service instances.
// The Invocation row and lease fence remain authoritative when a notification
// is lost, duplicated, or delivered to a process without the active claim.
type CancellationSignaller interface {
	NotifyCancellation(context.Context, string)
	SubscribeCancellations(context.Context) CancellationSubscription
}

type CancellationSubscription interface {
	Wait(context.Context, time.Duration) (string, bool)
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

// ModelPricingResolver reports the exact local registry's USD pricing
// capability before a provider call. Unpriced is authoritative for capped work;
// unknown means the adapter cannot decide until normalized response evidence is
// available.
type ModelPricingResolver interface {
	ResolveModelPricing(provider, model string) domain.ModelPricingCapability
}
