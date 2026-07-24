package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

func canonicalCreateInvocation(input CreateInvocationInput) CreateInvocationInput {
	provider, _ := CanonicalModelProvider(input.Spec.Model.Provider)
	input.Spec.Model.Provider = provider
	if input.Spec.MCPServers != nil {
		servers := make([]MCPServerSpec, len(input.Spec.MCPServers))
		for index, server := range input.Spec.MCPServers {
			servers[index] = resolvedMCPServerSpec(server)
		}
		input.Spec.MCPServers = servers
	}
	if input.ProviderCredentials != nil {
		selections := make([]ProviderCredentialSelection, len(input.ProviderCredentials))
		for index, selection := range input.ProviderCredentials {
			selections[index] = selection
			selections[index].Provider, _ = CanonicalModelProvider(selection.Provider)
			if selection.Credential != nil {
				credential := *selection.Credential
				selections[index].Credential = &credential
			}
		}
		input.ProviderCredentials = selections
	}
	return input
}

func validateProviderCredentialPolicy(policy ProviderCredentialPolicy) error {
	if policy.DeploymentMode != CredentialDeploymentSelfHosted && policy.DeploymentMode != CredentialDeploymentCloud {
		return fmt.Errorf("credential deployment mode is invalid")
	}
	if !policy.DefaultSource.Valid() || policy.DefaultSource == domain.ProviderCredentialSourceCallerEphemeral {
		return fmt.Errorf("default provider credential source is invalid")
	}
	if policy.DeploymentMode == CredentialDeploymentSelfHosted && policy.DefaultSource == domain.ProviderCredentialSourcePlatform {
		return fmt.Errorf("platform credentials are unavailable in self-hosted mode")
	}
	if policy.DeploymentMode == CredentialDeploymentCloud && policy.DefaultSource == domain.ProviderCredentialSourceInstallationBYOK {
		return fmt.Errorf("installation credentials are unavailable in Cloud mode")
	}
	return nil
}

func (s *RuntimeService) validateCredentialSelection(input CreateInvocationInput) error {
	if err := validateProviderCredentialPolicy(s.credentialPolicy); err != nil {
		return &PublicError{Code: CodeUnavailable, Message: "The service is temporarily unavailable.", Cause: err}
	}
	source := s.credentialPolicy.DefaultSource
	if input.ProviderCredentials != nil {
		source = input.ProviderCredentials[0].Source
	}
	switch source {
	case domain.ProviderCredentialSourceCallerEphemeral,
		domain.ProviderCredentialSourceAccountBYOK,
		domain.ProviderCredentialSourceTenantBYOK:
		if s.credentialCipher == nil {
			return invalidRequest("The selected provider credential source is not enabled for this installation.")
		}
	case domain.ProviderCredentialSourcePlatform:
		if s.credentialPolicy.DeploymentMode != CredentialDeploymentCloud {
			return invalidRequest("platform provider credentials are available only in nvoken Cloud.")
		}
	case domain.ProviderCredentialSourceInstallationBYOK:
		if input.ProviderCredentials != nil {
			return invalidRequest("installation_byok is selected only by the installation default.")
		}
		if s.credentialPolicy.DeploymentMode != CredentialDeploymentSelfHosted {
			return invalidRequest("installation provider credentials are unavailable in nvoken Cloud.")
		}
	default:
		return invalidRequest("The selected provider credential source is invalid.")
	}
	return nil
}

func (s *RuntimeService) invocationProviderCredentialBinding(
	ctx context.Context,
	invocation domain.Invocation,
	partition domain.TenantPartition,
	input CreateInvocationInput,
	bindingID string,
	now time.Time,
) (domain.InvocationProviderCredential, error) {
	provider := input.Spec.Model.Provider
	source := s.credentialPolicy.DefaultSource
	var supplied *ProviderStaticCredentialInput
	if input.ProviderCredentials != nil {
		selection := input.ProviderCredentials[0]
		source = selection.Source
		supplied = selection.Credential
	}
	binding := domain.InvocationProviderCredential{
		ID:                bindingID,
		InvocationID:      invocation.ID,
		AccountID:         invocation.AccountID,
		TenantPartitionID: invocation.TenantPartitionID,
		Provider:          provider,
		Source:            source,
		CreatedAt:         now,
	}
	switch source {
	case domain.ProviderCredentialSourceCallerEphemeral:
		if supplied == nil || s.credentialCipher == nil {
			return domain.InvocationProviderCredential{}, invalidRequest("caller_ephemeral credential material is required.")
		}
		encrypted, err := s.credentialCipher.Encrypt(
			[]byte(supplied.APIKey),
			invocationProviderCredentialAAD(invocation.ID, provider, bindingID),
		)
		if err != nil {
			return domain.InvocationProviderCredential{}, &PublicError{
				Code:    CodeUnavailable,
				Message: "Provider credential encryption is unavailable.",
				Cause:   err,
			}
		}
		keyID := encrypted.KeyID
		expiresAt := invocation.DeadlineAt.Add(s.credentialCleanupGrace)
		binding.EncryptionKeyID = &keyID
		binding.Nonce = encrypted.Nonce
		binding.Ciphertext = encrypted.Ciphertext
		binding.ExpiresAt = &expiresAt
	case domain.ProviderCredentialSourceAccountBYOK:
		credential, version, err := s.activeProviderCredentialVersion(
			ctx,
			invocation.AccountID,
			nil,
			provider,
			now,
		)
		if err != nil {
			return domain.InvocationProviderCredential{}, err
		}
		binding.ProviderCredentialID = &credential.ID
		binding.CredentialVersionID = &version.ID
	case domain.ProviderCredentialSourceTenantBYOK:
		if partition.TenantKey == nil {
			return domain.InvocationProviderCredential{}, invalidRequest("tenant_byok requires a named tenant_key partition.")
		}
		partitionID := partition.ID
		credential, version, err := s.activeProviderCredentialVersion(
			ctx,
			invocation.AccountID,
			&partitionID,
			provider,
			now,
		)
		if err != nil {
			return domain.InvocationProviderCredential{}, err
		}
		binding.ProviderCredentialID = &credential.ID
		binding.CredentialVersionID = &version.ID
	case domain.ProviderCredentialSourcePlatform:
		selector := "platform:" + provider
		binding.Selector = &selector
	case domain.ProviderCredentialSourceInstallationBYOK:
		selector := "installation:" + provider
		binding.Selector = &selector
	default:
		return domain.InvocationProviderCredential{}, invalidRequest("The selected provider credential source is invalid.")
	}
	return binding, nil
}

func (s *RuntimeService) activeProviderCredentialVersion(
	ctx context.Context,
	accountID string,
	partitionID *string,
	provider string,
	now time.Time,
) (domain.ProviderCredential, domain.ProviderCredentialVersion, error) {
	credential, err := s.store.GetActiveProviderCredential(ctx, accountID, partitionID, provider)
	if errors.Is(err, ports.ErrNotFound) {
		return domain.ProviderCredential{}, domain.ProviderCredentialVersion{}, invalidRequest("The selected provider credential is unavailable.")
	}
	if err != nil {
		return domain.ProviderCredential{}, domain.ProviderCredentialVersion{}, err
	}
	version, err := s.store.GetProviderCredentialVersion(ctx, credential.CurrentVersionID)
	if errors.Is(err, ports.ErrNotFound) {
		return domain.ProviderCredential{}, domain.ProviderCredentialVersion{}, invalidRequest("The selected provider credential is unavailable.")
	}
	if err != nil {
		return domain.ProviderCredential{}, domain.ProviderCredentialVersion{}, err
	}
	if version.ProviderCredentialID != credential.ID || version.Status != domain.ProviderCredentialVersionActive ||
		version.EncryptionKeyID == nil || len(version.Nonce) == 0 || len(version.Ciphertext) == 0 ||
		(version.ExpiresAt != nil && !version.ExpiresAt.After(now)) {
		return domain.ProviderCredential{}, domain.ProviderCredentialVersion{}, invalidRequest("The selected provider credential is unavailable.")
	}
	return credential, version, nil
}

func invocationProviderCredentialAAD(invocationID, provider, bindingID string) []byte {
	payload, _ := json.Marshal([]string{
		"invocation_provider_credential_v1",
		invocationID,
		strings.ToLower(provider),
		bindingID,
	})
	return payload
}
