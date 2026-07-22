package nvoken

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/deepnoodle-ai/nvoken/sdk/go/generated"
)

type Invocation = generated.Invocation
type InvocationResult = generated.InvocationResult
type InvocationStatus = generated.InvocationStatus
type Session = generated.Session
type SessionMessage = generated.SessionMessage
type PendingClientToolCall = generated.PendingClientToolCall
type ToolResultResponse = generated.SubmitClientToolResultsResponse
type ModelProvider = generated.ModelProvider
type ModelPricingCapability = generated.ModelPricingCapability
type ProviderCredential = generated.ProviderCredential
type ProviderCredentialList = generated.ProviderCredentialList
type ProviderCredentialScope = generated.ProviderCredentialScope
type ProviderCredentialStatus = generated.ProviderCredentialStatus

const (
	InvocationQueued                = generated.InvocationStatusQueued
	InvocationRunning               = generated.InvocationStatusRunning
	InvocationWaiting               = generated.InvocationStatusWaiting
	InvocationCompleted             = generated.InvocationStatusCompleted
	InvocationFailed                = generated.InvocationStatusFailed
	InvocationCancelled             = generated.InvocationStatusCancelled
	ModelProviderAnthropic          = generated.Anthropic
	ModelProviderOpenAI             = generated.Openai
	ProviderCredentialScopeAccount  = generated.Account
	ProviderCredentialScopeTenant   = generated.Tenant
	ProviderCredentialStatusActive  = generated.ProviderCredentialStatusActive
	ProviderCredentialStatusRevoked = generated.ProviderCredentialStatusRevoked
)

type Model struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

type Budgets struct {
	WallClockTimeoutSeconds       *int     `json:"wall_clock_timeout_seconds,omitempty"`
	ActiveExecutionTimeoutSeconds *int     `json:"active_execution_timeout_seconds,omitempty"`
	MaxOutputTokens               *int     `json:"max_output_tokens,omitempty"`
	MaxEstimatedCostUSD           *float32 `json:"max_estimated_cost_usd,omitempty"`
	MaxIterations                 *int     `json:"max_iterations,omitempty"`
}

type Tool struct {
	Mode        string          `json:"mode"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema map[string]any  `json:"input_schema"`
	Callback    *CallbackTarget `json:"callback,omitempty"`
}

type CallbackTarget struct {
	URL string `json:"url"`
}

type ExecutionSpec struct {
	Instructions string         `json:"instructions"`
	Model        Model          `json:"model"`
	Budgets      *Budgets       `json:"budgets,omitempty"`
	Tools        []Tool         `json:"tools,omitempty"`
	OutputSchema map[string]any `json:"-"`
}

type InvokeRequest struct {
	AgentRef       string
	TenantRef      *string
	SessionID      *string
	SessionKey     *string
	IdempotencyKey string
	Input          string
	Spec           ExecutionSpec
}

type ListProviderCredentialsOptions struct {
	Provider  *ModelProvider
	Scope     *ProviderCredentialScope
	Status    *ProviderCredentialStatus
	TenantRef *string
	Limit     *int
}

type CreateProviderCredentialInput struct {
	Provider       ModelProvider
	Scope          ProviderCredentialScope
	TenantRef      *string
	APIKey         string
	ExpiresAt      *time.Time
	IdempotencyKey string
}

type RotateProviderCredentialInput struct {
	APIKey         string
	ExpiresAt      *time.Time
	OverlapSeconds *int
	IdempotencyKey string
}

type ToolResult struct {
	ToolCallID string
	Content    any
	IsError    bool
}

type ListInvocationsOptions struct {
	TenantRef     *string
	DefaultTenant *bool
	SessionID     *string
	AgentID       *string
	Status        *InvocationStatus
	Cursor        *string
	Limit         *int
}

type ListSessionsOptions struct {
	TenantRef     *string
	DefaultTenant *bool
	AgentID       *string
	Cursor        *string
	Limit         *int
}

type MessageListOptions struct {
	Cursor *string
	Limit  *int
}

type TranscriptOptions struct {
	Cursor    *string
	PageToken *string
	Limit     *int
}

type WaitOptions struct {
	MinimumDelay time.Duration
	MaximumDelay time.Duration
}

func (o WaitOptions) normalized() WaitOptions {
	if o.MinimumDelay <= 0 {
		o.MinimumDelay = 100 * time.Millisecond
	}
	if o.MaximumDelay <= 0 {
		o.MaximumDelay = 2 * time.Second
	}
	if o.MaximumDelay < o.MinimumDelay {
		o.MaximumDelay = o.MinimumDelay
	}
	return o
}

func (r InvokeRequest) generated() (generated.CreateInvocationRequest, error) {
	if r.AgentRef == "" || r.IdempotencyKey == "" {
		return generated.CreateInvocationRequest{}, fmt.Errorf("agent reference and idempotency key are required")
	}
	if r.Input == "" {
		return generated.CreateInvocationRequest{}, fmt.Errorf("input is required")
	}
	spec := map[string]any{
		"instructions": r.Spec.Instructions,
		"model":        r.Spec.Model,
	}
	if r.Spec.Budgets != nil {
		spec["budgets"] = r.Spec.Budgets
	}
	if len(r.Spec.Tools) > 0 {
		spec["tools"] = r.Spec.Tools
	}
	if r.Spec.OutputSchema != nil {
		spec["output"] = map[string]any{"schema": r.Spec.OutputSchema}
	}
	wire := map[string]any{
		"agent_ref":       r.AgentRef,
		"idempotency_key": r.IdempotencyKey,
		"input": map[string]any{
			"content": []map[string]any{{"type": "text", "text": r.Input}},
		},
		"spec": spec,
	}
	if r.TenantRef != nil {
		wire["tenant_ref"] = *r.TenantRef
	}
	if r.SessionID != nil {
		wire["session_id"] = *r.SessionID
	}
	if r.SessionKey != nil {
		wire["session_key"] = *r.SessionKey
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return generated.CreateInvocationRequest{}, fmt.Errorf("encode invocation: %w", err)
	}
	var request generated.CreateInvocationRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return generated.CreateInvocationRequest{}, fmt.Errorf("convert invocation to generated transport: %w", err)
	}
	return request, nil
}

func generatedToolResults(results []ToolResult) (generated.SubmitClientToolResultsRequest, error) {
	wire := struct {
		Results []map[string]any `json:"results"`
	}{Results: make([]map[string]any, 0, len(results))}
	for _, result := range results {
		item := map[string]any{
			"tool_call_id": result.ToolCallID,
			"content":      result.Content,
		}
		if result.IsError {
			item["is_error"] = true
		}
		wire.Results = append(wire.Results, item)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return generated.SubmitClientToolResultsRequest{}, fmt.Errorf("encode tool results: %w", err)
	}
	var request generated.SubmitClientToolResultsRequest
	if err := json.Unmarshal(encoded, &request); err != nil {
		return generated.SubmitClientToolResultsRequest{}, fmt.Errorf("convert tool results to generated transport: %w", err)
	}
	return request, nil
}

func terminal(status InvocationStatus) bool {
	return status == InvocationCompleted || status == InvocationFailed || status == InvocationCancelled
}
