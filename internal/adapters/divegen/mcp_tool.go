package divegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/deepnoodle-ai/dive"

	"github.com/deepnoodle-ai/nvoken/internal/domain"
	"github.com/deepnoodle-ai/nvoken/internal/ports"
)

const (
	maxMCPToolResultBytes = 256 << 10
	maxMCPToolResultDepth = 32
)

type mcpTool struct {
	client      ports.MCPClient
	credentials ports.MCPServerCredentialResolver
	coordinator ports.MCPToolCallCoordinator
	claim       domain.InvocationClaim
	state       *generationCheckpointState
	definition  domain.MCPToolDefinition
	schema      *dive.Schema
	logger      *slog.Logger
}

func newMCPTool(
	client ports.MCPClient,
	credentials ports.MCPServerCredentialResolver,
	coordinator ports.MCPToolCallCoordinator,
	claim domain.InvocationClaim,
	state *generationCheckpointState,
	definition domain.MCPToolDefinition,
	schema *dive.Schema,
	logger *slog.Logger,
) dive.Tool {
	return &mcpTool{
		client:      client,
		credentials: credentials,
		coordinator: coordinator,
		claim:       claim,
		state:       state,
		definition:  definition,
		schema:      schema,
		logger:      logger,
	}
}

func (t *mcpTool) Name() string         { return t.definition.Name }
func (t *mcpTool) Description() string  { return t.definition.Description }
func (t *mcpTool) Schema() *dive.Schema { return t.schema }

func (t *mcpTool) Annotations() *dive.ToolAnnotations {
	return &dive.ToolAnnotations{
		ReadOnlyHint:    positiveMCPAnnotation(t.definition.Annotations.ReadOnlyHint),
		IdempotentHint:  positiveMCPAnnotation(t.definition.Annotations.IdempotentHint),
		DestructiveHint: positiveMCPAnnotation(t.definition.Annotations.DestructiveHint),
	}
}

func (t *mcpTool) Call(ctx context.Context, input any) (*dive.ToolResult, error) {
	providerCallID := dive.ToolCallID(ctx)
	iteration := int(t.state.iteration.Load())
	start, err := t.coordinator.StartMCPToolCall(
		ctx,
		t.claim,
		iteration,
		providerCallID,
		safeMCPRetry(t.definition.Annotations),
	)
	if err != nil {
		return nil, err
	}
	if start.Execution == nil {
		if len(start.RecoveredContent) == 0 || !start.RecoveredIsError {
			return nil, ports.ErrToolCallConflict
		}
		return dive.NewToolResultError(jsonToolResultText(start.RecoveredContent)), nil
	}
	raw, err := rawToolInput(input)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now()
	headers, resolveErr := t.credentials.ResolveMCPServerHeaders(
		ctx,
		t.claim.Invocation.ID,
		t.definition.ServerName,
	)
	if resolveErr != nil {
		return t.acceptFailure(ctx, *start.Execution, startedAt, "credential_unavailable")
	}
	callContext, cancel := context.WithTimeout(ctx, t.definition.CallTimeout)
	defer cancel()
	result, callErr := t.client.Call(callContext, domain.MCPServerConnection{
		Name:        t.definition.ServerName,
		URL:         t.definition.URL,
		Headers:     headers,
		CallTimeout: t.definition.CallTimeout,
	}, t.definition.RemoteName, raw)
	if callErr != nil {
		return t.acceptFailure(ctx, *start.Execution, startedAt, "call_failed")
	}
	content, normalizeErr := normalizeMCPToolResult(result)
	if normalizeErr != nil {
		return t.acceptFailure(ctx, *start.Execution, startedAt, "invalid_result")
	}
	if _, err := t.coordinator.AcceptMCPToolResult(
		ctx,
		t.claim,
		*start.Execution,
		content,
		result.IsError,
	); err != nil {
		return nil, err
	}
	t.logOutcome(start.Execution, startedAt, len(content), result.IsError, "accepted")
	if result.IsError {
		return dive.NewToolResultError(jsonToolResultText(content)), nil
	}
	return dive.NewToolResultText(jsonToolResultText(content)), nil
}

func (t *mcpTool) acceptFailure(
	ctx context.Context,
	execution domain.ToolCallExecution,
	startedAt time.Time,
	code string,
) (*dive.ToolResult, error) {
	content := json.RawMessage(`"The remote MCP tool call failed."`)
	if _, err := t.coordinator.AcceptMCPToolResult(
		ctx,
		t.claim,
		execution,
		content,
		true,
	); err != nil {
		return nil, err
	}
	t.logOutcome(&execution, startedAt, len(content), true, code)
	return dive.NewToolResultError(jsonToolResultText(content)), nil
}

func (t *mcpTool) logOutcome(
	execution *domain.ToolCallExecution,
	startedAt time.Time,
	resultBytes int,
	isError bool,
	code string,
) {
	if t.logger == nil || execution == nil {
		return
	}
	t.logger.Info(
		"Remote MCP ToolCall settled",
		"invocation_id", t.claim.Invocation.ID,
		"server_name", t.definition.ServerName,
		"tool_call_id", execution.Call.ID,
		"tool_attempt", execution.Attempt.Attempt,
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"result_bytes", resultBytes,
		"is_error", isError,
		"outcome_code", code,
	)
}

func normalizeMCPToolResult(result domain.MCPCallResult) (json.RawMessage, error) {
	content, err := decodeBoundedMCPJSON(result.Content)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"content": content}
	if len(result.StructuredContent) != 0 {
		structured, err := decodeBoundedMCPJSON(result.StructuredContent)
		if err != nil {
			return nil, err
		}
		payload["structured_content"] = structured
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > maxMCPToolResultBytes || mcpJSONDepth(payload) > maxMCPToolResultDepth {
		return nil, fmt.Errorf("MCP result exceeds bounds")
	}
	return encoded, nil
}

func decodeBoundedMCPJSON(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || len(raw) > maxMCPToolResultBytes {
		return nil, fmt.Errorf("MCP result is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("MCP result is invalid")
	}
	if decoder.Decode(&struct{}{}) == nil {
		return nil, fmt.Errorf("MCP result contains trailing data")
	}
	if mcpJSONDepth(value) > maxMCPToolResultDepth {
		return nil, fmt.Errorf("MCP result exceeds depth")
	}
	return value, nil
}

func mcpJSONDepth(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		maximum := 1
		for _, child := range typed {
			maximum = max(maximum, 1+mcpJSONDepth(child))
		}
		return maximum
	case []any:
		maximum := 1
		for _, child := range typed {
			maximum = max(maximum, 1+mcpJSONDepth(child))
		}
		return maximum
	default:
		return 0
	}
}

func safeMCPRetry(annotations domain.MCPToolAnnotations) bool {
	destructive := positiveMCPAnnotation(annotations.DestructiveHint)
	return !destructive &&
		(positiveMCPAnnotation(annotations.ReadOnlyHint) ||
			positiveMCPAnnotation(annotations.IdempotentHint))
}

func positiveMCPAnnotation(value *bool) bool {
	return value != nil && *value
}

func jsonToolResultText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	return string(content)
}
