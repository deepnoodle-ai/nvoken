package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/secretcrypto"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type resolverCredentialStore struct {
	ports.ProviderCredentialRepository
	binding    domain.InvocationProviderCredential
	credential domain.ProviderCredential
	version    domain.ProviderCredentialVersion
}

func (s *resolverCredentialStore) GetInvocationProviderCredential(
	_ context.Context,
	invocationID string,
	provider string,
) (domain.InvocationProviderCredential, error) {
	if s.binding.InvocationID != invocationID || s.binding.Provider != provider {
		return domain.InvocationProviderCredential{}, ports.ErrNotFound
	}
	return s.binding, nil
}

func (s *resolverCredentialStore) GetProviderCredential(_ context.Context, id string) (domain.ProviderCredential, error) {
	if s.credential.ID != id {
		return domain.ProviderCredential{}, ports.ErrNotFound
	}
	return s.credential, nil
}

func (s *resolverCredentialStore) GetProviderCredentialVersion(_ context.Context, id string) (domain.ProviderCredentialVersion, error) {
	if s.version.ID != id {
		return domain.ProviderCredentialVersion{}, ports.ErrNotFound
	}
	return s.version, nil
}

type credentialResolverClock struct{ now time.Time }

func (c credentialResolverClock) Now() time.Time { return c.now }

type recordingFundingGate struct {
	allowed bool
	calls   int
}

func (g *recordingFundingGate) AuthorizePlatformModelCall(
	context.Context,
	string,
	string,
	string,
	string,
) error {
	g.calls++
	if !g.allowed {
		return errors.New("funding denied")
	}
	return nil
}

func TestProviderCredentialResolverUsesOnlyDurableSelectedSource(t *testing.T) {
	now := time.Date(2026, time.July, 21, 18, 0, 0, 0, time.UTC)
	keyring, err := secretcrypto.NewKeyring("v1", map[string][]byte{
		"v1": []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}

	t.Run("caller ephemeral", func(t *testing.T) {
		binding := domain.InvocationProviderCredential{
			ID:                "binding-caller",
			InvocationID:      "invocation-caller",
			AccountID:         "account",
			TenantPartitionID: "tenant",
			Provider:          "anthropic",
			Source:            domain.ProviderCredentialSourceCallerEphemeral,
			CreatedAt:         now,
		}
		expiresAt := now.Add(time.Hour)
		encrypted, err := keyring.Encrypt(
			[]byte("caller-secret"),
			invocationProviderCredentialAAD(binding.InvocationID, binding.Provider, binding.ID),
		)
		if err != nil {
			t.Fatalf("encrypt caller secret: %v", err)
		}
		binding.EncryptionKeyID = &encrypted.KeyID
		binding.Nonce = encrypted.Nonce
		binding.Ciphertext = encrypted.Ciphertext
		binding.ExpiresAt = &expiresAt
		store := &resolverCredentialStore{binding: binding}
		resolver := NewProviderCredentialResolver(store, keyring, credentialResolverClock{now: now}, CredentialResolverConfig{
			DeploymentMode:      CredentialDeploymentSelfHosted,
			InstallationAPIKeys: map[string]string{"anthropic": "installation-secret"},
		}, nil)
		resolved, err := resolver.ResolveProviderCredential(context.Background(), binding.InvocationID, "ANTHROPIC")
		if err != nil {
			t.Fatalf("resolve caller secret: %v", err)
		}
		if resolved.APIKey != "caller-secret" || resolved.Source != domain.ProviderCredentialSourceCallerEphemeral ||
			resolved.ProviderCredentialID != "" || resolved.CredentialVersionID != "" {
			t.Fatalf("resolved caller credential = %#v", resolved)
		}

		binding.ExpiresAt = &now
		store.binding = binding
		if _, err := resolver.ResolveProviderCredential(context.Background(), binding.InvocationID, binding.Provider); !errors.Is(err, ports.ErrCredentialUnavailable) {
			t.Fatalf("expired caller error = %v", err)
		}
	})

	t.Run("account version and live revocation", func(t *testing.T) {
		credentialID := "credential-account"
		versionID := "version-account"
		binding := domain.InvocationProviderCredential{
			ID:                   "binding-account",
			InvocationID:         "invocation-account",
			AccountID:            "account",
			TenantPartitionID:    "tenant",
			Provider:             "openai",
			Source:               domain.ProviderCredentialSourceAccountBYOK,
			ProviderCredentialID: &credentialID,
			CredentialVersionID:  &versionID,
			CreatedAt:            now,
		}
		credential := domain.ProviderCredential{
			ID:               credentialID,
			AccountID:        binding.AccountID,
			Provider:         binding.Provider,
			Scope:            domain.ProviderCredentialScopeAccount,
			Status:           domain.ProviderCredentialActive,
			CurrentVersionID: versionID,
			CurrentVersion:   1,
		}
		encrypted, err := keyring.Encrypt(
			[]byte("account-secret"),
			providerCredentialVersionAAD(binding.AccountID, nil, binding.Provider, credentialID, versionID),
		)
		if err != nil {
			t.Fatalf("encrypt account secret: %v", err)
		}
		version := domain.ProviderCredentialVersion{
			ID:                   versionID,
			ProviderCredentialID: credentialID,
			AccountID:            binding.AccountID,
			Provider:             binding.Provider,
			Version:              1,
			Status:               domain.ProviderCredentialVersionActive,
			EncryptionKeyID:      &encrypted.KeyID,
			Nonce:                encrypted.Nonce,
			Ciphertext:           encrypted.Ciphertext,
		}
		store := &resolverCredentialStore{binding: binding, credential: credential, version: version}
		resolver := NewProviderCredentialResolver(store, keyring, credentialResolverClock{now: now}, CredentialResolverConfig{
			DeploymentMode:      CredentialDeploymentSelfHosted,
			InstallationAPIKeys: map[string]string{"openai": "installation-secret"},
		}, nil)
		resolved, err := resolver.ResolveProviderCredential(context.Background(), binding.InvocationID, binding.Provider)
		if err != nil {
			t.Fatalf("resolve account secret: %v", err)
		}
		if resolved.APIKey != "account-secret" || resolved.ProviderCredentialID != credentialID ||
			resolved.CredentialVersionID != versionID || resolved.Source != domain.ProviderCredentialSourceAccountBYOK {
			t.Fatalf("resolved account credential = %#v", resolved)
		}
		wrongPartition := "wrong-partition"
		store.version.TenantPartitionID = &wrongPartition
		if _, err := resolver.ResolveProviderCredential(context.Background(), binding.InvocationID, binding.Provider); !errors.Is(err, ports.ErrCredentialUnavailable) {
			t.Fatalf("mismatched version partition error = %v", err)
		}
		store.version.TenantPartitionID = nil
		overlapExpiresAt := now.Add(time.Minute)
		store.credential.CurrentVersionID = "new-current-version"
		store.version.Status = domain.ProviderCredentialVersionOverlap
		store.version.OverlapExpiresAt = &overlapExpiresAt
		if _, err := resolver.ResolveProviderCredential(context.Background(), binding.InvocationID, binding.Provider); err != nil {
			t.Fatalf("resolve explicit overlap: %v", err)
		}
		store.version.OverlapExpiresAt = &now
		if _, err := resolver.ResolveProviderCredential(context.Background(), binding.InvocationID, binding.Provider); !errors.Is(err, ports.ErrCredentialUnavailable) {
			t.Fatalf("expired overlap error = %v", err)
		}
		store.version.Status = domain.ProviderCredentialVersionActive
		store.version.OverlapExpiresAt = nil
		store.credential.CurrentVersionID = versionID

		store.credential.Status = domain.ProviderCredentialRevoked
		if _, err := resolver.ResolveProviderCredential(context.Background(), binding.InvocationID, binding.Provider); !errors.Is(err, ports.ErrCredentialUnavailable) {
			t.Fatalf("revoked account error = %v", err)
		}
	})

	t.Run("platform funding gate", func(t *testing.T) {
		store := &resolverCredentialStore{binding: domain.InvocationProviderCredential{
			ID:                "binding-platform",
			InvocationID:      "invocation-platform",
			AccountID:         "account",
			TenantPartitionID: "tenant",
			Provider:          "openai",
			Source:            domain.ProviderCredentialSourcePlatform,
			CreatedAt:         now,
		}}
		gate := &recordingFundingGate{}
		resolver := NewProviderCredentialResolver(store, keyring, credentialResolverClock{now: now}, CredentialResolverConfig{
			DeploymentMode:  CredentialDeploymentCloud,
			PlatformAPIKeys: map[string]string{"openai": "platform-secret"},
		}, gate)
		if _, err := resolver.ResolveProviderCredential(context.Background(), store.binding.InvocationID, store.binding.Provider); !errors.Is(err, ports.ErrCredentialUnavailable) {
			t.Fatalf("funding denial error = %v", err)
		}
		if gate.calls != 1 {
			t.Fatalf("funding calls = %d, want 1", gate.calls)
		}
		gate.allowed = true
		resolved, err := resolver.ResolveProviderCredential(context.Background(), store.binding.InvocationID, store.binding.Provider)
		if err != nil || resolved.APIKey != "platform-secret" || resolved.Source != domain.ProviderCredentialSourcePlatform {
			t.Fatalf("platform resolution = %#v, %v", resolved, err)
		}
	})
}
