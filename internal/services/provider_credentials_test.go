package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/secretcrypto"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type credentialLifecycleStore struct {
	providerCredentialStore
	account     domain.Account
	credentials map[string]domain.ProviderCredential
	versions    map[string]domain.ProviderCredentialVersion
}

func newCredentialLifecycleStore(accountID string) *credentialLifecycleStore {
	return &credentialLifecycleStore{
		account: domain.Account{
			ID: accountID,
		},
		credentials: make(map[string]domain.ProviderCredential),
		versions:    make(map[string]domain.ProviderCredentialVersion),
	}
}

func (s *credentialLifecycleStore) GetAccount(_ context.Context, id string) (domain.Account, error) {
	if id != s.account.ID {
		return domain.Account{}, ports.ErrNotFound
	}
	return s.account, nil
}

func (s *credentialLifecycleStore) CreateProviderCredential(_ context.Context, credential domain.ProviderCredential) error {
	if _, exists := s.credentials[credential.ID]; exists {
		return ports.ErrProviderCredentialConflict
	}
	s.credentials[credential.ID] = credential
	return nil
}

func (s *credentialLifecycleStore) CreateProviderCredentialVersion(_ context.Context, version domain.ProviderCredentialVersion) error {
	if _, exists := s.versions[version.ID]; exists {
		return ports.ErrProviderCredentialConflict
	}
	s.versions[version.ID] = version
	return nil
}

func (s *credentialLifecycleStore) GetProviderCredential(_ context.Context, id string) (domain.ProviderCredential, error) {
	credential, ok := s.credentials[id]
	if !ok {
		return domain.ProviderCredential{}, ports.ErrNotFound
	}
	return credential, nil
}

func (s *credentialLifecycleStore) GetProviderCredentialForUpdate(ctx context.Context, id string) (domain.ProviderCredential, error) {
	return s.GetProviderCredential(ctx, id)
}

func (s *credentialLifecycleStore) GetProviderCredentialVersion(_ context.Context, id string) (domain.ProviderCredentialVersion, error) {
	version, ok := s.versions[id]
	if !ok {
		return domain.ProviderCredentialVersion{}, ports.ErrNotFound
	}
	return version, nil
}

func (s *credentialLifecycleStore) GetProviderCredentialByCreateIdempotencyKey(
	_ context.Context,
	accountID string,
	idempotencyKey string,
) (domain.ProviderCredential, error) {
	for _, credential := range s.credentials {
		if credential.AccountID == accountID && credential.CreateIdempotencyKey == idempotencyKey {
			return credential, nil
		}
	}
	return domain.ProviderCredential{}, ports.ErrNotFound
}

func (s *credentialLifecycleStore) GetProviderCredentialVersionByRotationIdempotencyKey(
	_ context.Context,
	accountID string,
	idempotencyKey string,
) (domain.ProviderCredentialVersion, error) {
	for _, version := range s.versions {
		if version.AccountID == accountID && version.RotationIdempotencyKey != nil &&
			*version.RotationIdempotencyKey == idempotencyKey {
			return version, nil
		}
	}
	return domain.ProviderCredentialVersion{}, ports.ErrNotFound
}

func (s *credentialLifecycleStore) GetActiveProviderCredential(
	_ context.Context,
	accountID string,
	partitionID *string,
	provider string,
) (domain.ProviderCredential, error) {
	for _, credential := range s.credentials {
		if credential.AccountID == accountID && credential.Provider == provider &&
			credential.Status == domain.ProviderCredentialActive && equalOptionalString(credential.TenantPartitionID, partitionID) {
			return credential, nil
		}
	}
	return domain.ProviderCredential{}, ports.ErrNotFound
}

func (s *credentialLifecycleStore) ListProviderCredentials(
	_ context.Context,
	query ports.ProviderCredentialListQuery,
) ([]domain.ProviderCredential, error) {
	var result []domain.ProviderCredential
	for _, credential := range s.credentials {
		if credential.AccountID != query.AccountID ||
			(query.TenantPartitionID != nil && !equalOptionalString(credential.TenantPartitionID, query.TenantPartitionID)) ||
			(query.Provider != nil && credential.Provider != *query.Provider) ||
			(query.Scope != nil && credential.Scope != *query.Scope) ||
			(query.Status != nil && credential.Status != *query.Status) {
			continue
		}
		result = append(result, credential)
		if len(result) == query.Limit {
			break
		}
	}
	return result, nil
}

func (s *credentialLifecycleStore) ActivateProviderCredentialVersion(
	_ context.Context,
	credentialID string,
	versionID string,
	version int,
	overlapExpiresAt *time.Time,
	observedAt time.Time,
) (domain.ProviderCredential, error) {
	credential, ok := s.credentials[credentialID]
	if !ok || credential.Status != domain.ProviderCredentialActive {
		return domain.ProviderCredential{}, ports.ErrNotFound
	}
	previous := s.versions[credential.CurrentVersionID]
	if overlapExpiresAt == nil {
		previous.Status = domain.ProviderCredentialVersionRevoked
		previous.EncryptionKeyID = nil
		previous.Nonce = nil
		previous.Ciphertext = nil
		previous.DestroyedAt = &observedAt
	} else {
		previous.Status = domain.ProviderCredentialVersionOverlap
		previous.OverlapExpiresAt = overlapExpiresAt
	}
	s.versions[previous.ID] = previous
	credential.CurrentVersionID = versionID
	credential.CurrentVersion = version
	credential.UpdatedAt = observedAt
	s.credentials[credential.ID] = credential
	return credential, nil
}

func (s *credentialLifecycleStore) RevokeProviderCredential(
	_ context.Context,
	credentialID string,
	observedAt time.Time,
) (domain.ProviderCredential, error) {
	credential, ok := s.credentials[credentialID]
	if !ok || credential.Status != domain.ProviderCredentialActive {
		return domain.ProviderCredential{}, ports.ErrNotFound
	}
	credential.Status = domain.ProviderCredentialRevoked
	credential.RevokedAt = &observedAt
	credential.UpdatedAt = observedAt
	s.credentials[credential.ID] = credential
	for id, version := range s.versions {
		if version.ProviderCredentialID != credentialID ||
			(version.Status != domain.ProviderCredentialVersionActive && version.Status != domain.ProviderCredentialVersionOverlap) {
			continue
		}
		version.Status = domain.ProviderCredentialVersionRevoked
		version.EncryptionKeyID = nil
		version.Nonce = nil
		version.Ciphertext = nil
		version.OverlapExpiresAt = nil
		version.DestroyedAt = &observedAt
		s.versions[id] = version
	}
	return credential, nil
}

func equalOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type credentialLifecycleTx struct{}

func (credentialLifecycleTx) WithTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type credentialLifecycleClock struct{ now time.Time }

func (c credentialLifecycleClock) Now() time.Time { return c.now }

func TestProviderCredentialLifecycleEncryptsSecretsAndReturnsMetadataOnly(t *testing.T) {
	now := time.Date(2026, time.July, 21, 18, 30, 0, 0, time.UTC)
	accountID := "acct_019b0a12-0000-7000-8000-000000000001"
	store := newCredentialLifecycleStore(accountID)
	keyring, err := secretcrypto.NewKeyring("v1", map[string][]byte{
		"v1": []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	clock := credentialLifecycleClock{now: now}
	service := NewProviderCredentialService(
		store,
		credentialLifecycleTx{},
		clock,
		identity.NewUUIDv7Generator(clock),
		keyring,
	)
	auth := domain.RuntimeAuthContext{
		AccountID: accountID,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationCreateProviderCredential: {},
			domain.OperationListProviderCredentials:  {},
			domain.OperationGetProviderCredential:    {},
			domain.OperationRotateProviderCredential: {},
			domain.OperationRevokeProviderCredential: {},
		},
		EffectiveProfile: domain.CredentialProfileOperator,
		CredentialID:     "operator:test",
	}
	created, err := service.Create(context.Background(), auth, CreateProviderCredentialInput{
		Provider: "open-ai",
		Scope:    domain.ProviderCredentialScopeAccount,
		Credential: ProviderStaticCredentialInput{
			APIKey: "create-secret",
		},
		IdempotencyKey: "create-once",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Provider != "openai" || created.Scope != domain.ProviderCredentialScopeAccount || created.Version != 1 {
		t.Fatalf("created metadata = %#v", created)
	}
	storedVersion := store.versions[created.VersionID]
	if bytes.Contains(storedVersion.Ciphertext, []byte("create-secret")) || len(storedVersion.Ciphertext) == 0 {
		t.Fatalf("stored ciphertext = %q", storedVersion.Ciphertext)
	}
	encoded, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("marshal created metadata: %v", err)
	}
	if bytes.Contains(encoded, []byte("create-secret")) || bytes.Contains(encoded, []byte("api_key")) {
		t.Fatalf("metadata disclosed secret shape: %s", encoded)
	}
	tenantRef := "tenant-a"
	runtimeAuth := auth
	runtimeAuth.EffectiveProfile = domain.CredentialProfileRuntime
	runtimeAuth.TenantConstraint = &tenantRef
	for name, attempt := range map[string]func() error{
		"get": func() error {
			_, err := service.Get(context.Background(), runtimeAuth, created.ID)
			return err
		},
		"rotate": func() error {
			_, err := service.Rotate(context.Background(), runtimeAuth, created.ID, RotateProviderCredentialInput{
				Credential:     ProviderStaticCredentialInput{APIKey: "unauthorized-secret"},
				IdempotencyKey: "unauthorized-rotation",
			})
			return err
		},
		"revoke": func() error {
			_, err := service.Revoke(context.Background(), runtimeAuth, created.ID)
			return err
		},
	} {
		t.Run("Runtime Account "+name+" is hidden", func(t *testing.T) {
			var public *PublicError
			if err := attempt(); !errors.As(err, &public) || public.Code != CodeNotFound {
				t.Fatalf("error = %v", err)
			}
		})
	}

	replayed, err := service.Create(context.Background(), auth, CreateProviderCredentialInput{
		Provider: "openai",
		Scope:    domain.ProviderCredentialScopeAccount,
		Credential: ProviderStaticCredentialInput{
			APIKey: "different-replay-secret",
		},
		IdempotencyKey: "create-once",
	})
	if err != nil || replayed.ID != created.ID || len(store.credentials) != 1 || len(store.versions) != 1 {
		t.Fatalf("idempotent create = %#v, %v; roots=%d versions=%d", replayed, err, len(store.credentials), len(store.versions))
	}

	rotated, err := service.Rotate(context.Background(), auth, created.ID, RotateProviderCredentialInput{
		Credential: ProviderStaticCredentialInput{
			APIKey: "rotated-secret",
		},
		IdempotencyKey: "rotate-once",
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if rotated.Version != 2 || rotated.PreviousVersionID == nil || *rotated.PreviousVersionID != created.VersionID {
		t.Fatalf("rotated metadata = %#v", rotated)
	}
	rotationReplay, err := service.Rotate(context.Background(), auth, created.ID, RotateProviderCredentialInput{
		Credential: ProviderStaticCredentialInput{
			APIKey: "different-rotation-replay-secret",
		},
		IdempotencyKey: "rotate-once",
	})
	if err != nil || rotationReplay.VersionID != rotated.VersionID || len(store.versions) != 2 {
		t.Fatalf("idempotent rotation = %#v, %v; versions=%d", rotationReplay, err, len(store.versions))
	}
	if old := store.versions[created.VersionID]; old.Status != domain.ProviderCredentialVersionRevoked || len(old.Ciphertext) != 0 {
		t.Fatalf("old version retained material = %#v", old)
	}

	listed, err := service.List(context.Background(), auth, ProviderCredentialListInput{})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].VersionID != rotated.VersionID {
		t.Fatalf("List = %#v, %v", listed, err)
	}
	viewerAuth := domain.RuntimeAuthContext{
		AccountID: accountID,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationListProviderCredentials: {},
			domain.OperationGetProviderCredential:   {},
		},
		EffectiveProfile: domain.CredentialProfileViewer,
		CredentialID:     "viewer:test",
	}
	viewerList, err := service.List(context.Background(), viewerAuth, ProviderCredentialListInput{})
	if err != nil || len(viewerList.Items) != 1 || viewerList.Items[0].VersionID != rotated.VersionID {
		t.Fatalf("Viewer List = %#v, %v", viewerList, err)
	}
	viewerRead, err := service.Get(context.Background(), viewerAuth, created.ID)
	if err != nil || viewerRead.VersionID != rotated.VersionID {
		t.Fatalf("Viewer Get = %#v, %v", viewerRead, err)
	}
	if _, err := service.Rotate(context.Background(), viewerAuth, created.ID, RotateProviderCredentialInput{
		Credential:     ProviderStaticCredentialInput{APIKey: "viewer-secret"},
		IdempotencyKey: "viewer-rotation",
	}); err == nil {
		t.Fatal("Viewer Rotate succeeded")
	}
	revoked, err := service.Revoke(context.Background(), auth, created.ID)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if revoked.Status != domain.ProviderCredentialRevoked || revoked.RevokedAt == nil {
		t.Fatalf("revoked metadata = %#v", revoked)
	}
	if current := store.versions[rotated.VersionID]; len(current.Ciphertext) != 0 || current.Status != domain.ProviderCredentialVersionRevoked {
		t.Fatalf("revoked version retained material = %#v", current)
	}
}

func TestProviderCredentialAccountScopeRequiresOperator(t *testing.T) {
	accountID := "acct_019b0a12-0000-7000-8000-000000000001"
	clock := credentialLifecycleClock{now: time.Now().UTC()}
	service := NewProviderCredentialService(
		newCredentialLifecycleStore(accountID),
		credentialLifecycleTx{},
		clock,
		identity.NewUUIDv7Generator(clock),
		&unavailableCredentialCipher{},
	)
	auth := domain.RuntimeAuthContext{
		AccountID: accountID,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationCreateProviderCredential: {},
		},
		EffectiveProfile: domain.CredentialProfileRuntime,
	}
	_, err := service.Create(context.Background(), auth, CreateProviderCredentialInput{
		Provider: "openai",
		Scope:    domain.ProviderCredentialScopeAccount,
		Credential: ProviderStaticCredentialInput{
			APIKey: "secret",
		},
		IdempotencyKey: "key",
	})
	var public *PublicError
	if !errors.As(err, &public) || public.Code != CodeForbidden {
		t.Fatalf("Runtime Account create error = %v", err)
	}
	tenantRef := "tenant-a"
	auth.EffectiveProfile = domain.CredentialProfileOperator
	auth.TenantConstraint = &tenantRef
	_, err = service.Create(context.Background(), auth, CreateProviderCredentialInput{
		Provider: "openai",
		Scope:    domain.ProviderCredentialScopeAccount,
		Credential: ProviderStaticCredentialInput{
			APIKey: "secret",
		},
		IdempotencyKey: "operator-key",
	})
	if !errors.As(err, &public) || public.Code != CodeForbidden {
		t.Fatalf("tenant-constrained Operator Account create error = %v", err)
	}
}

type unavailableCredentialCipher struct{}

func (*unavailableCredentialCipher) Encrypt([]byte, []byte) (domain.EncryptedCredential, error) {
	return domain.EncryptedCredential{}, errors.New("unavailable")
}

func (*unavailableCredentialCipher) Decrypt(domain.EncryptedCredential, []byte) ([]byte, error) {
	return nil, errors.New("unavailable")
}
