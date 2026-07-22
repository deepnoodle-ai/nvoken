package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type CredentialResolverConfig struct {
	DeploymentMode      CredentialDeploymentMode
	InstallationAPIKeys map[string]string
	PlatformAPIKeys     map[string]string
}

type ProviderCredentialResolver struct {
	store       ports.ProviderCredentialRepository
	cipher      ports.CredentialCipher
	clock       ports.Clock
	config      CredentialResolverConfig
	fundingGate ports.PlatformFundingGate
}

func NewProviderCredentialResolver(
	store ports.ProviderCredentialRepository,
	cipher ports.CredentialCipher,
	clock ports.Clock,
	config CredentialResolverConfig,
	fundingGate ports.PlatformFundingGate,
) *ProviderCredentialResolver {
	config.InstallationAPIKeys = cloneProviderKeys(config.InstallationAPIKeys)
	config.PlatformAPIKeys = cloneProviderKeys(config.PlatformAPIKeys)
	return &ProviderCredentialResolver{
		store:       store,
		cipher:      cipher,
		clock:       clock,
		config:      config,
		fundingGate: fundingGate,
	}
}

func (r *ProviderCredentialResolver) ResolveProviderCredential(
	ctx context.Context,
	invocationID string,
	providerInput string,
) (domain.ResolvedProviderCredential, error) {
	provider, ok := CanonicalModelProvider(providerInput)
	if !ok || r == nil || r.store == nil || r.clock == nil {
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
	binding, err := r.store.GetInvocationProviderCredential(ctx, invocationID, provider)
	if err != nil {
		return domain.ResolvedProviderCredential{}, credentialStoreError("load Invocation provider credential", err)
	}
	if binding.InvocationID != invocationID || binding.Provider != provider || !binding.Source.Valid() {
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
	resolved := domain.ResolvedProviderCredential{
		Provider: provider,
		Source:   binding.Source,
	}
	now := r.clock.Now().UTC()
	switch binding.Source {
	case domain.ProviderCredentialSourceCallerEphemeral:
		if r.cipher == nil || binding.EncryptionKeyID == nil || binding.ExpiresAt == nil ||
			!binding.ExpiresAt.After(now) || len(binding.Nonce) == 0 || len(binding.Ciphertext) == 0 {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
		plaintext, err := r.cipher.Decrypt(domain.EncryptedCredential{
			KeyID:      *binding.EncryptionKeyID,
			Nonce:      binding.Nonce,
			Ciphertext: binding.Ciphertext,
		}, invocationProviderCredentialAAD(binding.InvocationID, binding.Provider, binding.ID))
		if err != nil || len(plaintext) == 0 {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
		resolved.APIKey = string(plaintext)
		return resolved, nil
	case domain.ProviderCredentialSourceAccountBYOK,
		domain.ProviderCredentialSourceTenantBYOK:
		return r.resolveReusable(ctx, binding, resolved, now)
	case domain.ProviderCredentialSourcePlatform:
		if r.config.DeploymentMode != CredentialDeploymentCloud || r.fundingGate == nil {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
		if err := r.fundingGate.AuthorizePlatformModelCall(
			ctx,
			binding.AccountID,
			binding.TenantPartitionID,
			binding.InvocationID,
			binding.Provider,
		); err != nil {
			return domain.ResolvedProviderCredential{}, platformFundingError(err)
		}
		resolved.APIKey = r.config.PlatformAPIKeys[provider]
		if resolved.APIKey == "" {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
		return resolved, nil
	case domain.ProviderCredentialSourceInstallationBYOK:
		if r.config.DeploymentMode != CredentialDeploymentSelfHosted {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
		resolved.APIKey = r.config.InstallationAPIKeys[provider]
		if resolved.APIKey == "" {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
		return resolved, nil
	default:
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
}

func (r *ProviderCredentialResolver) resolveReusable(
	ctx context.Context,
	binding domain.InvocationProviderCredential,
	resolved domain.ResolvedProviderCredential,
	now time.Time,
) (domain.ResolvedProviderCredential, error) {
	if r.cipher == nil || binding.ProviderCredentialID == nil || binding.CredentialVersionID == nil {
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
	credential, err := r.store.GetProviderCredential(ctx, *binding.ProviderCredentialID)
	if err != nil {
		return domain.ResolvedProviderCredential{}, credentialStoreError("load provider credential", err)
	}
	version, err := r.store.GetProviderCredentialVersion(ctx, *binding.CredentialVersionID)
	if err != nil {
		return domain.ResolvedProviderCredential{}, credentialStoreError("load provider credential version", err)
	}
	if credential.Status != domain.ProviderCredentialActive ||
		credential.AccountID != binding.AccountID || credential.Provider != binding.Provider ||
		version.ProviderCredentialID != credential.ID || version.AccountID != binding.AccountID ||
		version.Provider != binding.Provider || !sameOptionalString(version.TenantPartitionID, credential.TenantPartitionID) ||
		version.EncryptionKeyID == nil ||
		len(version.Nonce) == 0 || len(version.Ciphertext) == 0 ||
		(version.ExpiresAt != nil && !version.ExpiresAt.After(now)) {
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
	switch version.Status {
	case domain.ProviderCredentialVersionActive:
		if credential.CurrentVersionID != version.ID {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
	case domain.ProviderCredentialVersionOverlap:
		if version.OverlapExpiresAt == nil || !version.OverlapExpiresAt.After(now) {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
	default:
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
	if binding.Source == domain.ProviderCredentialSourceAccountBYOK {
		if credential.Scope != domain.ProviderCredentialScopeAccount || credential.TenantPartitionID != nil {
			return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
		}
	} else if credential.Scope != domain.ProviderCredentialScopeTenant || credential.TenantPartitionID == nil ||
		*credential.TenantPartitionID != binding.TenantPartitionID {
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
	plaintext, err := r.cipher.Decrypt(domain.EncryptedCredential{
		KeyID:      *version.EncryptionKeyID,
		Nonce:      version.Nonce,
		Ciphertext: version.Ciphertext,
	}, providerCredentialVersionAAD(
		credential.AccountID,
		credential.TenantPartitionID,
		credential.Provider,
		credential.ID,
		version.ID,
	))
	if err != nil || len(plaintext) == 0 {
		return domain.ResolvedProviderCredential{}, ports.ErrCredentialUnavailable
	}
	resolved.ProviderCredentialID = credential.ID
	resolved.CredentialVersionID = version.ID
	resolved.APIKey = string(plaintext)
	return resolved, nil
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneProviderKeys(input map[string]string) map[string]string {
	cloned := make(map[string]string, len(input))
	for provider, key := range input {
		canonical, ok := CanonicalModelProvider(provider)
		if ok && strings.TrimSpace(key) != "" {
			cloned[canonical] = key
		}
	}
	return cloned
}

func credentialStoreError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ports.ErrRetryable) {
		return err
	}
	if errors.Is(err, ports.ErrNotFound) || errors.Is(err, ports.ErrCredentialUnavailable) {
		return ports.ErrCredentialUnavailable
	}
	return fmt.Errorf("%w: %s: %w", ports.ErrRetryable, operation, err)
}

func platformFundingError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ports.ErrPlatformFundingDenied) || errors.Is(err, ports.ErrCredentialUnavailable) {
		return ports.ErrCredentialUnavailable
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ports.ErrRetryable) {
		return err
	}
	return fmt.Errorf("%w: authorize platform funding: %w", ports.ErrRetryable, err)
}

var _ ports.ProviderCredentialResolver = (*ProviderCredentialResolver)(nil)
