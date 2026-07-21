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

func (s *Store) CreateCallbackDelivery(ctx context.Context, delivery domain.CallbackDelivery) error {
	return s.q(ctx).CreateCallbackDelivery(ctx, postgresdb.CreateCallbackDeliveryParams{
		ID:                delivery.ID,
		ToolCallID:        delivery.ToolCallID,
		InvocationID:      delivery.InvocationID,
		SessionID:         delivery.SessionID,
		AccountID:         delivery.AccountID,
		TenantPartitionID: delivery.TenantPartitionID,
		AgentID:           delivery.AgentID,
		EndpointUrl:       delivery.EndpointURL,
		Status:            string(delivery.Status),
		AvailableAt:       delivery.AvailableAt,
		Owner:             delivery.Owner,
		LeaseExpiresAt:    delivery.LeaseExpiresAt,
		Attempt:           delivery.Attempt,
		LastErrorCode:     delivery.LastErrorCode,
		ResponseStatus:    int32Pointer(delivery.ResponseStatus),
		CreatedAt:         delivery.CreatedAt,
		UpdatedAt:         delivery.UpdatedAt,
		TerminalAt:        delivery.TerminalAt,
	})
}

func (s *Store) GetCallbackDelivery(ctx context.Context, id string) (domain.CallbackDelivery, error) {
	row, err := s.q(ctx).GetCallbackDelivery(ctx, id)
	if err != nil {
		return domain.CallbackDelivery{}, normalizeNotFound(err)
	}
	return callbackDeliveryFromRow(row), nil
}

func (s *Store) GetCallbackDeliveryForUpdate(ctx context.Context, id string) (domain.CallbackDelivery, error) {
	row, err := s.q(ctx).GetCallbackDeliveryForUpdate(ctx, id)
	if err != nil {
		return domain.CallbackDelivery{}, normalizeNotFound(err)
	}
	return callbackDeliveryFromRow(row), nil
}

func (s *Store) ActivateCallbackDeliveries(ctx context.Context, invocationID string, observedAt time.Time) (int64, error) {
	return s.q(ctx).ActivateCallbackDeliveries(ctx, postgresdb.ActivateCallbackDeliveriesParams{
		ObservedAt:   &observedAt,
		InvocationID: invocationID,
	})
}

func (s *Store) ClaimNextCallbackDelivery(
	ctx context.Context,
	owner string,
	observedAt time.Time,
	leaseExpiresAt time.Time,
) (domain.CallbackDelivery, error) {
	row, err := s.q(ctx).ClaimNextCallbackDelivery(ctx, postgresdb.ClaimNextCallbackDeliveryParams{
		Owner:          &owner,
		LeaseExpiresAt: &leaseExpiresAt,
		ObservedAt:     observedAt,
	})
	if err != nil {
		return domain.CallbackDelivery{}, normalizeNotFound(err)
	}
	return callbackDeliveryFromRow(row), nil
}

func (s *Store) ReturnCallbackDeliveryPending(
	ctx context.Context,
	id string,
	owner string,
	attempt int64,
	availableAt time.Time,
	errorCode string,
	observedAt time.Time,
) (domain.CallbackDelivery, error) {
	row, err := s.q(ctx).ReturnCallbackDeliveryPending(ctx, postgresdb.ReturnCallbackDeliveryPendingParams{
		AvailableAt:   &availableAt,
		LastErrorCode: &errorCode,
		ObservedAt:    observedAt,
		ID:            id,
		Owner:         &owner,
		Attempt:       attempt,
	})
	if err != nil {
		return domain.CallbackDelivery{}, normalizeCallbackLeaseLost(err)
	}
	return callbackDeliveryFromRow(row), nil
}

func (s *Store) SettleCallbackDelivery(
	ctx context.Context,
	id string,
	owner string,
	attempt int64,
	status domain.CallbackDeliveryStatus,
	errorCode string,
	responseStatus *int,
	observedAt time.Time,
) (domain.CallbackDelivery, error) {
	var errorCodePointer *string
	if errorCode != "" {
		errorCodePointer = &errorCode
	}
	row, err := s.q(ctx).SettleCallbackDelivery(ctx, postgresdb.SettleCallbackDeliveryParams{
		Status:         string(status),
		LastErrorCode:  errorCodePointer,
		ResponseStatus: int32Pointer(responseStatus),
		ObservedAt:     observedAt,
		ID:             id,
		Owner:          &owner,
		Attempt:        attempt,
	})
	if err != nil {
		return domain.CallbackDelivery{}, normalizeCallbackLeaseLost(err)
	}
	return callbackDeliveryFromRow(row), nil
}

func (s *Store) AbandonActiveCallbackDeliveries(
	ctx context.Context,
	invocationID string,
	errorCode string,
	observedAt time.Time,
) (int64, error) {
	return s.q(ctx).AbandonActiveCallbackDeliveries(ctx, postgresdb.AbandonActiveCallbackDeliveriesParams{
		LastErrorCode: &errorCode,
		ObservedAt:    observedAt,
		InvocationID:  invocationID,
	})
}

func (s *Store) RecoverExpiredCallbackDeliveries(ctx context.Context, observedAt time.Time, limit int) (int64, error) {
	return s.q(ctx).RecoverExpiredCallbackDeliveries(ctx, postgresdb.RecoverExpiredCallbackDeliveriesParams{
		ObservedAt: &observedAt,
		BatchLimit: boundedBatchLimit(limit),
	})
}

func (s *Store) PruneTerminalCallbackDeliveries(ctx context.Context, before time.Time, limit int) (int64, error) {
	return s.q(ctx).PruneTerminalCallbackDeliveries(ctx, postgresdb.PruneTerminalCallbackDeliveriesParams{
		PruneBefore: &before,
		BatchLimit:  boundedBatchLimit(limit),
	})
}

func callbackDeliveryFromRow(row postgresdb.CallbackDelivery) domain.CallbackDelivery {
	return domain.CallbackDelivery{
		ID:                row.ID,
		ToolCallID:        row.ToolCallID,
		InvocationID:      row.InvocationID,
		SessionID:         row.SessionID,
		AccountID:         row.AccountID,
		TenantPartitionID: row.TenantPartitionID,
		AgentID:           row.AgentID,
		EndpointURL:       row.EndpointUrl,
		Status:            domain.CallbackDeliveryStatus(row.Status),
		AvailableAt:       row.AvailableAt,
		Owner:             row.Owner,
		LeaseExpiresAt:    row.LeaseExpiresAt,
		Attempt:           row.Attempt,
		LastErrorCode:     row.LastErrorCode,
		ResponseStatus:    intPointer(row.ResponseStatus),
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		TerminalAt:        row.TerminalAt,
	}
}

func normalizeCallbackLeaseLost(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrCallbackDeliveryLeaseLost
	}
	return err
}

func int32Pointer(value *int) *int32 {
	if value == nil {
		return nil
	}
	converted := int32(*value)
	return &converted
}

func intPointer(value *int32) *int {
	if value == nil {
		return nil
	}
	converted := int(*value)
	return &converted
}
