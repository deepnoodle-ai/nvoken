package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/structuredoutput"
)

const (
	MaxMCPProjectedTools   = 64
	MaxMCPDescriptionRunes = 4096
	MaxMCPInputSchemaDepth = 32
)

type MCPListToolsInput struct {
	Server MCPServerSpec `json:"server"`
}

type MCPListToolsResult = domain.MCPDiscoveryCatalog

func (s *RuntimeService) ListMCPTools(
	ctx context.Context,
	auth domain.RuntimeAuthContext,
	input MCPListToolsInput,
) (MCPListToolsResult, error) {
	if err := s.ready(); err != nil {
		return MCPListToolsResult{}, err
	}
	if err := authorize(auth, domain.OperationCreateInvocation); err != nil {
		return MCPListToolsResult{}, err
	}
	if err := validateMCPServers([]MCPServerSpec{input.Server}); err != nil {
		return MCPListToolsResult{}, err
	}
	if s.mcpClient == nil {
		return MCPListToolsResult{}, &PublicError{
			Code:    CodeUnavailable,
			Message: "MCP discovery is not configured for this installation.",
		}
	}
	started := s.clock.Now().UTC()
	result, err := s.discoverMCPServer(ctx, input.Server, nil)
	duration := s.clock.Now().UTC().Sub(started)
	if err != nil {
		s.logger.WarnContext(
			ctx,
			"MCP discovery failed",
			"event", "mcp_discovery",
			"outcome", "failed",
			"server_name", input.Server.Name,
			"duration_ms", duration.Milliseconds(),
			"error_code", CodeMCPDiscoveryFailed,
		)
		return MCPListToolsResult{}, mcpDiscoveryFailed(err)
	}
	s.logger.InfoContext(
		ctx,
		"MCP discovery completed",
		"event", "mcp_discovery",
		"outcome", "success",
		"server_name", input.Server.Name,
		"duration_ms", duration.Milliseconds(),
		"tool_count", len(result.Tools),
		"exclusion_count", len(result.Exclusions),
	)
	return result, nil
}

func (s *RuntimeService) discoverMCPServer(
	ctx context.Context,
	unresolved MCPServerSpec,
	occupied map[string]struct{},
) (domain.MCPDiscoveryCatalog, error) {
	server := resolvedMCPServerSpec(unresolved)
	deadline := time.Duration(server.Timeouts.DiscoverySeconds) * time.Second
	discoveryContext, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	remote, err := s.mcpClient.Discover(discoveryContext, mcpConnection(server))
	if err != nil {
		return domain.MCPDiscoveryCatalog{}, err
	}
	return projectMCPTools(server, remote, occupied)
}

func projectMCPTools(
	server MCPServerSpec,
	remote []domain.MCPRemoteTool,
	occupied map[string]struct{},
) (domain.MCPDiscoveryCatalog, error) {
	result := domain.MCPDiscoveryCatalog{
		Tools:      []domain.MCPProjectedTool{},
		Exclusions: []domain.MCPToolExclusion{},
	}
	used := make(map[string]struct{}, len(occupied)+len(remote))
	for name := range occupied {
		used[name] = struct{}{}
	}
	allowlist := make(map[string]struct{}, len(server.AllowedTools))
	for _, name := range server.AllowedTools {
		allowlist[name] = struct{}{}
	}
	projectedAllowed := make(map[string]struct{}, len(allowlist))
	for _, tool := range remote {
		if server.AllowedTools != nil {
			if _, allowed := allowlist[tool.Name]; !allowed {
				result.Exclusions = append(result.Exclusions, domain.MCPToolExclusion{
					ServerName: server.Name,
					RemoteName: tool.Name,
					Reason:     "not_allowlisted",
				})
				continue
			}
		}
		projectedName := server.Name + "__" + tool.Name
		reason := ""
		if len(projectedName) > MaxHostToolNameBytes || !hostToolNamePattern.MatchString(projectedName) {
			reason = "invalid_name"
		} else if _, collision := used[projectedName]; collision {
			reason = "name_collision"
		}
		canonical, schemaReason := canonicalMCPInputSchema(tool.InputSchema)
		if reason == "" {
			reason = schemaReason
		}
		if reason != "" {
			result.Exclusions = append(result.Exclusions, domain.MCPToolExclusion{
				ServerName: server.Name,
				RemoteName: tool.Name,
				Reason:     reason,
			})
			continue
		}
		used[projectedName] = struct{}{}
		if _, allowed := allowlist[tool.Name]; allowed {
			projectedAllowed[tool.Name] = struct{}{}
		}
		result.Tools = append(result.Tools, domain.MCPProjectedTool{
			ServerName:    server.Name,
			ProjectedName: projectedName,
			RemoteName:    tool.Name,
			Description:   truncateRunes(tool.Description, MaxMCPDescriptionRunes),
			InputSchema:   canonical,
			Annotations: domain.MCPToolAnnotations{
				ReadOnlyHint:    positiveHint(tool.ReadOnly),
				IdempotentHint:  positiveHint(tool.Idempotent),
				DestructiveHint: tool.Destructive,
			},
		})
		if len(result.Tools) > MaxMCPProjectedTools {
			return domain.MCPDiscoveryCatalog{}, fmt.Errorf("projected MCP tool limit exceeded")
		}
	}
	if server.AllowedTools != nil {
		for _, name := range server.AllowedTools {
			if _, ok := projectedAllowed[name]; !ok {
				return domain.MCPDiscoveryCatalog{}, fmt.Errorf("allowlisted MCP tool is missing or not projectable")
			}
		}
	}
	return result, nil
}

func canonicalMCPInputSchema(input json.RawMessage) (json.RawMessage, string) {
	canonical, err := canonicalJSON(input)
	if err != nil {
		return nil, "invalid_schema"
	}
	if len(canonical) > structuredoutput.MaxSchemaBytes {
		return nil, "schema_too_large"
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, "invalid_schema"
	}
	root, ok := value.(map[string]any)
	if !ok || root["type"] != "object" {
		return nil, "invalid_schema"
	}
	if jsonDepth(value) > MaxMCPInputSchemaDepth {
		return nil, "schema_too_deep"
	}
	return canonical, ""
}

func jsonDepth(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		maximum := 1
		for _, child := range typed {
			maximum = max(maximum, 1+jsonDepth(child))
		}
		return maximum
	case []any:
		maximum := 1
		for _, child := range typed {
			maximum = max(maximum, 1+jsonDepth(child))
		}
		return maximum
	default:
		return 0
	}
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 || !utf8.ValidString(value) {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}

func positiveHint(value bool) *bool {
	if !value {
		return nil
	}
	positive := true
	return &positive
}

func mcpConnection(server MCPServerSpec) domain.MCPServerConnection {
	server = resolvedMCPServerSpec(server)
	return domain.MCPServerConnection{
		Name:             server.Name,
		URL:              server.URL,
		Headers:          server.Headers,
		DiscoveryTimeout: time.Duration(server.Timeouts.DiscoverySeconds) * time.Second,
		CallTimeout:      time.Duration(server.Timeouts.CallSeconds) * time.Second,
	}
}

func mcpDiscoveryFailed(cause error) error {
	return &PublicError{
		Code:    CodeMCPDiscoveryFailed,
		Message: "The remote MCP server could not be discovered.",
		Details: map[string]any{
			"reason": "discovery_failed",
		},
		Cause: cause,
	}
}

func isSafeMCPRetry(annotations domain.MCPToolAnnotations) bool {
	destructive := annotations.DestructiveHint != nil && *annotations.DestructiveHint
	return !destructive &&
		(annotations.ReadOnlyHint != nil && *annotations.ReadOnlyHint ||
			annotations.IdempotentHint != nil && *annotations.IdempotentHint)
}

func mcpToolName(serverName, remoteName string) string {
	return strings.TrimSpace(serverName) + "__" + strings.TrimSpace(remoteName)
}
