package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/adapters/secretcrypto"
	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type fakeMCPClient struct {
	connection domain.MCPServerConnection
	tools      []domain.MCPRemoteTool
	err        error
}

type resolverMCPStore struct {
	ports.MCPRepository
	binding domain.InvocationMCPServerBinding
	err     error
}

func (s *resolverMCPStore) GetInvocationMCPServerBinding(
	context.Context,
	string,
	string,
) (domain.InvocationMCPServerBinding, error) {
	return s.binding, s.err
}

func (c *fakeMCPClient) Discover(
	_ context.Context,
	connection domain.MCPServerConnection,
) ([]domain.MCPRemoteTool, error) {
	c.connection = connection
	return c.tools, c.err
}

func (c *fakeMCPClient) Call(
	context.Context,
	domain.MCPServerConnection,
	string,
	json.RawMessage,
) (domain.MCPCallResult, error) {
	return domain.MCPCallResult{}, errors.New("not used")
}

func TestListMCPToolsUsesEphemeralHeadersAndStableErrors(t *testing.T) {
	client := &fakeMCPClient{
		tools: []domain.MCPRemoteTool{
			{
				Name:        "lookup",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		},
	}
	service := NewRuntimeService(
		&legacyFingerprintStore{},
		recoveryTestTx{},
		recoveryTestClock{},
		recoveryTestIDs{},
		WithMCPClient(client),
		WithRuntimeLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	)
	result, err := service.ListMCPTools(
		context.Background(),
		recoveryAuth(domain.OperationCreateInvocation),
		MCPListToolsInput{
			Server: MCPServerSpec{
				Name: "calendar",
				URL:  "https://calendar.example.test/mcp",
				Headers: map[string]string{
					"Authorization": "Bearer secret",
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("list MCP tools: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].ProjectedName != "calendar__lookup" {
		t.Fatalf("result = %#v", result)
	}
	if client.connection.Headers["Authorization"] != "Bearer secret" {
		t.Fatalf("connection headers = %#v", client.connection.Headers)
	}

	client.err = errors.New("upstream included Bearer secret")
	_, err = service.ListMCPTools(
		context.Background(),
		recoveryAuth(domain.OperationCreateInvocation),
		MCPListToolsInput{
			Server: MCPServerSpec{
				Name: "calendar",
				URL:  "https://calendar.example.test/mcp",
			},
		},
	)
	var public *PublicError
	if !errors.As(err, &public) || public.Code != CodeMCPDiscoveryFailed {
		t.Fatalf("error = %#v", err)
	}
	encoded, marshalErr := json.Marshal(struct {
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}{
		Message: public.Message,
		Details: public.Details,
	})
	if marshalErr != nil {
		t.Fatalf("marshal public error: %v", marshalErr)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "upstream") {
		t.Fatalf("public error leaks cause: %s", encoded)
	}
}

func TestMCPServerCredentialResolverDecryptsOnlyLiveBinding(t *testing.T) {
	now := time.Date(2026, time.July, 24, 16, 0, 0, 0, time.UTC)
	keyring, err := secretcrypto.NewKeyring("v1", map[string][]byte{
		"v1": []byte("0123456789abcdef0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	binding := domain.InvocationMCPServerBinding{
		ID:           "binding",
		InvocationID: "invocation",
		ServerName:   "calendar",
	}
	expiresAt := now.Add(time.Hour)
	encrypted, err := keyring.Encrypt(
		[]byte(`{"Authorization":"Bearer secret"}`),
		invocationMCPServerBindingAAD(binding.InvocationID, binding.ServerName, binding.ID),
	)
	if err != nil {
		t.Fatalf("encrypt headers: %v", err)
	}
	binding.EncryptionKeyID = &encrypted.KeyID
	binding.Nonce = encrypted.Nonce
	binding.Ciphertext = encrypted.Ciphertext
	binding.ExpiresAt = &expiresAt
	store := &resolverMCPStore{binding: binding}
	resolver := NewMCPServerCredentialResolver(
		store,
		keyring,
		credentialResolverClock{now: now},
	)
	headers, err := resolver.ResolveMCPServerHeaders(
		context.Background(),
		binding.InvocationID,
		binding.ServerName,
	)
	if err != nil || headers["Authorization"] != "Bearer secret" {
		t.Fatalf("resolved headers = %#v, %v", headers, err)
	}

	binding.ExpiresAt = &now
	store.binding = binding
	if _, err := resolver.ResolveMCPServerHeaders(
		context.Background(),
		binding.InvocationID,
		binding.ServerName,
	); !errors.Is(err, ports.ErrMCPServerCredentialUnavailable) {
		t.Fatalf("expired error = %v", err)
	}

	store.binding = domain.InvocationMCPServerBinding{
		InvocationID: binding.InvocationID,
		ServerName:   binding.ServerName,
	}
	headers, err = resolver.ResolveMCPServerHeaders(
		context.Background(),
		binding.InvocationID,
		binding.ServerName,
	)
	if err != nil || len(headers) != 0 {
		t.Fatalf("unauthenticated headers = %#v, %v", headers, err)
	}
}

func TestProjectMCPToolsAppliesStableProjectionAndExclusions(t *testing.T) {
	server := resolvedMCPServerSpec(MCPServerSpec{
		Name: "calendar",
		URL:  "https://calendar.example.test/mcp",
	})
	destructive := false
	result, err := projectMCPTools(server, []domain.MCPRemoteTool{
		{
			Name:        "lookup",
			Description: strings.Repeat("界", MaxMCPDescriptionRunes+1),
			InputSchema: json.RawMessage(`{"properties":{"id":{"type":"string"}},"type":"object"}`),
			ReadOnly:    true,
			Destructive: &destructive,
		},
		{
			Name:        "invalid.name",
			Description: "Invalid projected name",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		{
			Name:        "collision",
			Description: "Collides with a declared host tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		{
			Name:        "invalid_schema",
			Description: "Schema is not object-rooted",
			InputSchema: json.RawMessage(`{"type":"string"}`),
		},
	}, map[string]struct{}{"calendar__collision": {}})
	if err != nil {
		t.Fatalf("project tools: %v", err)
	}
	if len(result.Tools) != 1 || result.Tools[0].ProjectedName != "calendar__lookup" {
		t.Fatalf("projected tools = %#v", result.Tools)
	}
	if len([]rune(result.Tools[0].Description)) != MaxMCPDescriptionRunes {
		t.Fatalf("description runes = %d", len([]rune(result.Tools[0].Description)))
	}
	if string(result.Tools[0].InputSchema) != `{"properties":{"id":{"type":"string"}},"type":"object"}` {
		t.Fatalf("canonical input schema = %s", result.Tools[0].InputSchema)
	}
	if result.Tools[0].Annotations.ReadOnlyHint == nil ||
		!*result.Tools[0].Annotations.ReadOnlyHint ||
		result.Tools[0].Annotations.IdempotentHint != nil {
		t.Fatalf("annotations = %#v", result.Tools[0].Annotations)
	}
	reasons := make(map[string]string, len(result.Exclusions))
	for _, exclusion := range result.Exclusions {
		reasons[exclusion.RemoteName] = exclusion.Reason
	}
	if reasons["invalid.name"] != "invalid_name" ||
		reasons["collision"] != "name_collision" ||
		reasons["invalid_schema"] != "invalid_schema" {
		t.Fatalf("exclusions = %#v", result.Exclusions)
	}
}

func TestProjectMCPToolsEnforcesAllowlistAndCap(t *testing.T) {
	server := resolvedMCPServerSpec(MCPServerSpec{
		Name:         "support",
		URL:          "https://support.example.test/mcp",
		AllowedTools: []string{"lookup"},
	})
	result, err := projectMCPTools(server, []domain.MCPRemoteTool{
		{
			Name:        "lookup",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
		{
			Name:        "hidden",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}, nil)
	if err != nil {
		t.Fatalf("project allowlist: %v", err)
	}
	if len(result.Tools) != 1 || len(result.Exclusions) != 1 ||
		result.Exclusions[0].Reason != "not_allowlisted" {
		t.Fatalf("allowlist projection = %#v", result)
	}

	server.AllowedTools = []string{"missing"}
	if _, err := projectMCPTools(server, nil, nil); err == nil {
		t.Fatal("missing allowlisted tool was accepted")
	}

	server.AllowedTools = nil
	remote := make([]domain.MCPRemoteTool, MaxMCPProjectedTools+1)
	for index := range remote {
		remote[index] = domain.MCPRemoteTool{
			Name:        fmt.Sprintf("tool_%d", index),
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}
	}
	if _, err := projectMCPTools(server, remote, nil); err == nil {
		t.Fatal("projected tool cap was exceeded")
	}
}

func TestMCPRetryRequiresPositiveUncontradictedHint(t *testing.T) {
	positive := true
	negative := false
	for name, annotations := range map[string]domain.MCPToolAnnotations{
		"read only": {
			ReadOnlyHint: &positive,
		},
		"idempotent": {
			IdempotentHint: &positive,
		},
		"omitted": {},
		"negative": {
			ReadOnlyHint:   &negative,
			IdempotentHint: &negative,
		},
		"contradictory": {
			ReadOnlyHint:    &positive,
			DestructiveHint: &positive,
		},
	} {
		want := name == "read only" || name == "idempotent"
		if got := isSafeMCPRetry(annotations); got != want {
			t.Fatalf("%s retry = %t, want %t", name, got, want)
		}
	}
}
