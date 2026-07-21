package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresdb "github.com/deepnoodle-ai/nvoken/internal/adapters/postgres/sqlc"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type Store struct {
	queries *postgresdb.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{queries: postgresdb.New(pool)}
}

var (
	_ ports.AccountRepository               = (*Store)(nil)
	_ ports.TenantPartitionRepository       = (*Store)(nil)
	_ ports.AgentRepository                 = (*Store)(nil)
	_ ports.SessionRepository               = (*Store)(nil)
	_ ports.ExecutionSpecSnapshotRepository = (*Store)(nil)
	_ ports.SessionMessageRepository        = (*Store)(nil)
	_ ports.InvocationRepository            = (*Store)(nil)
	_ ports.InvocationStateRepository       = (*Store)(nil)
	_ ports.TransactionManager              = (*TransactionManager)(nil)
)

func (s *Store) q(ctx context.Context) *postgresdb.Queries {
	if tx, ok := transactionFromContext(ctx); ok {
		return s.queries.WithTx(tx)
	}
	return s.queries
}

func (s *Store) CreateAccount(ctx context.Context, account domain.Account) error {
	return s.q(ctx).CreateAccount(ctx, postgresdb.CreateAccountParams{
		ID: account.ID, CreatedAt: account.CreatedAt,
	})
}

func (s *Store) GetAccount(ctx context.Context, id string) (domain.Account, error) {
	row, err := s.q(ctx).GetAccount(ctx, id)
	if err != nil {
		return domain.Account{}, normalizeNotFound(err)
	}
	return domain.Account{ID: row.ID, CreatedAt: row.CreatedAt}, nil
}

func (s *Store) ListAccounts(ctx context.Context) ([]domain.Account, error) {
	rows, err := s.q(ctx).ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	accounts := make([]domain.Account, len(rows))
	for i, row := range rows {
		accounts[i] = domain.Account{ID: row.ID, CreatedAt: row.CreatedAt}
	}
	return accounts, nil
}

func (s *Store) LockInstallationBootstrap(ctx context.Context) error {
	if _, ok := transactionFromContext(ctx); !ok {
		return fmt.Errorf("installation bootstrap lock requires a transaction")
	}
	_, err := s.q(ctx).LockInstallationBootstrap(ctx)
	return err
}

func (s *Store) CreateTenantPartition(ctx context.Context, partition domain.TenantPartition) error {
	return s.q(ctx).CreateTenantPartition(ctx, postgresdb.CreateTenantPartitionParams{
		ID: partition.ID, AccountID: partition.AccountID,
		TenantRef: partition.TenantRef, CreatedAt: partition.CreatedAt,
	})
}

func (s *Store) ResolveTenantPartition(ctx context.Context, partition domain.TenantPartition) (domain.TenantPartition, error) {
	if partition.TenantRef == nil {
		if err := s.q(ctx).CreateDefaultTenantPartitionIfAbsent(ctx, postgresdb.CreateDefaultTenantPartitionIfAbsentParams{
			ID: partition.ID, AccountID: partition.AccountID, CreatedAt: partition.CreatedAt,
		}); err != nil {
			return domain.TenantPartition{}, err
		}
		return s.GetDefaultTenantPartition(ctx, partition.AccountID)
	}
	if err := s.q(ctx).CreateTenantPartitionByRefIfAbsent(ctx, postgresdb.CreateTenantPartitionByRefIfAbsentParams{
		ID: partition.ID, AccountID: partition.AccountID,
		TenantRef: partition.TenantRef, CreatedAt: partition.CreatedAt,
	}); err != nil {
		return domain.TenantPartition{}, err
	}
	return s.GetTenantPartitionByRef(ctx, partition.AccountID, *partition.TenantRef)
}

func (s *Store) GetTenantPartition(ctx context.Context, id string) (domain.TenantPartition, error) {
	row, err := s.q(ctx).GetTenantPartition(ctx, id)
	if err != nil {
		return domain.TenantPartition{}, normalizeNotFound(err)
	}
	return tenantPartitionFromRow(row), nil
}

func (s *Store) GetDefaultTenantPartition(ctx context.Context, accountID string) (domain.TenantPartition, error) {
	row, err := s.q(ctx).GetDefaultTenantPartition(ctx, accountID)
	if err != nil {
		return domain.TenantPartition{}, normalizeNotFound(err)
	}
	return tenantPartitionFromRow(row), nil
}

func (s *Store) GetTenantPartitionByRef(ctx context.Context, accountID, tenantRef string) (domain.TenantPartition, error) {
	row, err := s.q(ctx).GetTenantPartitionByRef(ctx, postgresdb.GetTenantPartitionByRefParams{
		AccountID: accountID, TenantRef: tenantRef,
	})
	if err != nil {
		return domain.TenantPartition{}, normalizeNotFound(err)
	}
	return tenantPartitionFromRow(row), nil
}

func tenantPartitionFromRow(row postgresdb.TenantPartition) domain.TenantPartition {
	return domain.TenantPartition{
		ID: row.ID, AccountID: row.AccountID, TenantRef: row.TenantRef, CreatedAt: row.CreatedAt,
	}
}

func (s *Store) CreateAgent(ctx context.Context, agent domain.Agent) error {
	return s.q(ctx).CreateAgent(ctx, postgresdb.CreateAgentParams{
		ID: agent.ID, AccountID: agent.AccountID, AgentRef: agent.AgentRef, CreatedAt: agent.CreatedAt,
	})
}

func (s *Store) ResolveAgent(ctx context.Context, agent domain.Agent) (domain.Agent, error) {
	if err := s.q(ctx).CreateAgentIfAbsent(ctx, postgresdb.CreateAgentIfAbsentParams{
		ID: agent.ID, AccountID: agent.AccountID, AgentRef: agent.AgentRef, CreatedAt: agent.CreatedAt,
	}); err != nil {
		return domain.Agent{}, err
	}
	return s.GetAgentByRef(ctx, agent.AccountID, agent.AgentRef)
}

func (s *Store) GetAgentByRef(ctx context.Context, accountID, agentRef string) (domain.Agent, error) {
	row, err := s.q(ctx).GetAgentByRef(ctx, postgresdb.GetAgentByRefParams{
		AccountID: accountID, AgentRef: agentRef,
	})
	if err != nil {
		return domain.Agent{}, normalizeNotFound(err)
	}
	return domain.Agent{
		ID: row.ID, AccountID: row.AccountID, AgentRef: row.AgentRef, CreatedAt: row.CreatedAt,
	}, nil
}

func (s *Store) CreateSession(ctx context.Context, session domain.Session) error {
	return s.q(ctx).CreateSession(ctx, postgresdb.CreateSessionParams{
		ID: session.ID, AccountID: session.AccountID,
		TenantPartitionID: session.TenantPartitionID, AgentID: session.AgentID,
		SessionKey: session.SessionKey, NextMessageSequence: session.NextMessageSequence,
		NextLifecycleRevision: session.NextLifecycleRevision,
		CreatedAt:             session.CreatedAt, UpdatedAt: session.UpdatedAt,
	})
}

func (s *Store) ResolveSessionByKey(ctx context.Context, session domain.Session) (domain.Session, error) {
	if session.SessionKey == nil {
		return domain.Session{}, fmt.Errorf("resolve session by key requires a session key")
	}
	if err := s.q(ctx).CreateSessionIfAbsent(ctx, postgresdb.CreateSessionIfAbsentParams{
		ID: session.ID, AccountID: session.AccountID,
		TenantPartitionID: session.TenantPartitionID, AgentID: session.AgentID,
		SessionKey: session.SessionKey, NextMessageSequence: session.NextMessageSequence,
		NextLifecycleRevision: session.NextLifecycleRevision,
		CreatedAt:             session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}); err != nil {
		return domain.Session{}, err
	}
	return s.GetSessionByKey(ctx, session.AccountID, session.TenantPartitionID, session.AgentID, *session.SessionKey)
}

func (s *Store) GetSession(ctx context.Context, id string) (domain.Session, error) {
	row, err := s.q(ctx).GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, normalizeNotFound(err)
	}
	return sessionFromRow(row), nil
}

func (s *Store) GetSessionForUpdate(ctx context.Context, id string) (domain.Session, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.Session{}, fmt.Errorf("session row lock requires a transaction")
	}
	row, err := s.q(ctx).GetSessionForUpdate(ctx, id)
	if err != nil {
		return domain.Session{}, normalizeNotFound(err)
	}
	return sessionFromRow(row), nil
}

func (s *Store) GetSessionByKey(ctx context.Context, accountID, partitionID, agentID, sessionKey string) (domain.Session, error) {
	row, err := s.q(ctx).GetSessionByKey(ctx, postgresdb.GetSessionByKeyParams{
		AccountID: accountID, TenantPartitionID: partitionID, AgentID: agentID, SessionKey: sessionKey,
	})
	if err != nil {
		return domain.Session{}, normalizeNotFound(err)
	}
	return sessionFromRow(row), nil
}

func sessionFromRow(row postgresdb.Session) domain.Session {
	return domain.Session{
		ID: row.ID, AccountID: row.AccountID, TenantPartitionID: row.TenantPartitionID,
		AgentID: row.AgentID, SessionKey: row.SessionKey,
		NextMessageSequence:   row.NextMessageSequence,
		NextLifecycleRevision: row.NextLifecycleRevision,
		CreatedAt:             row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func (s *Store) ReserveMessageSequence(ctx context.Context, sessionID string) (int64, error) {
	return s.q(ctx).ReserveMessageSequence(ctx, sessionID)
}

func (s *Store) ReserveLifecycleRevision(ctx context.Context, sessionID string) (int64, error) {
	return s.q(ctx).ReserveLifecycleRevision(ctx, sessionID)
}

func (s *Store) CreateExecutionSpecSnapshot(ctx context.Context, snapshot domain.ExecutionSpecSnapshot) error {
	return s.q(ctx).CreateExecutionSpecSnapshot(ctx, postgresdb.CreateExecutionSpecSnapshotParams{
		ID: snapshot.ID, AccountID: snapshot.AccountID, Spec: snapshot.Spec, CreatedAt: snapshot.CreatedAt,
	})
}

func (s *Store) GetExecutionSpecSnapshot(ctx context.Context, id string) (domain.ExecutionSpecSnapshot, error) {
	row, err := s.q(ctx).GetExecutionSpecSnapshot(ctx, id)
	if err != nil {
		return domain.ExecutionSpecSnapshot{}, normalizeNotFound(err)
	}
	return domain.ExecutionSpecSnapshot{
		ID: row.ID, AccountID: row.AccountID, Spec: row.Spec, CreatedAt: row.CreatedAt,
	}, nil
}

func (s *Store) CreateInvocation(ctx context.Context, invocation domain.Invocation) error {
	return s.q(ctx).CreateInvocation(ctx, postgresdb.CreateInvocationParams{
		ID: invocation.ID, SessionID: invocation.SessionID, AccountID: invocation.AccountID,
		TenantPartitionID: invocation.TenantPartitionID, AgentID: invocation.AgentID,
		SpecSnapshotID: invocation.SpecSnapshotID, IdempotencyKey: invocation.IdempotencyKey,
		RequestFingerprint: invocation.RequestFingerprint, Status: string(invocation.Status),
		CurrentStateRevision: invocation.CurrentStateRevision, ErrorPayload: invocation.Error,
		CreatedAt: invocation.CreatedAt, UpdatedAt: invocation.UpdatedAt,
		CompletedAt: invocation.CompletedAt,
	})
}

func (s *Store) GetInvocation(ctx context.Context, id string) (domain.Invocation, error) {
	row, err := s.q(ctx).GetInvocation(ctx, id)
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) GetInvocationForUpdate(ctx context.Context, id string) (domain.Invocation, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.Invocation{}, fmt.Errorf("invocation row lock requires a transaction")
	}
	row, err := s.q(ctx).GetInvocationForUpdate(ctx, id)
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) FindNextQueuedInvocationForUpdate(ctx context.Context) (domain.Invocation, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.Invocation{}, fmt.Errorf("queued Invocation Session lock requires a transaction")
	}
	row, err := s.q(ctx).FindNextQueuedInvocationForUpdate(ctx)
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) ListExpiredInvocationLeases(ctx context.Context, observedAt time.Time, limit int) ([]domain.Invocation, error) {
	if limit <= 0 {
		return []domain.Invocation{}, nil
	}
	if limit > int(^uint32(0)>>1) {
		limit = int(^uint32(0) >> 1)
	}
	rows, err := s.q(ctx).ListExpiredInvocationLeases(ctx, postgresdb.ListExpiredInvocationLeasesParams{
		ObservedAt: &observedAt, BatchLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	invocations := make([]domain.Invocation, len(rows))
	for i, row := range rows {
		invocations[i] = invocationFromRow(row)
	}
	return invocations, nil
}

func (s *Store) GetInvocationByIdempotencyKey(ctx context.Context, accountID, partitionID, agentID, key string) (domain.Invocation, error) {
	row, err := s.q(ctx).GetInvocationByIdempotencyKey(ctx, postgresdb.GetInvocationByIdempotencyKeyParams{
		AccountID: accountID, TenantPartitionID: partitionID, AgentID: agentID, IdempotencyKey: key,
	})
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) GetNonterminalInvocationBySession(ctx context.Context, sessionID string) (domain.Invocation, error) {
	row, err := s.q(ctx).GetNonterminalInvocationBySession(ctx, sessionID)
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) LockInvocationAdmissionKey(ctx context.Context, key string) error {
	if _, ok := transactionFromContext(ctx); !ok {
		return fmt.Errorf("invocation admission lock requires a transaction")
	}
	_, err := s.q(ctx).LockInvocationAdmissionKey(ctx, key)
	return err
}

func invocationFromRow(row postgresdb.Invocation) domain.Invocation {
	return domain.Invocation{
		ID: row.ID, SessionID: row.SessionID, AccountID: row.AccountID,
		TenantPartitionID: row.TenantPartitionID, AgentID: row.AgentID,
		SpecSnapshotID: row.SpecSnapshotID, IdempotencyKey: row.IdempotencyKey,
		RequestFingerprint: row.RequestFingerprint, Status: domain.InvocationStatus(row.Status),
		CurrentStateRevision: row.CurrentStateRevision,
		LeaseOwner:           row.LeaseOwner,
		LeaseExpiresAt:       row.LeaseExpiresAt,
		LeaseAttempt:         row.LeaseAttempt,
		Error:                row.Error,
		CreatedAt:            row.CreatedAt, UpdatedAt: row.UpdatedAt, CompletedAt: row.CompletedAt,
	}
}

func (s *Store) ClaimInvocation(
	ctx context.Context,
	id, owner string,
	leaseExpiresAt time.Time,
	stateRevision int64,
	observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).ClaimInvocation(ctx, postgresdb.ClaimInvocationParams{
		ID: id, LeaseOwner: &owner, LeaseExpiresAt: &leaseExpiresAt,
		StateRevision: stateRevision, ObservedAt: observedAt,
	})
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) RenewInvocationLease(
	ctx context.Context,
	id, owner string,
	attempt int64,
	leaseExpiresAt, observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).RenewInvocationLease(ctx, postgresdb.RenewInvocationLeaseParams{
		ID: id, LeaseOwner: &owner, LeaseAttempt: attempt,
		LeaseExpiresAt: &leaseExpiresAt, ObservedAt: observedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) SettleInvocation(
	ctx context.Context,
	id, owner string,
	attempt int64,
	status domain.InvocationStatus,
	stateRevision int64,
	errorPayload []byte,
	observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).SettleInvocation(ctx, postgresdb.SettleInvocationParams{
		ID: id, LeaseOwner: &owner, LeaseAttempt: attempt,
		Status: string(status), StateRevision: stateRevision,
		ErrorPayload: errorPayload, ObservedAt: &observedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) ReapInvocationLease(
	ctx context.Context,
	id string,
	attempt, stateRevision int64,
	errorPayload []byte,
	observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).ReapInvocationLease(ctx, postgresdb.ReapInvocationLeaseParams{
		ID: id, LeaseAttempt: attempt, StateRevision: stateRevision,
		ErrorPayload: errorPayload, ObservedAt: &observedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) AppendSessionMessage(ctx context.Context, message domain.SessionMessage) error {
	return s.q(ctx).AppendSessionMessage(ctx, postgresdb.AppendSessionMessageParams{
		ID: message.ID, SessionID: message.SessionID, AccountID: message.AccountID,
		TenantPartitionID: message.TenantPartitionID, AgentID: message.AgentID,
		InvocationID: message.InvocationID, Sequence: message.Sequence,
		Role: string(message.Role), Content: message.Content, CreatedAt: message.CreatedAt,
	})
}

func (s *Store) ListSessionMessages(ctx context.Context, sessionID string) ([]domain.SessionMessage, error) {
	rows, err := s.q(ctx).ListSessionMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages := make([]domain.SessionMessage, len(rows))
	for i, row := range rows {
		messages[i] = domain.SessionMessage{
			ID: row.ID, SessionID: row.SessionID, AccountID: row.AccountID,
			TenantPartitionID: row.TenantPartitionID, AgentID: row.AgentID,
			InvocationID: row.InvocationID, Sequence: row.Sequence,
			Role: domain.MessageRole(row.Role), Content: row.Content, CreatedAt: row.CreatedAt,
		}
	}
	return messages, nil
}

func (s *Store) AppendInvocationState(ctx context.Context, state domain.InvocationState) error {
	return s.q(ctx).AppendInvocationState(ctx, postgresdb.AppendInvocationStateParams{
		ID: state.ID, InvocationID: state.InvocationID, SessionID: state.SessionID,
		AccountID: state.AccountID, TenantPartitionID: state.TenantPartitionID,
		AgentID: state.AgentID, Revision: state.Revision, Status: string(state.Status),
		LeaseAttempt:           state.LeaseAttempt,
		ThroughMessageSequence: state.ThroughMessageSequence, CreatedAt: state.CreatedAt,
	})
}

func (s *Store) GetCurrentInvocationState(ctx context.Context, invocationID string) (domain.InvocationState, error) {
	row, err := s.q(ctx).GetCurrentInvocationState(ctx, invocationID)
	if err != nil {
		return domain.InvocationState{}, normalizeNotFound(err)
	}
	return invocationStateFromRow(row), nil
}

func (s *Store) ListInvocationStates(ctx context.Context, sessionID string) ([]domain.InvocationState, error) {
	rows, err := s.q(ctx).ListInvocationStates(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	states := make([]domain.InvocationState, len(rows))
	for i, row := range rows {
		states[i] = invocationStateFromRow(row)
	}
	return states, nil
}

func invocationStateFromRow(row postgresdb.InvocationState) domain.InvocationState {
	return domain.InvocationState{
		ID: row.ID, InvocationID: row.InvocationID, SessionID: row.SessionID,
		AccountID: row.AccountID, TenantPartitionID: row.TenantPartitionID,
		AgentID: row.AgentID, Revision: row.Revision,
		Status: domain.InvocationStatus(row.Status), LeaseAttempt: row.LeaseAttempt,
		ThroughMessageSequence: row.ThroughMessageSequence, CreatedAt: row.CreatedAt,
	}
}

func normalizeNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrNotFound
	}
	return err
}
