package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

var errMCPAggregateDiscoveryDeadline = errors.New("MCP aggregate discovery deadline reached")

func (e *GenerationExecutor) prepareMCPGenerationTools(
	ctx context.Context,
	claim domain.InvocationClaim,
	spec InlineExecutionSpec,
) ([]domain.MCPToolDefinition, *domain.InvocationExecutionResult, error) {
	if len(spec.MCPServers) == 0 {
		return nil, nil, nil
	}
	store, ok := e.store.(ports.MCPRepository)
	if !ok || e.mcpClient == nil || e.mcpCredentials == nil || e.ids == nil {
		failed := mcpDiscoveryGenerationFailure()
		return nil, &failed, nil
	}
	catalog, err := e.loadOrDiscoverMCPCatalog(ctx, store, claim, spec)
	if err != nil {
		if errors.Is(err, ports.ErrLeaseLost) ||
			(errors.Is(err, context.Canceled) && !errors.Is(context.Cause(ctx), errMCPAggregateDiscoveryDeadline)) ||
			(errors.Is(err, context.DeadlineExceeded) && !errors.Is(context.Cause(ctx), errMCPAggregateDiscoveryDeadline)) {
			return nil, nil, err
		}
		e.logger.Warn(
			"Invocation MCP discovery failed",
			"invocation_id", claim.Invocation.ID,
			"lease_attempt", claim.Attempt,
			"server_count", len(spec.MCPServers),
			"error_code", CodeMCPDiscoveryFailed,
		)
		failed := mcpDiscoveryGenerationFailure()
		return nil, &failed, nil
	}
	tools, err := mcpToolDefinitions(spec.MCPServers, catalog)
	if err != nil {
		e.logExecutionFailure(claim, "invalid_mcp_discovery", "", "")
		return nil, nil, fmt.Errorf("validate durable MCP discovery: %w", err)
	}
	return tools, nil, nil
}

func (e *GenerationExecutor) loadOrDiscoverMCPCatalog(
	ctx context.Context,
	store ports.MCPRepository,
	claim domain.InvocationClaim,
	spec InlineExecutionSpec,
) (domain.MCPDiscoveryCatalog, error) {
	existing, err := store.GetInvocationMCPDiscovery(ctx, claim.Invocation.ID)
	switch {
	case err == nil:
		if existing.InvocationID != claim.Invocation.ID ||
			existing.AccountID != claim.Invocation.AccountID ||
			existing.TenantPartitionID != claim.Invocation.TenantPartitionID {
			return domain.MCPDiscoveryCatalog{}, fmt.Errorf("MCP discovery scope mismatch")
		}
		var catalog domain.MCPDiscoveryCatalog
		if json.Unmarshal(existing.Catalog, &catalog) != nil {
			return domain.MCPDiscoveryCatalog{}, fmt.Errorf("decode MCP discovery catalog")
		}
		return catalog, nil
	case !errors.Is(err, ports.ErrNotFound):
		return domain.MCPDiscoveryCatalog{}, err
	}

	catalog, err := e.discoverInvocationMCPServers(ctx, claim.Invocation.ID, spec)
	if err != nil {
		return domain.MCPDiscoveryCatalog{}, err
	}
	payload, err := json.Marshal(catalog)
	if err != nil {
		return domain.MCPDiscoveryCatalog{}, err
	}
	discoveryID, err := e.ids.NewID(domain.PrefixInvocationMCPDiscovery)
	if err != nil {
		return domain.MCPDiscoveryCatalog{}, err
	}
	created, err := store.CreateInvocationMCPDiscovery(ctx, domain.InvocationMCPDiscovery{
		ID:                discoveryID,
		InvocationID:      claim.Invocation.ID,
		AccountID:         claim.Invocation.AccountID,
		TenantPartitionID: claim.Invocation.TenantPartitionID,
		Catalog:           payload,
		CreatedAt:         e.clock.Now().UTC(),
	}, claim.Owner, claim.Attempt)
	if err != nil {
		return domain.MCPDiscoveryCatalog{}, err
	}
	var committed domain.MCPDiscoveryCatalog
	if json.Unmarshal(created.Catalog, &committed) != nil {
		return domain.MCPDiscoveryCatalog{}, fmt.Errorf("decode committed MCP discovery catalog")
	}
	e.logger.Info(
		"Invocation MCP discovery committed",
		"invocation_id", claim.Invocation.ID,
		"lease_attempt", claim.Attempt,
		"server_count", len(spec.MCPServers),
		"tool_count", len(committed.Tools),
		"exclusion_count", len(committed.Exclusions),
	)
	return committed, nil
}

func (e *GenerationExecutor) discoverInvocationMCPServers(
	ctx context.Context,
	invocationID string,
	spec InlineExecutionSpec,
) (domain.MCPDiscoveryCatalog, error) {
	servers := make([]MCPServerSpec, len(spec.MCPServers))
	maximum := time.Duration(0)
	for index, unresolved := range spec.MCPServers {
		servers[index] = resolvedMCPServerSpec(unresolved)
		timeout := time.Duration(servers[index].Timeouts.DiscoverySeconds) * time.Second
		if timeout > maximum {
			maximum = timeout
		}
	}
	aggregate, cancel := context.WithTimeoutCause(ctx, maximum, errMCPAggregateDiscoveryDeadline)
	defer cancel()

	type discoveryResult struct {
		tools []domain.MCPRemoteTool
		err   error
	}
	results := make([]discoveryResult, len(servers))
	var wait sync.WaitGroup
	wait.Add(len(servers))
	for index := range servers {
		go func(index int) {
			defer wait.Done()
			server := servers[index]
			headers, err := e.mcpCredentials.ResolveMCPServerHeaders(
				aggregate,
				invocationID,
				server.Name,
			)
			if err != nil {
				results[index].err = err
				return
			}
			server.Headers = headers
			serverContext, cancelServer := context.WithTimeout(
				aggregate,
				time.Duration(server.Timeouts.DiscoverySeconds)*time.Second,
			)
			defer cancelServer()
			results[index].tools, results[index].err = e.mcpClient.Discover(
				serverContext,
				mcpConnection(server),
			)
		}(index)
	}
	wait.Wait()
	if cause := context.Cause(aggregate); cause != nil {
		return domain.MCPDiscoveryCatalog{}, cause
	}

	catalog := domain.MCPDiscoveryCatalog{
		Tools:      []domain.MCPProjectedTool{},
		Exclusions: []domain.MCPToolExclusion{},
	}
	occupied := make(map[string]struct{}, len(spec.Tools))
	for _, tool := range spec.Tools {
		occupied[tool.Name] = struct{}{}
	}
	for index, result := range results {
		if result.err != nil {
			return domain.MCPDiscoveryCatalog{}, result.err
		}
		projected, err := projectMCPTools(servers[index], result.tools, occupied)
		if err != nil {
			return domain.MCPDiscoveryCatalog{}, err
		}
		for _, tool := range projected.Tools {
			occupied[tool.ProjectedName] = struct{}{}
			catalog.Tools = append(catalog.Tools, tool)
			if len(catalog.Tools) > MaxMCPProjectedTools {
				return domain.MCPDiscoveryCatalog{}, fmt.Errorf("projected MCP tool limit exceeded")
			}
		}
		catalog.Exclusions = append(catalog.Exclusions, projected.Exclusions...)
	}
	return catalog, nil
}

func mcpToolDefinitions(
	unresolved []MCPServerSpec,
	catalog domain.MCPDiscoveryCatalog,
) ([]domain.MCPToolDefinition, error) {
	servers := make(map[string]MCPServerSpec, len(unresolved))
	for _, server := range unresolved {
		resolved := resolvedMCPServerSpec(server)
		servers[resolved.Name] = resolved
	}
	definitions := make([]domain.MCPToolDefinition, len(catalog.Tools))
	seen := make(map[string]struct{}, len(catalog.Tools))
	for index, tool := range catalog.Tools {
		server, ok := servers[tool.ServerName]
		if !ok || tool.ProjectedName != mcpToolName(tool.ServerName, tool.RemoteName) ||
			!hostToolNamePattern.MatchString(tool.ProjectedName) ||
			len(tool.ProjectedName) > MaxHostToolNameBytes ||
			len(tool.InputSchema) == 0 || !json.Valid(tool.InputSchema) {
			return nil, fmt.Errorf("invalid projected MCP tool")
		}
		if _, duplicate := seen[tool.ProjectedName]; duplicate {
			return nil, fmt.Errorf("duplicate projected MCP tool")
		}
		seen[tool.ProjectedName] = struct{}{}
		definitions[index] = domain.MCPToolDefinition{
			ServerName:  server.Name,
			URL:         server.URL,
			RemoteName:  tool.RemoteName,
			Name:        tool.ProjectedName,
			Description: tool.Description,
			InputSchema: append(json.RawMessage(nil), tool.InputSchema...),
			Annotations: tool.Annotations,
			CallTimeout: time.Duration(server.Timeouts.CallSeconds) * time.Second,
		}
	}
	return definitions, nil
}

func mcpDiscoveryGenerationFailure() domain.InvocationExecutionResult {
	return domain.InvocationExecutionResult{
		Status: domain.InvocationFailed,
		Error: invocationFailureWithDetails(
			string(CodeMCPDiscoveryFailed),
			"The remote MCP server could not be discovered.",
			map[string]string{"reason": "discovery_failed"},
		),
	}
}
