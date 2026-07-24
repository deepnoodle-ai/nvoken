package services

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

type mcpGenerationStore struct {
	*fakeGenerationStore
	discovery *domain.InvocationMCPDiscovery
	creates   int
}

func (*mcpGenerationStore) CreateInvocationMCPServerBinding(
	context.Context,
	domain.InvocationMCPServerBinding,
) error {
	return errors.New("not used")
}

func (*mcpGenerationStore) GetInvocationMCPServerBinding(
	context.Context,
	string,
	string,
) (domain.InvocationMCPServerBinding, error) {
	return domain.InvocationMCPServerBinding{}, errors.New("not used")
}

func (*mcpGenerationStore) ListInvocationMCPServerBindings(
	context.Context,
	string,
) ([]domain.InvocationMCPServerBinding, error) {
	return nil, errors.New("not used")
}

func (*mcpGenerationStore) ClearExpiredMCPServerBindingMaterial(
	context.Context,
	time.Time,
	int,
) (int64, error) {
	return 0, errors.New("not used")
}

func (s *mcpGenerationStore) CreateInvocationMCPDiscovery(
	_ context.Context,
	discovery domain.InvocationMCPDiscovery,
	_ string,
	_ int64,
) (domain.InvocationMCPDiscovery, error) {
	s.creates++
	copy := discovery
	copy.Catalog = append(json.RawMessage(nil), discovery.Catalog...)
	s.discovery = &copy
	return copy, nil
}

func (s *mcpGenerationStore) GetInvocationMCPDiscovery(
	context.Context,
	string,
) (domain.InvocationMCPDiscovery, error) {
	if s.discovery == nil {
		return domain.InvocationMCPDiscovery{}, ports.ErrNotFound
	}
	copy := *s.discovery
	copy.Catalog = append(json.RawMessage(nil), s.discovery.Catalog...)
	return copy, nil
}

type mcpExecutionIDs struct{}

func (mcpExecutionIDs) NewID(prefix domain.StableIDPrefix) (string, error) {
	return string(prefix) + "_018fb8d0-0f00-7000-8000-000000000001", nil
}

type mcpExecutionCredentials struct{}

func (mcpExecutionCredentials) ResolveMCPServerHeaders(
	_ context.Context,
	_ string,
	serverName string,
) (map[string]string, error) {
	return map[string]string{"Authorization": "Bearer " + serverName}, nil
}

type recordingMCPExecutionClient struct {
	mu          sync.Mutex
	tools       map[string][]domain.MCPRemoteTool
	errors      map[string]error
	connections []domain.MCPServerConnection
}

func (c *recordingMCPExecutionClient) Discover(
	_ context.Context,
	connection domain.MCPServerConnection,
) ([]domain.MCPRemoteTool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connections = append(c.connections, connection)
	return append([]domain.MCPRemoteTool(nil), c.tools[connection.Name]...), c.errors[connection.Name]
}

func (*recordingMCPExecutionClient) Call(
	context.Context,
	domain.MCPServerConnection,
	string,
	json.RawMessage,
) (domain.MCPCallResult, error) {
	return domain.MCPCallResult{}, errors.New("not used")
}

func TestGenerationExecutorCommitsMCPDiscoveryBeforeProviderCall(t *testing.T) {
	claim := generationClaim()
	claim.Invocation.DeadlineAt = time.Now().Add(time.Minute)
	base := generationStoreFixture(claim)
	base.snapshot.Spec = json.RawMessage(`{
		"instructions":"durable instructions",
		"model":{"provider":"anthropic","id":"claude-test"},
		"mcp_servers":[
			{"name":"alpha","url":"https://alpha.example.test/mcp","timeouts":{"discovery_seconds":2,"call_seconds":7}},
			{"name":"beta","url":"https://beta.example.test/mcp","allowed_tools":["find"]}
		]
	}`)
	store := &mcpGenerationStore{fakeGenerationStore: base}
	client := &recordingMCPExecutionClient{tools: map[string][]domain.MCPRemoteTool{
		"alpha": {{
			Name:        "lookup",
			Description: "Look up an item",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		"beta": {{
			Name:        "find",
			Description: "Find an item",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		}},
	}}
	generator := &fakeModelGenerator{response: successfulGenerationResponse()}
	result, err := NewGenerationExecutor(
		store,
		generator,
		nil,
		WithGenerationMCP(client, mcpExecutionCredentials{}, mcpExecutionIDs{}),
	).Execute(context.Background(), claim)
	if err != nil || result.Status != domain.InvocationCompleted {
		t.Fatalf("Execute = %#v, %v", result, err)
	}
	if store.creates != 1 || store.discovery == nil || len(generator.requests) != 1 {
		t.Fatalf("discovery creates = %d, model calls = %d", store.creates, len(generator.requests))
	}
	tools := generator.requests[0].MCPTools
	if len(tools) != 2 ||
		tools[0].Name != "alpha__lookup" ||
		tools[1].Name != "beta__find" ||
		tools[0].CallTimeout != 7*time.Second {
		t.Fatalf("MCP tools = %#v", tools)
	}
	for _, connection := range client.connections {
		if connection.Headers["Authorization"] != "Bearer "+connection.Name {
			t.Fatalf("ephemeral connection headers = %#v", connection.Headers)
		}
	}
	if string(store.discovery.Catalog) == "" ||
		json.Valid(store.discovery.Catalog) == false {
		t.Fatalf("durable catalog = %s", store.discovery.Catalog)
	}
}

func TestGenerationExecutorMCPDiscoveryFailureSkipsProvider(t *testing.T) {
	claim := generationClaim()
	claim.Invocation.DeadlineAt = time.Now().Add(time.Minute)
	base := generationStoreFixture(claim)
	base.snapshot.Spec = json.RawMessage(`{
		"instructions":"durable instructions",
		"model":{"provider":"anthropic","id":"claude-test"},
		"mcp_servers":[
			{"name":"alpha","url":"https://alpha.example.test/mcp"},
			{"name":"beta","url":"https://beta.example.test/mcp"}
		]
	}`)
	store := &mcpGenerationStore{fakeGenerationStore: base}
	client := &recordingMCPExecutionClient{
		tools: map[string][]domain.MCPRemoteTool{
			"alpha": {{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		},
		errors: map[string]error{"beta": errors.New("unreachable")},
	}
	generator := &fakeModelGenerator{response: successfulGenerationResponse()}
	result, err := NewGenerationExecutor(
		store,
		generator,
		nil,
		WithGenerationMCP(client, mcpExecutionCredentials{}, mcpExecutionIDs{}),
	).Execute(context.Background(), claim)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertFailureCode(t, result, string(CodeMCPDiscoveryFailed))
	if len(generator.requests) != 0 || store.discovery != nil {
		t.Fatalf("model calls = %d, discovery = %#v", len(generator.requests), store.discovery)
	}
}

func TestGenerationExecutorReusesCommittedMCPCatalog(t *testing.T) {
	claim := generationClaim()
	base := generationStoreFixture(claim)
	base.snapshot.Spec = json.RawMessage(`{
		"instructions":"durable instructions",
		"model":{"provider":"anthropic","id":"claude-test"},
		"mcp_servers":[{"name":"alpha","url":"https://alpha.example.test/mcp"}]
	}`)
	catalog, _ := json.Marshal(domain.MCPDiscoveryCatalog{
		Tools: []domain.MCPProjectedTool{{
			ServerName:    "alpha",
			ProjectedName: "alpha__lookup",
			RemoteName:    "lookup",
			InputSchema:   json.RawMessage(`{"type":"object"}`),
		}},
	})
	store := &mcpGenerationStore{
		fakeGenerationStore: base,
		discovery: &domain.InvocationMCPDiscovery{
			ID:                "mcpd_existing",
			InvocationID:      claim.Invocation.ID,
			AccountID:         claim.Invocation.AccountID,
			TenantPartitionID: claim.Invocation.TenantPartitionID,
			Catalog:           catalog,
		},
	}
	client := &recordingMCPExecutionClient{}
	generator := &fakeModelGenerator{response: successfulGenerationResponse()}
	result, err := NewGenerationExecutor(
		store,
		generator,
		nil,
		WithGenerationMCP(client, mcpExecutionCredentials{}, mcpExecutionIDs{}),
	).Execute(context.Background(), claim)
	if err != nil || result.Status != domain.InvocationCompleted {
		t.Fatalf("Execute = %#v, %v", result, err)
	}
	if len(client.connections) != 0 || store.creates != 0 ||
		len(generator.requests) != 1 || len(generator.requests[0].MCPTools) != 1 {
		t.Fatalf("connections = %d, creates = %d, requests = %#v",
			len(client.connections), store.creates, generator.requests)
	}
}
