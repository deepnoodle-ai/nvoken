package postgres

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
	"github.com/deepnoodle-ai/nvoken/internal/services"
)

type integrationMCPClient struct{}

func (*integrationMCPClient) Discover(
	context.Context,
	domain.MCPServerConnection,
) ([]domain.MCPRemoteTool, error) {
	return nil, errors.New("not used")
}

func (*integrationMCPClient) Call(
	context.Context,
	domain.MCPServerConnection,
	string,
	json.RawMessage,
) (domain.MCPCallResult, error) {
	return domain.MCPCallResult{}, errors.New("not used")
}

func TestMCPAdmissionBindingsCleanupAndDiscoveryFenceIntegration(t *testing.T) {
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
	runtime := services.NewRuntimeService(
		store,
		txm,
		clock,
		ids,
		services.WithProviderCredentialPolicy(services.ProviderCredentialPolicy{
			DeploymentMode: services.CredentialDeploymentSelfHosted,
			DefaultSource:  domain.ProviderCredentialSourceInstallationBYOK,
		}, keyring, 5*time.Minute),
		services.WithMCPClient(&integrationMCPClient{}),
	)
	auth := runtimeAuth(account.ID)
	input := runtimeInput()
	input.Spec.MCPServers = []services.MCPServerSpec{
		{
			Name: "calendar",
			URL:  "https://calendar.example.test/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer first-mcp-secret",
			},
		},
	}
	ack, err := runtime.Admit(ctx, auth, input)
	if err != nil {
		t.Fatalf("admit MCP Invocation: %v", err)
	}
	binding, err := store.GetInvocationMCPServerBinding(ctx, ack.InvocationID, "calendar")
	if err != nil {
		t.Fatalf("load MCP binding: %v", err)
	}
	if binding.EncryptionKeyID == nil || len(binding.Nonce) == 0 || len(binding.Ciphertext) == 0 ||
		bytes.Contains(binding.Ciphertext, []byte("first-mcp-secret")) || binding.ExpiresAt == nil {
		t.Fatalf("encrypted MCP binding = %#v", binding)
	}
	invocation, err := store.GetInvocation(ctx, ack.InvocationID)
	if err != nil {
		t.Fatalf("load MCP Invocation: %v", err)
	}
	snapshot, err := store.GetExecutionSpecSnapshot(ctx, invocation.SpecSnapshotID)
	if err != nil {
		t.Fatalf("load MCP spec snapshot: %v", err)
	}
	if bytes.Contains(snapshot.Spec, []byte("first-mcp-secret")) ||
		bytes.Contains(snapshot.Spec, []byte(`"headers"`)) {
		t.Fatalf("durable spec contains MCP headers: %s", snapshot.Spec)
	}
	for _, expected := range [][]byte{
		[]byte(`"transport":"streamable_http"`),
		[]byte(`"discovery_seconds":10`),
		[]byte(`"call_seconds":30`),
	} {
		if !bytes.Contains(snapshot.Spec, expected) {
			t.Fatalf("durable spec lacks %s: %s", expected, snapshot.Spec)
		}
	}

	replayInput := input
	replayInput.Spec.MCPServers = append([]services.MCPServerSpec(nil), input.Spec.MCPServers...)
	replayInput.Spec.MCPServers[0].Headers = map[string]string{
		"Authorization": "Bearer changed-mcp-secret",
	}
	replay, err := runtime.Admit(ctx, auth, replayInput)
	if err != nil || !replay.Deduplicated || replay.InvocationID != ack.InvocationID {
		t.Fatalf("MCP secret-only replay = %#v, %v", replay, err)
	}
	replayedBinding, err := store.GetInvocationMCPServerBinding(ctx, ack.InvocationID, "calendar")
	if err != nil || !bytes.Equal(replayedBinding.Ciphertext, binding.Ciphertext) ||
		!bytes.Equal(replayedBinding.Nonce, binding.Nonce) {
		t.Fatalf("replay replaced MCP binding = %#v, %v", replayedBinding, err)
	}
	if _, err := runtime.CancelInvocation(ctx, auth, ack.InvocationID); err != nil {
		t.Fatalf("cancel MCP Invocation: %v", err)
	}
	cleared, err := store.GetInvocationMCPServerBinding(ctx, ack.InvocationID, "calendar")
	if err != nil || cleared.EncryptionKeyID != nil || len(cleared.Nonce) != 0 ||
		len(cleared.Ciphertext) != 0 || cleared.ClearedAt == nil {
		t.Fatalf("terminal MCP cleanup = %#v, %v", cleared, err)
	}

	sweepInput := runtimeInput()
	sweepInput.AgentKey = "mcp-sweep"
	sweepInput.SessionKey = pointerString("mcp-sweep")
	sweepInput.IdempotencyKey = "mcp-sweep"
	sweepInput.Spec.MCPServers = input.Spec.MCPServers
	sweepAck, err := runtime.Admit(ctx, auth, sweepInput)
	if err != nil {
		t.Fatalf("admit sweep fixture: %v", err)
	}
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if _, err := pool.Exec(ctx, `
		UPDATE invocation_mcp_server_bindings
		SET expires_at = $1
		WHERE invocation_id = $2
	`, expiredAt, sweepAck.InvocationID); err != nil {
		t.Fatalf("expire MCP binding fixture: %v", err)
	}
	if clearedRows, err := store.ClearExpiredMCPServerBindingMaterial(ctx, time.Now().UTC(), 10); err != nil || clearedRows != 1 {
		t.Fatalf("sweep MCP bindings = %d, %v", clearedRows, err)
	}
	swept, err := store.GetInvocationMCPServerBinding(ctx, sweepAck.InvocationID, "calendar")
	if err != nil || swept.ClearedAt == nil || len(swept.Ciphertext) != 0 {
		t.Fatalf("swept MCP binding = %#v, %v", swept, err)
	}

	discoveryInput := runtimeInput()
	discoveryInput.AgentKey = "mcp-discovery"
	discoveryInput.SessionKey = pointerString("mcp-discovery")
	discoveryInput.IdempotencyKey = "mcp-discovery"
	discoveryInput.Spec.MCPServers = []services.MCPServerSpec{
		{
			Name: "public",
			URL:  "https://public.example.test/mcp",
		},
	}
	discoveryAck, err := runtime.Admit(ctx, auth, discoveryInput)
	if err != nil {
		t.Fatalf("admit discovery fixture: %v", err)
	}
	ownership := services.NewInvocationExecutionService(store, txm, clock, ids)
	claim, disposition, err := ownership.ClaimExact(ctx, discoveryAck.InvocationID, "mcp-owner", time.Minute)
	if err != nil || disposition != services.Claimed {
		t.Fatalf("claim discovery fixture = %#v, %q, %v", claim, disposition, err)
	}
	catalog, err := json.Marshal(domain.MCPDiscoveryCatalog{
		Tools:      []domain.MCPProjectedTool{},
		Exclusions: []domain.MCPToolExclusion{},
	})
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	discovery := domain.InvocationMCPDiscovery{
		ID:                testID(t, domain.PrefixInvocationMCPDiscovery),
		InvocationID:      claim.Invocation.ID,
		AccountID:         claim.Invocation.AccountID,
		TenantPartitionID: claim.Invocation.TenantPartitionID,
		Catalog:           catalog,
		CreatedAt:         time.Now().UTC(),
	}
	if _, err := store.CreateInvocationMCPDiscovery(ctx, discovery, "stale-owner", claim.Attempt); !errors.Is(err, ports.ErrLeaseLost) {
		t.Fatalf("stale discovery fence error = %v", err)
	}
	created, err := store.CreateInvocationMCPDiscovery(ctx, discovery, claim.Owner, claim.Attempt)
	if err != nil || created.InvocationID != claim.Invocation.ID {
		t.Fatalf("create fenced discovery = %#v, %v", created, err)
	}
	loaded, err := store.GetInvocationMCPDiscovery(ctx, claim.Invocation.ID)
	if err != nil || !bytes.Equal(loaded.Catalog, catalog) {
		t.Fatalf("load fenced discovery = %#v, %v", loaded, err)
	}
}
