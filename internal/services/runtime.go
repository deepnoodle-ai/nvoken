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
	MaxInvocationBodyBytes           = 1 << 20
	MaxInputBlocks                   = 64
	MaxReferenceCharacters           = 255
	MaxHostTools                     = 32
	MaxHostToolNameBytes             = 64
	MaxHostToolDescriptionCharacters = 4096
)

type TextInputBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type InvocationInput struct {
	Content []TextInputBlock `json:"content"`
}

func (i *InvocationInput) UnmarshalJSON(payload []byte) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) != 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return err
		}
		i.Content = []TextInputBlock{{Type: "text", Text: text}}
		return nil
	}
	type wire InvocationInput
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	*i = InvocationInput(decoded)
	return nil
}

type ModelSelection struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type InlineExecutionSpec struct {
	Instructions string                `json:"instructions,omitempty"`
	Model        ModelSelection        `json:"model"`
	Limits       *InvocationLimitInput `json:"limits,omitempty"`
	Output       *StructuredOutputSpec `json:"output,omitempty"`
	Tools        []HostToolSpec        `json:"tools,omitempty"`
	MCPServers   []MCPServerSpec       `json:"mcp_servers,omitempty"`
}

type HostToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Mode        string          `json:"mode"`
	InputSchema json.RawMessage `json:"input_schema"`
	Callback    *CallbackTarget `json:"callback,omitempty"`
	callbackSet bool
}

func (s *HostToolSpec) UnmarshalJSON(payload []byte) error {
	type wire HostToolSpec
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(payload, &members); err != nil {
		return err
	}
	*s = HostToolSpec(decoded)
	_, s.callbackSet = members["callback"]
	return nil
}

type CallbackTarget struct {
	URL string `json:"url"`
}

type StructuredOutputSpec struct {
	Schema json.RawMessage `json:"schema"`
}

type CreateInvocationInput struct {
	AgentKey            string                        `json:"agent_key"`
	TenantKey           *string                       `json:"tenant_key,omitempty"`
	SessionID           *string                       `json:"session_id,omitempty"`
	SessionKey          *string                       `json:"session_key,omitempty"`
	IdempotencyKey      string                        `json:"idempotency_key"`
	Input               InvocationInput               `json:"input"`
	Spec                InlineExecutionSpec           `json:"spec"`
	ProviderCredentials []ProviderCredentialSelection `json:"provider_credentials,omitempty"`
}

type ProviderCredentialSelection struct {
	Provider      string                          `json:"provider"`
	Source        domain.ProviderCredentialSource `json:"source"`
	Credential    *ProviderStaticCredentialInput  `json:"credential,omitempty"`
	credentialSet bool
}

func (s *ProviderCredentialSelection) UnmarshalJSON(payload []byte) error {
	type wire ProviderCredentialSelection
	var decoded wire
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(payload, &members); err != nil {
		return err
	}
	*s = ProviderCredentialSelection(decoded)
	_, s.credentialSet = members["credential"]
	return nil
}

type InvocationAcknowledgement struct {
	AgentID      string
	SessionID    string
	InvocationID string
	Status       domain.InvocationStatus
	Deduplicated bool
	DeadlineAt   time.Time
}

type InvocationRead struct {
	ID                string
	AgentID           string
	SessionID         string
	Status            domain.InvocationStatus
	Error             json.RawMessage
	Usage             json.RawMessage
	Provenance        json.RawMessage
	Output            json.RawMessage
	OutputProvenance  json.RawMessage
	Limits            InvocationLimitRead
	ActiveExecutionMS int64
	DeadlineAt        time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	EndedAt           *time.Time
	PendingToolCalls  []PendingHostToolCall
}

type SessionRead struct {
	ID                     string
	AgentID                string
	TenantKey              *string
	SessionKey             *string
	ActiveInvocationID     *string
	ActiveInvocationStatus *domain.InvocationStatus
	CreatedAt              time.Time
	UpdatedAt              time.Time
	PendingToolCalls       []PendingHostToolCall
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
	ports.ProviderCredentialRepository
	ports.MCPRepository
}

type InvocationExecutionMode string

const (
	InvocationExecutionEmbedded   InvocationExecutionMode = "embedded"
	InvocationExecutionCloudTasks InvocationExecutionMode = "cloud_tasks"
)

type RuntimeService struct {
	store                  admissionStore
	txm                    ports.TransactionManager
	clock                  ports.Clock
	ids                    ports.IDGenerator
	signaller              ports.WorkSignaller
	cancellations          ports.CancellationSignaller
	limitPolicy            LimitPolicy
	logger                 *slog.Logger
	executionMode          InvocationExecutionMode
	dispatchQueue          string
	callbackTools          bool
	credentialPolicy       ProviderCredentialPolicy
	credentialCipher       ports.CredentialCipher
	credentialCleanupGrace time.Duration
	mcpClient              ports.MCPClient
}

type RuntimeOption func(*RuntimeService)

func WithWorkSignaller(signaller ports.WorkSignaller) RuntimeOption {
	return func(service *RuntimeService) { service.signaller = signaller }
}

func WithCancellationSignaller(signaller ports.CancellationSignaller) RuntimeOption {
	return func(service *RuntimeService) { service.cancellations = signaller }
}

func WithLimitPolicy(policy LimitPolicy) RuntimeOption {
	return func(service *RuntimeService) { service.limitPolicy = policy }
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

func WithCallbackTools(enabled bool) RuntimeOption {
	return func(service *RuntimeService) { service.callbackTools = enabled }
}

type CredentialDeploymentMode string

const (
	CredentialDeploymentSelfHosted CredentialDeploymentMode = "self_hosted"
	CredentialDeploymentCloud      CredentialDeploymentMode = "cloud"
)

type ProviderCredentialPolicy struct {
	DeploymentMode CredentialDeploymentMode
	DefaultSource  domain.ProviderCredentialSource
}

func WithProviderCredentialPolicy(
	policy ProviderCredentialPolicy,
	cipher ports.CredentialCipher,
	cleanupGrace time.Duration,
) RuntimeOption {
	return func(service *RuntimeService) {
		service.credentialPolicy = policy
		service.credentialCipher = cipher
		service.credentialCleanupGrace = cleanupGrace
	}
}

func WithMCPClient(client ports.MCPClient) RuntimeOption {
	return func(service *RuntimeService) {
		service.mcpClient = client
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
		store:         store,
		txm:           txm,
		clock:         clock,
		ids:           ids,
		limitPolicy:   DefaultLimitPolicy(),
		logger:        slog.Default(),
		executionMode: InvocationExecutionEmbedded,
		credentialPolicy: ProviderCredentialPolicy{
			DeploymentMode: CredentialDeploymentSelfHosted,
			DefaultSource:  domain.ProviderCredentialSourceInstallationBYOK,
		},
		credentialCleanupGrace: 5 * time.Minute,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service
}

func ValidateCreateInvocation(input CreateInvocationInput) error {
	if err := validateBoundedString("agent_key", input.AgentKey, MaxReferenceCharacters); err != nil {
		return err
	}
	if err := validateBoundedString("idempotency_key", input.IdempotencyKey, MaxReferenceCharacters); err != nil {
		return err
	}
	if input.TenantKey != nil {
		if err := validateBoundedString("tenant_key", *input.TenantKey, MaxReferenceCharacters); err != nil {
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
	if !utf8.ValidString(input.Spec.Instructions) ||
		(input.Spec.Instructions != "" && strings.TrimSpace(input.Spec.Instructions) == "") {
		return invalidRequest("spec.instructions must not be blank when supplied.")
	}
	if err := validateBoundedString("spec.model.provider", input.Spec.Model.Provider, MaxReferenceCharacters); err != nil {
		return err
	}
	if err := validateBoundedString("spec.model.id", input.Spec.Model.ID, MaxReferenceCharacters); err != nil {
		return err
	}
	if err := validateRequestedLimits(input.Spec.Limits); err != nil {
		return err
	}
	if input.Spec.Output != nil {
		if _, err := structuredOutputSchemaDigest(input.Spec.Output.Schema); err != nil {
			return invalidRequest("spec.output.schema is invalid: " + err.Error() + ".")
		}
	}
	if err := validateHostTools(input.Spec.Tools); err != nil {
		return err
	}
	if err := validateMCPServers(input.Spec.MCPServers); err != nil {
		return err
	}
	provider, ok := CanonicalModelProvider(input.Spec.Model.Provider)
	if !ok {
		return invalidRequest("spec.model.provider is not supported.")
	}
	seenProviders := make(map[string]struct{}, len(input.ProviderCredentials))
	if input.ProviderCredentials != nil && len(input.ProviderCredentials) != 1 {
		return invalidRequest("provider_credentials must contain exactly one selection when supplied.")
	}
	for index, selection := range input.ProviderCredentials {
		canonical, ok := CanonicalModelProvider(selection.Provider)
		if !ok {
			return invalidRequest(fmt.Sprintf("provider_credentials[%d].provider is not supported.", index))
		}
		if _, duplicate := seenProviders[canonical]; duplicate {
			return invalidRequest("provider_credentials contains duplicate provider aliases.")
		}
		seenProviders[canonical] = struct{}{}
		if canonical != provider {
			return invalidRequest(fmt.Sprintf("provider_credentials[%d] is not used by spec.model.provider.", index))
		}
		if selection.Source != domain.ProviderCredentialSourceCallerEphemeral &&
			selection.Source != domain.ProviderCredentialSourceAccountBYOK &&
			selection.Source != domain.ProviderCredentialSourceTenantBYOK &&
			selection.Source != domain.ProviderCredentialSourcePlatform {
			return invalidRequest(fmt.Sprintf("provider_credentials[%d].source is invalid.", index))
		}
		if selection.Source == domain.ProviderCredentialSourceCallerEphemeral {
			if selection.Credential == nil {
				return invalidRequest(fmt.Sprintf("provider_credentials[%d].credential is required for caller_ephemeral.", index))
			}
			if err := validateProviderAPIKey(selection.Credential.APIKey); err != nil {
				return err
			}
		} else if selection.Credential != nil || selection.credentialSet {
			return invalidRequest(fmt.Sprintf("provider_credentials[%d].credential is valid only for caller_ephemeral.", index))
		}
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
	rawInput := input
	if err := ValidateCreateInvocation(input); err != nil {
		return InvocationAcknowledgement{}, err
	}
	input = canonicalCreateInvocation(input)
	if err := s.validateCredentialSelection(input); err != nil {
		return InvocationAcknowledgement{}, err
	}
	if err := s.validateMCPServerBindings(input); err != nil {
		return InvocationAcknowledgement{}, err
	}
	if hasCallbackTools(input.Spec.Tools) && !s.callbackTools {
		return InvocationAcknowledgement{}, invalidRequest("spec.tools callback mode is not configured for this installation.")
	}
	if auth.TenantConstraint != nil && input.TenantKey != nil && *auth.TenantConstraint != *input.TenantKey {
		return InvocationAcknowledgement{}, forbidden("The requested tenant_key conflicts with the credential constraint.")
	}
	if auth.SessionConstraint != nil && (input.SessionID == nil || *input.SessionID != *auth.SessionConstraint) {
		return InvocationAcknowledgement{}, forbidden("The requested Session conflicts with the credential constraint.")
	}
	resolvedLimits, err := s.limitPolicy.ResolveForFeatures(
		input.Spec.Limits,
		input.Spec.Output != nil,
		len(input.Spec.Tools) != 0 || len(input.Spec.MCPServers) != 0,
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
	fingerprint, err := InvocationFingerprintV8(input)
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
				ID:        agentID,
				AccountID: account.ID,
				AgentKey:  input.AgentKey,
				CreatedAt: now,
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
			if existing, found, err := s.findIdempotent(txCtx, account.ID, partition.ID, agent.ID, input.IdempotencyKey, rawInput, input, fingerprint); err != nil {
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
			if existing, found, err := s.findIdempotent(txCtx, account.ID, partition.ID, agent.ID, input.IdempotencyKey, rawInput, input, fingerprint); err != nil {
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
			ids, err := s.newAdmissionIDs(
				s.executionMode == InvocationExecutionCloudTasks,
				len(input.Spec.MCPServers),
			)
			if err != nil {
				return err
			}
			specJSON, err := json.Marshal(durableExecutionSpec(input.Spec))
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
				FingerprintVersion:     currentAdmissionFingerprintVersion,
				Status:                 domain.InvocationQueued,
				CurrentStateRevision:   revision,
				TotalTimeoutMS:         resolvedLimits.TotalTimeout.Milliseconds(),
				ActiveTimeoutMS:        resolvedLimits.ActiveTimeout.Milliseconds(),
				WaitingTimeoutMS:       resolvedLimits.WaitingTimeout.Milliseconds(),
				MaxOutputTokens:        resolvedLimits.MaxOutputTokens,
				MaxEstimatedCostMicros: resolvedLimits.MaxEstimatedCostMicros,
				MaxIterations:          resolvedLimits.MaxIterations,
				OutputSchemaDigest:     outputSchemaDigest,
				DeadlineAt:             now.Add(resolvedLimits.TotalTimeout),
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
			binding, err := s.invocationProviderCredentialBinding(
				txCtx,
				invocation,
				partition,
				input,
				ids.binding,
				now,
			)
			if err != nil {
				return err
			}
			if err := s.store.CreateInvocationProviderCredential(txCtx, binding); err != nil {
				return err
			}
			for index, server := range input.Spec.MCPServers {
				mcpBinding, err := s.invocationMCPServerBinding(
					invocation,
					server,
					ids.mcpBindings[index],
					now,
				)
				if err != nil {
					return err
				}
				if err := s.store.CreateInvocationMCPServerBinding(txCtx, mcpBinding); err != nil {
					return err
				}
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
		if partition.AccountID != account.ID || !tenantMatches(auth.TenantConstraint, partition.TenantKey) || !tenantMatches(input.TenantKey, partition.TenantKey) {
			return domain.TenantPartition{}, nil, notFound()
		}
		return partition, &session, nil
	}

	effectiveTenant := auth.TenantConstraint
	if effectiveTenant == nil {
		effectiveTenant = input.TenantKey
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
		ID:        partitionID,
		AccountID: account.ID,
		TenantKey: cloneString(effectiveTenant),
		CreatedAt: now,
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
	if invocation.AccountID != auth.AccountID || !auth.AllowsSession(invocation.SessionID) {
		return InvocationRead{}, notFound()
	}
	partition, err := s.store.GetTenantPartition(ctx, invocation.TenantPartitionID)
	if err != nil || partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantKey) {
		if errors.Is(err, ports.ErrNotFound) || err == nil {
			return InvocationRead{}, notFound()
		}
		return InvocationRead{}, err
	}
	read := invocationReadFromDomain(invocation)
	read.PendingToolCalls, err = s.pendingHostToolCalls(ctx, invocation)
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
	if session.AccountID != auth.AccountID || !auth.AllowsSession(session.ID) {
		return SessionRead{}, notFound()
	}
	partition, err := s.store.GetTenantPartition(ctx, session.TenantPartitionID)
	if err != nil || partition.AccountID != auth.AccountID || !tenantMatches(auth.TenantConstraint, partition.TenantKey) {
		if errors.Is(err, ports.ErrNotFound) || err == nil {
			return SessionRead{}, notFound()
		}
		return SessionRead{}, err
	}
	var activeID *string
	var activeStatus *domain.InvocationStatus
	var pending []PendingHostToolCall
	active, err := s.store.GetNonterminalInvocationBySession(ctx, session.ID)
	if err == nil {
		activeID = &active.ID
		activeStatus = &active.Status
		pending, err = s.pendingHostToolCalls(ctx, active)
		if err != nil {
			return SessionRead{}, err
		}
	} else if !errors.Is(err, ports.ErrNotFound) {
		return SessionRead{}, err
	}
	return SessionRead{
		ID:                     session.ID,
		AgentID:                session.AgentID,
		TenantKey:              cloneString(partition.TenantKey),
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
		ID:                invocation.ID,
		AgentID:           invocation.AgentID,
		SessionID:         invocation.SessionID,
		Status:            invocation.Status,
		Error:             invocation.Error,
		Usage:             invocation.Usage,
		Provenance:        invocation.Provenance,
		Output:            invocation.Output,
		OutputProvenance:  invocation.OutputProvenance,
		Limits:            limitReadFromDomain(invocation),
		ActiveExecutionMS: invocation.ActiveExecutionMS,
		DeadlineAt:        invocation.DeadlineAt,
		CreatedAt:         invocation.CreatedAt,
		UpdatedAt:         invocation.UpdatedAt,
		EndedAt:           invocation.CompletedAt,
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
	if err := validateProviderCredentialPolicy(s.credentialPolicy); err != nil {
		return &PublicError{Code: CodeUnavailable, Message: "The service is temporarily unavailable.", Cause: err}
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
	legacyInput CreateInvocationInput,
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
		if legacyInput.Spec.Limits != nil || legacyInput.Spec.Output != nil ||
			len(legacyInput.Spec.Tools) != 0 || len(legacyInput.Spec.MCPServers) != 0 ||
			legacyInput.ProviderCredentials != nil {
			return domain.Invocation{}, false, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used with a different request."}
		}
		legacy, legacyErr := InvocationFingerprintV1(legacyInput)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 2:
		if legacyInput.Spec.Output != nil || len(legacyInput.Spec.Tools) != 0 ||
			len(legacyInput.Spec.MCPServers) != 0 || legacyInput.ProviderCredentials != nil {
			return domain.Invocation{}, false, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used with a different request."}
		}
		legacy, legacyErr := InvocationFingerprintV2(legacyInput)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 3:
		if len(legacyInput.Spec.Tools) != 0 || len(legacyInput.Spec.MCPServers) != 0 ||
			legacyInput.ProviderCredentials != nil {
			return domain.Invocation{}, false, &PublicError{
				Code:    CodeIdempotencyConflict,
				Message: "The idempotency key was already used with a different request.",
			}
		}
		legacy, legacyErr := InvocationFingerprintV3(legacyInput)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 4:
		if hasCallbackTools(legacyInput.Spec.Tools) || len(legacyInput.Spec.MCPServers) != 0 ||
			legacyInput.ProviderCredentials != nil {
			return domain.Invocation{}, false, &PublicError{
				Code:    CodeIdempotencyConflict,
				Message: "The idempotency key was already used with a different request.",
			}
		}
		legacy, legacyErr := InvocationFingerprintV4(legacyInput)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 5:
		if len(legacyInput.Spec.MCPServers) != 0 || legacyInput.ProviderCredentials != nil {
			return domain.Invocation{}, false, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used with a different request."}
		}
		legacy, legacyErr := InvocationFingerprintV5(legacyInput)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 6:
		if len(legacyInput.Spec.MCPServers) != 0 {
			return domain.Invocation{}, false, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used with a different request."}
		}
		provider := input.Spec.Model.Provider
		binding, bindingErr := s.store.GetInvocationProviderCredential(ctx, existing.ID, provider)
		if bindingErr != nil || binding.InvocationID != existing.ID || binding.Provider != provider {
			return domain.Invocation{}, false, &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
		}
		legacy, legacyErr := InvocationFingerprintV6(input)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 7:
		if len(legacyInput.Spec.MCPServers) != 0 {
			return domain.Invocation{}, false, &PublicError{Code: CodeIdempotencyConflict, Message: "The idempotency key was already used with a different request."}
		}
		provider := input.Spec.Model.Provider
		binding, bindingErr := s.store.GetInvocationProviderCredential(ctx, existing.ID, provider)
		if bindingErr != nil || binding.InvocationID != existing.ID || binding.Provider != provider {
			return domain.Invocation{}, false, &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
		}
		legacy, legacyErr := InvocationFingerprintV7(input)
		if legacyErr != nil {
			return domain.Invocation{}, false, legacyErr
		}
		expected = legacy[:]
	case 8:
		provider := input.Spec.Model.Provider
		binding, bindingErr := s.store.GetInvocationProviderCredential(ctx, existing.ID, provider)
		if bindingErr != nil || binding.InvocationID != existing.ID || binding.Provider != provider {
			return domain.Invocation{}, false, &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
		}
		mcpBindings, bindingErr := s.store.ListInvocationMCPServerBindings(ctx, existing.ID)
		if bindingErr != nil || !mcpBindingsMatchSpec(existing.ID, input.Spec.MCPServers, mcpBindings) {
			return domain.Invocation{}, false, &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
		}
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

type admissionIDs struct {
	snapshot    string
	invocation  string
	message     string
	state       string
	dispatch    string
	binding     string
	mcpBindings []string
}

func (s *RuntimeService) newAdmissionIDs(includeDispatch bool, mcpBindings int) (admissionIDs, error) {
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
	if ids.binding, err = s.ids.NewID(domain.PrefixInvocationProviderCredential); err != nil {
		return ids, err
	}
	ids.mcpBindings = make([]string, mcpBindings)
	for index := range ids.mcpBindings {
		if ids.mcpBindings[index], err = s.ids.NewID(domain.PrefixInvocationMCPServerBinding); err != nil {
			return ids, err
		}
	}
	if includeDispatch {
		if ids.dispatch, err = s.ids.NewID(domain.PrefixExecutionDispatch); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func mcpBindingsMatchSpec(
	invocationID string,
	servers []MCPServerSpec,
	bindings []domain.InvocationMCPServerBinding,
) bool {
	if len(servers) != len(bindings) {
		return false
	}
	expected := make(map[string]struct{}, len(servers))
	for _, server := range servers {
		expected[server.Name] = struct{}{}
	}
	for _, binding := range bindings {
		if binding.InvocationID != invocationID {
			return false
		}
		if _, ok := expected[binding.ServerName]; !ok {
			return false
		}
		delete(expected, binding.ServerName)
	}
	return len(expected) == 0
}

func acknowledgementFor(invocation domain.Invocation, deduplicated bool) InvocationAcknowledgement {
	return InvocationAcknowledgement{
		AgentID:      invocation.AgentID,
		SessionID:    invocation.SessionID,
		InvocationID: invocation.ID,
		Status:       invocation.Status,
		Deduplicated: deduplicated,
		DeadlineAt:   invocation.DeadlineAt,
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
