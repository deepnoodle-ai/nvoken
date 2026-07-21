package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	postgresdb "github.com/deepnoodle-ai/nvoken/internal/adapters/postgres/sqlc"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func (s *Store) CreateToolCall(ctx context.Context, call domain.ToolCall) error {
	return s.q(ctx).CreateToolCall(ctx, postgresdb.CreateToolCallParams{
		ID:                     call.ID,
		InvocationID:           call.InvocationID,
		SessionID:              call.SessionID,
		AccountID:              call.AccountID,
		TenantPartitionID:      call.TenantPartitionID,
		AgentID:                call.AgentID,
		Iteration:              int32(call.Iteration),
		BatchOrdinal:           int32(call.BatchOrdinal),
		ProviderCallID:         call.ProviderCallID,
		Name:                   call.Name,
		Mode:                   string(call.Mode),
		RequestMessageID:       call.RequestMessageID,
		RequestMessageSequence: call.RequestMessageSequence,
		RequestDigest:          call.RequestDigest,
		Status:                 string(call.Status),
		DeadlineAt:             call.DeadlineAt,
		CurrentAttempt:         int32(call.CurrentAttempt),
		ResultMessageID:        call.ResultMessageID,
		ResultMessageSequence:  call.ResultMessageSequence,
		CreatedAt:              call.CreatedAt,
		UpdatedAt:              call.UpdatedAt,
		CompletedAt:            call.CompletedAt,
	})
}

func (s *Store) GetToolCall(ctx context.Context, id string) (domain.ToolCall, error) {
	row, err := s.q(ctx).GetToolCall(ctx, id)
	if err != nil {
		return domain.ToolCall{}, normalizeNotFound(err)
	}
	return toolCallFromRow(row), nil
}

func (s *Store) GetToolCallForUpdate(ctx context.Context, id string) (domain.ToolCall, error) {
	row, err := s.q(ctx).GetToolCallForUpdate(ctx, id)
	if err != nil {
		return domain.ToolCall{}, normalizeNotFound(err)
	}
	return toolCallFromRow(row), nil
}

func (s *Store) GetToolCallByProviderIdentityForUpdate(ctx context.Context, invocationID string, iteration int, providerCallID string) (domain.ToolCall, error) {
	row, err := s.q(ctx).GetToolCallByProviderIdentityForUpdate(ctx, postgresdb.GetToolCallByProviderIdentityForUpdateParams{
		InvocationID:   invocationID,
		Iteration:      int32(iteration),
		ProviderCallID: providerCallID,
	})
	if err != nil {
		return domain.ToolCall{}, normalizeNotFound(err)
	}
	return toolCallFromRow(row), nil
}

func (s *Store) ListOpenToolCallsForUpdate(ctx context.Context, invocationID string) ([]domain.ToolCall, error) {
	rows, err := s.q(ctx).ListOpenToolCallsForUpdate(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	items := make([]domain.ToolCall, len(rows))
	for index, row := range rows {
		items[index] = toolCallFromRow(row)
	}
	return items, nil
}

func (s *Store) ListToolCallsByIteration(ctx context.Context, invocationID string, iteration int) ([]domain.ToolCall, error) {
	rows, err := s.q(ctx).ListToolCallsByIteration(ctx, postgresdb.ListToolCallsByIterationParams{
		InvocationID: invocationID,
		Iteration:    int32(iteration),
	})
	if err != nil {
		return nil, err
	}
	items := make([]domain.ToolCall, len(rows))
	for index, row := range rows {
		items[index] = toolCallFromRow(row)
	}
	return items, nil
}

func (s *Store) StartToolCallAttempt(ctx context.Context, id string, observedAt time.Time) (domain.ToolCall, error) {
	row, err := s.q(ctx).StartToolCallAttempt(ctx, postgresdb.StartToolCallAttemptParams{
		ID:         id,
		ObservedAt: observedAt,
	})
	if err != nil {
		return domain.ToolCall{}, normalizeNotFound(err)
	}
	return toolCallFromRow(row), nil
}

func (s *Store) CreateToolCallAttempt(ctx context.Context, attempt domain.ToolCallAttempt) error {
	return s.q(ctx).CreateToolCallAttempt(ctx, postgresdb.CreateToolCallAttemptParams{
		ID:                     attempt.ID,
		ToolCallID:             attempt.ToolCallID,
		InvocationID:           attempt.InvocationID,
		SessionID:              attempt.SessionID,
		AccountID:              attempt.AccountID,
		TenantPartitionID:      attempt.TenantPartitionID,
		AgentID:                attempt.AgentID,
		Attempt:                int32(attempt.Attempt),
		InvocationLeaseAttempt: attempt.InvocationLeaseAttempt,
		Status:                 string(attempt.Status),
		StartedAt:              attempt.StartedAt,
		CompletedAt:            attempt.CompletedAt,
	})
}

func (s *Store) SettleToolCall(ctx context.Context, id string, status domain.ToolCallStatus, messageID string, sequence int64, observedAt time.Time) (domain.ToolCall, error) {
	row, err := s.q(ctx).SettleToolCall(ctx, postgresdb.SettleToolCallParams{
		ID:                    id,
		Status:                string(status),
		ResultMessageID:       &messageID,
		ResultMessageSequence: &sequence,
		ObservedAt:            &observedAt,
	})
	if err != nil {
		return domain.ToolCall{}, normalizeNotFound(err)
	}
	return toolCallFromRow(row), nil
}

func (s *Store) SettleToolCallAttempt(ctx context.Context, id string, status domain.ToolCallStatus, observedAt time.Time) (domain.ToolCallAttempt, error) {
	row, err := s.q(ctx).SettleToolCallAttempt(ctx, postgresdb.SettleToolCallAttemptParams{
		ID:         id,
		Status:     string(status),
		ObservedAt: &observedAt,
	})
	if err != nil {
		return domain.ToolCallAttempt{}, normalizeNotFound(err)
	}
	return toolCallAttemptFromRow(row), nil
}

func (s *Store) SettleRunningToolCallAttempts(ctx context.Context, id string, status domain.ToolCallStatus, observedAt time.Time) (int64, error) {
	return s.q(ctx).SettleRunningToolCallAttempts(ctx, postgresdb.SettleRunningToolCallAttemptsParams{
		ToolCallID: id,
		Status:     string(status),
		ObservedAt: &observedAt,
	})
}

func (s *Store) CreateModelUsageReceipt(ctx context.Context, receipt domain.ModelUsageReceipt) error {
	return s.q(ctx).CreateModelUsageReceipt(ctx, postgresdb.CreateModelUsageReceiptParams{
		ID:                receipt.ID,
		InvocationID:      receipt.InvocationID,
		SessionID:         receipt.SessionID,
		AccountID:         receipt.AccountID,
		TenantPartitionID: receipt.TenantPartitionID,
		AgentID:           receipt.AgentID,
		Iteration:         int32(receipt.Iteration),
		MessageID:         receipt.MessageID,
		MessageSequence:   receipt.MessageSequence,
		Usage:             receipt.Usage,
		Provenance:        receipt.Provenance,
		EvidenceDigest:    receipt.EvidenceDigest,
		CreatedAt:         receipt.CreatedAt,
	})
}

func (s *Store) GetModelUsageReceiptByIteration(ctx context.Context, invocationID string, iteration int) (domain.ModelUsageReceipt, error) {
	row, err := s.q(ctx).GetModelUsageReceiptByIteration(ctx, postgresdb.GetModelUsageReceiptByIterationParams{
		InvocationID: invocationID,
		Iteration:    int32(iteration),
	})
	if err != nil {
		return domain.ModelUsageReceipt{}, normalizeNotFound(err)
	}
	return modelUsageReceiptFromRow(row), nil
}

func (s *Store) ListModelUsageReceipts(ctx context.Context, invocationID string) ([]domain.ModelUsageReceipt, error) {
	rows, err := s.q(ctx).ListModelUsageReceipts(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	items := make([]domain.ModelUsageReceipt, len(rows))
	for index, row := range rows {
		items[index] = modelUsageReceiptFromRow(row)
	}
	return items, nil
}

func (s *Store) CreateInvocationCheckpoint(ctx context.Context, checkpoint domain.InvocationCheckpoint) error {
	return s.q(ctx).CreateInvocationCheckpoint(ctx, postgresdb.CreateInvocationCheckpointParams{
		ID:                     checkpoint.ID,
		InvocationID:           checkpoint.InvocationID,
		SessionID:              checkpoint.SessionID,
		AccountID:              checkpoint.AccountID,
		TenantPartitionID:      checkpoint.TenantPartitionID,
		AgentID:                checkpoint.AgentID,
		Sequence:               checkpoint.Sequence,
		Iteration:              int32(checkpoint.Iteration),
		Kind:                   string(checkpoint.Kind),
		LeaseAttempt:           checkpoint.LeaseAttempt,
		ThroughMessageSequence: checkpoint.ThroughMessageSequence,
		UsageReceiptID:         checkpoint.UsageReceiptID,
		ToolCallID:             checkpoint.ToolCallID,
		CreatedAt:              checkpoint.CreatedAt,
	})
}

func (s *Store) GetLatestInvocationCheckpoint(ctx context.Context, invocationID string) (domain.InvocationCheckpoint, error) {
	row, err := s.q(ctx).GetLatestInvocationCheckpoint(ctx, invocationID)
	if err != nil {
		return domain.InvocationCheckpoint{}, normalizeNotFound(err)
	}
	return invocationCheckpointFromRow(row), nil
}

func (s *Store) ListInvocationCheckpoints(ctx context.Context, invocationID string) ([]domain.InvocationCheckpoint, error) {
	rows, err := s.q(ctx).ListInvocationCheckpoints(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	items := make([]domain.InvocationCheckpoint, len(rows))
	for index, row := range rows {
		items[index] = invocationCheckpointFromRow(row)
	}
	return items, nil
}

func (s *Store) AdvanceInvocationCheckpoint(ctx context.Context, id, owner string, attempt int64, observedAt time.Time, sequence int64, iteration int) (domain.Invocation, error) {
	row, err := s.q(ctx).AdvanceInvocationCheckpoint(ctx, postgresdb.AdvanceInvocationCheckpointParams{
		ID:                 id,
		LeaseOwner:         &owner,
		LeaseAttempt:       attempt,
		ObservedAt:         observedAt,
		CheckpointSequence: sequence,
		Iteration:          int32(iteration),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Invocation{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.Invocation{}, err
	}
	return invocationFromRow(row), nil
}

func (s *Store) AdvanceInvocationCheckpointForTerminal(ctx context.Context, id string, sequence int64, iteration int) (domain.Invocation, error) {
	row, err := s.q(ctx).AdvanceInvocationCheckpointForTerminal(ctx, postgresdb.AdvanceInvocationCheckpointForTerminalParams{
		ID:                 id,
		CheckpointSequence: sequence,
		Iteration:          int32(iteration),
	})
	if err != nil {
		return domain.Invocation{}, normalizeNotFound(err)
	}
	return invocationFromRow(row), nil
}

func toolCallFromRow(row postgresdb.ToolCall) domain.ToolCall {
	return domain.ToolCall{
		ID:                     row.ID,
		InvocationID:           row.InvocationID,
		SessionID:              row.SessionID,
		AccountID:              row.AccountID,
		TenantPartitionID:      row.TenantPartitionID,
		AgentID:                row.AgentID,
		Iteration:              int(row.Iteration),
		BatchOrdinal:           int(row.BatchOrdinal),
		ProviderCallID:         row.ProviderCallID,
		Name:                   row.Name,
		Mode:                   domain.ToolCallMode(row.Mode),
		RequestMessageID:       row.RequestMessageID,
		RequestMessageSequence: row.RequestMessageSequence,
		RequestDigest:          row.RequestDigest,
		Status:                 domain.ToolCallStatus(row.Status),
		DeadlineAt:             row.DeadlineAt,
		CurrentAttempt:         int(row.CurrentAttempt),
		ResultMessageID:        row.ResultMessageID,
		ResultMessageSequence:  row.ResultMessageSequence,
		CreatedAt:              row.CreatedAt,
		UpdatedAt:              row.UpdatedAt,
		CompletedAt:            row.CompletedAt,
	}
}

func toolCallAttemptFromRow(row postgresdb.ToolCallAttempt) domain.ToolCallAttempt {
	return domain.ToolCallAttempt{
		ID:                     row.ID,
		ToolCallID:             row.ToolCallID,
		InvocationID:           row.InvocationID,
		SessionID:              row.SessionID,
		AccountID:              row.AccountID,
		TenantPartitionID:      row.TenantPartitionID,
		AgentID:                row.AgentID,
		Attempt:                int(row.Attempt),
		InvocationLeaseAttempt: row.InvocationLeaseAttempt,
		Status:                 domain.ToolCallStatus(row.Status),
		StartedAt:              row.StartedAt,
		CompletedAt:            row.CompletedAt,
	}
}

func modelUsageReceiptFromRow(row postgresdb.ModelUsageReceipt) domain.ModelUsageReceipt {
	return domain.ModelUsageReceipt{
		ID:                row.ID,
		InvocationID:      row.InvocationID,
		SessionID:         row.SessionID,
		AccountID:         row.AccountID,
		TenantPartitionID: row.TenantPartitionID,
		AgentID:           row.AgentID,
		Iteration:         int(row.Iteration),
		MessageID:         row.MessageID,
		MessageSequence:   row.MessageSequence,
		Usage:             row.Usage,
		Provenance:        row.Provenance,
		EvidenceDigest:    row.EvidenceDigest,
		CreatedAt:         row.CreatedAt,
	}
}

func invocationCheckpointFromRow(row postgresdb.InvocationCheckpoint) domain.InvocationCheckpoint {
	return domain.InvocationCheckpoint{
		ID:                     row.ID,
		InvocationID:           row.InvocationID,
		SessionID:              row.SessionID,
		AccountID:              row.AccountID,
		TenantPartitionID:      row.TenantPartitionID,
		AgentID:                row.AgentID,
		Sequence:               row.Sequence,
		Iteration:              int(row.Iteration),
		Kind:                   domain.InvocationCheckpointKind(row.Kind),
		LeaseAttempt:           row.LeaseAttempt,
		ThroughMessageSequence: row.ThroughMessageSequence,
		UsageReceiptID:         row.UsageReceiptID,
		ToolCallID:             row.ToolCallID,
		CreatedAt:              row.CreatedAt,
	}
}
