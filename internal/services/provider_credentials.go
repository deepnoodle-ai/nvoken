package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	MaxProviderCredentialBodyBytes = 1 << 20
	MaxProviderAPIKeyBytes         = 64 << 10
	MaxCredentialRotationOverlap   = time.Hour
)

type ProviderStaticCredentialInput struct {
	APIKey string `json:"api_key"`
}

type CreateProviderCredentialInput struct {
	Provider       string                         `json:"provider"`
	Scope          domain.ProviderCredentialScope `json:"scope"`
	TenantRef      *string                        `json:"tenant_ref,omitempty"`
	Credential     ProviderStaticCredentialInput  `json:"credential"`
	ExpiresAt      *time.Time                     `json:"expires_at,omitempty"`
	IdempotencyKey string                         `json:"idempotency_key"`
}

type RotateProviderCredentialInput struct {
	Credential     ProviderStaticCredentialInput `json:"credential"`
	ExpiresAt      *time.Time                    `json:"expires_at,omitempty"`
	OverlapSeconds int64                         `json:"overlap_seconds,omitempty"`
	IdempotencyKey string                        `json:"idempotency_key"`
}

type ProviderCredentialListInput struct {
	Provider  *string
	Scope     *domain.ProviderCredentialScope
	Status    *domain.ProviderCredentialStatus
	TenantRef *string
	Limit     int
}

type ProviderCredentialRead struct {
	ID                string                                 `json:"id"`
	Provider          string                                 `json:"provider"`
	Scope             domain.ProviderCredentialScope         `json:"scope"`
	TenantRef         *string                                `json:"tenant_ref"`
	Status            domain.ProviderCredentialStatus        `json:"status"`
	Version           int                                    `json:"version"`
	VersionID         string                                 `json:"version_id"`
	PreviousVersionID *string                                `json:"previous_version_id"`
	VersionStatus     domain.ProviderCredentialVersionStatus `json:"version_status"`
	ExpiresAt         *time.Time                             `json:"expires_at"`
	OverlapExpiresAt  *time.Time                             `json:"overlap_expires_at"`
	CreatedBy         string                                 `json:"created_by"`
	CreatedAt         time.Time                              `json:"created_at"`
	UpdatedAt         time.Time                              `json:"updated_at"`
	RevokedAt         *time.Time                             `json:"revoked_at"`
}

type ProviderCredentialList struct {
	Items []ProviderCredentialRead `json:"items"`
}

type providerCredentialStore interface {
	ports.AccountRepository
	ports.TenantPartitionRepository
	ports.ProviderCredentialRepository
}

type ProviderCredentialService struct {
	store  providerCredentialStore
	txm    ports.TransactionManager
	clock  ports.Clock
	ids    ports.IDGenerator
	cipher ports.CredentialCipher
}

func NewProviderCredentialService(
	store providerCredentialStore,
	txm ports.TransactionManager,
	clock ports.Clock,
	ids ports.IDGenerator,
	cipher ports.CredentialCipher,
) *ProviderCredentialService {
	return &ProviderCredentialService{
		store:  store,
		txm:    txm,
		clock:  clock,
		ids:    ids,
		cipher: cipher,
	}
}

func CanonicalModelProvider(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "anthropic":
		return string(domain.ModelProviderAnthropic), true
	case "openai", "open-ai", "open_ai":
		return string(domain.ModelProviderOpenAI), true
	default:
		return "", false
	}
}

func (s *ProviderCredentialService) Create(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	input CreateProviderCredentialInput,
) (ProviderCredentialRead, error) {
	if err := s.ready(true); err != nil {
		return ProviderCredentialRead{}, err
	}
	if err := authorize(auth, domain.OperationCreateProviderCredential); err != nil {
		return ProviderCredentialRead{}, err
	}
	provider, err := validateCreateProviderCredential(input, s.clock.Now().UTC())
	if err != nil {
		return ProviderCredentialRead{}, err
	}
	if input.Scope == domain.ProviderCredentialScopeAccount &&
		(effectiveAuthProfile(auth) != domain.AuthProfileOperator || auth.TenantConstraint != nil) {
		return ProviderCredentialRead{}, forbidden("Account-scoped provider credentials require unconstrained Operator authority.")
	}
	input.Provider = provider
	fingerprint := createProviderCredentialFingerprint(input)
	for attempt := 0; attempt < 2; attempt++ {
		var result ProviderCredentialRead
		err = s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
			if _, err := s.store.GetAccount(txCtx, auth.AccountID); err != nil {
				return fmt.Errorf("resolve authenticated Account: %w", err)
			}
			existing, err := s.store.GetProviderCredentialByCreateIdempotencyKey(
				txCtx,
				auth.AccountID,
				input.IdempotencyKey,
			)
			if err == nil {
				if !bytes.Equal(existing.CreateFingerprint, fingerprint[:]) {
					return providerCredentialConflict("The idempotency key was already used with a different provider credential request.")
				}
				if err := s.authorizeProviderCredential(txCtx, auth, existing); err != nil {
					return err
				}
				read, err := s.readCredential(txCtx, existing, existing.CurrentVersionID)
				if err != nil {
					return err
				}
				result = read
				return nil
			}
			if !errors.Is(err, ports.ErrNotFound) {
				return err
			}

			partition, err := s.resolveCredentialPartition(txCtx, auth, input.Scope, input.TenantRef)
			if err != nil {
				return err
			}
			partitionID := (*string)(nil)
			if partition != nil {
				partitionID = &partition.ID
			}
			if _, err := s.store.GetActiveProviderCredential(txCtx, auth.AccountID, partitionID, provider); err == nil {
				return providerCredentialConflict("An active credential already exists for this scope and provider.")
			} else if !errors.Is(err, ports.ErrNotFound) {
				return err
			}
			credentialID, err := s.ids.NewID(domain.PrefixProviderCredential)
			if err != nil {
				return err
			}
			versionID, err := s.ids.NewID(domain.PrefixProviderCredentialVersion)
			if err != nil {
				return err
			}
			encrypted, err := s.cipher.Encrypt(
				[]byte(input.Credential.APIKey),
				providerCredentialVersionAAD(auth.AccountID, partitionID, provider, credentialID, versionID),
			)
			if err != nil {
				return &PublicError{
					Code:    CodeUnavailable,
					Message: "Provider credential encryption is unavailable.",
					Cause:   err,
				}
			}
			now := s.clock.Now().UTC()
			actor := providerCredentialActor(auth)
			credential := domain.ProviderCredential{
				ID:                   credentialID,
				AccountID:            auth.AccountID,
				TenantPartitionID:    cloneString(partitionID),
				Provider:             provider,
				Scope:                input.Scope,
				Status:               domain.ProviderCredentialActive,
				CurrentVersionID:     versionID,
				CurrentVersion:       1,
				CreateIdempotencyKey: input.IdempotencyKey,
				CreateFingerprint:    fingerprint[:],
				CreatedBy:            actor,
				CreatedAt:            now,
				UpdatedAt:            now,
			}
			keyID := encrypted.KeyID
			version := domain.ProviderCredentialVersion{
				ID:                   versionID,
				ProviderCredentialID: credentialID,
				AccountID:            auth.AccountID,
				TenantPartitionID:    cloneString(partitionID),
				Provider:             provider,
				Version:              1,
				Status:               domain.ProviderCredentialVersionActive,
				EncryptionKeyID:      &keyID,
				Nonce:                encrypted.Nonce,
				Ciphertext:           encrypted.Ciphertext,
				ExpiresAt:            cloneTime(input.ExpiresAt),
				CreatedBy:            actor,
				CreatedAt:            now,
			}
			if err := s.store.CreateProviderCredential(txCtx, credential); err != nil {
				return err
			}
			if err := s.store.CreateProviderCredentialVersion(txCtx, version); err != nil {
				return err
			}
			result = providerCredentialRead(credential, version, input.TenantRef)
			return nil
		})
		if err == nil {
			return result, nil
		}
		if errors.Is(err, ports.ErrProviderCredentialConflict) && attempt == 0 {
			continue
		}
		if errors.Is(err, ports.ErrProviderCredentialConflict) {
			return ProviderCredentialRead{}, providerCredentialConflict("The provider credential request conflicted.")
		}
		return ProviderCredentialRead{}, err
	}
	return ProviderCredentialRead{}, providerCredentialConflict("The provider credential request conflicted.")
}

func (s *ProviderCredentialService) List(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	input ProviderCredentialListInput,
) (ProviderCredentialList, error) {
	if err := s.ready(false); err != nil {
		return ProviderCredentialList{}, err
	}
	if err := authorize(auth, domain.OperationListProviderCredentials); err != nil {
		return ProviderCredentialList{}, err
	}
	query, err := s.providerCredentialListQuery(ctx, auth, input)
	if err != nil {
		return ProviderCredentialList{}, err
	}
	credentials, err := s.store.ListProviderCredentials(ctx, query)
	if err != nil {
		return ProviderCredentialList{}, err
	}
	items := make([]ProviderCredentialRead, 0, len(credentials))
	for _, credential := range credentials {
		if err := s.authorizeProviderCredential(ctx, auth, credential); err != nil {
			continue
		}
		read, err := s.readCredential(ctx, credential, credential.CurrentVersionID)
		if err != nil {
			return ProviderCredentialList{}, err
		}
		items = append(items, read)
	}
	return ProviderCredentialList{Items: items}, nil
}

func (s *ProviderCredentialService) Get(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	id string,
) (ProviderCredentialRead, error) {
	if err := s.ready(false); err != nil {
		return ProviderCredentialRead{}, err
	}
	if err := authorize(auth, domain.OperationGetProviderCredential); err != nil {
		return ProviderCredentialRead{}, err
	}
	if !domain.ValidStableID(id, domain.PrefixProviderCredential) {
		return ProviderCredentialRead{}, invalidRequest("provider_credential_id is invalid.")
	}
	credential, err := s.store.GetProviderCredential(ctx, id)
	if errors.Is(err, ports.ErrNotFound) || (err == nil && credential.AccountID != auth.AccountID) {
		return ProviderCredentialRead{}, notFound()
	}
	if err != nil {
		return ProviderCredentialRead{}, err
	}
	if err := s.authorizeProviderCredential(ctx, auth, credential); err != nil {
		return ProviderCredentialRead{}, err
	}
	return s.readCredential(ctx, credential, credential.CurrentVersionID)
}

func (s *ProviderCredentialService) Rotate(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	id string,
	input RotateProviderCredentialInput,
) (ProviderCredentialRead, error) {
	if err := s.ready(true); err != nil {
		return ProviderCredentialRead{}, err
	}
	if err := authorize(auth, domain.OperationRotateProviderCredential); err != nil {
		return ProviderCredentialRead{}, err
	}
	if !domain.ValidStableID(id, domain.PrefixProviderCredential) {
		return ProviderCredentialRead{}, invalidRequest("provider_credential_id is invalid.")
	}
	if err := validateRotateProviderCredential(input, s.clock.Now().UTC()); err != nil {
		return ProviderCredentialRead{}, err
	}
	fingerprint := rotateProviderCredentialFingerprint(id, input)
	for attempt := 0; attempt < 2; attempt++ {
		var result ProviderCredentialRead
		err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
			credential, err := s.store.GetProviderCredentialForUpdate(txCtx, id)
			if errors.Is(err, ports.ErrNotFound) || (err == nil && credential.AccountID != auth.AccountID) {
				return notFound()
			}
			if err != nil {
				return err
			}
			if err := s.authorizeProviderCredential(txCtx, auth, credential); err != nil {
				return err
			}
			existing, err := s.store.GetProviderCredentialVersionByRotationIdempotencyKey(
				txCtx,
				auth.AccountID,
				input.IdempotencyKey,
			)
			if err == nil {
				if existing.ProviderCredentialID != id || !bytes.Equal(existing.RotationFingerprint, fingerprint[:]) {
					return providerCredentialConflict("The idempotency key was already used with a different provider credential rotation.")
				}
				read, err := s.readCredential(txCtx, credential, existing.ID)
				if err != nil {
					return err
				}
				result = read
				return nil
			}
			if !errors.Is(err, ports.ErrNotFound) {
				return err
			}
			if credential.Status != domain.ProviderCredentialActive {
				return providerCredentialConflict("The provider credential is revoked.")
			}
			versionID, err := s.ids.NewID(domain.PrefixProviderCredentialVersion)
			if err != nil {
				return err
			}
			encrypted, err := s.cipher.Encrypt(
				[]byte(input.Credential.APIKey),
				providerCredentialVersionAAD(
					auth.AccountID,
					credential.TenantPartitionID,
					credential.Provider,
					credential.ID,
					versionID,
				),
			)
			if err != nil {
				return &PublicError{
					Code:    CodeUnavailable,
					Message: "Provider credential encryption is unavailable.",
					Cause:   err,
				}
			}
			now := s.clock.Now().UTC()
			keyID := encrypted.KeyID
			rotationKey := input.IdempotencyKey
			previousID := credential.CurrentVersionID
			version := domain.ProviderCredentialVersion{
				ID:                     versionID,
				ProviderCredentialID:   credential.ID,
				AccountID:              credential.AccountID,
				TenantPartitionID:      cloneString(credential.TenantPartitionID),
				Provider:               credential.Provider,
				Version:                credential.CurrentVersion + 1,
				Status:                 domain.ProviderCredentialVersionActive,
				PreviousVersionID:      &previousID,
				EncryptionKeyID:        &keyID,
				Nonce:                  encrypted.Nonce,
				Ciphertext:             encrypted.Ciphertext,
				ExpiresAt:              cloneTime(input.ExpiresAt),
				RotationIdempotencyKey: &rotationKey,
				RotationFingerprint:    fingerprint[:],
				CreatedBy:              providerCredentialActor(auth),
				CreatedAt:              now,
			}
			if err := s.store.CreateProviderCredentialVersion(txCtx, version); err != nil {
				return err
			}
			var overlapExpiresAt *time.Time
			if input.OverlapSeconds > 0 {
				value := now.Add(time.Duration(input.OverlapSeconds) * time.Second)
				overlapExpiresAt = &value
			}
			updated, err := s.store.ActivateProviderCredentialVersion(
				txCtx,
				credential.ID,
				version.ID,
				version.Version,
				overlapExpiresAt,
				now,
			)
			if err != nil {
				return err
			}
			tenantRef, err := s.tenantRefForCredential(txCtx, updated)
			if err != nil {
				return err
			}
			result = providerCredentialRead(updated, version, tenantRef)
			return nil
		})
		if err == nil {
			return result, nil
		}
		if errors.Is(err, ports.ErrProviderCredentialConflict) && attempt == 0 {
			continue
		}
		if errors.Is(err, ports.ErrProviderCredentialConflict) {
			return ProviderCredentialRead{}, providerCredentialConflict("The provider credential rotation conflicted.")
		}
		return ProviderCredentialRead{}, err
	}
	return ProviderCredentialRead{}, providerCredentialConflict("The provider credential rotation conflicted.")
}

func (s *ProviderCredentialService) Revoke(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	id string,
) (ProviderCredentialRead, error) {
	if err := s.ready(false); err != nil {
		return ProviderCredentialRead{}, err
	}
	if err := authorize(auth, domain.OperationRevokeProviderCredential); err != nil {
		return ProviderCredentialRead{}, err
	}
	if !domain.ValidStableID(id, domain.PrefixProviderCredential) {
		return ProviderCredentialRead{}, invalidRequest("provider_credential_id is invalid.")
	}
	var result ProviderCredentialRead
	err := s.txm.WithTransaction(ctx, func(txCtx context.Context) error {
		credential, err := s.store.GetProviderCredentialForUpdate(txCtx, id)
		if errors.Is(err, ports.ErrNotFound) || (err == nil && credential.AccountID != auth.AccountID) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if err := s.authorizeProviderCredential(txCtx, auth, credential); err != nil {
			return err
		}
		if credential.Status == domain.ProviderCredentialActive {
			credential, err = s.store.RevokeProviderCredential(txCtx, id, s.clock.Now().UTC())
			if err != nil {
				return err
			}
		}
		read, err := s.readCredential(txCtx, credential, credential.CurrentVersionID)
		if err != nil {
			return err
		}
		result = read
		return nil
	})
	return result, err
}

func validateCreateProviderCredential(input CreateProviderCredentialInput, now time.Time) (string, error) {
	provider, ok := CanonicalModelProvider(input.Provider)
	if !ok {
		return "", invalidRequest("provider is not supported.")
	}
	if input.Scope != domain.ProviderCredentialScopeAccount && input.Scope != domain.ProviderCredentialScopeTenant {
		return "", invalidRequest("scope must be account or tenant.")
	}
	if input.Scope == domain.ProviderCredentialScopeAccount && input.TenantRef != nil {
		return "", invalidRequest("tenant_ref is valid only for tenant scope.")
	}
	if input.Scope == domain.ProviderCredentialScopeTenant {
		if input.TenantRef == nil {
			return "", invalidRequest("tenant_ref is required for tenant scope.")
		}
		if err := validateBoundedString("tenant_ref", *input.TenantRef, MaxReferenceCharacters); err != nil {
			return "", err
		}
	}
	if err := validateProviderAPIKey(input.Credential.APIKey); err != nil {
		return "", err
	}
	if err := validateBoundedString("idempotency_key", input.IdempotencyKey, MaxReferenceCharacters); err != nil {
		return "", err
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return "", invalidRequest("expires_at must be in the future.")
	}
	return provider, nil
}

func validateRotateProviderCredential(input RotateProviderCredentialInput, now time.Time) error {
	if err := validateProviderAPIKey(input.Credential.APIKey); err != nil {
		return err
	}
	if err := validateBoundedString("idempotency_key", input.IdempotencyKey, MaxReferenceCharacters); err != nil {
		return err
	}
	if input.ExpiresAt != nil && !input.ExpiresAt.After(now) {
		return invalidRequest("expires_at must be in the future.")
	}
	if input.OverlapSeconds < 0 || input.OverlapSeconds > int64(MaxCredentialRotationOverlap/time.Second) {
		return invalidRequest("overlap_seconds must be between 0 and 3600.")
	}
	return nil
}

func validateProviderAPIKey(value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" {
		return invalidRequest("credential.api_key must not be blank.")
	}
	if len(value) > MaxProviderAPIKeyBytes {
		return invalidRequest(fmt.Sprintf("credential.api_key must be at most %d bytes.", MaxProviderAPIKeyBytes))
	}
	return nil
}

func (s *ProviderCredentialService) resolveCredentialPartition(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	scope domain.ProviderCredentialScope,
	tenantRef *string,
) (*domain.TenantPartition, error) {
	if scope == domain.ProviderCredentialScopeAccount {
		if effectiveAuthProfile(auth) != domain.AuthProfileOperator {
			return nil, forbidden("Account-scoped provider credentials require Operator authority.")
		}
		if auth.TenantConstraint != nil {
			return nil, forbidden("A tenant-constrained credential cannot manage Account-scoped provider credentials.")
		}
		return nil, nil
	}
	if auth.TenantConstraint != nil && (tenantRef == nil || *auth.TenantConstraint != *tenantRef) {
		return nil, forbidden("The requested tenant_ref conflicts with the credential constraint.")
	}
	profile := effectiveAuthProfile(auth)
	if profile != domain.AuthProfileOperator && profile != domain.AuthProfileRuntime {
		return nil, forbidden("The authenticated profile cannot manage tenant provider credentials.")
	}
	if profile == domain.AuthProfileRuntime && auth.TenantConstraint == nil {
		return nil, forbidden("A Runtime credential must be constrained to the requested tenant_ref.")
	}
	id, err := s.ids.NewID(domain.PrefixTenantPartition)
	if err != nil {
		return nil, err
	}
	partition, err := s.store.ResolveTenantPartition(ctx, domain.TenantPartition{
		ID:        id,
		AccountID: auth.AccountID,
		TenantRef: cloneString(tenantRef),
		CreatedAt: s.clock.Now().UTC(),
	})
	if err != nil {
		return nil, err
	}
	return &partition, nil
}

func (s *ProviderCredentialService) authorizeProviderCredential(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	credential domain.ProviderCredential,
) error {
	if credential.AccountID != auth.AccountID {
		return notFound()
	}
	if credential.Scope == domain.ProviderCredentialScopeAccount {
		if effectiveAuthProfile(auth) != domain.AuthProfileOperator || auth.TenantConstraint != nil {
			return notFound()
		}
		return nil
	}
	if credential.TenantPartitionID == nil {
		return &PublicError{Code: CodeInternal, Message: "The request could not be completed."}
	}
	profile := effectiveAuthProfile(auth)
	if profile != domain.AuthProfileOperator && profile != domain.AuthProfileRuntime {
		return forbidden("The authenticated profile cannot manage tenant provider credentials.")
	}
	if profile == domain.AuthProfileRuntime && auth.TenantConstraint == nil {
		return forbidden("A Runtime credential must be constrained to one tenant.")
	}
	if auth.TenantConstraint != nil {
		partition, err := s.store.GetTenantPartition(ctx, *credential.TenantPartitionID)
		if errors.Is(err, ports.ErrNotFound) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if partition.AccountID != auth.AccountID || partition.TenantRef == nil || *partition.TenantRef != *auth.TenantConstraint {
			return notFound()
		}
	}
	return nil
}

func (s *ProviderCredentialService) providerCredentialListQuery(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	input ProviderCredentialListInput,
) (ports.ProviderCredentialListQuery, error) {
	limit := input.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 100 {
		return ports.ProviderCredentialListQuery{}, invalidRequest("limit must be between 1 and 100.")
	}
	var provider *string
	if input.Provider != nil {
		canonical, ok := CanonicalModelProvider(*input.Provider)
		if !ok {
			return ports.ProviderCredentialListQuery{}, invalidRequest("provider is not supported.")
		}
		provider = &canonical
	}
	var partitionID *string
	effectiveTenant := input.TenantRef
	if auth.TenantConstraint != nil {
		if input.TenantRef != nil && *input.TenantRef != *auth.TenantConstraint {
			return ports.ProviderCredentialListQuery{}, forbidden("The requested tenant_ref conflicts with the credential constraint.")
		}
		effectiveTenant = auth.TenantConstraint
	}
	profile := effectiveAuthProfile(auth)
	if profile != domain.AuthProfileOperator && profile != domain.AuthProfileRuntime {
		return ports.ProviderCredentialListQuery{}, forbidden("The authenticated profile cannot manage provider credentials.")
	}
	if profile == domain.AuthProfileRuntime && auth.TenantConstraint == nil {
		return ports.ProviderCredentialListQuery{}, forbidden("A Runtime credential must be constrained to one tenant.")
	}
	if effectiveTenant != nil {
		partition, err := s.store.GetTenantPartitionByRef(ctx, auth.AccountID, *effectiveTenant)
		if errors.Is(err, ports.ErrNotFound) {
			missingPartition := "__missing_tenant_partition__"
			return ports.ProviderCredentialListQuery{
				AccountID:         auth.AccountID,
				TenantPartitionID: &missingPartition,
				Provider:          provider,
				Scope:             input.Scope,
				Status:            input.Status,
				Limit:             limit,
			}, nil
		}
		if err != nil {
			return ports.ProviderCredentialListQuery{}, err
		}
		partitionID = &partition.ID
	}
	return ports.ProviderCredentialListQuery{
		AccountID:         auth.AccountID,
		TenantPartitionID: partitionID,
		Provider:          provider,
		Scope:             input.Scope,
		Status:            input.Status,
		Limit:             limit,
	}, nil
}

func (s *ProviderCredentialService) readCredential(
	ctx context.Context,
	credential domain.ProviderCredential,
	versionID string,
) (ProviderCredentialRead, error) {
	version, err := s.store.GetProviderCredentialVersion(ctx, versionID)
	if err != nil {
		return ProviderCredentialRead{}, err
	}
	if version.ProviderCredentialID != credential.ID || version.AccountID != credential.AccountID ||
		version.Provider != credential.Provider || !sameOptionalString(version.TenantPartitionID, credential.TenantPartitionID) {
		return ProviderCredentialRead{}, fmt.Errorf("provider credential version scope mismatch")
	}
	tenantRef, err := s.tenantRefForCredential(ctx, credential)
	if err != nil {
		return ProviderCredentialRead{}, err
	}
	return providerCredentialRead(credential, version, tenantRef), nil
}

func (s *ProviderCredentialService) tenantRefForCredential(
	ctx context.Context,
	credential domain.ProviderCredential,
) (*string, error) {
	if credential.TenantPartitionID == nil {
		return nil, nil
	}
	partition, err := s.store.GetTenantPartition(ctx, *credential.TenantPartitionID)
	if err != nil {
		return nil, err
	}
	if partition.AccountID != credential.AccountID || partition.TenantRef == nil {
		return nil, fmt.Errorf("provider credential tenant partition is invalid")
	}
	return cloneString(partition.TenantRef), nil
}

func providerCredentialRead(
	credential domain.ProviderCredential,
	version domain.ProviderCredentialVersion,
	tenantRef *string,
) ProviderCredentialRead {
	return ProviderCredentialRead{
		ID:                credential.ID,
		Provider:          credential.Provider,
		Scope:             credential.Scope,
		TenantRef:         cloneString(tenantRef),
		Status:            credential.Status,
		Version:           version.Version,
		VersionID:         version.ID,
		PreviousVersionID: cloneString(version.PreviousVersionID),
		VersionStatus:     version.Status,
		ExpiresAt:         cloneTime(version.ExpiresAt),
		OverlapExpiresAt:  cloneTime(version.OverlapExpiresAt),
		CreatedBy:         credential.CreatedBy,
		CreatedAt:         credential.CreatedAt,
		UpdatedAt:         credential.UpdatedAt,
		RevokedAt:         cloneTime(credential.RevokedAt),
	}
}

func createProviderCredentialFingerprint(input CreateProviderCredentialInput) [sha256.Size]byte {
	value := input.Provider + "\x00" + string(input.Scope) + "\x00" + stringValue(input.TenantRef) + "\x00" + timeValue(input.ExpiresAt)
	return sha256.Sum256([]byte(value))
}

func rotateProviderCredentialFingerprint(id string, input RotateProviderCredentialInput) [sha256.Size]byte {
	value := id + "\x00" + timeValue(input.ExpiresAt) + "\x00" + strconv.FormatInt(input.OverlapSeconds, 10)
	return sha256.Sum256([]byte(value))
}

func providerCredentialVersionAAD(accountID string, partitionID *string, provider, credentialID, versionID string) []byte {
	payload, _ := json.Marshal([]string{
		"provider_credential_version_v1",
		accountID,
		stringValue(partitionID),
		provider,
		credentialID,
		versionID,
	})
	return payload
}

func providerCredentialActor(auth domain.RuntimeAuthContext) string {
	if strings.TrimSpace(auth.ActorID) != "" {
		return auth.ActorID
	}
	return "profile:" + string(effectiveAuthProfile(auth))
}

func effectiveAuthProfile(auth domain.RuntimeAuthContext) domain.AuthProfile {
	if auth.Profile == "" {
		return domain.AuthProfileRuntime
	}
	return auth.Profile
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timeValue(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func (s *ProviderCredentialService) ready(requireCipher bool) error {
	if s == nil || s.store == nil || s.txm == nil || s.clock == nil || s.ids == nil || (requireCipher && s.cipher == nil) {
		return &PublicError{
			Code:    CodeUnavailable,
			Message: "The service is temporarily unavailable.",
		}
	}
	return nil
}

func providerCredentialConflict(message string) error {
	return &PublicError{Code: CodeProviderCredentialConflict, Message: message}
}
