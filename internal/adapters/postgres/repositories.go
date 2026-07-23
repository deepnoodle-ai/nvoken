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
	_ ports.ToolCallRepository              = (*Store)(nil)
	_ ports.CallbackDeliveryRepository      = (*Store)(nil)
	_ ports.RecoveryRepository              = (*Store)(nil)
	_ ports.ExecutionDispatchRepository     = (*Store)(nil)
	_ ports.ProviderCredentialRepository    = (*Store)(nil)
	_ ports.TransactionManager              = (*TransactionManager)(nil)
)

func (s *Store) CreateSyntheticDispatchWork(ctx context.Context, work domain.SyntheticDispatchWork) error {
	return s.q(ctx).CreateSyntheticDispatchWork(ctx, postgresdb.CreateSyntheticDispatchWorkParams{
		ID: work.ID, Status: string(work.Status), SettlementCount: int32(work.SettlementCount),
		CreatedAt: work.CreatedAt, UpdatedAt: work.UpdatedAt, SettledAt: work.SettledAt,
	})
}

func (s *Store) GetSyntheticDispatchWork(ctx context.Context, id string) (domain.SyntheticDispatchWork, error) {
	row, err := s.q(ctx).GetSyntheticDispatchWork(ctx, id)
	if err != nil {
		return domain.SyntheticDispatchWork{}, normalizeNotFound(err)
	}
	return syntheticDispatchWorkFromRow(row), nil
}

func (s *Store) GetSyntheticDispatchWorkForUpdate(ctx context.Context, id string) (domain.SyntheticDispatchWork, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.SyntheticDispatchWork{}, fmt.Errorf("synthetic dispatch work row lock requires a transaction")
	}
	row, err := s.q(ctx).GetSyntheticDispatchWorkForUpdate(ctx, id)
	if err != nil {
		return domain.SyntheticDispatchWork{}, normalizeNotFound(err)
	}
	return syntheticDispatchWorkFromRow(row), nil
}

func (s *Store) SettleSyntheticDispatchWork(ctx context.Context, id string, observedAt time.Time) (domain.SyntheticDispatchWork, error) {
	row, err := s.q(ctx).SettleSyntheticDispatchWork(ctx, postgresdb.SettleSyntheticDispatchWorkParams{
		ID: id, ObservedAt: &observedAt,
	})
	if err != nil {
		return domain.SyntheticDispatchWork{}, normalizeNotFound(err)
	}
	return syntheticDispatchWorkFromRow(row), nil
}

func syntheticDispatchWorkFromRow(row postgresdb.SyntheticDispatchWork) domain.SyntheticDispatchWork {
	return domain.SyntheticDispatchWork{
		ID: row.ID, Status: domain.SyntheticDispatchWorkStatus(row.Status),
		SettlementCount: int(row.SettlementCount), CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, SettledAt: row.SettledAt,
	}
}

func (s *Store) CreateExecutionDispatch(ctx context.Context, dispatch domain.ExecutionDispatch) error {
	return s.q(ctx).CreateExecutionDispatch(ctx, postgresdb.CreateExecutionDispatchParams{
		ID: dispatch.ID, Kind: string(dispatch.Kind), WorkID: dispatch.WorkID,
		AccountID: dispatch.AccountID, TenantPartitionID: dispatch.TenantPartitionID,
		Queue: dispatch.Queue, Status: string(dispatch.Status), AvailableAt: dispatch.AvailableAt,
		TaskName: dispatch.TaskName, PublishAttempts: int32(dispatch.PublishAttempts),
		LastError: dispatch.LastError, PublisherOwner: dispatch.PublisherOwner,
		PublisherLeaseExpiresAt: dispatch.PublisherLeaseExpires,
		PublisherAttempt:        dispatch.PublisherAttempt, PublishedAt: dispatch.PublishedAt,
		SettledAt: dispatch.SettledAt, CreatedAt: dispatch.CreatedAt, UpdatedAt: dispatch.UpdatedAt,
	})
}

func (s *Store) GetExecutionDispatch(ctx context.Context, id string) (domain.ExecutionDispatch, error) {
	row, err := s.q(ctx).GetExecutionDispatch(ctx, id)
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeNotFound(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) GetExecutionDispatchForUpdate(ctx context.Context, id string) (domain.ExecutionDispatch, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.ExecutionDispatch{}, fmt.Errorf("execution dispatch row lock requires a transaction")
	}
	row, err := s.q(ctx).GetExecutionDispatchForUpdate(ctx, id)
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeNotFound(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) ClaimNextExecutionDispatch(ctx context.Context, queue, owner string, observedAt, leaseExpiresAt time.Time) (domain.ExecutionDispatch, error) {
	row, err := s.q(ctx).ClaimNextExecutionDispatch(ctx, postgresdb.ClaimNextExecutionDispatchParams{
		QueueName: queue, PublisherOwner: &owner, PublisherLeaseExpiresAt: &leaseExpiresAt, ObservedAt: observedAt,
	})
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeNotFound(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) RenewExecutionDispatchPublication(ctx context.Context, id, owner string, attempt int64, observedAt, leaseExpiresAt time.Time) (domain.ExecutionDispatch, error) {
	row, err := s.q(ctx).RenewExecutionDispatchPublication(ctx, postgresdb.RenewExecutionDispatchPublicationParams{
		ID: id, PublisherOwner: &owner, PublisherAttempt: attempt,
		ObservedAt: observedAt, PublisherLeaseExpiresAt: &leaseExpiresAt,
	})
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeDispatchLeaseLost(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) MarkExecutionDispatchPublished(ctx context.Context, id, owner string, attempt int64, taskName string, observedAt time.Time) (domain.ExecutionDispatch, error) {
	row, err := s.q(ctx).MarkExecutionDispatchPublished(ctx, postgresdb.MarkExecutionDispatchPublishedParams{
		ID: id, PublisherOwner: &owner, PublisherAttempt: attempt,
		TaskName: &taskName, ObservedAt: &observedAt,
	})
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeDispatchLeaseLost(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) ReturnExecutionDispatchPending(ctx context.Context, id, owner string, attempt int64, availableAt time.Time, lastError string, observedAt time.Time) (domain.ExecutionDispatch, error) {
	row, err := s.q(ctx).ReturnExecutionDispatchPending(ctx, postgresdb.ReturnExecutionDispatchPendingParams{
		ID: id, PublisherOwner: &owner, PublisherAttempt: attempt,
		AvailableAt: availableAt, LastError: &lastError, ObservedAt: observedAt,
	})
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeDispatchLeaseLost(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) SettleExecutionDispatch(ctx context.Context, id string, observedAt time.Time) (domain.ExecutionDispatch, error) {
	row, err := s.q(ctx).SettleExecutionDispatch(ctx, postgresdb.SettleExecutionDispatchParams{ID: id, ObservedAt: &observedAt})
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeNotFound(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) SettleActiveExecutionDispatchForWork(ctx context.Context, kind domain.ExecutionDispatchKind, workID string, observedAt time.Time) (int64, error) {
	return s.q(ctx).SettleActiveExecutionDispatchForWork(ctx, postgresdb.SettleActiveExecutionDispatchForWorkParams{
		ObservedAt: &observedAt, Kind: string(kind), WorkID: workID,
	})
}

func (s *Store) AbandonExecutionDispatch(ctx context.Context, id, reason string, observedAt time.Time) (domain.ExecutionDispatch, error) {
	row, err := s.q(ctx).AbandonExecutionDispatch(ctx, postgresdb.AbandonExecutionDispatchParams{
		ID: id, LastError: &reason, ObservedAt: &observedAt,
	})
	if err != nil {
		return domain.ExecutionDispatch{}, normalizeNotFound(err)
	}
	return executionDispatchFromRow(row), nil
}

func (s *Store) ListAgedExecutionDispatches(ctx context.Context, staleBefore time.Time, limit int) ([]domain.ExecutionDispatch, error) {
	rows, err := s.q(ctx).ListAgedExecutionDispatches(ctx, postgresdb.ListAgedExecutionDispatchesParams{
		StaleBefore: staleBefore, BatchLimit: boundedBatchLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	return executionDispatchesFromRows(rows), nil
}

func (s *Store) ListAlertableAgedExecutionDispatches(ctx context.Context, staleBefore, observedAt time.Time, limit int) ([]domain.ExecutionDispatch, error) {
	rows, err := s.q(ctx).ListAlertableAgedExecutionDispatches(ctx, postgresdb.ListAlertableAgedExecutionDispatchesParams{
		StaleBefore: staleBefore, ObservedAt: &observedAt, BatchLimit: boundedBatchLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]domain.ExecutionDispatch, 0, len(rows))
	for _, row := range rows {
		items = append(items, executionDispatchFromRow(row))
	}
	return items, nil
}

func (s *Store) ListStalePublishedExecutionDispatches(ctx context.Context, staleBefore time.Time, limit int) ([]domain.ExecutionDispatch, error) {
	rows, err := s.q(ctx).ListStalePublishedExecutionDispatches(ctx, postgresdb.ListStalePublishedExecutionDispatchesParams{
		StaleBefore: staleBefore, BatchLimit: boundedBatchLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	return executionDispatchesFromRows(rows), nil
}

func (s *Store) PruneTerminalExecutionDispatches(ctx context.Context, before time.Time, limit int) (int64, error) {
	return s.q(ctx).PruneTerminalExecutionDispatches(ctx, postgresdb.PruneTerminalExecutionDispatchesParams{
		PruneBefore: &before, BatchLimit: boundedBatchLimit(limit),
	})
}

func executionDispatchesFromRows(rows []postgresdb.ExecutionDispatch) []domain.ExecutionDispatch {
	dispatches := make([]domain.ExecutionDispatch, len(rows))
	for i, row := range rows {
		dispatches[i] = executionDispatchFromRow(row)
	}
	return dispatches
}

func executionDispatchFromRow(row postgresdb.ExecutionDispatch) domain.ExecutionDispatch {
	return domain.ExecutionDispatch{
		ID: row.ID, Kind: domain.ExecutionDispatchKind(row.Kind), WorkID: row.WorkID,
		AccountID: row.AccountID, TenantPartitionID: row.TenantPartitionID,
		Queue: row.Queue, Status: domain.ExecutionDispatchStatus(row.Status),
		AvailableAt: row.AvailableAt, TaskName: row.TaskName,
		PublishAttempts: int(row.PublishAttempts), LastError: row.LastError,
		PublisherOwner: row.PublisherOwner, PublisherLeaseExpires: row.PublisherLeaseExpiresAt,
		PublisherAttempt: row.PublisherAttempt, PublishedAt: row.PublishedAt,
		SettledAt: row.SettledAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func normalizeDispatchLeaseLost(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrDispatchLeaseLost
	}
	return err
}

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
		ID:        partition.ID,
		AccountID: partition.AccountID,
		TenantKey: partition.TenantKey,
		CreatedAt: partition.CreatedAt,
	})
}

func (s *Store) ResolveTenantPartition(ctx context.Context, partition domain.TenantPartition) (domain.TenantPartition, error) {
	if partition.TenantKey == nil {
		if err := s.q(ctx).CreateDefaultTenantPartitionIfAbsent(ctx, postgresdb.CreateDefaultTenantPartitionIfAbsentParams{
			ID:        partition.ID,
			AccountID: partition.AccountID,
			CreatedAt: partition.CreatedAt,
		}); err != nil {
			return domain.TenantPartition{}, err
		}
		return s.GetDefaultTenantPartition(ctx, partition.AccountID)
	}
	if err := s.q(ctx).CreateTenantPartitionByRefIfAbsent(ctx, postgresdb.CreateTenantPartitionByRefIfAbsentParams{
		ID:        partition.ID,
		AccountID: partition.AccountID,
		TenantKey: partition.TenantKey,
		CreatedAt: partition.CreatedAt,
	}); err != nil {
		return domain.TenantPartition{}, err
	}
	return s.GetTenantPartitionByRef(ctx, partition.AccountID, *partition.TenantKey)
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

func (s *Store) GetTenantPartitionByRef(ctx context.Context, accountID, tenantKey string) (domain.TenantPartition, error) {
	row, err := s.q(ctx).GetTenantPartitionByRef(ctx, postgresdb.GetTenantPartitionByRefParams{
		AccountID: accountID,
		TenantKey: tenantKey,
	})
	if err != nil {
		return domain.TenantPartition{}, normalizeNotFound(err)
	}
	return tenantPartitionFromRow(row), nil
}

func tenantPartitionFromRow(row postgresdb.TenantPartition) domain.TenantPartition {
	return domain.TenantPartition{
		ID:        row.ID,
		AccountID: row.AccountID,
		TenantKey: row.TenantKey,
		CreatedAt: row.CreatedAt,
	}
}

func (s *Store) CreateAgent(ctx context.Context, agent domain.Agent) error {
	return s.q(ctx).CreateAgent(ctx, postgresdb.CreateAgentParams{
		ID:        agent.ID,
		AccountID: agent.AccountID,
		AgentKey:  agent.AgentKey,
		CreatedAt: agent.CreatedAt,
	})
}

func (s *Store) ResolveAgent(ctx context.Context, agent domain.Agent) (domain.Agent, error) {
	if err := s.q(ctx).CreateAgentIfAbsent(ctx, postgresdb.CreateAgentIfAbsentParams{
		ID:        agent.ID,
		AccountID: agent.AccountID,
		AgentKey:  agent.AgentKey,
		CreatedAt: agent.CreatedAt,
	}); err != nil {
		return domain.Agent{}, err
	}
	return s.GetAgentByRef(ctx, agent.AccountID, agent.AgentKey)
}

func (s *Store) GetAgentByRef(ctx context.Context, accountID, agentKey string) (domain.Agent, error) {
	row, err := s.q(ctx).GetAgentByRef(ctx, postgresdb.GetAgentByRefParams{
		AccountID: accountID,
		AgentKey:  agentKey,
	})
	if err != nil {
		return domain.Agent{}, normalizeNotFound(err)
	}
	return domain.Agent{
		ID:        row.ID,
		AccountID: row.AccountID,
		AgentKey:  row.AgentKey,
		CreatedAt: row.CreatedAt,
	}, nil
}

func (s *Store) GetAgentByID(ctx context.Context, id string) (domain.Agent, error) {
	row, err := s.q(ctx).GetAgentByID(ctx, id)
	if err != nil {
		return domain.Agent{}, normalizeNotFound(err)
	}
	return domain.Agent{
		ID:        row.ID,
		AccountID: row.AccountID,
		AgentKey:  row.AgentKey,
		CreatedAt: row.CreatedAt,
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
	var maxOutputTokens *int32
	if invocation.MaxOutputTokens != nil {
		value := int32(*invocation.MaxOutputTokens)
		maxOutputTokens = &value
	}
	return s.q(ctx).CreateInvocation(ctx, postgresdb.CreateInvocationParams{
		ID:                        invocation.ID,
		SessionID:                 invocation.SessionID,
		AccountID:                 invocation.AccountID,
		TenantPartitionID:         invocation.TenantPartitionID,
		AgentID:                   invocation.AgentID,
		SpecSnapshotID:            invocation.SpecSnapshotID,
		IdempotencyKey:            invocation.IdempotencyKey,
		RequestFingerprint:        invocation.RequestFingerprint,
		Status:                    string(invocation.Status),
		RequestFingerprintVersion: int16(invocation.FingerprintVersion),
		CurrentStateRevision:      invocation.CurrentStateRevision,
		ErrorPayload:              invocation.Error,
		TotalTimeoutMs:            invocation.TotalTimeoutMS,
		ActiveTimeoutMs:           invocation.ActiveTimeoutMS,
		WaitingTimeoutMs:          invocation.WaitingTimeoutMS,
		MaxOutputTokens:           maxOutputTokens,
		MaxEstimatedCostMicrousd:  invocation.MaxEstimatedCostMicros,
		MaxIterations:             int32(invocation.MaxIterations),
		ActiveExecutionMs:         invocation.ActiveExecutionMS,
		WaitingExecutionMs:        invocation.WaitingExecutionMS,
		DeadlineAt:                invocation.DeadlineAt,
		OutputSchemaDigest:        invocation.OutputSchemaDigest,
		CreatedAt:                 invocation.CreatedAt,
		UpdatedAt:                 invocation.UpdatedAt,
		CompletedAt:               invocation.CompletedAt,
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

func (s *Store) FindNextQueuedInvocationForUpdate(ctx context.Context, observedAt time.Time) (domain.Invocation, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.Invocation{}, fmt.Errorf("queued Invocation Session lock requires a transaction")
	}
	row, err := s.q(ctx).FindNextQueuedInvocationForUpdate(ctx, observedAt)
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) FindQueuedInvocationWithoutActiveDispatchForUpdate(ctx context.Context, observedAt time.Time) (domain.Invocation, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.Invocation{}, fmt.Errorf("queued Invocation dispatch repair requires a transaction")
	}
	row, err := s.q(ctx).FindQueuedInvocationWithoutActiveDispatchForUpdate(ctx, observedAt)
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

func (s *Store) ListExpiredInvocationDeadlines(ctx context.Context, observedAt time.Time, limit int) ([]domain.Invocation, error) {
	if limit <= 0 {
		return []domain.Invocation{}, nil
	}
	rows, err := s.q(ctx).ListExpiredInvocationDeadlines(ctx, postgresdb.ListExpiredInvocationDeadlinesParams{
		ObservedAt: observedAt, BatchLimit: boundedBatchLimit(limit),
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
	var maxOutputTokens *int
	if row.MaxOutputTokens != nil {
		value := int(*row.MaxOutputTokens)
		maxOutputTokens = &value
	}
	return domain.Invocation{
		ID:                        row.ID,
		SessionID:                 row.SessionID,
		AccountID:                 row.AccountID,
		TenantPartitionID:         row.TenantPartitionID,
		AgentID:                   row.AgentID,
		SpecSnapshotID:            row.SpecSnapshotID,
		IdempotencyKey:            row.IdempotencyKey,
		RequestFingerprint:        row.RequestFingerprint,
		Status:                    domain.InvocationStatus(row.Status),
		FingerprintVersion:        int(row.RequestFingerprintVersion),
		CurrentStateRevision:      row.CurrentStateRevision,
		LeaseOwner:                row.LeaseOwner,
		LeaseExpiresAt:            row.LeaseExpiresAt,
		LeaseAttempt:              row.LeaseAttempt,
		TotalTimeoutMS:            row.TotalTimeoutMs,
		ActiveTimeoutMS:           row.ActiveTimeoutMs,
		WaitingTimeoutMS:          row.WaitingTimeoutMs,
		MaxOutputTokens:           maxOutputTokens,
		MaxEstimatedCostMicros:    row.MaxEstimatedCostMicrousd,
		MaxIterations:             int(row.MaxIterations),
		ActiveExecutionMS:         row.ActiveExecutionMs,
		WaitingExecutionMS:        row.WaitingExecutionMs,
		DeadlineAt:                row.DeadlineAt,
		ActiveSegmentStartedAt:    row.ActiveSegmentStartedAt,
		WaitingSegmentStartedAt:   row.WaitingSegmentStartedAt,
		ExecutionDeadlineAt:       row.ExecutionDeadlineAt,
		ExecutionDeadlineScope:    row.ExecutionDeadlineScope,
		CurrentCheckpointSequence: row.CurrentCheckpointSequence,
		CurrentIteration:          int(row.CurrentIteration),
		OutputSchemaDigest:        row.OutputSchemaDigest,
		Output:                    row.Output,
		OutputProvenance:          row.OutputProvenance,
		Error:                     row.Error,
		Usage:                     row.Usage,
		Provenance:                row.Provenance,
		CreatedAt:                 row.CreatedAt,
		UpdatedAt:                 row.UpdatedAt,
		CompletedAt:               row.CompletedAt,
	}
}

func (s *Store) ClaimInvocation(
	ctx context.Context,
	id, owner string,
	leaseExpiresAt time.Time,
	stateRevision int64,
	observedAt time.Time,
	executionDeadlineAt time.Time,
	executionDeadlineScope string,
) (domain.Invocation, error) {
	row, err := s.q(ctx).ClaimInvocation(ctx, postgresdb.ClaimInvocationParams{
		ID: id, LeaseOwner: &owner, LeaseExpiresAt: &leaseExpiresAt,
		StateRevision: stateRevision, ObservedAt: &observedAt,
		ExecutionDeadlineAt: &executionDeadlineAt, ExecutionDeadlineScope: &executionDeadlineScope,
	})
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) CancelInvocation(ctx context.Context, id string, stateRevision int64, observedAt time.Time) (domain.Invocation, error) {
	row, err := s.q(ctx).CancelInvocation(ctx, postgresdb.CancelInvocationParams{
		ID: id, StateRevision: stateRevision, ObservedAt: &observedAt,
	})
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func (s *Store) ReapInvocationDeadline(ctx context.Context, id string, stateRevision int64, errorPayload []byte, observedAt time.Time) (domain.Invocation, error) {
	row, err := s.q(ctx).ReapInvocationDeadline(ctx, postgresdb.ReapInvocationDeadlineParams{
		ID: id, StateRevision: stateRevision, ErrorPayload: errorPayload, ObservedAt: &observedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
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
	errorPayload, usagePayload, provenancePayload []byte,
	outputPayload, outputProvenancePayload []byte,
	observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).SettleInvocation(ctx, postgresdb.SettleInvocationParams{
		ID:                      id,
		LeaseOwner:              &owner,
		LeaseAttempt:            attempt,
		Status:                  string(status),
		StateRevision:           stateRevision,
		ErrorPayload:            errorPayload,
		UsagePayload:            usagePayload,
		ProvenancePayload:       provenancePayload,
		OutputPayload:           outputPayload,
		OutputProvenancePayload: outputProvenancePayload,
		ObservedAt:              &observedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) ParkInvocationForHostTools(
	ctx context.Context,
	id, owner string,
	attempt, stateRevision int64,
	observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).ParkInvocationForHostTools(ctx, postgresdb.ParkInvocationForHostToolsParams{
		ID:            id,
		LeaseOwner:    &owner,
		LeaseAttempt:  attempt,
		StateRevision: stateRevision,
		ObservedAt:    &observedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) QueueWaitingInvocation(
	ctx context.Context,
	id string,
	stateRevision int64,
	observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).QueueWaitingInvocation(ctx, postgresdb.QueueWaitingInvocationParams{
		ID:            id,
		StateRevision: stateRevision,
		ObservedAt:    observedAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) RecoverInvocationLease(
	ctx context.Context,
	id string,
	attempt, stateRevision int64,
	observedAt time.Time,
) (domain.Invocation, error) {
	row, err := s.q(ctx).RecoverInvocationLease(ctx, postgresdb.RecoverInvocationLeaseParams{
		ID:            id,
		LeaseAttempt:  attempt,
		StateRevision: stateRevision,
		ObservedAt:    observedAt,
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
		messages[i] = sessionMessageFromRow(row)
	}
	return messages, nil
}

func (s *Store) ListSessionMessagesByInvocation(ctx context.Context, invocationID string) ([]domain.SessionMessage, error) {
	rows, err := s.q(ctx).ListSessionMessagesByInvocation(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	messages := make([]domain.SessionMessage, len(rows))
	for i, row := range rows {
		messages[i] = sessionMessageFromRow(row)
	}
	return messages, nil
}

func (s *Store) ListSessionMessagesForGeneration(ctx context.Context, sessionID string) ([]domain.SessionMessage, error) {
	rows, err := s.q(ctx).ListSessionMessagesForGeneration(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	messages := make([]domain.SessionMessage, len(rows))
	for i, row := range rows {
		messages[i] = domain.SessionMessage{
			ID:                row.ID,
			SessionID:         row.SessionID,
			AccountID:         row.AccountID,
			TenantPartitionID: row.TenantPartitionID,
			AgentID:           row.AgentID,
			InvocationID:      row.InvocationID,
			Sequence:          row.Sequence,
			Role:              domain.MessageRole(row.Role),
			Content:           row.Content,
			CreatedAt:         row.CreatedAt,
		}
	}
	return messages, nil
}

func sessionMessageFromRow(row postgresdb.SessionMessage) domain.SessionMessage {
	return domain.SessionMessage{
		ID: row.ID, SessionID: row.SessionID, AccountID: row.AccountID,
		TenantPartitionID: row.TenantPartitionID, AgentID: row.AgentID,
		InvocationID: row.InvocationID, Sequence: row.Sequence,
		Role: domain.MessageRole(row.Role), Content: row.Content, CreatedAt: row.CreatedAt,
	}
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

func (s *Store) ListInvocations(ctx context.Context, query ports.InvocationListQuery) ([]domain.Invocation, error) {
	status := (*string)(nil)
	if query.Status != nil {
		value := string(*query.Status)
		status = &value
	}
	rows, err := s.q(ctx).ListInvocationsForRecovery(ctx, postgresdb.ListInvocationsForRecoveryParams{
		AccountID:         query.AccountID,
		TenantPartitionID: query.TenantPartitionID,
		SessionID:         query.SessionID,
		AgentID:           query.AgentID,
		Status:            status,
		BeforeCreatedAt:   query.BeforeCreatedAt,
		BeforeID:          query.BeforeInvocationID,
		BatchLimit:        boundedBatchLimit(query.Limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]domain.Invocation, len(rows))
	for i, row := range rows {
		items[i] = invocationFromRow(row)
	}
	return items, nil
}

func (s *Store) ListSessions(ctx context.Context, query ports.SessionListQuery) ([]ports.SessionRecoveryRow, error) {
	rows, err := s.q(ctx).ListSessionsForRecovery(ctx, postgresdb.ListSessionsForRecoveryParams{
		AccountID:         query.AccountID,
		TenantPartitionID: query.TenantPartitionID,
		AgentID:           query.AgentID,
		SessionKey:        query.SessionKey,
		BeforeCreatedAt:   query.BeforeCreatedAt,
		BeforeID:          query.BeforeSessionID,
		BatchLimit:        boundedBatchLimit(query.Limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]ports.SessionRecoveryRow, len(rows))
	for i, row := range rows {
		items[i] = ports.SessionRecoveryRow{
			Session: domain.Session{
				ID: row.ID, AccountID: row.AccountID, TenantPartitionID: row.TenantPartitionID,
				AgentID: row.AgentID, SessionKey: row.SessionKey,
				NextMessageSequence: row.NextMessageSequence, NextLifecycleRevision: row.NextLifecycleRevision,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
			},
			TenantKey: row.TenantKey,
		}
		if row.ActiveInvocationID != "" {
			status := domain.InvocationStatus(row.ActiveInvocationStatus)
			items[i].ActiveInvocationID = &row.ActiveInvocationID
			items[i].ActiveInvocationStatus = &status
		}
	}
	return items, nil
}

func (s *Store) ListSessionMessagesRange(ctx context.Context, sessionID string, after, through int64, limit int) ([]domain.SessionMessage, error) {
	rows, err := s.q(ctx).ListSessionMessagesRange(ctx, postgresdb.ListSessionMessagesRangeParams{
		SessionID: sessionID, AfterSequence: after, ThroughSequence: through,
		BatchLimit: boundedBatchLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]domain.SessionMessage, len(rows))
	for i, row := range rows {
		items[i] = sessionMessageFromRow(row)
	}
	return items, nil
}

func (s *Store) ListInvocationLifecycleChanges(ctx context.Context, sessionID string, after, through int64, limit int) ([]domain.InvocationLifecycleChange, error) {
	rows, err := s.q(ctx).ListInvocationLifecycleChanges(ctx, postgresdb.ListInvocationLifecycleChangesParams{
		SessionID: sessionID, AfterRevision: after, ThroughRevision: through,
		BatchLimit: boundedBatchLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	items := make([]domain.InvocationLifecycleChange, len(rows))
	for i, row := range rows {
		state := domain.InvocationState{
			ID: row.ID, InvocationID: row.InvocationID, SessionID: row.SessionID,
			AccountID: row.AccountID, TenantPartitionID: row.TenantPartitionID,
			AgentID: row.AgentID, Revision: row.Revision,
			Status: domain.InvocationStatus(row.Status), LeaseAttempt: row.LeaseAttempt,
			ThroughMessageSequence: row.ThroughMessageSequence, CreatedAt: row.CreatedAt,
		}
		items[i].InvocationState = state
		if state.Status.Terminal() {
			items[i].Error = row.Error
			items[i].Usage = row.Usage
			items[i].Provenance = row.Provenance
			items[i].Output = row.Output
			items[i].OutputProvenance = row.OutputProvenance
		}
	}
	return items, nil
}

func boundedBatchLimit(limit int) int32 {
	if limit <= 0 {
		return 0
	}
	if limit > int(^uint32(0)>>1) {
		return int32(^uint32(0) >> 1)
	}
	return int32(limit)
}

func normalizeNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrNotFound
	}
	return err
}
