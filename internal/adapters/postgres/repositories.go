package postgres

import (
	"context"
	"time"

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
		return domain.Account{}, err
	}
	return domain.Account{ID: row.ID, CreatedAt: row.CreatedAt}, nil
}

func (s *Store) CreateTenantPartition(ctx context.Context, partition domain.TenantPartition) error {
	return s.q(ctx).CreateTenantPartition(ctx, postgresdb.CreateTenantPartitionParams{
		ID: partition.ID, AccountID: partition.AccountID,
		TenantRef: partition.TenantRef, CreatedAt: partition.CreatedAt,
	})
}

func (s *Store) GetDefaultTenantPartition(ctx context.Context, accountID string) (domain.TenantPartition, error) {
	row, err := s.q(ctx).GetDefaultTenantPartition(ctx, accountID)
	if err != nil {
		return domain.TenantPartition{}, err
	}
	return tenantPartitionFromRow(row), nil
}

func (s *Store) GetTenantPartitionByRef(ctx context.Context, accountID, tenantRef string) (domain.TenantPartition, error) {
	row, err := s.q(ctx).GetTenantPartitionByRef(ctx, postgresdb.GetTenantPartitionByRefParams{
		AccountID: accountID, TenantRef: tenantRef,
	})
	if err != nil {
		return domain.TenantPartition{}, err
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

func (s *Store) GetAgentByRef(ctx context.Context, accountID, agentRef string) (domain.Agent, error) {
	row, err := s.q(ctx).GetAgentByRef(ctx, postgresdb.GetAgentByRefParams{
		AccountID: accountID, AgentRef: agentRef,
	})
	if err != nil {
		return domain.Agent{}, err
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

func (s *Store) GetSession(ctx context.Context, id string) (domain.Session, error) {
	row, err := s.q(ctx).GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	return sessionFromRow(row), nil
}

func (s *Store) GetSessionByKey(ctx context.Context, accountID, partitionID, agentID, sessionKey string) (domain.Session, error) {
	row, err := s.q(ctx).GetSessionByKey(ctx, postgresdb.GetSessionByKeyParams{
		AccountID: accountID, TenantPartitionID: partitionID, AgentID: agentID, SessionKey: sessionKey,
	})
	if err != nil {
		return domain.Session{}, err
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
		return domain.ExecutionSpecSnapshot{}, err
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
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) GetInvocationByIdempotencyKey(ctx context.Context, accountID, partitionID, agentID, key string) (domain.Invocation, error) {
	row, err := s.q(ctx).GetInvocationByIdempotencyKey(ctx, postgresdb.GetInvocationByIdempotencyKeyParams{
		AccountID: accountID, TenantPartitionID: partitionID, AgentID: agentID, IdempotencyKey: key,
	})
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func invocationFromRow(row postgresdb.Invocation) domain.Invocation {
	return domain.Invocation{
		ID: row.ID, SessionID: row.SessionID, AccountID: row.AccountID,
		TenantPartitionID: row.TenantPartitionID, AgentID: row.AgentID,
		SpecSnapshotID: row.SpecSnapshotID, IdempotencyKey: row.IdempotencyKey,
		RequestFingerprint: row.RequestFingerprint, Status: domain.InvocationStatus(row.Status),
		CurrentStateRevision: row.CurrentStateRevision, Error: row.Error,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, CompletedAt: row.CompletedAt,
	}
}

func (s *Store) UpdateInvocationStatus(
	ctx context.Context,
	id string,
	status domain.InvocationStatus,
	stateRevision int64,
	errorPayload []byte,
	completedAt *time.Time,
) error {
	return s.q(ctx).UpdateInvocationStatus(ctx, postgresdb.UpdateInvocationStatusParams{
		ID: id, Status: string(status), StateRevision: stateRevision,
		ErrorPayload: errorPayload, CompletedAt: completedAt,
	})
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
		ThroughMessageSequence: state.ThroughMessageSequence, CreatedAt: state.CreatedAt,
	})
}

func (s *Store) ListInvocationStates(ctx context.Context, sessionID string) ([]domain.InvocationState, error) {
	rows, err := s.q(ctx).ListInvocationStates(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	states := make([]domain.InvocationState, len(rows))
	for i, row := range rows {
		states[i] = domain.InvocationState{
			ID: row.ID, InvocationID: row.InvocationID, SessionID: row.SessionID,
			AccountID: row.AccountID, TenantPartitionID: row.TenantPartitionID,
			AgentID: row.AgentID, Revision: row.Revision,
			Status:                 domain.InvocationStatus(row.Status),
			ThroughMessageSequence: row.ThroughMessageSequence, CreatedAt: row.CreatedAt,
		}
	}
	return states, nil
}
