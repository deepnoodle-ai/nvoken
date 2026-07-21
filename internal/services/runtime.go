package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	MaxInvocationBodyBytes             = 1 << 20
	MaxInputBlocks                     = 64
	MaxReferenceCharacters             = 255
	MaxClientTools                     = 32
	MaxClientToolNameBytes             = 64
	MaxClientToolDescriptionCharacters = 4096
)

type TextInputBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type InvocationInput struct {
	Content []TextInputBlock `json:"content"`
}

type ModelSelection struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

type InlineExecutionSpec struct {
	Instructions string                 `json:"instructions"`
	Model        ModelSelection         `json:"model"`
	Budgets      *InvocationBudgetInput `json:"budgets,omitempty"`
	Output       *StructuredOutputSpec  `json:"output,omitempty"`
	Tools        []ClientToolSpec       `json:"tools,omitempty"`
}

type ClientToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Mode        string          `json:"mode"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type StructuredOutputSpec struct {
	Schema json.RawMessage `json:"schema"`
}

type CreateInvocationInput struct {
	AgentRef       string              `json:"agent_ref"`
	TenantRef      *string             `json:"tenant_ref,omitempty"`
	SessionID      *string             `json:"session_id,omitempty"`
	SessionKey     *string             `json:"session_key,omitempty"`
	IdempotencyKey string              `json:"idempotency_key"`
	Input          InvocationInput     `json:"input"`
	Spec           InlineExecutionSpec `json:"spec"`
}

type InvocationAcknowledgement struct {
	AgentID      string
	SessionID    string
	InvocationID string
	Status       domain.InvocationStatus
	Deduplicated bool
}

type InvocationRead struct {
	ID                  string
	AgentID             string
	SessionID           string
	Status              domain.InvocationStatus
	Error               json.RawMessage
	Usage               json.RawMessage
	Provenance          json.RawMessage
	Output              json.RawMessage
	OutputProvenance    json.RawMessage
	Budgets             InvocationBudgetRead
	ActiveExecutionMS   int64
	WallClockDeadlineAt time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time
	PendingToolCalls    []PendingClientToolCall
}

type SessionRead struct {
	ID                     string
	AgentID                string
	TenantRef              *string
	SessionKey             *string
	ActiveInvocationID     *string
	ActiveInvocationStatus *domain.InvocationStatus
	CreatedAt              time.Time
	UpdatedAt              time.Time
	PendingToolCalls       []PendingClientToolCall
}

type admissionStore interface {
	ports.AccountRepository
	ports.TenantPartitionRepository
	ports.AgentRepository
	ports.SessionRepository
	ports.ExecutionSpecSnapshotRepository
	ports.SessionMessageRepository
	ports.InvocationRepository
	ports.InvocationStateRepository
	ports.RecoveryRepository
	ports.ExecutionDispatchRepository
	ports.ToolCallRepository
}

type InvocationExecutionMode string

const (
	InvocationExecutionEmbedded   InvocationExecutionMode = "embedded"
	InvocationExecutionCloudTasks InvocationExecutionMode = "cloud_tasks"
)

type RuntimeService struct {
	store         admissionStore
	txm           ports.TransactionManager
	clock         ports.Clock
	ids           ports.IDGenerator
	signaller     ports.WorkSignaller
	cancellations ports.CancellationSignaller
	budgetPolicy  BudgetPolicy
	logger        *slog.Logger
	executionMode InvocationExecutionMode
	dispatchQueue string
}

type RuntimeOption func(*RuntimeService)

func WithWorkSignaller(signaller ports.WorkSignaller) RuntimeOption {
	return func(service *RuntimeService) { service.signaller = signaller }
}

func WithCancellationSignaller(signaller ports.CancellationSignaller) RuntimeOption {
	return func(service *RuntimeService) { service.cancellations = signaller }
}

func WithBudgetPolicy(policy BudgetPolicy) RuntimeOption {
	return func(service *RuntimeService) { service.budgetPolicy = policy }
}

func WithRuntimeLogger(logger *slog.Logger) RuntimeOption {
	return func(service *RuntimeService) {
		if logger != nil {
			service.logger = logger
		}
	}
}

func WithInvocationExecutionMode(mode InvocationExecutionMode, dispatchQueue string) RuntimeOption {
	return func(service *RuntimeService) {
		service.executionMode = mode
		service.dispatchQueue = dispatchQueue
	}
}

func NewRuntimeService(
	store admissionStore,
	txm ports.TransactionManager,
	clock ports.Clock,
	ids ports.IDGenerator,
	options ...RuntimeOption,
) *RuntimeService {
	service := &RuntimeService{
		store: store, txm: txm, clock: clock, ids: ids,
		budgetPolicy: DefaultBudgetPolicy(), logger: slog.Default(),
		executionMode: InvocationExecutionEmbedded,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func ValidateCreateInvocation(input CreateInvocationInput) error {
	if err := validateBoundedString("agent_ref", input.AgentRef, MaxReferenceCharacters); err != nil {
		return err
	}
	if err := validateBoundedString("idempotency_key", input.IdempotencyKey, MaxReferenceCharacters); err != nil {
		return err
	}
	if input.TenantRef != nil {
		if err := validateBoundedString("tenant_ref", *input.TenantRef, MaxReferenceCharacters); err != nil {
			return err
		}
	}
	if input.SessionID != nil && input.SessionKey != nil {
		return invalidRequest("Supply at most one of session_id and session_key.")
	}
	if input.SessionID != nil && !domain.ValidStableID(*input.SessionID, domain.PrefixSession) {
		return invalidRequest("session_id is invalid.")
	}
	if input.SessionKey != nil {
		if err := validateBoundedString("session_key", *input.SessionKey, MaxReferenceCharacters); err != nil {
			return err
		}
	}
	if len(input.Input.Content) == 0 || len(input.Input.Content) > MaxInputBlocks {
		return invalidRequest(fmt.Sprintf("input.content must contain between 1 and %d blocks.", MaxInputBlocks))
	}
	for index, block := range input.Input.Content {
		if block.Type != "text" {
			return invalidRequest(fmt.Sprintf("input.content[%d].type must be text.", index))
		}
		if !utf8.ValidString(block.Text) || strings.TrimSpace(block.Text) == "" {
			return invalidRequest(fmt.Sprintf("input.content[%d].text must not be blank.", index))
		}
	}
	if !utf8.ValidString(input.Spec.Instructions) || strings.TrimSpace(input.Spec.Instructions) == "" {
		return invalidRequest("spec.instructions must not be blank.")
	}
	if err := validateBoundedString("spec.model.provider", input.Spec.Model.Provider, MaxReferenceCharacters); err != nil {
		return err
	}
	if err := validateBoundedString("spec.model.name", input.Spec.Model.Name, MaxReferenceCharacters); err != nil {
		return err
	}
	if err := validateRequestedBudgets(input.Spec.Budgets); err != nil {
		return err
	}
	if input.Spec.Output != nil {
		if _, err := structuredOutputSchemaDigest(input.Spec.Output.Schema); err != nil {
			return invalidRequest("spec.output.schema is invalid: " + err.Error() + ".")
		}
	}
	if err := validateClientTools(input.Spec.Tools); err != nil {
		return err
	}
	return nil
}

func validateBoundedString(field, value string, maximum int) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return invalidRequest(field + " must not be blank.")
	}
	if utf8.RuneCountInString(value) > maximum {
		return invalidRequest(fmt.Sprintf("%s must be at most %d Unicode characters.", field, maximum))
	}
	return nil
}

func (s *RuntimeService) Admit(ctx context.Context, auth domain.RuntimeAuthContext, input CreateInvocationInput) (InvocationAcknowledgement, error) {
	if err := s.ready(); err != nil {
		return InvocationAcknowledgement{}, err
	}
	if err := authorize(auth, domain.OperationCreateInvocation); err != nil {
		return InvocationAcknowledgement{}, err
	}
	if err := ValidateCreateInvocation(input); err != nil {
		return InvocationAcknowledgement{}, err
	}
	if auth.TenantConstraint != nil && input.TenantRef != nil && *auth.TenantConstraint != *input.TenantRef {
		return InvocationAcknowledgement{}, forbidden("The requested tenant_ref conflicts with the credential constraint.")
	}
	resolvedBudgets, err := s.budgetPolicy.ResolveForFeatures(
		input.Spec.Budgets,
		input.Spec.Output != nil,
		len(input.Spec.Tools) != 0,
	)
	if err != nil {
		return InvocationAcknowledgement{}, err
	}
	var outputSchemaDigest []byte
	if input.Spec.Output != nil {
		outputSchemaDigest, err = structuredOutputSchemaDigest(input.Spec.Output.Schema)
		if err != nil {
			return InvocationAcknowledgement{}, &PublicError{Code: CodeInternal, Message: "The request could not be completed.", Cause: err}
		}
	}
	fingerprint, err := InvocationFingerprintV4(input)
	if err != nil {
		return InvocationAcknowledgement{}, &PublicError{Code: CodeInternal, Message: "The request could not be completed.", Cause: err}
	}

	for attempt := 0; attempt < 2; attempt++ {
		var acknowledgement InvocationAcknowledgement
		err = s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
			account, err := s.store.GetAccount(txCtx, auth.AccountID)
			if err != nil {
				return fmt.Errorf("resolve authenticated Account: %w", err)
			}
			now := s.clock.Now().UTC()
			agentID, err := s.ids.NewID(domain.PrefixAgent)
			if err != nil {
				return err
			}
			agent, err := s.store.ResolveAgent(txCtx, domain.Agent{
				ID: agentID, AccountID: account.ID, AgentRef: input.AgentRef, CreatedAt: now,
			})
			if err != nil {
				return err
			}

			partition, selectedSession, err := s.resolvePartitionAndSelectedSession(txCtx, account, agent, auth, input, now)
			if err != nil {
				return err
			}
			if err := s.store.LockInvocationAdmissionKey(txCtx, invocationAdmissionLockKey(account.ID, partition.ID, agent.ID, input.IdempotencyKey)); err != nil {
				return err
			}
			if existing, found, err := s.findIdempotent(txCtx, account.ID, partition.ID, agent.ID, input.IdempotencyKey, input, fingerprint); err != nil {
				return err
			} else if found {
				acknowledgement = acknowledgementFor(existing, true)
				return nil
			}

			session, err := s.resolveSession(txCtx, account, partition, agent, selectedSession, input, now)
			if err != nil {
				return err
			}
			session, err = s.store.GetSessionForUpdate(txCtx, session.ID)
			if err != nil {
				return err
			}
			if session.AccountID != account.ID || session.TenantPartitionID != partition.ID || session.AgentID != agent.ID {
				return notFound()
			}
			if existing, found, err := s.findIdempotent(txCtx, account.ID, partition.ID, agent.ID, input.IdempotencyKey, input, fingerprint); err != nil {
				return err
			} else if found {
				acknowledgement = acknowledgementFor(existing, true)
				return nil
			}
			active, err := s.store.GetNonterminalInvocationBySession(txCtx, session.ID)
			if err == nil {
				return &PublicError{
					Code: CodeSessionInvocationActive, Message: "This Session already has a nonterminal Invocation.",
					Details: map[string]any{"invocation_id": active.ID, "status": active.Status},
				}
			}
			if !errors.Is(err, ports.ErrNotFound) {
				return err
			}

			sequence, err := s.store.ReserveMessageSequence(txCtx, session.ID)
			if err != nil {
				return err
			}
			revision, err := s.store.ReserveLifecycleRevision(txCtx, session.ID)
			if err != nil {
				return err
			}
			ids, err := s.newAdmissionIDs(s.executionMode == InvocationExecutionCloudTasks)
			if err != nil {
				return err
			}
			specJSON, err := json.Marshal(input.Spec)
			if err != nil {
				return err
			}
			contentJSON, err := json.Marshal(input.Input.Content)
			if err != nil {
				return err
			}
			snapshot := domain.ExecutionSpecSnapshot{
				ID:        ids.snapshot,
				AccountID: account.ID,
				Spec:      specJSON,
				CreatedAt: now,
			}
			invocation := domain.Invocation{
				ID:                     ids.invocation,
				SessionID:              session.ID,
				AccountID:              account.ID,
				TenantPartitionID:      partition.ID,
				AgentID:                agent.ID,
				SpecSnapshotID:         snapshot.ID,
				IdempotencyKey:         input.IdempotencyKey,
				RequestFingerprint:     fingerprint[:],
				FingerprintVersion:     4,
				Status:                 domain.InvocationQueued,
				CurrentStateRevision:   revision,
				WallClockTimeoutMS:     resolvedBudgets.WallClockTimeout.Milliseconds(),
				ActiveTimeoutMS:        resolvedBudgets.ActiveExecutionTimeout.Milliseconds(),
				MaxOutputTokens:        resolvedBudgets.MaxOutputTokens,
				MaxEstimatedCostMicros: resolvedBudgets.MaxEstimatedCostMicros,
				MaxIterations:          resolvedBudgets.MaxIterations,
				OutputSchemaDigest:     outputSchemaDigest,
				WallClockDeadlineAt:    now.Add(resolvedBudgets.WallClockTimeout),
				CreatedAt:              now,
				UpdatedAt:              now,
			}
			message := domain.SessionMessage{
				ID:                ids.message,
				SessionID:         session.ID,
				AccountID:         account.ID,
				TenantPartitionID: partition.ID,
				AgentID:           agent.ID,
				InvocationID:      invocation.ID,
				Sequence:          sequence,
				Role:              domain.MessageRoleUser,
				Content:           contentJSON,
				CreatedAt:         now,
			}
			state := domain.InvocationState{
				ID:                     ids.state,
				InvocationID:           invocation.ID,
				SessionID:              session.ID,
				AccountID:              account.ID,
				TenantPartitionID:      partition.ID,
				AgentID:                agent.ID,
				Revision:               revision,
				Status:                 domain.InvocationQueued,
				ThroughMessageSequence: &sequence,
				CreatedAt:              now,
			}
			if err := s.store.CreateExecutionSpecSnapshot(txCtx, snapshot); err != nil {
				return err
			}
			if err := s.store.CreateInvocation(txCtx, invocation); err != nil {
				return err
			}
			if err := s.store.AppendSessionMessage(txCtx, message); err != nil {
				return err
			}
			if err := s.store.AppendInvocationState(txCtx, state); err != nil {
				return err
			}
			if s.executionMode == InvocationExecutionCloudTasks {
				accountID := account.ID
				partitionID := partition.ID
				if err := s.store.CreateExecutionDispatch(txCtx, domain.ExecutionDispatch{
					ID: ids.dispatch, Kind: domain.ExecutionDispatchInvocation, WorkID: invocation.ID,
					AccountID: &accountID, TenantPartitionID: &partitionID,
					Queue: s.dispatchQueue, Status: domain.ExecutionDispatchPending,
					AvailableAt: now, CreatedAt: now, UpdatedAt: now,
				}); err != nil {
					return err
				}
			}
			acknowledgement = acknowledgementFor(invocation, false)
			return nil
		})
		if err == nil {
			if !acknowledgement.Deduplicated && s.executionMode == InvocationExecutionEmbedded && s.signaller != nil {
				s.signaller.Notify(ctx, ports.InvocationExecutionQueue)
			}
			return acknowledgement, nil
		}
		if errors.Is(err, ports.ErrConcurrentAdmission) && attempt == 0 && ctx.Err() == nil {
			continue
		}
		if errors.Is(err, ports.ErrRetryable) || errors.Is(err, ports.ErrConcurrentAdmission) {
			return InvocationAcknowledgement{}, &PublicError{
				Code: CodeUnavailable, Message: "The service is temporarily unavailable.", Cause: err,
			}
		}
		return InvocationAcknowledgement{}, err
	}
	return InvocationAcknowledgement{}, &PublicError{
		Code: CodeUnavailable, Message: "The service is temporarily unavailable.",
	}
}

func (s *RuntimeService) resolvePartitionAndSelectedSession(
	ctx context.Context,
	account domain.Account,
	agent domain.Agent,
	auth domain.RuntimeAuthContext,
	input CreateInvocationInput,
	now time.Time,
) (domain.TenantPartition, *domain.Session, error) {
	if input.SessionID != nil {
		session, err := s.store.GetSession(ctx, *input.SessionID)
		if errors.Is(err, ports.ErrNotFound) {
			return domain.TenantPartition{}, nil, notFound()
		}
		if err != nil {
			return domain.TenantPartition{}, nil, err
		}
		if session.AccountID != account.ID || session.AgentID != agent.ID {
			return domain.TenantPartition{}, nil, notFound()
		}
		partition, err := s.store.GetTenantPartition(ctx, session.TenantPartitionID)
		if errors.Is(err, ports.ErrNotFound) {
			return domain.TenantPartition{}, nil, notFound()
		}
		if err != nil {
			return domain.TenantPartition{}, nil, err
		}
		if partition.AccountID != account.ID || !tenantMatches(auth.TenantConstraint, partition.TenantRef) || !tenantMatches(input.TenantRef, partition.TenantRef) {
			return domain.TenantPartition{}, nil, notFound()
		}
		return partition, &session, nil
	}

	effectiveTenant := auth.TenantConstraint
	if effectiveTenant == nil {
		effectiveTenant = input.TenantRef
	}
	if effectiveTenant == nil {
		partition, err := s.store.GetDefaultTenantPartition(ctx, account.ID)
		return partition, nil, err
	}
	partitionID, err := s.ids.NewID(domain.PrefixTenantPartition)
	if err != nil {
		return domain.TenantPartition{}, nil, err
	}
	partition, err := s.store.ResolveTenantPartition(ctx, domain.TenantPartition{
		ID: partitionID, AccountID: account.ID, TenantRef: cloneString(effectiveTenant), CreatedAt: now,
	})
	return partition, nil, err
}

func (s *RuntimeService) resolveSession(
	ctx context.Context,
	account domain.Account,
	partition domain.TenantPartition,
	agent domain.Agent,
	selected *domain.Session,
	input CreateInvocationInput,
	now time.Time,
) (domain.Session, error) {
	if selected != nil {
		return *selected, nil
	}
	sessionID, err := s.ids.NewID(domain.PrefixSession)
	if err != nil {
		return domain.Session{}, err
	}
	session := domain.Session{
		ID: sessionID, AccountID: account.ID, TenantPartitionID: partition.ID, AgentID: agent.ID,
		SessionKey: cloneString(input.SessionKey), NextMessageSequence: 1, NextLifecycleRevision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if input.SessionKey != nil {
		return s.store.ResolveSessionByKey(ctx, session)
	}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

func (s *RuntimeService) GetInvocation(ctx context.Context, auth domain.RuntimeAuthContext, invocationID string) (InvocationRead, error) {
	if err := s.ready(); err != nil {
		return InvocationRead{}, err
	}
	if err := authorize(auth, domain.OperationGetInvocation); err != nil {
		return InvocationRead{}, err
	}
	if !domain.ValidStableID(invocationID, domain.PrefixInvocation) {
		return InvocationRead{}, invalidRequest("invocation_id is invalid.")
	}
	invocation, err := s.store.GetInvocation(ctx, invocationID)
	if errors.Is(err, ports.ErrNotFound) {
		return InvocationRead{}, notFound()
	}
	if err != nil {
		return InvocationRead{}, err
	}
	if invocation.AccountID != auth.AccountID {
		return InvocationRead{}, notFound()
	}
	partition, err := s.store.GetTenantPartition(ctx, invocation.TenantPartitionID)
	if err != nil || partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantRef) {
		if errors.Is(err, ports.ErrNotFound) || err == nil {
			return InvocationRead{}, notFound()
		}
		return InvocationRead{}, err
	}
	read := invocationReadFromDomain(invocation)
	read.PendingToolCalls, err = s.pendingClientToolCalls(ctx, invocation)
	if err != nil {
		return InvocationRead{}, err
	}
	return read, nil
}

func (s *RuntimeService) GetSession(ctx context.Context, auth domain.RuntimeAuthContext, sessionID string) (SessionRead, error) {
	if err := s.ready(); err != nil {
		return SessionRead{}, err
	}
	if err := authorize(auth, domain.OperationGetSession); err != nil {
		return SessionRead{}, err
	}
	if !domain.ValidStableID(sessionID, domain.PrefixSession) {
		return SessionRead{}, invalidRequest("session_id is invalid.")
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if errors.Is(err, ports.ErrNotFound) {
		return SessionRead{}, notFound()
	}
	if err != nil {
		return SessionRead{}, err
	}
	if session.AccountID != auth.AccountID {
		return SessionRead{}, notFound()
	}
	partition, err := s.store.GetTenantPartition(ctx, session.TenantPartitionID)
	if err != nil || partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantRef) {
		if errors.Is(err, ports.ErrNotFound) || err == nil {
			return SessionRead{}, notFound()
		}
		return SessionRead{}, err
	}
	var activeID *string
	var activeStatus *domain.InvocationStatus
	var pending []PendingClientToolCall
	active, err := s.store.GetNonterminalInvocationBySession(ctx, session.ID)
	if err == nil {
		activeID = &active.ID
		activeStatus = &active.Status
		pending, err = s.pendingClientToolCalls(ctx, active)
		if err != nil {
			return SessionRead{}, err
		}
	} else if !errors.Is(err, ports.ErrNotFound) {
		return SessionRead{}, err
	}
	return SessionRead{
		ID:                     session.ID,
		AgentID:                session.AgentID,
		TenantRef:              cloneString(partition.TenantRef),
		SessionKey:             cloneString(session.SessionKey),
		ActiveInvocationID:     activeID,
		ActiveInvocationStatus: activeStatus,
		PendingToolCalls:       pending,
		CreatedAt:              session.CreatedAt,
		UpdatedAt:              session.UpdatedAt,
	}, nil
}

func invocationReadFromDomain(invocation domain.Invocation) InvocationRead {
	return InvocationRead{
		ID:                  invocation.ID,
		AgentID:             invocation.AgentID,
		SessionID:           invocation.SessionID,
		Status:              invocation.Status,
		Error:               invocation.Error,
		Usage:               invocation.Usage,
		Provenance:          invocation.Provenance,
		Output:              invocation.Output,
		OutputProvenance:    invocation.OutputProvenance,
		Budgets:             budgetReadFromDomain(invocation),
		ActiveExecutionMS:   invocation.ActiveExecutionMS,
		WallClockDeadlineAt: invocation.WallClockDeadlineAt,
		CreatedAt:           invocation.CreatedAt,
		UpdatedAt:           invocation.UpdatedAt,
		CompletedAt:         invocation.CompletedAt,
	}
}

func (s *RuntimeService) ready() error {
	if s == nil || s.store == nil || s.txm == nil || s.clock == nil || s.ids == nil {
		return &PublicError{Code: CodeUnavailable, Message: "The service is temporarily unavailable."}
	}
	if s.executionMode != InvocationExecutionEmbedded && s.executionMode != InvocationExecutionCloudTasks {
		return &PublicError{Code: CodeUnavailable, Message: "The service is temporarily unavailable."}
	}
	if s.executionMode == InvocationExecutionCloudTasks && strings.TrimSpace(s.dispatchQueue) == "" {
		return &PublicError{Code: CodeUnavailable, Message: "The service is temporarily unavailable."}
	}
	return nil
}

func authorize(auth domain.RuntimeAuthContext, operation domain.RuntimeOperation) error {
	if !domain.ValidStableID(auth.AccountID, domain.PrefixAccount) {
		return &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
	}
	if !auth.Allows(operation) {
		return forbidden("The authenticated credential is not permitted to make this request.")
	}
	return nil
}

func invocationAdmissionLockKey(accountID, partitionID, agentID, idempotencyKey string) string {
	hash := sha256.New()
	for _, part := range []string{accountID, partitionID, agentID, idempotencyKey} {
		_, _ = fmt.Fprintf(hash, "%d:", len(part))
		_, _ = hash.Write([]byte(part))
	}
	return "nvoken:invocation-admission:" + hex.EncodeToString(hash.Sum(nil))
}

func (s *RuntimeService) findIdempotent(
	ctx context.Context,
	accountID, partitionID, agentID, key string,
	input CreateInvocationInput,
	fingerprint [sha256.Size]byte,
) (domain.Invocation, bool, error) {
	existing, err := s.store.GetInvocationByIdempotencyKey(ctx, accountID, partitionID, agentID, key)
	if errors.Is(err, ports.ErrNotFound) {
		return domain.Invocation{}, false, nil
	}
	if err != nil {
		return domain.Invocation{}, false, err
	}
	expected := fingerprint[:]
	switch existing.FingerprintVersion {
	case 1:
		if input.Spec.Budgets != nil || input.Spec.Output != nil || len(input.Spec.Tools) != 0 {
			return domain.Invocation{}, false, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used with a different request."}
		}
		legacy, legacyErr := InvocationFingerprintV1(input)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 2:
		if input.Spec.Output != nil || len(input.Spec.Tools) != 0 {
			return domain.Invocation{}, false, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used with a different request."}
		}
		legacy, legacyErr := InvocationFingerprintV2(input)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 3:
		if len(input.Spec.Tools) != 0 {
			return domain.Invocation{}, false, &PublicError{
				Code:    CodeIdempotencyConflict,
				Message: "The idempotency key was already used with a different request.",
			}
		}
		legacy, legacyErr := InvocationFingerprintV3(input)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 4:
	default:
		return domain.Invocation{}, false, &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
	}
	if !bytes.Equal(existing.RequestFingerprint, expected) {
		return domain.Invocation{}, false, &PublicError{
			Code:    CodeIdempotencyConflict,
			Message: "The idempotency key was already used with a different request.",
		}
	}
	return existing, true, nil
}

type admissionIDs struct{ snapshot, invocation, message, state, dispatch string }

func (s *RuntimeService) newAdmissionIDs(includeDispatch bool) (admissionIDs, error) {
	var ids admissionIDs
	var err error
	if ids.snapshot, err = s.ids.NewID(domain.PrefixExecutionSpecSnapshot); err != nil {
		return ids, err
	}
	if ids.invocation, err = s.ids.NewID(domain.PrefixInvocation); err != nil {
		return ids, err
	}
	if ids.message, err = s.ids.NewID(domain.PrefixSessionMessage); err != nil {
		return ids, err
	}
	if ids.state, err = s.ids.NewID(domain.PrefixInvocationState); err != nil {
		return ids, err
	}
	if includeDispatch {
		if ids.dispatch, err = s.ids.NewID(domain.PrefixExecutionDispatch); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func acknowledgementFor(invocation domain.Invocation, deduplicated bool) InvocationAcknowledgement {
	return InvocationAcknowledgement{
		AgentID: invocation.AgentID, SessionID: invocation.SessionID,
		InvocationID: invocation.ID, Status: invocation.Status, Deduplicated: deduplicated,
	}
}

func tenantMatches(asserted, stored *string) bool {
	if asserted == nil {
		return true
	}
	return stored != nil && *asserted == *stored
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
