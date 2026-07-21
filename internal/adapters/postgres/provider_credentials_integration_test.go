package postgres

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/identity"
	"github.com/deepnoodle-ai/nvoken/internal/adapters/secretcrypto"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

func TestProviderCredentialAdmissionResolutionAndCleanupIntegration(t *testing.T) {
	pool, _ := testDatabase(t, true)
	ctx := context.Background()
	store := NewStore(pool)
	txm := NewTransactionManager(pool)
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	account, err := services.BootstrapInstallation(ctx, store, txm, clock, ids)
	if err != nil {
		t.Fatalf("bootstrap installation: %v", err)
	}
	keyring, err := secretcrypto.NewKeyring("v1", map[string][]byte{
		"v1": []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	auth := runtimeAuth(account.ID)
	auth.Profile = domain.AuthProfileOperator
	auth.ActorID = "operator:integration"
	for _, operation := range []domain.RuntimeOperation{
		domain.OperationListProviderCredentials,
		domain.OperationCreateProviderCredential,
		domain.OperationGetProviderCredential,
		domain.OperationRotateProviderCredential,
		domain.OperationRevokeProviderCredential,
	} {
		auth.Operations[operation] = struct{}{}
	}
	runtime := services.NewRuntimeService(
		store,
		txm,
		clock,
		ids,
		services.WithProviderCredentialPolicy(services.ProviderCredentialPolicy{
			DeploymentMode: services.CredentialDeploymentSelfHosted,
			DefaultSource:  domain.ProviderCredentialSourceInstallationBYOK,
		}, keyring, 5*time.Minute),
	)
	omittedInput := runtimeInput()
	omittedInput.AgentRef = "omitted-default"
	omittedInput.SessionKey = pointerString("omitted-default")
	omittedInput.IdempotencyKey = "omitted-default"
	omittedAck, err := runtime.Admit(ctx, auth, omittedInput)
	if err != nil {
		t.Fatalf("admit omitted installation default: %v", err)
	}
	changedDefaultRuntime := services.NewRuntimeService(
		store,
		txm,
		clock,
		ids,
		services.WithProviderCredentialPolicy(services.ProviderCredentialPolicy{
			DeploymentMode: services.CredentialDeploymentSelfHosted,
			DefaultSource:  domain.ProviderCredentialSourceAccountBYOK,
		}, keyring, 5*time.Minute),
	)
	omittedReplay, err := changedDefaultRuntime.Admit(ctx, auth, omittedInput)
	if err != nil || omittedReplay.InvocationID != omittedAck.InvocationID || !omittedReplay.Deduplicated {
		t.Fatalf("omitted replay after default change = %#v, %v", omittedReplay, err)
	}
	omittedBinding, err := store.GetInvocationProviderCredential(ctx, omittedAck.InvocationID, "anthropic")
	if err != nil || omittedBinding.Source != domain.ProviderCredentialSourceInstallationBYOK {
		t.Fatalf("omitted binding after default change = %#v, %v", omittedBinding, err)
	}

	callerInput := runtimeInput()
	callerInput.ProviderCredentials = []services.ProviderCredentialSelection{
		{
			Provider: "anthropic",
			Source:   domain.ProviderCredentialSourceCallerEphemeral,
			Credential: &services.ProviderStaticCredentialInput{
				APIKey: "caller-integration-secret",
			},
		},
	}
	callerAck, err := runtime.Admit(ctx, auth, callerInput)
	if err != nil {
		t.Fatalf("admit caller credential: %v", err)
	}
	callerBinding, err := store.GetInvocationProviderCredential(ctx, callerAck.InvocationID, "anthropic")
	if err != nil {
		t.Fatalf("load caller binding: %v", err)
	}
	if callerBinding.Source != domain.ProviderCredentialSourceCallerEphemeral || len(callerBinding.Ciphertext) == 0 ||
		bytes.Contains(callerBinding.Ciphertext, []byte("caller-integration-secret")) {
		t.Fatalf("caller binding = %#v", callerBinding)
	}
	invocation, err := store.GetInvocation(ctx, callerAck.InvocationID)
	if err != nil {
		t.Fatalf("load caller Invocation: %v", err)
	}
	snapshot, err := store.GetExecutionSpecSnapshot(ctx, invocation.SpecSnapshotID)
	if err != nil {
		t.Fatalf("load caller spec snapshot: %v", err)
	}
	if bytes.Contains(snapshot.Spec, []byte("caller-integration-secret")) {
		t.Fatalf("spec snapshot contains caller secret: %s", snapshot.Spec)
	}

	replayInput := callerInput
	replayInput.ProviderCredentials = append([]services.ProviderCredentialSelection(nil), callerInput.ProviderCredentials...)
	replayCredential := *callerInput.ProviderCredentials[0].Credential
	replayCredential.APIKey = "changed-replay-secret"
	replayInput.ProviderCredentials[0].Credential = &replayCredential
	replay, err := runtime.Admit(ctx, auth, replayInput)
	if err != nil || replay.InvocationID != callerAck.InvocationID || !replay.Deduplicated {
		t.Fatalf("caller idempotent replay = %#v, %v", replay, err)
	}
	replayedBinding, err := store.GetInvocationProviderCredential(ctx, callerAck.InvocationID, "anthropic")
	if err != nil || !bytes.Equal(replayedBinding.Ciphertext, callerBinding.Ciphertext) ||
		!bytes.Equal(replayedBinding.Nonce, callerBinding.Nonce) {
		t.Fatalf("replay replaced caller binding = %#v, %v", replayedBinding, err)
	}

	if _, err := runtime.CancelInvocation(ctx, auth, callerAck.InvocationID); err != nil {
		t.Fatalf("cancel caller Invocation: %v", err)
	}
	cleared, err := store.GetInvocationProviderCredential(ctx, callerAck.InvocationID, "anthropic")
	if err != nil {
		t.Fatalf("load cleared caller binding: %v", err)
	}
	if len(cleared.Ciphertext) != 0 || len(cleared.Nonce) != 0 || cleared.EncryptionKeyID != nil || cleared.ClearedAt == nil ||
		cleared.Source != domain.ProviderCredentialSourceCallerEphemeral {
		t.Fatalf("terminal caller cleanup = %#v", cleared)
	}

	lifecycle := services.NewProviderCredentialService(store, txm, clock, ids, keyring)
	created, err := lifecycle.Create(ctx, auth, services.CreateProviderCredentialInput{
		Provider: "openai",
		Scope:    domain.ProviderCredentialScopeAccount,
		Credential: services.ProviderStaticCredentialInput{
			APIKey: "account-integration-secret",
		},
		IdempotencyKey: "account-openai-create",
	})
	if err != nil {
		t.Fatalf("create Account BYOK: %v", err)
	}
	storedVersion, err := store.GetProviderCredentialVersion(ctx, created.VersionID)
	if err != nil || len(storedVersion.Ciphertext) == 0 ||
		bytes.Contains(storedVersion.Ciphertext, []byte("account-integration-secret")) {
		t.Fatalf("stored Account version = %#v, %v", storedVersion, err)
	}

	accountInput := runtimeInput()
	accountInput.AgentRef = "openai-support"
	accountInput.SessionKey = pointerString("openai-ticket")
	accountInput.IdempotencyKey = "account-openai-invocation"
	accountInput.Spec.Model.Provider = "openai"
	accountInput.ProviderCredentials = []services.ProviderCredentialSelection{
		{
			Provider: "openai",
			Source:   domain.ProviderCredentialSourceAccountBYOK,
		},
	}
	accountAck, err := runtime.Admit(ctx, auth, accountInput)
	if err != nil {
		t.Fatalf("admit Account BYOK Invocation: %v", err)
	}
	resolver := services.NewProviderCredentialResolver(
		store,
		keyring,
		clock,
		services.CredentialResolverConfig{
			DeploymentMode: services.CredentialDeploymentSelfHosted,
			InstallationAPIKeys: map[string]string{
				"openai": "installation-fallback-must-not-run",
			},
		},
		nil,
	)
	resolved, err := resolver.ResolveProviderCredential(ctx, accountAck.InvocationID, "openai")
	if err != nil || resolved.APIKey != "account-integration-secret" ||
		resolved.ProviderCredentialID != created.ID || resolved.CredentialVersionID != created.VersionID {
		t.Fatalf("resolve Account BYOK = %#v, %v", resolved, err)
	}
	if _, err := lifecycle.Revoke(ctx, auth, created.ID); err != nil {
		t.Fatalf("revoke Account BYOK: %v", err)
	}
	if _, err := resolver.ResolveProviderCredential(ctx, accountAck.InvocationID, "openai"); !errors.Is(err, ports.ErrCredentialUnavailable) {
		t.Fatalf("revoked Account BYOK resolution error = %v", err)
	}

	for index, tenant := range []struct {
		ref    string
		secret string
	}{
		{ref: "tenant-a", secret: "tenant-a-secret"},
		{ref: "tenant-b", secret: "tenant-b-secret"},
	} {
		created, err := lifecycle.Create(ctx, auth, services.CreateProviderCredentialInput{
			Provider:  "anthropic",
			Scope:     domain.ProviderCredentialScopeTenant,
			TenantRef: &tenant.ref,
			Credential: services.ProviderStaticCredentialInput{
				APIKey: tenant.secret,
			},
			IdempotencyKey: "tenant-anthropic-create-" + tenant.ref,
		})
		if err != nil {
			t.Fatalf("create %s BYOK: %v", tenant.ref, err)
		}
		input := runtimeInput()
		input.AgentRef = "tenant-support-" + tenant.ref
		input.TenantRef = &tenant.ref
		input.SessionKey = pointerString("tenant-ticket")
		input.IdempotencyKey = "tenant-invocation-" + tenant.ref
		input.ProviderCredentials = []services.ProviderCredentialSelection{
			{
				Provider: "anthropic",
				Source:   domain.ProviderCredentialSourceTenantBYOK,
			},
		}
		ack, err := runtime.Admit(ctx, auth, input)
		if err != nil {
			t.Fatalf("admit %s BYOK Invocation: %v", tenant.ref, err)
		}
		resolved, err := resolver.ResolveProviderCredential(ctx, ack.InvocationID, "anthropic")
		if err != nil || resolved.APIKey != tenant.secret || resolved.ProviderCredentialID != created.ID ||
			resolved.Source != domain.ProviderCredentialSourceTenantBYOK {
			t.Fatalf("resolve %s BYOK at index %d = %#v, %v", tenant.ref, index, resolved, err)
		}
	}
	tenantConstraint := "tenant-a"
	tenantRuntimeAuth := domain.RuntimeAuthContext{
		AccountID:        account.ID,
		TenantConstraint: &tenantConstraint,
		Operations: map[domain.RuntimeOperation]struct{}{
			domain.OperationCreateProviderCredential: {},
			domain.OperationGetProviderCredential:    {},
		},
		Profile: domain.AuthProfileRuntime,
		ActorID: "runtime:tenant-a",
	}
	tenantRuntimeCredential, err := lifecycle.Create(ctx, tenantRuntimeAuth, services.CreateProviderCredentialInput{
		Provider:  "openai",
		Scope:     domain.ProviderCredentialScopeTenant,
		TenantRef: &tenantConstraint,
		Credential: services.ProviderStaticCredentialInput{
			APIKey: "tenant-runtime-secret",
		},
		IdempotencyKey: "tenant-runtime-openai",
	})
	if err != nil {
		t.Fatalf("tenant-constrained Runtime create: %v", err)
	}
	if _, err := lifecycle.Get(ctx, tenantRuntimeAuth, tenantRuntimeCredential.ID); err != nil {
		t.Fatalf("tenant-constrained Runtime get: %v", err)
	}
	otherTenant := "tenant-b"
	_, err = lifecycle.Create(ctx, tenantRuntimeAuth, services.CreateProviderCredentialInput{
		Provider:  "openai",
		Scope:     domain.ProviderCredentialScopeTenant,
		TenantRef: &otherTenant,
		Credential: services.ProviderStaticCredentialInput{
			APIKey: "wrong-tenant-secret",
		},
		IdempotencyKey: "wrong-tenant",
	})
	var public *services.PublicError
	if !errors.As(err, &public) || public.Code != services.CodeForbidden {
		t.Fatalf("cross-tenant Runtime create error = %v", err)
	}
}

func TestExpiredCallerCredentialCleanupIntegration(t *testing.T) {
	pool, runtime, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	clock := identity.SystemClock{}
	keyring, err := secretcrypto.NewKeyring("v1", map[string][]byte{
		"v1": []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	runtime = services.NewRuntimeService(
		store,
		NewTransactionManager(pool),
		clock,
		identity.NewUUIDv7Generator(clock),
		services.WithProviderCredentialPolicy(services.ProviderCredentialPolicy{
			DeploymentMode: services.CredentialDeploymentSelfHosted,
			DefaultSource:  domain.ProviderCredentialSourceInstallationBYOK,
		}, keyring, 5*time.Minute),
	)
	input := runtimeInput()
	input.ProviderCredentials = []services.ProviderCredentialSelection{
		{
			Provider: "anthropic",
			Source:   domain.ProviderCredentialSourceCallerEphemeral,
			Credential: &services.ProviderStaticCredentialInput{
				APIKey: "expiring-secret",
			},
		},
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit expiring caller credential: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE invocation_provider_credentials
		SET expires_at = CURRENT_TIMESTAMP - INTERVAL '1 minute'
		WHERE invocation_id = $1
	`, ack.InvocationID); err != nil {
		t.Fatalf("expire caller binding: %v", err)
	}
	cleared, err := store.ClearExpiredProviderCredentialMaterial(ctx, time.Now().UTC(), 10)
	if err != nil || cleared != 1 {
		t.Fatalf("clear expired credential count = %d, error = %v", cleared, err)
	}
	binding, err := store.GetInvocationProviderCredential(ctx, ack.InvocationID, "anthropic")
	if err != nil || len(binding.Ciphertext) != 0 || binding.ClearedAt == nil {
		t.Fatalf("expired caller binding = %#v, %v", binding, err)
	}
}

func TestRotateProviderCredentialAfterCurrentVersionExpiresIntegration(t *testing.T) {
	pool, _, store, auth := newRuntimeFixture(t)
	ctx := context.Background()
	clock := identity.SystemClock{}
	ids := identity.NewUUIDv7Generator(clock)
	auth.Profile = domain.AuthProfileOperator
	auth.ActorID = "operator:rotation-integration"
	for _, operation := range []domain.RuntimeOperation{
		domain.OperationCreateProviderCredential,
		domain.OperationRotateProviderCredential,
	} {
		auth.Operations[operation] = struct{}{}
	}
	keyring, err := secretcrypto.NewKeyring("v1", map[string][]byte{
		"v1": []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	lifecycle := services.NewProviderCredentialService(
		store,
		NewTransactionManager(pool),
		clock,
		ids,
		keyring,
	)
	created, err := lifecycle.Create(ctx, auth, services.CreateProviderCredentialInput{
		Provider: "anthropic",
		Scope:    domain.ProviderCredentialScopeAccount,
		Credential: services.ProviderStaticCredentialInput{
			APIKey: "expired-version-secret",
		},
		IdempotencyKey: "expired-version-create",
	})
	if err != nil {
		t.Fatalf("create provider credential: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE provider_credential_versions
		SET expires_at = CURRENT_TIMESTAMP - INTERVAL '1 minute'
		WHERE id = $1
	`, created.VersionID); err != nil {
		t.Fatalf("expire provider credential version: %v", err)
	}
	if cleared, err := store.ClearExpiredProviderCredentialMaterial(ctx, time.Now().UTC(), 10); err != nil || cleared != 1 {
		t.Fatalf("clear expired provider credential count = %d, error = %v", cleared, err)
	}
	rotated, err := lifecycle.Rotate(ctx, auth, created.ID, services.RotateProviderCredentialInput{
		Credential: services.ProviderStaticCredentialInput{
			APIKey: "replacement-version-secret",
		},
		OverlapSeconds: 60,
		IdempotencyKey: "expired-version-rotate",
	})
	if err != nil {
		t.Fatalf("rotate expired provider credential: %v", err)
	}
	if rotated.Version != 2 || rotated.VersionStatus != domain.ProviderCredentialVersionActive {
		t.Fatalf("rotated provider credential = %#v", rotated)
	}
	previous, err := store.GetProviderCredentialVersion(ctx, created.VersionID)
	if err != nil || previous.Status != domain.ProviderCredentialVersionRevoked || previous.DestroyedAt == nil ||
		previous.OverlapExpiresAt != nil || len(previous.Ciphertext) != 0 {
		t.Fatalf("retired expired provider credential version = %#v, %v", previous, err)
	}
}
