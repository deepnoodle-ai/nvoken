package postgres

import (
	"context"
	"fmt"
	"time"

	postgresdb "github.com/deepnoodle-ai/nvoken/internal/adapters/postgres/sqlc"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func (s *Store) CreateProviderCredential(ctx context.Context, credential domain.ProviderCredential) error {
	return s.q(ctx).CreateProviderCredential(ctx, postgresdb.CreateProviderCredentialParams{
		ID:                   credential.ID,
		AccountID:            credential.AccountID,
		TenantPartitionID:    credential.TenantPartitionID,
		Provider:             credential.Provider,
		Scope:                string(credential.Scope),
		Status:               string(credential.Status),
		CurrentVersionID:     credential.CurrentVersionID,
		CurrentVersion:       int32(credential.CurrentVersion),
		CreateIdempotencyKey: credential.CreateIdempotencyKey,
		CreateFingerprint:    credential.CreateFingerprint,
		CreatedBy:            credential.CreatedBy,
		CreatedAt:            credential.CreatedAt,
		UpdatedAt:            credential.UpdatedAt,
		RevokedAt:            credential.RevokedAt,
	})
}

func (s *Store) CreateProviderCredentialVersion(ctx context.Context, version domain.ProviderCredentialVersion) error {
	return s.q(ctx).CreateProviderCredentialVersion(ctx, postgresdb.CreateProviderCredentialVersionParams{
		ID:                     version.ID,
		ProviderCredentialID:   version.ProviderCredentialID,
		AccountID:              version.AccountID,
		TenantPartitionID:      version.TenantPartitionID,
		Provider:               version.Provider,
		Version:                int32(version.Version),
		Status:                 string(version.Status),
		PreviousVersionID:      version.PreviousVersionID,
		EncryptionKeyID:        version.EncryptionKeyID,
		Nonce:                  version.Nonce,
		Ciphertext:             version.Ciphertext,
		ExpiresAt:              version.ExpiresAt,
		OverlapExpiresAt:       version.OverlapExpiresAt,
		RotationIdempotencyKey: version.RotationIdempotencyKey,
		RotationFingerprint:    version.RotationFingerprint,
		CreatedBy:              version.CreatedBy,
		CreatedAt:              version.CreatedAt,
		DestroyedAt:            version.DestroyedAt,
	})
}

func (s *Store) GetProviderCredential(ctx context.Context, id string) (domain.ProviderCredential, error) {
	row, err := s.q(ctx).GetProviderCredential(ctx, id)
	if err != nil {
		return domain.ProviderCredential{}, normalizeNotFound(err)
	}
	return providerCredentialFromRow(row), nil
}

func (s *Store) GetProviderCredentialForUpdate(ctx context.Context, id string) (domain.ProviderCredential, error) {
	if _, ok := transactionFromContext(ctx); !ok {
		return domain.ProviderCredential{}, fmt.Errorf("provider credential row lock requires a transaction")
	}
	row, err := s.q(ctx).GetProviderCredentialForUpdate(ctx, id)
	if err != nil {
		return domain.ProviderCredential{}, normalizeNotFound(err)
	}
	return providerCredentialFromRow(row), nil
}

func (s *Store) GetProviderCredentialVersion(ctx context.Context, id string) (domain.ProviderCredentialVersion, error) {
	row, err := s.q(ctx).GetProviderCredentialVersion(ctx, id)
	if err != nil {
		return domain.ProviderCredentialVersion{}, normalizeNotFound(err)
	}
	return providerCredentialVersionFromRow(row), nil
}

func (s *Store) GetProviderCredentialByCreateIdempotencyKey(ctx context.Context, accountID, key string) (domain.ProviderCredential, error) {
	row, err := s.q(ctx).GetProviderCredentialByCreateIdempotencyKey(ctx, postgresdb.GetProviderCredentialByCreateIdempotencyKeyParams{
		AccountID:      accountID,
		IdempotencyKey: key,
	})
	if err != nil {
		return domain.ProviderCredential{}, normalizeNotFound(err)
	}
	return providerCredentialFromRow(row), nil
}

func (s *Store) GetProviderCredentialVersionByRotationIdempotencyKey(ctx context.Context, accountID, key string) (domain.ProviderCredentialVersion, error) {
	row, err := s.q(ctx).GetProviderCredentialVersionByRotationIdempotencyKey(ctx, postgresdb.GetProviderCredentialVersionByRotationIdempotencyKeyParams{
		AccountID:      accountID,
		IdempotencyKey: &key,
	})
	if err != nil {
		return domain.ProviderCredentialVersion{}, normalizeNotFound(err)
	}
	return providerCredentialVersionFromRow(row), nil
}

func (s *Store) GetActiveProviderCredential(ctx context.Context, accountID string, partitionID *string, provider string) (domain.ProviderCredential, error) {
	row, err := s.q(ctx).GetActiveProviderCredential(ctx, postgresdb.GetActiveProviderCredentialParams{
		AccountID:         accountID,
		TenantPartitionID: partitionID,
		Provider:          provider,
	})
	if err != nil {
		return domain.ProviderCredential{}, normalizeNotFound(err)
	}
	return providerCredentialFromRow(row), nil
}

func (s *Store) ListProviderCredentials(ctx context.Context, query ports.ProviderCredentialListQuery) ([]domain.ProviderCredential, error) {
	var scope *string
	if query.Scope != nil {
		value := string(*query.Scope)
		scope = &value
	}
	var status *string
	if query.Status != nil {
		value := string(*query.Status)
		status = &value
	}
	rows, err := s.q(ctx).ListProviderCredentials(ctx, postgresdb.ListProviderCredentialsParams{
		AccountID:         query.AccountID,
		TenantPartitionID: query.TenantPartitionID,
		Provider:          query.Provider,
		Scope:             scope,
		Status:            status,
		BatchLimit:        boundedBatchLimit(query.Limit),
	})
	if err != nil {
		return nil, err
	}
	credentials := make([]domain.ProviderCredential, len(rows))
	for index, row := range rows {
		credentials[index] = providerCredentialFromRow(row)
	}
	return credentials, nil
}

func (s *Store) ActivateProviderCredentialVersion(
	ctx context.Context,
	credentialID string,
	versionID string,
	version int,
	overlapExpiresAt *time.Time,
	observedAt time.Time,
) (domain.ProviderCredential, error) {
	row, err := s.q(ctx).ActivateProviderCredentialVersion(ctx, postgresdb.ActivateProviderCredentialVersionParams{
		CurrentVersionID: versionID,
		CurrentVersion:   int32(version),
		ObservedAt:       observedAt,
		CredentialID:     credentialID,
		OverlapExpiresAt: overlapExpiresAt,
	})
	if err != nil {
		return domain.ProviderCredential{}, normalizeNotFound(err)
	}
	return providerCredentialFromRow(row), nil
}

func (s *Store) RevokeProviderCredential(ctx context.Context, id string, observedAt time.Time) (domain.ProviderCredential, error) {
	row, err := s.q(ctx).RevokeProviderCredential(ctx, postgresdb.RevokeProviderCredentialParams{
		ObservedAt:   &observedAt,
		CredentialID: id,
	})
	if err != nil {
		return domain.ProviderCredential{}, normalizeNotFound(err)
	}
	return providerCredentialFromRow(row), nil
}

func (s *Store) CreateInvocationProviderCredential(ctx context.Context, binding domain.InvocationProviderCredential) error {
	return s.q(ctx).CreateInvocationProviderCredential(ctx, postgresdb.CreateInvocationProviderCredentialParams{
		ID:                   binding.ID,
		InvocationID:         binding.InvocationID,
		AccountID:            binding.AccountID,
		TenantPartitionID:    binding.TenantPartitionID,
		Provider:             binding.Provider,
		Source:               string(binding.Source),
		ProviderCredentialID: binding.ProviderCredentialID,
		CredentialVersionID:  binding.CredentialVersionID,
		Selector:             binding.Selector,
		EncryptionKeyID:      binding.EncryptionKeyID,
		Nonce:                binding.Nonce,
		Ciphertext:           binding.Ciphertext,
		ExpiresAt:            binding.ExpiresAt,
		ClearedAt:            binding.ClearedAt,
		CreatedAt:            binding.CreatedAt,
	})
}

func (s *Store) GetInvocationProviderCredential(ctx context.Context, invocationID, provider string) (domain.InvocationProviderCredential, error) {
	row, err := s.q(ctx).GetInvocationProviderCredential(ctx, postgresdb.GetInvocationProviderCredentialParams{
		InvocationID: invocationID,
		Provider:     provider,
	})
	if err != nil {
		return domain.InvocationProviderCredential{}, normalizeNotFound(err)
	}
	return invocationProviderCredentialFromRow(row), nil
}

func (s *Store) ClearExpiredProviderCredentialMaterial(ctx context.Context, observedAt time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	bindings, err := s.q(ctx).ClearExpiredInvocationCredentialMaterial(ctx, postgresdb.ClearExpiredInvocationCredentialMaterialParams{
		ObservedAt: &observedAt,
		BatchLimit: boundedBatchLimit(limit),
	})
	if err != nil {
		return 0, err
	}
	versions, err := s.q(ctx).ExpireProviderCredentialVersions(ctx, postgresdb.ExpireProviderCredentialVersionsParams{
		ObservedAt: &observedAt,
		BatchLimit: boundedBatchLimit(limit),
	})
	return bindings + versions, err
}

func providerCredentialFromRow(row postgresdb.ProviderCredential) domain.ProviderCredential {
	return domain.ProviderCredential{
		ID:                   row.ID,
		AccountID:            row.AccountID,
		TenantPartitionID:    row.TenantPartitionID,
		Provider:             row.Provider,
		Scope:                domain.ProviderCredentialScope(row.Scope),
		Status:               domain.ProviderCredentialStatus(row.Status),
		CurrentVersionID:     row.CurrentVersionID,
		CurrentVersion:       int(row.CurrentVersion),
		CreateIdempotencyKey: row.CreateIdempotencyKey,
		CreateFingerprint:    row.CreateFingerprint,
		CreatedBy:            row.CreatedBy,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		RevokedAt:            row.RevokedAt,
	}
}

func providerCredentialVersionFromRow(row postgresdb.ProviderCredentialVersion) domain.ProviderCredentialVersion {
	return domain.ProviderCredentialVersion{
		ID:                     row.ID,
		ProviderCredentialID:   row.ProviderCredentialID,
		AccountID:              row.AccountID,
		TenantPartitionID:      row.TenantPartitionID,
		Provider:               row.Provider,
		Version:                int(row.Version),
		Status:                 domain.ProviderCredentialVersionStatus(row.Status),
		PreviousVersionID:      row.PreviousVersionID,
		EncryptionKeyID:        row.EncryptionKeyID,
		Nonce:                  row.Nonce,
		Ciphertext:             row.Ciphertext,
		ExpiresAt:              row.ExpiresAt,
		OverlapExpiresAt:       row.OverlapExpiresAt,
		RotationIdempotencyKey: row.RotationIdempotencyKey,
		RotationFingerprint:    row.RotationFingerprint,
		CreatedBy:              row.CreatedBy,
		CreatedAt:              row.CreatedAt,
		DestroyedAt:            row.DestroyedAt,
	}
}

func invocationProviderCredentialFromRow(row postgresdb.InvocationProviderCredential) domain.InvocationProviderCredential {
	return domain.InvocationProviderCredential{
		ID:                   row.ID,
		InvocationID:         row.InvocationID,
		AccountID:            row.AccountID,
		TenantPartitionID:    row.TenantPartitionID,
		Provider:             row.Provider,
		Source:               domain.ProviderCredentialSource(row.Source),
		ProviderCredentialID: row.ProviderCredentialID,
		CredentialVersionID:  row.CredentialVersionID,
		Selector:             row.Selector,
		EncryptionKeyID:      row.EncryptionKeyID,
		Nonce:                row.Nonce,
		Ciphertext:           row.Ciphertext,
		ExpiresAt:            row.ExpiresAt,
		ClearedAt:            row.ClearedAt,
		CreatedAt:            row.CreatedAt,
	}
}
