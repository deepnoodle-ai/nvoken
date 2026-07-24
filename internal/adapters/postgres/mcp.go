package postgres

import (
	"context"
	"errors"
	"time"

	postgresdb "github.com/deepnoodle-ai/nvoken/internal/adapters/postgres/sqlc"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateInvocationMCPServerBinding(
	ctx context.Context,
	binding domain.InvocationMCPServerBinding,
) error {
	return s.q(ctx).CreateInvocationMCPServerBinding(ctx, postgresdb.CreateInvocationMCPServerBindingParams{
		ID:                binding.ID,
		InvocationID:      binding.InvocationID,
		AccountID:         binding.AccountID,
		TenantPartitionID: binding.TenantPartitionID,
		ServerName:        binding.ServerName,
		EncryptionKeyID:   binding.EncryptionKeyID,
		Nonce:             binding.Nonce,
		Ciphertext:        binding.Ciphertext,
		ExpiresAt:         binding.ExpiresAt,
		ClearedAt:         binding.ClearedAt,
		CreatedAt:         binding.CreatedAt,
	})
}

func (s *Store) GetInvocationMCPServerBinding(
	ctx context.Context,
	invocationID string,
	serverName string,
) (domain.InvocationMCPServerBinding, error) {
	row, err := s.q(ctx).GetInvocationMCPServerBinding(ctx, postgresdb.GetInvocationMCPServerBindingParams{
		InvocationID: invocationID,
		ServerName:   serverName,
	})
	if err != nil {
		return domain.InvocationMCPServerBinding{}, normalizeNotFound(err)
	}
	return invocationMCPServerBindingFromRow(row), nil
}

func (s *Store) ListInvocationMCPServerBindings(
	ctx context.Context,
	invocationID string,
) ([]domain.InvocationMCPServerBinding, error) {
	rows, err := s.q(ctx).ListInvocationMCPServerBindings(ctx, invocationID)
	if err != nil {
		return nil, err
	}
	bindings := make([]domain.InvocationMCPServerBinding, len(rows))
	for index, row := range rows {
		bindings[index] = invocationMCPServerBindingFromRow(row)
	}
	return bindings, nil
}

func (s *Store) ClearExpiredMCPServerBindingMaterial(
	ctx context.Context,
	observedAt time.Time,
	limit int,
) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	return s.q(ctx).ClearExpiredMCPServerBindingMaterial(ctx, postgresdb.ClearExpiredMCPServerBindingMaterialParams{
		ObservedAt: &observedAt,
		BatchLimit: boundedBatchLimit(limit),
	})
}

func (s *Store) CreateInvocationMCPDiscovery(
	ctx context.Context,
	discovery domain.InvocationMCPDiscovery,
	leaseOwner string,
	leaseAttempt int64,
) (domain.InvocationMCPDiscovery, error) {
	row, err := s.q(ctx).CreateInvocationMCPDiscovery(ctx, postgresdb.CreateInvocationMCPDiscoveryParams{
		ID:                discovery.ID,
		InvocationID:      discovery.InvocationID,
		AccountID:         discovery.AccountID,
		TenantPartitionID: discovery.TenantPartitionID,
		Catalog:           discovery.Catalog,
		CreatedAt:         discovery.CreatedAt,
		LeaseOwner:        &leaseOwner,
		LeaseAttempt:      leaseAttempt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.InvocationMCPDiscovery{}, ports.ErrLeaseLost
	}
	if err != nil {
		return domain.InvocationMCPDiscovery{}, err
	}
	return invocationMCPDiscoveryFromRow(row), nil
}

func (s *Store) GetInvocationMCPDiscovery(
	ctx context.Context,
	invocationID string,
) (domain.InvocationMCPDiscovery, error) {
	row, err := s.q(ctx).GetInvocationMCPDiscovery(ctx, invocationID)
	if err != nil {
		return domain.InvocationMCPDiscovery{}, normalizeNotFound(err)
	}
	return invocationMCPDiscoveryFromRow(row), nil
}

func invocationMCPServerBindingFromRow(
	row postgresdb.InvocationMcpServerBinding,
) domain.InvocationMCPServerBinding {
	return domain.InvocationMCPServerBinding{
		ID:                row.ID,
		InvocationID:      row.InvocationID,
		AccountID:         row.AccountID,
		TenantPartitionID: row.TenantPartitionID,
		ServerName:        row.ServerName,
		EncryptionKeyID:   row.EncryptionKeyID,
		Nonce:             row.Nonce,
		Ciphertext:        row.Ciphertext,
		ExpiresAt:         row.ExpiresAt,
		ClearedAt:         row.ClearedAt,
		CreatedAt:         row.CreatedAt,
	}
}

func invocationMCPDiscoveryFromRow(
	row postgresdb.InvocationMcpDiscovery,
) domain.InvocationMCPDiscovery {
	return domain.InvocationMCPDiscovery{
		ID:                row.ID,
		InvocationID:      row.InvocationID,
		AccountID:         row.AccountID,
		TenantPartitionID: row.TenantPartitionID,
		Catalog:           row.Catalog,
		CreatedAt:         row.CreatedAt,
	}
}
